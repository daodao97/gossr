package gossr

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
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

func mustSessionToken(t *testing.T, payload map[string]any) string {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal session payload failed: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
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
}

func TestEnrichPayloadFromRequestWithForwardedOrigin(t *testing.T) {
	t.Setenv("TRUST_FORWARDED_HEADERS", "1")

	req := httptest.NewRequest(http.MethodGet, "/zh/demo", nil)
	req.Host = "10.0.0.12:8080"
	req.Header.Set("X-Forwarded-Host", "app.example.com")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Port", "443")

	enriched := enrichPayloadFromRequest(map[string]any{"foo": "bar"}, req)
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

	sessionToken := mustSessionToken(t, map[string]any{
		"id":       "u_demo_1001",
		"name":     "SSR Demo User",
		"email":    "demo@example.com",
		"provider": "example",
		"iat":      1700000000,
	})

	w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/session-demo", func(req *http.Request) {
		req.Host = "127.0.0.1:8080"
		addSessionTokenCookie(req, sessionToken)
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}

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

func TestEnrichPayloadFromRequestInjectsSessionForSSRRender(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/session-demo", nil)
	req.Host = "127.0.0.1:8080"

	sessionToken := mustSessionToken(t, map[string]any{
		"id":       "u_demo_1001",
		"name":     "SSR Demo User",
		"email":    "demo@example.com",
		"provider": "example",
		"iat":      1700000000,
	})
	addSessionTokenCookie(req, sessionToken)

	enriched := enrichPayloadFromRequest(map[string]any{"foo": "bar"}, req)
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
}

func TestSSRFetchGuardWithoutToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SSR_FETCH_TOKEN", "")

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

	t.Run("allow explicit header only when unsafe bypass enabled", func(t *testing.T) {
		t.Setenv("SSR_ALLOW_UNSAFE_FETCH_HEADER", "1")

		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Host = "127.0.0.1:8080"
			req.Header.Set("X-SSR-Fetch", "1")
		})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestSSRFetchGuardWithSharedToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SSR_FETCH_TOKEN", "secret-token")

	withTestSSREngine(t, func(engine *gin.Engine) {
		engine.GET("/guard-demo", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	})

	router := gin.New()
	registerSSRFetchRoutes(router)

	t.Run("same origin without token still forbidden", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Host = "127.0.0.1:8080"
			req.Header.Set("Origin", "http://127.0.0.1:8080")
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Host = "127.0.0.1:8080"
			req.Header.Set("X-SSR-Token", "bad-token")
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("correct token", func(t *testing.T) {
		w := performRequest(router, http.MethodGet, DefaultSSRDataRoute+"/guard-demo", func(req *http.Request) {
			req.Host = "127.0.0.1:8080"
			req.Header.Set("X-SSR-Token", "secret-token")
		})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestRegisterSSRFetchRoutesFetcherForwardsCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SSR_FETCH_TOKEN", "")

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

func TestSessionTokenParserCanBeCustomized(t *testing.T) {
	SetSessionTokenParser(func(token string) (map[string]any, error) {
		if token != "signed-ok" {
			return nil, errors.New("invalid signature")
		}
		return map[string]any{
			"user": map[string]any{
				"email": "signed@example.com",
			},
			"session_token": token,
		}, nil
	})
	defer SetSessionTokenParser(nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	addSessionTokenCookie(req, "signed-ok")
	session := sessionStateFromRequest(req)
	if session == nil {
		t.Fatal("expected custom parser to return session")
	}

	reqInvalid := httptest.NewRequest(http.MethodGet, "/", nil)
	addSessionTokenCookie(reqInvalid, "bad-token")
	if session := sessionStateFromRequest(reqInvalid); session != nil {
		t.Fatalf("expected invalid token to be rejected, got %#v", session)
	}
}

func TestSessionTokenParserResetToDefault(t *testing.T) {
	SetSessionTokenParser(nil)

	token := mustSessionToken(t, map[string]any{
		"id":       "u1",
		"name":     "Tester",
		"email":    "tester@example.com",
		"provider": "test",
		"iat":      1700000000,
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	addSessionTokenCookie(req, token)
	session := sessionStateFromRequest(req)
	if session == nil {
		t.Fatal("expected default parser to parse base64 JSON session")
	}
}

func TestSessionStateFromRequestLogsParseFailureWithoutLeakingToken(t *testing.T) {
	SetSessionTokenParser(nil)
	defer SetSessionTokenParser(nil)

	const token = "not-base64-token-@@@"
	req := httptest.NewRequest(http.MethodGet, "/session-demo", nil)
	addSessionTokenCookie(req, token)

	var session map[string]any
	logOutput := captureLogOutput(t, func() {
		session = sessionStateFromRequest(req)
	})

	if session != nil {
		t.Fatalf("expected parse failure to return nil, got %#v", session)
	}
	if !strings.Contains(logOutput, "invalid session_token cookie ignored") {
		t.Fatalf("expected parse failure log, got %q", logOutput)
	}
	if strings.Contains(logOutput, token) {
		t.Fatalf("log should not contain raw token value, got %q", logOutput)
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
}

func TestRunBlockingFallbackKeepsNoCacheHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	router := testRouterWithRunBlocking(`globalThis.__not_renderer__ = true`)

	w := performRequest(router, http.MethodGet, "/fallback", nil)

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
