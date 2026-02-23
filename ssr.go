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
		ssrPath := c.Param("path")
		if ssrPath == "" {
			ssrPath = "/"
		}

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, ssrPath+"?"+c.Request.URL.RawQuery, nil)
		req = req.WithContext(c.Request.Context())
		req.Header = c.Request.Header.Clone()
		req.Host = c.Request.Host
		req.TLS = c.Request.TLS
		req.RemoteAddr = c.Request.RemoteAddr
		SsrEngine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			c.Data(w.Code, "application/json", w.Body.Bytes())
			return
		}

		var data map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			c.Data(w.Code, "application/json", w.Body.Bytes())
			return
		}

		c.JSON(http.StatusOK, enrichPayloadFromRequest(data, req))
	})
}

// Resolve 服务端内部调用，获取 SSR 数据
func Resolve(ctx context.Context, rawPath, rawQuery string) (SSRPayload, int, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cleanPath := path.Clean("/" + strings.TrimPrefix(strings.TrimSpace(rawPath), "/"))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, cleanPath+"?"+rawQuery, nil)
	req = req.WithContext(ctx)
	SsrEngine.ServeHTTP(w, req)

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

	return mapPayload(data), http.StatusOK, nil
}

func resolveRequest(ctx context.Context, req *http.Request) (SSRPayload, int, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if req == nil || req.URL == nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("nil request")
	}

	cleanPath := path.Clean("/" + strings.TrimPrefix(strings.TrimSpace(req.URL.Path), "/"))

	w := httptest.NewRecorder()
	internalReq := httptest.NewRequest(http.MethodGet, cleanPath+"?"+req.URL.RawQuery, nil)
	internalReq = internalReq.WithContext(ctx)
	internalReq.Header = req.Header.Clone()
	internalReq.Host = req.Host
	internalReq.TLS = req.TLS
	internalReq.RemoteAddr = req.RemoteAddr

	SsrEngine.ServeHTTP(w, internalReq)

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

	return mapPayload(data), http.StatusOK, nil
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

	// Add static file cache headers middleware
	r.Use(staticCacheMiddleware())

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

// staticCacheMiddleware adds cache headers for static assets
func staticCacheMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// Skip non-GET requests
		if c.Request.Method != "GET" {
			c.Next()
			return
		}

		// Assets with hash in filename (immutable) - cache for 1 year
		if strings.HasPrefix(path, "/assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
			c.Next()
			return
		}

		// Compressed files (.gz, .br)
		if strings.HasSuffix(path, ".gz") || strings.HasSuffix(path, ".br") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
			c.Next()
			return
		}

		// Fonts - cache for 1 year
		if strings.HasSuffix(path, ".woff") || strings.HasSuffix(path, ".woff2") ||
			strings.HasSuffix(path, ".ttf") || strings.HasSuffix(path, ".otf") ||
			strings.HasSuffix(path, ".eot") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
			c.Next()
			return
		}

		// Images - cache for 1 week
		if strings.HasSuffix(path, ".png") || strings.HasSuffix(path, ".jpg") ||
			strings.HasSuffix(path, ".jpeg") || strings.HasSuffix(path, ".gif") ||
			strings.HasSuffix(path, ".webp") || strings.HasSuffix(path, ".avif") ||
			strings.HasSuffix(path, ".ico") {
			c.Header("Cache-Control", "public, max-age=604800")
			c.Next()
			return
		}

		// SVG files - cache for 1 week
		if strings.HasSuffix(path, ".svg") {
			c.Header("Cache-Control", "public, max-age=604800")
			c.Next()
			return
		}

		// HTML pages - no cache (SSR content)
		if strings.HasSuffix(path, ".html") || !strings.Contains(path, ".") {
			c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
			c.Header("Pragma", "no-cache")
			c.Header("Expires", "0")
		}

		c.Next()
	}
}

func registerSSRFetchRoutes(r *gin.Engine) BackendDataFetcher {
	group := r.Group(DefaultSSRFetchPrefix, ssrGuardMiddleware())
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
		flagHeader := c.GetHeader("X-SSR-Fetch") == "1"
		originOK := sameOriginRequest(c.Request)

		if sharedToken != "" && c.GetHeader("X-SSR-Token") != sharedToken {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		// 允许同源请求；若非同源则必须显式标头
		if !originOK && !flagHeader {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}

		c.Next()
	}
}

func sameOriginRequest(r *http.Request) bool {
	host := r.Host
	if xf := r.Header.Get("X-Forwarded-Host"); xf != "" {
		parts := strings.Split(xf, ",")
		if len(parts) > 0 {
			host = strings.TrimSpace(parts[0])
		}
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

type mapPayload map[string]any

func (m mapPayload) AsMap() map[string]any {
	return m
}
