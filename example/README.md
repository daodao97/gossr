# gossr Vue minimal example

这个示例展示了 gossr 在 Go + Vue SSR 场景下的最小用法。

## 关键点

- 通过 `gossr.SsrEngine + gossr.WrapSSR` 注册页面数据接口
- 通过 `web/embed.go` 使用 `embed.FS` 内嵌 `web/dist`
- 在 Go 入口直接调用 `gossr.Ssr(router, web.Dist)`，复用 gossr 上层封装

## 目录结构

```text
example/
├── Makefile
├── main.go
└── web/
    ├── embed.go
    ├── index.html
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

### 生产模式

```bash
cd example
make web-install
make web-build
make run
```

访问：

- `http://127.0.0.1:8080/`
- `http://127.0.0.1:8080/en`
- `http://127.0.0.1:8080/zh`
- `http://127.0.0.1:8080/hi/gopher`
- `http://127.0.0.1:8080/en/hi/gopher`
- `http://127.0.0.1:8080/zh/hi/gopher`
- `http://127.0.0.1:8080/hi/vue?title=Ms.`
- `http://127.0.0.1:8080/seo-demo?title=SSR%20SEO%20Title`
- `http://127.0.0.1:8080/session-demo`
- `http://127.0.0.1:8080/slow-ssr`

## 新增示例

### 0) Locale 路由与多语言

- 路由示例：`/en/...`、`/zh/...`
- 后端基于路径首段识别 locale，并写入 payload 的 `locale`
- 前端会根据路由路径同步 `document.documentElement.lang`
- 前端页面文案（导航、标题、按钮、提示）会按 locale 自动切换中英文
- Home 布局内置语言切换，可在当前页面一键切换 `EN / ZH`

### 1) SEO / Head 注入

- 路由：`/seo-demo`
- 前端通过 `<teleport to="head">` 输出 `title/meta`
- 服务端通过 `__SSR_HEAD__` 注入到最终 HTML `<head>`

### 2) Session Cookie 注入

- 路由：`/session-demo`
- 登录示例接口：`/demo/session/login?next=/session-demo`
- 登出示例接口：`/demo/session/logout?next=/session-demo`
- `session_token` cookie 会由 gossr 自动解码并注入 `payload.session`

### 3) SSR 超时与 Fallback

- 路由：`/slow-ssr`
- 前端 SSR 入口会对该路由故意延迟 `3.5s`，超过 gossr 默认 `3s` 渲染超时
- 服务端返回 fallback 页面并注入 `meta[name="ssr-error-id"]`
- 客户端随后接管渲染，页面会显示该 `ssr-error-id`
