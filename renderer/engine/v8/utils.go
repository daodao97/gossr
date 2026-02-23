//go:build !nov8

package v8

import (
	"context"
	"errors"
	"fmt"

	"rogchap.com/v8go"
)

func resolveV8Promise(v8ctx *v8go.Context, val *v8go.Value, err error, execCtx context.Context) (*v8go.Value, error) {
	if err != nil || !val.IsPromise() {
		return val, err
	}
	if execCtx == nil {
		execCtx = context.Background()
	}
	for {
		select {
		case <-execCtx.Done():
			return nil, execCtx.Err()
		default:
		}

		switch p, _ := val.AsPromise(); p.State() {
		case v8go.Fulfilled:
			return p.Result(), nil
		case v8go.Rejected:
			return nil, errors.New(p.Result().DetailString())
		case v8go.Pending:
			v8ctx.PerformMicrotaskCheckpoint() // run VM to make progress on the promise
			// go round the loop again...
		default:
			return nil, fmt.Errorf("illegal v8go.Promise state %d", p) // unreachable
		}
	}
}

func formatV8Error(err error) error {
	var jsErr *v8go.JSError
	if errors.As(err, &jsErr) {
		err = fmt.Errorf("%v", jsErr.StackTrace)
	}

	return err
}
