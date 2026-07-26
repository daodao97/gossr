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
import { clearStaleClientRecovery, recoverStaleClientRoute } from './stale-client.js'
import {
  canonicalNavigationURL,
  documentURLFromRouter,
  navigationURLsMatch,
} from './url.js'
import type { ParsedDocument } from './document.js'
import type {
  GossrAppDefinition,
  GossrPlatform,
  NavigationCoordinator,
} from './types.js'
import type { ManagedNavigationCoordinator } from './navigation.js'

interface RuntimeOptions<Document> {
  platform: GossrPlatform
  initial?: ParsedDocument<Document>
  hydrate?: boolean
  navigateDocument?: (url: string) => void
}

export interface GossrApplicationRuntime<Document> {
  readonly app: App
  readonly router: Router
  readonly navigation: NavigationCoordinator<Document>
  readonly platform: GossrPlatform
  readonly initialNavigation?: Promise<RouteLocationNormalized>
  mount: (container: string | Element) => ComponentPublicInstance
  dispose: () => void
}

export function createApplicationRuntime<Document>(
  definition: GossrAppDefinition<Document>,
  options: RuntimeOptions<Document>,
): GossrApplicationRuntime<Document> {
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

  let managedNavigation: ManagedNavigationCoordinator<Document>
  try {
    managedNavigation = createNavigationCoordinator({
      codec: definition.document,
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
        state: clientNavigation,
        frameworkDisposers,
      })
    }

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

function createVueApp<Document>(
  definition: GossrAppDefinition<Document>,
  options: RuntimeOptions<Document>,
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

function createPublicNavigation<Document>(
  platform: GossrPlatform,
  router: Router,
  navigation: ManagedNavigationCoordinator<Document>,
  clientState: ClientNavigationState | undefined,
): NavigationCoordinator<Document> {
  const current = platform === 'client'
    ? computed(() => {
        const route = router.currentRoute.value
        if (route === START_LOCATION)
          return navigation.current.value
        const documentURL = safeDocumentURL(route.fullPath)
        return documentURL === undefined
          ? undefined
          : navigation.documentFor(documentURL)
      })
    : navigation.current

  return {
    current,
    loading: navigation.loading,
    error: navigation.error,
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
      if (result.kind === 'error')
        clientState.fallback(documentURL)
      return false
    },
  }
}

function installClientNavigationObservers<Document>(options: {
  appId: string
  router: Router
  navigation: ManagedNavigationCoordinator<Document>
  state: ClientNavigationState
  frameworkDisposers: Array<() => void>
}) {
  const {
    appId,
    router,
    navigation,
    state,
    frameworkDisposers,
  } = options

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
    if (prepared) {
      state.preparedRoutes.delete(to)
      releasePendingRoute(state)
      if (!failure && !navigation.commit(prepared.documentURL))
        state.fallback(to.fullPath)
    }

    if (failure) {
      if (
        !prepared
        && !handled
        && !isNavigationFailure(failure, NavigationFailureType.cancelled)
      ) {
        navigation.cancelRoute()
      }
      return
    }

    clearStaleClientRecovery(appId)
  }))
}

function installClientNavigationLoader<Document>(options: {
  router: Router
  navigation: ManagedNavigationCoordinator<Document>
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

    state.pendingRoutes += 1
    let releasePending = true
    try {
      const preparation = await navigation.prepare(documentURL, {
        force: from !== START_LOCATION,
      })
      if (preparation.kind === 'ready') {
        state.preparedRoutes.set(to, { documentURL })
        releasePending = false
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
  }))
}

function releasePendingRoute(state: ClientNavigationState) {
  state.pendingRoutes = Math.max(0, state.pendingRoutes - 1)
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

export async function navigateServerRuntime<Document>(
  runtime: GossrApplicationRuntime<Document>,
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
