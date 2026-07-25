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

const pageSchemaVersion = 1

// pageDocument 是 Go 与 Vue 共享的页面快照。首屏渲染和浏览器内导航
// 都由同一个 resolvePage 产出同一份文档。
type pageDocument struct {
	SchemaVersion int            `json:"schema_version"`
	URL           string         `json:"url"`
	Locale        string         `json:"locale"`
	Session       map[string]any `json:"session"`
	Page          pageData       `json:"page"`
}

type pageData struct {
	Kind        string `json:"kind"`
	Message     string `json:"message"`
	GeneratedAt string `json:"generated_at"`
}

var routeKinds = map[string]string{
	"/":             "home",
	"/hi/gopher":    "hi",
	"/hi/vue":       "hi",
	"/seo-demo":     "seo",
	"/session-demo": "session",
	"/protected":    "protected",
	"/slow-ssr":     "slow_ssr",
	"/slow-fetch":   "slow_fetch",
}

var messageKeyByKind = map[string]string{
	"home":       "payload.home.message",
	"seo":        "payload.seo.message",
	"session":    "payload.session.message",
	"slow_ssr":   "payload.slowSsr.message",
	"slow_fetch": "payload.slowFetch.message",
}

var localeMessages = mustLoadLocaleMessages()

// resolvePage 是应用唯一的页面边界:文档请求和 /_ssr/data 导航请求
// 共用它,gossr 负责渲染、序列化、缓存头和 cookie 写回。
func resolvePage(ctx context.Context, request gossr.PageRequest) (gossr.PageResult, error) {
	locale := localeFromRequestPath(request.URL.Path)
	routePath := stripLocalePrefix(request.URL.Path)
	kind, found := routeKinds[routePath]

	session, err := resolveDemoSession(ctx, request.Source)
	if err != nil {
		return gossr.PageResult{}, err
	}

	if kind == "protected" && session == nil {
		target := request.URL.RequestURI()
		location := localizedRoutePathFor(locale, "/session-demo") +
			"?next=" + url.QueryEscape(target)
		return gossr.PageResult{
			Redirect: &gossr.Redirect{Status: http.StatusFound, Location: location},
		}, nil
	}

	if kind == "slow_fetch" {
		// 演示慢数据源:只延迟数据阶段,页面通过框架的 loading 状态反馈。
		select {
		case <-time.After(3500 * time.Millisecond):
		case <-ctx.Done():
			return gossr.PageResult{}, ctx.Err()
		}
	}

	document := pageDocument{
		SchemaVersion: pageSchemaVersion,
		URL:           request.URL.RequestURI(),
		Locale:        locale,
		Session:       session,
		Page: pageData{
			Kind:        kind,
			Message:     pageMessage(kind, locale, routePath, request.URL.Query()),
			GeneratedAt: time.Now().Format(time.RFC3339),
		},
	}
	status := http.StatusOK
	if !found {
		document.Page.Kind = "not_found"
		status = http.StatusNotFound
	}

	payload, err := gossr.ObjectPayload(document)
	if err != nil {
		return gossr.PageResult{}, err
	}
	return gossr.PageResult{Payload: payload, Status: status}, nil
}

func pageMessage(kind string, locale string, routePath string, query url.Values) string {
	if kind == "hi" {
		name := strings.TrimPrefix(routePath, "/hi/")
		if name == "" {
			name = localizedText(locale, "payload.hi.friend")
		}
		if title := strings.TrimSpace(query.Get("title")); title != "" {
			name = fmt.Sprintf("%s %s", title, name)
		}
		return fmt.Sprintf(localizedText(locale, "payload.hi.template"), name)
	}
	key, ok := messageKeyByKind[kind]
	if !ok {
		return ""
	}
	return localizedText(locale, key)
}

func localizedRoutePathFor(locale string, basePath string) string {
	if locale == locales.Default {
		return basePath
	}
	if basePath == "/" {
		return "/" + locale
	}
	return "/" + locale + basePath
}

func stripLocalePrefix(rawPath string) string {
	trimmed := strings.Trim(rawPath, "/")
	if trimmed == "" {
		return "/"
	}
	segments := strings.SplitN(trimmed, "/", 2)
	if !locales.IsSupported(segments[0]) {
		return "/" + trimmed
	}
	if len(segments) == 1 {
		return "/"
	}
	return "/" + segments[1]
}

func localeFromRequestPath(rawPath string) string {
	trimmed := strings.Trim(rawPath, "/")
	if trimmed == "" {
		return locales.Default
	}

	candidate := strings.TrimSpace(strings.Split(trimmed, "/")[0])
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

	runtime, err := gossr.New(gossr.Config{
		Bundle: web.Dist,
		// slow-fetch 演示会在 resolver 里等待 3.5s,放宽端到端预算。
		PageTimeout: 10 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := runtime.MountGin(router, gossr.GinOptions{
		Resolver:             resolvePage,
		ExcludedPathPrefixes: []string{"/demo"},
	}); err != nil {
		_ = runtime.Close()
		log.Fatal(err)
	}

	addr := ":8080"
	log.Printf("gossr example is running at http://127.0.0.1%s", addr)
	err = router.Run(addr)
	if closeErr := runtime.Close(); closeErr != nil {
		log.Printf("close SSR runtime failed: %v", closeErr)
	}
	if err != nil {
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
	if req == nil {
		return nil, nil
	}
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
