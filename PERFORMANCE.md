# gossr 性能优化报告

## 2026-07-26 复测(架构精简轮之后)

机器同前(Apple M3, darwin/arm64, Go 1.26, `wrk -t4 -c16 -d30s`, 同机压测)。
此轮之前的架构变更:模板启动预编译、goja 池内联、干净异常复用 runtime、
ABI 收缩为纯标记、旧 ABI 路径删除。**example 应用本身也变重了**
(数据信封 + i18n + 非阻塞导航链路,bundle 190KB → 202KB,单次渲染分配
717KB → 1.77MB),因此与 07-16 表格不构成同应用对比;本节数字是新基线。

### example 应用端到端(GIN_MODE=release)

| 配置 | 吞吐 | p50 | p90 | p99 | 负载后 RSS | 非预期错误 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 默认 GC | 890 req/s | 16.8 ms | 32.7 ms | 101 ms | 143 MiB | 0 |
| `GOGC=300 GOMEMLIMIT=512MiB` | 1,412 req/s | 9.9 ms | 24.0 ms | 120 ms | 378 MiB | 0 |

瓶颈是 JS 渲染 CPU + 分配速率(~1.6 GB/s 触发的 GC):并行微基准
`BenchmarkRendererExampleBundleParallel` 的换算吞吐 ≈ 920 ops/s,与
wrk 观测一致,说明 HTTP 层与模板拼接不构成瓶颈。注意 gin debug 模式
(每请求日志)会显著拉低吞吐并抬高 p99,压测必须 `GIN_MODE=release`。

### subapi 真实 bundle(线上 gojs 路径,`web/ssr_engine_bench_test.go`)

| 路由 | 串行耗时 | 每次渲染分配 |
| --- | ---: | ---: |
| `/`(营销首页) | 4.71 ms | 3.78 MB |
| `/dashboard` | 4.87 ms | 3.10 MB |
| `/dashboard/billing` | 5.00 ms | 3.43 MB |
| `/missing`(404) | 3.20 ms | 2.24 MB |
| billing 并行(8 核) | 2.11 ms/op | — |

单机文档渲染吞吐上限 ≈ 470 req/s(默认 GC,按并行 2.11 ms/op 换算;
`GOGC=300` 预计 ~700+)。两点让这个上限在实践中更宽裕:站内导航
(`/_ssr/data`)只走 resolver 不渲染,不消耗此预算;p99 尖刺主要来自
runtime 200 次回收的重建(~17.8 ms/次)与 GC,均有界。若指标显示
document 渲染逼近上限,优先级依次是:`GOGC`/`GOMEMLIMIT` 调优 →
访客页短缓存 → 渲染层降分配。

---


日期：2026-07-16。测试机器：Apple M3，darwin/arm64，Go 1.25，`wrk -t4 -c16`。示例使用生产构建后的真实 Vue SSR bundle，不以简单字符串脚本代替业务渲染。所有数字都是本机观测值，应在目标部署机器复测。

## 最终结论

当前默认设计在“纯 Go、无需 CGO、宿主易部署”的约束下已经达到较好的平衡。最终默认配置的 30 秒 HTTP 压测为：

| 配置 | 吞吐 | p50 | p90 | p99 | 负载后 RSS | 错误 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 初始版本，默认 GC | 2,406 req/s | 5.96 ms | 13.88 ms | 25.67 ms | 212 MiB | 0 |
| 最终版本，默认 GC | 3,201 req/s | 4.75 ms | 9.09 ms | 14.08 ms | 113 MiB | 0 |
| 最终版本，`GOGC=300` | 4,575 req/s | 2.97 ms | 7.64 ms | 13.74 ms | 292 MiB | 0 |

默认配置相对初始版本吞吐提升约 33%，p99 降低约 45%，压力期 RSS 降低约 47%。`GOGC=300` 相对初始吞吐提升约 90%，代价是更高内存。

真实 bundle 的单线程热路径中位数从约 `0.978 ms/op、784 KB/op、10,365 allocs/op` 降至约 `0.870 ms/op、717 KB/op、9,542 allocs/op`：耗时降低约 11%，分配字节降低约 8.5%，分配次数降低约 7.9%。端到端收益更大，主要来自更合适的 runtime 回收与 GC 行为。

## 最新前端依赖复测

