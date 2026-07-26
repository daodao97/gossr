// gossr-smoke 用真实的 gojs 渲染器执行一次 staged SSR bundle 渲染,
// 供宿主构建管线在发布前拦下坏 bundle。快照与期望文本由宿主传入。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/daodao97/gossr/cmd/gossr-smoke/internal/smoke"
)

type expectFlags []string

func (e *expectFlags) String() string { return fmt.Sprint(*e) }

func (e *expectFlags) Set(value string) error {
	*e = append(*e, value)
	return nil
}

func main() {
	bundlePath := flag.String("bundle", "", "staged server.js path (required)")
	snapshotPath := flag.String("snapshot", "", "JSON snapshot file passed to ssrRender (required)")
	renderURL := flag.String("url", "/__gossr_smoke__", "render target URL")
	timeout := flag.Duration("timeout", 30*time.Second, "render deadline")
	var expects expectFlags
	flag.Var(&expects, "expect", "substring the rendered HTML must contain (repeatable)")
	flag.Parse()

	if *bundlePath == "" || *snapshotPath == "" {
		flag.Usage()
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if err := smoke.RunFile(ctx, smoke.Options{
		BundlePath:   *bundlePath,
		SnapshotPath: *snapshotPath,
		URL:          *renderURL,
		Expects:      expects,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Println("staged SSR bundle passed the gossr Goja render smoke")
}
