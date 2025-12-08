//go:build nov8

package gossr

import (
	"log"

	"gossr/renderer"
	rendegojs "gossr/renderer/engine/gojs"
)

func newRendererFromEnv(scriptContents string) renderer.Renderer {
	log.Printf("Using goja SSR engine (v8 disabled via build tag)")
	return rendegojs.NewRenderer(scriptContents)
}
