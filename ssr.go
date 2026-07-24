package gossr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"

	"github.com/daodao97/gossr/renderer"
	"github.com/gin-gonic/gin"
)

// errNotFound 用于 handler 表示路由不匹配（如无效的 locale）
var errNotFound = errors.New("not found")

// SsrEngine 是兼容旧用法的默认 SSR 数据引擎。
// Deprecated: 新代码应通过 NewDataEngine 创建实例并用 Options.DataEngine 注入，
// 避免多个 SSR 应用共享全局路由表。
var SsrEngine = NewDataEngine()

// NewDataEngine 创建隔离的 SSR 数据路由引擎。
func NewDataEngine() *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery())
	return engine
}

// WrapSSR 包装 SSR handler 为 gin handler
func WrapSSR(h func(*gin.Context) (SSRPayload, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		payload, err := h(c)
		if errors.Is(err, errNotFound) {
			c.Status(http.StatusNotFound)
			return
		}
		if err != nil {
			log.Printf("ssr handler failed path=%q err=%q", c.Request.URL.Path, err)
			if exposeSSRErrors() {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		if payload == nil {
			c.JSON(http.StatusOK, gin.H{})
			return
		}
		c.JSON(http.StatusOK, payload.AsMap())
	}
}

func exposeSSRErrors() bool {
	if !isDevMode() {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(os.Getenv("SSR_EXPOSE_HANDLER_ERROR"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Router 挂载 SSR 路由到外部 gin group（供客户端 fetch 调用）
func Router(group *gin.RouterGroup) {
	RouterWithEngineAndAuthorizer(group, SsrEngine, nil)
}

// RouterWithEngine 将指定数据引擎挂载到外部 gin group。
func RouterWithEngine(group *gin.RouterGroup, engine *gin.Engine) {
	RouterWithEngineAndAuthorizer(group, engine, nil)
}

// RouterWithEngineAndAuthorizer 将指定数据引擎和宿主授权器挂载到外部 gin group。
func RouterWithEngineAndAuthorizer(group *gin.RouterGroup, engine *gin.Engine, authorizer SSRFetchAuthorizer) {
	if engine == nil {
		engine = SsrEngine
	}
	group.GET("/*path", ssrDataNoStoreMiddleware(), ssrGuardMiddleware(authorizer), func(c *gin.Context) {
		w, req := callSsrEngineWithEngine(engine, c.Request.Context(), c.Request, c.Param("path"), c.Request.URL.RawQuery)

		if w.Code != http.StatusOK {
			c.Data(w.Code, "application/json", w.Body.Bytes())
			return
		}

		var data map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			log.Printf("ssr data handler returned invalid JSON path=%q err=%q", c.Request.URL.Path, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}

		c.JSON(http.StatusOK, enrichPayloadForSSRFetchResponse(data, req))
	})
}

func setSSRDataNoStoreHeaders(c *gin.Context) {
	c.Header("Cache-Control", cacheNoStoreHTML)
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

func ssrDataNoStoreMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		setSSRDataNoStoreHeaders(c)
		c.Next()
	}
}

// Resolve 服务端内部调用，获取 SSR 数据
func Resolve(ctx context.Context, rawPath, rawQuery string) (SSRPayload, int, error) {
	return ResolveWithEngine(ctx, SsrEngine, rawPath, rawQuery)
}

// ResolveWithEngine 从指定 SSR 数据引擎解析 payload。
func ResolveWithEngine(ctx context.Context, engine *gin.Engine, rawPath, rawQuery string) (SSRPayload, int, error) {
	requestPath := ensureLeadingSlash(rawPath)
	w, _ := callSsrEngineWithEngine(engine, ctx, nil, requestPath, rawQuery)
	data, status, err := parseSSRPayloadResponse(w)
	if err != nil || status != http.StatusOK {
		return nil, status, err
	}

	return mapPayload(data), status, nil
}

func resolveRequestWithEngine(ctx context.Context, engine *gin.Engine, req *http.Request) (SSRPayload, int, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if req == nil || req.URL == nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("nil request")
	}

	requestPath := ensureLeadingSlash(req.URL.Path)
	w, _ := callSsrEngineWithEngine(engine, ctx, req, requestPath, req.URL.RawQuery)
	data, status, err := parseSSRPayloadResponse(w)
	if err != nil || status != http.StatusOK {
		return nil, status, err
	}

	return mapPayload(data), status, nil
}

func callSsrEngineWithEngine(engine *gin.Engine, ctx context.Context, sourceReq *http.Request, requestPath, rawQuery string) (*httptest.ResponseRecorder, *http.Request) {
	if ctx == nil {
		ctx = context.Background()
	}

	requestPath = ensureLeadingSlash(requestPath)

	w := httptest.NewRecorder()
	requestURL := &url.URL{Path: requestPath, RawQuery: rawQuery}
	req := &http.Request{
		Method:     http.MethodGet,
		URL:        requestURL,
		Header:     make(http.Header),
		Host:       "example.com",
		RemoteAddr: "192.0.2.1:1234",
		RequestURI: requestURL.RequestURI(),
	}
	req = req.WithContext(ctx)
	if sourceReq != nil {
		req.Header = sourceReq.Header.Clone()
		req.Host = sourceReq.Host
		req.TLS = sourceReq.TLS
		req.RemoteAddr = sourceReq.RemoteAddr
	}
	if engine == nil {
		engine = SsrEngine
	}
	engine.ServeHTTP(w, req)

	return w, req
}

func ensureLeadingSlash(rawPath string) string {
	if rawPath == "" {
		return "/"
	}
	if strings.HasPrefix(rawPath, "/") {
		return rawPath
	}
	return "/" + rawPath
}

func parseSSRPayloadResponse(w *httptest.ResponseRecorder) (map[string]any, int, error) {
	if w.Code == http.StatusNotFound {
		return nil, http.StatusNotFound, nil
	}

	if w.Code != http.StatusOK {
		return nil, w.Code, fmt.Errorf("ssr handler returned %d", w.Code)
	}

	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		return nil, http.StatusInternalServerError, err
	}

	return data, http.StatusOK, nil
}

func Ssr(r *gin.Engine, dist fs.FS) error {
	return SsrWithOptions(r, dist, Options{})
}

// Options 配置单个 SSR 实例的可插拔能力。
type Options struct {
	RendererFactory renderer.Factory
	SessionResolver SessionResolver
	// PageResolver is the preferred typed boundary for both initial documents
	// and browser navigation snapshots.
	PageResolver PageResolver
	// ExcludedPathPrefixes reserves host namespaces such as /api and /admin
	// from the frontend document fallback.
	ExcludedPathPrefixes []string
	// SSRFetchAuthorizer 由宿主应用负责 /_ssr/data 的额外授权。
	// 为空时使用浏览器同源校验；设置后由授权器完整决定是否放行。
	SSRFetchAuthorizer SSRFetchAuthorizer
	// DataEngine 隔离当前 SSR 实例的数据路由；为空时兼容使用全局 SsrEngine。
	DataEngine *gin.Engine
	// SiteOrigin 固定规范站点 origin，并作为默认浏览器数据请求的同源边界；
	// 为空时从经过校验的请求 Host 推断。
	SiteOrigin string
	// ShutdownContext 结束时释放实现 renderer.Closer 的内置或外部渲染器。
	ShutdownContext context.Context
}

// SsrWithRendererFactory 使用自定义渲染器工厂挂载 SSR。
// factory 为 nil 时使用内置 gojs 渲染器。
func SsrWithRendererFactory(r *gin.Engine, dist fs.FS, factory renderer.Factory) error {
	return SsrWithOptions(r, dist, Options{RendererFactory: factory})
}

// SsrWithOptions 使用指定配置挂载 SSR。
func SsrWithOptions(r *gin.Engine, dist fs.FS, options Options) error {
	if r == nil {
		return errors.New("gin engine is nil")
	}
	if dist == nil {
		return errors.New("frontend filesystem is nil")
	}
	frontendFs, err := fs.Sub(dist, "dist/client")
	if err != nil {
		return err
	}
	serverFs, err := fs.Sub(dist, "dist/server")
	if err != nil {
		return err
	}
	siteOrigin, err := normalizeSiteOrigin(options.SiteOrigin)
	if err != nil {
		return err
	}

	if options.PageResolver != nil {
		if options.DataEngine != nil {
			return errors.New("PageResolver and DataEngine cannot be used together")
		}
		if options.SessionResolver != nil {
			return errors.New("PageResolver and SessionResolver cannot be used together; resolve authentication in host middleware")
		}
		if options.ShutdownContext != nil && options.ShutdownContext.Err() != nil {
			return fmt.Errorf("shutdown context is already done: %w", options.ShutdownContext.Err())
		}

		compatibilityConcurrency := compatibilityPageConcurrency()
		runtime, err := newRuntime(Config{
			Bundle:             dist,
			SiteOrigin:         siteOrigin,
			Mode:               ModeAuto,
			RendererFactory:    options.RendererFactory,
			MaxConcurrentPages: compatibilityConcurrency,
		}, compatibilityConcurrency == 0)
		if err != nil {
			return err
		}
		if err := runtime.MountGin(r, GinOptions{
			Resolver:             options.PageResolver,
			ExcludedPathPrefixes: options.ExcludedPathPrefixes,
			SSRFetchAuthorizer:   options.SSRFetchAuthorizer,
		}); err != nil {
			_ = runtime.Close()
			return err
		}
		if options.ShutdownContext != nil {
			context.AfterFunc(options.ShutdownContext, func() {
				if err := runtime.Close(); err != nil {
					log.Printf("close SSR runtime failed: %v", err)
				}
			})
		}
		return nil
	}

	engine := options.DataEngine
	if engine == nil {
		engine = SsrEngine
	}
	if err := runBlocking(
		r,
		FrontendBuild{
			FrontendDist:    frontendFs,
			ServerDist:      serverFs,
			RendererFactory: options.RendererFactory,
			SessionResolver: options.SessionResolver,
			SiteOrigin:      siteOrigin,
			ShutdownContext: options.ShutdownContext,
		},
		dataFetcherForEngine(engine),
	); err != nil {
		return err
	}
	alternateOrigins := []string(nil)
	if isDevMode() {
		if parsedDevServerURL, parseErr := url.Parse(devServerURL()); parseErr == nil {
			if devOrigin, ok := canonicalOrigin(
				parsedDevServerURL.Scheme,
				parsedDevServerURL.Host,
			); ok {
				alternateOrigins = append(alternateOrigins, devOrigin)
			}
		}
	}
	mountSSRFetchRoutes(
		r,
		engine,
		options.SSRFetchAuthorizer,
		siteOrigin,
		alternateOrigins...,
	)
	return nil
}

func compatibilityPageConcurrency() int {
	return renderConcurrencyLimit()
}

func mountPageResolverRoutes(
	r *gin.Engine,
	resolver PageResolver,
	authorizer SSRFetchAuthorizer,
	configuredSiteOrigin string,
	admission *pageAdmission,
) {
	handler := pageResolverNavigationHandler(resolver, configuredSiteOrigin, admission)
	guardAuthorizer := configuredSSRFetchAuthorizer(
		authorizer,
		configuredSiteOrigin,
	)
	group := r.Group(DefaultSSRDataRoute)
	group.GET("", ssrDataNoStoreMiddleware(), ssrGuardMiddleware(guardAuthorizer), handler)
	group.GET("/*path", ssrDataNoStoreMiddleware(), ssrGuardMiddleware(guardAuthorizer), handler)
}

func pageResolverNavigationHandler(
	resolver PageResolver,
	configuredSiteOrigin string,
	admission *pageAdmission,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestContext, release, err := admission.enter(c.Request.Context())
		if err != nil {
			status := pageRequestErrorStatus(err)
			writeNavigationError(c, status, "request_timeout", "page request timed out")
			return
		}
		defer release()
		c.Request = c.Request.WithContext(requestContext)

		request, err := newNavigationPageRequest(c.Request, c.Param("path"))
		if err != nil {
			writeNavigationError(c, http.StatusBadRequest, "invalid_target", "invalid navigation target")
			return
		}
		request.SiteOrigin = configuredSiteOrigin
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

func writeNavigationError(c *gin.Context, status int, code, message string) {
	setSSRDataNoStoreHeaders(c)
	c.JSON(status, navigationErrorOutcome{
		Kind:    "error",
		Status:  status,
		Code:    code,
		Message: message,
	})
}

func registerSSRFetchRoutes(r *gin.Engine) BackendDataFetcher {
	return registerSSRFetchRoutesWithEngine(r, SsrEngine)
}

func registerSSRFetchRoutesWithEngine(r *gin.Engine, engine *gin.Engine) BackendDataFetcher {
	return registerSSRFetchRoutesWithAuthorizer(r, engine, nil)
}

func registerSSRFetchRoutesWithAuthorizer(r *gin.Engine, engine *gin.Engine, authorizer SSRFetchAuthorizer) BackendDataFetcher {
	if engine == nil {
		engine = SsrEngine
	}
	mountSSRFetchRoutes(r, engine, authorizer, "")
	return dataFetcherForEngine(engine)
}

func mountSSRFetchRoutes(
	r *gin.Engine,
	engine *gin.Engine,
	authorizer SSRFetchAuthorizer,
	configuredSiteOrigin string,
	alternateOrigins ...string,
) {
	group := r.Group(DefaultSSRDataRoute)
	RouterWithEngineAndAuthorizer(
		group,
		engine,
		configuredSSRFetchAuthorizer(
			authorizer,
			configuredSiteOrigin,
			alternateOrigins...,
		),
	)
}

func dataFetcherForEngine(engine *gin.Engine) BackendDataFetcher {
	return func(ctx context.Context, req *http.Request) (SSRPayload, error) {
		payload, status, err := resolveRequestWithEngine(ctx, engine, req)
		if err != nil {
			return nil, err
		}

		switch status {
		case http.StatusOK:
			return payload, nil
		case http.StatusNotFound:
			return mapPayload{}, nil
		default:
			return nil, fmt.Errorf("ssr fetch %s returned status %d", req.URL.Path, status)
		}
	}
}

func ssrGuardMiddleware(authorizer SSRFetchAuthorizer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if code, ok := authorizeSSRFetch(c.Request, authorizer); !ok {
			c.AbortWithStatus(code)
			return
		}

		c.Next()
	}
}

func authorizeSSRFetch(r *http.Request, authorizer SSRFetchAuthorizer) (int, bool) {
	if r == nil {
		return http.StatusForbidden, false
	}
	if authorizer == nil {
		authorizer = DefaultSSRFetchAuthorizer
	}

	status, allowed := authorizer(r)
	if allowed {
		return 0, true
	}
	if status < 400 || status > 499 {
		status = http.StatusForbidden
	}
	return status, false
}

// DefaultSSRFetchAuthorizer 使用浏览器 Origin/Referer 或 Sec-Fetch-Site
// 验证同源请求。自定义授权器可调用它并叠加宿主身份校验。
func DefaultSSRFetchAuthorizer(r *http.Request) (int, bool) {
	if sameOriginRequest(r) {
		return 0, true
	}
	return http.StatusForbidden, false
}

func configuredSSRFetchAuthorizer(
	authorizer SSRFetchAuthorizer,
	configuredSiteOrigin string,
	alternateOrigins ...string,
) SSRFetchAuthorizer {
	if authorizer != nil {
		return authorizer
	}

	targetOrigins := make([]string, 0, 1+len(alternateOrigins))
	addTargetOrigin := func(raw string) {
		if raw == "" {
			return
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			return
		}
		targetOrigin, ok := canonicalOrigin(parsed.Scheme, parsed.Host)
		if !ok {
			return
		}
		for _, existing := range targetOrigins {
			if existing == targetOrigin {
				return
			}
		}
		targetOrigins = append(targetOrigins, targetOrigin)
	}
	addTargetOrigin(configuredSiteOrigin)
	for _, alternateOrigin := range alternateOrigins {
		addTargetOrigin(alternateOrigin)
	}
	if len(targetOrigins) == 0 {
		return authorizer
	}

	return func(r *http.Request) (int, bool) {
		if configuredSiteOrigin == "" && sameOriginRequest(r) {
			return 0, true
		}
		for _, targetOrigin := range targetOrigins {
			if sameOriginRequestAgainst(r, targetOrigin) {
				return 0, true
			}
		}
		return http.StatusForbidden, false
	}
}

func sameOriginRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	targetOrigin, ok := canonicalOrigin(requestScheme(r), primaryHost(r))
	if !ok {
		return false
	}
	return sameOriginRequestAgainst(r, targetOrigin)
}

func sameOriginRequestAgainst(r *http.Request, targetOrigin string) bool {
	if r == nil || targetOrigin == "" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin")
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	sourceOrigin, ok := canonicalOrigin(parsed.Scheme, parsed.Host)
	if !ok {
		return false
	}

	return sourceOrigin == targetOrigin
}

type mapPayload map[string]any

func (m mapPayload) AsMap() map[string]any {
	return m
}
