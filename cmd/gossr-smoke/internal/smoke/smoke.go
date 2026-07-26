// Package smoke 在真实的 gojs 渲染路径上执行一次 staged bundle 渲染。
package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/daodao97/gossr/renderer"
	"github.com/daodao97/gossr/renderer/engine/gojs"
)

type Options struct {
	BundlePath   string
	SnapshotPath string
	URL          string
	// Expects 是渲染结果必须包含的文本片段;由宿主指定,通常取一个
	// 该快照必然渲染出的字符串,证明真实路由树被执行了。
	Expects []string
}

func RunFile(ctx context.Context, options Options) (err error) {
	source, err := os.ReadFile(options.BundlePath)
	if err != nil {
		return fmt.Errorf("read staged SSR bundle: %w", err)
	}
	rawSnapshot, err := os.ReadFile(options.SnapshotPath)
	if err != nil {
		return fmt.Errorf("read smoke snapshot: %w", err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(rawSnapshot, &snapshot); err != nil {
		return fmt.Errorf("decode smoke snapshot: %w", err)
	}
	return Run(ctx, string(source), snapshot, options.URL, options.Expects)
}

func Run(ctx context.Context, source string, snapshot map[string]any, url string, expects []string) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	instance, err := newRenderer(source)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := renderer.Close(instance); closeErr != nil && err == nil {
			err = fmt.Errorf("close staged SSR renderer: %w", closeErr)
		}
	}()

	result, err := instance.Render(ctx, url, snapshot)
	if err != nil {
		return fmt.Errorf("render staged SSR bundle: %w", err)
	}
	if strings.TrimSpace(result.HTML) == "" {
		return fmt.Errorf("staged SSR renderer returned empty HTML")
	}
	for _, expected := range expects {
		if !strings.Contains(result.HTML, expected) {
			return fmt.Errorf("staged SSR output does not contain %q", expected)
		}
	}
	return nil
}

func newRenderer(source string) (instance renderer.Renderer, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			instance = nil
			err = fmt.Errorf("initialize staged SSR renderer: %v", recovered)
		}
	}()
	return gojs.NewRenderer(source), nil
}
