package gossr

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/daodao97/gossr/renderer"
	"github.com/gin-gonic/gin"
)

type runtimeBarrierRenderer struct {
	entered          chan struct{}
	exited           chan struct{}
	enterOnce        sync.Once
	exitOnce         sync.Once
	closeCalls       atomic.Int32
	closedBeforeExit atomic.Bool
	closeErr         error
}

func (r *runtimeBarrierRenderer) Render(
	ctx context.Context,
	_ string,
	_ map[string]any,
) (renderer.Result, error) {
	r.enterOnce.Do(func() { close(r.entered) })
	<-ctx.Done()
	r.exitOnce.Do(func() { close(r.exited) })
	return renderer.Result{}, ctx.Err()
}

func (r *runtimeBarrierRenderer) Close() error {
	select {
	case <-r.exited:
	default:
		r.closedBeforeExit.Store(true)
	}
	r.closeCalls.Add(1)
	return r.closeErr
}

func validRuntimeDist() fstest.MapFS {
	return fstest.MapFS{
		"dist/client/index.html": {
			Data: []byte(`<!doctype html><html><head></head><body><div id="app"><!--app-html--></div></body></html>`),
		},
		"dist/client/assets/app.js": {Data: []byte("app")},
		"dist/server/server.js":     {Data: []byte("external renderer input")},
	}
}

func newTestRuntime(
	t *testing.T,
	instance renderer.Renderer,
	mutate func(*Config),
) *Runtime {
	t.Helper()
	config := Config{
		Bundle: validRuntimeDist(),
		Mode:   ModeProduction,
		RendererFactory: func(string) renderer.Renderer {
			return instance
		},
	}
	if mutate != nil {
		mutate(&config)
	}
	runtime, err := New(config)
	if err != nil {
		t.Fatalf("New runtime: %v", err)
	}
	return runtime
}

func TestRuntimeDefaultNavigationAuthorizerUsesConfiguredSiteOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testCases := []struct {
		name       string
		mode       Mode
		siteOrigin string
		devURL     string
		host       string
		header     string
		source     string
	}{
		{
			name:       "Vite proxy keeps browser origin",
			mode:       ModeDevelopment,
			siteOrigin: "http://127.0.0.1:4001",
			devURL:     "http://127.0.0.1:3333",
			host:       "127.0.0.1:4001",
			header:     "Referer",
			source:     "http://127.0.0.1:3333/dashboard",
		},
		{
			name:       "TLS terminator keeps public origin",
			siteOrigin: "https://app.example",
			host:       "app:4001",
			header:     "Origin",
			source:     "https://app.example",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := newTestRuntime(
				t,
				testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
					return renderer.Result{HTML: "<main>ok</main>"}, nil
				}),
				func(config *Config) {
					if testCase.mode != 0 {
						config.Mode = testCase.mode
					}
					config.SiteOrigin = testCase.siteOrigin
					config.DevServerURL = testCase.devURL
				},
			)
			t.Cleanup(func() {
				if err := runtime.Close(); err != nil {
					t.Errorf("Close runtime: %v", err)
				}
			})
			router := gin.New()
			if err := runtime.MountGin(router, GinOptions{
				Resolver: func(context.Context, PageRequest) (PageResult, error) {
					return PageResult{Payload: mapPayload{"ok": true}}, nil
				},
			}); err != nil {
				t.Fatalf("MountGin: %v", err)
			}

			allowed := performRequest(
				router,
				http.MethodGet,
				DefaultSSRDataRoute+"/dashboard",
				func(req *http.Request) {
					req.Host = testCase.host
					req.Header.Set(testCase.header, testCase.source)
				},
			)
			if allowed.Code != http.StatusOK {
				t.Fatalf(
					"configured public origin was rejected: status=%d body=%s",
					allowed.Code,
					allowed.Body.String(),
				)
			}

			rejected := performRequest(
				router,
				http.MethodGet,
				DefaultSSRDataRoute+"/dashboard",
				func(req *http.Request) {
					req.Host = testCase.host
					req.Header.Set("Origin", "https://cross-origin.example")
				},
			)
			if rejected.Code != http.StatusForbidden {
				t.Fatalf("cross-origin request status=%d, want 403", rejected.Code)
			}
		})
	}
}

