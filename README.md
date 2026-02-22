# gossr

一个用于 **Go + 前端 SSR（Vue/React 等）** 的轻量基础库。

它负责把前端 SSR 构建产物接入 Gin，并统一处理：

- SSR 渲染（`server.js -> ssrRender`）
- 页面数据获取（`/__ssr_fetch`）
- HTML/Head/SSR 数据注入
- dev 代理与生产静态资源分发
- 渲染超时、并发限制、失败回退

## 特性

- Gin 集成简单：`gossr.Ssr(router, dist)`
- 前端框架无关：只要提供 `server.js` 中的 `ssrRender(url)`
- SSR 数据层解耦：`WrapSSR` + `SSRPayload`
- 渲染引擎可切换：`v8go`（默认）/ `goja`
- 支持 dev 模式代理前端服务：`DEV_MODE=1`

## 目录结构

```text
gossr/
├── server.go           # SSR 主流程（dev 代理、渲染、注入、fallback）
├── ssr.go              # Ssr/SsrEngine/WrapSSR/Resolve/路由保护
├── payload.go          # SSRPayload 接口
├── ssr_v8.go           # 默认引擎选择（v8/goja）
├── ssr_nov8.go         # nov8 tag 下强制 goja
├── locales/            # locale 工具
├── renderer/           # 渲染器抽象与引擎实现
└── example/            # 最小 Go + Vue 示例
```

## 快速开始（用仓库内示例）

```bash
cd example
make web-install
make web-build
make run
```

访问：

- <http://127.0.0.1:8080/>
- <http://127.0.0.1:8080/hi/gopher>
- <http://127.0.0.1:8080/hi/vue?title=Ms.>

开发模式：

```bash
cd example
make web-install
make web-dev
# 新开终端
make dev
```

## 在你的项目中集成

### 1) 前端构建约定

`gossr.Ssr` 期望在 embed 文件系统里有以下目录：

```text
dist/
├── client/
│   ├── index.html
│   └── assets/
└── server/
    └── server.js
```

其中 `server.js` 需要暴露全局函数：

```ts
;(globalThis as any).ssrRender = async (url: string) => {
  // 返回 HTML 字符串
}
```

可选：设置 `globalThis.__SSR_HEAD__` 注入 `<head>`。

### 2) 内嵌前端产物

```go
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
```

### 3) 注册页面 SSR 数据接口

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

### 4) 接入 Gin

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

## SSR 数据接口说明

`SSRPayload` 接口：

```go
type SSRPayload interface {
  AsMap() map[string]any
}
```

- `WrapSSR` 会把你的 handler 转成 JSON 响应。
- `Resolve` 支持服务端内部直接解析某路径对应的 SSR 数据。
- 默认 SSR fetch 前缀是 `/__ssr_fetch`。

> 非同源请求访问 `/__ssr_fetch` 时，需要 `X-SSR-Fetch: 1`；
> 可通过 `SSR_FETCH_TOKEN` + `X-SSR-Token` 增加共享密钥保护。

## 运行参数

- `DEV_MODE=1`：开启开发模式（非 `/__ssr_fetch` 请求代理到前端 dev server）
- `DEV_SERVER_URL`：前端 dev server 地址（默认 `http://127.0.0.1:3333`）
- `SSR_ENGINE`：`v8` / `goja`（默认 `v8`）
- `SSR_RENDER_LIMIT`：SSR 并发渲染上限
- `SSR_FETCH_TOKEN`：保护 `__ssr_fetch` 接口的共享 token
- `GOJA_POOL_SIZE` / `GOJA_POOL_TIMEOUT`：goja runtime 池参数

## 构建与测试

```bash
go build ./...
go test ./...
go test -tags nov8 ./...
```

`nov8` tag 可在无 v8go 环境下走 goja 路径。

## 渲染流程（简化）

```text
Request
  -> Gin NoRoute
  -> fetch SSR payload (Resolve / __ssr_fetch)
  -> renderer.Render(path, payload)
  -> inject app html + head + window.__SSR_DATA__
  -> HTML Response
```
