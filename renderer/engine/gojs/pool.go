package gojs

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

const (
	minGojaPoolSize       = 1
	maxGojaPoolSize       = 512
	defaultRuntimeMaxUses = 200
	maxRuntimeMaxUses     = 1_000_000
	// gojaPoolWaitTimeout 兜底没有请求级 deadline 的直接调用方;经过
	// gossr Runtime 的请求总是先被 admission 的更短超时取消。
	gojaPoolWaitTimeout = 5 * time.Second
)

// runtimePool 是带容量上限、动态创建和关闭语义的 goja runtime 池。
type runtimePool struct {
	program *goja.Program
	maxUses int

	mu          sync.Mutex
	idle        chan *runtimeContainer
	maxSize     int
	currentSize int
	closed      bool
	done        chan struct{}
}

type runtimeContainer struct {
	runtime    *goja.Runtime
	renderFunc goja.Callable
	parseJSON  goja.Callable
	uses       int
}

// newRuntimePool 创建预热的 Goja runtime 池。
func newRuntimePool(program *goja.Program) *runtimePool {
	defaultPoolSize := runtime.GOMAXPROCS(0)
	if defaultPoolSize < minGojaPoolSize {
		defaultPoolSize = minGojaPoolSize
	}
	if defaultPoolSize > maxGojaPoolSize {
		defaultPoolSize = maxGojaPoolSize
	}

	// 池大小优先从环境变量读取，否则与 Go 可用并行度保持一致。
	poolSize := parseGojaPoolSize(defaultPoolSize)

	p := &runtimePool{
		program: program,
		maxUses: parseRuntimeMaxUses(defaultRuntimeMaxUses),
		idle:    make(chan *runtimeContainer, poolSize),
		maxSize: poolSize,
		done:    make(chan struct{}),
	}

	// 只预热首个 runtime，其余按并发需求惰性创建，避免大 bundle 的启动峰值。
	p.mu.Lock()
	p.currentSize++
	p.mu.Unlock()
	p.idle <- p.createReserved()

	return p
}

// createReserved 创建已占用容量的 runtime；创建 panic 时回滚容量后继续抛出。
func (p *runtimePool) createReserved() (container *runtimeContainer) {
	defer func() {
		if recovered := recover(); recovered != nil {
			p.mu.Lock()
			if p.currentSize > 0 {
				p.currentSize--
			}
			p.mu.Unlock()
			panic(recovered)
		}
	}()
	return p.createRuntime()
}

func parseRuntimeMaxUses(defaultMaxUses int) int {
	raw := strings.TrimSpace(os.Getenv("GOJA_RUNTIME_MAX_RENDERS"))
	if raw == "" {
		return defaultMaxUses
	}

	maxUses, err := strconv.Atoi(raw)
	if err != nil || maxUses < 0 {
		log.Printf("config: invalid GOJA_RUNTIME_MAX_RENDERS=%q, use default %d", raw, defaultMaxUses)
		return defaultMaxUses
	}
	if maxUses > maxRuntimeMaxUses {
		log.Printf("config: GOJA_RUNTIME_MAX_RENDERS=%d exceeds max %d, clamped", maxUses, maxRuntimeMaxUses)
		return maxRuntimeMaxUses
	}
	return maxUses
}

func parseGojaPoolSize(defaultSize int) int {
	raw := strings.TrimSpace(os.Getenv("GOJA_POOL_SIZE"))
	if raw == "" {
		return defaultSize
	}

	size, err := strconv.Atoi(raw)
	if err != nil || size <= 0 {
		log.Printf("config: invalid GOJA_POOL_SIZE=%q, use default %d", raw, defaultSize)
		return defaultSize
	}
	if size < minGojaPoolSize {
		log.Printf("config: GOJA_POOL_SIZE=%d below min %d, clamped", size, minGojaPoolSize)
		return minGojaPoolSize
	}
	if size > maxGojaPoolSize {
		log.Printf("config: GOJA_POOL_SIZE=%d exceeds max %d, clamped", size, maxGojaPoolSize)
		return maxGojaPoolSize
	}
	return size
}