func TestRuntimeCustomNavigationAuthorizerOverridesConfiguredOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	runtime := newTestRuntime(
		t,
		testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
			return renderer.Result{HTML: "<main>ok</main>"}, nil
		}),
		func(config *Config) {
			config.SiteOrigin = "https://app.example"
		},
	)
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("Close runtime: %v", err)
		}
	})
	router := gin.New()
	if err := runtime.MountGin(router, GinOptions{
		Resolver: func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{Payload: mapPayload{"ok": true}}, nil
		},
		SSRFetchAuthorizer: func(*http.Request) (int, bool) {
			return http.StatusUnauthorized, false
		},
	}); err != nil {
		t.Fatalf("MountGin: %v", err)
	}

	response := performRequest(
		router,
		http.MethodGet,
		DefaultSSRDataRoute+"/dashboard",
		func(req *http.Request) {
			req.Host = "internal:4001"
			req.Header.Set("Origin", "https://app.example")
		},
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("custom authorizer status=%d, want 401", response.Code)
	}
}

func TestRuntimeCloseCancelsAndDrainsBeforeClosingRenderer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	closeErr := errors.New("renderer close failed")
	instance := &runtimeBarrierRenderer{
		entered:  make(chan struct{}),
		exited:   make(chan struct{}),
		closeErr: closeErr,
	}
	runtime := newTestRuntime(t, instance, nil)
	router := gin.New()
	if err := runtime.MountGin(router, GinOptions{
		Resolver: func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{Payload: mapPayload{"ok": true}}, nil
		},
	}); err != nil {
		t.Fatalf("MountGin: %v", err)
	}

	requestDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		requestDone <- htmlRequest(router, http.MethodGet, "/slow", nil)
	}()
	select {
	case <-instance.entered:
	case <-time.After(time.Second):
		t.Fatal("request never entered renderer")
	}

	const closeCallers = 4
	results := make(chan error, closeCallers)
	for range closeCallers {
		go func() {
			results <- runtime.Close()
		}()
	}
	for range closeCallers {
		if err := <-results; !errors.Is(err, closeErr) {
			t.Fatalf("Close error=%v, want %v", err, closeErr)
		}
	}

	select {
	case <-instance.exited:
	default:
		t.Fatal("Close returned before active renderer exited")
	}
	if instance.closedBeforeExit.Load() {
		t.Fatal("renderer closed before active render exited")
	}
	if got := instance.closeCalls.Load(); got != 1 {
		t.Fatalf("renderer Close calls=%d, want 1", got)
	}
	if err := runtime.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("repeated Close error=%v, want %v", err, closeErr)
	}

	select {
	case response := <-requestDone:
		if response.Code != http.StatusOK {
			t.Fatalf("canceled render fallback status=%d body=%s", response.Code, response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("request did not finish after runtime cancellation")
	}

	document := htmlRequest(router, http.MethodGet, "/after-close", nil)
	if document.Code != http.StatusServiceUnavailable {
		t.Fatalf("document after close status=%d", document.Code)
	}
	navigation := navigationRequest(router, "/after-close", nil)
	if navigation.Code != http.StatusServiceUnavailable ||
		!strings.Contains(navigation.Body.String(), `"code":"service_unavailable"`) {
		t.Fatalf("navigation after close status=%d body=%s", navigation.Code, navigation.Body.String())
	}
}

func TestRuntimeCloseCancelsResolverBeforeClosingRenderer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resolverEntered := make(chan struct{})
	resolverExited := make(chan struct{})
	var closeCalls atomic.Int32
	var closedBeforeResolverExit atomic.Bool
	var renderCalls atomic.Int32
	instance := &testClosingRenderer{
		render: func(context.Context, string, map[string]any) (renderer.Result, error) {
			renderCalls.Add(1)
			return renderer.Result{}, nil
		},
		close: func() error {
			select {
			case <-resolverExited:
			default:
				closedBeforeResolverExit.Store(true)
			}
			closeCalls.Add(1)
			return nil
		},
	}
	runtime := newTestRuntime(t, instance, nil)
	router := gin.New()
	if err := runtime.MountGin(router, GinOptions{
		Resolver: func(ctx context.Context, _ PageRequest) (PageResult, error) {
			close(resolverEntered)
			<-ctx.Done()
			close(resolverExited)
			return PageResult{}, ctx.Err()
		},
	}); err != nil {
		t.Fatalf("MountGin: %v", err)
	}

	requestDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		requestDone <- htmlRequest(router, http.MethodGet, "/resolver", nil)
	}()
	select {
	case <-resolverEntered:
	case <-time.After(time.Second):
		t.Fatal("request never entered resolver")
	}

	if err := runtime.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closedBeforeResolverExit.Load() {
		t.Fatal("renderer closed before active resolver exited")
	}
	if got := closeCalls.Load(); got != 1 {
		t.Fatalf("renderer Close calls=%d, want 1", got)
	}
	if got := renderCalls.Load(); got != 0 {
		t.Fatalf("renderer calls=%d, want 0", got)
	}

	select {
	case response := <-requestDone:
		if response.Code != http.StatusRequestTimeout {
			t.Fatalf("canceled resolver status=%d body=%s", response.Code, response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("resolver request did not finish after runtime cancellation")
	}
}

func TestRuntimeCloseCancelsQueuedAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var closeCalls atomic.Int32
	instance := &testClosingRenderer{
		render: func(context.Context, string, map[string]any) (renderer.Result, error) {
			return renderer.Result{}, nil
		},
		close: func() error {
			closeCalls.Add(1)
			return nil
		},
	}
	runtime := newTestRuntime(t, instance, func(config *Config) {
		config.MaxConcurrentPages = 1
	})
	if err := runtime.MountGin(gin.New(), GinOptions{
		Resolver: func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{Payload: mapPayload{}}, nil
		},
	}); err != nil {
		t.Fatalf("MountGin: %v", err)
	}

	// Occupy the only admission slot, then register a real Runtime request
	// before attempting to enter the queue. Close must cancel that queued
	// context and wait for its Runtime release before closing the renderer.
	_, releaseSlot, err := runtime.admission.enter(context.Background())
	if err != nil {
		t.Fatalf("occupy admission slot: %v", err)
	}
	defer releaseSlot()

	queuedContext, releaseRuntime, err := runtime.beginRequest(context.Background())
	if err != nil {
		t.Fatalf("begin queued request: %v", err)
	}
	queueResult := make(chan error, 1)
	go func() {
		_, releaseAdmission, enterErr := runtime.admission.enter(queuedContext)
		if enterErr == nil {
			releaseAdmission()
		}
		releaseRuntime()
		queueResult <- enterErr
	}()

	closeResult := make(chan error, 1)
	go func() {
		closeResult <- runtime.Close()
	}()

	select {
	case enterErr := <-queueResult:
		if !errors.Is(enterErr, context.Canceled) {
			t.Fatalf("queued admission error=%v, want context.Canceled", enterErr)
		}
	case <-time.After(time.Second):
		t.Fatal("queued admission did not observe runtime cancellation")
	}
	select {
	case closeErr := <-closeResult:
		if closeErr != nil {
			t.Fatalf("Close: %v", closeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not drain queued admission")
	}
	if got := closeCalls.Load(); got != 1 {
		t.Fatalf("renderer Close calls=%d, want 1", got)
	}
}

func TestRuntimeMountOnlySucceedsOnce(t *testing.T) {
	instance := testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
		return renderer.Result{HTML: "<main>ok</main>"}, nil
	})
	runtime := newTestRuntime(t, instance, nil)
	t.Cleanup(func() { _ = runtime.Close() })

	options := GinOptions{
		Resolver: func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{Payload: mapPayload{}}, nil
		},
	}
	if err := runtime.MountGin(gin.New(), options); err != nil {
		t.Fatalf("first MountGin: %v", err)
	}
	if err := runtime.MountGin(gin.New(), options); err == nil {
		t.Fatal("second MountGin succeeded")
	}
}

