import { routes } from 'vue-router/auto-routes'

import App from './App.vue'
import { createLocaleTextContext, localeTextKey } from '~/composables/useLocaleText'
import { defineGossrApp } from '@daodao97/gossr-vue'
import { parsePageData } from '~/page-data'
import type { PageData } from '~/page-data'

export default defineGossrApp<PageData>({
  appId: 'gossr-example',
  root: App,
  routes,
  pageData: {
    parse: parsePageData,
    url: pageData => pageData.url,
  },
  setup({ app, router }) {
    app.provide(localeTextKey, createLocaleTextContext(router))
  },
})
