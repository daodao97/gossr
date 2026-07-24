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
	"net/http/pprof"
	"net/url"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/daodao97/gossr/locales"
	"github.com/daodao97/gossr/renderer"
	"github.com/daodao97/gossr/renderer/engine/gojs"

	"github.com/gin-gonic/gin"
)

type FrontendBuild struct {
	FrontendDist fs.FS
	ServerDist   fs.FS
	// PageResolver is the preferred direct page-data boundary. When set, the
	// same resolver is used for document and browser navigation requests.
	PageResolver PageResolver
	// ExcludedPathPrefixes are host-owned route namespaces that must never be
	// treated as frontend documents (for example /api and /admin).
	ExcludedPathPrefixes []string
	pageAdmission        *pageAdmission
	// RendererFactory 可注入外部 SSR 引擎；为空时使用内置 gojs。
	RendererFactory renderer.Factory
	// SessionResolver 由宿主应用负责认证并返回可安全注入前端的 session 数据。
	// 为空时 gossr 不读取或注入任何 session。
	SessionResolver SessionResolver
	// SiteOrigin 固定注入前端的规范站点 origin；生产环境建议设置，避免依赖 Host 推断。
	SiteOrigin string
	// ShutdownContext 结束时关闭实现 renderer.Closer 的渲染器。
	ShutdownContext context.Context
}

type BackendDataFetcher func(context.Context, *http.Request) (SSRPayload, error)

// SessionResolver 从请求中解析 session。身份验证、过期和撤销校验均由宿主应用负责。
// 无 session 时返回 nil, nil；返回的数据会直接注入 payload.session，不应包含原始凭证。
type SessionResolver func(context.Context, *http.Request) (map[string]any, error)

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

func RunBlocking(router *gin.Engine, frontendBuild FrontendBuild, fetcher BackendDataFetcher) {
	if err := runBlocking(router, frontendBuild, fetcher); err != nil {
		panic(err)
	}
}

