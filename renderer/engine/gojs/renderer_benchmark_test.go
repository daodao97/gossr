package gojs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/daodao97/gossr/renderer"
)

const benchmarkScript = `
globalThis.ssrRender = function(url) {
  const data = globalThis.__SSR_DATA__ || {};
  globalThis.__SSR_HEAD__ = "<title>" + (data.title || "benchmark") + "</title>";
  return "<main><h1>" + (data.message || "") + "</h1><p>" + url + "</p></main>";
};
`

var benchmarkPayload = map[string]any{
	"title":   "gojs benchmark",
	"message": "render a representative payload",
	"items": []any{
		map[string]any{"id": 1, "name": "alpha"},
		map[string]any{"id": 2, "name": "beta"},
		map[string]any{"id": 3, "name": "gamma"},
	},
}

var retainedBenchmarkRenderer *Renderer

func benchmarkRender(b *testing.B, script string, parallel bool) {
	b.Helper()
	r := NewRenderer(script)
	b.Cleanup(r.pool.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ensure startup, compilation and lazy runtime creation are outside the hot-path metric.
	if _, err := r.Render(ctx, "/benchmark", benchmarkPayload); err != nil {
		b.Fatalf("warm renderer: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	if parallel {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if _, err := r.Render(ctx, "/benchmark?q=gojs", benchmarkPayload); err != nil {
					b.Fatalf("render: %v", err)
				}
			}
		})
		return
	}

	for i := 0; i < b.N; i++ {
		if _, err := r.Render(ctx, "/benchmark?q=gojs", benchmarkPayload); err != nil {
			b.Fatalf("render: %v", err)
		}
	}
}

func BenchmarkRendererSynthetic(b *testing.B) {
	benchmarkRender(b, benchmarkScript, false)
}

func BenchmarkRendererSyntheticParallel(b *testing.B) {
	benchmarkRender(b, benchmarkScript, true)
}

func BenchmarkRendererExampleBundle(b *testing.B) {
	scriptPath := filepath.Join("..", "..", "..", "example", "web", "dist", "server", renderer.DefaultSSRScriptName)
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		b.Skipf("example bundle is unavailable: %v", err)
	}
	benchmarkRender(b, string(script), false)
}

func BenchmarkRendererExampleBundleParallel(b *testing.B) {
	scriptPath := filepath.Join("..", "..", "..", "example", "web", "dist", "server", renderer.DefaultSSRScriptName)
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		b.Skipf("example bundle is unavailable: %v", err)
	}
	benchmarkRender(b, string(script), true)
}

func BenchmarkRendererStartupSynthetic(b *testing.B) {
	benchmarkRendererStartup(b, benchmarkScript)
}

func BenchmarkRendererStartupExampleBundle(b *testing.B) {
	scriptPath := filepath.Join("..", "..", "..", "example", "web", "dist", "server", renderer.DefaultSSRScriptName)
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		b.Skipf("example bundle is unavailable: %v", err)
	}
	benchmarkRendererStartup(b, string(script))
}

// BenchmarkRendererExampleBundleRetained keeps the renderer reachable so an
// in-use memory profile can detect state accumulated inside a reused runtime.
func BenchmarkRendererExampleBundleRetained(b *testing.B) {
	scriptPath := filepath.Join("..", "..", "..", "example", "web", "dist", "server", renderer.DefaultSSRScriptName)
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		b.Skipf("example bundle is unavailable: %v", err)
	}

	r := NewRenderer(string(script))
	retainedBenchmarkRenderer = r
	if _, err := r.Render(context.Background(), "/benchmark", benchmarkPayload); err != nil {
		b.Fatalf("warm renderer: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Render(context.Background(), "/benchmark?q=gojs", benchmarkPayload); err != nil {
			b.Fatalf("render: %v", err)
		}
	}
}

func benchmarkRendererStartup(b *testing.B, script string) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := NewRenderer(script)
		r.pool.Close()
	}
}
