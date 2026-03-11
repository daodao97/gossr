//go:build !nov8

package v8

import (
	"testing"
	"time"
)

func TestParseV8PoolSize(t *testing.T) {
	const defaultSize = 32

	tests := []struct {
		name string
		env  string
		want int
	}{
		{name: "default when empty", env: "", want: defaultSize},
		{name: "invalid fallback", env: "abc", want: defaultSize},
		{name: "below min clamp", env: "1", want: minV8PoolSize},
		{name: "above max clamp", env: "9999", want: maxV8PoolSize},
		{name: "valid value", env: "64", want: 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("V8_POOL_SIZE", tt.env)
			if got := parseV8PoolSize(defaultSize); got != tt.want {
				t.Fatalf("parseV8PoolSize(%q)=%d, want %d", tt.env, got, tt.want)
			}
		})
	}
}

func TestParseV8PoolTimeout(t *testing.T) {
	defaultTimeout := defaultV8PoolTimeout

	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "default when empty", env: "", want: defaultTimeout},
		{name: "invalid fallback", env: "abc", want: defaultTimeout},
		{name: "negative to zero", env: "-1s", want: 0},
		{name: "above max clamp", env: "60s", want: maxV8PoolTimeout},
		{name: "valid timeout", env: "250ms", want: 250 * time.Millisecond},
		{name: "no unit fallback", env: "5", want: defaultTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("V8_POOL_TIMEOUT", tt.env)
			if got := parseV8PoolTimeout(defaultTimeout); got != tt.want {
				t.Fatalf("parseV8PoolTimeout(%q)=%s, want %s", tt.env, got, tt.want)
			}
		})
	}
}
