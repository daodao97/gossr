import { createApp, createSSRApp } from 'vue'
import { createMemoryHistory, createRouter, createWebHistory, type Router } from 'vue-router'
import { routes } from 'vue-router/auto-routes'

import App from './App.vue'

import { createSsrDataContext, ssrDataKey, type SsrState } from '~/composables/useSsrData'
import { createI18nInstance, isSupportedLocale } from '~/modules/i18n'

const isServer = typeof window === 'undefined'

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
  return createRouter({
    history: isServer ? createMemoryHistory() : createWebHistory('/'),
    routes,
  })
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

  const removeAuthGuard = router.beforeEach((to) => {
    const requiresAuth = to.matched.some(record => record.meta.requiresAuth === true)
    if (!requiresAuth)
      return true

    if (isServer)
      return isAuthenticated(ssrContext.state.value) || {
        path: sessionDemoPathFor(to.path),
        query: { next: to.fullPath },
        replace: true,
      }

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