func TestRuntimeConcurrentMountAndCloseIsSafe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for iteration := range 32 {
		var closeCalls atomic.Int32
		instance := &testClosingRenderer{
			render: func(context.Context, string, map[string]any) (renderer.Result, error) {
				return renderer.Result{HTML: "<main>ok</main>"}, nil
			},
			close: func() error {
				closeCalls.Add(1)
				return nil
			},
		}
		runtime := newTestRuntime(t, instance, nil)
		options := GinOptions{
			Resolver: func(context.Context, PageRequest) (PageResult, error) {
				return PageResult{Payload: mapPayload{}}, nil
			},
		}

		start := make(chan struct{})
		mountResult := make(chan error, 1)
		closeResult := make(chan error, 1)
		go func() {
			<-start
			mountResult <- runtime.MountGin(gin.New(), options)
		}()
		go func() {
			<-start
			closeResult <- runtime.Close()
		}()
		close(start)

		if err := <-closeResult; err != nil {
			t.Fatalf("iteration %d Close: %v", iteration, err)
		}
		// Mount may win the lock and succeed, or observe the already-closed
		// state. Both outcomes are valid; neither may panic or race.
		_ = <-mountResult
		if err := runtime.Close(); err != nil {
			t.Fatalf("iteration %d repeated Close: %v", iteration, err)
		}
		if got := closeCalls.Load(); got != 1 {
			t.Fatalf("iteration %d renderer Close calls=%d, want 1", iteration, got)
		}
	}
}

