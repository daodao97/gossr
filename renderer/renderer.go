package renderer

import "context"

// Renderer 定义 SSR 引擎需要实现的接口。
// 实现必须支持并发调用、响应 ctx 取消，并且不得修改传入的 payload。
type Renderer interface {
	Render(ctx context.Context, urlPath string, payload map[string]any) (Result, error)
}

// Closer 是渲染器可选实现的资源释放接口。
// 适用于持有原生 isolate、连接池或后台任务的外部引擎。
type Closer interface {
	Close() error
}

// Close 在渲染器实现 Closer 时释放资源；其他实现无需额外处理。
func Close(instance Renderer) error {
	if closer, ok := instance.(Closer); ok {
		return closer.Close()
	}
	return nil
}

// Factory 根据 SSR 脚本创建已初始化且可立即使用的渲染器。
// 外部引擎可通过实现 Renderer 并提供 Factory 注入。
type Factory func(scriptContents string) Renderer

// Redirect is the only renderer-controlled HTTP mutation. Header ownership
// remains with the Go orchestrator.
type Redirect struct {
	Status   int    `json:"status"`
	Location string `json:"location"`
}

// Result is the structured output of one SSR render.
//
// Status is optional; zero leaves the resolver status unchanged. Redirect is
// also optional and is validated by the orchestrator before it is written.
type Result struct {
	HTML     string
	Head     string
	Status   int
	Redirect *Redirect
}

const DefaultSSRScriptName = "server.js"
