package pool

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Callbacks 定义池在不同生命周期阶段的行为。
type Callbacks[T any] struct {
	Create     func() T
	Reset      func(T)
	Dispose    func(T)
	ClosedErr  func() error
	TimeoutErr func(time.Duration) error
}

// Bounded 提供带容量上限、超时和关闭语义的通用资源池。
type Bounded[T any] struct {
	pool        chan T
	maxSize     int
	currentSize int
	mu          sync.Mutex
	timeout     time.Duration
	closed      bool
	done        chan struct{}
	callbacks   Callbacks[T]
}

// NewBounded 创建一个有界资源池。
func NewBounded[T any](size int, timeout time.Duration, callbacks Callbacks[T]) *Bounded[T] {
	if size <= 0 {
		panic("pool size must be greater than zero")
	}
	if callbacks.Create == nil {
		panic("pool create callback is required")
	}
	if callbacks.Reset == nil {
		callbacks.Reset = func(T) {}
	}
	if callbacks.Dispose == nil {
		callbacks.Dispose = func(T) {}
	}
	if callbacks.ClosedErr == nil {
		callbacks.ClosedErr = func() error { return fmt.Errorf("resource pool is closed") }
	}
	if callbacks.TimeoutErr == nil {
		callbacks.TimeoutErr = func(timeout time.Duration) error {
			return fmt.Errorf("pool timeout after %v", timeout)
		}
	}

	return &Bounded[T]{
		pool:      make(chan T, size),
		maxSize:   size,
		timeout:   timeout,
		done:      make(chan struct{}),
		callbacks: callbacks,
	}
}

// Warmup 预创建指定数量的资源并放入池中。
func (p *Bounded[T]) Warmup(count int) {
	if count <= 0 {
		return
	}

	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			resource := p.callbacks.Create()

			p.mu.Lock()
			if p.closed {
				p.mu.Unlock()
				p.callbacks.Dispose(resource)
				return
			}
			p.currentSize++
			p.mu.Unlock()

			select {
			case p.pool <- resource:
			case <-p.done:
				p.mu.Lock()
				if p.currentSize > 0 {
					p.currentSize--
				}
				p.mu.Unlock()
				p.callbacks.Dispose(resource)
			}
		}()
	}
	wg.Wait()
}

// Get 从池中获取资源，支持上下文取消和等待超时。
func (p *Bounded[T]) Get(ctx context.Context) (T, error) {
	var zero T

	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-p.done:
		return zero, p.callbacks.ClosedErr()
	default:
	}

	// 先尝试非阻塞获取
	select {
	case resource := <-p.pool:
		return resource, nil
	default:
	}

	// 尝试动态创建资源
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return zero, p.callbacks.ClosedErr()
	}
	if p.currentSize < p.maxSize {
		p.currentSize++
		p.mu.Unlock()
		return p.callbacks.Create(), nil
	}
	p.mu.Unlock()

	waitCtx := ctx
	cancel := func() {}
	if p.timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, p.timeout)
	}
	defer cancel()

	select {
	case <-p.done:
		return zero, p.callbacks.ClosedErr()
	case resource := <-p.pool:
		return resource, nil
	case <-waitCtx.Done():
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		return zero, p.callbacks.TimeoutErr(p.timeout)
	}
}

// Put 归还资源到池中。
func (p *Bounded[T]) Put(resource T) {
	p.callbacks.Reset(resource)

	p.mu.Lock()
	if p.closed {
		if p.currentSize > 0 {
			p.currentSize--
		}
		p.mu.Unlock()
		p.callbacks.Dispose(resource)
		return
	}

	select {
	case p.pool <- resource:
		p.mu.Unlock()
	default:
		if p.currentSize > 0 {
			p.currentSize--
		}
		p.mu.Unlock()
		p.callbacks.Dispose(resource)
	}
}

// Discard 丢弃资源并减少计数。
func (p *Bounded[T]) Discard(resource T) {
	p.mu.Lock()
	if p.currentSize > 0 {
		p.currentSize--
	}
	p.mu.Unlock()
	p.callbacks.Dispose(resource)
}

// Close 关闭池并释放池中资源。
func (p *Bounded[T]) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.done)

	drained := make([]T, 0, len(p.pool))
	for {
		select {
		case resource := <-p.pool:
			drained = append(drained, resource)
		default:
			p.currentSize -= len(drained)
			if p.currentSize < 0 {
				p.currentSize = 0
			}
			p.mu.Unlock()

			for _, resource := range drained {
				p.callbacks.Dispose(resource)
			}
			return
		}
	}
}
