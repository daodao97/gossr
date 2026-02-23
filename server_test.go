package gossr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/daodao97/gossr/renderer"
)

type testRenderer func(ctx context.Context, urlPath string, payload map[string]any) (renderer.Result, error)

func (f testRenderer) Render(ctx context.Context, urlPath string, payload map[string]any) (renderer.Result, error) {
	return f(ctx, urlPath, payload)
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

	_, err := renderWithTimeout(slowRenderer, "/", nil, 15*time.Millisecond, sem)
	if err == nil || !strings.Contains(err.Error(), "render timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}

	time.Sleep(120 * time.Millisecond)

	fastRenderer := testRenderer(func(_ context.Context, _ string, _ map[string]any) (renderer.Result, error) {
		return renderer.Result{HTML: "ok"}, nil
	})

	result, err := renderWithTimeout(fastRenderer, "/", nil, 200*time.Millisecond, sem)
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

	_, err := renderWithTimeout(rendererFn, "/", nil, 20*time.Millisecond, sem)
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

func TestRouterFetchInjectsSessionInDevFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oldEngine := SsrEngine
	defer func() {
		SsrEngine = oldEngine
	}()

	SsrEngine = gin.New()
	SsrEngine.Use(gin.Recovery())
	SsrEngine.GET("/session-demo", func(c *gin.Context) {
		token, _ := c.Cookie("session_token")
		c.JSON(http.StatusOK, gin.H{
			"path":         c.Request.URL.Path,
			"handlerToken": token,
		})
	})

	router := gin.New()
	Router(router.Group(DefaultSSRFetchPrefix))

	sessionRaw, err := json.Marshal(map[string]any{
		"id":       "u_demo_1001",
		"name":     "SSR Demo User",
		"email":    "demo@example.com",
		"provider": "example",
		"iat":      1700000000,
	})
	if err != nil {
		t.Fatalf("marshal session payload failed: %v", err)
	}
	sessionToken := base64.StdEncoding.EncodeToString(sessionRaw)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, DefaultSSRFetchPrefix+"/session-demo", nil)
	req.Host = "127.0.0.1:8080"
	req.AddCookie(&http.Cookie{Name: "session_token", Value: sessionToken})
	router.ServeHTTP(w, req)

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

	session, ok := body["session"].(map[string]any)
	if !ok {
		t.Fatalf("expected session object in response, got %#v", body["session"])
	}

	user, ok := session["user"].(map[string]any)
	if !ok {
		t.Fatalf("expected session.user object, got %#v", session["user"])
	}

	if got, ok := user["email"].(string); !ok || got != "demo@example.com" {
		t.Fatalf("expected session.user.email=demo@example.com, got %#v", user["email"])
	}
}

func TestSSRFetchRejectsMissingOriginWithoutFlagHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SSR_FETCH_TOKEN", "")

	oldEngine := SsrEngine
	defer func() {
		SsrEngine = oldEngine
	}()

	SsrEngine = gin.New()
	SsrEngine.Use(gin.Recovery())
	SsrEngine.GET("/guard-demo", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	router := gin.New()
	registerSSRFetchRoutes(router)

	t.Run("missing origin and missing header", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, DefaultSSRFetchPrefix+"/guard-demo", nil)
		req.Host = "127.0.0.1:8080"
		router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("missing origin but explicit header", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, DefaultSSRFetchPrefix+"/guard-demo", nil)
		req.Host = "127.0.0.1:8080"
		req.Header.Set("X-SSR-Fetch", "1")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("same origin without explicit header", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, DefaultSSRFetchPrefix+"/guard-demo", nil)
		req.Host = "127.0.0.1:8080"
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestRegisterSSRFetchRoutesFetcherForwardsCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SSR_FETCH_TOKEN", "")

	oldEngine := SsrEngine
	defer func() {
		SsrEngine = oldEngine
	}()

	SsrEngine = gin.New()
	SsrEngine.Use(gin.Recovery())
	SsrEngine.GET("/cookie-demo", func(c *gin.Context) {
		token, _ := c.Cookie("session_token")
		c.JSON(http.StatusOK, gin.H{
			"handlerToken": token,
		})
	})

	router := gin.New()
	fetcher := registerSSRFetchRoutes(router)

	const token = "session-token-xyz"
	req := httptest.NewRequest(http.MethodGet, "/cookie-demo?from=server", nil)
	req.Host = "127.0.0.1:8080"
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})

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

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	router.ServeHTTP(w, req)

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

	router := gin.New()
	router.GET("/boom", WrapSSR(func(*gin.Context) (SSRPayload, error) {
		return nil, errors.New("db: secret leaked")
	}))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	router.ServeHTTP(w, req)

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
	req.AddCookie(&http.Cookie{Name: "session_token", Value: "signed-ok"})
	session := sessionStateFromRequest(req)
	if session == nil {
		t.Fatal("expected custom parser to return session")
	}

	reqInvalid := httptest.NewRequest(http.MethodGet, "/", nil)
	reqInvalid.AddCookie(&http.Cookie{Name: "session_token", Value: "bad-token"})
	if session := sessionStateFromRequest(reqInvalid); session != nil {
		t.Fatalf("expected invalid token to be rejected, got %#v", session)
	}
}

func TestSessionTokenParserResetToDefault(t *testing.T) {
	SetSessionTokenParser(nil)

	raw, err := json.Marshal(map[string]any{
		"id":       "u1",
		"name":     "Tester",
		"email":    "tester@example.com",
		"provider": "test",
		"iat":      1700000000,
	})
	if err != nil {
		t.Fatalf("marshal payload failed: %v", err)
	}
	token := base64.StdEncoding.EncodeToString(raw)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	session := sessionStateFromRequest(req)
	if session == nil {
		t.Fatal("expected default parser to parse base64 JSON session")
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
		{name: "locale svg", path: "/en/logo.svg", want: true},
		{name: "root page", path: "/", want: false},
		{name: "locale root", path: "/zh", want: false},
		{name: "normal route", path: "/slow-ssr", want: false},
		{name: "dot in middle segment", path: "/a.b/c", want: false},
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
