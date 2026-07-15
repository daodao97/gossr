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

	// 注入 SSR 数据
	if len(payload) > 0 {
		payloadValue, native, err := nativePayloadValue(rt, payload)
		if err != nil {
			return renderer.Result{}, fmt.Errorf("encode SSR payload: %w", err)
		}
		if !native {
			payloadJSON, err := json.Marshal(payload)
			if err != nil {
				return renderer.Result{}, fmt.Errorf("encode SSR payload: %w", err)
			}
			payloadValue, err = container.parseJSON(goja.Undefined(), rt.ToValue(string(payloadJSON)))
			if err != nil {
				return renderer.Result{}, fmt.Errorf("parse SSR payload: %w", formatGojaError(err))
			}
		}
		if err := rt.Set("__SSR_DATA__", payloadValue); err != nil {
			return renderer.Result{}, err
		}
	} else {
		_ = rt.Set("__SSR_DATA__", goja.Undefined())
	}

	if container.renderFunc == nil {
		return renderer.Result{}, errors.New("ssrRender is not a function")
	}

	val, err := container.renderFunc(goja.Undefined(), rt.ToValue(urlPath))
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

	headVal := rt.Get("__SSR_HEAD__")
	head := ""
	if headVal != nil && !goja.IsNull(headVal) && !goja.IsUndefined(headVal) {
		head = headVal.String()
	}

	result := renderer.Result{
		HTML: resultVal.String(),
		Head: head,
	}
	healthy = true
	return result, nil
}
