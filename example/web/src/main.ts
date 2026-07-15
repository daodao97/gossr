import { createApp, createSSRApp } from 'vue'
import { createMemoryHistory, createRouter, createWebHistory, type Router } from 'vue-router'
import { routes } from 'vue-router/auto-routes'

import App from './App.vue'

import { createLocaleTextContext, localeTextKey } from '~/composables/useLocaleText'
import { createSsrDataContext, ssrDataKey, type SsrState } from '~/composables/useSsrData'
import { createI18nInstance, isSupportedLocale } from '~/modules/i18n'

const isServer = typeof window === 'undefined'
const serverAuthPolicyCache = new WeakMap<Router, {
  dynamic: boolean
  protectedPaths: ReadonlySet<string>
}>()
const trackedServerRouters = new WeakSet<Router>()

function trackServerRouteMutations(router: Router): void {
  if (trackedServerRouters.has(router))
    return

  trackedServerRouters.add(router)
  const invalidate = () => serverAuthPolicyCache.delete(router)

  const addRoute = router.addRoute.bind(router) as unknown as (...args: unknown[]) => () => void
  router.addRoute = ((...args: unknown[]) => {
    const remove = addRoute(...args)
    invalidate()
    return () => {
      remove()
      invalidate()
    }
  }) as Router['addRoute']

  const removeRoute = router.removeRoute.bind(router)
  router.removeRoute = ((name) => {
    removeRoute(name)
    invalidate()
  }) as Router['removeRoute']

  const clearRoutes = router.clearRoutes.bind(router)
  router.clearRoutes = (() => {
    clearRoutes()
    invalidate()
  }) as Router['clearRoutes']
}

function isAuthenticated(state: SsrState): boolean {
  const session = state.session
  if (!session || typeof session !== 'object')
    return false

  const user = (session as Record<string, unknown>).user
  if (!user || typeof user !== 'object')
    return false

  const email = (user as Record<string, unknown>).email
  return typeof email === 'string' && email.trim().length > 0
}

function sessionDemoPathFor(pathname: string): string {
  const trimmed = pathname.replace(/^\/+/, '')
  const firstSegment = trimmed.split('/')[0]
  if (isSupportedLocale(firstSegment))
    return `/${firstSegment}/session-demo`

  return '/session-demo'
}

export function resolveServerRenderURL(router: Router, rawURL: string, state: SsrState): string {
  if (isAuthenticated(state))
    return rawURL

  let policy = serverAuthPolicyCache.get(router)
  if (!policy) {
    trackServerRouteMutations(router)
    const protectedPaths = new Set<string>()
    let dynamic = false
    for (const record of router.getRoutes()) {
      if (record.meta.requiresAuth !== true)
        continue
      if (record.path.includes(':'))
        dynamic = true
      else {
        protectedPaths.add(record.path)
        if (record.path !== '/' && !record.path.endsWith('/'))
          protectedPaths.add(`${record.path}/`)
      }
    }
    policy = { dynamic, protectedPaths }
    serverAuthPolicyCache.set(router, policy)
  }

  const queryIndex = rawURL.indexOf('?')
  const hashIndex = rawURL.indexOf('#')
  const pathEnd = queryIndex < 0 ? hashIndex : hashIndex < 0 ? queryIndex : Math.min(queryIndex, hashIndex)
  const pathname = pathEnd < 0 ? rawURL : rawURL.slice(0, pathEnd)
  if (!policy.dynamic && !policy.protectedPaths.has(pathname))
    return rawURL

  const target = router.resolve(rawURL)
  const requiresAuth = target.matched.some(record => record.meta.requiresAuth === true)
  if (!requiresAuth)
    return target.fullPath

  return router.resolve({
    path: sessionDemoPathFor(target.path),
    query: { next: target.fullPath },
  }).fullPath
}

async function resolveCurrentSession(): Promise<boolean> {
  if (typeof window === 'undefined')
    return false

  const response = await fetch('/demo/session/status', {
    credentials: 'same-origin',
    headers: {
      'Accept': 'application/json',
    },
  })

  if (!response.ok)
    return false

  const data = await response.json()
  if (!data || typeof data !== 'object')
    return false

  return (data as Record<string, unknown>).authenticated === true
}

export function createAppRouter(): Router {
  const router = createRouter({
    history: isServer ? createMemoryHistory() : createWebHistory('/'),
    routes,
  })
  if (isServer)
    trackServerRouteMutations(router)
  return router
}

interface AppOptions {
  hydrate?: boolean
  router?: Router
}

export function makeApp(
  initialState: SsrState = {},
  options: AppOptions = {},
) {
  const router = options.router ?? createAppRouter()
  const hydrate = options.hydrate ?? true
  const app = isServer || hydrate ? createSSRApp(App) : createApp(App)
  const ssrContext = createSsrDataContext(initialState)
  // 服务端翻译直接从 URL 推断 locale，不需要创建仅供客户端持久化使用的 ref。
  const i18n = isServer ? null : createI18nInstance()

  const removeAuthGuard = isServer ? () => {} : router.beforeEach((to) => {
    const requiresAuth = to.matched.some(record => record.meta.requiresAuth === true)
    if (!requiresAuth)
      return true

    return resolveCurrentSession()
      .then((authed) => {
        if (authed)
          return true

        return {
          path: sessionDemoPathFor(to.path),
          query: {
            next: to.fullPath,
          },
          replace: true,
        }
      })
      .catch(() => {
        return {
          path: sessionDemoPathFor(to.path),
          query: {
            next: to.fullPath,
          },
          replace: true,
        }
      })
  })

  app.use(router)
  app.provide(ssrDataKey, ssrContext)
  app.provide(localeTextKey, createLocaleTextContext(router))

  return {
    app,
    router,
    i18n,
    ssrContext,
    dispose() {
      removeAuthGuard()
      app.unmount()
    },
  }
}
