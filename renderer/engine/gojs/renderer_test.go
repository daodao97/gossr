package gojs

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRendererReusesRuntimeAfterCleanScriptError(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	t.Setenv("GOJA_RUNTIME_MAX_RENDERS", "0")

	// 干净的 JS 异常不丢弃 runtime：一个稳定报错的页面不应把每次请求都
	// 变成完整的 bundle 重建。跨请求不可变全局是 bundle 的契约（成功路径
	// 从来就复用 runtime），异常路径与其保持一致。
	r := NewRenderer(`
globalThis.renders = 0;
globalThis.ssrRender = function() {
  globalThis.renders++;
  const data = globalThis.__SSR_DATA__ || {};
  if (data.fail) {
    throw new Error("render failed");
  }
  return String(globalThis.renders);
};
`)
	t.Cleanup(r.pool.Close)

	if _, err := r.Render(context.Background(), "/fail", map[string]any{"fail": true}); err == nil {
		t.Fatal("expected first render to fail")
	}

	result, err := r.Render(context.Background(), "/ok", nil)
	if err != nil {
		t.Fatalf("second render failed: %v", err)
	}
	if strings.TrimSpace(result.HTML) != "2" {
		t.Fatalf("runtime was not reused after a clean script error, got HTML %q", result.HTML)
	}
}

func TestRendererDiscardsRuntimeAfterInterrupt(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	t.Setenv("GOJA_RUNTIME_MAX_RENDERS", "0")

	// 中断会留下执行了一半的 JS，runtime 必须丢弃，下一次请求拿到全新实例。
	r := NewRenderer(`
globalThis.renders = 0;
globalThis.ssrRender = function() {
  globalThis.renders++;
  const data = globalThis.__SSR_DATA__ || {};
  if (data.spin) {
    for (;;) {}
  }
  return String(globalThis.renders);
};
`)
	t.Cleanup(r.pool.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := r.Render(ctx, "/spin", map[string]any{"spin": true}); err == nil {
		t.Fatal("expected interrupted render to fail")
	}

	result, err := r.Render(context.Background(), "/ok", nil)
	if err != nil {
		t.Fatalf("render after interrupt failed: %v", err)
	}
	if strings.TrimSpace(result.HTML) != "1" {
		t.Fatalf("interrupted runtime was reused, got HTML %q", result.HTML)
	}
}

func TestRendererCancellationWatcherCannotInterruptNextRequest(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	t.Setenv("GOJA_RUNTIME_MAX_RENDERS", "0")
	r := NewRenderer(`globalThis.ssrRender = function() { return "ok" }`)
	t.Cleanup(r.pool.Close)

	for i := 0; i < 500; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if _, err := r.Render(ctx, "/first", nil); err != nil {
			cancel()
			t.Fatalf("first render %d failed: %v", i, err)
		}
		cancel()
		if _, err := r.Render(context.Background(), "/next", nil); err != nil {
			t.Fatalf("next request was interrupted by previous watcher at iteration %d: %v", i, err)
		}
	}
}

func TestNewRendererRejectsMissingSSRRender(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("expected renderer construction to panic without ssrRender")
		}
	}()
	_ = NewRenderer(`globalThis.notTheEntry = function() { return "wrong" }`)
}

func TestRendererProvidesBase64WebAPIs(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	r := NewRenderer(`
globalThis.ssrRender = function() {
  return atob("aGVsbG8=") + ":" + btoa("\u00ff");
};
`)
	t.Cleanup(r.pool.Close)

	result, err := r.Render(context.Background(), "/", nil)
	if err != nil {
		t.Fatalf("render with base64 APIs: %v", err)
	}
	if result.HTML != "hello:/w==" {
		t.Fatalf("unexpected base64 result %q", result.HTML)
	}
}

func TestRendererCannotMutateHostPayload(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	r := NewRenderer(`
globalThis.ssrRender = function() {
  globalThis.__SSR_DATA__.session.user.email = "attacker@example.com";
  globalThis.__SSR_DATA__.items[0].name = "changed";
  return "ok";
};
`)
	t.Cleanup(r.pool.Close)

	user := map[string]any{"email": "verified@example.com"}
	item := map[string]any{"name": "original"}
	payload := map[string]any{
		"session": map[string]any{"user": user},
		"items":   []any{item},
	}
	if _, err := r.Render(context.Background(), "/", payload); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if user["email"] != "verified@example.com" || item["name"] != "original" {
		t.Fatalf("renderer mutated host payload: user=%#v item=%#v", user, item)
	}
}

func TestRendererRejectsNonJSONPayload(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	r := NewRenderer(`globalThis.ssrRender = function() { return "ok" }`)
	t.Cleanup(r.pool.Close)

	_, err := r.Render(context.Background(), "/", map[string]any{"invalid": make(chan int)})
	if err == nil || !strings.Contains(err.Error(), "encode SSR payload") {
		t.Fatalf("expected payload encoding error, got %v", err)
	}
}