func runBlocking(router *gin.Engine, frontendBuild FrontendBuild, fetcher BackendDataFetcher) error {
	if router == nil {
		return errors.New("gin engine is nil")
	}
	devMode := isDevMode()
	if frontendBuild.ShutdownContext != nil && frontendBuild.ShutdownContext.Err() != nil {
		return fmt.Errorf("shutdown context is already done: %w", frontendBuild.ShutdownContext.Err())
	}
	siteOrigin, err := normalizeSiteOrigin(frontendBuild.SiteOrigin)
	if err != nil {
		return err
	}

	var (
		indexHTML string
		ssr       renderer.Renderer
		proxy     *httputil.ReverseProxy
		renderSem chan struct{}
		admission *pageAdmission
	)
	if frontendBuild.PageResolver != nil {
		admission = frontendBuild.pageAdmission
		if admission == nil {
			admission = newPageAdmission(renderConcurrencyLimit(), defaultPageRequestTimeout)
		}
	}

	if devMode {
		var err error
		proxy, err = buildDevProxy(devServerURL())
		if err != nil {
			return err
		}
		registerPprof(router)
		log.Printf("Development mode enabled. Proxying to %s", devServerURL())
		router.NoRoute(func(c *gin.Context) {
			if frontendBuild.PageResolver != nil &&
				!shouldHandleDocument(c.Request, frontendBuild.ExcludedPathPrefixes) {
				c.Status(http.StatusNotFound)
				return
			}

			if isSSRDataPath(c.Request.URL.Path) {
				c.Status(http.StatusNotFound)
				return
			}

			proxy.ServeHTTP(c.Writer, c.Request)
		})
	} else {
		indexBytes, err := readFSFile(frontendBuild.FrontendDist, "index.html")
		if err != nil {
			return fmt.Errorf("failed to read index.html: %w", err)
		}
		indexHTML = string(indexBytes)

		serverEntry, err := readFSFile(frontendBuild.ServerDist, "server.js")
		if err != nil {
			return fmt.Errorf("failed to read server.js: %w", err)
		}
		factory := frontendBuild.RendererFactory
		if factory == nil {
			factory = func(scriptContents string) renderer.Renderer {
				return gojs.NewRenderer(scriptContents)
			}
		}
		ssr, err = createRenderer(factory, string(serverEntry))
		if err != nil {
			return err
		}
		if frontendBuild.ShutdownContext != nil {
			context.AfterFunc(frontendBuild.ShutdownContext, func() {
				if err := renderer.Close(ssr); err != nil {
					log.Printf("close SSR renderer failed: %v", err)
				}
			})
		}

		if frontendBuild.PageResolver == nil {
			renderLimit := renderConcurrencyLimit()
			if renderLimit > 0 {
				renderSem = make(chan struct{}, renderLimit)
			}
		}

		assetsFS, err := fs.Sub(frontendBuild.FrontendDist, "assets")
		if err != nil {
			return fmt.Errorf("failed to prepare assets filesystem: %w", err)
		}
		registerPprof(router)

		// /assets 目录使用长期缓存（文件名带 hash）
		router.Group("/assets", cacheControlMiddleware(cacheImmutableAsset)).
			StaticFS("/", http.FS(assetsFS))

		// 根目录静态文件使用短期缓存
		registerRootStaticFiles(router, frontendBuild.FrontendDist)

		router.NoRoute(func(c *gin.Context) {
			if frontendBuild.PageResolver != nil {
				handlePageDocument(
					c,
					indexHTML,
					ssr,
					admission,
					frontendBuild.PageResolver,
					frontendBuild.ExcludedPathPrefixes,
					siteOrigin,
				)
				return
			}

			if isSSRDataPath(c.Request.URL.Path) {
				c.Status(http.StatusNotFound)
				return
			}
			if isStaticAssetLikePath(c.Request.URL.Path) {
				c.Status(http.StatusNotFound)
				return
			}

			var (
				payload    SSRPayload
				payloadMap map[string]any
				err        error
			)

			if fetcher != nil {
				payload, err = fetcher(c.Request.Context(), c.Request)
				if err != nil {
					log.Printf("fetch SSR data failed path=%q err=%q", c.Request.URL.Path, err)
					c.Status(http.StatusInternalServerError)
					return
				}
			}

			payloadMap, err = enrichPayloadFromRequestWithSiteOrigin(payloadToMap(payload), c.Request, frontendBuild.SessionResolver, siteOrigin)
			if err != nil {
				log.Printf("resolve session failed path=%q", c.Request.URL.Path)
				c.Status(http.StatusInternalServerError)
				return
			}

			locale := localeFromPath(c.Request.URL.Path)

			reqID := fmt.Sprintf("%d", time.Now().UnixNano())

			result, err := renderWithTimeout(c.Request.Context(), ssr, requestRenderURL(c.Request), payloadMap, 3*time.Second, renderSem)
			if err != nil {
				log.Printf("ssr render failed id=%s path=%q err=%q", reqID, c.Request.URL.Path, err)

				fallback := buildFallbackPage(indexHTML, payloadMap, locale, reqID)
				setHTMLNoCacheHeaders(c)
				c.Header("Content-Type", "text/html")
				c.String(http.StatusOK, fallback)
				return
			}

			page, replaceErr := replaceAppHTMLMarker(indexHTML, result.HTML)
			if replaceErr != nil {
				log.Printf("replace SSR app marker failed id=%s path=%q err=%q", reqID, c.Request.URL.Path, replaceErr)
				page = buildFallbackPage(indexHTML, nil, locale, reqID)
				setHTMLNoCacheHeaders(c)
				c.Header("Content-Type", "text/html")
				c.String(http.StatusInternalServerError, page)
				return
			}
			if locale != "" {
				page = applyHTMLLang(page, locale)
			}
			page = injectHeadContent(page, result.Head)
			page, injectErr := injectSSRData(page, payloadMap)
			if injectErr != nil {
				log.Printf("inject SSR data failed id=%s path=%q err=%q", reqID, c.Request.URL.Path, injectErr)
				page = buildFallbackPage(indexHTML, nil, locale, reqID)
			}

			setHTMLNoCacheHeaders(c)
			c.Header("Content-Type", "text/html")
			c.String(http.StatusOK, page)
		})
	}
	return nil
}

func handlePageDocument(
	c *gin.Context,
	indexHTML string,
	ssr renderer.Renderer,
	admission *pageAdmission,
	resolver PageResolver,
	excludedPrefixes []string,
	configuredSiteOrigin string,
) {
	handlePageDocumentWithRenderTimeout(
		c,
		indexHTML,
		ssr,
		admission,
		resolver,
		excludedPrefixes,
		configuredSiteOrigin,
		defaultPageRequestTimeout,
	)
}

