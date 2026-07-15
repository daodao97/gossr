package gossr

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/daodao97/gossr/renderer"
)

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

func withTestSSREngine(t *testing.T, register func(*gin.Engine)) {
	t.Helper()

	oldEngine := SsrEngine
	engine := gin.New()
	engine.Use(gin.Recovery())
	if register != nil {
		register(engine)
	}
	SsrEngine = engine

	t.Cleanup(func() {
		SsrEngine = oldEngine
	})
}

func addSessionTokenCookie(req *http.Request, token string) {
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
}

func TestRenderWithTimeoutReleasesSemaphoreAfterTimeout(t *testing.T) {
	sem := make(chan struct{}, 1)

	slowRenderer := testRenderer(func(ctx context.Context, _ string, _ map[string]any) (renderer.Result, error) {
		select {
		case <-time.After(80 * time.Millisecond):
			return renderer.Result{HTML: "slow"}, nil
		case <-ctx.Done():
			return renderer.Result{}, ctx.Err()
		}
	})

	_, err := renderWithTimeout(context.Background(), slowRenderer, "/", nil, 15*time.Millisecond, sem)
	if err == nil || !strings.Contains(err.Error(), "render timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}

	time.Sleep(120 * time.Millisecond)

	fastRenderer := testRenderer(func(_ context.Context, _ string, _ map[string]any) (renderer.Result, error) {
		return renderer.Result{HTML: "ok"}, nil
	})

	result, err := renderWithTimeout(context.Background(), fastRenderer, "/", nil, 200*time.Millisecond, sem)
	if err != nil {
		t.Fatalf("expected render to succeed after timeout, got %v", err)
	}
	if result.HTML != "ok" {
		t.Fatalf("unexpected html result: %q", result.HTML)
	}
}

func TestRenderWithTimeoutDoesNotRunAfterSemaphoreWaitTimeout(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{}

	called := make(chan struct{}, 1)
	rendererFn := testRenderer(func(_ context.Context, _ string, _ map[string]any) (renderer.Result, error) {
		called <- struct{}{}
		return renderer.Result{HTML: "late"}, nil
	})

	_, err := renderWithTimeout(context.Background(), rendererFn, "/", nil, 20*time.Millisecond, sem)
	if err == nil || !strings.Contains(err.Error(), "render timeout") {
		t.Fatalf("expected timeout error while waiting semaphore, got %v", err)
	}

	<-sem

	select {
	case <-called:
		t.Fatalf("renderer should not run after timeout while waiting semaphore")
	case <-time.After(60 * time.Millisecond):
	}
}

func TestRenderConcurrencyLimit(t *testing.T) {
	defaultLimit := runtime.GOMAXPROCS(0)
	tests := []struct {
		name        string
		envValue    string
		want        int
		logContains string
	}{
		{name: "default", envValue: "", want: defaultLimit},
		{name: "invalid", envValue: "abc", want: defaultLimit, logContains: "invalid SSR_RENDER_LIMIT"},
		{name: "negative", envValue: "-1", want: defaultLimit, logContains: "invalid SSR_RENDER_LIMIT"},
		{name: "zero unlimited", envValue: "0", want: 0, logContains: "SSR_RENDER_LIMIT=0"},
		{name: "clamped", envValue: strconv.Itoa(maxSSRRenderLimit + 100), want: maxSSRRenderLimit, logContains: "clamped"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SSR_RENDER_LIMIT", tt.envValue)

			var got int
			logOutput := captureLogOutput(t, func() {
				got = renderConcurrencyLimit()
			})

			if got != tt.want {
				t.Fatalf("renderConcurrencyLimit()=%d, want %d", got, tt.want)
			}
			if tt.logContains != "" && !strings.Contains(logOutput, tt.logContains) {
				t.Fatalf("expected log to contain %q, got %q", tt.logContains, logOutput)
			}
		})
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

func TestInjectHeadContentReplacesTemplateTitle(t *testing.T) {
	html := `<!doctype html><html><head><title>default</title><meta charset="utf-8"></head><body></body></html>`
	head := `<title>route title</title><meta name="description" content="route">`
	result := injectHeadContent(html, head)
	if strings.Contains(result, "<title>default</title>") {
		t.Fatalf("template title was retained: %s", result)
	}
	if count := strings.Count(strings.ToLower(result), "<title"); count != 1 {
		t.Fatalf("title count=%d, want 1: %s", count, result)
	}
	if !strings.Contains(result, "<title>route title</title>") {
		t.Fatalf("route title missing: %s", result)
	}

	withoutTitle := injectHeadContent(html, `<meta name="robots" content="index">`)
	if !strings.Contains(withoutTitle, "<title>default</title>") {
		t.Fatalf("template title should remain when SSR head has no title: %s", withoutTitle)
	}
}

func TestNewDevProxyRejectsInvalidURL(t *testing.T) {
	for _, rawURL := range []string{"localhost:3333", "ftp://example.com", "://bad"} {
		t.Run(rawURL, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("expected invalid DEV_SERVER_URL %q to panic", rawURL)
				}
			}()
			_ = newDevProxy(rawURL)
		})
	}
}

