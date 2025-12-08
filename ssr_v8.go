//go:build !nov8

package gossr

import (
	"log"
	"os"
	"strings"

	"gossr/renderer"

	rendegojs "github.com/daodao97/gossr/renderer/engine/gojs"
	renderv8 "github.com/daodao97/gossr/renderer/engine/v8"
)

func newRendererFromEnv(scriptContents string) renderer.Renderer {
	engine := strings.ToLower(strings.TrimSpace(os.Getenv("SSR_ENGINE")))
	switch engine {
	case "goja", "gojs", "js":
		log.Printf("Using goja SSR engine")
		return rendegojs.NewRenderer(scriptContents)
	case "", "v8", "v8go", "default":
		log.Printf("Using v8go SSR engine")
		return renderv8.NewRenderer(scriptContents)
	default:
		log.Printf("Unknown SSR_ENGINE=%s, fallback to v8go", engine)
		return renderv8.NewRenderer(scriptContents)
	}
}
