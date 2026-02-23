//go:build !nov8

package v8

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"strconv"
	"sync/atomic"

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
func (r *Renderer) Render(ctx context.Context, urlPath string, payload map[string]any) (renderer.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	iso, err := r.pool.Get()
	if err != nil {
		return renderer.Result{}, err
	}

	var terminated atomic.Bool
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			terminated.Store(true)
			iso.Isolate.TerminateExecution()
		case <-stopWatch:
		}
	}()

	defer close(stopWatch)
	defer func() {
		if terminated.Load() || iso.Isolate.IsExecutionTerminating() {
			r.pool.Discard(iso)
			return
		}
		r.pool.Put(iso)
	}()

	v8ctx := v8go.NewContext(iso.Isolate)
	defer v8ctx.Close()

	if len(payload) > 0 {
		jsonData, err := json.Marshal(payload)
		if err != nil {
			return renderer.Result{}, err
		}

		escaped := template.JSEscapeString(string(jsonData))
		script := fmt.Sprintf(`globalThis.__SSR_DATA__ = JSON.parse("%s");`, escaped)
		if _, err := v8ctx.RunScript(script, "ssr-data.js"); err != nil {
			if terminated.Load() && ctx.Err() != nil {
				return renderer.Result{}, ctx.Err()
			}
			return renderer.Result{}, formatV8Error(err)
		}
	}

	if _, err := iso.RenderScript.Run(v8ctx); err != nil {
		if terminated.Load() && ctx.Err() != nil {
			return renderer.Result{}, ctx.Err()
		}
		return renderer.Result{}, formatV8Error(err)
	}

	quotedPath := strconv.Quote(urlPath)
	renderCmd := fmt.Sprintf("ssrRender(%s)", quotedPath)
	val, err := v8ctx.RunScript(renderCmd, r.ssrScriptName)
	if err != nil {
		if terminated.Load() && ctx.Err() != nil {
			return renderer.Result{}, ctx.Err()
		}
		return renderer.Result{}, formatV8Error(err)
	}

	renderedHtml := ""

	if val.IsPromise() {
		result, err := resolveV8Promise(v8ctx, val, err, ctx)
		if err != nil {
			if terminated.Load() && ctx.Err() != nil {
				return renderer.Result{}, ctx.Err()
			}
			return renderer.Result{}, formatV8Error(err)
		}

		renderedHtml = result.String()
	} else {
		renderedHtml = val.String()
	}

	headVal, err := v8ctx.RunScript("globalThis.__SSR_HEAD__ || ''", "ssr-head.js")
	if err != nil {
		if terminated.Load() && ctx.Err() != nil {
			return renderer.Result{}, ctx.Err()
		}
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
