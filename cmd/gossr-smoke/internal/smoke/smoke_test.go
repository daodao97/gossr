package smoke

import (
	"context"
	"strings"
	"testing"
)

const testBundle = `
globalThis.__GOSSR_RENDER_ABI__ = 2;
globalThis.ssrRender = function(input) {
  return { html: "<main>" + input.url + ":" + input.snapshot.page.kind + "</main>" };
};
`

func TestRunAssertsExpectedContent(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	snapshot := map[string]any{"page": map[string]any{"kind": "not_found"}}

	if err := Run(context.Background(), testBundle, snapshot, "/smoke", []string{"not_found", "/smoke"}); err != nil {
		t.Fatalf("smoke failed: %v", err)
	}

	err := Run(context.Background(), testBundle, snapshot, "/smoke", []string{"missing-text"})
	if err == nil || !strings.Contains(err.Error(), "missing-text") {
		t.Fatalf("expected content assertion to fail, got %v", err)
	}
}

func TestRunRejectsBrokenBundles(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	if err := Run(context.Background(), "not javascript {{{", nil, "/", nil); err == nil {
		t.Fatal("broken bundle passed the smoke")
	}
	if err := Run(context.Background(), "globalThis.x = 1", nil, "/", nil); err == nil {
		t.Fatal("bundle without ssrRender passed the smoke")
	}
}
