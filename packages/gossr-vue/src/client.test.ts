// @vitest-environment jsdom

import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  bootstrapClient,
  readBootPageData,
  shouldHydrateApp,
} from './client'
import { defineGossrApp } from './definition'

interface TestDocument {
  url: string
  value: string
}

describe('boot document and hydration decision', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/current?tab=one')
    document.body.innerHTML = '<div id="app"></div>'
  })

  it('parses boot ingress once and hydrates only matching SSR markup', () => {
    let parseCount = 0
    const definition = testDefinition((value) => {
      parseCount += 1
      return value as TestDocument
    })
    document.body.innerHTML = `
      <div id="app" data-ssr="true"><main>server</main></div>
      <script id="__GOSSR_BOOT__" type="application/json">
        {"url":"/current?tab=one","value":"server"}
      </script>
    `

    const initial = readBootPageData(definition)
    expect(parseCount).toBe(1)
    expect(initial?.data.value).toBe('server')
    expect(shouldHydrateApp(initial)).toBe(true)
  })

  it('selects cold mount when marker is absent or document URL is stale', () => {
    const definition = testDefinition(value => value as TestDocument)
    document.body.innerHTML = `
      <div id="app"><main>fallback</main></div>
      <script id="__GOSSR_BOOT__" type="application/json">
        {"url":"/current?tab=one","value":"fallback"}
      </script>
    `
    expect(shouldHydrateApp(readBootPageData(definition))).toBe(false)

    document.querySelector('#app')?.setAttribute('data-ssr', 'true')
    document.querySelector('#__GOSSR_BOOT__')!.textContent
      = '{"url":"/stale","value":"stale"}'
    expect(shouldHydrateApp(readBootPageData(definition))).toBe(false)
  })

  it('keeps the existing DOM node after successful hydration', async () => {
    const definition = domDefinition('server')
    document.body.innerHTML = `
      <div id="app" data-ssr="true"><main>server</main></div>
      <script id="__GOSSR_BOOT__" type="application/json">
        {"url":"/current?tab=one","value":"server"}
      </script>
    `
    const serverNode = document.querySelector('#app > main')

    const runtime = await bootstrapClient(definition)
    expect(document.querySelector('#app > main')).toBe(serverNode)
    expect(document.querySelector('#app')?.textContent).toBe('server')
    runtime.dispose()
  })

  it('hydrates a direct-load URL whose browser route includes a fragment', async () => {
    window.history.replaceState({}, '', '/current?tab=one#models')
    const definition = domDefinition('server')
    document.body.innerHTML = `
      <div id="app" data-ssr="true"><main>server</main></div>
      <script id="__GOSSR_BOOT__" type="application/json">
        {"url":"/current?tab=one","value":"server"}
      </script>
    `
    const serverNode = document.querySelector('#app > main')

    const runtime = await bootstrapClient(definition)
    expect(runtime.router.currentRoute.value.fullPath)
      .toBe('/current?tab=one#models')
    expect(document.querySelector('#app > main')).toBe(serverNode)
    expect(runtime.navigation.current.value?.url).toBe('/current?tab=one')
    expect(runtime.navigation.error.value).toBeNull()
    runtime.dispose()
  })

  it('replaces stale fallback DOM during a cold mount', async () => {
    const definition = domDefinition('client')
    document.body.innerHTML = `
      <div id="app"><main>stale server fallback</main></div>
      <script id="__GOSSR_BOOT__" type="application/json">
        {"url":"/current?tab=one","value":"client"}
      </script>
    `
    const staleNode = document.querySelector('#app > main')

    const runtime = await bootstrapClient(definition)
    expect(document.querySelector('#app > main')).not.toBe(staleNode)
    expect(document.querySelector('#app')?.textContent).toBe('client')
    runtime.dispose()
  })

  it('loads a complete document before mounting without boot data', async () => {
    const definition = domDefinition('client')
    document.body.innerHTML = '<div id="app"><main>static fallback</main></div>'
    const staticNode = document.querySelector('#app > main')
    let resolveResponse!: (response: Response) => void
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async () => await new Promise<Response>((resolve) => {
        resolveResponse = resolve
      }),
    )

    const bootstrap = bootstrapClient(definition)
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledOnce()
    })
    expect(document.querySelector('#app > main')).toBe(staticNode)

    resolveResponse({
      status: 200,
      async json() {
        return {
          kind: 'render',
          status: 200,
          snapshot: {
            url: '/current?tab=one',
            value: 'client',
          },
        }
      },
    } as Response)

    const runtime = await bootstrap
    expect(document.querySelector('#app > main')).not.toBe(staticNode)
    expect(document.querySelector('#app')?.textContent).toBe('client')
    runtime.dispose()
    fetchMock.mockRestore()
  })

  it('removes the boot element even when bootstrap fails', async () => {
    const definition = testDefinition(value => value as TestDocument)
    document.body.innerHTML = `
      <script id="__GOSSR_BOOT__" type="application/json">
        {"url":"/current?tab=one","value":"server"}
      </script>
    `

    await expect(bootstrapClient(definition)).rejects.toThrow('#app is missing')
    expect(document.querySelector('#__GOSSR_BOOT__')).toBeNull()
  })
})

function testDefinition(parse: (value: unknown) => TestDocument) {
  return defineGossrApp<TestDocument>({
    appId: 'boot-test',
    root: defineComponent(() => () => h('main')),
    routes: [{ path: '/:pathMatch(.*)*', component: defineComponent(() => () => h('div')) }],
    pageData: {
      parse,
      url: pageDocument => pageDocument.url,
    },
  })
}

function domDefinition(text: string) {
  return defineGossrApp<TestDocument>({
    appId: `dom-test-${text}`,
    root: defineComponent(() => () => h('main', text)),
    routes: [{
      path: '/:pathMatch(.*)*',
      component: defineComponent(() => () => h('div')),
    }],
    pageData: {
      parse: value => value as TestDocument,
      url: pageDocument => pageDocument.url,
    },
  })
}
