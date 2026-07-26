package gossr

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const defaultPageRequestTimeout = 3 * time.Second

// pageAdmission is the single queue in front of typed page work. A slot covers
// the complete resolver + renderer lifecycle, so expensive data loading cannot
// grow without bound ahead of the renderer pool.
type pageAdmission struct {
	slots   chan struct{}
	timeout time.Duration
}

func newPageAdmission(limit int, timeout time.Duration) *pageAdmission {
	if timeout <= 0 {
		timeout = defaultPageRequestTimeout
	}
	admission := &pageAdmission{timeout: timeout}
	if limit > 0 {
		admission.slots = make(chan struct{}, limit)
	}
	return admission
}

func (a *pageAdmission) enter(parent context.Context) (context.Context, func(), error) {
	if parent == nil {
		parent = context.Background()
	}

	ctx, cancel := context.WithTimeout(parent, a.timeout)
	if a.slots == nil {
		return ctx, cancel, nil
	}

	select {
	case a.slots <- struct{}{}:
		return ctx, func() {
			<-a.slots
			cancel()
		}, nil
	case <-ctx.Done():
		err := ctx.Err()
		cancel()
		return nil, func() {}, err
	}
}

func pageRequestErrorStatus(err error) int {
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	if errors.Is(err, context.Canceled) {
		return http.StatusRequestTimeout
	}
	return http.StatusInternalServerError
}