随后将直接依赖升级到 Vue `3.5.39`、Vue Router `5.2.0`、Vite `8.1.4`、Vitest `4.1.10`、TypeScript `7.0.2` 等 npm 当前最新版。TypeScript 7 配置移除了 `baseUrl`，Vite 8 改用 Oxc，并以 `codeSplitting: false` 生成 Goja 所需的单文件 bundle。

- SSR bundle 从 `194,298` 字节降到 `189,919` 字节，约减少 2.3%。
- 热渲染同机即时对比从约 `0.942 ms/op、716.6 KB/op、9,537 allocs/op` 变为约 `0.969 ms/op、719.8 KB/op、9,708 allocs/op`，解释器微基准约慢 3%。
- 旧、新二进制交替 HTTP A/B 中，旧版两轮为 `2,474 / 2,450 req/s`，新版两轮为 `2,559 / 2,669 req/s`；新版端到端没有吞吐回退，但负载 RSS 约增加 16–20 MiB。
- Terser 输出增加约 25 KB/op 和 250 allocs/op；外置 esbuild 输出更大且没有执行收益，均已拒绝，保留 Vite 8 推荐的 Oxc。

bundle 增长首先影响每个 Goja runtime 的脚本解析、模块初始化和常驻内存；单次请求主要受当前匹配路由实际执行的组件树影响，不会与总 bundle 字节数等比例变慢。由于当前 SSR 为自包含单文件并在 runtime 回收后重新初始化，大型项目应持续记录 bundle 字节数、`BenchmarkRendererStartupExampleBundle` 和真实路由矩阵，而不能只观察 gzip 体积。

## 优化过程

1. 建立真实基线并做 CPU/alloc profile。超过 90% 的分配来自 Goja 执行 Vue SSR，而非 Gin 或 HTML 注入；runtime 首次执行 bundle 约 `16.6 ms`，因此优化重点放在减少每次 Vue app 创建的工作和控制 runtime 保留堆。
2. `useSsrData` 直接返回共享 `ShallowRef`，取消每个组件额外创建的 computed；初始 state 不再做无意义浅拷贝。
3. 每个 app 只创建一个 locale computed 与翻译函数，通过 provide/inject 共享，避免每个组件重复推导 URL locale。
4. layout glob 与 registry 移到模块初始化阶段，每个 Goja runtime 只构建一次；locale 路由纯函数也从 layout setup 移到模块，降低每请求闭包数量并改善可测试性。
5. 服务端不再安装每请求异步鉴权 guard。缓存静态受保护路由集合，只对可能受保护的 URL 调用 `router.resolve()`；公开页面保留快速路径。复用 router 连续渲染同 URL 时仍会重新检查 session，标准 `addRoute/removeRoute/clearRoutes` 会自动失效缓存，不要求业务改用专用 API。
6. 补充 `/protected`、本地化 alias、重复 URL、尾斜杠和 session 隔离测试。测试发现 `/protected/` 可匹配 Vue 路由但未命中快速集合，曾造成 SSR 鉴权绕过；缓存现同时登记尾斜杠形式。
7. Go payload 的普通属性由通用 descriptor 定义改为 `Object.Set`，仅 `__proto__` 保留显式 data property，兼顾性能和原型污染语义。合成 payload 从约 `4,637 B / 73 allocs` 降到 `4,205 B / 64 allocs`。
8. payload 循环检测由哈希表改为最大深度 100 的有界祖先栈。合成渲染约从 `2.8 µs` 降至 `2.5 µs`，循环与深度错误仍被拒绝。
9. 删除首页、404、loading 状态和保护页中只做简单字段转发的 computed。首页和 404 各减少约 50 次 JS 分配、5–6 KB 分配；复杂且需要客户端响应式缓存的 computed 保留。
10. 对 runtime 回收周期进行真实 HTTP 搜索。`maxUses=200` 两轮约 `2,705 req/s / p99 8.9 ms / 73 MiB`；`1000` 两轮约 `2,646 req/s / p99 12.3 ms / 115 MiB`，因此默认从 1000 调到 200。
11. 对 GC 搜索。`GOGC=300` 在本机达到吞吐甜点，继续提高到 400 已无收益。加入 `GOMEMLIMIT=256MiB` 时测得约 `4,687 req/s / p99 13.13 ms / 208 MiB RSS`，适合作为内存受限部署的起点。

## 被撤回或拒绝的方案

