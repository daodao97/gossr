import type { SsrState } from '~/composables/useSsrData'

import { watch } from 'vue'
import { makeApp } from '~/main'
import {
  preferredLocalePathAfterHydration,
  readSavedLocale,
  saveLocale,
  shouldHydrateDocument,
} from '~/modules/client-startup'
import {
  availableLocales,
  getLocaleRef,
  localeFromPath,
} from '~/modules/i18n'

declare global {
  interface Window {
    __SSR_DATA__?: SsrState
  }
}

const ssrPayload = window.__SSR_DATA__
const hasInitialSsrPayload = !!ssrPayload && Object.keys(ssrPayload).length > 0
const initialState = ssrPayload ?? {}
const shouldHydrate = shouldHydrateDocument(document)
const { app, router, ssrContext, i18n } = makeApp(initialState, { hydrate: shouldHydrate })
if (!i18n)
  throw new Error('client i18n instance is unavailable')
const SSR_FETCH_TIMEOUT_MS = 5000
const persistentSsrKeys = new Set(['session', 'locale', 'siteOrigin', '__ssrFetchLoading'])
const localeRef = getLocaleRef(i18n)
let activeSsrFetchCount = 0

setSsrFetchLoading(false)

const savedLocale = readSavedLocale(window.localStorage)
localeRef.value = localeFromPath(window.location.pathname)

document.documentElement.setAttribute('lang', localeRef.value)
saveLocale(window.localStorage, localeRef.value)

watch(localeRef, (newLocale) => {
  if (availableLocales.includes(newLocale)) {
    saveLocale(window.localStorage, newLocale)
    document.documentElement.setAttribute('lang', newLocale)
  }
})

const fullPath = window.location.pathname + window.location.search + window.location.hash

let isFirstNavigation = true
let latestSsrFetchId = 0

router.beforeResolve((to, from) => {
  const targetLocale = localeFromPath(to.path)
  if (localeRef.value !== targetLocale)
    localeRef.value = targetLocale

  if (isFirstNavigation) {
    isFirstNavigation = false
    return true
  }

  if (to.fullPath === from.fullPath)
    return true

  if (!shouldFetchSsrDataForRoute(to)) {
    clearRouteSsrState()
    return true
  }

  clearRouteSsrState()

  const fetchId = ++latestSsrFetchId
  startSsrFetchLoading()
  void fetchSsrData(to.fullPath)
    .then((data) => {
      // Ignore outdated responses from older navigations.
      if (fetchId !== latestSsrFetchId)
        return

      replaceRouteSsrState(data)
    })
    .catch((error) => {
      if (fetchId !== latestSsrFetchId)
        return

      console.error('Failed to fetch SSR data', error)
    })
    .finally(() => {
      stopSsrFetchLoading()
    })

  return true
})

router.replace(fullPath)
router.isReady().then(() => {
  app.mount('#app')
  delete window.__SSR_DATA__

  const preferredPath = preferredLocalePathAfterHydration(window.location, savedLocale)
  if (preferredPath)
    void router.replace(preferredPath)

  // Avoid blocking first paint when no server-injected payload is present.
  if (!preferredPath && !hasInitialSsrPayload && shouldFetchSsrDataForRoute(router.currentRoute.value))
    void fetchInitialSsrData(router.currentRoute.value.fullPath)
})

async function fetchSsrData(path: string, timeoutMs = SSR_FETCH_TIMEOUT_MS): Promise<Record<string, unknown>> {
  const url = new URL(path, window.location.origin)
  const endpoint = `/_ssr/data${url.pathname}${url.search}`
  const controller = new AbortController()
  const timeoutId = window.setTimeout(() => controller.abort(), timeoutMs)

  let response: Response
  try {
    response = await fetch(endpoint, {
      credentials: 'same-origin',
      signal: controller.signal,
      headers: {
        'Accept': 'application/json',
      },
    })
  }
  catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError')
      throw new Error(`SSR data fetch timeout after ${timeoutMs}ms`)
    throw error
  }
  finally {
    window.clearTimeout(timeoutId)
  }

  if (!response.ok)
    throw new Error(`Request failed with status ${response.status}`)

  const data = await response.json()
  if (data && typeof data === 'object')
    return data as Record<string, unknown>

  return {}
}

async function fetchInitialSsrData(path: string): Promise<void> {
  startSsrFetchLoading()
  try {
    const initialData = await fetchSsrData(path)
    replaceRouteSsrState(initialData)
  }
  catch (error) {
    console.error('Failed to fetch initial SSR data', error)
  }
  finally {
    stopSsrFetchLoading()
  }
}

function clearRouteSsrState() {
  ssrContext.setState(extractPersistentSsrState(ssrContext.state.value))
}

function replaceRouteSsrState(data: Record<string, unknown>) {
  ssrContext.setState({
    ...extractPersistentSsrState(ssrContext.state.value),
    ...data,
  })
}

function extractPersistentSsrState(source: Record<string, unknown>): Record<string, unknown> {
  const persistent: Record<string, unknown> = {}
  for (const key of persistentSsrKeys) {
    if (Object.prototype.hasOwnProperty.call(source, key))
      persistent[key] = source[key]
  }
  return persistent
}

function shouldFetchSsrDataForRoute(route: { meta: { ssrData?: boolean } }): boolean {
  return route.meta.ssrData !== false
}

function setSsrFetchLoading(loading: boolean) {
  ssrContext.setState({
    ...ssrContext.state.value,
    __ssrFetchLoading: loading,
  })
}

function startSsrFetchLoading() {
  activeSsrFetchCount += 1
  if (activeSsrFetchCount === 1)
    setSsrFetchLoading(true)
}

function stopSsrFetchLoading() {
  if (activeSsrFetchCount > 0)
    activeSsrFetchCount -= 1

  if (activeSsrFetchCount === 0)
    setSsrFetchLoading(false)
}
