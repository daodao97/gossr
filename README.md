# gossr

一个用于 **Go + 前端 SSR（Vue/React 等）** 的轻量基础库。  
它负责把前端 SSR 构建产物接入 Gin，并统一处理渲染、数据获取与注入流程。

## 核心能力

- SSR 渲染：执行结构化 `ssrRender({ url, snapshot })`，并兼容旧 `ssrRender(url)`
- 页面数据：优先使用一个 typed `PageResolver` 同时服务 document 与 navigation
- 数据通道：自动挂载 `/_ssr/data`，返回 render / redirect / error 判别结果
- 注入能力：注入 HTML、`<head>` 与 inert JSON boot snapshot
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
├── page.go                  # PageResolver、typed outcome、document request 筛选
├── payload.go               # SSRPayload 接口
├── locales/                 # locale 支持（默认 en，支持 en/zh）
├── renderer/
│   ├── renderer.go          # 渲染器接口与工厂
│   └── engine/gojs/         # 内置 goja 渲染器与 runtime 池
├── packages/gossr-vue/      # Vue runtime、client/server 入口与 Vite presets
├── cmd/gossr-init/          # Vue SSR 前端初始化器
└── example/                 # 最小 Go + Vue 示例
```

## Vue runtime

`packages/gossr-vue` 提供与业务无关的 Vue SSR 生命周期、导航协议以及
Vite/Goja 构建约定。迁移期可由 pnpm 直接锁定仓库提交和包目录：

```json
{
  "dependencies": {
    "@daodao97/gossr-vue": "github:daodao97/gossr#<full-commit-sha>&path:/packages/gossr-vue"
  }
}
```

业务应用只定义根组件、路由、页面文档 codec 和可选的应用级 setup；client
bootstrap、每请求 SSR app、router/history、导航协调器与清理顺序由该包负责。
具体接口见 `packages/gossr-vue/README.md`。

## 初始化完整项目

无参数命令默认创建一个位于 `gossr-app/` 的完整 Gin + gossr + Vue SSR 项目：

```bash
go run github.com/daodao97/gossr/cmd/gossr-init@latest

cd gossr-app/web
npm install
npm run build
cd ..
go mod tidy
go run .
```

在终端中运行时会通过 `huh/v2` 表单引导选择模板，并填写输出目录、项目名、Go module 和前端 Go package；方向键选择、回车确认，每项都有默认值和即时校验，生成前还会显示完整配置。CI 或脚本中使用 `--yes` 跳过交互：

```bash
go run github.com/daodao97/gossr/cmd/gossr-init@latest --yes
```

默认 module 是 `example.com/gossr-app`。正式项目建议初始化时直接指定目录和 module：

```bash
go run github.com/daodao97/gossr/cmd/gossr-init@latest \
  --dir my-app \
  --module github.com/me/my-app
```

已有 Go 项目只需要前端时，可显式使用 `--template minimal --dir web`；`full` 在此基础上额外演示自动导航、布局与 Head 注入。鉴权、Session 与国际化继续由宿主项目决定，不成为脚手架的默认身份逻辑。

初始化器默认不覆盖已有文件；可先用 `--dry-run` 检查。已有项目的逐步迁移、模板边界和参数说明见 [Vue 脚手架与迁移指南](docs/vue-scaffold.md)。

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

- Go `1.25.8+`
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

推荐的 v2 bundle 显式声明 ABI，并返回结构化结果（也可返回 Promise）：

```ts
;(globalThis as any).__GOSSR_RENDER_ABI__ = 2
;(globalThis as any).ssrRender = async ({ url, snapshot }) => {
  return {
    html: `<div>${url}: ${snapshot.message}</div>`,
    head: "<title>My SSR Page</title>",
  }
}
```

v2 请求数据只通过函数参数传递。旧 bundle 的 `ssrRender(url)`、
`__SSR_DATA__`、`__SSR_HEAD__` 以及结构化结果中的 `status/redirect`
仍由兼容 adapter 解码；typed `PageResolver` 路径以 `PageResult` 为 HTTP
状态和跳转的唯一权威，renderer 不得覆盖。

### 3) 内嵌前端产物

```go
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
```

### 4) 注册 typed PageResolver（推荐）

```go
type snapshot struct {
  SchemaVersion int            `json:"schema_version"`
  URL           string         `json:"url"`
  Context       map[string]any `json:"context"`
}

func resolvePage(ctx context.Context, request gossr.PageRequest) (gossr.PageResult, error) {
  // principal 应由真实 Gin middleware 校验后放入 request.Source.Context()。
  payload, err := gossr.ObjectPayload(snapshot{
    SchemaVersion: 1,
    URL: request.URL.RequestURI(),
    Context: map[string]any{"site_origin": request.SiteOrigin},
  })
  if err != nil {
    return gossr.PageResult{}, err
  }
  return gossr.PageResult{
    Payload: payload,
  }, nil
}

ssrRuntime, err := gossr.New(gossr.Config{
  Bundle: web.Dist,
  SiteOrigin: "https://www.example.com",
  Mode: gossr.ModeAuto,
  PageTimeout: 3 * time.Second,
})
if err != nil {
  log.Fatal(err)
}

if err := ssrRuntime.MountGin(r, gossr.GinOptions{
  Resolver: resolvePage,
  ExcludedPathPrefixes: []string{"/api", "/backend", "/admin"},
}); err != nil {
  _ = ssrRuntime.Close()
  log.Fatal(err)
}

