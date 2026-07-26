// @vitest-environment jsdom

import { defineComponent, h, watch } from 'vue'
import {
  NavigationFailureType,
  RouterLink,
  isNavigationFailure,
} from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { defineGossrApp } from './definition'
import { createApplicationRuntime } from './runtime'

describe('Vue Router 5 client initialization', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/')
    document.body.innerHTML = '<div id="app"></div>'
  })

  it('tracks the single automatic initial navigation', async () => {
    const runtime = createApplicationRuntime(testDefinition(), {
      platform: 'client',
      initial: {
        data: { url: '/' },
        url: '/',
      },
      hydrate: false,
    })

    await expect(runtime.initialNavigation).resolves.toMatchObject({
      fullPath: '/',
    })
    expect(runtime.router.currentRoute.value.fullPath).toBe('/')
    expect(runtime.navigation).not.toHaveProperty('prepare')
    expect(runtime.navigation).not.toHaveProperty('commit')
    expect(runtime.navigation).not.toHaveProperty('stagedURL')
    runtime.dispose()
  })

  it('keeps the document ready when a RouterLink click changes only the hash', async () => {
    window.history.replaceState({}, '', '/?tab=usage')
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockRejectedValue(
      new Error('hash-only navigation must not fetch a page document'),
    )
    const runtime = createApplicationRuntime(hashLinkDefinition(), {
      platform: 'client',
      initial: {
        data: { url: '/?tab=usage' },
        url: '/?tab=usage',
      },
      hydrate: false,
    })

    await runtime.initialNavigation
    runtime.mount('#app')
    document.querySelector('a')?.dispatchEvent(new MouseEvent('click', {
      bubbles: true,
      button: 0,
      cancelable: true,
    }))
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(runtime.router.currentRoute.value.fullPath)
      .toBe('/?tab=usage#models')
    expect(runtime.navigation.current.value).toEqual({ url: '/?tab=usage' })
    expect(runtime.navigation.error.value).toBeNull()
    expect(fetchMock).not.toHaveBeenCalled()
    expect(consoleError).not.toHaveBeenCalled()

    runtime.dispose()
    fetchMock.mockRestore()
    consoleError.mockRestore()
  })

  it('keeps showing the current page while a switch loads, and reversal aborts it', async () => {
    window.history.replaceState({}, '', '/dashboard/api-keys')
    const requestedURLs: string[] = []
    let billingSignal: AbortSignal | undefined
    let resolveApiKeys: ((response: Response) => void) | undefined
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async (input, init) => {
        const url = String(input)
        requestedURLs.push(url)

        if (url.endsWith('/dashboard/billing')) {
          billingSignal = init?.signal ?? undefined
          return await new Promise<never>((_resolve, reject) => {
            billingSignal?.addEventListener('abort', () => {
              reject(new DOMException('Aborted', 'AbortError'))
            }, { once: true })
          })
        }

        return await new Promise<Response>((resolve) => {
          resolveApiKeys = resolve
        })
      },
    )
    const runtime = createApplicationRuntime(switchingDefinition(), {
      platform: 'client',
      initial: {
        data: {
          url: '/dashboard/api-keys',
          version: 'initial',
        },
        url: '/dashboard/api-keys',
      },
      hydrate: false,
    })

    await runtime.initialNavigation
    expect(fetchMock).not.toHaveBeenCalled()
    expect(runtime.navigation.ready.value).toBe(true)

    // Non-blocking: the route commits immediately while data loads; the
    // previous document stays current (stale-while-loading).
    await runtime.router.push('/dashboard/billing')
    expect(runtime.router.currentRoute.value.fullPath)
      .toBe('/dashboard/billing')
    expect(runtime.navigation.current.value).toEqual({
      url: '/dashboard/api-keys',
      version: 'initial',
    })
    expect(runtime.navigation.ready.value).toBe(false)
    await vi.waitFor(() => {
      expect(requestedURLs).toEqual(['/_ssr/data/dashboard/billing'])
    })

    // Reversing aborts the pending billing load. The api-keys document is
    // still current, so the page is ready instantly while it revalidates.
    await runtime.router.push('/dashboard/api-keys')
    expect(billingSignal?.aborted).toBe(true)
    expect(runtime.router.currentRoute.value.fullPath)
      .toBe('/dashboard/api-keys')
    expect(runtime.navigation.current.value).toEqual({
      url: '/dashboard/api-keys',
      version: 'initial',
    })
    expect(runtime.navigation.ready.value).toBe(true)

    await vi.waitFor(() => {
      expect(resolveApiKeys).toBeDefined()
    })
    resolveApiKeys!(navigationResponse('/dashboard/api-keys', 'revalidated'))
    await vi.waitFor(() => {
      expect(runtime.navigation.current.value).toEqual({
        url: '/dashboard/api-keys',
        version: 'revalidated',
      })
    })

    runtime.dispose()
    fetchMock.mockRestore()
  })

  it('does not let a cancelled route abort the newer route load', async () => {
    window.history.replaceState({}, '', '/dashboard/api-keys')
    const requestedURLs: string[] = []
    let billingSignal: AbortSignal | undefined
    let usageSignal: AbortSignal | undefined
    let resolveUsage!: (response: Response) => void
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async (input, init) => {
        const url = String(input)
        requestedURLs.push(url)
        if (url.endsWith('/dashboard/billing')) {
          billingSignal = init?.signal ?? undefined
          return await new Promise<Response>((_resolve, reject) => {
            billingSignal?.addEventListener('abort', () => {
              reject(new DOMException('Aborted', 'AbortError'))
            }, { once: true })
          })
        }

        usageSignal = init?.signal ?? undefined
        return await new Promise<Response>((resolve) => {
          resolveUsage = resolve
        })
      },
    )
    const runtime = createApplicationRuntime(switchingDefinition(), {
      platform: 'client',
      initial: {
        data: {
          url: '/dashboard/api-keys',
          version: 'initial',
        },
        url: '/dashboard/api-keys',
      },
      hydrate: false,
    })

    await runtime.initialNavigation
    await runtime.router.push('/dashboard/billing')
    await vi.waitFor(() => {
      expect(requestedURLs).toEqual(['/_ssr/data/dashboard/billing'])
    })
    await runtime.router.push('/dashboard/usage_log')
    await vi.waitFor(() => {
      expect(requestedURLs).toEqual([
        '/_ssr/data/dashboard/billing',
        '/_ssr/data/dashboard/usage_log',
      ])
    })

    expect(billingSignal?.aborted).toBe(true)
    expect(usageSignal?.aborted).toBe(false)
    expect(runtime.router.currentRoute.value.fullPath)
      .toBe('/dashboard/usage_log')
    expect(runtime.navigation.ready.value).toBe(false)

    resolveUsage(navigationResponse('/dashboard/usage_log', 'loaded'))
    await vi.waitFor(() => {
      expect(runtime.navigation.current.value).toEqual({
        url: '/dashboard/usage_log',
        version: 'loaded',
      })
    })
    expect(runtime.navigation.ready.value).toBe(true)

    runtime.dispose()
    fetchMock.mockRestore()
  })

  it('keeps a pending document request across a hash-only navigation', async () => {
    window.history.replaceState({}, '', '/dashboard/api-keys')
    const requestedURLs: string[] = []
    let billingSignal: AbortSignal | undefined
    let resolveBilling!: (response: Response) => void
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async (input, init) => {
        const url = String(input)
        requestedURLs.push(url)
        billingSignal = init?.signal ?? undefined
        return await new Promise<Response>((resolve) => {
          resolveBilling = resolve
        })
      },
    )
    const runtime = createApplicationRuntime(switchingDefinition(), {
      platform: 'client',
      initial: {
        data: {
          url: '/dashboard/api-keys',
          version: 'initial',
        },
        url: '/dashboard/api-keys',
      },
      hydrate: false,
    })

    await runtime.initialNavigation
    const billingNavigation = runtime.router.push('/dashboard/billing')
    await vi.waitFor(() => {
      expect(requestedURLs).toEqual(['/_ssr/data/dashboard/billing'])
    })
    const hashNavigation = runtime.router.push('/dashboard/billing#payment')

    expect(requestedURLs).toEqual(['/_ssr/data/dashboard/billing'])
    expect(billingSignal?.aborted).toBe(false)

    resolveBilling(navigationResponse('/dashboard/billing', 'loaded'))
    await Promise.all([billingNavigation, hashNavigation])

    expect(runtime.navigation.current.value).toEqual({
      url: '/dashboard/billing',
      version: 'loaded',
    })
    expect(runtime.router.currentRoute.value.fullPath)
      .toBe('/dashboard/billing#payment')

    runtime.dispose()
    fetchMock.mockRestore()
  })

  it('publishes the target route immediately and the document when ready', async () => {
    window.history.replaceState({}, '', '/dashboard/api-keys')
    let billingSignal: AbortSignal | undefined
    let resolveBilling!: (response: Response) => void
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async (_input, init) => {
        billingSignal = init?.signal ?? undefined
        return await new Promise<Response>((resolve) => {
          resolveBilling = resolve
        })
      },
    )
    const observations: Array<{
      route: string
      document?: string
      ready: boolean
    }> = []
    const runtime = createApplicationRuntime(switchingDefinition(({
      router,
      navigation,
    }) => {
      watch(
        () => router.currentRoute.value.fullPath,
        route => observations.push({
          route,
          document: navigation.current.value?.url,
          ready: navigation.ready.value,
        }),
        { flush: 'sync' },
      )
    }), {
      platform: 'client',
      initial: {
        data: {
          url: '/dashboard/api-keys',
          version: 'initial',
        },
        url: '/dashboard/api-keys',
      },
      hydrate: false,
    })

    await runtime.initialNavigation
    observations.length = 0

    // The click responds instantly: route committed, previous document kept,
    // ready flipped false for page-level loading states.
    await runtime.router.push('/dashboard/billing')
    expect(observations).toEqual([{
      route: '/dashboard/billing',
      document: '/dashboard/api-keys',
      ready: false,
    }])
    expect(runtime.navigation.loading.value).toBe(true)
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledOnce()
    })

    // refresh() is rejected while a route load is pending.
    await expect(runtime.navigation.refresh()).resolves.toBe(false)
    expect(billingSignal?.aborted).toBe(false)

    resolveBilling(navigationResponse('/dashboard/billing', 'loaded'))
    await vi.waitFor(() => {
      expect(runtime.navigation.current.value).toEqual({
        url: '/dashboard/billing',
        version: 'loaded',
      })
    })
    expect(runtime.navigation.ready.value).toBe(true)
    expect(runtime.navigation.loading.value).toBe(false)

    runtime.dispose()
    fetchMock.mockRestore()
  })

  it('falls back to a browser navigation when target data is invalid', async () => {
    window.history.replaceState({}, '', '/dashboard/api-keys')
    const navigateDocument = vi.fn()
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      navigationResponse('/dashboard/wrong', 'invalid'),
    )
    const runtime = createApplicationRuntime(switchingDefinition(), {
      platform: 'client',
      initial: {
        data: {
          url: '/dashboard/api-keys',
          version: 'initial',
        },
        url: '/dashboard/api-keys',
      },
      hydrate: false,
      navigateDocument,
    })

    await runtime.initialNavigation
    await runtime.router.push('/dashboard/billing')

    // The route commits optimistically; when its data turns out invalid the
    // framework hands the target to a full browser navigation.
    await vi.waitFor(() => {
      expect(navigateDocument).toHaveBeenCalledOnce()
    })
    expect(navigateDocument).toHaveBeenCalledWith('/dashboard/billing')
    expect(runtime.navigation.current.value).toEqual({
      url: '/dashboard/api-keys',
      version: 'initial',
    })

    runtime.dispose()
    fetchMock.mockRestore()
  })

  it('never sends an unsafe router target to the browser fallback', async () => {
    window.history.replaceState({}, '', '/dashboard/api-keys')
    const navigateDocument = vi.fn()
    const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const runtime = createApplicationRuntime(switchingDefinition(), {
      platform: 'client',
      initial: {
        data: {
          url: '/dashboard/api-keys',
          version: 'initial',
        },
        url: '/dashboard/api-keys',
      },
      hydrate: false,
      navigateDocument,
    })

    await runtime.initialNavigation
    await runtime.router.push('//evil.example/path')

    expect(navigateDocument).not.toHaveBeenCalled()
    expect(runtime.router.currentRoute.value.fullPath)
      .toBe('/dashboard/api-keys')

    await runtime.router.push('/missing')
    expect(navigateDocument).toHaveBeenCalledOnce()
    expect(navigateDocument).toHaveBeenCalledWith('/missing')

    runtime.dispose()
    consoleWarn.mockRestore()
  })

  it('loads only the final target of an application redirect', async () => {
    window.history.replaceState({}, '', '/dashboard/api-keys')
    const requestedURLs: string[] = []
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async (input) => {
        const url = String(input)
        requestedURLs.push(url)
        return navigationResponse('/dashboard/usage_log', 'loaded')
      },
    )
    const runtime = createApplicationRuntime(switchingDefinition(({
      router,
    }) => {
      router.beforeResolve((to) => {
        if (to.path === '/dashboard/billing')
          return '/dashboard/usage_log'
        return true
      })
    }), {
      platform: 'client',
      initial: {
        data: {
          url: '/dashboard/api-keys',
          version: 'initial',
        },
        url: '/dashboard/api-keys',
      },
      hydrate: false,
    })

    await runtime.initialNavigation
    await runtime.router.push('/dashboard/billing')

    expect(requestedURLs).toEqual(['/_ssr/data/dashboard/usage_log'])
    expect(runtime.router.currentRoute.value.fullPath)
      .toBe('/dashboard/usage_log')
    expect(runtime.navigation.current.value?.url)
      .toBe('/dashboard/usage_log')

    runtime.dispose()
    fetchMock.mockRestore()
  })

  it('restarts data loading for a server-directed client redirect', async () => {
    window.history.replaceState({}, '', '/dashboard/api-keys')
    const requestedURLs: string[] = []
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async (input) => {
        const url = String(input)
        requestedURLs.push(url)
        if (url.endsWith('/dashboard/billing')) {
          return {
            status: 200,
            async json() {
              return {
                kind: 'redirect',
                status: 302,
                location: '/dashboard/usage_log',
              }
            },
          } as Response
        }
        return navigationResponse('/dashboard/usage_log', 'loaded')
      },
    )
    const runtime = createApplicationRuntime(switchingDefinition(), {
      platform: 'client',
      initial: {
        data: {
          url: '/dashboard/api-keys',
          version: 'initial',
        },
        url: '/dashboard/api-keys',
      },
      hydrate: false,
    })

    await runtime.initialNavigation
    await runtime.router.push('/dashboard/billing')

    await vi.waitFor(() => {
      expect(runtime.router.currentRoute.value.fullPath)
        .toBe('/dashboard/usage_log')
    })
    await vi.waitFor(() => {
      expect(runtime.navigation.current.value?.url)
        .toBe('/dashboard/usage_log')
    })
    expect(requestedURLs).toEqual([
      '/_ssr/data/dashboard/billing',
      '/_ssr/data/dashboard/usage_log',
    ])

    runtime.dispose()
    fetchMock.mockRestore()
  })

  it('rejects instead of hanging when the automatic navigation is aborted', async () => {
    const runtime = createApplicationRuntime(testDefinition(({ router }) => {
      router.beforeEach(() => false)
    }), {
      platform: 'client',
      initial: {
        data: { url: '/' },
        url: '/',
      },
      hydrate: false,
    })

    await expect(runtime.initialNavigation).rejects.toSatisfy(error => (
      isNavigationFailure(error, NavigationFailureType.aborted)
    ))
    runtime.dispose()
  })

  it('rejects through router.onError when an initial guard throws', async () => {
    const expected = new Error('guard exploded')
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    const runtime = createApplicationRuntime(testDefinition(({ router }) => {
      router.beforeEach(() => {
        throw expected
      })
    }), {
      platform: 'client',
      initial: {
        data: { url: '/' },
        url: '/',
      },
      hydrate: false,
    })

    await expect(runtime.initialNavigation).rejects.toBe(expected)
    runtime.dispose()
    expect(consoleError).toHaveBeenCalled()
  })
})

