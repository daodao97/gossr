package gojs

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// runtimePool 支持动态扩缩容的有界池
type runtimePool struct {
	pool        chan *goja.Runtime
	program     *goja.Program
	maxSize     int
	currentSize int
	mu          sync.Mutex
	timeout     time.Duration
}

// newRuntimePool 创建预热的 Goja runtime 池
func newRuntimePool(program *goja.Program) *runtimePool {
	// 池大小优先从环境变量读取，否则使用 CPU 核心数 * 4
	poolSize := runtime.NumCPU() * 4
	if envSize := os.Getenv("GOJA_POOL_SIZE"); envSize != "" {
		if size, err := strconv.Atoi(envSize); err == nil && size > 0 {
			poolSize = size
		}
	}
	if poolSize < 8 {
		poolSize = 8
	}

	// 获取超时配置 (默认 5 秒)
	timeout := 5 * time.Second
	if envTimeout := os.Getenv("GOJA_POOL_TIMEOUT"); envTimeout != "" {
		if d, err := time.ParseDuration(envTimeout); err == nil {
			timeout = d
		}
	}

	p := &runtimePool{
		pool:    make(chan *goja.Runtime, poolSize),
		program: program,
		maxSize: poolSize,
		timeout: timeout,
	}

	// 预热：启动时创建一半的 runtime
	p.warmup(poolSize / 2)

	return p
}

// warmup 预创建指定数量的 runtime
func (p *runtimePool) warmup(count int) {
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt := p.createRuntime()
			p.mu.Lock()
			p.currentSize++
			p.mu.Unlock()
			p.pool <- rt
		}()
	}
	wg.Wait()
}

// createRuntime 创建新的 Goja runtime
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

// Get 从池中获取 runtime，支持超时和动态创建
func (p *runtimePool) Get() (*goja.Runtime, error) {
	// 先尝试非阻塞获取
	select {
	case rt := <-p.pool:
		return rt, nil
	default:
	}

	// 尝试动态创建新的 runtime
	p.mu.Lock()
	if p.currentSize < p.maxSize {
		p.currentSize++
		p.mu.Unlock()
		return p.createRuntime(), nil
	}
	p.mu.Unlock()

	// 等待可用的 runtime，带超时
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	select {
	case rt := <-p.pool:
		return rt, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("goja pool timeout after %v", p.timeout)
	}
}

// Put 归还 runtime 到池中
func (p *runtimePool) Put(rt *goja.Runtime) {
	if rt == nil {
		return
	}

	// runtime 可能被 Interrupt 过，归还前必须清理中断标记。
	rt.ClearInterrupt()

	// 清理 per-request 数据
	_ = rt.Set("__SSR_DATA__", goja.Undefined())
	_ = rt.Set("__SSR_HEAD__", goja.Undefined())

	select {
	case p.pool <- rt:
		// 成功归还
	default:
		// 池满，减少计数（让 GC 回收）
		p.mu.Lock()
		p.currentSize--
		p.mu.Unlock()
	}
}

// Close 关闭池
func (p *runtimePool) Close() {
	close(p.pool)
}
