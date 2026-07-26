// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'

import { createApplicationRuntime } from './runtime'
import { defineGossrApp } from './definition'
import { useNavigation } from './use-navigation'
import type { NavigationCoordinator } from './types'

describe('useNavigation', () => {
  it('components resolve the runtime-provided coordinator without host wiring', async () => {
    window.history.replaceState({}, '', '/')
    document.body.innerHTML = '<div id="app"></div>'

    let seen: NavigationCoordinator<{ url: string }> | undefined
    const definition = defineGossrApp<{ url: string }>({
      appId: 'use-navigation-test',
      root: defineComponent(() => {
        seen = useNavigation<{ url: string }>()
        return () => h('main')
      }),
      routes: [{ path: '/', component: defineComponent(() => () => h('div')) }],
      pageData: {
        parse: value => value as { url: string },
        url: pageData => pageData.url,
      },
    })
    const runtime = createApplicationRuntime(definition, {
      platform: 'client',
      initial: { data: { url: '/' }, url: '/' },
      hydrate: false,
    })
    await runtime.initialNavigation
    runtime.mount('#app')

    expect(seen).toBe(runtime.navigation)
    runtime.dispose()
  })

  it('throws outside a gossr runtime', () => {
    expect(() => useNavigation()).toThrowError('gossr application runtime')
  })
})
