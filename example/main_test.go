package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestAccessLogEnabledOverride(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "enabled", raw: "1", want: true},
		{name: "enabled case insensitive", raw: " YES ", want: true},
		{name: "disabled", raw: "0", want: false},
		{name: "unknown is disabled", raw: "verbose", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HTTP_ACCESS_LOG", tt.raw)
			if got := accessLogEnabled(); got != tt.want {
				t.Fatalf("accessLogEnabled()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestDemoSessionStoreExpiresAndBoundsEntries(t *testing.T) {
	now := time.Unix(10_000, 0)
	store := newDemoSessionStore(2, time.Hour)
	store.store("expired", demoSession{IssuedAt: now.Add(-2 * time.Hour).Unix()}, now)
	store.store("first", demoSession{IssuedAt: now.Add(-time.Minute).Unix()}, now)
	store.store("second", demoSession{IssuedAt: now.Unix()}, now)

	if _, ok := store.load("expired", now); ok {
		t.Fatal("expired session remains available")
	}

	store.store("third", demoSession{IssuedAt: now.Add(time.Minute).Unix()}, now.Add(time.Minute))
	if _, ok := store.load("first", now.Add(time.Minute)); ok {
		t.Fatal("oldest session was not evicted at capacity")
	}
	if _, ok := store.load("second", now.Add(time.Minute)); !ok {
		t.Fatal("newer session was unexpectedly evicted")
	}
	if _, ok := store.load("third", now.Add(time.Minute)); !ok {
		t.Fatal("new session was not stored")
	}
}

func TestSanitizeNextPath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "local path", raw: "/protected?tab=profile#details", want: "/protected?tab=profile#details"},
		{name: "absolute URL", raw: "https://evil.example/path", want: "/fallback"},
		{name: "scheme relative URL", raw: "//evil.example/path", want: "/fallback"},
		{name: "backslash authority", raw: `/\evil.example/path`, want: "/fallback"},
		{name: "relative path", raw: "protected", want: "/fallback"},
		{name: "empty", raw: "  ", want: "/fallback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeNextPath(tt.raw, "/fallback"); got != tt.want {
				t.Fatalf("sanitizeNextPath(%q)=%q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestSessionDemoRoutesRequireSameOriginPost(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerSessionDemoRoutes(router)

	request := func(method, origin string) *httptest.ResponseRecorder {
		form := url.Values{"next": {"/protected"}}
		req := httptest.NewRequest(method, "http://example.com/demo/session/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	if w := request(http.MethodGet, "http://example.com"); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("state-changing GET should not exist, got %d", w.Code)
	}
	if w := request(http.MethodPost, "https://evil.example"); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login should be forbidden, got %d", w.Code)
	}
	w := request(http.MethodPost, "http://example.com")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/protected" {
		t.Fatalf("same-origin login failed: status=%d location=%q", w.Code, w.Header().Get("Location"))
	}
	if cookie := w.Header().Get("Set-Cookie"); !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Lax") {
		t.Fatalf("session cookie lacks security attributes: %q", cookie)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "http://example.com/demo/session/status", nil)
	for _, cookie := range w.Result().Cookies() {
		statusReq.AddCookie(cookie)
	}
	statusW := httptest.NewRecorder()
	router.ServeHTTP(statusW, statusReq)
	if statusW.Code != http.StatusOK || !strings.Contains(statusW.Body.String(), `"authenticated":true`) {
		t.Fatalf("session status did not recognize login: status=%d body=%s", statusW.Code, statusW.Body.String())
	}
	if !strings.Contains(statusW.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("session status is cacheable: %q", statusW.Header().Get("Cache-Control"))
	}
}