describe('framework route matching semantics', () => {
  it.each([
    ['/dashboard', 'dashboard'],
    ['/dashboard/', 'dashboard'],
    ['/dashboard/billing', 'dashboard-billing'],
  ])('matches the exact route %s as %s', (path, expectedName) => {
    const runtime = createApplicationRuntime(routeMatchingDefinition(), {
      platform: 'server',
    })

    expect(runtime.router.resolve(path).name).toBe(expectedName)
    runtime.dispose()
  })

  it.each([
    '/Dashboard',
    '/dashboard%2Fbilling',
    '/dash%62oard',
    '/dashboard/Billing',
  ])('sends the non-exact route %s to the catch-all', (path) => {
    const runtime = createApplicationRuntime(routeMatchingDefinition(), {
      platform: 'server',
    })

    expect(runtime.router.resolve(path).name).toBe('not-found')
    runtime.dispose()
  })
})

function testDefinition(
  setup?: Parameters<typeof defineGossrApp<{ url: string }>>[0]['setup'],
) {
  return defineGossrApp({
    appId: 'router-test',
    root: defineComponent(() => () => h('main')),
    routes: [{
      path: '/',
      component: defineComponent(() => () => h('div')),
    }],
    pageData: {
      parse: value => value as { url: string },
      url: pageDocument => pageDocument.url,
    },
    setup,
  })
}

