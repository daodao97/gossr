//go:build nov8

package gossr

import (
	"log"

	"github.com/daodao97/gossr/renderer"
	rendegojs "github.com/daodao97/gossrc/render/engine/gojs"
)

func newRendererFromEnv(scriptContents string) renderer.Renderer {
	log.Printf("Using goja SSR engine (v8 disabled via build tag)")
	return rendegojs.NewRenderer(scriptContents)
}
