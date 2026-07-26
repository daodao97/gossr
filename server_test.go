package gossr

import (
	"bytes"
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/daodao97/gossr/renderer"
)

type mapPayload map[string]any

func (m mapPayload) AsMap() map[string]any {
	return m
}

type testRenderer func(ctx context.Context, urlPath string, payload map[string]any) (renderer.Result, error)

func (f testRenderer) Render(ctx context.Context, urlPath string, payload map[string]any) (renderer.Result, error) {
	return f(ctx, urlPath, payload)
}

type closeTrackingRenderer struct {
	closed chan struct{}
}

func (r *closeTrackingRenderer) Render(context.Context, string, map[string]any) (renderer.Result, error) {
	return renderer.Result{HTML: "<main>closed on shutdown</main>"}, nil
}

func (r *closeTrackingRenderer) Close() error {
	close(r.closed)
	return nil
}

func captureLogOutput(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	oldPrefix := log.Prefix()

	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	})

	fn()
	return buf.String()
}

func performRequest(handler http.Handler, method, target string, setup func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if setup != nil {
		setup(req)
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func addSessionTokenCookie(req *http.Request, token string) {
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
}

func TestRenderWithTimeoutCancelsSlowRender(t *testing.T) {
	slowRenderer := testRenderer(func(ctx context.Context, _ string, _ map[string]any) (renderer.Result, error) {
		select {
		case <-time.After(80 * time.Millisecond):
			return renderer.Result{HTML: "slow"}, nil
		case <-ctx.Done():
			return renderer.Result{}, ctx.Err()
		}
	})

	_, err := renderWithTimeout(context.Background(), slowRenderer, "/", nil, 15*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "render timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}

	fastRenderer := testRenderer(func(_ context.Context, _ string, _ map[string]any) (renderer.Result, error) {
		return renderer.Result{HTML: "ok"}, nil
	})

	result, err := renderWithTimeout(context.Background(), fastRenderer, "/", nil, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("expected render to succeed, got %v", err)
	}
	if result.HTML != "ok" {
		t.Fatalf("unexpected html result: %q", result.HTML)
	}
}

func TestRequestOrigin(t *testing.T) {
	t.Run("host and tls fallback", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
		req.Host = "example.com"
		if got := requestOrigin(req); got != "http://example.com" {
			t.Fatalf("requestOrigin()=%q, want %q", got, "http://example.com")
		}

		req.TLS = &tls.ConnectionState{}
		if got := requestOrigin(req); got != "https://example.com" {
			t.Fatalf("requestOrigin()=%q, want %q", got, "https://example.com")
		}
	})

	t.Run("forwarded headers ignored by default", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
		req.Host = "10.0.0.12:8080"
		req.Header.Set("X-Forwarded-Host", "app.example.com")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Port", "443")

		if got := requestOrigin(req); got != "http://10.0.0.12:8080" {
			t.Fatalf("requestOrigin()=%q, want %q", got, "http://10.0.0.12:8080")
		}
	})

	t.Run("forwarded host proto and port", func(t *testing.T) {
		t.Setenv("TRUST_FORWARDED_HEADERS", "1")

		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
		req.Host = "10.0.0.12:8080"
		req.Header.Set("X-Forwarded-Host", "app.example.com, proxy.internal")
		req.Header.Set("X-Forwarded-Proto", "https,http")
		req.Header.Set("X-Forwarded-Port", "443,80")

		if got := requestOrigin(req); got != "https://app.example.com:443" {
			t.Fatalf("requestOrigin()=%q, want %q", got, "https://app.example.com:443")
		}
	})

	t.Run("host already has explicit port", func(t *testing.T) {
		t.Setenv("TRUST_FORWARDED_HEADERS", "1")

		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
		req.Host = "10.0.0.12:8080"
		req.Header.Set("X-Forwarded-Host", "app.example.com:8443")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Port", "443")

		if got := requestOrigin(req); got != "https://app.example.com:8443" {
			t.Fatalf("requestOrigin()=%q, want %q", got, "https://app.example.com:8443")
		}
	})

	t.Run("invalid forwarded origin values are rejected", func(t *testing.T) {
		t.Setenv("TRUST_FORWARDED_HEADERS", "1")

		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
		req.Host = "app.example.com"
		req.Header.Set("X-Forwarded-Proto", "javascript")
		if got := requestOrigin(req); got != "" {
			t.Fatalf("invalid forwarded proto produced origin %q", got)
		}

		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Port", "99999")
		if got := requestOrigin(req); got != "" {
			t.Fatalf("invalid forwarded port produced origin %q", got)
		}
	})
}

func TestRenderDocumentReplacesTemplateTitle(t *testing.T) {
	html := `<!doctype html><html><head><title>default</title><meta charset="utf-8"></head>` +
		`<body><div id="app"><!--app-html--></div></body></html>`
	page, err := compileIndexTemplate(html)
	if err != nil {
		t.Fatalf("compileIndexTemplate: %v", err)
	}

	head := `<title>route title</title><meta name="description" content="route">`
	result := page.renderDocument(renderer.Result{HTML: "<main>x</main>", Head: head}, "", "")
	if strings.Contains(result, "<title>default</title>") {
		t.Fatalf("template title was retained: %s", result)
	}
	if count := strings.Count(strings.ToLower(result), "<title"); count != 1 {
		t.Fatalf("title count=%d, want 1: %s", count, result)
	}
	if !strings.Contains(result, "<title>route title</title>") {
		t.Fatalf("route title missing: %s", result)
	}

	withoutTitle := page.renderDocument(
		renderer.Result{HTML: "<main>x</main>", Head: `<meta name="robots" content="index">`},
		"",
		"",
	)
	if !strings.Contains(withoutTitle, "<title>default</title>") {
		t.Fatalf("template title should remain when SSR head has no title: %s", withoutTitle)
	}
}

func TestApplyHTMLLangUsesTheStructuralHTMLTag(t *testing.T) {
	html := `<script>const fake = "<html lang='script'>"</script>` +
		`<HTML LANG='en' data-shell="true"><head></head><body></body></HTML>`
	result := applyHTMLLang(html, "zh-CN")
	if !strings.Contains(result, `const fake = "<html lang='script'>"`) {
		t.Fatalf("script text was changed: %s", result)
	}
	if strings.Count(strings.ToLower(result), `lang=`) != 2 {
		t.Fatalf("structural html tag gained duplicate lang attributes: %s", result)
	}
	if !strings.Contains(result, `lang="zh-CN"`) {
		t.Fatalf("structural html lang was not updated: %s", result)
	}
}

func TestIsStaticAssetLikePath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "favicon", path: "/favicon.ico", want: true},
		{name: "root txt", path: "/robots.txt", want: true},
		{name: "nested js", path: "/assets/app.js", want: true},
		{name: "hashed css", path: "/assets/app.123abc.css", want: true},
		{name: "locale svg", path: "/en/logo.svg", want: true},
		{name: "wasm asset", path: "/assets/engine.wasm", want: true},
		{name: "root page", path: "/", want: false},
		{name: "locale root", path: "/zh", want: false},
		{name: "normal route", path: "/slow-ssr", want: false},
		{name: "dot in middle segment", path: "/a.b/c", want: false},
		{name: "email route", path: "/users/alice@example.com", want: false},
		{name: "version route", path: "/posts/release-1.2", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isStaticAssetLikePath(tt.path)
			if got != tt.want {
				t.Fatalf("isStaticAssetLikePath(%q)=%v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

const testIndexHTML = `<!doctype html><html><head></head><body><div id="app"><!--app-html--></div></body></html>`

func assertNoCacheHeaders(t *testing.T, header http.Header) {
	t.Helper()

	if got := header.Get("Cache-Control"); got != "no-cache, no-store, must-revalidate" {
		t.Fatalf("expected no-cache header, got %q", got)
	}
	if got := header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("expected Pragma=no-cache, got %q", got)
	}
	if got := header.Get("Expires"); got != "0" {
		t.Fatalf("expected Expires=0, got %q", got)
	}
}

func testRouterWithScript(t *testing.T, script string) *gin.Engine {
	t.Helper()
	runtime, err := New(Config{
		Bundle: fstest.MapFS{
			"dist/client/index.html":    {Data: []byte(testIndexHTML)},
			"dist/client/assets/app.js": {Data: []byte("console.log('ok')")},
			"dist/client/favicon.ico":   {Data: []byte("ico")},
			"dist/server/server.js":     {Data: []byte(script)},
		},
		Mode: ModeProduction,
	})
	if err != nil {
		t.Fatalf("create SSR runtime: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close SSR runtime: %v", err)
		}
	})
	router := gin.New()
	if err := runtime.MountGin(router, GinOptions{
		Resolver: func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{Payload: mapPayload{"viewer": nil}}, nil
		},
	}); err != nil {
		t.Fatalf("mount SSR runtime: %v", err)
	}
	return router
}

func TestDocumentCacheHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	router := testRouterWithScript(t, `globalThis.ssrRender = function(url) { return "<div>SSR:" + url + "</div>" }`)

	t.Run("ssr html uses no-cache headers", func(t *testing.T) {
		w := htmlRequest(router, http.MethodGet, "/hello", nil)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
		}
		assertNoCacheHeaders(t, w.Header())
		if !strings.Contains(w.Body.String(), "SSR:/hello") {
			t.Fatalf("expected rendered html in body, got %s", w.Body.String())
		}
	})

	t.Run("assets use immutable long cache", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, "/assets/app.js", nil)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Fatalf("expected immutable cache header, got %q", got)
		}
	})

	t.Run("root static files use short cache", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, "/favicon.ico", nil)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Cache-Control"); got != "public, max-age=86400" {
			t.Fatalf("expected short cache header, got %q", got)
		}
	})

	t.Run("similar data prefix remains an application route", func(t *testing.T) {
		w := htmlRequest(router, http.MethodGet, "/_ssr/database", nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "SSR:/_ssr/database") {
			t.Fatalf("similar prefix was swallowed by data route: status=%d body=%q", w.Code, w.Body.String())
		}
	})
}

func TestDocumentFallbackKeepsNoCacheHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	router := testRouterWithScript(t, `globalThis.ssrRender = function() { throw new Error("render failed") }`)

	w := htmlRequest(router, http.MethodGet, "/fallback", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}
	assertNoCacheHeaders(t, w.Header())
	if !strings.Contains(w.Body.String(), `name="ssr-error-id"`) {
		t.Fatalf("expected fallback page to include ssr-error-id meta, got %s", w.Body.String())
	}
}

func TestRegisterRootStaticFilesSkipsIndexAndNestedEntries(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	frontendDist := fstest.MapFS{
		"index.html": {
			Data: []byte(testIndexHTML),
		},
		"robots.txt": {
			Data: []byte("User-agent: *"),
		},
		"assets/app.js": {
			Data: []byte("console.log('ok')"),
		},
	}

	registerRootStaticFiles(router, frontendDist)

	t.Run("root file is registered with short cache", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, "/robots.txt", nil)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Cache-Control"); got != cacheShortRootFile {
			t.Fatalf("expected short cache header %q, got %q", cacheShortRootFile, got)
		}
		if body := w.Body.String(); !strings.Contains(body, "User-agent: *") {
			t.Fatalf("expected robots.txt body, got %q", body)
		}
	})

	t.Run("index html is not directly exposed", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, "/index.html", nil)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("nested file is not registered at root", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, "/assets/app.js", nil)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d body=%s", w.Code, w.Body.String())
		}
	})
}
