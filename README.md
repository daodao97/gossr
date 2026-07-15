# gossr

一个用于 **Go + 前端 SSR（Vue/React 等）** 的轻量基础库。  
它负责把前端 SSR 构建产物接入 Gin，并统一处理渲染、数据获取与注入流程。

## 核心能力

- SSR 渲染：执行 `server.js` 中的 `ssrRender(url)`
- 页面数据：通过宿主创建的 `gossr.NewDataEngine()` + `gossr.WrapSSR` 组织隔离的数据接口
- 数据通道：自动挂载 `/_ssr/data`，支持前端请求与服务端内部 `Resolve`
- 注入能力：注入 HTML、`<head>` 内容、`window.__SSR_DATA__`
- 运行保障：渲染超时、并发限制、fallback 页面
- 模式切换：dev 代理 + 生产静态资源分发
- 内置引擎：仅保留基于 goja 的 `gojs`
- 引擎扩展：可通过 `renderer.Factory` 注入外部 V8 或其他实现

## 适用场景

- 使用 Gin 构建 BFF/网关，希望承接前端 SSR 产物
- 需要在 Go 层统一注入 session、locale、origin 等上下文
- 希望在同一套接口里兼顾 SSR 数据直取和服务端内部数据解析

## 项目结构

```text
gossr/
├── server.go                # SSR 主流程、NoRoute、注入、fallback、pprof
├── ssr.go                   # Ssr/NewDataEngine/WrapSSR/ResolveWithEngine/SSR fetch 路由保护
├── payload.go               # SSRPayload 接口
├── locales/                 # locale 支持（默认 en，支持 en/zh）
├── renderer/
│   ├── renderer.go          # 渲染器接口与工厂
│   └── engine/gojs/         # 内置 goja 渲染器与 runtime 池
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
- 默认且唯一内置的引擎是基于 goja 的 `gojs`，不依赖 v8go 或系统 V8。

```bash
go get github.com/daodao97/gossr
```

### 2) 前端产物约定

`gossr.Ssr` 接受任意标准 `fs.FS`（`embed.FS`、`os.DirFS`、`fstest.MapFS` 等），并期望其中包含：

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

var DataEngine = gossr.NewDataEngine()

func init() {
  DataEngine.GET("/", gossr.WrapSSR(Home))
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
  "yourapp/page"
  "yourapp/web"
)

func main() {
  r := gin.Default()
  if err := gossr.SsrWithOptions(r, web.Dist, gossr.Options{
    DataEngine: page.DataEngine,
    SiteOrigin: "https://www.example.com",
  }); err != nil {
    log.Fatal(err)
  }
  _ = r.Run(":8080")
}
```

### 注入外部 V8 渲染器

外部包只需实现 `renderer.Renderer`，并用工厂接收 `server.js` 内容：

```go
package v8adapter

import (
  "context"

  "github.com/daodao97/gossr/renderer"
)

type Renderer struct {
  // isolate pool、预编译脚本等由外部包自行管理
}

func New(scriptContents string) *Renderer {
  // 使用 v8go、remote V8 service 或其他 V8 binding 初始化
  return &Renderer{}
}

func (r *Renderer) Render(
  ctx context.Context,
  urlPath string,
  payload map[string]any,
) (renderer.Result, error) {
  // 注入 payload，执行 ssrRender(urlPath)，并读取 __SSR_HEAD__
  return renderer.Result{HTML: "<div>rendered by external V8</div>"}, nil
}

func (r *Renderer) Close() error {
  // 释放 isolate pool 或连接池；不持有资源时可以不实现此方法
  return nil
}
```

应用侧通过 `SsrWithOptions` 同时注入隔离的数据引擎和 renderer：

```go
factory := func(scriptContents string) renderer.Renderer {
  return v8adapter.New(scriptContents)
}

if err := gossr.SsrWithOptions(r, web.Dist, gossr.Options{
  DataEngine: page.DataEngine,
  RendererFactory: factory,
  SiteOrigin: "https://www.example.com",
  ShutdownContext: appShutdownContext,
}); err != nil {
  log.Fatal(err)
}
```

如果直接使用 `RunBlocking`，也可设置
`FrontendBuild.RendererFactory`。工厂必须返回已经初始化且可用的实例；gossr 不会通过伪造一次 `/` 请求做隐式预热。实例会被并发请求复用，因此外部实现必须保证并发安全，并正确响应 `ctx` 取消。传入 `ShutdownContext` 后，context 结束时会自动调用实现了 `renderer.Closer` 的渲染器。

## 运行时约定（接入前必读）

