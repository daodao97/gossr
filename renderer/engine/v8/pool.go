//go:build !nov8

package v8

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"rogchap.com/v8go"
)

// V8IsolateContainer 包装 V8 isolate 和预编译脚本
type V8IsolateContainer struct {
	Isolate      *v8go.Isolate
	RenderScript *v8go.UnboundScript
}

// V8IsolatePool 支持动态扩缩容的有界池
type V8IsolatePool struct {
	pool             chan *V8IsolateContainer
	ssrScriptContent string
	ssrScriptName    string
	maxSize          int
	currentSize      int
	mu               sync.Mutex
	timeout          time.Duration
}

// NewV8IsolatePool 创建预热的 V8 isolate 池
func NewV8IsolatePool(ssrScriptContents string, ssrScriptName string) *V8IsolatePool {
	// 池大小优先从环境变量读取，否则使用 CPU 核心数 * 4
	poolSize := runtime.NumCPU() * 4
	if envSize := os.Getenv("V8_POOL_SIZE"); envSize != "" {
		if size, err := strconv.Atoi(envSize); err == nil && size > 0 {
			poolSize = size
		}
	}
	if poolSize < 8 {
		poolSize = 8
	}

	// 获取超时配置 (默认 5 秒)
	timeout := 5 * time.Second
	if envTimeout := os.Getenv("V8_POOL_TIMEOUT"); envTimeout != "" {
		if d, err := time.ParseDuration(envTimeout); err == nil {
			timeout = d
		}
	}

	p := &V8IsolatePool{
		pool:             make(chan *V8IsolateContainer, poolSize),
		ssrScriptContent: ssrScriptContents,
		ssrScriptName:    ssrScriptName,
		maxSize:          poolSize,
		timeout:          timeout,
	}

	// 预热：启动时创建一半的 isolate
	p.warmup(poolSize / 2)

	return p
}

// warmup 预创建指定数量的 isolate
func (p *V8IsolatePool) warmup(count int) {
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			container := p.createIsolate()
			p.mu.Lock()
			p.currentSize++
			p.mu.Unlock()
			p.pool <- container
		}()
	}
	wg.Wait()
}

// createIsolate 创建新的 V8 isolate
func (p *V8IsolatePool) createIsolate() *V8IsolateContainer {
	isolate := v8go.NewIsolate()
	script, err := isolate.CompileUnboundScript(
		p.ssrScriptContent,
		p.ssrScriptName,
		v8go.CompileOptions{},
	)
	if err != nil {
		panic("failed to compile SSR script: " + err.Error())
	}

	return &V8IsolateContainer{
		Isolate:      isolate,
		RenderScript: script,
	}
}

// Get 从池中获取 isolate，支持超时和动态创建
func (p *V8IsolatePool) Get() (*V8IsolateContainer, error) {
	// 先尝试非阻塞获取
	select {
	case container := <-p.pool:
		return container, nil
	default:
	}

	// 尝试动态创建新的 isolate
	p.mu.Lock()
	if p.currentSize < p.maxSize {
		p.currentSize++
		p.mu.Unlock()
		return p.createIsolate(), nil
	}
	p.mu.Unlock()

	// 等待可用的 isolate，带超时
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	select {
	case container := <-p.pool:
		return container, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("v8 pool timeout after %v", p.timeout)
	}
}

// Put 归还 isolate 到池中
func (p *V8IsolatePool) Put(container *V8IsolateContainer) {
	if container == nil {
		return
	}
	select {
	case p.pool <- container:
		// 成功归还
	default:
		// 池满，释放多余的 isolate
		p.Discard(container)
	}
}

// Discard 丢弃 isolate 并更新池计数。
func (p *V8IsolatePool) Discard(container *V8IsolateContainer) {
	if container == nil {
		return
	}

	p.mu.Lock()
	p.currentSize--
	p.mu.Unlock()
	container.Isolate.Dispose()
}

// Close 关闭池并释放所有资源
func (p *V8IsolatePool) Close() {
	close(p.pool)
	for container := range p.pool {
		container.Isolate.Dispose()
	}
}
