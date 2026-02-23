package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/daodao97/gossr"
	"github.com/daodao97/gossr/example/web"
	"github.com/daodao97/gossr/locales"
	"github.com/gin-gonic/gin"
)

type greetingPayload struct {
	Message     string
	Locale      string
	Path        string
	Query       string
	GeneratedAt string
}

func (p greetingPayload) AsMap() map[string]any {
	return map[string]any{
		"message":     p.Message,
		"locale":      p.Locale,
		"path":        p.Path,
		"query":       p.Query,
		"generatedAt": p.GeneratedAt,
	}
}

var demoLocales = []string{"en", "zh"}

func init() {
	registerLocalizedSSRRoute("/", homePayload)
	registerLocalizedSSRRoute("/hi/:name", hiPayload)
	registerLocalizedSSRRoute("/seo-demo", seoDemoPayload)
	registerLocalizedSSRRoute("/session-demo", sessionDemoPayload)
	registerLocalizedSSRRoute("/slow-ssr", slowSSRPayload)
	registerLocalizedSSRRoute("/slow-fetch", slowFetchPayload)
}

func homePayload(c *gin.Context) (gossr.SSRPayload, error) {
	locale := localeFromRequestPath(c.Request.URL.Path)
	message := localizedText(locale, "Hello from gossr + Vue SSR", "来自 gossr + Vue SSR 的你好")
	return buildPayload(c, message), nil
}

func hiPayload(c *gin.Context) (gossr.SSRPayload, error) {
	locale := localeFromRequestPath(c.Request.URL.Path)
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		name = localizedText(locale, "friend", "朋友")
	}

	title := strings.TrimSpace(c.Query("title"))
	if title != "" {
		name = fmt.Sprintf("%s %s", title, name)
	}

	message := localizedText(
		locale,
		fmt.Sprintf("Hi, %s!", name),
		fmt.Sprintf("你好，%s！", name),
	)
	return buildPayload(c, message), nil
}

func seoDemoPayload(c *gin.Context) (gossr.SSRPayload, error) {
	locale := localeFromRequestPath(c.Request.URL.Path)
	message := localizedText(
		locale,
		"SEO head tags are rendered on server side",
		"SEO Head 标签已在服务端渲染",
	)
	return buildPayload(c, message), nil
}

func sessionDemoPayload(c *gin.Context) (gossr.SSRPayload, error) {
	locale := localeFromRequestPath(c.Request.URL.Path)
	message := localizedText(
		locale,
		"Session is injected from session_token cookie",
		"Session 已从 session_token cookie 注入",
	)
	return buildPayload(c, message), nil
}

func slowSSRPayload(c *gin.Context) (gossr.SSRPayload, error) {
	locale := localeFromRequestPath(c.Request.URL.Path)
	message := localizedText(
		locale,
		"This route intentionally simulates slow SSR rendering",
		"该路由会故意模拟慢速 SSR 渲染",
	)
	return buildPayload(c, message), nil
}

func slowFetchPayload(c *gin.Context) (gossr.SSRPayload, error) {
	// 模拟 __ssr_fetch 慢查询：只延迟数据阶段，不影响 SSR 渲染阶段逻辑。
	select {
	case <-time.After(3500 * time.Millisecond):
	case <-c.Request.Context().Done():
		return nil, c.Request.Context().Err()
	}

	locale := localeFromRequestPath(c.Request.URL.Path)
	message := localizedText(
		locale,
		"This route intentionally simulates slow __ssr_fetch payload",
		"该路由会故意模拟慢速 __ssr_fetch 数据",
	)
	return buildPayload(c, message), nil
}

func buildPayload(c *gin.Context, message string) greetingPayload {
	locale := localeFromRequestPath(c.Request.URL.Path)
	return greetingPayload{
		Message:     message,
		Locale:      locale,
		Path:        c.Request.URL.Path,
		Query:       c.Request.URL.RawQuery,
		GeneratedAt: time.Now().Format(time.RFC3339),
	}
}

func registerLocalizedSSRRoute(basePath string, handler func(*gin.Context) (gossr.SSRPayload, error)) {
	gossr.SsrEngine.GET(basePath, gossr.WrapSSR(handler))
	for _, locale := range demoLocales {
		gossr.SsrEngine.GET(localizedRoutePath(locale, basePath), gossr.WrapSSR(handler))
	}
}

func localizedRoutePath(locale string, basePath string) string {
	if basePath == "/" {
		return "/" + locale
	}
	return "/" + locale + basePath
}

func localeFromRequestPath(rawPath string) string {
	trimmed := strings.Trim(rawPath, "/")
	if trimmed == "" {
		return locales.Default
	}

	segments := strings.Split(trimmed, "/")
	if len(segments) == 0 {
		return locales.Default
	}

	candidate := strings.TrimSpace(segments[0])
	if locales.IsSupported(candidate) {
		return locales.Normalize(candidate)
	}

	return locales.Default
}

func localizedText(locale string, en string, zh string) string {
	if locale == "zh" {
		return zh
	}
	return en
}

func main() {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	registerSessionDemoRoutes(router)

	if err := gossr.Ssr(router, web.Dist); err != nil {
		log.Fatal(err)
	}

	addr := ":8080"
	log.Printf("gossr example is running at http://127.0.0.1%s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatal(err)
	}
}

type demoSessionToken struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Provider string `json:"provider"`
	IssuedAt int64  `json:"iat"`
}

func registerSessionDemoRoutes(router *gin.Engine) {
	router.GET("/demo/session/login", func(c *gin.Context) {
		nextPath := sanitizeNextPath(c.Query("next"), "/session-demo")
		payload := demoSessionToken{
			ID:       "u_demo_1001",
			Name:     "SSR Demo User",
			Email:    "demo@example.com",
			Provider: "example",
			IssuedAt: time.Now().Unix(),
		}

		raw, err := json.Marshal(payload)
		if err != nil {
			c.String(http.StatusInternalServerError, "encode session payload failed")
			return
		}

		token := base64.StdEncoding.EncodeToString(raw)
		c.SetCookie("session_token", token, 60*60*24*7, "/", "", false, true)
		c.Redirect(http.StatusFound, nextPath)
	})

	router.GET("/demo/session/logout", func(c *gin.Context) {
		nextPath := sanitizeNextPath(c.Query("next"), "/session-demo")
		c.SetCookie("session_token", "", -1, "/", "", false, true)
		c.Redirect(http.StatusFound, nextPath)
	})
}

func sanitizeNextPath(raw string, fallback string) string {
	nextPath := strings.TrimSpace(raw)
	if nextPath == "" {
		return fallback
	}
	if !strings.HasPrefix(nextPath, "/") || strings.HasPrefix(nextPath, "//") {
		return fallback
	}
	return nextPath
}