func handlePageDocumentWithRenderTimeout(
	c *gin.Context,
	indexHTML string,
	ssr renderer.Renderer,
	admission *pageAdmission,
	resolver PageResolver,
	excludedPrefixes []string,
	configuredSiteOrigin string,
	renderTimeout time.Duration,
) {
	if !shouldHandleDocument(c.Request, excludedPrefixes) {
		c.Status(http.StatusNotFound)
		return
	}

	requestContext, release, err := admission.enter(c.Request.Context())
	if err != nil {
		setHTMLNoCacheHeaders(c)
		c.Status(pageRequestErrorStatus(err))
		return
	}
	defer release()
	c.Request = c.Request.WithContext(requestContext)

	pageRequest, err := newDocumentPageRequest(c.Request)
	if err != nil {
		setHTMLNoCacheHeaders(c)
		c.Status(http.StatusBadRequest)
		return
	}
	pageRequest.SiteOrigin = configuredSiteOrigin
	if pageRequest.SiteOrigin == "" {
		pageRequest.SiteOrigin = requestOrigin(c.Request)
	}

	resolved, payload, err := resolvePage(c.Request.Context(), resolver, pageRequest)
	if err != nil {
		log.Printf("resolve page failed path=%q err=%q", c.Request.URL.Path, err)
		setHTMLNoCacheHeaders(c)
		c.Status(pageRequestErrorStatus(err))
		return
	}
	writePageCookies(c, resolved.Cookies)
	setPageCacheHeaders(c, resolved.Cache)

	if resolved.Redirect != nil {
		writePageRedirect(c, *resolved.Redirect)
		return
	}

	locale := explicitLocaleFromPath(pageRequest.URL.Path)
	reqID := fmt.Sprintf("%d", time.Now().UnixNano())
	rendered, renderErr := renderWithTimeout(
		c.Request.Context(),
		ssr,
		targetRequestURI(pageRequest),
		payload,
		renderTimeout,
		nil,
	)
	if renderErr != nil {
		log.Printf("ssr render failed id=%s path=%q err=%q", reqID, c.Request.URL.Path, renderErr)
		fallback := buildPageFallback(indexHTML, payload, locale, reqID)
		writeHTMLDocument(c, resolved.Status, fallback)
		return
	}

	status, redirect, err := mergeRenderOutcome(resolved, rendered)
	if err != nil {
		log.Printf("invalid ssr render outcome id=%s path=%q err=%q", reqID, c.Request.URL.Path, err)
		fallback := buildPageFallback(indexHTML, payload, locale, reqID)
		writeHTMLDocument(c, http.StatusInternalServerError, fallback)
		return
	}
	if redirect != nil {
		writePageRedirect(c, *redirect)
		return
	}

	page, err := markSSRApp(indexHTML)
	if err != nil {
		log.Printf("mark SSR app failed id=%s path=%q err=%q", reqID, c.Request.URL.Path, err)
		page = buildPageFallback(indexHTML, nil, locale, reqID)
		writeHTMLDocument(c, http.StatusInternalServerError, page)
		return
	}
	page, err = replaceAppHTMLMarker(page, rendered.HTML)
	if err != nil {
		log.Printf("replace SSR app marker failed id=%s path=%q err=%q", reqID, c.Request.URL.Path, err)
		page = buildPageFallback(indexHTML, nil, locale, reqID)
		writeHTMLDocument(c, http.StatusInternalServerError, page)
		return
	}
	if locale != "" {
		page = applyHTMLLang(page, locale)
	}
	page = injectHeadContent(page, rendered.Head)
	page, err = injectSSRBootData(page, payload)
	if err != nil {
		log.Printf("inject SSR boot data failed id=%s path=%q err=%q", reqID, c.Request.URL.Path, err)
		page = buildPageFallback(indexHTML, nil, locale, reqID)
		status = http.StatusInternalServerError
	}
	writeHTMLDocument(c, status, page)
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

func mergeRenderOutcome(page PageResult, rendered renderer.Result) (int, *Redirect, error) {
	if rendered.Redirect != nil {
		return 0, nil, errors.New("typed renderer must not return a redirect")
	}
	if rendered.Status != 0 && rendered.Status != page.Status {
		return 0, nil, fmt.Errorf(
			"renderer status %d conflicts with resolver status %d",
			rendered.Status,
			page.Status,
		)
	}
	return page.Status, nil, nil
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

func injectSSRBootData(page string, payload map[string]any) (string, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return page, err
	}

	const bootID = "__GOSSR_BOOT__"
	hasBootElement, err := documentHasElementID(page, bootID)
	if err != nil {
		return page, fmt.Errorf("inspect SSR document: %w", err)
	}
	if hasBootElement {
		return page, fmt.Errorf("document already contains %s", bootID)
	}
	script := `<script id="` + bootID + `" type="application/json">` + string(jsonData) + `</script>`
	withBoot, found, err := insertBeforeHTMLElementEnd(page, "head", script)
	if err != nil {
		return page, err
	}
	if found {
		return withBoot, nil
	}
	withBoot, found, err = insertBeforeHTMLElementEnd(page, "body", script)
	if err != nil {
		return page, err
	}
	if found {
		return withBoot, nil
	}
	return page + script, nil
}

func buildPageFallback(indexHTML string, payload map[string]any, locale string, reqID string) string {
	page, err := replaceAppHTMLMarker(indexHTML, "")
	if err != nil {
		page = indexHTML
	}
	if locale != "" {
		page = applyHTMLLang(page, locale)
	}
	if strings.TrimSpace(reqID) != "" {
		page = injectHeadContent(
			page,
			fmt.Sprintf(`<meta name="ssr-error-id" content="%s">`, template.HTMLEscapeString(reqID)),
		)
	}
	if injected, err := injectSSRBootData(page, payload); err == nil {
		return injected
	}
	return page
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

func injectHeadContent(html string, head string) string {
	if strings.TrimSpace(head) == "" {
		return html
	}
	// SSR head 中的 title 对当前路由具有权威性，模板默认 title 必须移除，
	// 否则浏览器和搜索引擎通常会采用第一个旧标题。
	if hasTitle, err := documentHasHTMLElement(head, "title"); err == nil && hasTitle {
		if withoutTitles, removeErr := removeHTMLElementsWithin(
			html,
			"title",
			"head",
		); removeErr == nil {
			html = withoutTitles
		}
	}

	injection := head
	if !strings.HasSuffix(injection, "\n") {
		injection += "\n"
	}

	if injected, found, err := insertBeforeHTMLElementEnd(html, "head", injection); err == nil && found {
		return injected
	}

	return injection + html
}

func injectSSRData(html string, payload map[string]any) (string, error) {
	if len(payload) == 0 {
		return html, nil
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return html, err
	}

	escaped := template.JSEscapeString(string(jsonData))
	script := fmt.Sprintf(`<script id="ssr-data">window.__SSR_DATA__=JSON.parse("%s")</script>`, escaped)

	injected, found, err := insertBeforeHTMLElementEnd(html, "head", script)
	if err != nil {
		return html, err
	}
	if found {
		return injected, nil
	}
	injected, found, err = insertBeforeHTMLElementEnd(html, "body", script)
	if err != nil {
		return html, err
	}
	if found {
		return injected, nil
	}

	return html + script, nil
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

func enrichPayloadForSSRFetchResponse(payload map[string]any, req *http.Request) map[string]any {
	return enrichPayloadWithRequestContext(payload, req)
}

func enrichPayloadFromRequest(payload map[string]any, req *http.Request, resolver SessionResolver) (map[string]any, error) {
	return enrichPayloadFromRequestWithSiteOrigin(payload, req, resolver, "")
}

func enrichPayloadFromRequestWithSiteOrigin(payload map[string]any, req *http.Request, resolver SessionResolver, siteOrigin string) (map[string]any, error) {
	enriched := enrichPayloadWithRequestContext(payload, req)
	if siteOrigin != "" {
		enriched["siteOrigin"] = siteOrigin
	}
	// session 是认证边界的保留字段，只能由当前 SSR 实例的 resolver 写入。
	// 这也会清除数据 handler 遗留或伪造的 session，避免匿名请求继承身份。
	delete(enriched, "session")
	if req == nil || resolver == nil {
		return enriched, nil
	}

	session, err := resolver(req.Context(), req)
	if err != nil {
		return nil, err
	}
	if session != nil {
		enriched["session"] = session
	}

	return enriched, nil
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

func requestRenderURL(req *http.Request) string {
	if req == nil || req.URL == nil {
		return "/"
	}

	requestURI := req.URL.RequestURI()
	if requestURI == "" {
		return "/"
	}
	return requestURI
}

func enrichPayloadWithRequestContext(payload map[string]any, req *http.Request) map[string]any {
	enriched := make(map[string]any, len(payload)+3)
	for k, v := range payload {
		enriched[k] = v
	}

	if req == nil {
		return enriched
	}

	if locale := localeFromPath(req.URL.Path); locale != "" {
		enriched["locale"] = locale
	}

	if origin := requestOrigin(req); origin != "" {
		enriched["siteOrigin"] = origin
	}

	return enriched
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

func localeFromPath(p string) string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return locales.Default
	}

	segments := strings.Split(trimmed, "/")
	if len(segments) == 0 {
		return locales.Default
	}

	candidate := segments[0]
	if locales.IsSupported(candidate) {
		return locales.Normalize(candidate)
	}

	return locales.Default
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

func newDevProxy(rawURL string) *httputil.ReverseProxy {
	proxy, err := buildDevProxy(rawURL)
	if err != nil {
		panic(err)
	}
	return proxy
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

func renderWithTimeout(parentCtx context.Context, ssr renderer.Renderer, urlPath string, payload map[string]any, timeout time.Duration, sem chan struct{}) (result renderer.Result, err error) {
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

	if sem != nil {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return renderer.Result{}, fmt.Errorf("render timeout after %s", timeout)
			}
			return renderer.Result{}, ctx.Err()
		}
	}

	result, err = ssr.Render(ctx, urlPath, payload)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return renderer.Result{}, fmt.Errorf("render timeout after %s", timeout)
	}
	return result, err
}

func renderConcurrencyLimit() int {
	defaultLimit := runtime.GOMAXPROCS(0)

	if raw := strings.TrimSpace(os.Getenv("SSR_RENDER_LIMIT")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			log.Printf("config: invalid SSR_RENDER_LIMIT=%q, fallback to %d", raw, defaultLimit)
			return defaultLimit
		}

		if v == 0 {
			log.Printf("config: SSR_RENDER_LIMIT=0 (unlimited)")
			return 0
		}

		if v > maxSSRRenderLimit {
			log.Printf("config: SSR_RENDER_LIMIT=%d exceeds max %d, clamped", v, maxSSRRenderLimit)
			return maxSSRRenderLimit
		}

		return v
	}

	return defaultLimit
}

func registerPprof(router *gin.Engine) {
	if !isPprofEnabled() {
		return
	}
	log.Printf("pprof enabled at /debug/pprof")
	group := router.Group("/debug/pprof")
	group.GET("/", gin.WrapF(pprof.Index))
	group.GET("/cmdline", gin.WrapF(pprof.Cmdline))
	group.GET("/profile", gin.WrapF(pprof.Profile))
	group.POST("/symbol", gin.WrapF(pprof.Symbol))
	group.GET("/symbol", gin.WrapF(pprof.Symbol))
	group.GET("/trace", gin.WrapF(pprof.Trace))
	group.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
	group.GET("/block", gin.WrapH(pprof.Handler("block")))
	group.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
	group.GET("/heap", gin.WrapH(pprof.Handler("heap")))
	group.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
	group.GET("/threadcreate", gin.WrapH(pprof.Handler("threadcreate")))
}

func isPprofEnabled() bool {
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("ENABLE_PPROF"))); raw != "" {
		switch raw {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	}
	return isDevMode()
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

func buildFallbackPage(indexHTML string, payload map[string]any, locale string, reqID string) string {
	page, err := replaceAppHTMLMarker(indexHTML, "")
	if err != nil {
		page = indexHTML
	}
	if locale != "" {
		page = applyHTMLLang(page, locale)
	}

	headMeta := ""
	if strings.TrimSpace(reqID) != "" {
		headMeta = fmt.Sprintf(`<meta name="ssr-error-id" content="%s">`, template.HTMLEscapeString(reqID))
		page = injectHeadContent(page, headMeta)
	}

	if injected, err := injectSSRData(page, payload); err == nil {
		return injected
	}

	return page
}
