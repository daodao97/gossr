package gossr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/daodao97/gossr/renderer"
	"github.com/gin-gonic/gin"
)

func typedTestDist() fstest.MapFS {
	return fstest.MapFS{
		"dist/client/index.html": {
			Data: []byte(`<!doctype html><html lang="zh-CN"><head></head><body><div id="app"><!--app-html--></div></body></html>`),
		},
		"dist/client/assets/app.js": {Data: []byte("app")},
		"dist/server/server.js":     {Data: []byte("external renderer input")},
	}
}

type typedTestOptions struct {
	SiteOrigin           string
	ExcludedPathPrefixes []string
	OnPageEvent          func(PageEvent)
}

func mountTypedTestSSR(
	t *testing.T,
	router *gin.Engine,
	resolver PageResolver,
	rendererFn testRenderer,
	mutate func(*typedTestOptions),
) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	options := typedTestOptions{}
	if mutate != nil {
		mutate(&options)
	}
	runtime, err := New(Config{
		Bundle:     typedTestDist(),
		Mode:       ModeProduction,
		SiteOrigin: options.SiteOrigin,
		RendererFactory: func(string) renderer.Renderer {
			return rendererFn
		},
		OnPageEvent: options.OnPageEvent,
	})
	if err != nil {
		t.Fatalf("create typed SSR runtime: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close typed SSR runtime: %v", err)
		}
	})
	if err := runtime.MountGin(router, GinOptions{
		Resolver:             resolver,
		ExcludedPathPrefixes: options.ExcludedPathPrefixes,
	}); err != nil {
		t.Fatalf("mount typed SSR: %v", err)
	}
}

func htmlRequest(handler http.Handler, method, target string, setup func(*http.Request)) *httptest.ResponseRecorder {
	return performRequest(handler, method, target, func(req *http.Request) {
		req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9")
		if setup != nil {
			setup(req)
		}
	})
}

func navigationRequest(handler http.Handler, target string, setup func(*http.Request)) *httptest.ResponseRecorder {
	return performRequest(handler, http.MethodGet, DefaultSSRDataRoute+target, func(req *http.Request) {
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if setup != nil {
			setup(req)
		}
	})
}

func TestTypedDocumentOnlyOverridesExplicitURLLocale(t *testing.T) {
	router := gin.New()
	mountTypedTestSSR(
		t,
		router,
		func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{Payload: mapPayload{"viewer": nil}}, nil
		},
		testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
			return renderer.Result{HTML: "<main>page</main>"}, nil
		}),
		nil,
	)

	implicit := htmlRequest(router, http.MethodGet, "/dashboard", nil)
	if !strings.Contains(implicit.Body.String(), `<html lang="zh-CN">`) {
		t.Fatalf("typed document replaced template language: %s", implicit.Body.String())
	}
	explicit := htmlRequest(router, http.MethodGet, "/en/dashboard", nil)
	if !strings.Contains(explicit.Body.String(), `<html lang="en">`) {
		t.Fatalf("typed document ignored explicit URL locale: %s", explicit.Body.String())
	}
}

