package gossr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/daodao97/gossr/renderer"
	"github.com/daodao97/gossr/renderer/engine/gojs"
	"github.com/gin-gonic/gin"
	nethtml "golang.org/x/net/html"
)

// Mode controls whether Runtime serves a production bundle or proxies
// unmatched frontend requests to a development server.
type Mode uint8

const (
	ModeAuto Mode = iota
	ModeProduction
	ModeDevelopment
)

// Config prepares the reusable SSR resources. New performs all filesystem,
// template, proxy, and renderer validation before MountGin mutates a router.
type Config struct {
	Bundle fs.FS
	// SiteOrigin is the canonical public origin injected into page requests
	// and used by the default browser navigation authorizer. This keeps the
	// same-origin boundary correct behind development and TLS proxies.
	SiteOrigin   string
	Mode         Mode
	DevServerURL string

	RendererFactory renderer.Factory

	// PageTimeout covers queueing, resolver work, and rendering.
	PageTimeout time.Duration
	// MaxConcurrentPages bounds the complete typed page lifecycle. Zero uses
	// GOMAXPROCS; negative values are invalid.
	MaxConcurrentPages int
	// RenderTimeout optionally adds a tighter deadline around only Render.
	RenderTimeout time.Duration
}

// GinOptions supplies the host-owned typed page boundary at mount time.
type GinOptions struct {
	Resolver PageResolver

	// ExcludedPathPrefixes must be explicit. Runtime never infers host route
	// namespaces from Gin's route table.
	ExcludedPathPrefixes []string

	SSRFetchAuthorizer SSRFetchAuthorizer
}

type runtimeState uint8

const (
	runtimePrepared runtimeState = iota + 1
	runtimeMounted
	runtimeMountFailed
	runtimeClosing
	runtimeClosed
)

var errRuntimeClosing = errors.New("gossr runtime is closing")

// Runtime owns production render resources or a development proxy and
// coordinates their request and shutdown lifecycles.
type Runtime struct {
	mu        sync.Mutex
	state     runtimeState
	closeDone chan struct{}
	closeErr  error

	rootContext context.Context
	cancelRoot  context.CancelFunc
	active      sync.WaitGroup

	mode            Mode
	frontendDist    fs.FS
	assetsFS        fs.FS
	indexHTML       string
	renderer        renderer.Renderer
	proxy           *httputil.ReverseProxy
	siteOrigin      string
	devServerOrigin string
	admission       *pageAdmission
	renderTimeout   time.Duration
	pageTimeout     time.Duration
	maxConcurrency  int
}

// New prepares a Runtime without mutating a Gin engine.
func New(config Config) (*Runtime, error) {
	return newRuntime(config, false)
}