// createRuntime 创建新的 Goja runtime。
func (p *runtimePool) createRuntime() *runtimeContainer {
	rt := goja.New()
	global := rt.GlobalObject()
	_ = global.Set("globalThis", global)
	_ = global.Set("global", global)
	installBase64Polyfills(rt, global)
	installIntlPolyfill(rt, global)

	// 注入 console polyfill (goja 默认不提供)
	console := rt.NewObject()
	noop := func(call goja.FunctionCall) goja.Value { return goja.Undefined() }
	_ = console.Set("log", noop)
	_ = console.Set("info", noop)
	_ = console.Set("warn", noop)
	_ = console.Set("error", noop)
	_ = console.Set("debug", noop)
	_ = console.Set("trace", noop)
	_ = global.Set("console", console)

	if _, err := rt.RunProgram(p.program); err != nil {
		panic("failed to run SSR program: " + err.Error())
	}

	renderFunc, ok := goja.AssertFunction(rt.Get("ssrRender"))
	if !ok {
		panic("ssrRender is not a function")
	}
	abiValue := rt.Get("__GOSSR_RENDER_ABI__")
	if abiValue == nil || goja.IsNull(abiValue) || goja.IsUndefined(abiValue) ||
		abiValue.ToInteger() != 2 {
		panic("SSR bundle must declare __GOSSR_RENDER_ABI__ = 2")
	}
	parseJSON, ok := goja.AssertFunction(rt.Get("JSON").ToObject(rt).Get("parse"))
	if !ok {
		panic("JSON.parse is not a function")
	}
	return &runtimeContainer{
		runtime:    rt,
		renderFunc: renderFunc,
		parseJSON:  parseJSON,
	}
}

func installBase64Polyfills(rt *goja.Runtime, global *goja.Object) {
	_ = global.Set("atob", func(call goja.FunctionCall) goja.Value {
		raw := call.Argument(0).String()
		raw = strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\t', '\n', '\r', '\f':
				return -1
			default:
				return r
			}
		}, raw)

		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(raw)
		}
		if err != nil {
			panic(rt.NewTypeError("invalid base64 input"))
		}

		latin1 := make([]rune, len(decoded))
		for i, value := range decoded {
			latin1[i] = rune(value)
		}
		return rt.ToValue(string(latin1))
	})

	_ = global.Set("btoa", func(call goja.FunctionCall) goja.Value {
		raw := call.Argument(0).String()
		bytes := make([]byte, 0, len(raw))
		for _, value := range raw {
			if value > 0xff {
				panic(rt.NewTypeError("btoa input contains characters outside Latin-1"))
			}
			bytes = append(bytes, byte(value))
		}
		return rt.ToValue(base64.StdEncoding.EncodeToString(bytes))
	})
}

func (p *runtimePool) resetRuntime(container *runtimeContainer) {
	if container == nil {
		return
	}

	// runtime 可能被 Interrupt 过，归还前必须清理中断标记。
	// ABI v2 的 payload 只作为函数参数传入，没有需要清理的请求级全局。
	container.runtime.ClearInterrupt()
}

// Get 从池中获取 runtime，支持超时、上下文取消和动态创建。
func (p *runtimePool) Get(ctx context.Context) (*runtimeContainer, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	select {
	case <-p.done:
		return nil, fmt.Errorf("goja runtime pool is closed")
	default:
	}

	// 先尝试非阻塞获取。
	select {
	case container := <-p.idle:
		return container, nil
	default:
	}

	// 未达容量上限时动态创建。
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("goja runtime pool is closed")
	}
	if p.currentSize < p.maxSize {
		p.currentSize++
		p.mu.Unlock()
		return p.createReserved(), nil
	}
	p.mu.Unlock()

	waitCtx, cancel := context.WithTimeout(ctx, gojaPoolWaitTimeout)
	defer cancel()

	select {
	case <-p.done:
		return nil, fmt.Errorf("goja runtime pool is closed")
	case container := <-p.idle:
		return container, nil
	case <-waitCtx.Done():
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("goja pool timeout after %v", gojaPoolWaitTimeout)
	}
}

// Put 归还 runtime 到池中；达到复用上限或池已满/已关闭时丢弃（交给 GC）。
func (p *runtimePool) Put(container *runtimeContainer) {
	if container == nil {
		return
	}
	if p.maxUses > 0 && container.uses >= p.maxUses {
		p.Discard(container)
		return
	}
	p.resetRuntime(container)

	p.mu.Lock()
	if p.closed {
		if p.currentSize > 0 {
			p.currentSize--
		}
		p.mu.Unlock()
		return
	}
	select {
	case p.idle <- container:
		p.mu.Unlock()
	default:
		if p.currentSize > 0 {
			p.currentSize--
		}
		p.mu.Unlock()
	}
}

// Discard 丢弃 runtime（不归还池），并更新池计数。
func (p *runtimePool) Discard(container *runtimeContainer) {
	if container == nil {
		return
	}
	p.mu.Lock()
	if p.currentSize > 0 {
		p.currentSize--
	}
	p.mu.Unlock()
}

// Close 关闭池并清空空闲 runtime；可安全重复调用。
func (p *runtimePool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.done)

	drained := 0
	for {
		select {
		case <-p.idle:
			drained++
		default:
			p.currentSize -= drained
			if p.currentSize < 0 {
				p.currentSize = 0
			}
			p.mu.Unlock()
			return
		}
	}
}
