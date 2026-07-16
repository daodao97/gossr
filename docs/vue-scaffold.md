# 全栈脚手架与 Vue 迁移指南

`example/web` 同时承担协议示例和功能演示，不适合让每个项目整目录复制。gossr 因此提供 `gossr-init`：稳定、很少变化的 Go 接口留在库里，必须随业务修改的 Vue 文件由模板生成到项目内。

## 全新项目

无参数运行会使用默认的 `fullstack` 模板，并输出到独立的 `gossr-app/` 目录：

```bash
go run github.com/daodao97/gossr/cmd/gossr-init@latest
```

终端中会出现基于 `huh/v2` 的交互表单，支持方向键选择和即时校验。流程包括：

```text
Choose a project template
  Full stack (recommended)
  Minimal Vue frontend
  Full Vue frontend

Output directory: gossr-app
Project/package name: gossr-app
Go module: example.com/gossr-app
Frontend Go package: web

Configuration
Create project?  Create / Cancel
```

显式传入的参数不会再次询问；通过管道或 CI 等非终端环境运行时也不会等待输入。自动化脚本建议加上 `--yes` 明确跳过向导。

生成结构：

```text
gossr-app/
├── go.mod
├── main.go
└── web/
    ├── embed.go
    ├── package.json
    ├── vite.config.ts
    └── src/
```

正式项目建议同时指定目录与 Go module：

```bash
go run github.com/daodao97/gossr/cmd/gossr-init@latest \
  --dir my-app \
  --module github.com/acme/my-app
```

团队项目建议把 `@latest` 换成明确的 gossr tag，让不同开发者生成完全相同的模板。可用参数：

- `--dir gossr-app`：输出目录，默认 `gossr-app`。
- `--template fullstack|minimal|full`：默认 `fullstack`；后两种只生成前端。
- `--module example.com/app`：fullstack 的 Go module，默认 `example.com/<name>`。
- `--name my-web`：覆盖 `package.json` 名称。
- `--go-package web`：覆盖 `embed.go` 的 package 名称；fullstack 默认 `web`。
- `--dry-run`：只预检并显示文件数量。
- `--force`：覆盖模板拥有的冲突路径；不会删除目录里的其他文件。
- `--yes`：接受默认值并跳过交互，适合 CI 和脚本。

完整项目生成后执行：

```bash
cd gossr-app/web
npm install
npm run typecheck
npm run build
cd ..
go mod tidy
go run .
```

生成的 `main.go` 已经把 `web.Dist` 传入 `gossr.SsrWithOptions`，并注册了一个首页 payload。身份认证仍由宿主中间件和 `SessionResolver` 负责。

## 已有 Go 项目只添加前端

推荐使用较小的 `minimal` 模板：

```bash
go run github.com/daodao97/gossr/cmd/gossr-init@latest \
  --dir web \
  --template minimal
```

需要布局、自动导航和 Head 示例时，将模板改成 `full`。生成后需要在宿主中自行把 `web.Dist` 接入 `gossr.SsrWithOptions`。

## 已有项目迁移

不要直接在已有前端上运行 `--force`。先生成到临时目录：

```bash
go run github.com/daodao97/gossr/cmd/gossr-init@latest \
  --dir /tmp/my-project-gossr-web \
  --template minimal
```

按下面顺序合并，能把一次性大迁移拆成可验证的小步骤：

1. 合并 `package.json`、两个 Vite 配置和 `tsconfig.json`，先让客户端与 `server.js` 都能构建。
2. 合并 `embed.go` 和 `dist/*/.keep`，确认宿主 Go 项目在未构建前端时仍可编译。
3. 采用 `main.ts`、`entry-server.ts` 的 app/router 生命周期；保留自己的 `App.vue` 和 pages。
4. 合并 `useSsrData.ts` 与 `entry-client.ts` 的 `/_ssr/data` 同步逻辑；把旧页面的数据读取逐个改成 `useSsrData<T>()`。
5. 开发代理必须保留 `changeOrigin: false`，否则后端的同源检查可能把 Vite 转发请求判为 403。
6. 最后执行 typecheck、前端双构建和宿主的 `go test ./...`，再删除旧入口。

生成目录里的 `.gossr-template.json` 只记录模板来源，便于代码审查和未来迁移；初始化器不会在后续运行时静默改写业务代码。相同参数重复运行是幂等的，文件一旦被项目修改，默认就会报告冲突并停止。

## 为什么不是一个大前端库

SSR 的全局函数名、产物目录和数据注入格式适合作为稳定协议；路由、布局、导航、鉴权和国际化通常会很快分化。把后者封进一个大 npm 库，会增加版本耦合、覆盖配置困难和调试心智负担。当前设计用初始化器消除第一次复制成本，同时让生成代码继续归宿主项目所有。