- `gossr.Ssr` 会挂载 `/_ssr/data/*path` 路由。
- `gossr.Ssr` 会接管 `Gin NoRoute`。
- dev 模式下，非 `/_ssr/data` 请求会被代理到 `DEV_SERVER_URL`。
- 生产模式下，`NoRoute` 会执行 SSR：取数据 -> 渲染 -> 注入 -> 返回 HTML。
- `ssrRender(url)` 收到完整 path + query，而不只是 path。
- 渲染失败或超时时，会返回 fallback 页面，并注入：
  - `meta[name="ssr-error-id"]`
  - `window.__SSR_DATA__`（若可序列化）
- gossr 不注册邀请、登录等业务路由；`/debug/pprof/*` 仅在 `ENABLE_PPROF` 打开或 dev 模式下启用。

## SSR 数据接口与自动注入

### SSRPayload

```go
type SSRPayload interface {
  AsMap() map[string]any
}
```

- `WrapSSR`：把业务 handler 统一转成 JSON 输出。
- `ResolveWithEngine`：从指定 DataEngine 调用 SSR 数据路由并拿到 payload。
- `RouterWithEngine`：将指定 DataEngine 映射到 `/_ssr/data`。
- `SsrEngine`、`Resolve`、`Router` 仅为旧版全局用法保留；新项目应注入独立 DataEngine，避免多实例路由污染。
- `WrapSSR` 默认会对 `500` 错误做脱敏（返回 `internal server error`）。
  - 如需调试原始错误，可设置 `SSR_EXPOSE_HANDLER_ERROR=1`（仅 `DEV_MODE` 生效）。

### 自动注入字段

渲染前会基于请求补充这些字段到 payload：

- `session`：保留字段；普通 payload 中同名字段会被清除，仅在 `SessionResolver` 返回非 nil 数据时注入
- `locale`：根据 URL 首段推断（默认 `en`，支持 `en`/`zh`）
- `siteOrigin`：优先使用 `Options.SiteOrigin`；未配置时才从经过格式校验的请求 Host/proxy 头推断
- `/_ssr/data` 外部响应默认不自动附带 `session`，只有 handler 显式返回时才会出现

gossr 不提供默认身份认证逻辑，也不会主动读取任何 cookie 或 header。token 格式、签名、过期、撤销和权限校验均由宿主应用负责。

通过 `SsrWithOptions` 为当前 SSR 实例配置 resolver：

```go
resolver := func(ctx context.Context, req *http.Request) (map[string]any, error) {
  cookie, err := req.Cookie("session_token")
  if errors.Is(err, http.ErrNoCookie) {
    return nil, nil // 匿名请求
  }
  if err != nil {
    return nil, err
  }

  user, err := sessions.Verify(ctx, cookie.Value)
  if err != nil {
    return nil, err // 当前 SSR 请求返回 500，不接受未验证身份
  }

  return map[string]any{
    "user": map[string]any{
      "id":    user.ID,
      "email": user.Email,
    },
  }, nil // 不返回原始 token
}

err := gossr.SsrWithOptions(r, web.Dist, gossr.Options{
  SessionResolver: resolver,
  DataEngine: dataEngine,
  SiteOrigin: "https://www.example.com",
})
```

约定：

- 无 session 返回 `nil, nil`，按匿名请求渲染。
- resolver 返回错误时终止本次 SSR 请求并返回 `500`。
- resolver 返回的数据会进入 HTML，禁止包含原始 token、密钥或其他凭证。
- resolver 属于单个 SSR 实例，不使用包级全局认证状态。
- 生产环境建议配置固定 `SiteOrigin`，避免 canonical URL 等业务逻辑依赖客户端可控的 Host。

## `/_ssr/data` 访问保护

- 默认按完整 origin 校验（scheme、规范化 host、有效端口均一致；默认端口等价）；无 referrer 时支持浏览器的 `Sec-Fetch-Site: same-origin`。
- `/_ssr/data` 响应默认只返回业务 payload + 路由上下文（如 `locale/siteOrigin`），不会自动附带 `session`。
- `Router` / `RouterWithEngine` 自带相同的 guard，成功和错误响应均使用 `no-store`，直接挂载不会绕过保护。
- 需要额外身份验证或服务间调用时，通过 `Options.SSRFetchAuthorizer` 注入宿主授权逻辑；gossr 不保存或分发共享密钥。
- 自定义授权器会完整接管判断；如需同时要求同源，可先调用 `gossr.DefaultSSRFetchAuthorizer(req)` 再验证宿主 session。
- 默认不信任 `X-Forwarded-*`。若部署环境可保证该头可信，可设置 `TRUST_FORWARDED_HEADERS=1`。

```go
options.SSRFetchAuthorizer = func(req *http.Request) (int, bool) {
  if status, ok := gossr.DefaultSSRFetchAuthorizer(req); !ok {
    return status, false
  }
  if !sessions.ValidRequest(req.Context(), req) {
    return http.StatusUnauthorized, false
  }
  return 0, true
}
```

