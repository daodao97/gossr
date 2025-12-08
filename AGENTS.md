# Repository Guidelines

## 项目结构与模块组织
- `server.go`：基于 gin 的入口，处理 SSR 渲染、回退页面、dev 代理与 `SSR_RENDER_LIMIT` 并发控制。
- `ssr_v8.go` / `ssr_nov8.go`：通过 build tag 在 v8go 与 goja 之间切换；默认使用 v8go，`nov8` tag 强制走 goja。
- `renderer/`：`renderer.go` 定义渲染接口；`engine/gojs` 与 `engine/v8` 分别实现 goja 与 v8go 渲染器，共享 runtime 池和工具。
- `payload.go` 暴露 `SSRPayload` 数据接口；`locales/` 管理语言列表与默认语言。
- 前端构建通过 `FrontendBuild` 注入，期望 `index.html`、`assets/`、`server.js`（默认入口名）可从打包产物挂载。

## 构建、测试与本地开发命令
- 依赖 Go 1.25+；默认需要系统满足 v8go 依赖。
- 常用命令：
```bash
go build ./...
go test ./...
go test -tags nov8 ./...    # 仅走 goja 路径，便于无 v8go 环境验证
go vet ./...
```
- 本地联调前端时可设置 `DEV_MODE=1 DEV_SERVER_URL=http://127.0.0.1:3333`，让后端代理至前端 dev server；生产模式读取打包产物。

## 代码风格与命名约定
- 一律使用 `gofmt`；保持 import 路径与模块一致，避免循环依赖。
- Go 命名遵循 Go 习惯，常见缩写大写（URL、SSR）；错误使用 `err` 逐层返回，避免吞掉上下文。
- 并发与池化逻辑优先复用现有 `runtimePool` 模式，保持简短的作用域和 `defer` 释放顺序。

## 架构与扩展提示
- SSR 流程：request -> payload 构建 -> `ssrRender` 执行 -> 注入 HTML/Head -> fallback 页面；修改时保持接口稳定，避免破坏渲染数据结构。
- 渲染器是可插拔的，新增引擎请实现 `renderer.Renderer` 并复用池化策略；确保初始化时即编译脚本，运行期仅复用。
- 网络层依赖 gin，常用中间件从入口注册；新增路由注意与 `DefaultSSRFetchPrefix` 前缀冲突，并保持 cookie/locale 行为一致。

## 测试指南
- 目前无测试文件，新功能请补充 table-driven `_test.go`：覆盖正常渲染、超时、非法 payload、语言推断与头部注入等。
- SSR 引擎需在默认（v8go）与 `-tags nov8`（goja）下均通过；对渲染结果可校验 HTML/Head 与注入数据。
- 升级依赖或调整 build tag 前先 `go test ./...` 与 `go test -tags nov8 ./...` 双向验证，避免单一路径回归。

## Commit 与 Pull Request
- commit 信息用祈使句，示例：`feat: add goja render tests`、`fix: guard payload decoding`、`chore: gofmt`.
- PR 需描述变更、影响范围与验证方式；涉及 SSR 路径或并发限制变更，请附本地验证命令输出/截图；如有 issue，请显式关联。

## 配置与安全提示
- 环境变量：`SSR_ENGINE`（goja/v8）、`SSR_RENDER_LIMIT` 控制并发、`DEV_MODE`/`DEV_SERVER_URL` 控制代理。
- 不要提交真实 cookie/token 或前端产物；前端资源应经构建后挂载到 `FrontendBuild`，确保 `assets/` 子路径可被静态服务。
