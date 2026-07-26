# gossr

一个用于 **Go + 前端 SSR（Vue/React 等）** 的轻量基础库。  
它负责把前端 SSR 构建产物接入 Gin，并统一处理渲染、数据获取与注入流程。

## 核心能力

- SSR 渲染：执行结构化 `ssrRender({ url, snapshot })`
- 页面数据：一个 typed `PageResolver` 同时服务 document 与 navigation
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
├── runtime.go               # Runtime 生命周期、MountGin、模板校验
├── server.go                # 文档渲染主流程、注入、fallback、静态资源
├── ssr.go                   # /_ssr/data fetch 路由保护（同源校验）
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

`gossr.Config.Bundle` 接受任意标准 `fs.FS`（`embed.FS`、`os.DirFS`、`fstest.MapFS` 等），并期望其中包含：

```text
dist/
├── client/
│   ├── index.html
│   └── assets/
└── server/
    └── server.js
```

bundle 必须显式声明 ABI v2，并返回结构化结果（也可返回 Promise）；
未声明 ABI 的旧式 bundle 会在启动时被拒绝：

```ts
;(globalThis as any).__GOSSR_RENDER_ABI__ = 2
;(globalThis as any).ssrRender = async ({ url, snapshot }) => {
  return {
    html: `<div>${url}: ${snapshot.message}</div>`,
    head: "<title>My SSR Page</title>",
  }
}
```

v2 请求数据只通过函数参数传递。`PageResult` 是 HTTP 状态和跳转的唯一权威，
renderer 不得覆盖。使用 `@daodao97/gossr-vue` 时,`installGojaRenderABI()`
已实现该 ABI,应用无需手写。

### 3) 内嵌前端产物

```go
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
```

### 4) 注册 PageResolver

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
resolver/renderer 中的请求退出，最后幂等关闭 renderer。若 `MountGin` 因 Gin 路由
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

应用侧通过 `Config.RendererFactory` 注入：

```go
runtime, err := gossr.New(gossr.Config{
  Bundle: web.Dist,
  SiteOrigin: "https://www.example.com",
  RendererFactory: func(scriptContents string) renderer.Renderer {
    return v8adapter.New(scriptContents)
  },
})
```

工厂必须返回已经初始化且可用的实例；gossr 不会通过伪造一次 `/` 请求做隐式预热。实例会被并发请求复用，因此外部实现必须保证并发安全，并正确响应 `ctx` 取消。`Runtime.Close()` 会调用实现了 `renderer.Closer` 的渲染器。

## 运行时约定（接入前必读）

- `MountGin` 会挂载 `/_ssr/data/*path` 路由并接管 `Gin NoRoute`。
- dev 模式下，非 `/_ssr/data` 请求会被代理到 `DEV_SERVER_URL`。
- 生产模式下，`NoRoute` 会执行 SSR：resolver -> 渲染 -> 注入 -> 返回 HTML。
- `ssrRender({ url, snapshot })` 的 `url` 是完整 path + query，而不只是 path。
- SSR bundle 必须无跨请求可变全局状态：goja runtime 会在请求间复用（无论渲染
  成功或抛出异常），模块级缓存会泄漏到其他用户的请求中。gossr-vue 生成的
  bundle 天然满足；宿主引入的第三方依赖需自行确认。建议像 subapi 一样用
  "同一 runtime 渲染两次结果必须一致"的测试守住这条线。
- 渲染失败或超时时，会返回 fallback 页面，并注入：
  - `meta[name="ssr-error-id"]`
  - 完整的 `__GOSSR_BOOT__` 文档数据（客户端可无 SSR 冷启动）
- gossr 不注册邀请、登录等业务路由。

## 页面数据

### SSRPayload

```go
type SSRPayload interface {
  AsMap() map[string]any
}
```

- `gossr.ObjectPayload(v)`：把类型化 struct 序列化为一次性的 detached payload,
  是 resolver 构造快照的推荐方式。
- 快照内容完全由 resolver 决定;gossr 不注入 `session`/`locale` 等隐式字段,
  也不会主动读取任何 cookie 或 header。token 格式、签名、过期、撤销和权限
  校验均由宿主应用负责(通常在注册于 gossr 之前的 Gin middleware 中完成,
  经 `request.Source.Context()` 传给 resolver)。
- 生产环境建议配置固定 `SiteOrigin`，避免 canonical URL 等业务逻辑依赖客户端可控的 Host。

## `/_ssr/data` 访问保护

