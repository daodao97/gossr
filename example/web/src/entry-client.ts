import type { SsrState } from '~/composables/useSsrData'

import { watch } from 'vue'
import { makeApp } from '~/main'
import { availableLocales, defaultLocale, getLocaleRef, isSupportedLocale } from '~/modules/i18n'

declare global {
  interface Window {
    __SSR_DATA__?: SsrState
  }
}

const ssrPayload = window.__SSR_DATA__
const hasInitialSsrPayload = !!ssrPayload && Object.keys(ssrPayload).length > 0
const initialState = ssrPayload ?? {}
const { app, router, ssrContext, i18n } = makeApp(initialState)
const SSR_FETCH_TIMEOUT_MS = 5000
const persistentSsrKeys = new Set(['session', 'locale', 'siteOrigin'])

if (typeof window !== 'undefined') {
  const savedLocale = window.localStorage.getItem('locale')
  const localeRef = getLocaleRef(i18n)

  if (savedLocale && isSupportedLocale(savedLocale))
    localeRef.value = savedLocale

  document.documentElement.setAttribute('lang', localeRef.value)

  watch(localeRef, (newLocale) => {
    if (availableLocales.includes(newLocale)) {
      window.localStorage.setItem('locale', newLocale)
      document.documentElement.setAttribute('lang', newLocale)
    }
  })

  installClientNavigation()
}
// Respect saved locale in URL if there's no locale segment
let fullPath = window.location.pathname + window.location.search
;(function ensureLocaleInPath() {
  const pathname = window.location.pathname
  const hasLocaleSegment = availableLocales.some((code) => {
    if (!pathname.startsWith(`/${code}`))
      return false
    const next = pathname.charAt(code.length + 1)
    return pathname === `/${code}` || next === '/'
  })

  const localeRef = getLocaleRef(i18n)
  const preferred = localeRef.value

  if (!hasLocaleSegment && isSupportedLocale(preferred) && availableLocales.includes(preferred)) {
    // Only prefix when preferred locale is not the default
    // to keep default locale at root path for SEO/UX.
    if (preferred && preferred !== defaultLocale) {
      fullPath = `/${preferred}${fullPath}`
    }
  }
})()

let isFirstNavigation = true
let latestSsrFetchId = 0

router.beforeResolve((to, from, next) => {
  if (isFirstNavigation) {
    isFirstNavigation = false
    return next()
  }

  if (to.fullPath === from.fullPath)
    return next()

  // Skip SSR data fetching if route meta explicitly disables it
  if (to.meta.ssrData === false) {
    clearRouteSsrState()
    return next()
  }

  clearRouteSsrState()
  next()

  const fetchId = ++latestSsrFetchId
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
})

router.replace(fullPath)
router.isReady().then(() => {
  app.mount('#app', shouldHydrateApp())
  delete window.__SSR_DATA__

  // Avoid blocking first paint when no server-injected payload is present.
  if (!hasInitialSsrPayload && router.currentRoute.value.meta.ssrData !== false)
    void fetchInitialSsrData(router.currentRoute.value.fullPath)
})

async function fetchSsrData(path: string, timeoutMs = SSR_FETCH_TIMEOUT_MS): Promise<Record<string, unknown>> {
  const url = new URL(path, window.location.origin)
  const endpoint = `/__ssr_fetch${url.pathname}${url.search}`
  const controller = new AbortController()
  const timeoutId = window.setTimeout(() => controller.abort(), timeoutMs)

  let response: Response
  try {
    response = await fetch(endpoint, {
      credentials: 'same-origin',
      signal: controller.signal,
      headers: {
        'Accept': 'application/json',
        'X-SSR-Fetch': '1',
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
  try {
    const initialData = await fetchSsrData(path)
    replaceRouteSsrState(initialData)
  }
  catch (error) {
    console.error('Failed to fetch initial SSR data', error)
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

function shouldHydrateApp(): boolean {
  if (document.querySelector('meta[name="ssr-error-id"]'))
    return false

  const appRoot = document.querySelector('#app')
  if (!(appRoot instanceof HTMLElement))
    return false

  return appRoot.innerHTML.trim().length > 0
}

function installClientNavigation() {
  document.addEventListener('click', (event) => {
    if (event.defaultPrevented || event.button !== 0)
      return

    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey)
      return

    const target = event.target
    if (!(target instanceof Element))
      return

    const anchor = target.closest('a[href]')
    if (!(anchor instanceof HTMLAnchorElement))
      return

    if (anchor.target && anchor.target !== '_self')
      return

    if (anchor.hasAttribute('download'))
      return

    const rel = anchor.getAttribute('rel') ?? ''
    if (rel.split(/\s+/).includes('external'))
      return

    const url = new URL(anchor.href, window.location.href)
    if (url.protocol !== 'http:' && url.protocol !== 'https:')
      return

    if (url.origin !== window.location.origin)
      return

    const nextPath = `${url.pathname}${url.search}${url.hash}`
    if (nextPath === router.currentRoute.value.fullPath)
      return

    event.preventDefault()
    void router.push(nextPath)
  })
}
