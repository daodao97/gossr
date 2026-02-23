# gossr

一个用于 **Go + 前端 SSR（Vue/React 等）** 的轻量基础库。  
它负责把前端 SSR 构建产物接入 Gin，并统一处理渲染、数据获取与注入流程。

## 核心能力

- SSR 渲染：执行 `server.js` 中的 `ssrRender(url)`
- 页面数据：通过 `gossr.SsrEngine + gossr.WrapSSR` 组织 SSR 数据接口
- 数据通道：自动挂载 `/_ssr/data`，支持前端请求与服务端内部 `Resolve`
- 注入能力：注入 HTML、`<head>` 内容、`window.__SSR_DATA__`
- 运行保障：渲染超时、并发限制、fallback 页面
- 模式切换：dev 代理 + 生产静态资源分发
- 引擎可选：`v8go`（默认）或 `goja`

## 适用场景

- 使用 Gin 构建 BFF/网关，希望承接前端 SSR 产物
- 需要在 Go 层统一注入 session、locale、origin 等上下文
- 希望在同一套接口里兼顾 SSR 数据直取和服务端内部数据解析

## 项目结构

```text
gossr/
├── server.go                # SSR 主流程、NoRoute、注入、fallback、pprof
├── ssr.go                   # Ssr/SsrEngine/WrapSSR/Resolve/SSR fetch 路由保护
├── payload.go               # SSRPayload 接口
├── ssr_v8.go                # 默认构建下按 SSR_ENGINE 选择 v8go/goja
├── ssr_nov8.go              # nov8 tag 下强制 goja
├── locales/                 # locale 支持（默认 en，支持 en/zh）
├── renderer/
│   ├── renderer.go          # 渲染器接口
│   └── engine/              # goja / v8go 渲染器实现与池化
├── scripts/
│   └── compare_ssr_engines.sh
└── example/                 # 最小 Go + Vue 示例
```

## 快速开始（仓库示例）

完整示例说明见 `example/README.md`。

### 生产模式

```bash
cd example
make web-install
make web-build
make run
```

### 开发模式

```bash
cd example
make web-install
make web-dev
# 新开终端
make dev
```

默认访问：`http://127.0.0.1:8080`。

## 在你的项目中集成

### 1) 依赖

- Go `1.25+`
- 默认引擎是 `v8go`。如果环境不方便安装 v8 依赖，可使用 `-tags nov8` 走 goja。

```bash
go get github.com/daodao97/gossr
```

### 2) 前端产物约定

`gossr.Ssr` 期望 embed 文件系统中包含：

```text
dist/
├── client/
│   ├── index.html
│   └── assets/
└── server/
    └── server.js
```

`server.js` 需要暴露全局函数 `ssrRender(url)`，返回 HTML 字符串（也可返回 Promise）：

```ts
;(globalThis as any).ssrRender = async (url: string) => {
  return "<div>hello</div>"
}

;(globalThis as any).__SSR_HEAD__ = "<title>My SSR Page</title>"
```

### 3) 内嵌前端产物

```go
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
```

### 4) 注册 SSR 数据接口

```go
package page

import (
  "github.com/daodao97/gossr"
  "github.com/gin-gonic/gin"
)

type homePayload struct{ Message string }

func (p homePayload) AsMap() map[string]any {
  return map[string]any{"message": p.Message}
}

func init() {
  gossr.SsrEngine.GET("/", gossr.WrapSSR(Home))
}

func Home(c *gin.Context) (gossr.SSRPayload, error) {
  return homePayload{Message: "hello"}, nil
}
```

### 5) 接入 Gin

```go
package main

import (
  "log"

  "github.com/daodao97/gossr"
  "github.com/gin-gonic/gin"
  "yourapp/web"
)

func main() {
  r := gin.Default()
  if err := gossr.Ssr(r, web.Dist); err != nil {
    log.Fatal(err)
  }
  _ = r.Run(":8080")
}
```

## 运行时约定（接入前必读）

- `gossr.Ssr` 会挂载 `/_ssr/data/*path` 路由。
- `gossr.Ssr` 会接管 `Gin NoRoute`。
- dev 模式下，非 `/_ssr/data` 请求会被代理到 `DEV_SERVER_URL`。
- 生产模式下，`NoRoute` 会执行 SSR：取数据 -> 渲染 -> 注入 -> 返回 HTML。
- 渲染失败或超时时，会返回 fallback 页面，并注入：
  - `meta[name="ssr-error-id"]`
  - `window.__SSR_DATA__`（若可序列化）
- 额外内置路由：
  - `/i/:invite_code`：写入 `invite_code` cookie 后重定向 `/`
  - `/debug/pprof/*`：`ENABLE_PPROF` 打开，或 dev 模式默认启用