func TestEnrichPayloadFromRequestWithForwardedOrigin(t *testing.T) {
	t.Setenv("TRUST_FORWARDED_HEADERS", "1")

	req := httptest.NewRequest(http.MethodGet, "/zh/demo", nil)
	req.Host = "10.0.0.12:8080"
	req.Header.Set("X-Forwarded-Host", "app.example.com")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Port", "443")

	enriched, err := enrichPayloadFromRequest(map[string]any{"foo": "bar"}, req, nil)
	if err != nil {
		t.Fatalf("enrich payload failed: %v", err)
	}
	if got, _ := enriched["foo"].(string); got != "bar" {
		t.Fatalf("expected foo field to be preserved, got %#v", enriched["foo"])
	}
	if got, _ := enriched["siteOrigin"].(string); got != "https://app.example.com:443" {
		t.Fatalf("expected siteOrigin from forwarded headers, got %#v", enriched["siteOrigin"])
	}
	if got, _ := enriched["locale"].(string); got != "zh" {
		t.Fatalf("expected locale=zh, got %#v", enriched["locale"])
	}
}

func TestRouterFetchDoesNotInjectSessionInSSRFetchResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTestSSREngine(t, func(engine *gin.Engine) {
		engine.GET("/session-demo", func(c *gin.Context) {
			token, _ := c.Cookie("session_token")
			c.JSON(http.StatusOK, gin.H{
				"path":         c.Request.URL.Path,
				"handlerToken": token,
			})
		})
	})

	router := gin.New()
	Router(router.Group(DefaultSSRDataRoute))

	const sessionToken = "opaque-token-forwarded-to-host-handler"

	w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/session-demo", func(req *http.Request) {
		req.Host = "127.0.0.1:8080"
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		addSessionTokenCookie(req, sessionToken)
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}
	assertNoCacheHeaders(t, w.Header())

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if got, ok := body["handlerToken"].(string); !ok || got != sessionToken {
		t.Fatalf("expected handlerToken=%q, got %#v", sessionToken, body["handlerToken"])
	}

	if _, exists := body["session"]; exists {
		t.Fatalf("expected no session in /_ssr/data response, got %#v", body["session"])
	}
}

func TestRouterWithEngineRejectsInvalidJSONResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := NewDataEngine()
	engine.GET("/invalid", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/plain", []byte("not-json"))
	})

	router := gin.New()
	RouterWithEngine(router.Group(DefaultSSRDataRoute), engine)
	w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/invalid", func(req *http.Request) {
		req.Host = "example.com"
		req.Header.Set("Origin", "http://example.com")
	})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("invalid JSON must not be returned as success: status=%d body=%q", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "not-json") {
		t.Fatalf("invalid backend body leaked to client: %q", w.Body.String())
	}
}

