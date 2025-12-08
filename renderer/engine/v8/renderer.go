//go:build !nov8

package v8

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strconv"

	"github.com/daodao97/gossr/renderer"
	"rogchap.com/v8go"
)

// Renderer renders a React application to HTML via v8go。
type Renderer struct {
	pool          *V8IsolatePool
	ssrScriptName string
}

// NewRenderer 创建 v8go 渲染器。
func NewRenderer(scriptContents string) *Renderer {
	return &Renderer{
		pool:          NewV8IsolatePool(scriptContents, renderer.DefaultSSRScriptName),
		ssrScriptName: renderer.DefaultSSRScriptName,
	}
}

// Render renders the provided path to HTML with optional data payload.
func (r *Renderer) Render(urlPath string, payload map[string]any) (renderer.Result, error) {
	iso, err := r.pool.Get()
	if err != nil {
		return renderer.Result{}, err
	}
	defer r.pool.Put(iso)

	ctx := v8go.NewContext(iso.Isolate)
	defer ctx.Close()

	if len(payload) > 0 {
		jsonData, err := json.Marshal(payload)
		if err != nil {
			return renderer.Result{}, err
		}

		escaped := template.JSEscapeString(string(jsonData))
		script := fmt.Sprintf(`globalThis.__SSR_DATA__ = JSON.parse("%s");`, escaped)
		if _, err := ctx.RunScript(script, "ssr-data.js"); err != nil {
			return renderer.Result{}, formatV8Error(err)
		}
	}

	iso.RenderScript.Run(ctx)

	quotedPath := strconv.Quote(urlPath)
	renderCmd := fmt.Sprintf("ssrRender(%s)", quotedPath)
	val, err := ctx.RunScript(renderCmd, r.ssrScriptName)
	if err != nil {
		return renderer.Result{}, formatV8Error(err)
	}

	renderedHtml := ""

	if val.IsPromise() {
		result, err := resolveV8Promise(ctx, val, err)
		if err != nil {
			return renderer.Result{}, formatV8Error(err)
		}

		renderedHtml = result.String()
	} else {
		renderedHtml = val.String()
	}

	headVal, err := ctx.RunScript("globalThis.__SSR_HEAD__ || ''", "ssr-head.js")
	if err != nil {
		return renderer.Result{}, formatV8Error(err)
	}

	headContent := ""
	if headVal != nil {
		headContent = headVal.String()
	}

	return renderer.Result{
		HTML: renderedHtml,
		Head: headContent,
	}, nil
}