func newRuntime(config Config, compatibilityUnlimited bool) (_ *Runtime, err error) {
	mode, err := normalizeRuntimeMode(config.Mode)
	if err != nil {
		return nil, err
	}
	siteOrigin, err := normalizeSiteOrigin(config.SiteOrigin)
	if err != nil {
		return nil, err
	}
	pageTimeout, renderTimeout, concurrency, err := normalizeRuntimeLimits(config, compatibilityUnlimited)
	if err != nil {
		return nil, err
	}

	rootContext, cancelRoot := context.WithCancel(context.Background())
	instance := &Runtime{
		state:          runtimePrepared,
		closeDone:      make(chan struct{}),
		rootContext:    rootContext,
		cancelRoot:     cancelRoot,
		mode:           mode,
		siteOrigin:     siteOrigin,
		admission:      newPageAdmission(concurrency, pageTimeout),
		renderTimeout:  renderTimeout,
		pageTimeout:    pageTimeout,
		maxConcurrency: concurrency,
	}
	defer func() {
		if err == nil {
			return
		}
		cancelRoot()
		if closeErr := closeRendererSafely(instance.renderer); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()

	if mode == ModeDevelopment {
		rawDevServerURL := strings.TrimSpace(config.DevServerURL)
		if rawDevServerURL == "" {
			rawDevServerURL = devServerURL()
		}
		instance.proxy, err = buildDevProxy(rawDevServerURL)
		if err != nil {
			return nil, err
		}
		parsedDevServerURL, _ := url.Parse(rawDevServerURL)
		instance.devServerOrigin, _ = canonicalOrigin(
			parsedDevServerURL.Scheme,
			parsedDevServerURL.Host,
		)
		return instance, nil
	}

	if config.Bundle == nil {
		return nil, errors.New("frontend bundle filesystem is nil")
	}
	instance.frontendDist, err = fs.Sub(config.Bundle, "dist/client")
	if err != nil {
		return nil, fmt.Errorf("prepare frontend filesystem: %w", err)
	}
	serverDist, err := fs.Sub(config.Bundle, "dist/server")
	if err != nil {
		return nil, fmt.Errorf("prepare server filesystem: %w", err)
	}

	indexBytes, err := readFSFile(instance.frontendDist, "index.html")
	if err != nil {
		return nil, fmt.Errorf("failed to read index.html: %w", err)
	}
	instance.indexHTML = string(indexBytes)
	if err := validateIndexTemplate(instance.indexHTML); err != nil {
		return nil, fmt.Errorf("invalid index.html: %w", err)
	}

	instance.assetsFS, err = fs.Sub(instance.frontendDist, "assets")
	if err != nil {
		return nil, fmt.Errorf("failed to prepare assets filesystem: %w", err)
	}
	if _, err := fs.Stat(instance.assetsFS, "."); err != nil {
		return nil, fmt.Errorf("failed to validate assets filesystem: %w", err)
	}
	if _, err := fs.ReadDir(instance.frontendDist, "."); err != nil {
		return nil, fmt.Errorf("failed to validate frontend root: %w", err)
	}

	serverEntry, err := readFSFile(serverDist, renderer.DefaultSSRScriptName)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", renderer.DefaultSSRScriptName, err)
	}
	factory := config.RendererFactory
	if factory == nil {
		factory = func(scriptContents string) renderer.Renderer {
			return gojs.NewRenderer(scriptContents)
		}
	}
	instance.renderer, err = createRenderer(factory, string(serverEntry))
	if err != nil {
		return nil, err
	}
	return instance, nil
}

func normalizeRuntimeMode(mode Mode) (Mode, error) {
	switch mode {
	case ModeAuto:
		if isDevMode() {
			return ModeDevelopment, nil
		}
		return ModeProduction, nil
	case ModeProduction, ModeDevelopment:
		return mode, nil
	default:
		return 0, fmt.Errorf("invalid runtime mode %d", mode)
	}
}

func normalizeRuntimeLimits(
	config Config,
	compatibilityUnlimited bool,
) (time.Duration, time.Duration, int, error) {
	if config.PageTimeout < 0 {
		return 0, 0, 0, errors.New("PageTimeout must not be negative")
	}
	if config.RenderTimeout < 0 {
		return 0, 0, 0, errors.New("RenderTimeout must not be negative")
	}
	if config.MaxConcurrentPages < 0 {
		return 0, 0, 0, errors.New("MaxConcurrentPages must not be negative")
	}

	pageTimeout := config.PageTimeout
	if pageTimeout == 0 {
		pageTimeout = defaultPageRequestTimeout
	}
	renderTimeout := config.RenderTimeout
	if renderTimeout == 0 {
		renderTimeout = defaultPageRequestTimeout
	}

	concurrency := config.MaxConcurrentPages
	if concurrency == 0 {
		if compatibilityUnlimited {
			return pageTimeout, renderTimeout, 0, nil
		}
		concurrency = goruntime.GOMAXPROCS(0)
	}
	if concurrency > maxSSRRenderLimit {
		concurrency = maxSSRRenderLimit
	}
	return pageTimeout, renderTimeout, concurrency, nil
}

