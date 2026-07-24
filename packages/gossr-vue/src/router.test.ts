// @vitest-environment jsdom

import { defineComponent, h } from 'vue'
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
        document: { url: '/' },
        url: '/',
      },
      hydrate: false,
    })

    await expect(runtime.initialNavigation).resolves.toMatchObject({
      fullPath: '/',
    })
    expect(runtime.router.currentRoute.value.fullPath).toBe('/')
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
        document: { url: '/?tab=usage' },
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

  it('refreshes the previous page when a pending page switch is reversed', async () => {
    window.history.replaceState({}, '', '/dashboard/api-keys')
    const requestedURLs: string[] = []
    let billingSignal: AbortSignal | undefined
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async (input, init) => {
        const url = String(input)
        requestedURLs.push(url)

        if (url.endsWith('/dashboard/billing')) {
          billingSignal = init?.signal ?? undefined
          await new Promise<never>((_resolve, reject) => {
            billingSignal?.addEventListener('abort', () => {
              reject(new DOMException('Aborted', 'AbortError'))
            }, { once: true })
          })
        }

        return navigationResponse('/dashboard/api-keys', 'refreshed')
      },
    )
    const runtime = createApplicationRuntime(switchingDefinition(), {
      platform: 'client',
      initial: {
        document: {
          url: '/dashboard/api-keys',
          version: 'initial',
        },
        url: '/dashboard/api-keys',
      },
      hydrate: false,
    })

    await runtime.initialNavigation
    expect(fetchMock).not.toHaveBeenCalled()

    await runtime.router.push('/dashboard/billing')
    expect(requestedURLs).toEqual(['/_ssr/data/dashboard/billing'])

    await runtime.router.push('/dashboard/api-keys')
    await vi.waitFor(() => {
      expect(requestedURLs).toEqual([
        '/_ssr/data/dashboard/billing',
        '/_ssr/data/dashboard/api-keys',
      ])
      expect(runtime.navigation.current.value).toEqual({
        url: '/dashboard/api-keys',
        version: 'refreshed',
      })
    })
    expect(billingSignal?.aborted).toBe(true)

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
        document: {
          url: '/dashboard/api-keys',
          version: 'initial',
        },
        url: '/dashboard/api-keys',
      },
      hydrate: false,
    })

    await runtime.initialNavigation
    await runtime.router.push('/dashboard/billing')
    await runtime.router.push('/dashboard/billing#payment')

    expect(requestedURLs).toEqual(['/_ssr/data/dashboard/billing'])
    expect(billingSignal?.aborted).toBe(false)

    resolveBilling(navigationResponse('/dashboard/billing', 'loaded'))
    await vi.waitFor(() => {
      expect(runtime.navigation.current.value).toEqual({
        url: '/dashboard/billing',
        version: 'loaded',
      })
    })
    expect(runtime.router.currentRoute.value.fullPath)
      .toBe('/dashboard/billing#payment')

    runtime.dispose()
    fetchMock.mockRestore()
  })

  it('rejects instead of hanging when the automatic navigation is aborted', async () => {
    const runtime = createApplicationRuntime(testDefinition(({ router }) => {
      router.beforeEach(() => false)
    }), {
      platform: 'client',
      initial: {
        document: { url: '/' },
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
        document: { url: '/' },
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
    document: {
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
    document: {
      parse: value => value as { url: string },
      url: pageDocument => pageDocument.url,
    },
  })
}

function switchingDefinition() {
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
    ],
    document: {
      parse: value => value as { url: string, version: string },
      url: pageDocument => pageDocument.url,
    },
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
    document: {
      parse: value => value as { url: string },
      url: pageDocument => pageDocument.url,
    },
  })
}
