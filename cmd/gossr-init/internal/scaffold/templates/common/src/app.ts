import { routes } from 'vue-router/auto-routes'

import App from './App.vue'
import { navigationKey } from '~/composables'
import { defineGossrApp } from '@daodao97/gossr-vue'
import { parsePageDocument } from '~/page-document'
import type { PageDocument } from '~/page-document'

export default defineGossrApp<PageDocument>({
  appId: '__GOSSR_PROJECT_NAME__',
  root: App,
  routes,
  document: {
    parse: parsePageDocument,
    url: document => document.url,
  },
  setup({ app, navigation }) {
    app.provide(navigationKey, navigation)
  },
})
