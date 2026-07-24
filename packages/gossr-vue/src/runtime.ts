import { createApp as createClientApp, createSSRApp } from 'vue'
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

  let navigation: ManagedNavigationCoordinator<Document>
  try {
    navigation = createNavigationCoordinator({
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
  let mounted = false
  let disposing = false
  let disposed = false
  let storedDisposeError: unknown

  let initialTracker: ReturnType<typeof createInitialNavigationTracker> | undefined

  try {
    initialTracker = options.platform === 'client'
      ? createInitialNavigationTracker(router, frameworkDisposers)
      : undefined

    installFrameworkHooks({
      appId: definition.appId,
      platform: options.platform,
      router,
      navigation,
      frameworkDisposers,
    })

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

    runCleanup(errors, navigation.dispose)
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

function installFrameworkHooks<Document>(options: {
  appId: string
  platform: GossrPlatform
  router: Router
  navigation: ManagedNavigationCoordinator<Document>
  frameworkDisposers: Array<() => void>
}) {
  const {
    appId,
    platform,
    router,
    navigation,
    frameworkDisposers,
  } = options

  if (platform === 'server') {
    frameworkDisposers.push(router.beforeResolve(async (to) => {
      const preparation = await navigation.prepare(
        documentURLFromRouter(to.fullPath),
      )
      if (preparation.kind === 'ready')
        return true
      if (preparation.kind === 'redirect')
        return preparation.location
      if (preparation.kind === 'error')
        throw preparation.error
      return false
    }))
  }
  else {
    frameworkDisposers.push(router.onError((error, to) => {
      if (!recoverStaleClientRoute(
        appId,
        error,
        documentURLFromRouter(to.fullPath),
      )) {
        console.error('[router] navigation failed', error)
      }
    }))
  }

  frameworkDisposers.push(router.afterEach((to, _from, failure) => {
    if (failure)
      return

    if (platform === 'server') {
      navigation.commit(documentURLFromRouter(to.fullPath))
      return
    }

    clearStaleClientRecovery(appId)
    void settleClientNavigation(router, navigation, to.fullPath).catch((error) => {
      console.error('[navigation] page data update failed', error)
    })
  }))
}

async function settleClientNavigation<Document>(
  router: Router,
  navigation: ManagedNavigationCoordinator<Document>,
  routerFullPath: string,
) {
  const documentURL = documentURLFromRouter(routerFullPath)
  const preparation = await navigation.prepare(documentURL)
  if (
    documentURLFromRouter(router.currentRoute.value.fullPath) !== documentURL
  ) {
    return
  }

  if (preparation.kind === 'ready') {
    navigation.commit(documentURL)
    return
  }
  if (preparation.kind === 'redirect')
    await router.replace(preparation.location)
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
