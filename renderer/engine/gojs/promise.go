package gojs

import (
	"errors"
	"fmt"

	"github.com/dop251/goja"
)

func resolveGojaValue(val goja.Value) (goja.Value, error) {
	if promise, ok := val.Export().(*goja.Promise); ok {
		switch promise.State() {
		case goja.PromiseStatePending:
			return nil, errors.New("ssrRender returned pending Promise")
		case goja.PromiseStateRejected:
			return nil, errors.New(promise.Result().String())
		case goja.PromiseStateFulfilled:
			return promise.Result(), nil
		default:
			return nil, fmt.Errorf("illegal goja.Promise state %d", promise.State())
		}
	}

	return val, nil
}
