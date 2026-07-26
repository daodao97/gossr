import { computed, createApp as createClientApp, createSSRApp } from 'vue'
import {
  NavigationFailureType,
  START_LOCATION,
  createMemoryHistory,
  createRouter,
  createWebHistory,
  isNavigationFailure,
} from 'vue-router'
import type { App, ComponentPublicInstance } from 'vue'
import type {
  NavigationFailure,
  RouteLocationNormalized,
  RouteRecordRaw,
  Router,
  RouterHistory,
} from 'vue-router'

import {
  SynchronousDisposableScope,
  UnsupportedAsyncLifecycleError,
  attachCleanupError,
  cleanupError,
  consumeThenableRejection,
  isThenable,
} from './lifecycle.js'
import { createNavigationCoordinator, fetchNavigationOutcome } from './navigation.js'
import type { ManagedNavigationCoordinator, NavigationPreparation } from './navigation.js'
import { clearStaleClientRecovery, recoverStaleClientRoute } from './stale-client.js'
import { navigationInjectionKey } from './use-navigation.js'
import {
  canonicalNavigationURL,
  documentURLFromRouter,
  navigationURLsMatch,
} from './url.js'
import type { ParsedPageData } from './page-data.js'
import type {
  GossrAppDefinition,
  GossrPlatform,
  NavigationCoordinator,
} from './types.js'

interface RuntimeOptions<PageData> {
  platform: GossrPlatform
  initial?: ParsedPageData<PageData>
  hydrate?: boolean
  navigateDocument?: (url: string) => void
}

export interface GossrApplicationRuntime<PageData> {
  readonly app: App
  readonly router: Router
  readonly navigation: NavigationCoordinator<PageData>
  readonly platform: GossrPlatform
  readonly initialNavigation?: Promise<RouteLocationNormalized>
  mount: (container: string | Element) => ComponentPublicInstance
  dispose: () => void
}