func TestPageResolverIsSharedByDocumentAndNavigation(t *testing.T) {
	var calls []PageRequest
	resolver := func(_ context.Context, request PageRequest) (PageResult, error) {
		calls = append(calls, request)
		if request.SiteOrigin != "https://canonical.example" {
			t.Fatalf("SiteOrigin=%q", request.SiteOrigin)
		}
		return PageResult{
			Payload: mapPayload{
				"schema_version": 1,
				"url":            targetRequestURI(request),
			},
		}, nil
	}
	router := gin.New()
	mountTypedTestSSR(
		t,
		router,
		resolver,
		testRenderer(func(_ context.Context, urlPath string, payload map[string]any) (renderer.Result, error) {
			return renderer.Result{HTML: "<main>" + urlPath + ":" + payload["url"].(string) + "</main>"}, nil
		}),
		func(options *typedTestOptions) {
			options.SiteOrigin = "https://canonical.example"
		},
	)

	document := htmlRequest(router, http.MethodGet, "/dashboard?tab=usage", nil)
	if document.Code != http.StatusOK {
		t.Fatalf("document status=%d body=%s", document.Code, document.Body.String())
	}
	body := document.Body.String()
	if !strings.Contains(body, `id="__GOSSR_BOOT__" type="application/json"`) ||
		!strings.Contains(body, `id="app" data-ssr="true"`) ||
		strings.Contains(body, "window.__SSR_DATA__") {
		t.Fatalf("document did not use inert v2 boot contract: %s", body)
	}

	navigation := navigationRequest(router, "/dashboard?tab=usage", nil)
	if navigation.Code != http.StatusOK {
		t.Fatalf("navigation status=%d body=%s", navigation.Code, navigation.Body.String())
	}
	var outcome navigationRenderOutcome
	if err := json.Unmarshal(navigation.Body.Bytes(), &outcome); err != nil {
		t.Fatalf("decode navigation outcome: %v", err)
	}
	if outcome.Kind != "render" || outcome.Status != http.StatusOK ||
		outcome.Snapshot["url"] != "/dashboard?tab=usage" {
		t.Fatalf("unexpected navigation outcome: %#v", outcome)
	}

	if len(calls) != 2 {
		t.Fatalf("resolver calls=%d, want 2", len(calls))
	}
	if calls[0].Kind != PageRequestDocument ||
		calls[0].Source.URL.Path != "/dashboard" ||
		targetRequestURI(calls[0]) != "/dashboard?tab=usage" {
		t.Fatalf("unexpected document request: %#v", calls[0])
	}
	if calls[1].Kind != PageRequestNavigation ||
		calls[1].Source.URL.Path != DefaultSSRDataRoute+"/dashboard" ||
		targetRequestURI(calls[1]) != "/dashboard?tab=usage" {
		t.Fatalf("unexpected navigation request: %#v", calls[1])
	}
}

func TestTypedDocumentFallbackOnlyHandlesHTMLDocuments(t *testing.T) {
	var calls atomic.Int32
	router := gin.New()
	mountTypedTestSSR(
		t,
		router,
		func(context.Context, PageRequest) (PageResult, error) {
			calls.Add(1)
			return PageResult{Payload: mapPayload{"ok": true}}, nil
		},
		testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
			return renderer.Result{HTML: "<main>ok</main>"}, nil
		}),
		func(options *typedTestOptions) {
			options.ExcludedPathPrefixes = []string{"/api", "/backend", "/admin"}
		},
	)

	tests := []struct {
		name   string
		method string
		target string
		accept string
	}{
		{name: "missing accept", method: http.MethodGet, target: "/page"},
		{name: "generic accept", method: http.MethodGet, target: "/page", accept: "*/*"},
		{name: "json accept", method: http.MethodGet, target: "/page", accept: "application/json"},
		{name: "post", method: http.MethodPost, target: "/page", accept: "text/html"},
		{name: "backend namespace", method: http.MethodGet, target: "/backend/missing", accept: "text/html"},
		{name: "api namespace boundary", method: http.MethodGet, target: "/api/missing", accept: "text/html"},
		{name: "assets namespace", method: http.MethodGet, target: "/assets/missing", accept: "text/html"},
		{name: "html disabled", method: http.MethodGet, target: "/page", accept: "text/html;q=0"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			response := performRequest(router, testCase.method, testCase.target, func(req *http.Request) {
				if testCase.accept != "" {
					req.Header.Set("Accept", testCase.accept)
				}
			})
			if response.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}

	allowed := htmlRequest(router, http.MethodGet, "/apiary", nil)
	if allowed.Code != http.StatusOK {
		t.Fatalf("prefix boundary rejected /apiary: %d", allowed.Code)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("resolver calls=%d, want 1", got)
	}
}

