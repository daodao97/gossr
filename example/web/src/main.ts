import { createApp, createSSRApp } from 'vue'
import { createMemoryHistory, createRouter, createWebHistory } from 'vue-router'
import { routes } from 'vue-router/auto-routes'

import App from './App.vue'

import { createSsrDataContext, ssrDataKey, type SsrState } from '~/composables/useSsrData'
import { createI18nInstance } from '~/modules/i18n'

const isServer = typeof window === 'undefined'

export function makeApp(initialState: SsrState = {}) {
  const app = isServer ? createSSRApp(App) : createApp(App)
  const router = createRouter({
    history: isServer ? createMemoryHistory() : createWebHistory('/'),
    routes,
  })
  const ssrContext = createSsrDataContext(initialState)
  const i18n = createI18nInstance()

  app.use(router)
  app.provide(ssrDataKey, ssrContext)

  return {
    app,
    router,
    i18n,
    ssrContext,
  }
}
