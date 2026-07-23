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
	"time"

	internalpool "github.com/daodao97/gossr/renderer/engine/internal/pool"
	"github.com/dop251/goja"
)

const (
	minGojaPoolSize        = 1
	maxGojaPoolSize        = 512
	defaultGojaPoolTimeout = 5 * time.Second
	maxGojaPoolTimeout     = 30 * time.Second
	defaultRuntimeMaxUses  = 200
	maxRuntimeMaxUses      = 1_000_000
)

// runtimePool 支持动态扩缩容的有界池。
type runtimePool struct {
	program *goja.Program
	bounded *internalpool.Bounded[*runtimeContainer]
	maxUses int
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
	// 获取超时配置 (默认 5 秒)。
	timeout := parseGojaPoolTimeout(defaultGojaPoolTimeout)

	p := &runtimePool{
		program: program,
		maxUses: parseRuntimeMaxUses(defaultRuntimeMaxUses),
	}
	p.bounded = internalpool.NewBounded[*runtimeContainer](
		poolSize,
		timeout,
		internalpool.Callbacks[*runtimeContainer]{
			Create: p.createRuntime,
			Reset:  p.resetRuntime,
			ClosedErr: func() error {
				return fmt.Errorf("goja runtime pool is closed")
			},
			TimeoutErr: func(timeout time.Duration) error {
				return fmt.Errorf("goja pool timeout after %v", timeout)
			},
		},
	)

	// 只预热首个 runtime，其余按并发需求惰性创建，避免大 bundle 的启动峰值。
	p.bounded.Warmup(1)

	return p
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

func parseGojaPoolTimeout(defaultTimeout time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv("GOJA_POOL_TIMEOUT"))
	if raw == "" {
		return defaultTimeout
	}

	timeout, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("config: invalid GOJA_POOL_TIMEOUT=%q, use default %s", raw, defaultTimeout)
		return defaultTimeout
	}

	if timeout < 0 {
		log.Printf("config: GOJA_POOL_TIMEOUT=%q is negative, treated as 0", raw)
		return 0
	}
	if timeout > maxGojaPoolTimeout {
		log.Printf("config: GOJA_POOL_TIMEOUT=%s exceeds max %s, clamped", timeout, maxGojaPoolTimeout)
		return maxGojaPoolTimeout
	}
	return timeout
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
	_ = rt.Set("__SSR_DATA__", goja.Undefined())
	_ = rt.Set("__SSR_HEAD__", goja.Undefined())

	renderFunc, ok := goja.AssertFunction(rt.Get("ssrRender"))
	if !ok {
		panic("ssrRender is not a function")
	}
	parseJSON, ok := goja.AssertFunction(rt.Get("JSON").ToObject(rt).Get("parse"))
	if !ok {
		panic("JSON.parse is not a function")
	}
	return &runtimeContainer{runtime: rt, renderFunc: renderFunc, parseJSON: parseJSON}
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
	rt := container.runtime

	// runtime 可能被 Interrupt 过，归还前必须清理中断标记。
	rt.ClearInterrupt()

	// 清理每个请求注入的全局数据，避免 runtime 空闲期间保留 payload。
	_ = rt.Set("__SSR_DATA__", goja.Undefined())
	_ = rt.Set("__SSR_HEAD__", goja.Undefined())
}

// Get 从池中获取 runtime，支持超时、上下文取消和动态创建。
func (p *runtimePool) Get(ctx context.Context) (*runtimeContainer, error) {
	return p.bounded.Get(ctx)
}

// Put 归还 runtime 到池中。
func (p *runtimePool) Put(container *runtimeContainer) {
	if container == nil {
		return
	}
	if p.maxUses > 0 && container.uses >= p.maxUses {
		p.bounded.Discard(container)
		return
	}
	p.bounded.Put(container)
}

// Discard 丢弃 runtime（不归还池），并更新池计数。
func (p *runtimePool) Discard(container *runtimeContainer) {
	if container == nil {
		return
	}
	p.bounded.Discard(container)
}

// Close 关闭池。
func (p *runtimePool) Close() {
	p.bounded.Close()
}