export function createApplicationRuntime<PageData>(
  definition: GossrAppDefinition<PageData>,
  options: RuntimeOptions<PageData>,
): GossrApplicationRuntime<PageData> {
  const app = createVueApp(definition, options)
  const history = createHistory(options.platform)
  let router: Router
  try {
    router = createRouter({
      history,
      // Go route dispatch is case-sensitive. Normalize every static record
      // here so file-generated routes, aliases, and nested children cannot
      // silently inherit Vue Router's case-insensitive default.
      sensitive: true,
      routes: caseSensitiveRouteRecords(definition.routes),
    })
  }
  catch (error) {
    throwAfterHistoryCleanup(error, history)
  }

  let managedNavigation: ManagedNavigationCoordinator<PageData>
  try {
    managedNavigation = createNavigationCoordinator({
      codec: definition.pageData,
      initial: options.initial,
      fetcher: options.platform === 'client' ? fetchNavigationOutcome : undefined,
    })
  }
  catch (error) {
    throwAfterHistoryCleanup(error, history)
  }

  const businessScope = new SynchronousDisposableScope()
  const frameworkDisposers: Array<() => void> = []
  const clientNavigation = options.platform === 'client'
    ? createClientNavigationState(options.navigateDocument)
    : undefined
  const navigation = createPublicNavigation(
    options.platform,
    router,
    managedNavigation,
    clientNavigation,
  )
  let mounted = false
  let disposing = false
  let disposed = false
  let storedDisposeError: unknown

  let initialTracker: ReturnType<typeof createInitialNavigationTracker> | undefined

  try {
    initialTracker = options.platform === 'client'
      ? createInitialNavigationTracker(router, frameworkDisposers)
      : undefined

    if (clientNavigation) {
      installClientNavigationObservers({
        appId: definition.appId,
        router,
        navigation: managedNavigation,
        publicNavigation: navigation,
        state: clientNavigation,
        frameworkDisposers,
      })
      installRestoredPageRevalidation(navigation, frameworkDisposers)
    }

    app.provide(
      navigationInjectionKey,
      navigation as NavigationCoordinator<unknown>,
    )

    let setupResult: unknown
    try {
      setupResult = definition.setup?.({
        app,
        router,
        navigation,
        platform: options.platform,
        onDispose: businessScope.onDispose,
      })
    }
    finally {
      businessScope.finishSetup()
    }

    if (isThenable(setupResult)) {
      consumeThenableRejection(setupResult)
      throw new UnsupportedAsyncLifecycleError('setup()')
    }
    if (setupResult !== undefined)
      throw new Error('setup() must not return a value; register cleanup with onDispose()')

    if (clientNavigation) {
      installClientNavigationLoader({
        router,
        navigation: managedNavigation,
        state: clientNavigation,
        frameworkDisposers,
      })
    }

    initialTracker?.arm()
    app.use(router)
  }
  catch (error) {
    let secondary: unknown
    try {
      dispose()
    }
    catch (cleanupFailure) {
      secondary = cleanupFailure
    }
    attachCleanupError(error, secondary)
    throw error
  }

  return {
    app,
    router,
    navigation,
    platform: options.platform,
    initialNavigation: initialTracker?.promise,
    mount,
    dispose,
  }

  function mount(container: string | Element) {
    if (options.platform !== 'client')
      throw new Error('Only a client gossr runtime can mount into the DOM')
    if (disposed)
      throw new Error('Cannot mount a disposed gossr runtime')

    try {
      const instance = app.mount(container)
      mounted = true
      return instance
    }
    catch (error) {
      mounted = hasMountedContainer(app)
      throw error
    }
  }

  function dispose() {
    if (disposed) {
      if (storedDisposeError)
        throw storedDisposeError
      return
    }
    if (disposing)
      return

    disposing = true
    const errors: unknown[] = []

    if (mounted || hasMountedContainer(app))
      runCleanup(errors, () => app.unmount())

    businessScope.disposeInto(errors)

    for (let index = frameworkDisposers.length - 1; index >= 0; index -= 1)
      runCleanup(errors, frameworkDisposers[index])
    frameworkDisposers.length = 0

    runCleanup(errors, managedNavigation.dispose)
    runCleanup(errors, history.destroy)

    storedDisposeError = cleanupError(errors)
    disposed = true
    disposing = false
    if (storedDisposeError)
      throw storedDisposeError
  }
}

function createVueApp<PageData>(
  definition: GossrAppDefinition<PageData>,
  options: RuntimeOptions<PageData>,
) {
  if (options.platform === 'server' || options.hydrate)
    return createSSRApp(definition.root)
  return createClientApp(definition.root)
}

function createHistory(platform: GossrPlatform): RouterHistory {
  return platform === 'server'
    ? createMemoryHistory('/')
    : createWebHistory('/')
}

function caseSensitiveRouteRecords(
  records: Readonly<RouteRecordRaw[]>,
): RouteRecordRaw[] {
  return records.map((record) => {
    const normalized: RouteRecordRaw = {
      ...record,
      sensitive: true,
    }
    if (record.children)
      normalized.children = caseSensitiveRouteRecords(record.children)
    return normalized
  })
}

interface PreparedClientRoute {
  documentURL: string
  // Present only for non-blocking (post-initial) navigations. The initial
  // navigation commits synchronously in afterEach so hydration never mounts
  // before its document is current.
  preparation?: Promise<NavigationPreparation>
}

interface ClientNavigationState {
  pendingRoutes: number
  preparedRoutes: WeakMap<RouteLocationNormalized, PreparedClientRoute>
  handledRoutes: WeakSet<RouteLocationNormalized>
  fallback: (url: string) => void
}

function createClientNavigationState(
  navigateDocument: (url: string) => void = url => window.location.assign(url),
): ClientNavigationState {
  let fallbackTarget: string | undefined

  return {
    pendingRoutes: 0,
    preparedRoutes: new WeakMap(),
    handledRoutes: new WeakSet(),
    fallback(url) {
      const safeURL = safeDocumentURL(url)
      if (!safeURL)
        return
      if (fallbackTarget !== undefined)
        return

      fallbackTarget = safeURL
      try {
        navigateDocument(safeURL)
      }
      catch (error) {
        fallbackTarget = undefined
        console.error('[navigation] browser fallback failed', error)
      }
    },
  }
}