- 跳过服务端 `app.unmount()`：热路径快约 6%，但关闭 runtime 回收时保留堆增长从约 `9 KB/次` 放大到约 `50 KB/次`。这是泄漏换性能，已撤回。恢复卸载后，`maxUses=200` 的 3,800 次诊断最终堆稳定在约 `2.87 MiB`。
- 每次公开请求都执行 `router.resolve()`：鉴权正确但真实 bundle 慢约 3%，并增加约 18 KB/400 次分配；改为受保护路由候选缓存。
- payload 一律 `json.Marshal + JSON.parse`：复杂 payload 的个别微基准略快，但真实页面标量 payload 更慢，合成路径约慢 2.5 倍且分配更多；保留原生转换，特殊类型才回退 JSON。
- 服务端完全取消鉴权解析：复用 router 连续访问相同保护 URL 时可能跳过 guard，存在 session 串请求风险；改为显式 `resolveServerRenderURL`。
- 无限复用 runtime：即使正确 `unmount`，单 runtime 仍会随 Vue/Router 工作量持续增长；关闭回收的 3,800 次测试从约 `5 MiB` 增到约 `40 MiB`，生产不应设 `GOJA_RUNTIME_MAX_RENDERS=0`。
- QuickJS：用相同 194 KB Vue bundle 实测，QuickJS-ng/CGO 热渲染约 `1.03–1.13 ms/op`，原生 Go 对象传参仍约 `1.03–1.13 ms/op`，runtime 初始化约 `37 ms`；Goja 分别约 `0.87 ms/op` 和 `16.6 ms`。QuickJS 的 Go heap 指标不包含 C 堆，不能直接比较内存，而且 quickjs-go 当前仍明确标注未准备好生产使用。因此不引入内置 QuickJS。参考 [quickjs-go README](https://github.com/buke/quickjs-go) 与 [QuickJS-ng](https://github.com/quickjs-ng/quickjs)。
- 更新到另一版 Goja：此前试验使长期保留堆从约 12 MiB 增到约 63 MiB，没有足够收益，已撤回。

## 建议配置

- 默认：保持 `GOJA_POOL_SIZE=GOMAXPROCS`、`GOJA_RUNTIME_MAX_RENDERS=200`、Go 默认 GC。适合优先稳定与内存效率。
- 吞吐优先：从 `GOGC=200`、`GOGC=300` 逐步测试，并设置符合容器余量的 `GOMEMLIMIT`。本机 8 runtime 下 `GOGC=300 GOMEMLIMIT=256MiB` 是较好的起点，不应直接当作跨机器默认值。
- 内存优先：降低 `GOJA_POOL_SIZE`。本机 `GOGC=300` 下池 1/2/4/8 的短压吞吐约为 `1,232 / 2,191 / 3,864 / 4,490 req/s`，RSS 约为 `59 / 75 / 108 / 238 MiB`；池 8 的吞吐更高，但 p99 从池 4 的约 `6.95 ms` 升到约 `14.11 ms`。并行度应按实例内存预算和尾延迟选择，不必机械等于 CPU 数。
- 不要关闭 runtime 回收。业务 bundle 比示例更复杂时，建议压测 `100/200/300` 三个回收周期。
- 前端保持 SSR 专用构建：`build.ssr=true`、依赖内联、Vue runtime-only ESM、自包含 bundle。导航继续由文件路由的 `meta.nav` 自动维护；业务页面无需手工维护 base nav。

## 验证命令

```bash
npm --prefix example/web run typecheck
npm --prefix example/web test
npm --prefix example/web run build

go build ./...
go test ./...
go test -race ./...
go vet ./...

GOJS_HEAP_TEST=1 go test -run '^TestExampleBundleRetainedHeap$' -count=1 -v ./renderer/engine/gojs
go test -run '^$' -bench '^BenchmarkRenderer(Synthetic|ExampleBundle|ExampleRoutes)' -benchmem -benchtime=3s -count=5 ./renderer/engine/gojs
```

## 剩余瓶颈与停止条件

最终 profile 的主要成本仍是 Vue SSR 在解释器中创建对象、VNode/SSR context 与响应式结构，属于业务框架工作量。继续在 Go 边界做微调的上限很低；进一步数量级提升需要页面级安全缓存、预渲染/静态化，或由宿主外部注入 JIT 引擎。这些策略会改变一致性、安全或部署模型，不适合作为 gossr 默认行为。当前保留的改动均有可重复收益或显著易用性/正确性价值，低收益高复杂度方案已停止。
