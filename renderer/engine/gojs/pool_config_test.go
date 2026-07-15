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

func TestParseGojaPoolTimeout(t *testing.T) {
	defaultTimeout := defaultGojaPoolTimeout

	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "default when empty", env: "", want: defaultTimeout},
		{name: "invalid fallback", env: "abc", want: defaultTimeout},
		{name: "negative to zero", env: "-1s", want: 0},
		{name: "above max clamp", env: "60s", want: maxGojaPoolTimeout},
		{name: "valid timeout", env: "250ms", want: 250 * time.Millisecond},
		{name: "no unit fallback", env: "5", want: defaultTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOJA_POOL_TIMEOUT", tt.env)
			if got := parseGojaPoolTimeout(defaultTimeout); got != tt.want {
				t.Fatalf("parseGojaPoolTimeout(%q)=%s, want %s", tt.env, got, tt.want)
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
	program, err := goja.Compile("test.js", "globalThis.ssrRender = function() { return 'ok' }", false)
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