function hashLinkDefinition() {
  return defineGossrApp({
    appId: 'router-hash-test',
    root: defineComponent(() => () => h(
      RouterLink,
      {
        to: '/?tab=usage#models',
      },
      {
        default: () => 'models',
      },
    )),
    routes: [{
      path: '/',
      component: defineComponent(() => () => h('div')),
    }],
    pageData: {
      parse: value => value as { url: string },
      url: pageDocument => pageDocument.url,
    },
  })
}

function switchingDefinition(
  setup?: Parameters<
    typeof defineGossrApp<{ url: string, version: string }>
  >[0]['setup'],
) {
  const routeComponent = defineComponent(() => () => h('div'))

  return defineGossrApp<{ url: string, version: string }>({
    appId: 'router-switching-test',
    root: defineComponent(() => () => h('main')),
    routes: [
      {
        path: '/dashboard/api-keys',
        component: routeComponent,
      },
      {
        path: '/dashboard/billing',
        component: routeComponent,
      },
      {
        path: '/dashboard/usage_log',
        component: routeComponent,
      },
    ],
    pageData: {
      parse: value => value as { url: string, version: string },
      url: pageDocument => pageDocument.url,
    },
    setup,
  })
}

function navigationResponse(url: string, version: string): Response {
  return {
    status: 200,
    async json() {
      return {
        kind: 'render',
        status: 200,
        snapshot: { url, version },
      }
    },
  } as Response
}

