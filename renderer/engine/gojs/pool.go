package gojs

import (
	"context"
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
	minGojaPoolSize        = 8
	maxGojaPoolSize        = 512
	defaultGojaPoolTimeout = 5 * time.Second
	maxGojaPoolTimeout     = 30 * time.Second
)

// runtimePool 支持动态扩缩容的有界池。
type runtimePool struct {
	program *goja.Program
	bounded *internalpool.Bounded[*goja.Runtime]
}

// newRuntimePool 创建预热的 Goja runtime 池。
func newRuntimePool(program *goja.Program) *runtimePool {
	defaultPoolSize := runtime.NumCPU() * 4
	if defaultPoolSize < minGojaPoolSize {
		defaultPoolSize = minGojaPoolSize
	}
	if defaultPoolSize > maxGojaPoolSize {
		defaultPoolSize = maxGojaPoolSize
	}

	// 池大小优先从环境变量读取，否则使用 CPU 核心数 * 4。
	poolSize := parseGojaPoolSize(defaultPoolSize)
	// 获取超时配置 (默认 5 秒)。
	timeout := parseGojaPoolTimeout(defaultGojaPoolTimeout)

	p := &runtimePool{program: program}
	p.bounded = internalpool.NewBounded[*goja.Runtime](
		poolSize,
		timeout,
		internalpool.Callbacks[*goja.Runtime]{
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

	// 预热：启动时创建一半的 runtime
	p.bounded.Warmup(poolSize / 2)

	return p
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
func (p *runtimePool) createRuntime() *goja.Runtime {
	rt := goja.New()
	global := rt.GlobalObject()
	_ = global.Set("globalThis", global)
	_ = global.Set("global", global)

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

	return rt
}

func (p *runtimePool) resetRuntime(rt *goja.Runtime) {
	if rt == nil {
		return
	}

	// runtime 可能被 Interrupt 过，归还前必须清理中断标记。
	rt.ClearInterrupt()

	// 清理 per-request 数据
	_ = rt.Set("__SSR_DATA__", goja.Undefined())
	_ = rt.Set("__SSR_HEAD__", goja.Undefined())
}

// Get 从池中获取 runtime，支持超时、上下文取消和动态创建。
func (p *runtimePool) Get(ctx context.Context) (*goja.Runtime, error) {
	return p.bounded.Get(ctx)
}

// Put 归还 runtime 到池中。
func (p *runtimePool) Put(rt *goja.Runtime) {
	if rt == nil {
		return
	}
	p.bounded.Put(rt)
}

// Discard 丢弃 runtime（不归还池），并更新池计数。
func (p *runtimePool) Discard(rt *goja.Runtime) {
	if rt == nil {
		return
	}
	p.bounded.Discard(rt)
}

// Close 关闭池。
func (p *runtimePool) Close() {
	p.bounded.Close()
}
