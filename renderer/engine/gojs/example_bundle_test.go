package gojs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/daodao97/gossr/renderer"
)

func TestExampleBundleRetainedHeap(t *testing.T) {
	if os.Getenv("GOJS_HEAP_TEST") == "" {
		t.Skip("set GOJS_HEAP_TEST=1 to run the retained-heap diagnostic")
	}
	maxRenders := os.Getenv("GOJS_HEAP_MAX_RENDERS")
	if maxRenders == "" {
		maxRenders = "200"
	}
	t.Setenv("GOJA_RUNTIME_MAX_RENDERS", maxRenders)

	scriptPath := filepath.Join("..", "..", "..", "example", "web", "dist", "server", renderer.DefaultSSRScriptName)
	scriptBytes, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Skipf("example bundle is unavailable: %v", err)
	}
	script := string(scriptBytes)
	r := NewRenderer(script)
	t.Cleanup(r.pool.Close)
	payload := map[string]any{
		"schema_version": 1,
		"url":            "/benchmark",
		"locale":         "en",
		"session":        nil,
		"page": map[string]any{
			"kind":         "home",
			"message":      "render a representative payload",
			"generated_at": "2026-07-25T00:00:00Z",
		},
	}

	for i := 0; i < 50; i++ {
		if _, err := r.Render(context.Background(), "/benchmark", payload); err != nil {
			t.Fatalf("warm renderer: %v", err)
		}
	}

	readHeap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return stats.HeapAlloc
	}

	previous := readHeap()
	plateauBaseline := uint64(0)
	totalRenders := 50
	t.Logf("renders=%d heap_alloc=%d", totalRenders, previous)
	for _, renders := range []int{250, 500, 1000, 2000} {
		for i := 0; i < renders; i++ {
			if _, err := r.Render(context.Background(), "/benchmark", payload); err != nil {
				t.Fatalf("render: %v", err)
			}
		}
		totalRenders += renders
		current := readHeap()
		t.Logf("renders=%d heap_alloc=%d delta=%+d", totalRenders, current, int64(current)-int64(previous))
		if totalRenders == 800 {
			plateauBaseline = current
		}
		previous = current
	}

	const allowedGrowth = 2 << 20
	if previous > plateauBaseline+allowedGrowth {
		t.Fatalf("retained heap did not plateau after runtime recycling: baseline=%d final=%d", plateauBaseline, previous)
	}
}

func TestExampleBundleIsIsolatedAcrossReusedRuntime(t *testing.T) {
	scriptPath := filepath.Join("..", "..", "..", "example", "web", "dist", "server", renderer.DefaultSSRScriptName)
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Skipf("example bundle is unavailable: %v", err)
	}

	r := NewRenderer(string(script))
	t.Cleanup(r.pool.Close)

	document := func(url, message string, session map[string]any) map[string]any {
		return map[string]any{
			"schema_version": 1,
			"url":            url,
			"locale":         "en",
			"session":        session,
			"page": map[string]any{
				"kind":         "seo",
				"message":      message,
				"generated_at": "2026-07-25T00:00:00Z",
			},
		}
	}

	tests := []struct {
		name        string
		url         string
		payload     map[string]any
		wantHTML    string
		wantHead    string
		rejectValue string
	}{
		{
			name:     "english seo",
			url:      "/seo-demo?title=FirstTitle",
			payload:  document("/seo-demo?title=FirstTitle", "first message", nil),
			wantHTML: "FirstTitle",
			wantHead: "FirstTitle",
		},
		{
			name:        "second seo replaces previous route state",
			url:         "/seo-demo?title=SecondTitle",
			payload:     document("/seo-demo?title=SecondTitle", "second message", nil),
			wantHTML:    "SecondTitle",
			wantHead:    "SecondTitle",
			rejectValue: "FirstTitle",
		},
		{
			name: "authenticated session",
			url:  "/session-demo",
			payload: document("/session-demo", "session message", map[string]any{
				"user": map[string]any{"email": "first@example.com"},
			}),
			wantHTML: "first@example.com",
		},
		{
			name:        "anonymous request does not inherit session",
			url:         "/",
			payload:     document("/", "anonymous", nil),
			wantHTML:    "anonymous",
			rejectValue: "first@example.com",
		},
	}

	for iteration := 0; iteration < 3; iteration++ {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result, err := r.Render(context.Background(), tt.url, tt.payload)
				if err != nil {
					t.Fatalf("render %s: %v", tt.url, err)
				}
				if !strings.Contains(result.HTML, tt.wantHTML) {
					t.Fatalf("HTML does not contain %q: %s", tt.wantHTML, result.HTML)
				}
				if tt.wantHead != "" && !strings.Contains(result.Head, tt.wantHead) {
					t.Fatalf("head does not contain %q: %s", tt.wantHead, result.Head)
				}
				if tt.rejectValue != "" && (strings.Contains(result.HTML, tt.rejectValue) || strings.Contains(result.Head, tt.rejectValue)) {
					t.Fatalf("response leaked previous value %q: html=%s head=%s", tt.rejectValue, result.HTML, result.Head)
				}
			})
		}
	}
}