func TestRouterWithEngineAppliesFetchGuardByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := NewDataEngine()
	engine.GET("/private", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"private": true})
	})

	router := gin.New()
	RouterWithEngine(router.Group(DefaultSSRDataRoute), engine)
	w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/private", func(req *http.Request) {
		req.Host = "example.com"
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("public RouterWithEngine bypassed fetch guard: status=%d body=%q", w.Code, w.Body.String())
	}
	assertNoCacheHeaders(t, w.Header())
}

func TestResolveWithEngineKeepsDataRoutesIsolated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engineA := NewDataEngine()
	engineB := NewDataEngine()
	engineA.GET("/same", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"owner": "a"})
	})
	engineB.GET("/same", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"owner": "b"})
	})

	payloadA, status, err := ResolveWithEngine(context.Background(), engineA, "/same", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("resolve engine A: status=%d err=%v", status, err)
	}
	payloadB, status, err := ResolveWithEngine(context.Background(), engineB, "/same", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("resolve engine B: status=%d err=%v", status, err)
	}
	if payloadA.AsMap()["owner"] != "a" || payloadB.AsMap()["owner"] != "b" {
		t.Fatalf("data routes crossed engines: a=%#v b=%#v", payloadA.AsMap(), payloadB.AsMap())
	}
}

func TestResolveWithEnginePreservesTrailingSlash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := NewDataEngine()
	engine.GET("/trailing/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"path": c.Request.URL.Path})
	})

	payload, status, err := ResolveWithEngine(context.Background(), engine, "/trailing/", "a=1")
	if err != nil || status != http.StatusOK {
		t.Fatalf("resolve trailing slash: status=%d err=%v", status, err)
	}
	if got := payload.AsMap()["path"]; got != "/trailing/" {
		t.Fatalf("request path was normalized unexpectedly: %#v", got)
	}
}

func TestSsrWithOptionsUsesInjectedDataEngineAndGenericFS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	dataEngine := NewDataEngine()
	dataEngine.GET("/page", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"owner": "isolated-engine"})
	})
	dist := fstest.MapFS{
		"dist/client/index.html":    {Data: []byte(testIndexHTML)},
		"dist/client/assets/app.js": {Data: []byte("app")},
		"dist/server/server.js":     {Data: []byte("external renderer input")},
	}
	router := gin.New()
	err := SsrWithOptions(router, dist, Options{
		DataEngine: dataEngine,
		RendererFactory: func(string) renderer.Renderer {
			return testRenderer(func(_ context.Context, _ string, payload map[string]any) (renderer.Result, error) {
				return renderer.Result{HTML: "<main>" + fmt.Sprint(payload["owner"]) + "</main>"}, nil
			})
		},
	})
	if err != nil {
		t.Fatalf("SsrWithOptions failed: %v", err)
	}

	w := performRequest(router, http.MethodGet, "/page", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "isolated-engine") {
		t.Fatalf("injected data engine was not used: status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestSsrWithOptionsReturnsStartupErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	t.Run("nil gin engine", func(t *testing.T) {
		if err := SsrWithOptions(nil, fstest.MapFS{}, Options{}); err == nil {
			t.Fatal("expected nil gin engine error")
		}
	})

	t.Run("nil filesystem", func(t *testing.T) {
		if err := SsrWithOptions(gin.New(), nil, Options{}); err == nil {
			t.Fatal("expected nil filesystem error")
		}
	})

	t.Run("missing server entry", func(t *testing.T) {
		dist := fstest.MapFS{
			"dist/client/index.html":    {Data: []byte(testIndexHTML)},
			"dist/client/assets/app.js": {Data: []byte("app")},
			"dist/server/.keep":         {Data: nil},
		}
		if err := SsrWithOptions(gin.New(), dist, Options{}); err == nil || !strings.Contains(err.Error(), "server.js") {
			t.Fatalf("expected server.js error, got %v", err)
		}
	})

	t.Run("renderer factory panic", func(t *testing.T) {
		dist := fstest.MapFS{
			"dist/client/index.html":    {Data: []byte(testIndexHTML)},
			"dist/client/assets/app.js": {Data: []byte("app")},
			"dist/server/server.js":     {Data: []byte("script")},
		}
		err := SsrWithOptions(gin.New(), dist, Options{
			RendererFactory: func(string) renderer.Renderer { panic("broken adapter") },
		})
		if err == nil || !strings.Contains(err.Error(), "broken adapter") {
			t.Fatalf("expected renderer creation error, got %v", err)
		}
	})

	t.Run("nil renderer", func(t *testing.T) {
		dist := fstest.MapFS{
			"dist/client/index.html":    {Data: []byte(testIndexHTML)},
			"dist/client/assets/app.js": {Data: []byte("app")},
			"dist/server/server.js":     {Data: []byte("script")},
		}
		err := SsrWithOptions(gin.New(), dist, Options{
			RendererFactory: func(string) renderer.Renderer { return nil },
		})
		if err == nil || !strings.Contains(err.Error(), "returned nil") {
			t.Fatalf("expected nil renderer error, got %v", err)
		}
	})

	t.Run("invalid site origin", func(t *testing.T) {
		dist := fstest.MapFS{
			"dist/client/index.html":    {Data: []byte(testIndexHTML)},
			"dist/client/assets/app.js": {Data: []byte("app")},
			"dist/server/server.js":     {Data: []byte("script")},
		}
		err := SsrWithOptions(gin.New(), dist, Options{
			SiteOrigin: "https://user:password@example.com/path?q=secret",
			RendererFactory: func(string) renderer.Renderer {
				return testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
					return renderer.Result{}, nil
				})
			},
		})
		if err == nil || !strings.Contains(err.Error(), "invalid SiteOrigin") {
			t.Fatalf("expected SiteOrigin validation error, got %v", err)
		}
	})
}

