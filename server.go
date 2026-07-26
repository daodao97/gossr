package gossr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/daodao97/gossr/locales"
	"github.com/daodao97/gossr/renderer"

	"github.com/gin-gonic/gin"
)

// SSRFetchAuthorizer 由宿主决定是否允许客户端读取 /_ssr/data。
// 返回 allowed=true 时放行；拒绝时 statusCode 应为 4xx，非法状态码按 403 处理。
type SSRFetchAuthorizer func(*http.Request) (statusCode int, allowed bool)

const (
	DefaultSSRDataRoute = "/_ssr/data"
	maxSSRRenderLimit   = 1024
	cacheNoStoreHTML    = "no-cache, no-store, must-revalidate"
	cacheImmutableAsset = "public, max-age=31536000, immutable"
	cacheShortRootFile  = "public, max-age=86400"
)

var staticAssetExts = map[string]struct{}{
	".avif":        {},
	".br":          {},
	".css":         {},
	".eot":         {},
	".gif":         {},
	".gz":          {},
	".ico":         {},
	".jpeg":        {},
	".jpg":         {},
	".js":          {},
	".map":         {},
	".mjs":         {},
	".otf":         {},
	".pdf":         {},
	".png":         {},
	".svg":         {},
	".ttf":         {},
	".txt":         {},
	".webmanifest": {},
	".webp":        {},
	".wasm":        {},
	".woff":        {},
	".woff2":       {},
	".xml":         {},
}

func handlePageDocumentWithRenderTimeout(
	c *gin.Context,
	page *compiledTemplate,
	ssr renderer.Renderer,
	admission *pageAdmission,
	resolver PageResolver,
	configuredSiteOrigin string,
	renderTimeout time.Duration,
	event *PageEvent,
) {
	// reqID 提前生成:所有失败分支下发的兜底壳都带 ssr-error-id,便于关联日志。
	reqID := fmt.Sprintf("%d", time.Now().UnixNano())

	requestContext, release, err := admission.enter(c.Request.Context())
	if err != nil {
		event.Outcome = "timeout"
		setHTMLNoCacheHeaders(c)
		writeHTMLDocument(
			c,
			pageRequestErrorStatus(err),
			page.renderFallback(nil, explicitLocaleFromPath(c.Request.URL.Path), reqID),
		)
		return
	}
	defer release()
	c.Request = c.Request.WithContext(requestContext)

	pageRequest, err := newDocumentPageRequest(c.Request)
	if err != nil {
		event.Outcome = "invalid_target"
		setHTMLNoCacheHeaders(c)
		c.Status(http.StatusBadRequest)
		return
	}
	pageRequest.SiteOrigin = configuredSiteOrigin
	if pageRequest.SiteOrigin == "" {
		pageRequest.SiteOrigin = requestOrigin(c.Request)
	}
	locale := explicitLocaleFromPath(pageRequest.URL.Path)

	resolved, payload, err := resolvePage(c.Request.Context(), resolver, pageRequest)
	if err != nil {
		log.Printf("resolve page failed path=%q err=%q", c.Request.URL.Path, err)
		event.Outcome = "resolver_error"
		if status := pageRequestErrorStatus(err); status == http.StatusGatewayTimeout ||
			status == http.StatusRequestTimeout {
			event.Outcome = "timeout"
		}
		// 下发无 boot 数据的 CSR 壳而不是空 body:客户端启动后会自动拉
		// /_ssr/data——服务端已恢复则页面照常出现,仍失败则落到应用内
		// 错误提示。任何失败路径都不该以白屏收场。
		setHTMLNoCacheHeaders(c)
		writeHTMLDocument(c, pageRequestErrorStatus(err), page.renderFallback(nil, locale, reqID))
		return
	}
	writePageCookies(c, resolved.Cookies)
	setPageCacheHeaders(c, resolved.Cache)

	if resolved.Redirect != nil {
		event.Outcome = "redirect"
		writePageRedirect(c, *resolved.Redirect)
		return
	}

	// HEAD 只需要状态码和响应头(监控探活、链接爬虫常用),渲染出的 body
	// 本来就会被丢弃,跳过整个 SSR 渲染。
	if c.Request.Method == http.MethodHead {
		writeHTMLDocument(c, resolved.Status, "")
		return
	}

	renderStart := time.Now()
	rendered, renderErr := renderWithTimeout(
		c.Request.Context(),
		ssr,
		targetRequestURI(pageRequest),
		payload,
		renderTimeout,
	)
	event.Render = time.Since(renderStart)
	if renderErr != nil {
		log.Printf("ssr render failed id=%s path=%q err=%q", reqID, c.Request.URL.Path, renderErr)
		event.Outcome = "fallback"
		writeHTMLDocument(c, resolved.Status, page.renderFallback(payload, locale, reqID))
		return
	}

	// The template was validated boot-free at startup; only rendered output can
	// smuggle a competing boot element in. A substring check is deliberately
	// conservative: a false positive merely downgrades to the CSR fallback.
	if strings.Contains(rendered.HTML, bootElementID) || strings.Contains(rendered.Head, bootElementID) {
		log.Printf("ssr render output contains %s id=%s path=%q", bootElementID, reqID, c.Request.URL.Path)
		event.Outcome = "fallback"
		writeHTMLDocument(c, http.StatusInternalServerError, page.renderFallback(nil, locale, reqID))
		return
	}
	boot, err := bootScript(payload)
	if err != nil {
		log.Printf("inject SSR boot data failed id=%s path=%q err=%q", reqID, c.Request.URL.Path, err)
		event.Outcome = "fallback"
		writeHTMLDocument(c, http.StatusInternalServerError, page.renderFallback(nil, locale, reqID))
		return
	}
	writeHTMLDocument(c, resolved.Status, page.renderDocument(rendered, boot, locale))
}

