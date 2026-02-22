package gossr

import (
	"strings"
	"testing"
	"time"

	"github.com/daodao97/gossr/renderer"
)

type testRenderer func(urlPath string, payload map[string]any) (renderer.Result, error)

func (f testRenderer) Render(urlPath string, payload map[string]any) (renderer.Result, error) {
	return f(urlPath, payload)
}

func TestRenderWithTimeoutReleasesSemaphoreAfterTimeout(t *testing.T) {
	sem := make(chan struct{}, 1)

	slowRenderer := testRenderer(func(_ string, _ map[string]any) (renderer.Result, error) {
		time.Sleep(80 * time.Millisecond)
		return renderer.Result{HTML: "slow"}, nil
	})

	_, err := renderWithTimeout(slowRenderer, "/", nil, 15*time.Millisecond, sem)
	if err == nil || !strings.Contains(err.Error(), "render timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}

	time.Sleep(120 * time.Millisecond)

	fastRenderer := testRenderer(func(_ string, _ map[string]any) (renderer.Result, error) {
		return renderer.Result{HTML: "ok"}, nil
	})

	result, err := renderWithTimeout(fastRenderer, "/", nil, 200*time.Millisecond, sem)
	if err != nil {
		t.Fatalf("expected render to succeed after timeout, got %v", err)
	}
	if result.HTML != "ok" {
		t.Fatalf("unexpected html result: %q", result.HTML)
	}
}

func TestRenderWithTimeoutDoesNotRunAfterSemaphoreWaitTimeout(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{}

	called := make(chan struct{}, 1)
	rendererFn := testRenderer(func(_ string, _ map[string]any) (renderer.Result, error) {
		called <- struct{}{}
		return renderer.Result{HTML: "late"}, nil
	})

	_, err := renderWithTimeout(rendererFn, "/", nil, 20*time.Millisecond, sem)
	if err == nil || !strings.Contains(err.Error(), "render timeout") {
		t.Fatalf("expected timeout error while waiting semaphore, got %v", err)
	}

	<-sem

	select {
	case <-called:
		t.Fatalf("renderer should not run after timeout while waiting semaphore")
	case <-time.After(60 * time.Millisecond):
	}
}
