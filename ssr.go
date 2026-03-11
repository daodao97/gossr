package gossr

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// errNotFound 用于 handler 表示路由不匹配（如无效的 locale）
var errNotFound = errors.New("not found")

// SsrEngine 内部 gin 引擎，用于 SSR 数据路由
var SsrEngine *gin.Engine

func init() {
	SsrEngine = gin.New()
	SsrEngine.Use(gin.Recovery())
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
			log.Printf("ssr handler failed path=%s err=%v", c.Request.URL.Path, err)
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
	group.GET("/*path", func(c *gin.Context) {
		w, req := callSsrEngine(c.Request.Context(), c.Request, c.Param("path"), c.Request.URL.RawQuery)

		if w.Code != http.StatusOK {
			c.Data(w.Code, "application/json", w.Body.Bytes())
			return
		}

		var data map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			c.Data(w.Code, "application/json", w.Body.Bytes())
			return
		}

		c.JSON(http.StatusOK, enrichPayloadForSSRFetchResponse(data, req))
	})
}

// Resolve 服务端内部调用，获取 SSR 数据
func Resolve(ctx context.Context, rawPath, rawQuery string) (SSRPayload, int, error) {
	cleanPath := path.Clean("/" + strings.TrimPrefix(strings.TrimSpace(rawPath), "/"))
	w, _ := callSsrEngine(ctx, nil, cleanPath, rawQuery)
	data, status, err := parseSSRPayloadResponse(w)
	if err != nil || status != http.StatusOK {
		return nil, status, err
	}

	return mapPayload(data), status, nil
}

func resolveRequest(ctx context.Context, req *http.Request) (SSRPayload, int, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if req == nil || req.URL == nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("nil request")
	}

	cleanPath := path.Clean("/" + strings.TrimPrefix(strings.TrimSpace(req.URL.Path), "/"))
	w, _ := callSsrEngine(ctx, req, cleanPath, req.URL.RawQuery)
	data, status, err := parseSSRPayloadResponse(w)
	if err != nil || status != http.StatusOK {
		return nil, status, err
	}

	return mapPayload(data), status, nil
}

func callSsrEngine(ctx context.Context, sourceReq *http.Request, requestPath, rawQuery string) (*httptest.ResponseRecorder, *http.Request) {
	if ctx == nil {
		ctx = context.Background()
	}

	requestPath = strings.TrimSpace(requestPath)
	if requestPath == "" {
		requestPath = "/"
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, requestPath+"?"+rawQuery, nil)
	req = req.WithContext(ctx)
	if sourceReq != nil {
		req.Header = sourceReq.Header.Clone()
		req.Host = sourceReq.Host
		req.TLS = sourceReq.TLS
		req.RemoteAddr = sourceReq.RemoteAddr
	}
	SsrEngine.ServeHTTP(w, req)

	return w, req
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

func Ssr(r *gin.Engine, dist embed.FS) error {
	frontendFs, err := fs.Sub(dist, "dist/client")
	if err != nil {
		return err
	}
	serverFs, err := fs.Sub(dist, "dist/server")
	if err != nil {
		return err
	}

	RunBlocking(
		r,
		FrontendBuild{
			FrontendDist: frontendFs,
			ServerDist:   serverFs,
		},
		registerSSRFetchRoutes(r),
	)

	return nil
}

func registerSSRFetchRoutes(r *gin.Engine) BackendDataFetcher {
	group := r.Group(DefaultSSRDataRoute, ssrGuardMiddleware())
	Router(group)

	return func(ctx context.Context, req *http.Request) (SSRPayload, error) {
		payload, status, err := resolveRequest(ctx, req)
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

func ssrGuardMiddleware() gin.HandlerFunc {
	sharedToken := strings.TrimSpace(os.Getenv("SSR_FETCH_TOKEN"))

	return func(c *gin.Context) {
		if code, ok := authorizeSSRFetch(c.Request, sharedToken); !ok {
			c.AbortWithStatus(code)
			return
		}

		c.Next()
	}
}

func authorizeSSRFetch(r *http.Request, sharedToken string) (int, bool) {
	if sharedToken != "" {
		if r.Header.Get("X-SSR-Token") != sharedToken {
			return http.StatusUnauthorized, false
		}
		return 0, true
	}

	if sameOriginRequest(r) {
		return 0, true
	}

	if allowUnsafeSSRFetchHeaderBypass() && r.Header.Get("X-SSR-Fetch") == "1" {
		return 0, true
	}

	return http.StatusForbidden, false
}

func sameOriginRequest(r *http.Request) bool {
	host := primaryHost(r)
	if host == "" {
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return false
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}

	return strings.EqualFold(parsed.Host, host)
}

func allowUnsafeSSRFetchHeaderBypass() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SSR_ALLOW_UNSAFE_FETCH_HEADER"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type mapPayload map[string]any

func (m mapPayload) AsMap() map[string]any {
	return m
}