// MountGin installs navigation, static-file, and final NoRoute handlers.
// Host routes must already be registered. The method can succeed only once.
// If Gin panics while registering a conflicting route, MountGin returns an
// error and closes Runtime; the partially-mutated router must be discarded.
func (r *Runtime) MountGin(router *gin.Engine, options GinOptions) (err error) {
	if r == nil {
		return errors.New("gossr runtime is nil")
	}
	if router == nil {
		return errors.New("gin engine is nil")
	}
	if options.Resolver == nil {
		return errors.New("page resolver is nil")
	}
	excludedPrefixes, err := validateExcludedPathPrefixes(options.ExcludedPathPrefixes)
	if err != nil {
		return err
	}

	r.mu.Lock()
	if r.state != runtimePrepared {
		state := r.state
		r.mu.Unlock()
		return fmt.Errorf("gossr runtime cannot mount from state %d", state)
	}

	locked := true
	defer func() {
		recovered := recover()
		if recovered == nil {
			if locked {
				r.mu.Unlock()
			}
			return
		}

		r.state = runtimeMountFailed
		r.mu.Unlock()
		locked = false
		mountErr := fmt.Errorf("mount gossr routes: %v", recovered)
		if closeErr := r.Close(); closeErr != nil {
			mountErr = errors.Join(mountErr, closeErr)
		}
		err = mountErr
	}()

	r.mountGinRoutes(router, options.Resolver, excludedPrefixes, options.SSRFetchAuthorizer)
	r.state = runtimeMounted
	r.mu.Unlock()
	locked = false
	return nil
}

func (r *Runtime) mountGinRoutes(
	router *gin.Engine,
	resolver PageResolver,
	excludedPrefixes []string,
	authorizer SSRFetchAuthorizer,
) {
	if r.mode == ModeProduction {
		router.Group("/assets", cacheControlMiddleware(cacheImmutableAsset)).
			StaticFS("/", http.FS(r.assetsFS))
		registerRootStaticFiles(router, r.frontendDist)
	}

	handler := r.navigationHandler(resolver)
	guardAuthorizer := configuredSSRFetchAuthorizer(
		authorizer,
		r.siteOrigin,
		r.devServerOrigin,
	)
	group := router.Group(DefaultSSRDataRoute)
	group.GET("", ssrDataNoStoreMiddleware(), ssrGuardMiddleware(guardAuthorizer), handler)
	group.GET("/*path", ssrDataNoStoreMiddleware(), ssrGuardMiddleware(guardAuthorizer), handler)

	if r.mode == ModeDevelopment {
		router.NoRoute(r.developmentNoRoute(excludedPrefixes))
		return
	}
	router.NoRoute(r.productionNoRoute(resolver, excludedPrefixes))
}

func (r *Runtime) productionNoRoute(
	resolver PageResolver,
	excludedPrefixes []string,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !shouldHandleDocument(c.Request, excludedPrefixes) {
			c.Status(http.StatusNotFound)
			return
		}

		requestContext, release, err := r.beginRequest(c.Request.Context())
		if err != nil {
			setHTMLNoCacheHeaders(c)
			c.Status(http.StatusServiceUnavailable)
			return
		}
		defer release()
		c.Request = c.Request.WithContext(requestContext)

		handlePageDocumentWithRenderTimeout(
			c,
			r.indexHTML,
			r.renderer,
			r.admission,
			resolver,
			excludedPrefixes,
			r.siteOrigin,
			r.renderTimeout,
		)
	}
}

func (r *Runtime) developmentNoRoute(excludedPrefixes []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if isSSRDataPath(c.Request.URL.Path) || pathMatchesAnyPrefix(c.Request.URL.Path, excludedPrefixes) {
			c.Status(http.StatusNotFound)
			return
		}

		requestContext, release, err := r.beginRequest(c.Request.Context())
		if err != nil {
			setHTMLNoCacheHeaders(c)
			c.Status(http.StatusServiceUnavailable)
			return
		}
		defer release()
		c.Request = c.Request.WithContext(requestContext)
		r.proxy.ServeHTTP(c.Writer, c.Request)
	}
}