## SSR 数据接口与自动注入

### SSRPayload

```go
type SSRPayload interface {
  AsMap() map[string]any
}
```

- `WrapSSR`：把业务 handler 统一转成 JSON 输出。
- `Resolve`：服务端内部调用 SSR 数据路由并拿到 payload。
- `Router`：将内部 `SsrEngine` 路由映射到 `/_ssr/data`。
- `WrapSSR` 默认会对 `500` 错误做脱敏（返回 `internal server error`）。
  - 如需调试原始错误，可设置 `SSR_EXPOSE_HANDLER_ERROR=1`。

### 自动注入字段

渲染前会基于请求补充这些字段到 payload：

- `session`：从 `session_token` cookie 解析后注入（默认 base64 JSON）
- `locale`：根据 URL 首段推断（默认 `en`，支持 `en`/`zh`）
- `siteOrigin`：根据请求 host 和协议推断，如 `https://example.com`

可通过 `SetSessionTokenParser` 自定义 `session_token` 的校验与解析逻辑：

```go
gossr.SetSessionTokenParser(func(token string) (map[string]any, error) {
  // 在这里做签名校验、解密或自定义 payload 结构
  return map[string]any{
    "session_token": token,
    "user": map[string]any{"email": "demo@example.com"},
  }, nil
})
```

`session` 结构示例：

```json
{
  "session_token": "<cookie原值>",
  "user": {
    "id": "u_demo_1001",
    "name": "SSR Demo User",
    "email": "demo@example.com",
    "provider": "example"
  }
}
```

## `/_ssr/data` 访问保护

- 同源请求：当 `Origin/Referer` 可识别且与 Host 一致时允许访问。
- 非同源请求：必须带 `X-SSR-Fetch: 1`，否则返回 `403`。
- 当请求没有 `Origin/Referer` 时，也需要显式带 `X-SSR-Fetch: 1`。
- 如果设置了 `SSR_FETCH_TOKEN`：请求必须带 `X-SSR-Token: <token>`，否则返回 `401`。

## 渲染引擎与性能控制

- 默认构建（无 `nov8`）：
  - `SSR_ENGINE=v8`（默认）使用 `v8go`
  - `SSR_ENGINE=goja` 使用 `goja`
- `-tags nov8`：强制使用 `goja`（忽略 v8 相关能力）
- `renderWithTimeout` 默认超时为 `3s`
- `SSR_RENDER_LIMIT` 控制并发渲染上限：
  - 不设置：默认 `runtime.GOMAXPROCS(0)`
  - `0`：不限制并发（不启用 semaphore）
  - `>0`：使用该值限制并发
- 渲染器启动后会异步预热一次首屏渲染

## 环境变量

- `DEV_MODE`：`1/true/yes/on/dev` 视为开发模式
- `DEV_SERVER_URL`：dev 代理地址，默认 `http://127.0.0.1:3333`
- `SSR_ENGINE`：`v8` / `goja`（默认 `v8`，仅默认构建下有效）
- `SSR_RENDER_LIMIT`：SSR 并发渲染上限
- `SSR_FETCH_TOKEN`：`/_ssr/data` 共享 token
- `SSR_EXPOSE_HANDLER_ERROR`：`1/true/yes/on` 时，`WrapSSR` 返回原始 handler 错误文本
- `ENABLE_PPROF`：`1/true/yes/on` 启用 pprof；未设置时 dev 模式默认启用
- `GOJA_POOL_SIZE` / `GOJA_POOL_TIMEOUT`：goja 池大小与获取超时（默认超时 `5s`）
- `V8_POOL_SIZE` / `V8_POOL_TIMEOUT`：v8 isolate 池大小与获取超时（默认超时 `5s`）

## 构建与测试

```bash
go build ./...
go test ./...
go test -tags nov8 ./...
go vet ./...
```

说明：

- 默认测试路径覆盖 v8go 相关逻辑。
- `-tags nov8` 用于无 v8 环境或验证 goja 路径。

## 性能对比脚本

仓库提供 `scripts/compare_ssr_engines.sh` 用于对比 v8go 与 goja。

```bash
cd example
make web-build

cd ..
./scripts/compare_ssr_engines.sh --help
./scripts/compare_ssr_engines.sh
```

输出默认写入 `benchmark_output/<timestamp>/`。

## 已知注意事项

- 接入后会接管 `NoRoute`，请先确认与现有项目路由策略不冲突。
- 内置 `/_ssr/data`、`/i/:invite_code`、`/debug/pprof` 路径，避免和业务路径冲突。
- 生产模式依赖前端产物完整存在：`dist/client/index.html`、`dist/client/assets`、`dist/server/server.js`。