func resolvePage(ctx context.Context, resolver PageResolver, request PageRequest) (PageResult, map[string]any, error) {
	if resolver == nil {
		return PageResult{}, nil, errors.New("page resolver is nil")
	}
	result, err := resolver(ctx, request)
	if err != nil {
		return PageResult{}, nil, err
	}
	result, err = normalizePageResult(result)
	if err != nil {
		return PageResult{}, nil, err
	}
	return result, payloadToMap(result.Payload), nil
}

func setPageCacheHeaders(c *gin.Context, policy CachePolicy) {
	switch policy {
	case CachePrivateRevalidate:
		c.Header("Cache-Control", "private, no-cache, must-revalidate")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
	default:
		setHTMLNoCacheHeaders(c)
	}
}

func writePageCookies(c *gin.Context, cookies []*http.Cookie) {
	for _, cookie := range cookies {
		http.SetCookie(c.Writer, cookie)
	}
}

func writePageRedirect(c *gin.Context, redirect Redirect) {
	c.Header("Location", redirect.Location)
	c.Status(redirect.Status)
}

func writeHTMLDocument(c *gin.Context, status int, page string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	if c.Request.Method == http.MethodHead {
		c.Data(status, "text/html; charset=utf-8", nil)
		return
	}
	c.Data(status, "text/html; charset=utf-8", []byte(page))
}

const bootElementID = "__GOSSR_BOOT__"

// bootScript serializes the payload into the boot element consumed by
// bootstrapClient. encoding/json escapes <, > and & by default, so the JSON
// cannot break out of the script element.
func bootScript(payload map[string]any) (string, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return `<script id="` + bootElementID + `" type="application/json">` + string(jsonData) + `</script>`, nil
}

// compiledTemplate is index.html pre-split at its two injection points — the
// </head> insertion point and the app-html marker — so the request path only
// concatenates strings instead of re-tokenizing the document.
type compiledTemplate struct {
	// preHead runs from the start of the document to just before </head>.
	preHead string
	// preHeadNoTitle is preHead with template <title> elements removed; used
	// when the SSR head carries an authoritative <title>.
	preHeadNoTitle string
	// headToMarkerSSR runs from </head> up to the app marker with the app
	// element marked data-ssr; headToMarkerCSR is the untouched fallback twin.
	headToMarkerSSR string
	headToMarkerCSR string
	// postMarker runs from the end of the app marker to the end of the document.
	postMarker string
}

func compileIndexTemplate(indexHTML string) (*compiledTemplate, error) {
	if err := validateIndexTemplate(indexHTML); err != nil {
		return nil, err
	}

	ssrDoc, err := markSSRApp(indexHTML)
	if err != nil {
		return nil, err
	}
	strippedDoc, err := removeHTMLElementsWithin(ssrDoc, "title", "head")
	if err != nil {
		return nil, err
	}

	preHead, headToMarkerSSR, postMarker, err := splitTemplateDocument(ssrDoc)
	if err != nil {
		return nil, err
	}
	preHeadNoTitle, _, _, err := splitTemplateDocument(strippedDoc)
	if err != nil {
		return nil, err
	}
	_, headToMarkerCSR, _, err := splitTemplateDocument(indexHTML)
	if err != nil {
		return nil, err
	}

	return &compiledTemplate{
		preHead:         preHead,
		preHeadNoTitle:  preHeadNoTitle,
		headToMarkerSSR: headToMarkerSSR,
		headToMarkerCSR: headToMarkerCSR,
		postMarker:      postMarker,
	}, nil
}

