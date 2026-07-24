package gossr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func mountTypedTestSSR(
	t *testing.T,
	router *gin.Engine,
	resolver PageResolver,
	rendererFn testRenderer,
	mutate func(*Options),
) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	options := Options{
		PageResolver: resolver,
		RendererFactory: func(string) renderer.Renderer {
			return rendererFn
		},
	}
	if mutate != nil {
		mutate(&options)
	}
	if err := SsrWithOptions(router, typedTestDist(), options); err != nil {
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
		func(options *Options) {
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
		func(options *Options) {
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
			case "/missing", "/conflict":
				return PageResult{Status: http.StatusNotFound, Payload: mapPayload{"page": "missing"}}, nil
			case "/go":
				return PageResult{Redirect: &Redirect{Status: http.StatusSeeOther, Location: "/login"}}, nil
			default:
				return PageResult{Payload: mapPayload{"page": "ok"}}, nil
			}
		},
		testRenderer(func(_ context.Context, urlPath string, _ map[string]any) (renderer.Result, error) {
			renders.Add(1)
			switch urlPath {
			case "/render-go":
				return renderer.Result{
					Redirect: &renderer.Redirect{Status: http.StatusTemporaryRedirect, Location: "/login?from=ssr"},
				}, nil
			case "/conflict":
				return renderer.Result{HTML: "<main>conflict</main>", Status: http.StatusInternalServerError}, nil
			default:
				return renderer.Result{HTML: "<main>page</main>"}, nil
			}
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

	rendererRedirect := htmlRequest(router, http.MethodGet, "/render-go", nil)
	if rendererRedirect.Code != http.StatusTemporaryRedirect ||
		rendererRedirect.Header().Get("Location") != "/login?from=ssr" {
		t.Fatalf("renderer redirect status=%d location=%q", rendererRedirect.Code, rendererRedirect.Header().Get("Location"))
	}

	conflict := htmlRequest(router, http.MethodGet, "/conflict", nil)
	if conflict.Code != http.StatusInternalServerError ||
		!strings.Contains(conflict.Body.String(), `name="ssr-error-id"`) ||
		strings.Contains(conflict.Body.String(), `data-ssr="true"`) {
		t.Fatalf("conflict did not safely fall back: status=%d body=%s", conflict.Code, conflict.Body.String())
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

	// /go is resolved twice but never rendered.
	if got := renders.Load(); got != 3 {
		t.Fatalf("renderer calls=%d, want 3", got)
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

func TestPageResolverRejectsLegacyDataAndSessionBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")
	resolver := PageResolver(func(context.Context, PageRequest) (PageResult, error) {
		return PageResult{}, nil
	})

	tests := []struct {
		name    string
		options Options
		want    string
	}{
		{
			name: "data engine",
			options: Options{
				PageResolver: resolver,
				DataEngine:   NewDataEngine(),
			},
			want: "PageResolver and DataEngine",
		},
		{
			name: "session resolver",
			options: Options{
				PageResolver: resolver,
				SessionResolver: func(context.Context, *http.Request) (map[string]any, error) {
					return nil, nil
				},
			},
			want: "PageResolver and SessionResolver",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := SsrWithOptions(gin.New(), typedTestDist(), testCase.options)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error=%v, want %q", err, testCase.want)
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
