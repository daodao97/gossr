package gojs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync/atomic"

	"github.com/daodao97/gossr/renderer"
	"github.com/dop251/goja"
)

// Renderer 是基于 goja 的内置 SSR 实现。
type Renderer struct {
	pool *runtimePool
}

// NewRenderer 创建 goja 渲染器，编译脚本供后续复用。
func NewRenderer(scriptContents string) *Renderer {
	program, err := goja.Compile(renderer.DefaultSSRScriptName, scriptContents, false)
	if err != nil {
		// 初始化时直接暴露脚本编译错误，避免把错误推迟到请求阶段。
		panic(fmt.Errorf("compile ssr script: %w", err))
	}

	return &Renderer{pool: newRuntimePool(program)}
}

// Close 释放 runtime 池；可安全重复调用。
func (r *Renderer) Close() error {
	if r != nil && r.pool != nil {
		r.pool.Close()
	}
	return nil
}

// Render 同步执行 ssrRender，支持 Promise 结果。
func (r *Renderer) Render(ctx context.Context, urlPath string, payload map[string]any) (renderer.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	container, err := r.pool.Get(ctx)
	if err != nil {
		return renderer.Result{}, err
	}
	container.uses++
	rt := container.runtime
	healthy := false

	var interrupted atomic.Bool
	stopWatch := func() {}
	if ctx.Done() != nil {
		stopped := make(chan struct{})
		stop := context.AfterFunc(ctx, func() {
			interrupted.Store(true)
			rt.Interrupt(ctx.Err())
			close(stopped)
		})
		stopWatch = func() {
			// stop=false 表示回调已经开始；必须等它结束后才能把 runtime
			// 放回池中，避免旧请求的取消信号打断下一次渲染。
			if !stop() {
				<-stopped
			}
		}
	}

	defer func() {
		// 必须先确认旧请求 watcher 已退出，才能让 runtime 被下一请求获取。
		stopWatch()
		if interrupted.Load() || !healthy {
			r.pool.Discard(container)
			return
		}
		r.pool.Put(container)
	}()

	if container.renderFunc == nil {
		return renderer.Result{}, errors.New("ssrRender is not a function")
	}

	payloadValue, err := renderPayloadValue(container, payload)
	if err != nil {
		return renderer.Result{}, err
	}

	var argument goja.Value
	if container.structuredABI {
		input := rt.NewObject()
		if err := input.Set("url", urlPath); err != nil {
			return renderer.Result{}, err
		}
		if err := input.Set("snapshot", payloadValue); err != nil {
			return renderer.Result{}, err
		}
		argument = input
	} else {
		// Compatibility adapter for pre-v2 bundles. New bundles receive the
		// snapshot only as an explicit function argument.
		if err := rt.Set("__SSR_DATA__", payloadValue); err != nil {
			return renderer.Result{}, err
		}
		argument = rt.ToValue(urlPath)
	}

	val, err := container.renderFunc(goja.Undefined(), argument)
	if err != nil {
		if interrupted.Load() && ctx.Err() != nil {
			return renderer.Result{}, ctx.Err()
		}
		return renderer.Result{}, formatGojaError(err)
	}

	resultVal, err := resolveGojaValue(val)
	if err != nil {
		if interrupted.Load() && ctx.Err() != nil {
			return renderer.Result{}, ctx.Err()
		}
		return renderer.Result{}, formatGojaError(err)
	}

	var result renderer.Result
	if container.structuredABI {
		result, err = decodeStructuredResult(rt, resultVal)
		if err != nil {
			return renderer.Result{}, err
		}
	} else {
		headVal := rt.Get("__SSR_HEAD__")
		head := ""
		if headVal != nil && !goja.IsNull(headVal) && !goja.IsUndefined(headVal) {
			head = headVal.String()
		}
		result = renderer.Result{
			HTML: resultVal.String(),
			Head: head,
		}
	}

	healthy = true
	return result, nil
}

func renderPayloadValue(container *runtimeContainer, payload map[string]any) (goja.Value, error) {
	rt := container.runtime
	if payload == nil {
		payload = map[string]any{}
	}
	payloadValue, native, err := nativePayloadValue(rt, payload)
	if err != nil {
		return nil, fmt.Errorf("encode SSR payload: %w", err)
	}
	if native {
		return payloadValue, nil
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode SSR payload: %w", err)
	}
	payloadValue, err = container.parseJSON(goja.Undefined(), rt.ToValue(string(payloadJSON)))
	if err != nil {
		return nil, fmt.Errorf("parse SSR payload: %w", formatGojaError(err))
	}
	return payloadValue, nil
}

func decodeStructuredResult(rt *goja.Runtime, value goja.Value) (renderer.Result, error) {
	if value == nil || goja.IsNull(value) || goja.IsUndefined(value) {
		return renderer.Result{}, errors.New("structured ssrRender returned no result")
	}
	object := value.ToObject(rt)
	if object.ClassName() != "Object" {
		return renderer.Result{}, fmt.Errorf("structured ssrRender returned %s, want object", object.ClassName())
	}

	html, err := requiredResultString(object.Get("html"), "html")
	if err != nil {
		return renderer.Result{}, err
	}
	head, err := optionalResultString(object.Get("head"), "head")
	if err != nil {
		return renderer.Result{}, err
	}
	status, err := optionalResultInt(object.Get("status"), "status")
	if err != nil {
		return renderer.Result{}, err
	}

	result := renderer.Result{HTML: html, Head: head, Status: status}
	redirectValue := object.Get("redirect")
	if redirectValue == nil || goja.IsNull(redirectValue) || goja.IsUndefined(redirectValue) {
		return result, nil
	}
	redirectObject := redirectValue.ToObject(rt)
	if redirectObject.ClassName() != "Object" {
		return renderer.Result{}, errors.New("structured ssrRender redirect must be an object")
	}
	location, err := requiredResultString(redirectObject.Get("location"), "redirect.location")
	if err != nil {
		return renderer.Result{}, err
	}
	redirectStatus, err := optionalResultInt(redirectObject.Get("status"), "redirect.status")
	if err != nil {
		return renderer.Result{}, err
	}
	result.Redirect = &renderer.Redirect{Status: redirectStatus, Location: location}
	return result, nil
}

func requiredResultString(value goja.Value, name string) (string, error) {
	if value == nil || goja.IsNull(value) || goja.IsUndefined(value) {
		return "", fmt.Errorf("structured ssrRender result.%s is required", name)
	}
	exported, ok := value.Export().(string)
	if !ok {
		return "", fmt.Errorf("structured ssrRender result.%s must be a string", name)
	}
	return exported, nil
}

func optionalResultString(value goja.Value, name string) (string, error) {
	if value == nil || goja.IsNull(value) || goja.IsUndefined(value) {
		return "", nil
	}
	exported, ok := value.Export().(string)
	if !ok {
		return "", fmt.Errorf("structured ssrRender result.%s must be a string", name)
	}
	return exported, nil
}

func optionalResultInt(value goja.Value, name string) (int, error) {
	if value == nil || goja.IsNull(value) || goja.IsUndefined(value) {
		return 0, nil
	}
	number, ok := value.Export().(int64)
	if ok {
		return int(number), nil
	}
	float, ok := value.Export().(float64)
	if !ok || math.Trunc(float) != float {
		return 0, fmt.Errorf("structured ssrRender result.%s must be an integer", name)
	}
	return int(float), nil
}
