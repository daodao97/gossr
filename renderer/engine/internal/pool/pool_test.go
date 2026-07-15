package pool

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestWarmupClampsToPoolCapacity(t *testing.T) {
	var created atomic.Int32
	var disposed atomic.Int32
	p := NewBounded[int](2, 0, Callbacks[int]{
		Create: func() int {
			return int(created.Add(1))
		},
		Reset: func(int) {},
		Dispose: func(int) {
			disposed.Add(1)
		},
	})

	p.Warmup(10)
	if got := created.Load(); got != 2 {
		t.Fatalf("warmup created %d resources, want 2", got)
	}
	if p.currentSize != 2 || len(p.pool) != 2 {
		t.Fatalf("unexpected pool state: currentSize=%d idle=%d", p.currentSize, len(p.pool))
	}

	p.Close()
	if got := disposed.Load(); got != 2 {
		t.Fatalf("close disposed %d resources, want 2", got)
	}
}

func TestCreatePanicRollsBackCapacity(t *testing.T) {
	var attempts atomic.Int32
	p := NewBounded[int](1, 0, Callbacks[int]{
		Create: func() int {
			if attempts.Add(1) == 1 {
				panic("create failed")
			}
			return 42
		},
	})
	t.Cleanup(p.Close)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected create panic")
			}
		}()
		_, _ = p.Get(context.Background())
	}()

	if p.currentSize != 0 {
		t.Fatalf("create panic leaked capacity: currentSize=%d", p.currentSize)
	}
	resource, err := p.Get(context.Background())
	if err != nil || resource != 42 {
		t.Fatalf("pool did not recover after create panic: resource=%d err=%v", resource, err)
	}
	p.Put(resource)
}

func TestGetRejectsAlreadyCanceledContext(t *testing.T) {
	var created atomic.Int32
	p := NewBounded[int](1, 0, Callbacks[int]{
		Create: func() int { return int(created.Add(1)) },
	})
	p.Warmup(1)
	t.Cleanup(p.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Get(ctx); err != context.Canceled {
		t.Fatalf("Get error=%v, want context.Canceled", err)
	}
	if got := len(p.pool); got != 1 {
		t.Fatalf("canceled Get consumed idle resource: idle=%d", got)
	}
	if got := created.Load(); got != 1 {
		t.Fatalf("canceled Get created resource: created=%d", got)
	}
}