func TestTypedPageStatusAndRedirectOutcomes(t *testing.T) {
	var renders atomic.Int32
	router := gin.New()
	mountTypedTestSSR(
		t,
		router,
		func(_ context.Context, request PageRequest) (PageResult, error) {
			switch request.URL.Path {
			case "/missing":
				return PageResult{Status: http.StatusNotFound, Payload: mapPayload{"page": "missing"}}, nil
			case "/go":
				return PageResult{Redirect: &Redirect{Status: http.StatusSeeOther, Location: "/login"}}, nil
			default:
				return PageResult{Payload: mapPayload{"page": "ok"}}, nil
			}
		},
		testRenderer(func(_ context.Context, _ string, _ map[string]any) (renderer.Result, error) {
			renders.Add(1)
			return renderer.Result{HTML: "<main>page</main>"}, nil
		}),
		nil,
	)

	missing := htmlRequest(router, http.MethodGet, "/missing", nil)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "<main>page</main>") {
		t.Fatalf("missing status=%d body=%s", missing.Code, missing.Body.String())
	}

	resolverRedirect := htmlRequest(router, http.MethodGet, "/go", nil)
	if resolverRedirect.Code != http.StatusSeeOther || resolverRedirect.Header().Get("Location") != "/login" {
		t.Fatalf("resolver redirect status=%d location=%q", resolverRedirect.Code, resolverRedirect.Header().Get("Location"))
	}

	navigation := navigationRequest(router, "/go", nil)
	if navigation.Code != http.StatusOK {
		t.Fatalf("redirect navigation status=%d body=%s", navigation.Code, navigation.Body.String())
	}
	var redirect navigationRedirectOutcome
	if err := json.Unmarshal(navigation.Body.Bytes(), &redirect); err != nil {
		t.Fatalf("decode redirect: %v", err)
	}
	if redirect.Kind != "redirect" || redirect.Status != http.StatusSeeOther || redirect.Location != "/login" {
		t.Fatalf("unexpected redirect outcome: %#v", redirect)
	}

	// /go is resolved twice but never rendered; only /missing renders.
	if got := renders.Load(); got != 1 {
		t.Fatalf("renderer calls=%d, want 1", got)
	}
}

func TestTypedRendererFailureFallsBackWithResolvedStatusAndInertState(t *testing.T) {
	router := gin.New()
	mountTypedTestSSR(
		t,
		router,
		func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{
				Status: http.StatusNotFound,
				Payload: mapPayload{
					"danger": "</script><script>alert(1)</script>\u2028",
				},
			}, nil
		},
		testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
			return renderer.Result{}, errors.New("render failed")
		}),
		nil,
	)

	response := htmlRequest(router, http.MethodGet, "/missing", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `name="ssr-error-id"`) ||
		!strings.Contains(body, `id="__GOSSR_BOOT__"`) ||
		strings.Contains(body, `data-ssr="true"`) ||
		strings.Contains(body, "</script><script>alert(1)") ||
		!strings.Contains(body, `\u003c/script\u003e`) {
		t.Fatalf("unsafe fallback body: %s", body)
	}
	assertNoCacheHeaders(t, response.Header())
}

func TestTypedDocumentHeadHasStatusAndNoBody(t *testing.T) {
	router := gin.New()
	mountTypedTestSSR(
		t,
		router,
		func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{Status: http.StatusNotFound, Payload: mapPayload{}}, nil
		},
		testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
			return renderer.Result{HTML: "<main>not found</main>"}, nil
		}),
		nil,
	)

	response := htmlRequest(router, http.MethodHead, "/missing", nil)
	if response.Code != http.StatusNotFound || response.Body.Len() != 0 {
		t.Fatalf("HEAD status=%d body=%q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("HEAD content type=%q", got)
	}
}

type pagePrincipalKey struct{}

func TestHostMiddlewareMutationsUseRealDocumentAndNavigationResponses(t *testing.T) {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if cookie, err := c.Request.Cookie("session"); err == nil && cookie.Value == "invalid" {
			c.SetSameSite(http.SameSiteLaxMode)
			c.SetCookie("session", "", -1, "/", "", false, true)
			ctx := context.WithValue(c.Request.Context(), pagePrincipalKey{}, "guest")
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	})
	mountTypedTestSSR(
		t,
		router,
		func(_ context.Context, request PageRequest) (PageResult, error) {
			if request.Source.Context().Value(pagePrincipalKey{}) != "guest" {
				return PageResult{}, errors.New("verified middleware context missing")
			}
			return PageResult{Payload: mapPayload{"viewer": nil}}, nil
		},
		testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
			return renderer.Result{HTML: "<main>guest</main>"}, nil
		}),
		nil,
	)

	addInvalidCookie := func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: "session", Value: "invalid"})
	}
	document := htmlRequest(router, http.MethodGet, "/", addInvalidCookie)
	navigation := navigationRequest(router, "/", addInvalidCookie)
	for name, response := range map[string]*httptest.ResponseRecorder{
		"document":   document,
		"navigation": navigation,
	} {
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", name, response.Code, response.Body.String())
		}
		if setCookie := response.Header().Get("Set-Cookie"); !strings.Contains(setCookie, "session=") ||
			!strings.Contains(setCookie, "Max-Age=0") {
			t.Fatalf("%s did not preserve middleware cookie mutation: %q", name, setCookie)
		}
	}
}

