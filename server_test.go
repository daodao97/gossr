package gossr

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/daodao97/gossr/renderer"
)

type testRenderer func(urlPath string, payload map[string]any) (renderer.Result, error)

func (f testRenderer) Render(urlPath string, payload map[string]any) (renderer.Result, error) {
	return f(urlPath, payload)
}

func TestRenderWithTimeoutReleasesSemaphoreAfterTimeout(t *testing.T) {
	sem := make(chan struct{}, 1)

	slowRenderer := testRenderer(func(_ string, _ map[string]any) (renderer.Result, error) {
		time.Sleep(80 * time.Millisecond)
		return renderer.Result{HTML: "slow"}, nil
	})

	_, err := renderWithTimeout(slowRenderer, "/", nil, 15*time.Millisecond, sem)
	if err == nil || !strings.Contains(err.Error(), "render timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}

	time.Sleep(120 * time.Millisecond)

	fastRenderer := testRenderer(func(_ string, _ map[string]any) (renderer.Result, error) {
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
	rendererFn := testRenderer(func(_ string, _ map[string]any) (renderer.Result, error) {
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