func TestRendererNativePayloadPreservesJSONSemantics(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	r := NewRenderer(`
globalThis.ssrRender = function() {
  const data = globalThis.__SSR_DATA__;
  if (data.nilMap !== null || data.nilItems !== null)
    throw new Error("nil values are not null");
  if (data.object.__proto__ !== "payload-value")
    throw new Error("__proto__ was not an own payload property");
  if (Object.getPrototypeOf(data.object) !== Object.prototype)
    throw new Error("payload changed object prototype");
  return "ok";
};
`)
	t.Cleanup(r.pool.Close)

	result, err := r.Render(context.Background(), "/", map[string]any{
		"nilMap":   map[string]any(nil),
		"nilItems": []any(nil),
		"object":   map[string]any{"__proto__": "payload-value"},
	})
	if err != nil || result.HTML != "ok" {
		t.Fatalf("native payload semantics failed: result=%#v err=%v", result, err)
	}
}

func TestRendererCloseStopsNewRenders(t *testing.T) {
	r := NewRenderer(`globalThis.ssrRender = function() { return "ok" }`)
	if err := r.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close() failed: %v", err)
	}
	if _, err := r.Render(context.Background(), "/", nil); err == nil {
		t.Fatal("render unexpectedly succeeded after Close")
	}
}

func TestRendererStructuredABIPassesExplicitSnapshotAndDecodesOutcome(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	r := NewRenderer(`
globalThis.__GOSSR_RENDER_ABI__ = 2;
globalThis.ssrRender = async function(input) {
  if (typeof globalThis.__SSR_DATA__ !== "undefined")
    throw new Error("structured ABI received ambient request state");
  return {
    html: "<main>" + input.url + ":" + input.snapshot.viewer.email + "</main>",
    head: "<title>Account</title>",
    status: 404
  };
};
`)
	t.Cleanup(r.pool.Close)

	result, err := r.Render(context.Background(), "/account?tab=keys", map[string]any{
		"viewer": map[string]any{"email": "safe@example.com"},
	})
	if err != nil {
		t.Fatalf("structured render failed: %v", err)
	}
	if result.HTML != "<main>/account?tab=keys:safe@example.com</main>" ||
		result.Head != "<title>Account</title>" ||
		result.Status != 404 ||
		result.Redirect != nil {
		t.Fatalf("unexpected structured result: %#v", result)
	}
}

func TestRendererStructuredABIDecodesRedirect(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	r := NewRenderer(`
globalThis.__GOSSR_RENDER_ABI__ = 2;
globalThis.ssrRender = function(input) {
  return Promise.resolve({
    html: "",
    redirect: { status: 307, location: "/login?from=" + encodeURIComponent(input.url) }
  });
};
`)
	t.Cleanup(r.pool.Close)

	result, err := r.Render(context.Background(), "/private", nil)
	if err != nil {
		t.Fatalf("structured redirect failed: %v", err)
	}
	if result.Redirect == nil ||
		result.Redirect.Status != 307 ||
		result.Redirect.Location != "/login?from=%2Fprivate" {
		t.Fatalf("unexpected redirect result: %#v", result)
	}
}

func TestRendererStructuredABIRejectsInvalidResult(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{
			name: "string",
			script: `
globalThis.__GOSSR_RENDER_ABI__ = 2;
globalThis.ssrRender = function() { return "legacy"; };
`,
		},
		{
			name: "missing html",
			script: `
globalThis.__GOSSR_RENDER_ABI__ = 2;
globalThis.ssrRender = function() { return { head: "<title>missing</title>" }; };
`,
		},
		{
			name: "fractional status",
			script: `
globalThis.__GOSSR_RENDER_ABI__ = 2;
globalThis.ssrRender = function() { return { html: "bad", status: 200.5 }; };
`,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("GOJA_POOL_SIZE", "1")
			r := NewRenderer(testCase.script)
			t.Cleanup(r.pool.Close)
			if _, err := r.Render(context.Background(), "/", nil); err == nil {
				t.Fatal("invalid structured result was accepted")
			}
		})
	}
}

func TestRendererLegacyABIStillUsesAmbientAdapter(t *testing.T) {
	t.Setenv("GOJA_POOL_SIZE", "1")
	r := NewRenderer(`
globalThis.ssrRender = function(url) {
  globalThis.__SSR_HEAD__ = "<title>Legacy</title>";
  return url + ":" + globalThis.__SSR_DATA__.value;
};
`)
	t.Cleanup(r.pool.Close)

	result, err := r.Render(context.Background(), "/legacy", map[string]any{"value": "ok"})
	if err != nil {
		t.Fatalf("legacy render failed: %v", err)
	}
	if result.HTML != "/legacy:ok" || result.Head != "<title>Legacy</title>" {
		t.Fatalf("legacy adapter changed: %#v", result)
	}
}