function routeMatchingDefinition() {
  const routeComponent = defineComponent(() => () => h('div'))

  return defineGossrApp({
    appId: 'route-matching-test',
    root: defineComponent(() => () => h('main')),
    routes: [
      {
        path: '/dashboard',
        name: 'dashboard',
        component: routeComponent,
        // The framework invariant must override application/file-route input.
        sensitive: false,
        children: [{
          path: 'billing',
          name: 'dashboard-billing',
          component: routeComponent,
          sensitive: false,
        }],
      },
      {
        path: '/:pathMatch(.*)*',
        name: 'not-found',
        component: routeComponent,
      },
    ],
    pageData: {
      parse: value => value as { url: string },
      url: pageDocument => pageDocument.url,
    },
  })
}

describe('bfcache restore revalidation', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/')
    document.body.innerHTML = '<div id="app"></div>'
  })

  it('refreshes the current document when a persisted page is restored', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      Response.json({
        kind: 'render',
        status: 200,
        snapshot: { url: '/' },
      }),
    )
    const runtime = createApplicationRuntime(testDefinition(), {
      platform: 'client',
      initial: {
        data: { url: '/' },
        url: '/',
      },
      hydrate: false,
    })
    await runtime.initialNavigation

    expect(fetchMock).not.toHaveBeenCalled()
    const restored = new Event('pageshow') as PageTransitionEvent
    Object.defineProperty(restored, 'persisted', { value: true })
    window.dispatchEvent(restored)
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        '/_ssr/data/',
        expect.objectContaining({ credentials: 'same-origin' }),
      )
    })

    runtime.dispose()

    const afterDispose = fetchMock.mock.calls.length
    window.dispatchEvent(restored)
    expect(fetchMock.mock.calls.length).toBe(afterDispose)
    fetchMock.mockRestore()
  })

  it('ignores non-persisted pageshow events', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockRejectedValue(
      new Error('non-persisted pageshow must not fetch'),
    )
    const runtime = createApplicationRuntime(testDefinition(), {
      platform: 'client',
      initial: {
        data: { url: '/' },
        url: '/',
      },
      hydrate: false,
    })
    await runtime.initialNavigation

    window.dispatchEvent(new Event('pageshow'))
    await Promise.resolve()
    expect(fetchMock).not.toHaveBeenCalled()

    runtime.dispose()
    fetchMock.mockRestore()
  })
})

