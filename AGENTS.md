# Repository Guidelines

给在这个仓库(及其宿主应用)上工作的 AI 与人类的指引。前半部分是**设计准绳与
不变量**——所有取舍以它们为准;后半部分是仓库机制。

## 最高设计目标(所有取舍的判据)

这套 Go SSR 要足够好用,但复杂度必须低。**页面作者只写:路由、loader、
数据类型、Vue 组件、局部交互**;Go 框架承接请求、首屏渲染、序列化和兜底。
任何让业务页面理解 SSR/水合/ready/占位/生命周期的抽象都是设计失败——
功能取舍时,以降低页面作者认知负担优先。

推论:

- 复杂度允许存在,但必须住在框架里(gossr / gossr-vue),或宿主的一次性
  胶水层里(App.vue、page-data.ts、entry 文件)。业务页面必须零感知。
- 一条规则若能从"约定"升级为"机制"(协议上不可表达、类型系统拒绝、
  启动时校验、回归测试钉死),就应该升级。修 bug 时优先考虑这种升级。
- 删除优先于新增。为未到来的需求预付复杂度是这套代码反复清除的东西;
  但注意:**当前没有宿主使用 ≠ 死代码**——`locales/` 多语言链路是框架
  规划的能力面(example 有完整 i18n 演示),不要当投机代码删除。

## 架构不变量(不要破坏;每条都有测试或机制钉着)

1. **HTTP 意图只属于 `PageResolver`**。`renderer.Result` 只有 `{HTML, Head}`,
   ABI v2 返回值里没有 status/redirect 字段(多余字段被忽略)——这是协议级
   不可表达,不是运行时否决。不要把 HTTP 语义加回渲染层。
2. **document 与 navigation 共用同一个 `PageResolver`**,没有双协议。
   navigation 结果用 JSON body 的 `kind: render/redirect/error` 表达而非
   HTTP 状态码(fetch 会自动跟随 302,客户端感知不到真实重定向)。
3. **SSR bundle 必须声明 `__GOSSR_RENDER_ABI__ = 2`**,未声明的在 NewRenderer
   时 panic。快照只经函数参数传入,无环境全局;渲染失败自动降级 CSR 壳
   (boot 数据保留,客户端可冷启动)。
4. **bundle 不得有跨请求可变模块级全局**。goja runtime 跨请求复用:干净 JS
   异常归还池(避免报错页引发重建风暴),中断/panic 必须丢弃。宿主应有
   "同一 runtime 先渲染登录用户再渲染访客,断言零残留"的测试
   (参考 subapi 的 `ssr_repeat_test.go`)。
5. **数字安全在 `ObjectPayload` 单点强制**(有限、整数不超过 ±2^53-1)。
   不要在宿主两端重复实现;宿主只补自己的业务规则(如 token 黑名单)。
6. **index.html 在启动时预编译切段**(`compileIndexTemplate`),请求路径只做
   字符串拼接。不要在请求路径上引入 HTML 解析;模板必须有闭合 `<head>`
   且在 app 标记之前。HEAD 请求走 resolver 但跳过渲染。
7. **部署偏差自愈**:`standardPageDataCodec` 校验结构 + `schema_version`,
   不认识的信封 → parse 抛错 → 整页浏览器导航 → 加载新 bundle。改 wire
   格式必须提升 schema_version。兜底阶梯:SSR → CSR fallback 壳 → 整页
   浏览器导航,最后一级永远走得通。
8. **命名**:三层(Go / wire / TS)统一叫 `PageData`。"document" 一词只用于
   浏览器 document 语义(如 `documentURLFromRouter`),不要再引入它指代
   页面数据。
9. **缓存与安全**:个性化响应全路径 no-store;`/_ssr/data` 有同源守卫、无
   CORS 头;不要给同一 URL 做 Accept 协商双表示(Vary 在 CDN 上不可靠,
   独立路径让缓存键在构造上分离)。`TRUST_FORWARDED_HEADERS` 只能在可信
   反代后开启;宿主应显式配置 `Config.SiteOrigin`。
10. **导航模型(gossr-vue)**:站内导航非阻塞(点击即换路由),初始导航阻塞
    (水合必须对准 boot 文档);`current` 保持上一份数据(stale-while-loading);
    bfcache 恢复(`pageshow` persisted)自动 refresh;**已提交文档按 URL
    缓存(SWR)**:回到访问过的页面立即用缓存提交(不出骨架),随即后台
    refresh 重验证——新鲜数据无感替换,服务端改判(登出后的重定向)走
    正常路由通道。**宿主必须在登录/登出/切换账号时调
    `navigation.clearCached()`**,否则上一会话的页面可能短暂闪现。
    宿主侧的配套模式:
    页面挂载时用占位数据渲染初始结构、数据提交后响应式填充;骨架屏是
    `src/skeletons/` 下按路由映射的**纯静态组件**,由 App.vue 挑选——
    永远不要把 ready/骨架判断塞回页面组件。