// 先停止接收 HTTP 请求并完成 drain，再释放 SSR runtime。
serveAndDrain(r)
if err := ssrRuntime.Close(); err != nil {
  log.Printf("close SSR runtime: %v", err)
}
```

`PageResolver` 会直接收到真实 transport request 和独立的目标 URL，不会经过内部
HTTP recorder。签名校验等已知的 Cookie mutation 应优先由注册在 gossr 之前的真实
Gin middleware 完成；若只有异步页面解析才能确认会话失效，可通过
`PageResult.Cookies` 返回经过校验的 Cookie。resolver 不拥有任意 response headers。

`New` 在修改 Gin 之前验证生产 bundle、HTML 模板和 renderer；开发模式只初始化
Vite proxy，不要求生产 bundle。`MountGin` 只能调用一次并接管 `NoRoute`，因此宿主
API、后台和 metrics 路由必须先注册。`Close` 会拒绝新页面请求、取消并等待队列/
resolver/renderer 中的请求退出，最后幂等关闭 renderer。旧项目可继续使用
`SsrWithOptions`，typed 路径内部会委托同一个 Runtime。若 `MountGin` 因 Gin 路由
冲突返回错误，传入的 router 可能已被部分修改，必须连同已关闭的 Runtime 一起丢弃。
生产模板必须有且只有一个 `id="app"`，并把唯一的 `<!--app-html-->` 作为它的直接
子占位；`data-ssr` 与 `__GOSSR_BOOT__` 均由 Runtime 注入，模板不得预置。

HTML fallback 只接受 `GET/HEAD` 且 `Accept` 明确包含 HTML 的请求。成功 SSR 会注入
`<script id="__GOSSR_BOOT__" type="application/json">` 和
`#app[data-ssr="true"]`。浏览器导航得到以下判别响应：

```text
{ "kind": "render", "status": 200, "snapshot": { ... } }
{ "kind": "redirect", "status": 303, "location": "/login" }
{ "kind": "error", "status": 500, "code": "resolve_failed", "message": "..." }
```

### 5) 注册 SSR DataEngine（兼容旧项目）

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

### 6) 旧 DataEngine 接入 Gin

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
- `Runtime` / `SsrWithOptions` 配置 `SiteOrigin` 后，以该 public origin 为默认边界，而不是内部 request Host；因此 TLS 终止代理不需要为了此项校验而信任 `X-Forwarded-*`。
- 开发模式还会接受显式 `DevServerURL`（或 `DEV_SERVER_URL`）的 origin，Vite 直连与经 Go 代理访问都可工作；生产模式不会放行该开发 origin。
- 未配置 `SiteOrigin` 时才回退到当前 request origin。
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
- typed `PageResolver` 路径把同一个 `3s` deadline 和 admission slot 覆盖到
  resolver + renderer 全链路；document 与 navigation 共用，不再额外叠加 render semaphore
- `SSR_RENDER_LIMIT` 控制并发渲染上限：
  - 不设置：默认 `runtime.GOMAXPROCS(0)`
  - `0`：不限制并发（不启用 semaphore）
  - `>0`：使用该值限制并发
- gojs 在初始化阶段编译脚本并预热 1 个 runtime，其余 runtime 按并发需求惰性创建；每个 runtime 会缓存 `ssrRender` 函数
- 默认每个 runtime 渲染 200 次后回收，限制前端 Promise、路由等第三方状态在长时间运行中的累积
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
- `GOJA_RUNTIME_MAX_RENDERS`：单个 goja runtime 的最大渲染次数，默认 `200`，最大 `1000000`；设为 `0` 会关闭回收。Vue 等复杂 bundle 在长时间复用时可能持续扩大 runtime，生产环境不建议关闭

`GOGC` 与 `GOMEMLIMIT` 属于宿主进程的全局 Go 配置，gossr 不会擅自修改。分配密集型 SSR 可在目标机器上重点对比 `GOGC=100/200/300`：提高该值通常能减少 GC CPU，但会增加峰值 RSS，必须结合内存限制和持续压测选择；示例 Vue bundle 在 Apple M3 上的吞吐甜点约为 `GOGC=300`。设置较高 `GOGC` 时建议同时按容器余量设置 `GOMEMLIMIT`，本机示例的 `256MiB` 软限制保留了大部分吞吐；这些数据不是所有应用的通用默认值。

真实前端 bundle 的基准可在完成 `example/web` 构建后运行：

```bash
GOMAXPROCS=4 go test ./renderer/engine/gojs \
  -run '^$' -bench '^BenchmarkRendererExampleBundle(Parallel)?$' \
  -benchmem -benchtime=3s -count=5
```

示例服务端入口在同一个 goja runtime 内复用 Vue Router 的路由匹配器，但每次请求仍创建独立 app 和 SSR data context。服务端只对可能受保护的路由执行完整解析，客户端继续使用异步鉴权 guard；`addRoute`、`removeRoute`、`clearRoutes` 会自动失效保护路由缓存。这样避免每次重新编译路由正则和公开路由上的无效 guard，同时保持动态路由、session 与 payload 请求隔离。自定义前端入口采用相同策略时，不要把请求 payload、用户信息或 head 缓存在模块级变量中。

完整的基准环境、优化过程、保留/撤回实验和调优矩阵见 [PERFORMANCE.md](./PERFORMANCE.md)。

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
