package gossr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/http/pprof"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/daodao97/gossr/locales"
	"github.com/daodao97/gossr/renderer"

	"github.com/gin-gonic/gin"
)

type FrontendBuild struct {
	FrontendDist fs.FS
	ServerDist   fs.FS
}

type BackendDataFetcher func(context.Context, *http.Request) (SSRPayload, error)

const DefaultSSRFetchPrefix = "/__ssr_fetch"

var langAttributePattern = regexp.MustCompile(`lang="[^"]*"`)

func RunBlocking(router *gin.Engine, frontendBuild FrontendBuild, fetcher BackendDataFetcher) {
	devMode := isDevMode()
	registerPprof(router)
	router.GET("/i/:invite_code", func(c *gin.Context) {
		inviteCode := strings.TrimSpace(c.Param("invite_code"))
		if inviteCode != "" {
			c.SetCookie("invite_code", inviteCode, 60*60*24*30, "/", "", false, true)
		}
		c.Redirect(http.StatusFound, "/")
	})

	var (
		indexHTML string
		ssr       renderer.Renderer
		proxy     *httputil.ReverseProxy
		renderSem chan struct{}
	)

	if devMode {
		proxy = newDevProxy(devServerURL())
		log.Printf("Development mode enabled. Proxying to %s", devServerURL())
		router.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, DefaultSSRFetchPrefix) {
				c.Status(http.StatusNotFound)
				return
			}

			proxy.ServeHTTP(c.Writer, c.Request)
		})
	} else {
		indexBytes, err := readFSFile(frontendBuild.FrontendDist, "index.html")
		if err != nil {
			log.Fatalf("failed to read index.html: %v", err)
		}
		indexHTML = string(indexBytes)

		serverEntry, err := readFSFile(frontendBuild.ServerDist, "server.js")
		if err != nil {
			log.Fatalf("failed to read server.js: %v", err)
		}
		ssr = newRendererFromEnv(string(serverEntry))
		prewarmRenderer(ssr)

		renderLimit := renderConcurrencyLimit()
		if renderLimit > 0 {
			renderSem = make(chan struct{}, renderLimit)
		}

		assetsFS, err := fs.Sub(frontendBuild.FrontendDist, "assets")
		if err != nil {
			log.Fatalf("failed to prepare assets filesystem: %v", err)
		}

		// /assets 目录使用长期缓存（文件名带 hash）
		router.Group("/assets", cacheControlMiddleware("public, max-age=31536000, immutable")).
			StaticFS("/", http.FS(assetsFS))

		// 根目录静态文件使用短期缓存
		registerRootStaticFiles(router, frontendBuild.FrontendDist)

		router.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, DefaultSSRFetchPrefix) {
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
					log.Println(err)
					c.Status(http.StatusInternalServerError)
					return
				}
			}

			payloadMap = payloadToMap(payload)
			if session := sessionStateFromRequest(c.Request); session != nil {
				payloadMap["session"] = session
			}

			locale := localeFromPath(c.Request.URL.Path)
			if locale != "" {
				payloadMap["locale"] = locale
			}

			if origin := requestOrigin(c.Request); origin != "" {
				payloadMap["siteOrigin"] = origin
			}

			reqID := fmt.Sprintf("%d", time.Now().UnixNano())

			result, err := renderWithTimeout(ssr, c.Request.URL.Path, payloadMap, 3*time.Second, renderSem)
			if err != nil {
				log.Printf("ssr render failed id=%s path=%s err=%v", reqID, c.Request.URL.Path, err)

				fallback := buildFallbackPage(indexHTML, payloadMap, locale, reqID)
				c.Header("Content-Type", "text/html")
				c.String(http.StatusOK, fallback)
				return
			}

			page := strings.Replace(indexHTML, "<!--app-html-->", result.HTML, 1)
			if locale != "" {
				page = applyHTMLLang(page, locale)
			}
			page = injectHeadContent(page, result.Head)
			page, injectErr := injectSSRData(page, payloadMap)
			if injectErr != nil {
				log.Println(injectErr)
			}

			c.Header("Content-Type", "text/html")
			c.String(http.StatusOK, page)
		})
	}
}

