# gossr Vue minimal example

这个示例展示 gossr 在 Go + Vue SSR 场景下的最小可运行方案。

主库文档见根目录 `README.md`，这里聚焦示例如何跑起来和如何验证行为。

## 关键点

- 通过 `gossr.SsrEngine + gossr.WrapSSR` 注册 SSR 数据接口
- 通过 `web/embed.go` 使用 `embed.FS` 内嵌 `web/dist`
- 在入口 `main.go` 调用 `gossr.Ssr(router, web.Dist)` 完成接入
- 示例默认使用 `-tags nov8`，即 goja 路径，不依赖 v8go

## 前置依赖

- Go `1.25+`
- Node.js + npm（用于构建前端）
- 可选：Docker + Docker Compose

## 目录结构

```text
example/
├── Makefile
├── main.go
├── Dockerfile
├── compose.yaml
└── web/
    ├── embed.go
    ├── package.json
    ├── vite.config.ts
    ├── vite.config.server.ts
    ├── dist/
    │   ├── client/.keep
    │   └── server/.keep
    └── src/
        ├── App.vue
        ├── main.ts
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

开发模式下后端会把非 `/__ssr_fetch` 请求代理到 `DEV_SERVER_URL`
（默认 `http://127.0.0.1:3333`）。

### 生产模式

```bash
cd example
make web-install
make web-build
make run
```

### Docker 运行

默认镜像走 `goja`（`-tags nov8`）：

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
- Session 自动注入：
  - `/session-demo`
  - `/demo/session/login?next=/session-demo`
  - `/demo/session/logout?next=/session-demo`
- SSR 超时 fallback：
  - `/slow-ssr`
- `__ssr_fetch` 慢数据示例：
  - `/slow-fetch`

## 示例覆盖能力

### 1) Locale 路由与多语言

- 路由前缀支持：`/en/...`、`/zh/...`
- 后端根据路径首段写入 payload 的 `locale`
- SSR 输出会同步 `document.documentElement.lang`
- 页面文案会按 locale 切换中英文

### 2) SEO / Head 注入

- 路由：`/seo-demo`
- 前端通过 `<teleport to="head">` 输出 `title/meta`
- 服务端通过 `__SSR_HEAD__` 注入最终 HTML 的 `<head>`

### 3) Session Cookie 注入

- 路由：`/session-demo`
- 登录：`/demo/session/login?next=/session-demo`
- 登出：`/demo/session/logout?next=/session-demo`
- `session_token` 会被 gossr 自动解码并注入 `payload.session`

### 4) SSR 超时与 Fallback

- 路由：`/slow-ssr`
- 前端 SSR 入口对该路由故意延迟 `3.5s`
- 超过默认 `3s` 渲染超时后，服务端返回 fallback 页面
- 响应中会注入 `meta[name="ssr-error-id"]`，客户端接管后可读取该标识

### 5) `__ssr_fetch` 慢数据

- 路由：`/slow-fetch`
- 后端 `__ssr_fetch` handler 对该路由故意延迟 `3.5s`
- 用于演示“数据阶段慢”，不是“SSR 渲染阶段慢”
- 该路由不应产生 `ssr-error-id` fallback（除非同时触发了其他渲染异常）

## `/__ssr_fetch` 调试方式

示例中的 SSR 数据接口挂在 `/__ssr_fetch/*path`，可直接调试：

```bash
curl -H "X-SSR-Fetch: 1" \
  "http://127.0.0.1:8080/__ssr_fetch/hi/gopher?title=Ms."
```

如果配置了 `SSR_FETCH_TOKEN`，还需增加 `X-SSR-Token: <token>`。

## 示例常用环境变量

- `DEV_MODE`：开发模式开关（`make dev` 已自动设置）
- `DEV_SERVER_URL`：开发模式代理地址（默认 `http://127.0.0.1:3333`）
- `SSR_RENDER_LIMIT`：限制 SSR 并发渲染数量
- `ENABLE_PPROF`：开启 `/debug/pprof`（未设置时 dev 模式默认开启）
