import { defineComponent, h } from 'vue'
import { describe, expect, it } from 'vitest'

import { defineGossrApp } from './definition'
import {
  SynchronousDisposableScope,
  UnsupportedAsyncLifecycleError,
} from './lifecycle'
import { createApplicationRuntime } from './runtime'
import type { GossrSetupContext } from './types'

describe('synchronous lifecycle', () => {
  it('runs registered disposers once in reverse order', () => {
    const calls: number[] = []
    const scope = new SynchronousDisposableScope()
    scope.onDispose(() => {
      calls.push(1)
    })
    scope.onDispose(() => {
      calls.push(2)
    })
    scope.finishSetup()

    const errors: unknown[] = []
    scope.disposeInto(errors)
    scope.disposeInto(errors)

    expect(calls).toEqual([2, 1])
    expect(errors).toEqual([])
  })

  it('consumes rejected thenable disposers while continuing cleanup', async () => {
    const calls: string[] = []
    const scope = new SynchronousDisposableScope()
    scope.onDispose((() => {
      calls.push('oldest')
    }))
    scope.onDispose((() => {
      calls.push('thenable')
      return Promise.reject(new Error('async disposer rejected'))
    }) as () => void)
    scope.onDispose(() => {
      calls.push('newest')
    })
    scope.finishSetup()

    const errors: unknown[] = []
    scope.disposeInto(errors)

    expect(calls).toEqual(['newest', 'thenable', 'oldest'])
    expect(errors).toHaveLength(1)
    expect(errors[0]).toBeInstanceOf(UnsupportedAsyncLifecycleError)
    await Promise.resolve()
  })

  it('cleans partial setup and consumes a rejected setup thenable', async () => {
    const calls: string[] = []
    const definition = defineGossrApp({
      appId: 'lifecycle-test',
      root: defineComponent(() => () => h('main')),
      routes: [{ path: '/', component: defineComponent(() => () => h('div')) }],
      document: {
        parse: value => value as { url: string },
        url: document => document.url,
      },
      setup: (({ onDispose }: GossrSetupContext<{ url: string }>) => {
        onDispose(() => {
          calls.push('disposed')
        })
        return Promise.reject(new Error('async setup rejected'))
      }) as unknown as (context: GossrSetupContext<{ url: string }>) => void,
    })

    expect(() => createApplicationRuntime(definition, {
      platform: 'server',
      initial: {
        document: { url: '/' },
        url: '/',
      },
    })).toThrow(UnsupportedAsyncLifecycleError)
    expect(calls).toEqual(['disposed'])
    await Promise.resolve()
  })

  it('cleans registrations made before setup throws', () => {
    const calls: number[] = []
    const definition = defineGossrApp({
      appId: 'partial-setup-test',
      root: defineComponent(() => () => h('main')),
      routes: [{ path: '/', component: defineComponent(() => () => h('div')) }],
      document: {
        parse: value => value as { url: string },
        url: document => document.url,
      },
      setup({ onDispose }) {
        onDispose(() => {
          calls.push(1)
        })
        onDispose(() => {
          calls.push(2)
        })
        throw new Error('setup failed')
      },
    })

    expect(() => createApplicationRuntime(definition, {
      platform: 'server',
      initial: {
        document: { url: '/' },
        url: '/',
      },
    })).toThrow('setup failed')
    expect(calls).toEqual([2, 1])
  })
})