func splitTemplateDocument(document string) (string, string, string, error) {
	headEnd, found, err := findHTMLElementEnd(document, "head")
	if err != nil {
		return "", "", "", err
	}
	if !found {
		return "", "", "", errors.New("template must contain a closed head element")
	}
	markerStart, markerEnd, err := findAppHTMLMarker(document)
	if err != nil {
		return "", "", "", err
	}
	if headEnd > markerStart {
		return "", "", "", errors.New("template head must close before the app-html marker")
	}
	return document[:headEnd], document[headEnd:markerStart], document[markerEnd:], nil
}

// renderDocument assembles the SSR document: template head, rendered head,
// boot data, then the rendered app HTML in place of the marker.
func (t *compiledTemplate) renderDocument(rendered renderer.Result, boot string, locale string) string {
	pre := t.preHead
	head := rendered.Head
	if strings.TrimSpace(head) == "" {
		head = ""
	} else {
		// SSR head 中的 title 对当前路由具有权威性，模板默认 title 必须移除，
		// 否则浏览器和搜索引擎通常会采用第一个旧标题。
		if hasTitle, err := documentHasHTMLElement(head, "title"); err == nil && hasTitle {
			pre = t.preHeadNoTitle
		}
		if !strings.HasSuffix(head, "\n") {
			head += "\n"
		}
	}
	if locale != "" {
		pre = applyHTMLLang(pre, locale)
	}

	var page strings.Builder
	page.Grow(len(pre) + len(head) + len(boot) + len(t.headToMarkerSSR) + len(rendered.HTML) + len(t.postMarker))
	page.WriteString(pre)
	page.WriteString(head)
	page.WriteString(boot)
	page.WriteString(t.headToMarkerSSR)
	page.WriteString(rendered.HTML)
	page.WriteString(t.postMarker)
	return page.String()
}

// renderFallback assembles the CSR fallback shell: no SSR markup, no data-ssr
// attribute, boot data preserved so the client can still boot from the payload.
func (t *compiledTemplate) renderFallback(payload map[string]any, locale string, reqID string) string {
	pre := t.preHead
	if locale != "" {
		pre = applyHTMLLang(pre, locale)
	}
	meta := ""
	if strings.TrimSpace(reqID) != "" {
		meta = fmt.Sprintf(`<meta name="ssr-error-id" content="%s">`, template.HTMLEscapeString(reqID))
	}
	// payload 为 nil 表示没有可信的页面数据(resolver 失败/注入失败):
	// 不注入 boot,让客户端冷启动并自行拉取导航数据。
	boot := ""
	if payload != nil {
		if script, err := bootScript(payload); err == nil {
			boot = script
		}
	}
	return pre + meta + boot + t.headToMarkerCSR + t.postMarker
}

func isSSRDataPath(rawPath string) bool {
	return rawPath == DefaultSSRDataRoute || strings.HasPrefix(rawPath, DefaultSSRDataRoute+"/")
}

func createRenderer(factory renderer.Factory, scriptContents string) (instance renderer.Renderer, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			instance = nil
			err = fmt.Errorf("create renderer: %v", recovered)
		}
	}()
	instance = factory(scriptContents)
	if instance == nil {
		return nil, errors.New("renderer factory returned nil")
	}
	return instance, nil
}

func applyHTMLLang(html string, locale string) string {
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return html
	}

	if updated, found, err := setHTMLElementAttribute(
		html,
		"html",
		"lang",
		locale,
	); err == nil && found {
		return updated
	}

	return html
}

func payloadToMap(payload SSRPayload) map[string]any {
	if payload == nil {
		return map[string]any{}
	}

	if m := payload.AsMap(); m != nil {
		return m
	}

	return map[string]any{}
}

func normalizeSiteOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid SiteOrigin %q: expected an http(s) origin without path, query, credentials, or fragment", raw)
	}
	if _, ok := canonicalOrigin(parsed.Scheme, parsed.Host); !ok {
		return "", fmt.Errorf("invalid SiteOrigin %q", raw)
	}
	return strings.ToLower(parsed.Scheme) + "://" + parsed.Host, nil
}

func readFSFile(f fs.FS, name string) ([]byte, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	contents, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	return contents, nil
}

func explicitLocaleFromPath(p string) string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return ""
	}
	candidate := strings.Split(trimmed, "/")[0]
	if !locales.IsSupported(candidate) {
		return ""
	}
	return locales.Normalize(candidate)
}

func requestOrigin(r *http.Request) string {
	host := primaryHost(r)
	if host == "" {
		return ""
	}

	scheme := requestScheme(r)

	if trustForwardedHeaders() {
		if forwardedPort := firstForwardedValue(r.Header.Get("X-Forwarded-Port")); forwardedPort != "" && !hostHasExplicitPort(host) {
			port, err := strconv.Atoi(forwardedPort)
			if err != nil || port <= 0 || port > 65535 {
				return ""
			}
			host += ":" + forwardedPort
		}
	}
	if _, ok := canonicalOrigin(scheme, host); !ok {
		return ""
	}

	return fmt.Sprintf("%s://%s", scheme, host)
}

