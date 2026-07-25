# gossr Vue minimal example

这个示例展示 gossr 在 Go + Vue SSR 场景下的最小可运行方案。

主库文档见根目录 `README.md`，这里聚焦示例如何跑起来和如何验证行为。

## 关键点

- Go 侧只实现一个 `gossr.PageResolver`（`main.go` 的 `resolvePage`）：
  首屏文档和浏览器内导航（`/_ssr/data`）共用同一个 resolver 和同一份页面快照
- 通过 `gossr.New(gossr.Config{...}) + runtime.MountGin(...)` 接入路由
- Vue 侧只写一个 `defineGossrApp()`（`web/src/app.ts`）：
  路由、水合、导航请求、提交时序全部由 `@daodao97/gossr-vue` 承接,
  页面组件通过 `usePage()/useSession()` 读取当前文档
- 通过 `web/embed.go` 使用 `embed.FS` 内嵌 `web/dist`
- 示例使用内置 gojs，不依赖 v8go
- 鉴权重定向由 Go resolver 决定（`/protected` 未登录跳 `/session-demo`），
  客户端不做二次鉴权路由

## 前置依赖

- Go `1.25.8+`
- Node.js `>=22.12.0` + npm（用于 Vite 8 构建前端）
- 可选：Docker + Docker Compose

## 目录结构

```text
example/
├── Makefile
├── main.go          # PageResolver + session demo 路由
├── Dockerfile
├── compose.yaml
└── web/
    ├── embed.go
    ├── package.json
    ├── vite.config.ts          # gossrVuePreset
    ├── vite.config.server.ts   # gossrGojaSsrPreset
    ├── scripts/entry-server.ts # side-effect-only Rollup 入口
    ├── dist/
    │   ├── client/.keep
    │   └── server/.keep
    └── src/
        ├── app.ts              # defineGossrApp
        ├── page-document.ts    # 共享文档 codec
        ├── App.vue
        ├── entry-client.ts
        └── entry-server.ts
```

## 常用命令

```bash
cd example
make help
```

### 开发模式

```bash
cd example
make web-install
make web-dev
# 新开一个终端
make dev
```

开发模式下后端会把非 `/_ssr/data` 请求代理到 `DEV_SERVER_URL`
（默认 `http://127.0.0.1:3333`）。

### 生产模式

```bash
cd example
make web-install
make web-build
make run
```

release 模式默认关闭 Gin 的同步 access log，避免高并发 SSR 时日志 I/O 成为瓶颈；
需要逐请求日志时设置 `HTTP_ACCESS_LOG=1`。debug 模式仍默认开启。

### Docker 运行

默认镜像使用内置 gojs：

```bash
cd example
docker compose up --build
```

后台运行：

```bash
cd example
docker compose up --build -d
```

停止并清理：

```bash
cd example
docker compose down
```

## 路由验证清单

启动后访问 `http://127.0.0.1:8080`，可按下面顺序验证：

导航菜单不维护中央路由数组，而是从 `vue-router/vite` 生成的路由表读取页面的
`meta.nav`。普通新页面无需额外配置；需要进入主导航时，在页面的 `<route>` 中声明：

```yaml
meta:
  layout: home
  nav:
    labelKey: layout.nav.example
    order: 110
    # 可选：query 或固定目标（动态路由、404 示例等）
    query:
      mode: demo
```

菜单会自动过滤 locale alias、按 `order` 排序，并随当前 locale 生成链接。`labelKey`
对应的多语言文案仍需加入 locale JSON。

- 基础与多语言：
  - `/`
  - `/en`
  - `/zh`
  - `/hi/gopher`
  - `/en/hi/gopher`
  - `/zh/hi/gopher`
  - `/hi/vue?title=Ms.`
- SEO head 注入：
  - `/seo-demo?title=SSR%20SEO%20Title`
- Session 与鉴权重定向：
  - `/session-demo`
  - `/protected`（未登录时由 Go resolver 302 到 `/session-demo?next=...`）
  - `POST /demo/session/login`（form 字段 `next=/session-demo`）
  - `POST /demo/session/logout`（form 字段 `next=/session-demo`）
- SSR 超时 fallback：
  - `/slow-ssr`
- resolver 慢数据示例：
  - `/slow-fetch`

## 示例覆盖能力

### 1) Locale 路由与多语言

- 路由前缀支持：`/en/...`、`/zh/...`
- 后端根据路径首段写入文档的 `locale`
- SSR 输出会同步 `document.documentElement.lang`
- 页面文案会按 locale 切换中英文

### 2) SEO / Head 注入

- 路由：`/seo-demo`
- 前端通过 `<teleport to="head">` 输出 `title/meta`
- 服务端渲染时注入最终 HTML 的 `<head>`

### 3) Session 与鉴权

- 路由：`/session-demo`、`/protected`
- 登录：`POST /demo/session/login`（同源表单）
- 登出：`POST /demo/session/logout`（同源表单）
- 示例应用生成随机 opaque token，并在宿主进程的 session store 中保存用户数据
- resolver 读取请求 cookie，只把用户公开字段写入文档的 `session`
- 未登录访问 `/protected` 时，resolver 返回 302；文档请求和浏览器内导航
  走同一条重定向逻辑，客户端无需重复实现

⚠️ 安全提示：
- gossr 默认不读取或解析任何 session；session 完全由宿主 resolver 决定。
- 示例 store 仅存在于单个进程内，重启即失效；生产环境应替换为自己的认证/session 服务。
- 写入文档的 session 字段会进入 HTML，不能包含原始 token 或其他凭证。

### 4) SSR 超时与 Fallback

- 路由：`/slow-ssr`
- 超过渲染超时后，服务端返回带完整文档数据的 fallback 页面
- 响应中会注入 `meta[name="ssr-error-id"]`，客户端接管后可读取该标识

### 5) resolver 慢数据

- 路由：`/slow-fetch`
- resolver 对该路由故意延迟 `3.5s`（`Config.PageTimeout` 已放宽到 10s）
- 首屏表现为文档响应变慢；浏览器内切换到该页时,导航框架会显示全局 loading 条
- 该路由不应产生 `ssr-error-id` fallback（除非同时触发了其他渲染异常）

## `/_ssr/data` 调试方式

浏览器内导航使用的数据接口挂在 `/_ssr/data/*path`，可直接调试：

```bash
curl -H "Origin: http://127.0.0.1:8080" \
  "http://127.0.0.1:8080/_ssr/data/hi/gopher?title=Ms."
```

响应是 `render | redirect | error` 信封,`render.snapshot` 与首屏
`__GOSSR_BOOT__` 的文档完全一致。默认按完整 origin 校验（scheme、host、
端口一致，默认端口等价），响应使用 `no-store`。生产应用如需额外身份校验，
应通过 `gossr.GinOptions.SSRFetchAuthorizer` 注入宿主逻辑。

## 示例常用环境变量

- `DEV_MODE`：开发模式开关（`make dev` 已自动设置）
- `DEV_SERVER_URL`：开发模式代理地址（默认 `http://127.0.0.1:3333`）
- `HTTP_ACCESS_LOG`：是否启用 Gin 同步逐请求日志；release 默认关闭，debug 默认开启
- `GOJA_RUNTIME_MAX_RENDERS`：单个 Goja runtime 的最大复用次数，默认 `200`；复杂 Vue bundle 不建议设为 `0`
- `GOGC` / `GOMEMLIMIT`：宿主 Go GC 配置；可优先实测 `GOGC=100/200/300`，再按容器余量设置内存软限制
