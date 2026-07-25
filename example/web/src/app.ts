import { routes } from 'vue-router/auto-routes'

import App from './App.vue'
import { navigationKey } from '~/composables/usePageDocument'
import { createLocaleTextContext, localeTextKey } from '~/composables/useLocaleText'
import { defineGossrApp } from '@daodao97/gossr-vue'
import { parsePageDocument } from '~/page-document'
import type { PageDocument } from '~/page-document'

export default defineGossrApp<PageDocument>({
  appId: 'gossr-example',
  root: App,
  routes,
  document: {
    parse: parsePageDocument,
    url: document => document.url,
  },
  setup({ app, router, navigation }) {
    app.provide(navigationKey, navigation)
    app.provide(localeTextKey, createLocaleTextContext(router))
  },
})
