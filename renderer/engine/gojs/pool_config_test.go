package gojs

import (
	"testing"
	"time"
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
		{name: "below min clamp", env: "1", want: minGojaPoolSize},
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