## 修改剧本

- **改 gossr-vue**:`pnpm build`(dist 是发布物)→ `pnpm vitest run` →
  `node scripts/check-exports.mjs`。宿主本地联调用
  `"file:../路径/packages/gossr-vue"`(必须 `file:` 不能 `link:`,否则
  vue-router 类型双实例导致 vue-tsc 失败);推送后宿主改回
  `github:daodao97/gossr#<完整sha>&path:/packages/gossr-vue`。
- **改 Go**:`go test ./...`;脚手架模板(`cmd/gossr-init/.../templates`)和
  `example/` 与公共 API 保持同步——重命名/改选项时三处一起改。
  宿主升级:`go get github.com/daodao97/gossr@<sha>`(sum.golang.org 对新
  commit 可能延迟几分钟,重试即可)。
- **改 wire 格式**:提升 `schema_version`;同步双端 contract fixtures
  (宿主 `testdata/gossr-contracts.json`,Go 和 Vue 各自独立断言同一份)。
  注意 fixtures 只放双端判定一致的用例;单端规则放各自的单元测试。
- **性能类改动**:跑 `renderer/engine/gojs` 基准与跨请求隔离测试;涉及池/
  并发的改动保留对应语义测试(取消不吞资源、关闭后拒绝、迟归还不泄漏)。

## 可观测性

框架不绑定指标系统,只暴露两个钩子:`Config.OnPageEvent`(每请求一个事件:
kind/outcome/status/总时长/渲染时长;回调 panic 被吞)与
`Runtime.RendererPoolStats()`(池快照,gojs 实现 `renderer.PoolStatsProvider`)。
宿主自己接 Prometheus 等。关键信号:`outcome=fallback` 应长期为 0;
discard 计数突增 = 渲染中断频繁;渲染 p99 持续偏高才考虑渲染缓存。

## 已评估过的取舍(不要重新发明,也不要无理由推翻)

- 非阻塞导航 + 占位数据 + 路由映射骨架:选定的 UX 模型。曾试过全阻塞
  (每页零占位但点击有延迟)和 App.vue 统一门控(整页灰降级),都被否决。
- TS 类型从 Go codegen:评估过,工具链成本 > 手写接口 + fixtures 护栏,不做。
- goja 内存上限:无原生支持,靠池上限 + 200 次回收 + 超时中断兜底,不自造。
- SWR 文档缓存:已实现(2026-07,信号是"回访页面仍出骨架")。失效规则:
  命中必重验证 + 全量 refresh 更新缓存 + 宿主在身份变化时 clearCached。
- hover 预取 / 访客页短缓存:仍按需,等指标(渲染 p99、骨架出现频率)给信号。
- 渲染器可插拔(`renderer.Factory`)保留,V8 等引擎由宿主注入,库内不内置。

## 项目结构与命令

- `runtime.go`:`Runtime` 生命周期(`New`/`MountGin`/`Close`)与 PageEvent;
  `server.go`:document 处理、模板预编译、CSR fallback;`page.go`:
  `PageResolver`/`PageResult`/URL 校验;`payload.go`:`ObjectPayload`;
  `admission.go`:并发闸;`locales/`:多语言能力面。
- `renderer/`:接口与 `PoolStats`;`engine/gojs`:内置渲染器与 runtime 池。
- `packages/gossr-vue/`:Vue 运行时(导航协调器、信封 codec、client/server
  入口、Vite preset);`cmd/gossr-init`:脚手架;`example/`:全功能示例。

```bash
go build ./... && go test ./... && go vet ./...
pnpm -C packages/gossr-vue run check     # test + typecheck + build + exports
npm --prefix example/web run typecheck && npm --prefix example/web test
```

本地联调:`DEV_MODE=1 DEV_SERVER_URL=http://127.0.0.1:3333`。
运行时旋钮:`Config.MaxConcurrentPages`/`PageTimeout`/`RenderTimeout`;
`GOJA_POOL_SIZE`/`GOJA_RUNTIME_MAX_RENDERS`。

## 代码风格 / Commit / 安全

- 一律 `gofmt`;错误逐层带上下文返回,不吞。注释只写代码表达不了的约束,
  不写"这行做了什么"。
- commit 用祈使句(`feat: ...`/`fix: ...`/`refactor!: ...`,破坏性变更加 `!`)。
  PR 描述变更、影响面与验证方式。
- 不要提交真实 cookie/token 或前端产物;快照数据不得含原始凭证——宿主在
  生成端强制(参考 subapi 的 `validatePageDataSafety`:null 白名单 +
  token 黑名单),客户端只做结构判别。
