package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/daodao97/gossr"
	"github.com/daodao97/gossr/example/web"
	"github.com/gin-gonic/gin"
)

type greetingPayload struct {
	Message     string
	Path        string
	Query       string
	GeneratedAt string
}

func (p greetingPayload) AsMap() map[string]any {
	return map[string]any{
		"message":     p.Message,
		"path":        p.Path,
		"query":       p.Query,
		"generatedAt": p.GeneratedAt,
	}
}

func init() {
	gossr.SsrEngine.GET("/", gossr.WrapSSR(homePayload))
	gossr.SsrEngine.GET("/hi/:name", gossr.WrapSSR(hiPayload))
}

func homePayload(c *gin.Context) (gossr.SSRPayload, error) {
	return buildPayload(c, "Hello from gossr + Vue SSR"), nil
}

func hiPayload(c *gin.Context) (gossr.SSRPayload, error) {
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		name = "friend"
	}

	title := strings.TrimSpace(c.Query("title"))
	if title != "" {
		name = fmt.Sprintf("%s %s", title, name)
	}

	return buildPayload(c, fmt.Sprintf("Hi, %s!", name)), nil
}

func buildPayload(c *gin.Context, message string) greetingPayload {
	return greetingPayload{
		Message:     message,
		Path:        c.Request.URL.Path,
		Query:       c.Request.URL.RawQuery,
		GeneratedAt: time.Now().Format(time.RFC3339),
	}
}

func main() {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())

	if err := gossr.Ssr(router, web.Dist); err != nil {
		log.Fatal(err)
	}

	addr := ":8080"
	log.Printf("gossr example is running at http://127.0.0.1%s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatal(err)
	}
}
