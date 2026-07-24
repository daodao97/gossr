import { defineComponent, h, inject } from 'vue'
import type { InjectionKey } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import { defineGossrApp } from './definition'
import { createSSRRenderer } from './server'

interface TestDocument {
  url: string
  owner: string
}

describe('generic SSR renderer', () => {
  it('isolates each request and returns only html/head', async () => {
    const documentKey: InjectionKey<TestDocument> = Symbol('test-document')
    const disposed: string[] = []
    const root = defineComponent({
      setup() {
        const pageDocument = inject(documentKey)
        return () => h('main', pageDocument?.owner)
      },
    })
    const definition = defineGossrApp<TestDocument>({
      appId: 'server-test',
      root,
      routes: [{
        path: '/:pathMatch(.*)*',
        component: defineComponent(() => () => h('div')),
      }],
      document: {
        parse: value => value as TestDocument,
        url: pageDocument => pageDocument.url,
      },
      setup({ app, navigation, onDispose }) {
        const pageDocument = navigation.current.value
        if (!pageDocument)
          throw new Error('missing request document')
        app.provide(documentKey, pageDocument)
        onDispose(() => {
          disposed.push(pageDocument.owner)
        })
      },
    })
    const render = createSSRRenderer(definition)

    const first = await render({
      url: '/first',
      snapshot: { url: '/first', owner: 'alice' },
    })
    const second = await render({
      url: '/second',
      snapshot: { url: '/second', owner: 'bob' },
    })
    const forceQuery = await render({
      url: '/force?',
      snapshot: { url: '/force?', owner: 'force-query' },
    })

    expect(Object.keys(first).sort()).toEqual(['head', 'html'])
    expect(first.html).toContain('alice')
    expect(first.html).not.toContain('bob')
    expect(second.html).toContain('bob')
    expect(second.html).not.toContain('alice')
    expect(forceQuery.html).toContain('force-query')
    expect(disposed).toEqual(['alice', 'bob', 'force-query'])
  })

  it.each([null, false, 0, ''])(
    'preserves a falsy render failure (%j)',
    async (failure) => {
      const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
      const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => {})
      const definition = defineGossrApp<TestDocument>({
        appId: 'falsy-error-test',
        root: defineComponent(() => () => {
          throw failure
        }),
        routes: [{
          path: '/',
          component: defineComponent(() => () => h('div')),
        }],
        document: {
          parse: value => value as TestDocument,
          url: pageDocument => pageDocument.url,
        },
      })

      await expect(createSSRRenderer(definition)({
        url: '/',
        snapshot: { url: '/', owner: 'nobody' },
      })).rejects.toBe(failure)
      consoleError.mockRestore()
      consoleWarn.mockRestore()
    },
  )
})