func (r *Runtime) navigationHandler(resolver PageResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestContext, releaseRuntime, err := r.beginRequest(c.Request.Context())
		if err != nil {
			writeNavigationError(
				c,
				http.StatusServiceUnavailable,
				"service_unavailable",
				"page runtime is shutting down",
			)
			return
		}
		defer releaseRuntime()
		c.Request = c.Request.WithContext(requestContext)

		pageContext, releaseAdmission, err := r.admission.enter(c.Request.Context())
		if err != nil {
			status := pageRequestErrorStatus(err)
			writeNavigationError(c, status, "request_timeout", "page request timed out")
			return
		}
		defer releaseAdmission()
		c.Request = c.Request.WithContext(pageContext)

		request, err := newNavigationPageRequest(c.Request, "")
		if err != nil {
			writeNavigationError(c, http.StatusBadRequest, "invalid_target", "invalid navigation target")
			return
		}
		request.SiteOrigin = r.siteOrigin
		if request.SiteOrigin == "" {
			request.SiteOrigin = requestOrigin(c.Request)
		}

		resolved, payload, err := resolvePage(c.Request.Context(), resolver, request)
		if err != nil {
			log.Printf("resolve navigation failed target=%q err=%q", targetRequestURI(request), err)
			status := pageRequestErrorStatus(err)
			code := "resolve_failed"
			message := "page resolution failed"
			if status == http.StatusGatewayTimeout || status == http.StatusRequestTimeout {
				code = "request_timeout"
				message = "page request timed out"
			}
			writeNavigationError(c, status, code, message)
			return
		}
		writePageCookies(c, resolved.Cookies)
		setPageCacheHeaders(c, resolved.Cache)

		if resolved.Redirect != nil {
			c.JSON(http.StatusOK, navigationRedirectOutcome{
				Kind:     "redirect",
				Status:   resolved.Redirect.Status,
				Location: resolved.Redirect.Location,
			})
			return
		}
		c.JSON(resolved.Status, navigationRenderOutcome{
			Kind:     "render",
			Status:   resolved.Status,
			Snapshot: payload,
		})
	}
}

func (r *Runtime) beginRequest(parent context.Context) (context.Context, func(), error) {
	if parent == nil {
		parent = context.Background()
	}

	r.mu.Lock()
	if r.state != runtimeMounted {
		r.mu.Unlock()
		return nil, func() {}, errRuntimeClosing
	}
	r.active.Add(1)
	rootContext := r.rootContext
	r.mu.Unlock()

	requestContext, cancelRequest := context.WithCancel(parent)
	stopRootCancellation := context.AfterFunc(rootContext, cancelRequest)
	var releaseOnce sync.Once
	return requestContext, func() {
		releaseOnce.Do(func() {
			stopRootCancellation()
			cancelRequest()
			r.active.Done()
		})
	}, nil
}

// Close is an idempotent shutdown barrier. It rejects new page work, cancels
// queued and active requests, waits for them to leave Runtime, then closes the
// renderer. Concurrent callers observe the same result.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	if r.closeDone == nil {
		r.closeDone = make(chan struct{})
	}
	switch r.state {
	case runtimeClosed:
		err := r.closeErr
		r.mu.Unlock()
		return err
	case runtimeClosing:
		done := r.closeDone
		r.mu.Unlock()
		<-done
		r.mu.Lock()
		err := r.closeErr
		r.mu.Unlock()
		return err
	default:
		r.state = runtimeClosing
	}
	cancelRoot := r.cancelRoot
	r.mu.Unlock()

	if cancelRoot != nil {
		cancelRoot()
	}
	r.active.Wait()
	closeErr := closeRendererSafely(r.renderer)

	r.mu.Lock()
	r.closeErr = closeErr
	r.state = runtimeClosed
	close(r.closeDone)
	r.mu.Unlock()
	return closeErr
}