describe('structured server errors stay in-app', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/')
    document.body.innerHTML = '<div id="app"></div>'
  })

  it('keeps the current page when navigation data returns a coded error', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      Response.json({
        kind: 'error',
        status: 504,
        code: 'request_timeout',
        message: 'page request timed out',
      }),
    )
    const navigateDocument = vi.fn()
    const routeComponent = defineComponent(() => () => h('div'))
    const definition = defineGossrApp<{ url: string }>({
      appId: 'router-coded-error-test',
      root: defineComponent(() => () => h('main')),
      routes: [
        { path: '/', component: routeComponent },
        { path: '/dashboard/usage_log', component: routeComponent },
      ],
      pageData: {
        parse: value => value as { url: string },
        url: pageDocument => pageDocument.url,
      },
    })
    const runtime = createApplicationRuntime(definition, {
      platform: 'client',
      initial: {
        data: { url: '/' },
        url: '/',
      },
      hydrate: false,
      navigateDocument,
    })
    await runtime.initialNavigation

    await runtime.router.push('/dashboard/usage_log')
    await vi.waitFor(() => {
      expect(runtime.navigation.error.value).not.toBeNull()
    })

    // 不触发整页导航,上一页数据保持已提交状态。
    expect(navigateDocument).not.toHaveBeenCalled()
    expect(runtime.navigation.current.value).toEqual({ url: '/' })
    expect(runtime.router.currentRoute.value.fullPath).toBe('/dashboard/usage_log')

    runtime.dispose()
    fetchMock.mockRestore()
  })

  it('a coded refresh error keeps the page without a browser navigation', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      Response.json({
        kind: 'error',
        status: 503,
        code: 'service_unavailable',
        message: 'temporarily unavailable',
      }),
    )
    const navigateDocument = vi.fn()
    const runtime = createApplicationRuntime(testDefinition(), {
      platform: 'client',
      initial: {
        data: { url: '/' },
        url: '/',
      },
      hydrate: false,
      navigateDocument,
    })
    await runtime.initialNavigation

    expect(await runtime.navigation.refresh()).toBe(false)
    expect(navigateDocument).not.toHaveBeenCalled()
    expect(runtime.navigation.current.value).toEqual({ url: '/' })
    expect(runtime.navigation.error.value).not.toBeNull()

    runtime.dispose()
    fetchMock.mockRestore()
  })
})
