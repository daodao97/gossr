package gossr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daodao97/gossr/renderer"
	"github.com/gin-gonic/gin"
)

func TestPageAdmissionTimesOutBeforeStartingQueuedWork(t *testing.T) {
	admission := newPageAdmission(1, 20*time.Millisecond)
	_, release, err := admission.enter(context.Background())
	if err != nil {
		t.Fatalf("acquire first slot: %v", err)
	}
	defer release()

	started := time.Now()
	_, _, err = admission.enter(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued enter error=%v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond {
		t.Fatalf("queued request returned before its deadline: %s", elapsed)
	}
}

func TestDocumentAndNavigationShareEndToEndAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	var resolverCalls atomic.Int32
	renderEntered := make(chan struct{})
	releaseRender := make(chan struct{})

	resolver := PageResolver(func(ctx context.Context, request PageRequest) (PageResult, error) {
		resolverCalls.Add(1)
		sourceDeadline, sourceOK := request.Source.Context().Deadline()
		resolverDeadline, resolverOK := ctx.Deadline()
		if !sourceOK || !resolverOK || !sourceDeadline.Equal(resolverDeadline) {
			return PageResult{}, errors.New("resolver did not receive the end-to-end deadline")
		}
		return PageResult{Payload: mapPayload{"url": targetRequestURI(request)}}, nil
	})

	runtime, err := New(Config{
		Bundle:             typedTestDist(),
		Mode:               ModeProduction,
		MaxConcurrentPages: 1,
		PageTimeout:        40 * time.Millisecond,
		RendererFactory: func(string) renderer.Renderer {
			return testRenderer(func(_ context.Context, _ string, _ map[string]any) (renderer.Result, error) {
				close(renderEntered)
				<-releaseRender
				return renderer.Result{HTML: "<main>done</main>"}, nil
			})
		},
	})
	if err != nil {
		t.Fatalf("create typed SSR runtime: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close typed SSR runtime: %v", err)
		}
	})
	router := gin.New()
	if err := runtime.MountGin(router, GinOptions{Resolver: resolver}); err != nil {
		t.Fatalf("mount typed SSR: %v", err)
	}

	documentDone := make(chan struct{})
	go func() {
		defer close(documentDone)
		_ = htmlRequest(router, http.MethodGet, "/hold", nil)
	}()
	select {
	case <-renderEntered:
	case <-time.After(time.Second):
		t.Fatal("document never reached renderer")
	}

	navigation := navigationRequest(router, "/next", nil)
	if navigation.Code != http.StatusGatewayTimeout {
		t.Fatalf("queued navigation status=%d body=%s", navigation.Code, navigation.Body.String())
	}
	if got := resolverCalls.Load(); got != 1 {
		t.Fatalf("queued navigation entered resolver: calls=%d", got)
	}

	close(releaseRender)
	select {
	case <-documentDone:
	case <-time.After(time.Second):
		t.Fatal("document did not release admission slot")
	}
}

func TestNavigationResolverUsesEndToEndDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEV_MODE", "")

	resolver := PageResolver(func(ctx context.Context, request PageRequest) (PageResult, error) {
		if _, ok := request.Source.Context().Deadline(); !ok {
			return PageResult{}, errors.New("source request has no deadline")
		}
		<-ctx.Done()
		return PageResult{}, ctx.Err()
	})
	runtime, err := New(Config{
		Bundle:      typedTestDist(),
		Mode:        ModeProduction,
		PageTimeout: 20 * time.Millisecond,
		RendererFactory: func(string) renderer.Renderer {
			return testRenderer(func(context.Context, string, map[string]any) (renderer.Result, error) {
				return renderer.Result{HTML: "must not render"}, nil
			})
		},
	})
	if err != nil {
		t.Fatalf("create typed SSR runtime: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close typed SSR runtime: %v", err)
		}
	})
	router := gin.New()
	if err := runtime.MountGin(router, GinOptions{Resolver: resolver}); err != nil {
		t.Fatalf("mount typed SSR: %v", err)
	}

	response := navigationRequest(router, "/slow", nil)
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var outcome navigationErrorOutcome
	if err := json.Unmarshal(response.Body.Bytes(), &outcome); err != nil {
		t.Fatalf("decode timeout outcome: %v", err)
	}
	if outcome.Kind != "error" || outcome.Code != "request_timeout" {
		t.Fatalf("unexpected timeout outcome: %#v", outcome)
	}
}