func TestSsrWithOptionsClosesRendererOnShutdown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	ctx, cancel := context.WithCancel(context.Background())
	instance := &closeTrackingRenderer{closed: make(chan struct{})}
	dist := fstest.MapFS{
		"dist/client/index.html":    {Data: []byte(testIndexHTML)},
		"dist/client/assets/app.js": {Data: []byte("app")},
		"dist/server/server.js":     {Data: []byte("script")},
	}
	err := SsrWithOptions(gin.New(), dist, Options{
		ShutdownContext: ctx,
		RendererFactory: func(string) renderer.Renderer {
			return instance
		},
	})
	if err != nil {
		t.Fatalf("SsrWithOptions failed: %v", err)
	}

	cancel()
	select {
	case <-instance.closed:
	case <-time.After(time.Second):
		t.Fatal("renderer was not closed after shutdown")
	}
}

func TestSsrWithOptionsUsesHostFetchAuthorizer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	dataEngine := NewDataEngine()
	dataEngine.GET("/private", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	dist := fstest.MapFS{
		"dist/client/index.html":    {Data: []byte(testIndexHTML)},
		"dist/client/assets/app.js": {Data: []byte("app")},
		"dist/server/server.js":     {Data: []byte("script")},
	}
	router := gin.New()
	err := SsrWithOptions(router, dist, Options{
		DataEngine: dataEngine,
		SSRFetchAuthorizer: func(req *http.Request) (int, bool) {
			return http.StatusUnauthorized, req.Header.Get("Authorization") == "Bearer allowed"
		},
		RendererFactory: func(string) renderer.Renderer {
			return testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
				return renderer.Result{HTML: "<main>ok</main>"}, nil
			})
		},
	})
	if err != nil {
		t.Fatalf("SsrWithOptions failed: %v", err)
	}

	denied := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/private", nil)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("host authorizer did not reject request: %d", denied.Code)
	}
	allowed := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/private", func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer allowed")
	})
	if allowed.Code != http.StatusOK {
		t.Fatalf("host authorizer did not allow request: %d body=%s", allowed.Code, allowed.Body.String())
	}
}

