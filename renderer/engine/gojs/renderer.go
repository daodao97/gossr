package gojs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	panicked := true
	defer func() {
		// 必须先确认旧请求 watcher 已退出，才能让 runtime 被下一请求获取。
		stopWatch()
		// 中断会留下执行了一半的 JS，panic 后 goja 状态未知，两者都必须丢弃。
		// 干净的 JS 异常不会污染 runtime；保留它避免每次失败渲染后都重新
		// 执行整个 bundle。
		if interrupted.Load() || panicked {
			r.pool.Discard(container)
			return
		}
		r.pool.Put(container)
	}()

	result, err := r.renderInContainer(ctx, container, urlPath, payload, &interrupted)
	panicked = false
	return result, err
}

func (r *Renderer) renderInContainer(
	ctx context.Context,
	container *runtimeContainer,
	urlPath string,
	payload map[string]any,
	interrupted *atomic.Bool,
) (renderer.Result, error) {
	rt := container.runtime

	if container.renderFunc == nil {
		return renderer.Result{}, errors.New("ssrRender is not a function")
	}

	payloadValue, err := renderPayloadValue(container, payload)
	if err != nil {
		return renderer.Result{}, err
	}

	input := rt.NewObject()
	if err := input.Set("url", urlPath); err != nil {
		return renderer.Result{}, err
	}
	if err := input.Set("snapshot", payloadValue); err != nil {
		return renderer.Result{}, err
	}

	val, err := container.renderFunc(goja.Undefined(), input)
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

	return decodeStructuredResult(rt, resultVal)
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
	// HTTP 意图(status/redirect)归 PageResolver 所有,协议上不可表达;
	// 渲染结果只承载标记。
	return renderer.Result{HTML: html, Head: head}, nil
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