func closeRendererSafely(instance renderer.Renderer) (err error) {
	if instance == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("close SSR renderer: %v", recovered)
		}
	}()
	if err := renderer.Close(instance); err != nil {
		return fmt.Errorf("close SSR renderer: %w", err)
	}
	return nil
}

func validateExcludedPathPrefixes(prefixes []string) ([]string, error) {
	validated := make([]string, 0, len(prefixes))
	for _, rawPrefix := range prefixes {
		prefix := strings.TrimSpace(rawPrefix)
		if prefix == "" {
			return nil, errors.New("excluded path prefix must not be empty")
		}
		target, err := parseRelativePageTarget(prefix)
		if err != nil || target.RawQuery != "" || target.ForceQuery {
			return nil, fmt.Errorf("invalid excluded path prefix %q", rawPrefix)
		}
		prefix = target.Path
		if prefix != "/" {
			prefix = strings.TrimRight(prefix, "/")
		}
		validated = append(validated, prefix)
	}
	return validated, nil
}

func pathMatchesAnyPrefix(rawPath string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if pathHasPrefix(rawPath, prefix) {
			return true
		}
	}
	return false
}

type templateElement struct {
	name string
	id   string
}

type indexTemplateInspection struct {
	appCount            int
	bootCount           int
	markerCount         int
	markerDirectlyInApp bool
	appHasSSRAttribute  bool
}

const appHTMLMarker = "<!--app-html-->"

var voidHTMLElements = map[string]struct{}{
	"area": {}, "base": {}, "br": {}, "col": {}, "embed": {}, "hr": {},
	"img": {}, "input": {}, "link": {}, "meta": {}, "param": {},
	"source": {}, "track": {}, "wbr": {},
}

func countRawStartTagAttribute(raw []byte, target string) int {
	count := 0
	index := 1
	for index < len(raw) && !isHTMLSpace(raw[index]) &&
		raw[index] != '>' && raw[index] != '/' {
		index++
	}

	for index < len(raw) {
		for index < len(raw) && isHTMLSpace(raw[index]) {
			index++
		}
		if index >= len(raw) || raw[index] == '>' ||
			(raw[index] == '/' && index+1 < len(raw) && raw[index+1] == '>') {
			break
		}

		nameStart := index
		for index < len(raw) && !isHTMLSpace(raw[index]) &&
			raw[index] != '=' && raw[index] != '>' && raw[index] != '/' {
			index++
		}
		if nameStart == index {
			index++
			continue
		}
		if strings.EqualFold(string(raw[nameStart:index]), target) {
			count++
		}

		for index < len(raw) && isHTMLSpace(raw[index]) {
			index++
		}
		if index >= len(raw) || raw[index] != '=' {
			continue
		}
		index++
		for index < len(raw) && isHTMLSpace(raw[index]) {
			index++
		}
		if index >= len(raw) {
			break
		}
		if raw[index] == '"' || raw[index] == '\'' {
			quote := raw[index]
			index++
			for index < len(raw) && raw[index] != quote {
				index++
			}
			if index < len(raw) {
				index++
			}
			continue
		}
		for index < len(raw) && !isHTMLSpace(raw[index]) && raw[index] != '>' {
			index++
		}
	}
	return count
}

func isHTMLSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	default:
		return false
	}
}

