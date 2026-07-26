import { routes } from 'vue-router/auto-routes'

import App from './App.vue'
import { defineGossrApp } from '@daodao97/gossr-vue'
import { parsePageData } from '~/page-data'
import type { PageData } from '~/page-data'

export default defineGossrApp<PageData>({
  appId: '__GOSSR_PROJECT_NAME__',
  root: App,
  routes,
  pageData: {
    parse: parsePageData,
    url: pageData => pageData.url,
  },
})
