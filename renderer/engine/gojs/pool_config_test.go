package gojs

import (
	"context"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func TestParseGojaPoolSize(t *testing.T) {
	const defaultSize = 32

	tests := []struct {
		name string
		env  string
		want int
	}{
		{name: "default when empty", env: "", want: defaultSize},
		{name: "invalid fallback", env: "abc", want: defaultSize},
		{name: "minimum value", env: "1", want: minGojaPoolSize},
		{name: "above max clamp", env: "9999", want: maxGojaPoolSize},
		{name: "valid value", env: "64", want: 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOJA_POOL_SIZE", tt.env)
			if got := parseGojaPoolSize(defaultSize); got != tt.want {
				t.Fatalf("parseGojaPoolSize(%q)=%d, want %d", tt.env, got, tt.want)
			}
		})
	}
}

func TestParseRuntimeMaxUses(t *testing.T) {
	const defaultMaxUses = 1000

	tests := []struct {
		name string
		env  string
		want int
	}{
		{name: "default when empty", env: "", want: defaultMaxUses},
		{name: "invalid fallback", env: "abc", want: defaultMaxUses},
		{name: "negative fallback", env: "-1", want: defaultMaxUses},
		{name: "zero disables recycling", env: "0", want: 0},
		{name: "above max clamp", env: "2000000", want: maxRuntimeMaxUses},
		{name: "valid value", env: "500", want: 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOJA_RUNTIME_MAX_RENDERS", tt.env)
			if got := parseRuntimeMaxUses(defaultMaxUses); got != tt.want {
				t.Fatalf("parseRuntimeMaxUses(%q)=%d, want %d", tt.env, got, tt.want)
			}
		})
	}
}

func TestRuntimePoolRecyclesContainerAtMaxUses(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	t.Setenv("GOJA_RUNTIME_MAX_RENDERS", "1")
	program, err := goja.Compile("test.js", "globalThis.__GOSSR_RENDER_ABI__ = 2; globalThis.ssrRender = function() { return { html: 'ok' } }", false)
	if err != nil {
		t.Fatalf("compile test program: %v", err)
	}

	pool := newRuntimePool(program)
	t.Cleanup(pool.Close)
	first, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("get first runtime: %v", err)
	}
	first.uses++
	pool.Put(first)

	second, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("get second runtime: %v", err)
	}
	defer pool.Put(second)
	if first == second {
		t.Fatal("runtime container was reused after reaching max renders")
	}
}

func TestRuntimePoolGetRejectsAlreadyCanceledContext(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	program, err := goja.Compile("test.js", "globalThis.__GOSSR_RENDER_ABI__ = 2; globalThis.ssrRender = function() { return { html: 'ok' } }", false)
	if err != nil {
		t.Fatalf("compile test program: %v", err)
	}

	pool := newRuntimePool(program)
	t.Cleanup(pool.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pool.Get(ctx); err != context.Canceled {
		t.Fatalf("Get error=%v, want context.Canceled", err)
	}
	if got := len(pool.idle); got != 1 {
		t.Fatalf("canceled Get consumed the warmed idle runtime: idle=%d", got)
	}
}

func TestRuntimePoolCloseStopsGetAndDropsLateReturns(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "2")
	program, err := goja.Compile("test.js", "globalThis.__GOSSR_RENDER_ABI__ = 2; globalThis.ssrRender = function() { return { html: 'ok' } }", false)
	if err != nil {
		t.Fatalf("compile test program: %v", err)
	}

	pool := newRuntimePool(program)
	held, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("get runtime: %v", err)
	}

	pool.Close()
	pool.Close()

	if _, err := pool.Get(context.Background()); err == nil {
		t.Fatal("Get succeeded after Close")
	}

	// 关闭后归还的 runtime 只应递减计数，不应回到空闲队列。
	pool.Put(held)
	if got := len(pool.idle); got != 0 {
		t.Fatalf("late Put re-entered a closed pool: idle=%d", got)
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.currentSize != 0 {
		t.Fatalf("late Put leaked capacity: currentSize=%d", pool.currentSize)
	}
}

func TestRendererPoolStatsTrackCreationAndDiscards(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	t.Setenv("GOJA_RUNTIME_MAX_RENDERS", "0")
	r := NewRenderer(`
globalThis.__GOSSR_RENDER_ABI__ = 2;
globalThis.ssrRender = function(input) {
  if (input.snapshot.spin) {
    for (;;) {}
  }
  return { html: "ok" };
};
`)
	t.Cleanup(r.pool.Close)

	if _, err := r.Render(context.Background(), "/", nil); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	stats := r.PoolStats()
	if stats.Created != 1 || stats.Discarded != 0 || stats.Size != 1 || stats.Idle != 1 {
		t.Fatalf("unexpected stats after clean render: %#v", stats)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := r.Render(ctx, "/spin", map[string]any{"spin": true}); err == nil {
		t.Fatal("expected interrupted render to fail")
	}
	stats = r.PoolStats()
	if stats.Discarded != 1 || stats.Size != 0 {
		t.Fatalf("interrupt did not register a discard: %#v", stats)
	}
}