func readTemplateStartTag(tokenizer *nethtml.Tokenizer) (templateElement, bool, error) {
	name, hasAttribute := tokenizer.TagName()
	element := templateElement{name: strings.ToLower(string(name))}
	if countRawStartTagAttribute(tokenizer.Raw(), "id") > 1 {
		return templateElement{}, false, fmt.Errorf(
			"element <%s> has duplicate id attributes",
			element.name,
		)
	}
	if countRawStartTagAttribute(tokenizer.Raw(), "data-ssr") > 1 {
		return templateElement{}, false, fmt.Errorf(
			"element <%s> has duplicate data-ssr attributes",
			element.name,
		)
	}
	hasSSRAttribute := false
	idAttributeCount := 0
	ssrAttributeCount := 0

	for hasAttribute {
		key, value, hasMore := tokenizer.TagAttr()
		hasAttribute = hasMore
		switch strings.ToLower(string(key)) {
		case "id":
			idAttributeCount++
			element.id = string(value)
		case "data-ssr":
			ssrAttributeCount++
			hasSSRAttribute = true
		}
	}
	if idAttributeCount > 1 {
		return templateElement{}, false, fmt.Errorf(
			"element <%s> has duplicate id attributes",
			element.name,
		)
	}
	if ssrAttributeCount > 1 {
		return templateElement{}, false, fmt.Errorf(
			"element <%s> has duplicate data-ssr attributes",
			element.name,
		)
	}
	return element, hasSSRAttribute, nil
}

func inspectIndexTemplate(indexHTML string) (indexTemplateInspection, error) {
	tokenizer := nethtml.NewTokenizer(strings.NewReader(indexHTML))
	stack := make([]templateElement, 0, 16)
	var inspection indexTemplateInspection

	for {
		tokenType := tokenizer.Next()
		switch tokenType {
		case nethtml.ErrorToken:
			if err := tokenizer.Err(); err != nil && !errors.Is(err, io.EOF) {
				return indexTemplateInspection{}, fmt.Errorf("parse HTML template: %w", err)
			}
			return inspection, nil

		case nethtml.StartTagToken, nethtml.SelfClosingTagToken:
			element, hasSSRAttribute, err := readTemplateStartTag(tokenizer)
			if err != nil {
				return indexTemplateInspection{}, err
			}
			switch element.id {
			case "app":
				inspection.appCount++
				inspection.appHasSSRAttribute = inspection.appHasSSRAttribute ||
					hasSSRAttribute
			case "__GOSSR_BOOT__":
				inspection.bootCount++
			}
			if tokenType == nethtml.StartTagToken {
				if _, isVoid := voidHTMLElements[element.name]; !isVoid {
					stack = append(stack, element)
				}
			}

		case nethtml.EndTagToken:
			rawName, _ := tokenizer.TagName()
			name := strings.ToLower(string(rawName))
			for index := len(stack) - 1; index >= 0; index-- {
				if stack[index].name == name {
					stack = stack[:index]
					break
				}
			}

		case nethtml.CommentToken:
			if string(tokenizer.Raw()) != appHTMLMarker {
				continue
			}
			inspection.markerCount++
			if len(stack) > 0 && stack[len(stack)-1].id == "app" {
				inspection.markerDirectlyInApp = true
			}
		}
	}
}

func validateIndexTemplate(indexHTML string) error {
	inspection, err := inspectIndexTemplate(indexHTML)
	if err != nil {
		return err
	}
	if inspection.markerCount != 1 {
		return fmt.Errorf(
			"expected exactly one structural %s marker, found %d",
			appHTMLMarker,
			inspection.markerCount,
		)
	}
	if inspection.appCount != 1 {
		return fmt.Errorf(
			"expected exactly one element with id=app, found %d",
			inspection.appCount,
		)
	}
	if !inspection.markerDirectlyInApp {
		return errors.New("app-html marker must be a direct child of the id=app element")
	}
	if inspection.appHasSSRAttribute {
		return errors.New("template app element must not pre-set data-ssr")
	}
	if inspection.bootCount != 0 {
		return errors.New("template must not contain __GOSSR_BOOT__")
	}
	return nil
}

func replaceAppHTMLMarker(document, replacement string) (string, error) {
	tokenizer := nethtml.NewTokenizer(strings.NewReader(document))
	offset := 0
	markerStart := -1
	markerEnd := -1
	markerCount := 0

	for {
		tokenType := tokenizer.Next()
		raw := tokenizer.Raw()
		tokenStart := offset
		offset += len(raw)

		switch tokenType {
		case nethtml.ErrorToken:
			if err := tokenizer.Err(); err != nil && !errors.Is(err, io.EOF) {
				return document, fmt.Errorf("parse HTML document: %w", err)
			}
			if markerCount != 1 {
				return document, fmt.Errorf(
					"expected exactly one structural %s marker, found %d",
					appHTMLMarker,
					markerCount,
				)
			}
			return document[:markerStart] + replacement + document[markerEnd:], nil

		case nethtml.CommentToken:
			if string(raw) != appHTMLMarker {
				continue
			}
			markerCount++
			if markerCount == 1 {
				markerStart = tokenStart
				markerEnd = offset
			}
		}
	}
}