function createPublicNavigation<PageData>(
  platform: GossrPlatform,
  router: Router,
  navigation: ManagedNavigationCoordinator<PageData>,
  clientState: ClientNavigationState | undefined,
): NavigationCoordinator<PageData> {
  // Stale-while-loading: `current` keeps the last committed document during a
  // client navigation so persistent chrome (viewer, layout) never flickers.
  // Page-level content gates on `ready` instead.
  const ready = platform === 'client'
    ? computed(() => {
        const route = router.currentRoute.value
        if (route === START_LOCATION)
          return navigation.current.value !== undefined
        const documentURL = safeDocumentURL(route.fullPath)
        return documentURL !== undefined
          && navigation.currentURL.value !== undefined
          && navigationURLsMatch(navigation.currentURL.value, documentURL)
      })
    : computed(() => navigation.current.value !== undefined)

  return {
    current: navigation.current,
    ready,
    loading: navigation.loading,
    error: navigation.error,
    clearCached: navigation.clearCached,
    async refresh() {
      if (platform !== 'client' || !clientState || clientState.pendingRoutes > 0)
        return false

      const documentURL = documentURLFromRouter(
        router.currentRoute.value.fullPath,
      )
      const result = await navigation.refresh(documentURL)
      if (result.kind === 'ready') {
        return navigationURLsMatch(
          documentURLFromRouter(router.currentRoute.value.fullPath),
          documentURL,
        )
      }
      if (result.kind === 'redirect') {
        try {
          return !(await router.replace(result.location))
        }
        catch {
          clientState.fallback(documentURL)
          return false
        }
      }
      // 同上:服务端结构化报错不升级为整页导航,保留当前页面与错误提示。
      if (result.kind === 'error' && result.code === undefined)
        clientState.fallback(documentURL)
      return false
    },
  }
}

function installClientNavigationObservers<PageData>(options: {
  appId: string
  router: Router
  navigation: ManagedNavigationCoordinator<PageData>
  publicNavigation: NavigationCoordinator<PageData>
  state: ClientNavigationState
  frameworkDisposers: Array<() => void>
}) {
  const {
    appId,
    router,
    navigation,
    publicNavigation,
    state,
    frameworkDisposers,
  } = options

  // 缓存命中的提交先展示上次的文档,随即走公开 refresh 重验证:
  // 新鲜数据无感替换;服务端已改判(如登出后的重定向)则由 refresh
  // 的正常通道处理(路由替换/整页兜底)。
  const revalidateIfServedFromCache = () => {
    if (navigation.consumeRevalidation())
      void publicNavigation.refresh()
  }

  frameworkDisposers.push(router.onError((error, to) => {
    const documentURL = safeDocumentURL(to.fullPath)
    if (!documentURL) {
      state.fallback(to.fullPath)
      return
    }
    const recovery = recoverStaleClientRoute(
      appId,
      error,
      documentURL,
    )
    if (recovery !== 'not-stale')
      return

    console.error('[router] navigation failed', error)
    state.fallback(documentURL)
  }))

  frameworkDisposers.push(router.afterEach((to, _from, failure) => {
    const prepared = state.preparedRoutes.get(to)
    const handled = state.handledRoutes.has(to)
    state.handledRoutes.delete(to)
    if (prepared)
      state.preparedRoutes.delete(to)

    if (failure) {
      if (prepared)
        releasePendingRoute(state)
      if (
        !handled
        && (!prepared || prepared.preparation)
        && !isNavigationFailure(failure, NavigationFailureType.cancelled)
      ) {
        navigation.cancelRoute()
      }
      return
    }

    if (prepared) {
      if (!prepared.preparation) {
        // Blocking (initial) navigation: the document was staged before the
        // route committed, so hydration mounts against its own data.
        releasePendingRoute(state)
        if (!navigation.commit(prepared.documentURL))
          state.fallback(to.fullPath)
        else
          revalidateIfServedFromCache()
      }
      else {
        // Non-blocking navigation: the route is already committed; settle the
        // document whenever its load finishes, unless a newer route took over.
        void prepared.preparation.then((preparation) => {
          releasePendingRoute(state)
          if (preparation.kind === 'cancelled')
            return
          const activeURL = safeDocumentURL(router.currentRoute.value.fullPath)
          if (
            activeURL === undefined
            || !navigationURLsMatch(activeURL, prepared.documentURL)
          ) {
            return
          }
          if (preparation.kind === 'ready') {
            if (!navigation.commit(prepared.documentURL))
              state.fallback(to.fullPath)
            else
              revalidateIfServedFromCache()
            return
          }
          if (preparation.kind === 'redirect') {
            router.replace(preparation.location).catch(() => {
              state.fallback(preparation.location)
            })
            return
          }
          // 服务端结构化报错(带 code,如超时/解析失败于服务端):整页导航
          // 只会把同一个失败放大成裸错误页。留在应用内——error ref 已置位,
          // 宿主的错误提示可见,站内切换仍然可用。只有无 code 的失败
          // (解析异常/网络中断,可能是版本偏差)才走整页导航自愈。
          if (preparation.kind === 'error' && preparation.code !== undefined)
            return
          state.fallback(prepared.documentURL)
        })
      }
    }

    clearStaleClientRecovery(appId)
  }))
}