func TestRunBlockingConfiguredSiteOriginOverridesHost(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	router := gin.New()
	RunBlocking(router, FrontendBuild{
		FrontendDist: testFrontendDistFS(),
		ServerDist: fstest.MapFS{
			"server.js": {Data: []byte("external renderer input")},
		},
		SiteOrigin: "https://canonical.example.com",
		RendererFactory: func(string) renderer.Renderer {
			return testRenderer(func(_ context.Context, _ string, payload map[string]any) (renderer.Result, error) {
				return renderer.Result{HTML: "<main>" + fmt.Sprint(payload["siteOrigin"]) + "</main>"}, nil
			})
		},
	}, nil)

	w := performRequest(router, http.MethodGet, "/", func(req *http.Request) {
		req.Host = "attacker-controlled.example"
	})
	if !strings.Contains(w.Body.String(), "https://canonical.example.com") || strings.Contains(w.Body.String(), "attacker-controlled.example") {
		t.Fatalf("configured SiteOrigin was not authoritative: %q", w.Body.String())
	}
}

func TestEnrichPayloadFromRequestInjectsSessionForSSRRender(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/session-demo", nil)
	req.Host = "127.0.0.1:8080"

	addSessionTokenCookie(req, "opaque-host-token")

	resolver := func(ctx context.Context, gotReq *http.Request) (map[string]any, error) {
		if ctx != req.Context() {
			t.Fatal("resolver did not receive request context")
		}
		cookie, err := gotReq.Cookie("session_token")
		if err != nil || cookie.Value != "opaque-host-token" {
			t.Fatalf("resolver received unexpected cookie: cookie=%#v err=%v", cookie, err)
		}
		return map[string]any{
			"user": map[string]any{
				"email": "demo@example.com",
			},
		}, nil
	}

	enriched, err := enrichPayloadFromRequest(map[string]any{"foo": "bar"}, req, resolver)
	if err != nil {
		t.Fatalf("enrich payload failed: %v", err)
	}
	session, ok := enriched["session"].(map[string]any)
	if !ok {
		t.Fatalf("expected session object in enriched payload, got %#v", enriched["session"])
	}

	user, ok := session["user"].(map[string]any)
	if !ok {
		t.Fatalf("expected session.user object, got %#v", session["user"])
	}

	if got, ok := user["email"].(string); !ok || got != "demo@example.com" {
		t.Fatalf("expected session.user.email=demo@example.com, got %#v", user["email"])
	}
	if _, exists := session["session_token"]; exists {
		t.Fatalf("resolver example must not expose raw credentials: %#v", session)
	}
}

func TestSSRFetchGuardDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	withTestSSREngine(t, func(engine *gin.Engine) {
		engine.GET("/guard-demo", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	})

	router := gin.New()
	registerSSRFetchRoutes(router)

	t.Run("missing origin and missing header", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Host = "127.0.0.1:8080"
		})
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("missing origin but explicit header", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Host = "127.0.0.1:8080"
			req.Header.Set("X-SSR-Fetch", "1")
		})
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("same origin without explicit header", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Host = "127.0.0.1:8080"
			req.Header.Set("Origin", "http://127.0.0.1:8080")
		})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("same host with different scheme is forbidden", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Host = "example.com"
			req.Header.Set("Origin", "https://example.com")
		})
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("default https port is equivalent", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Host = "example.com:443"
			req.TLS = &tls.ConnectionState{}
			req.Header.Set("Origin", "https://example.com")
		})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("different port is forbidden", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Host = "example.com:8080"
			req.Header.Set("Origin", "http://example.com:8081")
		})
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("browser same-origin metadata works without referrer", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Host = "127.0.0.1:8080"
			req.Header.Set("Sec-Fetch-Site", "same-origin")
		})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestSSRFetchGuardWithHostAuthorizer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	withTestSSREngine(t, func(engine *gin.Engine) {
		engine.GET("/guard-demo", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	})

	router := gin.New()
	registerSSRFetchRoutesWithAuthorizer(router, SsrEngine, func(req *http.Request) (int, bool) {
		if req.Header.Get("Authorization") != "Bearer host-token" {
			return http.StatusUnauthorized, false
		}
		return 0, true
	})

	t.Run("same origin without host authorization is rejected", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Host = "127.0.0.1:8080"
			req.Header.Set("Origin", "http://127.0.0.1:8080")
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("wrong host authorization", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer wrong-token")
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("host authorization can allow service requests", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer host-token")
		})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestRegisterSSRFetchRoutesFetcherForwardsCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTestSSREngine(t, func(engine *gin.Engine) {
		engine.GET("/cookie-demo", func(c *gin.Context) {
			token, _ := c.Cookie("session_token")
			c.JSON(http.StatusOK, gin.H{
				"handlerToken": token,
			})
		})
	})

	router := gin.New()
	fetcher := registerSSRFetchRoutes(router)

	const token = "session-token-xyz"
	req := httptest.NewRequest(http.MethodGet, "/cookie-demo?from=server", nil)
	req.Host = "127.0.0.1:8080"
	addSessionTokenCookie(req, token)

	payload, err := fetcher(context.Background(), req)
	if err != nil {
		t.Fatalf("fetcher returned error: %v", err)
	}

	body := payload.AsMap()
	if got, ok := body["handlerToken"].(string); !ok || got != token {
		t.Fatalf("expected handlerToken=%q, got %#v", token, body["handlerToken"])
	}
}

func TestWrapSSRMaskHandlerErrorByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SSR_EXPOSE_HANDLER_ERROR", "")

	router := gin.New()
	router.GET("/boom", WrapSSR(func(*gin.Context) (SSRPayload, error) {
		return nil, errors.New("db: secret leaked")
	}))

	w := performRequest(router, http.MethodGet, "/boom", nil)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if got, _ := body["error"].(string); got != "internal server error" {
		t.Fatalf("expected masked error, got %#v", body["error"])
	}
}

func TestWrapSSRCanExposeHandlerError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SSR_EXPOSE_HANDLER_ERROR", "1")
	t.Setenv("DEV_MODE", "1")

	router := gin.New()
	router.GET("/boom", WrapSSR(func(*gin.Context) (SSRPayload, error) {
		return nil, errors.New("db: secret leaked")
	}))

	w := performRequest(router, http.MethodGet, "/boom", nil)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if got, _ := body["error"].(string); got != "db: secret leaked" {
		t.Fatalf("expected original error, got %#v", body["error"])
	}
}

func TestWrapSSRDoesNotExposeHandlerErrorOutsideDevMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SSR_EXPOSE_HANDLER_ERROR", "1")
	t.Setenv("DEV_MODE", "")

	router := gin.New()
	router.GET("/boom", WrapSSR(func(*gin.Context) (SSRPayload, error) {
		return nil, errors.New("db: secret leaked")
	}))

	w := performRequest(router, http.MethodGet, "/boom", nil)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if got, _ := body["error"].(string); got != "internal server error" {
		t.Fatalf("expected masked error in non-dev mode, got %#v", body["error"])
	}
}

func TestEnrichPayloadWithoutResolverDoesNotReadSession(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/session-demo", nil)
	addSessionTokenCookie(req, "credential-must-be-ignored")

	enriched, err := enrichPayloadFromRequest(map[string]any{
		"foo":     "bar",
		"session": map[string]any{"user": "forged"},
	}, req, nil)
	if err != nil {
		t.Fatalf("enrich payload failed: %v", err)
	}
	if _, exists := enriched["session"]; exists {
		t.Fatalf("session must be stripped without resolver: %#v", enriched)
	}
}