func TestTypedResolverCookiesUseRealDocumentAndNavigationResponses(t *testing.T) {
	router := gin.New()
	mountTypedTestSSR(
		t,
		router,
		func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{
				Payload: mapPayload{"viewer": nil},
				Cookies: []*http.Cookie{{
					Name:     "session",
					Path:     "/",
					MaxAge:   -1,
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
				}},
			}, nil
		},
		testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
			return renderer.Result{HTML: "<main>guest</main>"}, nil
		}),
		nil,
	)

	for name, response := range map[string]*httptest.ResponseRecorder{
		"document":   htmlRequest(router, http.MethodGet, "/", nil),
		"navigation": navigationRequest(router, "/", nil),
	} {
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", name, response.Code, response.Body.String())
		}
		if setCookie := response.Header().Get("Set-Cookie"); !strings.Contains(setCookie, "session=") ||
			!strings.Contains(setCookie, "Max-Age=0") ||
			!strings.Contains(setCookie, "HttpOnly") {
			t.Fatalf("%s did not write resolver cookie: %q", name, setCookie)
		}
	}
}

func TestTypedResolverRejectsUnsafeOutcomes(t *testing.T) {
	tests := []struct {
		name   string
		result PageResult
	}{
		{
			name:   "external redirect",
			result: PageResult{Redirect: &Redirect{Status: http.StatusFound, Location: "https://attacker.example"}},
		},
		{
			name:   "protocol relative redirect",
			result: PageResult{Redirect: &Redirect{Status: http.StatusFound, Location: "//attacker.example"}},
		},
		{
			name:   "header injection",
			result: PageResult{Redirect: &Redirect{Status: http.StatusFound, Location: "/ok\r\nX-Evil: 1"}},
		},
		{
			name:   "redirect status without location",
			result: PageResult{Status: http.StatusFound},
		},
		{
			name: "invalid cookie",
			result: PageResult{
				Cookies: []*http.Cookie{{Name: "bad\r\ncookie"}},
			},
		},
		{
			name: "nil cookie",
			result: PageResult{
				Cookies: []*http.Cookie{nil},
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			router := gin.New()
			mountTypedTestSSR(
				t,
				router,
				func(context.Context, PageRequest) (PageResult, error) {
					return testCase.result, nil
				},
				testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
					return renderer.Result{HTML: "must not render"}, nil
				}),
				nil,
			)
			response := htmlRequest(router, http.MethodGet, "/", nil)
			if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "must not render") {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestNavigationResolverErrorUsesTypedNoStoreResponse(t *testing.T) {
	router := gin.New()
	mountTypedTestSSR(
		t,
		router,
		func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{}, errors.New("database details must stay private")
		},
		testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
			return renderer.Result{HTML: "must not render"}, nil
		}),
		nil,
	)

	response := navigationRequest(router, "/private", nil)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var outcome navigationErrorOutcome
	if err := json.Unmarshal(response.Body.Bytes(), &outcome); err != nil {
		t.Fatalf("decode navigation error: %v", err)
	}
	if outcome.Kind != "error" ||
		outcome.Code != "resolve_failed" ||
		strings.Contains(response.Body.String(), "database details") {
		t.Fatalf("unsafe navigation error: %#v body=%s", outcome, response.Body.String())
	}
	assertNoCacheHeaders(t, response.Header())
}

