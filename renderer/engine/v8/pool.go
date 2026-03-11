//go:build !nov8

package v8

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
	"rogchap.com/v8go"
)

const (
	minV8PoolSize        = 8
	maxV8PoolSize        = 512
	defaultV8PoolTimeout = 5 * time.Second
	maxV8PoolTimeout     = 30 * time.Second
)

// V8IsolateContainer 包装 V8 isolate 和预编译脚本。
type V8IsolateContainer struct {
	Isolate      *v8go.Isolate
	RenderScript *v8go.UnboundScript
}

// V8IsolatePool 支持动态扩缩容的有界池。
type V8IsolatePool struct {
	ssrScriptContent string
	ssrScriptName    string
	bounded          *internalpool.Bounded[*V8IsolateContainer]
}

// NewV8IsolatePool 创建预热的 V8 isolate 池。
func NewV8IsolatePool(ssrScriptContents string, ssrScriptName string) *V8IsolatePool {
	defaultPoolSize := runtime.NumCPU() * 4
	if defaultPoolSize < minV8PoolSize {
		defaultPoolSize = minV8PoolSize
	}
	if defaultPoolSize > maxV8PoolSize {
		defaultPoolSize = maxV8PoolSize
	}

	// 池大小优先从环境变量读取，否则使用 CPU 核心数 * 4。
	poolSize := parseV8PoolSize(defaultPoolSize)
	// 获取超时配置 (默认 5 秒)。
	timeout := parseV8PoolTimeout(defaultV8PoolTimeout)

	p := &V8IsolatePool{
		ssrScriptContent: ssrScriptContents,
		ssrScriptName:    ssrScriptName,
	}
	p.bounded = internalpool.NewBounded[*V8IsolateContainer](
		poolSize,
		timeout,
		internalpool.Callbacks[*V8IsolateContainer]{
			Create: p.createIsolate,
			Dispose: func(container *V8IsolateContainer) {
				if container == nil {
					return
				}
				container.Isolate.Dispose()
			},
			ClosedErr: func() error {
				return fmt.Errorf("v8 isolate pool is closed")
			},
			TimeoutErr: func(timeout time.Duration) error {
				return fmt.Errorf("v8 pool timeout after %v", timeout)
			},
		},
	)

	// 预热：启动时创建一半的 isolate
	p.bounded.Warmup(poolSize / 2)

	return p
}

func parseV8PoolSize(defaultSize int) int {
	raw := strings.TrimSpace(os.Getenv("V8_POOL_SIZE"))
	if raw == "" {
		return defaultSize
	}

	size, err := strconv.Atoi(raw)
	if err != nil || size <= 0 {
		log.Printf("config: invalid V8_POOL_SIZE=%q, use default %d", raw, defaultSize)
		return defaultSize
	}
	if size < minV8PoolSize {
		log.Printf("config: V8_POOL_SIZE=%d below min %d, clamped", size, minV8PoolSize)
		return minV8PoolSize
	}
	if size > maxV8PoolSize {
		log.Printf("config: V8_POOL_SIZE=%d exceeds max %d, clamped", size, maxV8PoolSize)
		return maxV8PoolSize
	}
	return size
}

func parseV8PoolTimeout(defaultTimeout time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv("V8_POOL_TIMEOUT"))
	if raw == "" {
		return defaultTimeout
	}

	timeout, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("config: invalid V8_POOL_TIMEOUT=%q, use default %s", raw, defaultTimeout)
		return defaultTimeout
	}

	if timeout < 0 {
		log.Printf("config: V8_POOL_TIMEOUT=%q is negative, treated as 0", raw)
		return 0
	}
	if timeout > maxV8PoolTimeout {
		log.Printf("config: V8_POOL_TIMEOUT=%s exceeds max %s, clamped", timeout, maxV8PoolTimeout)
		return maxV8PoolTimeout
	}
	return timeout
}

// createIsolate 创建新的 V8 isolate。
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

// Get 从池中获取 isolate，支持超时、上下文取消和动态创建。
func (p *V8IsolatePool) Get(ctx context.Context) (*V8IsolateContainer, error) {
	return p.bounded.Get(ctx)
}

// Put 归还 isolate 到池中。
func (p *V8IsolatePool) Put(container *V8IsolateContainer) {
	if container == nil {
		return
	}
	p.bounded.Put(container)
}

// Discard 丢弃 isolate 并更新池计数。
func (p *V8IsolatePool) Discard(container *V8IsolateContainer) {
	if container == nil {
		return
	}
	p.bounded.Discard(container)
}

// Close 关闭池并释放所有资源。
func (p *V8IsolatePool) Close() {
	p.bounded.Close()
}