func TestEnrichPayloadResolverIsAuthoritativeForSession(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/session-demo", nil)
	enriched, err := enrichPayloadFromRequest(map[string]any{
		"session": map[string]any{"user": "forged"},
	}, req, func(context.Context, *http.Request) (map[string]any, error) {
		return map[string]any{"user": "verified"}, nil
	})
	if err != nil {
		t.Fatalf("enrich payload failed: %v", err)
	}
	session, ok := enriched["session"].(map[string]any)
	if !ok || session["user"] != "verified" {
		t.Fatalf("resolver session must replace payload session: %#v", enriched["session"])
	}
}

func TestEnrichPayloadPropagatesSessionResolverError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/session-demo", nil)
	wantErr := errors.New("session backend unavailable")

	_, err := enrichPayloadFromRequest(nil, req, func(context.Context, *http.Request) (map[string]any, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected resolver error %v, got %v", wantErr, err)
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

const testIndexHTML = "<!doctype html><html><head></head><body><!--app-html--></body></html>"

func testFrontendDistFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html": {
			Data: []byte(testIndexHTML),
		},
		"assets/app.js": {
			Data: []byte("console.log('ok')"),
		},
		"favicon.ico": {
			Data: []byte("ico"),
		},
	}
}

func testRouterWithRunBlocking(serverScript string) *gin.Engine {
	router := gin.New()
	RunBlocking(router, FrontendBuild{
		FrontendDist: testFrontendDistFS(),
		ServerDist: fstest.MapFS{
			"server.js": {
				Data: []byte(serverScript),
			},
		},
	}, nil)
	return router
}

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

func TestRunBlockingCacheHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	router := testRouterWithRunBlocking(`globalThis.ssrRender = function(url) { return "<div id='app'>SSR:" + url + "</div>" }`)

	t.Run("ssr html uses no-cache headers", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, "/hello", nil)

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
		w := performRequest(router, http.MethodGet, "/_ssr/database", nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "SSR:/_ssr/database") {
			t.Fatalf("similar prefix was swallowed by data route: status=%d body=%q", w.Code, w.Body.String())
		}
	})
}

func TestRunBlockingDoesNotRegisterBusinessInviteRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	router := gin.New()
	router.GET("/i/:invite_code", func(c *gin.Context) {
		c.String(http.StatusOK, "host:"+c.Param("invite_code"))
	})
	RunBlocking(router, FrontendBuild{
		FrontendDist: testFrontendDistFS(),
		ServerDist: fstest.MapFS{
			"server.js": {Data: []byte(`globalThis.ssrRender = function() { return "ssr" }`)},
		},
	}, nil)

	w := performRequest(router, http.MethodGet, "/i/owned-by-host", nil)
	if w.Code != http.StatusOK || w.Body.String() != "host:owned-by-host" {
		t.Fatalf("host invite route was not preserved: status=%d body=%q", w.Code, w.Body.String())
	}
	if cookies := w.Header().Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf("library unexpectedly wrote invite cookie: %v", cookies)
	}
}

func TestRunBlockingUsesInjectedRendererFactory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	const serverScript = "external renderer input"
	var factoryScript string
	var renderCalls atomic.Int32
	router := gin.New()
	RunBlocking(router, FrontendBuild{
		FrontendDist: testFrontendDistFS(),
		ServerDist: fstest.MapFS{
			"server.js": {Data: []byte(serverScript)},
		},
		RendererFactory: func(scriptContents string) renderer.Renderer {
			factoryScript = scriptContents
			return testRenderer(func(_ context.Context, urlPath string, _ map[string]any) (renderer.Result, error) {
				renderCalls.Add(1)
				return renderer.Result{
					HTML: "<main>external:" + urlPath + "</main>",
					Head: "<title>external</title>",
				}, nil
			})
		},
	}, nil)

	if factoryScript != serverScript {
		t.Fatalf("factory received script %q, want %q", factoryScript, serverScript)
	}
	if got := renderCalls.Load(); got != 0 {
		t.Fatalf("renderer was called implicitly during registration: %d", got)
	}

	w := performRequest(router, http.MethodGet, "/injected?title=hello%20world&tag=a%2Fb", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "external:/injected?title=hello%20world&tag=a%2Fb") || !strings.Contains(body, "<title>external</title>") {
		t.Fatalf("expected injected renderer output, got %s", body)
	}
	if got := renderCalls.Load(); got != 1 {
		t.Fatalf("renderer call count=%d, want 1", got)
	}
}

func TestRunBlockingInjectsResolvedSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	router := gin.New()
	RunBlocking(router, FrontendBuild{
		FrontendDist: testFrontendDistFS(),
		ServerDist: fstest.MapFS{
			"server.js": {Data: []byte("external renderer input")},
		},
		RendererFactory: func(string) renderer.Renderer {
			return testRenderer(func(_ context.Context, _ string, payload map[string]any) (renderer.Result, error) {
				session, _ := payload["session"].(map[string]any)
				user, _ := session["user"].(map[string]any)
				email, _ := user["email"].(string)
				return renderer.Result{HTML: "<main>" + email + "</main>"}, nil
			})
		},
		SessionResolver: func(_ context.Context, req *http.Request) (map[string]any, error) {
			cookie, err := req.Cookie("app_session")
			if err != nil || cookie.Value != "verified-opaque-token" {
				return nil, nil
			}
			return map[string]any{
				"user": map[string]any{"email": "safe@example.com"},
			}, nil
		},
	}, nil)

	w := performRequest(router, http.MethodGet, "/private", func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: "app_session", Value: "verified-opaque-token"})
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "safe@example.com") || strings.Contains(body, "verified-opaque-token") {
		t.Fatalf("expected safe resolved session without raw credential, got %s", body)
	}
}

func TestRunBlockingRejectsSessionResolverError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	router := gin.New()
	RunBlocking(router, FrontendBuild{
		FrontendDist: testFrontendDistFS(),
		ServerDist: fstest.MapFS{
			"server.js": {Data: []byte("external renderer input")},
		},
		RendererFactory: func(string) renderer.Renderer {
			return testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
				return renderer.Result{HTML: "<main>must not render</main>"}, nil
			})
		},
		SessionResolver: func(context.Context, *http.Request) (map[string]any, error) {
			return nil, errors.New("invalid session signature")
		},
	}, nil)

	w := performRequest(router, http.MethodGet, "/private", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "must not render") {
		t.Fatalf("renderer must not run after resolver error, got %s", w.Body.String())
	}
}

func TestRunBlockingFallbackKeepsNoCacheHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	router := testRouterWithRunBlocking(`globalThis.ssrRender = function() { throw new Error("render failed") }`)

	w := performRequest(router, http.MethodGet, "/fallback", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}
	assertNoCacheHeaders(t, w.Header())
	if !strings.Contains(w.Body.String(), `name="ssr-error-id"`) {
		t.Fatalf("expected fallback page to include ssr-error-id meta, got %s", w.Body.String())
	}
}

func TestRunBlockingFallsBackWhenPayloadCannotBeInjected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	router := gin.New()
	RunBlocking(router, FrontendBuild{
		FrontendDist: testFrontendDistFS(),
		ServerDist: fstest.MapFS{
			"server.js": {Data: []byte("external renderer input")},
		},
		RendererFactory: func(string) renderer.Renderer {
			return testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
				return renderer.Result{HTML: "<main>must-not-be-served</main>"}, nil
			})
		},
	}, func(context.Context, *http.Request) (SSRPayload, error) {
		return mapPayload{"invalid": make(chan int)}, nil
	})

	w := performRequest(router, http.MethodGet, "/invalid-payload", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected fallback status 200, got %d", w.Code)
	}
	assertNoCacheHeaders(t, w.Header())
	if strings.Contains(w.Body.String(), "must-not-be-served") || !strings.Contains(w.Body.String(), `name="ssr-error-id"`) {
		t.Fatalf("expected empty fallback with error id, got %q", w.Body.String())
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