func requestScheme(r *http.Request) string {
	scheme := "http"
	if r != nil && r.TLS != nil {
		scheme = "https"
	}

	if r != nil && trustForwardedHeaders() {
		proto := strings.ToLower(firstForwardedValue(r.Header.Get("X-Forwarded-Proto")))
		if proto == "http" || proto == "https" {
			return proto
		}
		if proto != "" {
			return ""
		}
	}

	return scheme
}

func canonicalOrigin(scheme, host string) (string, bool) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme != "http" && scheme != "https" {
		return "", false
	}

	parsed, err := url.Parse(scheme + "://" + strings.TrimSpace(host))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}

	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "" {
		return "", false
	}
	port := parsed.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber <= 0 || portNumber > 65535 {
		return "", false
	}

	return scheme + "://" + net.JoinHostPort(hostname, port), true
}

func primaryHost(r *http.Request) string {
	if r == nil {
		return ""
	}

	host := strings.TrimSpace(r.Host)
	if trustForwardedHeaders() {
		if forwardedHost := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
			host = forwardedHost
		}
	}

	return host
}

func firstForwardedValue(raw string) string {
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func hostHasExplicitPort(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}

	if strings.HasPrefix(host, "[") {
		return strings.Contains(host, "]:")
	}

	return strings.Count(host, ":") == 1
}

func isDevMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DEV_MODE"))) {
	case "1", "true", "yes", "on", "dev":
		return true
	default:
		return false
	}
}

func trustForwardedHeaders() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TRUST_FORWARDED_HEADERS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func devServerURL() string {
	if raw := strings.TrimSpace(os.Getenv("DEV_SERVER_URL")); raw != "" {
		return raw
	}

	return "http://127.0.0.1:3333"
}

func buildDevProxy(rawURL string) (*httputil.ReverseProxy, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		if err == nil {
			err = fmt.Errorf("URL must use http or https and include a host")
		}
		return nil, fmt.Errorf("invalid DEV_SERVER_URL %q: %w", rawURL, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(parsed)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalHost := req.Host
		originalScheme := "http"
		if req.TLS != nil {
			originalScheme = "https"
		}
		director(req)
		req.Host = parsed.Host
		if req.Header.Get("X-Forwarded-Host") == "" && originalHost != "" {
			req.Header.Set("X-Forwarded-Host", originalHost)
		}
		if req.Header.Get("X-Forwarded-Proto") == "" {
			req.Header.Set("X-Forwarded-Proto", originalScheme)
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("dev proxy error: %q", err)
		http.Error(w, "dev server unavailable", http.StatusBadGateway)
	}

	return proxy, nil
}

func renderWithTimeout(parentCtx context.Context, ssr renderer.Renderer, urlPath string, payload map[string]any, timeout time.Duration) (result renderer.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = renderer.Result{}
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	if parentCtx == nil {
		parentCtx = context.Background()
	}

	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	if deadline, ok := parentCtx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return renderer.Result{}, context.DeadlineExceeded
		}
		if remaining < timeout {
			timeout = remaining
		}
	}

	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	result, err = ssr.Render(ctx, urlPath, payload)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return renderer.Result{}, fmt.Errorf("render timeout after %s", timeout)
	}
	return result, err
}

func registerRootStaticFiles(router *gin.Engine, frontendDist fs.FS) {
	entries, err := fs.ReadDir(frontendDist, ".")
	if err != nil {
		log.Printf("failed to read frontend dist root: %v", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "index.html" {
			continue
		}
		// 根目录文件（favicon, logo 等）使用短期缓存
		router.GET("/"+name, func(fileName string) gin.HandlerFunc {
			return func(c *gin.Context) {
				c.Header("Cache-Control", cacheShortRootFile)
				c.FileFromFS(fileName, http.FS(frontendDist))
			}
		}(name))
	}
}

func isStaticAssetLikePath(rawPath string) bool {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" || trimmed == "/" {
		return false
	}

	base := path.Base(strings.TrimRight(trimmed, "/"))
	if base == "" || base == "." || base == "/" {
		return false
	}

	ext := strings.ToLower(path.Ext(base))
	if ext == "" {
		return false
	}

	_, ok := staticAssetExts[ext]
	return ok
}

func cacheControlMiddleware(value string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", value)
		c.Next()
	}
}

func setHTMLNoCacheHeaders(c *gin.Context) {
	c.Header("Cache-Control", cacheNoStoreHTML)
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}
