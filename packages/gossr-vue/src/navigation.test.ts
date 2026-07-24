import { describe, expect, it } from 'vitest'

import { createNavigationCoordinator } from './navigation'

interface TestDocument {
  url: string
  owner: string
}

describe('generic navigation coordinator', () => {
  it('cancels stale requests and commits only the newest document', async () => {
    const requests = new Map<string, {
      signal: AbortSignal
      resolve: (value: unknown) => void
    }>()
    let parseCount = 0
    const navigation = createNavigationCoordinator<TestDocument>({
      codec: {
        parse(value) {
          parseCount += 1
          return value as TestDocument
        },
        url: document => document.url,
      },
      fetcher(url, signal) {
        return new Promise((resolve) => {
          requests.set(url, { signal, resolve })
        })
      },
    })

    const first = navigation.prepare('/first')
    const second = navigation.prepare('/second')
    expect(requests.get('/first')?.signal.aborted).toBe(true)

    requests.get('/second')?.resolve({
      kind: 'render',
      status: 200,
      snapshot: { url: '/second', owner: 'new' },
    })
    expect(await second).toEqual({ kind: 'ready' })
    expect(navigation.commit('/second')).toBe(true)

    requests.get('/first')?.resolve({
      kind: 'render',
      status: 200,
      snapshot: { url: '/first', owner: 'old' },
    })
    expect(await first).toEqual({ kind: 'cancelled' })
    expect(navigation.current.value).toEqual({
      url: '/second',
      owner: 'new',
    })
    expect(parseCount).toBe(1)

    navigation.dispose()
  })

  it('rejects a document whose codec URL differs from the request', async () => {
    const navigation = createNavigationCoordinator<TestDocument>({
      codec: {
        parse: value => value as TestDocument,
        url: document => document.url,
      },
      async fetcher() {
        return {
          kind: 'render',
          status: 200,
          snapshot: { url: '/other', owner: 'wrong' },
        }
      },
    })

    const result = await navigation.prepare('/expected')
    expect(result.kind).toBe('error')
    expect(navigation.current.value).toBeUndefined()
    navigation.dispose()
  })
})