func TestDocumentHEADSkipsRendering(t *testing.T) {
	rendered := 0
	router := gin.New()
	mountTypedTestSSR(
		t,
		router,
		func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{Payload: mapPayload{}}, nil
		},
		testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
			rendered++
			return renderer.Result{HTML: "<main>body</main>"}, nil
		}),
		nil,
	)

	response := htmlRequest(router, http.MethodHead, "/", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("HEAD status=%d", response.Code)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("HEAD carried a body: %q", response.Body.String())
	}
	if rendered != 0 {
		t.Fatalf("HEAD triggered %d SSR renders, want 0", rendered)
	}
}

func TestOnPageEventReportsDocumentAndNavigationOutcomes(t *testing.T) {
	var mu sync.Mutex
	var events []PageEvent
	router := gin.New()
	mountTypedTestSSR(
		t,
		router,
		func(_ context.Context, request PageRequest) (PageResult, error) {
			switch request.URL.Path {
			case "/go":
				return PageResult{Redirect: &Redirect{Status: http.StatusSeeOther, Location: "/login"}}, nil
			case "/boom":
				return PageResult{}, errors.New("resolver exploded")
			default:
				return PageResult{Payload: mapPayload{"ok": true}}, nil
			}
		},
		testRenderer(func(_ context.Context, urlPath string, _ map[string]any) (renderer.Result, error) {
			if urlPath == "/render-fail" {
				return renderer.Result{}, errors.New("render failed")
			}
			return renderer.Result{HTML: "<main>ok</main>"}, nil
		}),
		func(options *typedTestOptions) {
			options.OnPageEvent = func(event PageEvent) {
				mu.Lock()
				events = append(events, event)
				mu.Unlock()
				// 宿主回调 panic 不得影响请求处理。
				panic("observer must not break requests")
			}
		},
	)

	htmlRequest(router, http.MethodGet, "/", nil)
	htmlRequest(router, http.MethodGet, "/go", nil)
	htmlRequest(router, http.MethodGet, "/boom", nil)
	htmlRequest(router, http.MethodGet, "/render-fail", nil)
	navigationRequest(router, "/", nil)
	navigationRequest(router, "/go", nil)

	mu.Lock()
	defer mu.Unlock()
	want := []struct {
		kind, outcome string
		status        int
		rendered      bool
	}{
		{kind: "document", outcome: "ok", status: http.StatusOK, rendered: true},
		{kind: "document", outcome: "redirect", status: http.StatusSeeOther},
		{kind: "document", outcome: "resolver_error", status: http.StatusInternalServerError},
		{kind: "document", outcome: "fallback", status: http.StatusOK, rendered: true},
		{kind: "navigation", outcome: "ok", status: http.StatusOK},
		{kind: "navigation", outcome: "redirect", status: http.StatusOK},
	}
	if len(events) != len(want) {
		t.Fatalf("events=%d, want %d: %#v", len(events), len(want), events)
	}
	for index, expected := range want {
		got := events[index]
		if got.Kind != expected.kind || got.Outcome != expected.outcome || got.Status != expected.status {
			t.Fatalf("event[%d]=%#v, want %+v", index, got, expected)
		}
		if got.Duration <= 0 {
			t.Fatalf("event[%d] has no duration: %#v", index, got)
		}
		if expected.rendered != (got.Render > 0) {
			t.Fatalf("event[%d] render duration=%v, want rendered=%t", index, got.Render, expected.rendered)
		}
	}
}

func TestDocumentResolverFailureServesCSRShell(t *testing.T) {
	router := gin.New()
	mountTypedTestSSR(
		t,
		router,
		func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{}, context.DeadlineExceeded
		},
		testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
			t.Fatal("renderer must not run when the resolver fails")
			return renderer.Result{}, nil
		}),
		nil,
	)

	response := htmlRequest(router, http.MethodGet, "/dashboard/usage_log", nil)
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d, want 504", response.Code)
	}
	body := response.Body.String()
	// 白屏是不可接受的失败形态:超时也要下发可自愈的 CSR 壳。
	if !strings.Contains(body, `id="app"`) ||
		strings.Contains(body, `data-ssr`) ||
		strings.Contains(body, `<script id="__GOSSR_BOOT__" type="application/json">`) ||
		!strings.Contains(body, `name="ssr-error-id"`) {
		t.Fatalf("resolver failure did not serve the CSR shell: %s", body)
	}
	if got := response.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("shell must not be cacheable: %q", got)
	}
}
