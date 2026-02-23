package renderer

import "context"

// Renderer 定义 SSR 引擎需要实现的接口。
type Renderer interface {
	Render(ctx context.Context, urlPath string, payload map[string]any) (Result, error)
}

type Result struct {
	HTML string
	Head string
}

const DefaultSSRScriptName = "server.js"
