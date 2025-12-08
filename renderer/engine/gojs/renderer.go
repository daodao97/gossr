package gojs

import (
	"errors"
	"fmt"

	"github.com/daodao97/gossr/renderer"
	"github.com/dop251/goja"
)

// Renderer 基于 goja 的实现，便于在无 V8 环境下运行 SSR。
type Renderer struct {
	pool *runtimePool
}

// NewRenderer 创建 goja 渲染器，编译脚本供后续复用。
func NewRenderer(scriptContents string) *Renderer {
	program, err := goja.Compile(renderer.DefaultSSRScriptName, scriptContents, false)
	if err != nil {
		// 与 v8 版本保持行为，一旦脚本无法编译直接 panic，方便尽早暴露问题。
		panic(fmt.Errorf("compile ssr script: %w", err))
	}

	return &Renderer{pool: newRuntimePool(program)}
}

// Render 同步执行 ssrRender，支持 Promise 结果。
func (r *Renderer) Render(urlPath string, payload map[string]any) (renderer.Result, error) {
	rt, err := r.pool.Get()
	if err != nil {
		return renderer.Result{}, err
	}
	defer r.pool.Put(rt)

	// 注入 SSR 数据
	_ = rt.Set("__SSR_HEAD__", goja.Undefined())
	if len(payload) > 0 {
		if err := rt.Set("__SSR_DATA__", payload); err != nil {
			return renderer.Result{}, err
		}
	} else {
		_ = rt.Set("__SSR_DATA__", goja.Undefined())
	}

	renderVal := rt.Get("ssrRender")
	renderFunc, ok := goja.AssertFunction(renderVal)
	if !ok {
		return renderer.Result{}, errors.New("ssrRender is not a function")
	}

	val, err := renderFunc(goja.Undefined(), rt.ToValue(urlPath))
	if err != nil {
		return renderer.Result{}, formatGojaError(err)
	}

	resultVal, err := resolveGojaValue(val)
	if err != nil {
		return renderer.Result{}, formatGojaError(err)
	}

	headVal := rt.Get("__SSR_HEAD__")
	head := ""
	if headVal != nil && !goja.IsNull(headVal) && !goja.IsUndefined(headVal) {
		head = headVal.String()
	}

	return renderer.Result{
		HTML: resultVal.String(),
		Head: head,
	}, nil
}