func applyHTMLLang(html string, locale string) string {
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return html
	}

	replacement := fmt.Sprintf(`lang="%s"`, locale)
	if langAttributePattern.MatchString(html) {
		return langAttributePattern.ReplaceAllString(html, replacement)
	}

	if strings.Contains(html, "<html") {
		return strings.Replace(html, "<html", "<html "+replacement, 1)
	}

	return html
}

func injectHeadContent(html string, head string) string {
	if strings.TrimSpace(head) == "" {
		return html
	}

	injection := head
	if !strings.HasSuffix(injection, "\n") {
		injection += "\n"
	}

	if strings.Contains(html, "</head>") {
		return strings.Replace(html, "</head>", injection+"</head>", 1)
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

	if strings.Contains(html, "</head>") {
		return strings.Replace(html, "</head>", script+"</head>", 1), nil
	}

	if strings.Contains(html, "</body>") {
		return strings.Replace(html, "</body>", script+"</body>", 1), nil
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

type ssrSessionPayload struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Provider string `json:"provider"`
	IssuedAt int64  `json:"iat"`
}

func sessionStateFromRequest(r *http.Request) map[string]any {
	cookie, err := r.Cookie("session_token")
	if err != nil || cookie.Value == "" {
		return nil
	}

	decoded, err := base64.StdEncoding.DecodeString(cookie.Value)
	if err != nil {
		return nil
	}

	var payload ssrSessionPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return nil
	}

	if payload.Email == "" {
		return nil
	}

	return map[string]any{
		"session_token": cookie.Value,
		"user": map[string]any{
			"id":       payload.ID,
			"name":     payload.Name,
			"email":    payload.Email,
			"provider": payload.Provider,
		},
	}
}

func readFSFile(f fs.FS, name string) ([]byte, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}

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

func requestOrigin(r *http.Request) string {
	host := r.Host
	if host == "" {
		return ""
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		parts := strings.Split(proto, ",")
		if len(parts) > 0 {
			scheme = strings.TrimSpace(parts[0])
		}
	}

	return fmt.Sprintf("%s://%s", scheme, host)
}

func isDevMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DEV_MODE"))) {
	case "1", "true", "yes", "on", "dev":
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
	parsed, err := url.Parse(rawURL)
	if err != nil {
		log.Fatalf("invalid DEV_SERVER_URL %q: %v", rawURL, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(parsed)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("dev proxy error: %v", err)
		http.Error(w, "dev server unavailable", http.StatusBadGateway)
	}

	return proxy
}

func renderWithTimeout(ssr renderer.Renderer, urlPath string, payload map[string]any, timeout time.Duration, sem chan struct{}) (renderer.Result, error) {
	type renderResult struct {
		result renderer.Result
		err    error
	}

	ch := make(chan renderResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- renderResult{err: fmt.Errorf("panic: %v", r)}
			}
		}()
		if sem != nil {
			sem <- struct{}{}
			defer func() { <-sem }()
		}
		res, err := ssr.Render(urlPath, payload)
		ch <- renderResult{result: res, err: err}
	}()

	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	select {
	case r := <-ch:
		return r.result, r.err
	case <-time.After(timeout):
		return renderer.Result{}, fmt.Errorf("render timeout after %s", timeout)
	}
}

func renderConcurrencyLimit() int {
	if raw := strings.TrimSpace(os.Getenv("SSR_RENDER_LIMIT")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			return v
		}
	}
	return runtime.GOMAXPROCS(0)
}

func prewarmRenderer(ssr renderer.Renderer) {
	go func() {
		_, _ = ssr.Render("/", nil)
	}()
}

// newRendererFromEnv 在 ssr_v8.go 和 ssr_nov8.go 中定义

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
				c.Header("Cache-Control", "public, max-age=86400")
				c.FileFromFS(fileName, http.FS(frontendDist))
			}
		}(name))
	}
}

func cacheControlMiddleware(value string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", value)
		c.Next()
	}
}

func buildFallbackPage(indexHTML string, payload map[string]any, locale string, reqID string) string {
	page := strings.Replace(indexHTML, "<!--app-html-->", `<div id="app"></div>`, 1)
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