function installClientNavigationLoader<PageData>(options: {
  router: Router
  navigation: ManagedNavigationCoordinator<PageData>
  state: ClientNavigationState
  frameworkDisposers: Array<() => void>
}) {
  const {
    router,
    navigation,
    state,
    frameworkDisposers,
  } = options

  frameworkDisposers.push(router.beforeResolve(async (to, from) => {
    if (to.matched.length === 0) {
      state.handledRoutes.add(to)
      state.fallback(to.fullPath)
      return false
    }

    let documentURL: string
    try {
      documentURL = documentURLFromRouter(to.fullPath)
    }
    catch {
      state.handledRoutes.add(to)
      state.fallback(to.fullPath)
      return false
    }

    if (
      from !== START_LOCATION
      && navigationURLsMatch(
        documentURLFromRouter(from.fullPath),
        documentURL,
      )
    ) {
      return true
    }

    if (from === START_LOCATION) {
      // The initial navigation stays blocking: hydration must mount against
      // the boot document, and there is no previous page to keep showing.
      state.pendingRoutes += 1
      let releasePending = true
      try {
        const preparation = await navigation.prepare(documentURL)
        if (preparation.kind === 'ready') {
          state.preparedRoutes.set(to, { documentURL })
          releasePending = false
          return true
        }
        if (preparation.kind === 'error' && preparation.code !== undefined) {
          // 服务端结构化报错(如 resolver 超时下发的 CSR 壳启动后再次超时):
          // 整页重载只会拿到同样的壳,形成重载循环。改为无数据挂载——
          // 路由照常提交,宿主以占位/骨架渲染并展示 error ref 驱动的提示。
          return true
        }
        state.handledRoutes.add(to)
        if (preparation.kind === 'redirect')
          return preparation.location
        if (preparation.kind === 'error')
          state.fallback(documentURL)
        return false
      }
      catch {
        state.handledRoutes.add(to)
        state.fallback(to.fullPath)
        return false
      }
      finally {
        if (releasePending)
          releasePendingRoute(state)
      }
    }

    // In-app navigation is non-blocking: commit the route immediately so the
    // page responds on click, and let afterEach settle the document when the
    // load finishes. `current` keeps the previous document until then.
    state.pendingRoutes += 1
    state.preparedRoutes.set(to, {
      documentURL,
      preparation: navigation.prepare(documentURL, { force: true }),
    })
    return true
  }))
}

