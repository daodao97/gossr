package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
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

var demoLocales = append([]string(nil), locales.Supported...)
var localeMessages = mustLoadLocaleMessages()
var ssrDataEngine = gossr.NewDataEngine()

func init() {
	registerLocalizedSSRRoute(ssrDataEngine, "/", homePayload)
	registerLocalizedSSRRoute(ssrDataEngine, "/hi/:name", hiPayload)
	registerLocalizedSSRRoute(ssrDataEngine, "/seo-demo", seoDemoPayload)
	registerLocalizedSSRRoute(ssrDataEngine, "/session-demo", sessionDemoPayload)
	registerLocalizedSSRRoute(ssrDataEngine, "/slow-ssr", slowSSRPayload)
	registerLocalizedSSRRoute(ssrDataEngine, "/slow-fetch", slowFetchPayload)
}

func homePayload(c *gin.Context) (gossr.SSRPayload, error) {
	locale := localeFromRequestPath(c.Request.URL.Path)
	message := localizedText(locale, "payload.home.message")
	return buildPayload(c, message), nil
}

func hiPayload(c *gin.Context) (gossr.SSRPayload, error) {
	locale := localeFromRequestPath(c.Request.URL.Path)
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		name = localizedText(locale, "payload.hi.friend")
	}

	title := strings.TrimSpace(c.Query("title"))
	if title != "" {
		name = fmt.Sprintf("%s %s", title, name)
	}

	message := fmt.Sprintf(localizedText(locale, "payload.hi.template"), name)
	return buildPayload(c, message), nil
}

func seoDemoPayload(c *gin.Context) (gossr.SSRPayload, error) {
	locale := localeFromRequestPath(c.Request.URL.Path)
	message := localizedText(locale, "payload.seo.message")
	return buildPayload(c, message), nil
}

func sessionDemoPayload(c *gin.Context) (gossr.SSRPayload, error) {
	locale := localeFromRequestPath(c.Request.URL.Path)
	message := localizedText(locale, "payload.session.message")
	return buildPayload(c, message), nil
}

func slowSSRPayload(c *gin.Context) (gossr.SSRPayload, error) {
	locale := localeFromRequestPath(c.Request.URL.Path)
	message := localizedText(locale, "payload.slowSsr.message")
	return buildPayload(c, message), nil
}

func slowFetchPayload(c *gin.Context) (gossr.SSRPayload, error) {
	// 模拟 _ssr/data 慢查询：只延迟数据阶段，不影响 SSR 渲染阶段逻辑。
	select {
	case <-time.After(3500 * time.Millisecond):
	case <-c.Request.Context().Done():
		return nil, c.Request.Context().Err()
	}

	locale := localeFromRequestPath(c.Request.URL.Path)
	message := localizedText(locale, "payload.slowFetch.message")
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

func registerLocalizedSSRRoute(engine *gin.Engine, basePath string, handler func(*gin.Context) (gossr.SSRPayload, error)) {
	engine.GET(basePath, gossr.WrapSSR(handler))
	for _, locale := range demoLocales {
		engine.GET(localizedRoutePath(locale, basePath), gossr.WrapSSR(handler))
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

func localizedText(locale string, key string) string {
	if localeMessages == nil {
		return key
	}
	return localeMessages.Translate(locale, key)
}

func mustLoadLocaleMessages() *web.LocaleMessages {
	messages, err := web.LoadLocaleMessages()
	if err != nil {
		log.Fatalf("load locale messages failed: %v", err)
	}
	return messages
}

func main() {
	router := gin.New()
	if accessLogEnabled() {
		router.Use(gin.Logger())
	}
	router.Use(gin.Recovery())
	registerSessionDemoRoutes(router)

	if err := gossr.SsrWithOptions(router, web.Dist, gossr.Options{
		SessionResolver: resolveDemoSession,
		DataEngine:      ssrDataEngine,
	}); err != nil {
		log.Fatal(err)
	}

	addr := ":8080"
	log.Printf("gossr example is running at http://127.0.0.1%s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatal(err)
	}
}

func accessLogEnabled() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("HTTP_ACCESS_LOG")))
	if raw == "" {
		return gin.Mode() != gin.ReleaseMode
	}

	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type demoSession struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Provider string `json:"provider"`
	IssuedAt int64  `json:"iat"`
}

const (
	demoSessionTTL      = 7 * 24 * time.Hour
	demoSessionMaxItems = 1024
)