## 渲染引擎与性能控制

- 默认使用内置 `gojs`；`SSR_ENGINE` 和 `nov8` build tag 已移除
- V8 等其他引擎通过 `renderer.Factory` 从应用侧显式注入
- `renderWithTimeout` 默认超时为 `3s`
- `SSR_RENDER_LIMIT` 控制并发渲染上限：
  - 不设置：默认 `runtime.GOMAXPROCS(0)`
  - `0`：不限制并发（不启用 semaphore）
  - `>0`：使用该值限制并发
- gojs 在初始化阶段编译脚本并预热 1 个 runtime，其余 runtime 按并发需求惰性创建；每个 runtime 会缓存 `ssrRender` 函数
- 默认每个 runtime 渲染 1000 次后回收，限制前端 Promise、路由等第三方状态在长时间运行中的累积
- gojs 会把 payload 复制为原生 JS 数据，脚本不能反向修改宿主 map/slice；非 JSON 数据会在渲染前返回错误
- gojs 提供浏览器兼容的 `atob` / `btoa`，便于运行不依赖 Node `Buffer` 的自包含 SSR bundle
- goja 脚本异常或 payload 转换失败后会丢弃当前 runtime，避免半变更状态进入下一请求

## 环境变量

- `DEV_MODE`：`1/true/yes/on/dev` 视为开发模式
- `DEV_SERVER_URL`：dev 代理地址，默认 `http://127.0.0.1:3333`
- `SSR_RENDER_LIMIT`：SSR 并发渲染上限
- `SSR_RENDER_LIMIT=0` 表示不限制并发；非法值会回退默认值
- `SSR_RENDER_LIMIT>1024` 会被 clamp 到 `1024`
- `TRUST_FORWARDED_HEADERS`：`1/true/yes/on` 时信任 `X-Forwarded-Host/Proto/Port`（默认关闭）
- `SSR_EXPOSE_HANDLER_ERROR`：`1/true/yes/on` 时，`WrapSSR` 返回原始 handler 错误文本（仅 `DEV_MODE` 生效）
- `ENABLE_PPROF`：`1/true/yes/on` 启用 pprof；未设置时 dev 模式默认启用
- `GOJA_POOL_SIZE` / `GOJA_POOL_TIMEOUT`：goja 池大小与获取超时（默认超时 `5s`）
  - `GOJA_POOL_SIZE` 会限制在 `[1, 512]`，默认等于 `GOMAXPROCS`
  - `GOJA_POOL_TIMEOUT` 负值会按 `0` 处理，最大 `30s`
- `GOJA_RUNTIME_MAX_RENDERS`：单个 goja runtime 的最大渲染次数，默认 `1000`，最大 `1000000`；设为 `0` 会关闭回收。Vue 等复杂 bundle 在长时间复用时可能持续扩大 runtime，生产环境不建议关闭

`GOGC` 属于宿主进程的全局 Go 配置，gossr 不会擅自修改。分配密集型 SSR 可在目标机器上对比 `GOGC=100/150/200`：提高该值通常能减少 GC CPU，但会增加峰值 RSS，必须结合内存限制和持续压测选择。

真实前端 bundle 的基准可在完成 `example/web` 构建后运行：

```bash
GOMAXPROCS=4 go test ./renderer/engine/gojs \
  -run '^$' -bench '^BenchmarkRendererExampleBundle(Parallel)?$' \
  -benchmem -benchtime=3s -count=5
```

示例服务端入口在同一个 goja runtime 内复用 Vue Router 的路由匹配器，但每次请求仍创建独立 app、SSR data context 和鉴权 guard，并在完成后移除 guard。这样避免每次重新编译路由正则，同时保持 session 与 payload 请求隔离。自定义前端入口采用相同策略时，不要把请求 payload、用户信息或 head 缓存在模块级变量中。

Vue 项目应使用 Vite 的真实 SSR 编译（`build.ssr: true`），并生成不含 Node `require` 的自包含 bundle。参考 `example/web/vite.config.server.ts`：使用 Vue runtime-only ESM 和 `@vue/server-renderer` bundler ESM，既生成组件 `ssrRender`，又避免把运行时编译器、`node:stream` 和 `Buffer` 带入 Goja。

## 构建与测试

```bash
go build ./...
go test ./...
go test -race ./...
npm --prefix example/web run typecheck
npm --prefix example/web test
go vet ./...
```

## 已知注意事项

- 接入后会接管 `NoRoute`，请先确认与现有项目路由策略不冲突。
- 内置 `/_ssr/data`，并可选注册 `/debug/pprof`；避免和宿主路径冲突。
- 生产模式依赖前端产物完整存在：`dist/client/index.html`、`dist/client/assets`、`dist/server/server.js`。