- 默认按完整 origin 校验（scheme、规范化 host、有效端口均一致；默认端口等价）；无 referrer 时支持浏览器的 `Sec-Fetch-Site: same-origin`。
- `Config.SiteOrigin` 配置后，以该 public origin 为默认边界，而不是内部 request Host；因此 TLS 终止代理不需要为了此项校验而信任 `X-Forwarded-*`。
- 开发模式还会接受显式 `DevServerURL`（或 `DEV_SERVER_URL`）的 origin，Vite 直连与经 Go 代理访问都可工作；生产模式不会放行该开发 origin。
- 未配置 `SiteOrigin` 时才回退到当前 request origin。
- `/_ssr/data` 响应默认只返回业务 payload + 路由上下文（如 `locale/siteOrigin`），不会自动附带 `session`。
- 成功和错误响应均使用 `no-store`。
- 需要额外身份验证或服务间调用时，通过 `GinOptions.SSRFetchAuthorizer` 注入宿主授权逻辑；gossr 不保存或分发共享密钥。
- 自定义授权器会完整接管判断；如需同时要求同源，可先调用 `gossr.DefaultSSRFetchAuthorizer(req)` 再验证宿主 session。
- 默认不信任 `X-Forwarded-*`。若部署环境可保证该头可信，可设置 `TRUST_FORWARDED_HEADERS=1`。

```go
ginOptions.SSRFetchAuthorizer = func(req *http.Request) (int, bool) {
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
- `Config.PageTimeout`（默认 `3s`）作为端到端 deadline 覆盖
  resolver + renderer 全链路；document 与 navigation 共用同一个 admission 池
- `Config.MaxConcurrentPages` 控制并发页面上限，默认 `runtime.GOMAXPROCS(0)`
- gojs 在初始化阶段编译脚本并预热 1 个 runtime，其余 runtime 按并发需求惰性创建；每个 runtime 会缓存 `ssrRender` 函数
- 默认每个 runtime 渲染 200 次后回收，限制前端 Promise、路由等第三方状态在长时间运行中的累积
- gojs 会把 payload 复制为原生 JS 数据，脚本不能反向修改宿主 map/slice；非 JSON 数据会在渲染前返回错误
- gojs 提供浏览器兼容的 `atob` / `btoa`，便于运行不依赖 Node `Buffer` 的自包含 SSR bundle
- goja 渲染被中断（超时/取消）或 panic 后会丢弃当前 runtime；干净的脚本异常不丢弃，
  避免稳定报错的页面把每次请求都变成完整的 bundle 重建

## 环境变量

- `DEV_MODE`：`1/true/yes/on/dev` 视为开发模式
- `DEV_SERVER_URL`：dev 代理地址，默认 `http://127.0.0.1:3333`
- `TRUST_FORWARDED_HEADERS`：`1/true/yes/on` 时信任 `X-Forwarded-Host/Proto/Port`（默认关闭）。
  只能在"可信反代已覆写这些头"的部署中开启：若客户端可直连服务或反代原样透传，
  伪造的 Host/Proto 会进入 `requestOrigin` 兜底与 `/_ssr/data` 同源判断。
  显式配置 `Config.SiteOrigin` 的宿主不受此影响，推荐始终显式配置。
- `GOJA_POOL_SIZE`：goja 池大小，限制在 `[1, 512]`，默认等于 `GOMAXPROCS`。
  池等待固定 5s 兜底超时；经过 gossr Runtime 的请求总是先被更短的
  `PageTimeout` 取消，该兜底只保护无 deadline 的直接调用方
- `GOJA_RUNTIME_MAX_RENDERS`：单个 goja runtime 的最大渲染次数，默认 `200`，最大 `1000000`；设为 `0` 会关闭回收。Vue 等复杂 bundle 在长时间复用时可能持续扩大 runtime，生产环境不建议关闭

`GOGC` 与 `GOMEMLIMIT` 属于宿主进程的全局 Go 配置，gossr 不会擅自修改。分配密集型 SSR 可在目标机器上重点对比 `GOGC=100/200/300`：提高该值通常能减少 GC CPU，但会增加峰值 RSS，必须结合内存限制和持续压测选择；示例 Vue bundle 在 Apple M3 上的吞吐甜点约为 `GOGC=300`。设置较高 `GOGC` 时建议同时按容器余量设置 `GOMEMLIMIT`，本机示例的 `256MiB` 软限制保留了大部分吞吐；这些数据不是所有应用的通用默认值。

真实前端 bundle 的基准可在完成 `example/web` 构建后运行：

```bash
GOMAXPROCS=4 go test ./renderer/engine/gojs \
  -run '^$' -bench '^BenchmarkRendererExampleBundle(Parallel)?$' \
  -benchmem -benchtime=3s -count=5
```

`@daodao97/gossr-vue` 的服务端 runtime 为每个请求创建独立 app、router 与导航协调器,不要把请求 payload、用户信息或 head 缓存在模块级变量中。

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
- 内置 `/_ssr/data`；避免和宿主路径冲突。
- 生产模式依赖前端产物完整存在：`dist/client/index.html`、`dist/client/assets`、`dist/server/server.js`。
