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
- `http://127.0.0.1:8080/hi/gopher`
- `http://127.0.0.1:8080/hi/vue?title=Ms.`