type htmlByteRange struct {
	start int
	end   int
}

func insertBeforeHTMLElementEnd(
	document string,
	elementName string,
	insertion string,
) (string, bool, error) {
	tokenizer := nethtml.NewTokenizer(strings.NewReader(document))
	offset := 0
	for {
		tokenType := tokenizer.Next()
		raw := tokenizer.Raw()
		tokenStart := offset
		offset += len(raw)

		switch tokenType {
		case nethtml.ErrorToken:
			if err := tokenizer.Err(); err != nil && !errors.Is(err, io.EOF) {
				return document, false, fmt.Errorf("parse HTML document: %w", err)
			}
			return document, false, nil

		case nethtml.EndTagToken:
			name, _ := tokenizer.TagName()
			if strings.EqualFold(string(name), elementName) {
				return document[:tokenStart] + insertion + document[tokenStart:], true, nil
			}
		}
	}
}

func documentHasHTMLElement(document string, elementName string) (bool, error) {
	tokenizer := nethtml.NewTokenizer(strings.NewReader(document))
	for {
		switch tokenizer.Next() {
		case nethtml.ErrorToken:
			if err := tokenizer.Err(); err != nil && !errors.Is(err, io.EOF) {
				return false, fmt.Errorf("parse HTML document: %w", err)
			}
			return false, nil

		case nethtml.StartTagToken, nethtml.SelfClosingTagToken:
			name, _ := tokenizer.TagName()
			if strings.EqualFold(string(name), elementName) {
				return true, nil
			}
		}
	}
}

func removeHTMLElementsWithin(
	document string,
	elementName string,
	ancestorName string,
) (string, error) {
	tokenizer := nethtml.NewTokenizer(strings.NewReader(document))
	ranges := make([]htmlByteRange, 0, 1)
	offset := 0
	ancestorDepth := 0
	elementDepth := 0
	elementStart := -1

	for {
		tokenType := tokenizer.Next()
		raw := tokenizer.Raw()
		tokenStart := offset
		offset += len(raw)

		switch tokenType {
		case nethtml.ErrorToken:
			if err := tokenizer.Err(); err != nil && !errors.Is(err, io.EOF) {
				return document, fmt.Errorf("parse HTML document: %w", err)
			}
			if elementDepth > 0 {
				ranges = append(ranges, htmlByteRange{start: elementStart, end: len(document)})
			}
			if len(ranges) == 0 {
				return document, nil
			}

			var result strings.Builder
			result.Grow(len(document))
			previousEnd := 0
			for _, byteRange := range ranges {
				result.WriteString(document[previousEnd:byteRange.start])
				previousEnd = byteRange.end
			}
			result.WriteString(document[previousEnd:])
			return result.String(), nil

		case nethtml.StartTagToken:
			name, _ := tokenizer.TagName()
			if strings.EqualFold(string(name), ancestorName) {
				ancestorDepth++
				continue
			}
			if ancestorDepth == 0 {
				continue
			}
			if !strings.EqualFold(string(name), elementName) {
				continue
			}
			if elementDepth == 0 {
				elementStart = tokenStart
			}
			elementDepth++

		case nethtml.SelfClosingTagToken:
			name, _ := tokenizer.TagName()
			if ancestorDepth > 0 && elementDepth == 0 &&
				strings.EqualFold(string(name), elementName) {
				ranges = append(ranges, htmlByteRange{start: tokenStart, end: offset})
			}

		case nethtml.EndTagToken:
			name, _ := tokenizer.TagName()
			if elementDepth > 0 && strings.EqualFold(string(name), elementName) {
				elementDepth--
				if elementDepth == 0 {
					ranges = append(ranges, htmlByteRange{start: elementStart, end: offset})
					elementStart = -1
				}
			}
			if ancestorDepth > 0 && strings.EqualFold(string(name), ancestorName) {
				ancestorDepth--
			}
		}
	}
}