function releasePendingRoute(state: ClientNavigationState) {
  state.pendingRoutes = Math.max(0, state.pendingRoutes - 1)
}

// bfcache 恢复的页面是内存快照,可能跨越了登出/换号(部分浏览器对
// no-store 页面仍启用 bfcache)。恢复时重验证当前文档:会话已变化时
// refresh 沿用服务端裁决——重定向走路由替换,失败退回整页导航。
function installRestoredPageRevalidation<PageData>(
  navigation: NavigationCoordinator<PageData>,
  frameworkDisposers: Array<() => void>,
) {
  if (typeof window === 'undefined')
    return

  const revalidate = (event: PageTransitionEvent) => {
    if (event.persisted)
      void navigation.refresh()
  }
  window.addEventListener('pageshow', revalidate)
  frameworkDisposers.push(() => {
    window.removeEventListener('pageshow', revalidate)
  })
}

function safeDocumentURL(fullPath: string) {
  try {
    return documentURLFromRouter(fullPath)
  }
  catch {
    return undefined
  }
}

function createInitialNavigationTracker(
  router: Router,
  frameworkDisposers: Array<() => void>,
) {
  let armed = false
  let settled = false
  let resolvePromise!: (route: RouteLocationNormalized) => void
  let rejectPromise!: (error: unknown) => void

  const promise = new Promise<RouteLocationNormalized>((resolve, reject) => {
    resolvePromise = resolve
    rejectPromise = reject
  })

  frameworkDisposers.push(router.afterEach((to, from, failure) => {
    if (!armed || settled || from !== START_LOCATION)
      return

    settled = true
    if (failure)
      rejectPromise(failure)
    else
      resolvePromise(to)
  }))
  frameworkDisposers.push(router.onError((error, _to, from) => {
    if (!armed || settled || from !== START_LOCATION)
      return

    settled = true
    rejectPromise(error)
  }))

  return {
    promise,
    arm() {
      armed = true
    },
  }
}

export async function navigateServerRuntime<PageData>(
  runtime: GossrApplicationRuntime<PageData>,
  rawTarget: string,
  documentURL: string,
) {
  if (runtime.platform !== 'server')
    throw new Error('navigateServerRuntime() requires a server runtime')

  const target = canonicalNavigationURL(rawTarget)
  const failure = await runtime.router.push(target)
  if (
    failure
    && !isNavigationFailure(failure, NavigationFailureType.duplicated)
  ) {
    throw navigationFailureError(failure)
  }

  await runtime.router.isReady()

  const routeURL = documentURLFromRouter(
    runtime.router.currentRoute.value.fullPath,
  )
  const currentDocument = runtime.navigation.current.value
  if (
    !navigationURLsMatch(routeURL, target)
    || documentURL !== target
    || currentDocument === undefined
  ) {
    throw new Error(
      `SSR route/document mismatch: target=${target}, route=${routeURL}, document=${documentURL}`,
    )
  }
}

function navigationFailureError(failure: NavigationFailure) {
  const error = new Error(
    `SSR router navigation failed (${failure.type}) for ${failure.to.fullPath}`,
  )
  error.name = 'GossrNavigationFailure'
  return error
}

function runCleanup(errors: unknown[], callback: () => void) {
  try {
    const result: unknown = callback()
    if (isThenable(result)) {
      consumeThenableRejection(result)
      throw new UnsupportedAsyncLifecycleError('A framework cleanup hook')
    }
  }
  catch (error) {
    errors.push(error)
  }
}

function hasMountedContainer(app: App) {
  return Boolean((app as App & { _container?: unknown })._container)
}

function throwAfterHistoryCleanup(primary: unknown, history: RouterHistory): never {
  let secondary: unknown
  try {
    const result: unknown = history.destroy()
    if (isThenable(result)) {
      consumeThenableRejection(result)
      secondary = new UnsupportedAsyncLifecycleError('Router history cleanup')
    }
  }
  catch (error) {
    secondary = error
  }
  attachCleanupError(primary, secondary)
  throw primary
}