func TestRuntimeMountRecoversRouteConflictAndClosesRenderer(t *testing.T) {
	closed := atomic.Int32{}
	instance := &testClosingRenderer{
		render: func(context.Context, string, map[string]any) (renderer.Result, error) {
			return renderer.Result{HTML: "<main>ok</main>"}, nil
		},
		close: func() error {
			closed.Add(1)
			return nil
		},
	}
	runtime := newTestRuntime(t, instance, nil)
	router := gin.New()
	router.GET(DefaultSSRDataRoute, func(c *gin.Context) { c.Status(http.StatusNoContent) })

	err := runtime.MountGin(router, GinOptions{
		Resolver: func(context.Context, PageRequest) (PageResult, error) {
			return PageResult{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "mount gossr routes") {
		t.Fatalf("MountGin error=%v, want recovered route conflict", err)
	}
	if got := closed.Load(); got != 1 {
		t.Fatalf("renderer Close calls=%d, want 1", got)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close after mount failure: %v", err)
	}
	if got := closed.Load(); got != 1 {
		t.Fatalf("repeated Close calls=%d, want 1", got)
	}
}

func TestRuntimeCloseConvertsRendererClosePanicToStableError(t *testing.T) {
	instance := &testClosingRenderer{
		render: func(context.Context, string, map[string]any) (renderer.Result, error) {
			return renderer.Result{}, nil
		},
		close: func() error {
			panic("close exploded")
		},
	}
	runtime := newTestRuntime(t, instance, nil)
	first := runtime.Close()
	if first == nil || !strings.Contains(first.Error(), "close exploded") {
		t.Fatalf("Close error=%v, want recovered panic", first)
	}
	second := runtime.Close()
	if second == nil || second.Error() != first.Error() {
		t.Fatalf("repeated Close error=%v, want %v", second, first)
	}
}

type testClosingRenderer struct {
	render func(context.Context, string, map[string]any) (renderer.Result, error)
	close  func() error
}

func (r *testClosingRenderer) Render(
	ctx context.Context,
	target string,
	payload map[string]any,
) (renderer.Result, error) {
	return r.render(ctx, target, payload)
}

func (r *testClosingRenderer) Close() error {
	return r.close()
}

func TestRuntimeModeAndLimitValidation(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{name: "invalid mode", config: Config{Mode: Mode(99)}},
		{name: "negative page timeout", config: Config{Mode: ModeDevelopment, PageTimeout: -time.Second}},
		{name: "negative render timeout", config: Config{Mode: ModeDevelopment, RenderTimeout: -time.Second}},
		{name: "negative concurrency", config: Config{Mode: ModeDevelopment, MaxConcurrentPages: -1}},
		{name: "production bundle required", config: Config{Mode: ModeProduction}},
		{name: "invalid dev server", config: Config{Mode: ModeDevelopment, DevServerURL: "file:///tmp/dev"}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := New(testCase.config); err == nil {
				t.Fatalf("New(%#v) succeeded", testCase.config)
			}
		})
	}

	t.Run("auto development and concurrency clamp", func(t *testing.T) {
		t.Setenv("DEV_MODE", "1")
		runtime, err := New(Config{
			Mode:               ModeAuto,
			DevServerURL:       "http://127.0.0.1:3333",
			MaxConcurrentPages: maxSSRRenderLimit + 100,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if runtime.mode != ModeDevelopment {
			t.Fatalf("mode=%d, want development", runtime.mode)
		}
		if runtime.maxConcurrency != maxSSRRenderLimit {
			t.Fatalf("concurrency=%d, want %d", runtime.maxConcurrency, maxSSRRenderLimit)
		}
		if err := runtime.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
}

func TestValidateIndexTemplate(t *testing.T) {
	valid := `<!doctype html><html><body><div id="app"><!--app-html--></div></body></html>`
	if err := validateIndexTemplate(valid); err != nil {
		t.Fatalf("valid template failed: %v", err)
	}

	tests := []struct {
		name  string
		value string
	}{
		{name: "missing marker", value: `<div id="app"></div>`},
		{name: "duplicate marker", value: `<div id="app"><!--app-html--><!--app-html--></div>`},
		{name: "missing app", value: `<main><!--app-html--></main>`},
		{
			name:  "marker outside app",
			value: `<div id="app"></div><main><!--app-html--></main>`,
		},
		{
			name:  "marker nested below app",
			value: `<div id="app"><main><!--app-html--></main></div>`,
		},
		{
			name:  "marker only in script text",
			value: `<script>const marker = "<!--app-html-->"</script><div id="app"></div>`,
		},
		{name: "duplicate app", value: `<div id="app"></div><div id="app"><!--app-html--></div>`},
		{name: "duplicate id before app", value: `<div id="other" id="app"><!--app-html--></div>`},
		{name: "duplicate id after app", value: `<div id="app" id="other"><!--app-html--></div>`},
		{name: "pre-marked app", value: `<div id="app" data-ssr="true"><!--app-html--></div>`},
		{name: "pre-declared app marker", value: `<div data-ssr="false" id="app"><!--app-html--></div>`},
		{name: "duplicate data ssr", value: `<div id="app" data-ssr="false" data-ssr="true"><!--app-html--></div>`},
		{name: "boot state", value: `<div id="app"><!--app-html--></div><script id="__GOSSR_BOOT__"></script>`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if err := validateIndexTemplate(testCase.value); err == nil {
				t.Fatalf("template succeeded: %s", testCase.value)
			}
		})
	}
}

func TestStructuralSSRTemplateMutationIgnoresScriptText(t *testing.T) {
	indexHTML := `<!doctype html><html><head>` +
		`<script>const appSelector = '[id="app"]'; ` +
		`const bootSelector = '[id="__GOSSR_BOOT__"]'; ` +
		`const marker = "<!--app-html-->"</script>` +
		`</head><body><div class="shell" id='app'><!--app-html--></div></body></html>`
	if err := validateIndexTemplate(indexHTML); err != nil {
		t.Fatalf("validateIndexTemplate: %v", err)
	}

	marked, err := markSSRApp(indexHTML)
	if err != nil {
		t.Fatalf("markSSRApp: %v", err)
	}
	if !strings.Contains(marked, `<div class="shell" id='app' data-ssr="true">`) {
		t.Fatalf("real app element was not marked: %s", marked)
	}
	if !strings.Contains(marked, `const appSelector = '[id="app"]'`) {
		t.Fatalf("script text was changed: %s", marked)
	}

	rendered, err := replaceAppHTMLMarker(marked, `<main>SSR</main>`)
	if err != nil {
		t.Fatalf("replaceAppHTMLMarker: %v", err)
	}
	if !strings.Contains(rendered, `const marker = "<!--app-html-->"`) {
		t.Fatalf("script marker was changed: %s", rendered)
	}
	if !strings.Contains(rendered, `<div class="shell" id='app' data-ssr="true"><main>SSR</main></div>`) {
		t.Fatalf("structural marker was not replaced: %s", rendered)
	}

	fallback := buildPageFallback(indexHTML, nil, "", "")
	if !strings.Contains(fallback, `const marker = "<!--app-html-->"`) {
		t.Fatalf("fallback changed script marker: %s", fallback)
	}
	if strings.Contains(fallback, `<div class="shell" id='app'><!--app-html--></div>`) {
		t.Fatalf("fallback retained structural marker: %s", fallback)
	}

	injected, err := injectSSRBootData(rendered, map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("injectSSRBootData: %v", err)
	}
	hasBoot, err := documentHasElementID(injected, "__GOSSR_BOOT__")
	if err != nil {
		t.Fatalf("inspect injected document: %v", err)
	}
	if !hasBoot {
		t.Fatal("injected document has no structural boot element")
	}
}

func TestTypedRendererOutcomeCannotOverrideResolver(t *testing.T) {
	page := PageResult{Status: http.StatusNotFound}
	tests := []struct {
		name     string
		rendered renderer.Result
		wantErr  bool
	}{
		{name: "empty status", rendered: renderer.Result{}, wantErr: false},
		{name: "matching status", rendered: renderer.Result{Status: http.StatusNotFound}, wantErr: false},
		{name: "conflicting status", rendered: renderer.Result{Status: http.StatusOK}, wantErr: true},
		{
			name: "renderer redirect",
			rendered: renderer.Result{
				Redirect: &renderer.Redirect{Status: http.StatusFound, Location: "/login"},
			},
			wantErr: true,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			status, redirect, err := mergeRenderOutcome(page, testCase.rendered)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("mergeRenderOutcome error=%v, wantErr=%v", err, testCase.wantErr)
			}
			if !testCase.wantErr && (status != page.Status || redirect != nil) {
				t.Fatalf("status=%d redirect=%#v", status, redirect)
			}
		})
	}
}

func TestDevelopmentRuntimeProxiesModulesAndUpgrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamHosts := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		upstreamHosts <- req.Host
		if strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
			connection, readWriter, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("upstream hijack: %v", err)
				return
			}
			defer connection.Close()
			_, _ = fmt.Fprint(
				readWriter,
				"HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n",
			)
			if err := readWriter.Flush(); err != nil {
				return
			}
			line, err := readWriter.ReadString('\n')
			if err != nil {
				return
			}
			_, _ = fmt.Fprintf(readWriter, "pong:%s", line)
			_ = readWriter.Flush()
			return
		}
		_, _ = fmt.Fprintf(
			w,
			"path=%s forwarded-host=%s forwarded-proto=%s",
			req.URL.RequestURI(),
			req.Header.Get("X-Forwarded-Host"),
			req.Header.Get("X-Forwarded-Proto"),
		)
	}))
	defer upstream.Close()

	runtime, err := New(Config{
		Mode:         ModeDevelopment,
		DevServerURL: upstream.URL,
	})
	if err != nil {
		t.Fatalf("New development runtime: %v", err)
	}
	router := gin.New()
	var resolverCalls atomic.Int32
	if err := runtime.MountGin(router, GinOptions{
		Resolver: func(context.Context, PageRequest) (PageResult, error) {
			resolverCalls.Add(1)
			return PageResult{Payload: mapPayload{"page": "navigation"}}, nil
		},
		ExcludedPathPrefixes: []string{"/api"},
	}); err != nil {
		t.Fatalf("MountGin: %v", err)
	}

	module := performRequest(router, http.MethodGet, "/@vite/client?direct=1", func(req *http.Request) {
		req.Host = "app.example:8080"
	})
	if module.Code != http.StatusOK ||
		!strings.Contains(module.Body.String(), "path=/@vite/client?direct=1") ||
		!strings.Contains(module.Body.String(), "forwarded-host=app.example:8080") ||
		!strings.Contains(module.Body.String(), "forwarded-proto=http") {
		t.Fatalf("module proxy status=%d body=%s", module.Code, module.Body.String())
	}
	upstreamURL, _ := url.Parse(upstream.URL)
	if host := <-upstreamHosts; host != upstreamURL.Host {
		t.Fatalf("upstream Host=%q, want %q", host, upstreamURL.Host)
	}

	excluded := performRequest(router, http.MethodGet, "/api/missing", nil)
	if excluded.Code != http.StatusNotFound {
		t.Fatalf("excluded path status=%d", excluded.Code)
	}
	navigation := navigationRequest(router, "/dashboard", nil)
	if navigation.Code != http.StatusOK || resolverCalls.Load() != 1 {
		t.Fatalf("navigation status=%d calls=%d body=%s", navigation.Code, resolverCalls.Load(), navigation.Body.String())
	}

	backend := httptest.NewServer(router)
	backendURL, _ := url.Parse(backend.URL)
	connection, err := net.Dial("tcp", backendURL.Host)
	if err != nil {
		t.Fatalf("dial backend: %v", err)
	}
	reader := bufio.NewReader(connection)
	_, _ = fmt.Fprintf(
		connection,
		"GET /@vite/hmr HTTP/1.1\r\nHost: app.example:8080\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n",
	)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read upgrade status: %v", err)
	}
	if !strings.Contains(statusLine, "101 Switching Protocols") {
		t.Fatalf("upgrade status=%q", statusLine)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read upgrade headers: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}
	_, _ = fmt.Fprint(connection, "ping\n")
	echo, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read upgrade tunnel: %v", err)
	}
	if echo != "pong:ping\n" {
		t.Fatalf("upgrade echo=%q", echo)
	}
	_ = connection.Close()
	backend.Close()
	if host := <-upstreamHosts; host != upstreamURL.Host {
		t.Fatalf("upgrade upstream Host=%q, want %q", host, upstreamURL.Host)
	}

	if err := runtime.Close(); err != nil {
		t.Fatalf("Close development runtime: %v", err)
	}
}