func setHTMLElementAttribute(
	document string,
	elementName string,
	attributeName string,
	attributeValue string,
) (string, bool, error) {
	tokenizer := nethtml.NewTokenizer(strings.NewReader(document))
	offset := 0
	for {
		tokenType := tokenizer.Next()
		raw := tokenizer.Raw()
		tokenStart := offset
		offset += len(raw)

		switch tokenType {
		case nethtml.ErrorToken:
			if err := tokenizer.Err(); err != nil && !errors.Is(err, io.EOF) {
				return document, false, fmt.Errorf("parse HTML document: %w", err)
			}
			return document, false, nil

		case nethtml.StartTagToken, nethtml.SelfClosingTagToken:
			token := tokenizer.Token()
			if !strings.EqualFold(token.Data, elementName) {
				continue
			}

			attributes := make([]nethtml.Attribute, 0, len(token.Attr)+1)
			replaced := false
			for _, attribute := range token.Attr {
				if !strings.EqualFold(attribute.Key, attributeName) {
					attributes = append(attributes, attribute)
					continue
				}
				if replaced {
					continue
				}
				attribute.Key = attributeName
				attribute.Val = attributeValue
				attributes = append(attributes, attribute)
				replaced = true
			}
			if !replaced {
				attributes = append(attributes, nethtml.Attribute{
					Key: attributeName,
					Val: attributeValue,
				})
			}
			token.Attr = attributes
			return document[:tokenStart] + token.String() + document[offset:], true, nil
		}
	}
}

func markSSRApp(indexHTML string) (string, error) {
	tokenizer := nethtml.NewTokenizer(strings.NewReader(indexHTML))
	offset := 0
	for {
		tokenType := tokenizer.Next()
		raw := tokenizer.Raw()
		tokenStart := offset
		offset += len(raw)

		switch tokenType {
		case nethtml.ErrorToken:
			if err := tokenizer.Err(); err != nil && !errors.Is(err, io.EOF) {
				return indexHTML, fmt.Errorf("parse HTML template: %w", err)
			}
			return indexHTML, errors.New("template has no element with id=app")

		case nethtml.StartTagToken, nethtml.SelfClosingTagToken:
			element, hasSSRAttribute, err := readTemplateStartTag(tokenizer)
			if err != nil {
				return indexHTML, err
			}
			if element.id != "app" {
				continue
			}
			if hasSSRAttribute {
				return indexHTML, errors.New(
					"template app element already has data-ssr",
				)
			}

			insertion := len(raw) - 1
			if insertion <= 0 || raw[insertion] != '>' {
				return indexHTML, errors.New("invalid app start tag")
			}
			if insertion > 0 && raw[insertion-1] == '/' {
				insertion--
			}
			insertion += tokenStart
			return indexHTML[:insertion] +
				` data-ssr="true"` +
				indexHTML[insertion:], nil
		}
	}
}

func documentHasElementID(document, targetID string) (bool, error) {
	tokenizer := nethtml.NewTokenizer(strings.NewReader(document))
	for {
		switch tokenizer.Next() {
		case nethtml.ErrorToken:
			if err := tokenizer.Err(); err != nil && !errors.Is(err, io.EOF) {
				return false, err
			}
			return false, nil
		case nethtml.StartTagToken, nethtml.SelfClosingTagToken:
			for _, attribute := range tokenizer.Token().Attr {
				if strings.EqualFold(attribute.Key, "id") &&
					attribute.Val == targetID {
					return true, nil
				}
			}
		}
	}
}
