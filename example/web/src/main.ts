import { createApp, createSSRApp, ref } from 'vue'
import type { Ref } from 'vue'

import App from './App.vue'

import { createSsrDataContext, ssrDataKey, type SsrState } from '~/composables/useSsrData'
import { createI18nInstance } from '~/modules/i18n'

interface RouteMeta {
  ssrData?: boolean
}

interface RouteState {
  fullPath: string
  path: string
  meta: RouteMeta
}

type NavigationGuard = (to: RouteState, from: RouteState, next: () => void) => void | Promise<void>

interface AppRouter {
  currentRoute: Ref<RouteState>
  beforeResolve: (guard: NavigationGuard) => void
  replace: (path: string) => Promise<void>
  push: (path: string) => Promise<void>
  isReady: () => Promise<void>
}

const isServer = typeof window === 'undefined'

export function makeApp(initialState: SsrState = {}) {
  const app = isServer ? createSSRApp(App) : createApp(App)
  const router = createRouter(!isServer)
  const ssrContext = createSsrDataContext(initialState)
  const i18n = createI18nInstance()

  app.provide(ssrDataKey, ssrContext)

  return {
    app,
    router,
    i18n,
    ssrContext,
  }
}

function createRouter(isClient: boolean): AppRouter {
  const guards: NavigationGuard[] = []
  const currentRoute = ref<RouteState>(parseRoute('/'))
  let navigation = Promise.resolve()

  const navigate = (targetPath: string, mode: 'push' | 'replace' | 'pop') => {
    navigation = navigation.then(async () => {
      const to = parseRoute(targetPath)
      const from = currentRoute.value

      for (const guard of guards)
        await runGuard(guard, to, from)

      currentRoute.value = to

      if (!isClient)
        return

      if (mode === 'replace')
        window.history.replaceState(null, '', to.fullPath)
      else if (mode === 'push')
        window.history.pushState(null, '', to.fullPath)
    })

    return navigation
  }

  if (isClient) {
    window.addEventListener('popstate', () => {
      void navigate(currentPath(), 'pop')
    })
  }

  return {
    currentRoute,
    beforeResolve(guard) {
      guards.push(guard)
    },
    replace(path) {
      return navigate(path, 'replace')
    },
    push(path) {
      return navigate(path, 'push')
    },
    isReady() {
      return navigation
    },
  }
}

async function runGuard(guard: NavigationGuard, to: RouteState, from: RouteState): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    let settled = false
    const next = () => {
      if (settled)
        return
      settled = true
      resolve()
    }

    Promise.resolve(guard(to, from, next))
      .then(() => {
        if (!settled) {
          settled = true
          resolve()
        }
      })
      .catch(reject)
  })
}

function parseRoute(rawPath: string): RouteState {
  const url = new URL(rawPath, 'http://ssr.local')
  const fullPath = `${url.pathname}${url.search}${url.hash}`

  return {
    fullPath,
    path: url.pathname,
    meta: {},
  }
}

function currentPath() {
  return `${window.location.pathname}${window.location.search}${window.location.hash}`
}
