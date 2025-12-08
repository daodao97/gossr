package gossr

// SSRPayload 表示可注入到 SSR 渲染/客户端的序列化数据。
type SSRPayload interface {
	AsMap() map[string]any
}
