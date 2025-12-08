package gojs

import (
	"fmt"

	"github.com/dop251/goja"
)

func formatGojaError(err error) error {
	if exc, ok := err.(*goja.Exception); ok {
		return fmt.Errorf("%s", exc.String())
	}

	return err
}
