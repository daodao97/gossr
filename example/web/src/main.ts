import { createApp, createSSRApp } from 'vue'
import { createMemoryHistory, createRouter, createWebHistory } from 'vue-router'
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

async function resolveAuthFromRoute(path: string): Promise<boolean> {
  if (typeof window === 'undefined')
    return false

  const url = new URL(path, window.location.origin)
  const endpoint = `/_ssr/data${url.pathname}${url.search}`
  const response = await fetch(endpoint, {
    credentials: 'same-origin',
    headers: {
      'Accept': 'application/json',
      'X-SSR-Fetch': '1',
    },
  })

  if (!response.ok)
    return false

  const data = await response.json()
  if (!data || typeof data !== 'object')
    return false

  return isAuthenticated(data as SsrState)
}

export function makeApp(initialState: SsrState = {}) {
  const app = isServer ? createSSRApp(App) : createApp(App)
  const router = createRouter({
    history: isServer ? createMemoryHistory() : createWebHistory('/'),
    routes,
  })
  const ssrContext = createSsrDataContext(initialState)
  const i18n = createI18nInstance()

  router.beforeEach((to) => {
    const requiresAuth = to.matched.some(record => record.meta.requiresAuth === true)
    if (!requiresAuth)
      return true

    if (isAuthenticated(ssrContext.state.value))
      return true

    return resolveAuthFromRoute(to.fullPath)
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
  }
}