type demoSessionStore struct {
	mu       sync.Mutex
	sessions map[string]demoSession
	maxItems int
	ttl      time.Duration
}

func newDemoSessionStore(maxItems int, ttl time.Duration) *demoSessionStore {
	return &demoSessionStore{
		sessions: make(map[string]demoSession),
		maxItems: maxItems,
		ttl:      ttl,
	}
}

func (s *demoSessionStore) load(token string, now time.Time) (demoSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[token]
	if !ok {
		return demoSession{}, false
	}
	if s.expired(session, now) {
		delete(s.sessions, token)
		return demoSession{}, false
	}
	return session, true
}

func (s *demoSessionStore) store(token string, session demoSession, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldestToken := ""
	oldestIssuedAt := int64(0)
	for existingToken, existing := range s.sessions {
		if s.expired(existing, now) {
			delete(s.sessions, existingToken)
			continue
		}
		if oldestToken == "" || existing.IssuedAt < oldestIssuedAt {
			oldestToken = existingToken
			oldestIssuedAt = existing.IssuedAt
		}
	}

	if _, exists := s.sessions[token]; !exists && len(s.sessions) >= s.maxItems && oldestToken != "" {
		delete(s.sessions, oldestToken)
	}
	s.sessions[token] = session
}

func (s *demoSessionStore) delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *demoSessionStore) expired(session demoSession, now time.Time) bool {
	return now.Sub(time.Unix(session.IssuedAt, 0)) > s.ttl
}

var demoSessions = newDemoSessionStore(demoSessionMaxItems, demoSessionTTL)

func resolveDemoSession(_ context.Context, req *http.Request) (map[string]any, error) {
	cookie, err := req.Cookie("session_token")
	if err != nil || cookie.Value == "" {
		return nil, nil
	}

	session, ok := demoSessions.load(cookie.Value, time.Now())
	if !ok {
		return nil, nil
	}
	return map[string]any{
		"user": map[string]any{
			"id":       session.ID,
			"name":     session.Name,
			"email":    session.Email,
			"provider": session.Provider,
		},
	}, nil
}

func registerSessionDemoRoutes(router *gin.Engine) {
	methodNotAllowed := func(c *gin.Context) {
		c.Header("Allow", http.MethodPost)
		c.Status(http.StatusMethodNotAllowed)
	}
	router.GET("/demo/session/login", methodNotAllowed)
	router.GET("/demo/session/logout", methodNotAllowed)
	router.GET("/demo/session/status", func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Header("Pragma", "no-cache")
		session, err := resolveDemoSession(c.Request.Context(), c.Request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"authenticated": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"authenticated": session != nil})
	})

	router.POST("/demo/session/login", requireSameOriginForm, func(c *gin.Context) {
		nextPath := sanitizeNextPath(c.PostForm("next"), "/session-demo")
		session := demoSession{
			ID:       "u_demo_1001",
			Name:     "SSR Demo User",
			Email:    "demo@example.com",
			Provider: "example",
			IssuedAt: time.Now().Unix(),
		}

		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			c.String(http.StatusInternalServerError, "create session failed")
			return
		}

		token := hex.EncodeToString(tokenBytes)
		if cookie, err := c.Request.Cookie("session_token"); err == nil {
			demoSessions.delete(cookie.Value)
		}
		demoSessions.store(token, session, time.Now())
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie("session_token", token, int(demoSessionTTL.Seconds()), "/", "", c.Request.TLS != nil, true)
		c.Redirect(http.StatusFound, nextPath)
	})

	router.POST("/demo/session/logout", requireSameOriginForm, func(c *gin.Context) {
		nextPath := sanitizeNextPath(c.PostForm("next"), "/session-demo")
		if cookie, err := c.Request.Cookie("session_token"); err == nil {
			demoSessions.delete(cookie.Value)
		}
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie("session_token", "", -1, "/", "", c.Request.TLS != nil, true)
		c.Redirect(http.StatusFound, nextPath)
	})
}

func requireSameOriginForm(c *gin.Context) {
	if _, ok := gossr.DefaultSSRFetchAuthorizer(c.Request); !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	c.Next()
}

func sanitizeNextPath(raw string, fallback string) string {
	nextPath := strings.TrimSpace(raw)
	if nextPath == "" || strings.Contains(nextPath, "\\") {
		return fallback
	}

	parsed, err := url.Parse(nextPath)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil {
		return fallback
	}
	if !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") {
		return fallback
	}
	return nextPath
}
