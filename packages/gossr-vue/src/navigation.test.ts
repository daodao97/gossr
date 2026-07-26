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

describe('committed-page cache', () => {
  function cachingCoordinator() {
    const fetches: string[] = []
    let owner = 'v1'
    const navigation = createNavigationCoordinator<TestDocument>({
      codec: {
        parse: value => value as TestDocument,
        url: document => document.url,
      },
      async fetcher(url) {
        fetches.push(url)
        return {
          kind: 'render',
          status: 200,
          snapshot: { url: url.replace('/_ssr/data', '') || '/', owner },
        }
      },
    })
    return {
      navigation,
      fetches,
      setOwner(next: string) {
        owner = next
      },
    }
  }

  it('serves a revisited page from cache and flags one revalidation', async () => {
    const { navigation, fetches, setOwner } = cachingCoordinator()

    expect(await navigation.prepare('/a', { force: true })).toEqual({ kind: 'ready' })
    expect(navigation.commit('/a')).toBe(true)
    expect(navigation.consumeRevalidation()).toBe(false)

    expect(await navigation.prepare('/b', { force: true })).toEqual({ kind: 'ready' })
    expect(navigation.commit('/b')).toBe(true)
    expect(fetches).toEqual(['/a', '/b'])

    // 回到 /a:不发请求、立即就绪,提交后要求一次重验证。
    setOwner('v2')
    expect(await navigation.prepare('/a', { force: true })).toEqual({ kind: 'ready' })
    expect(fetches).toEqual(['/a', '/b'])
    expect(navigation.commit('/a')).toBe(true)
    expect(navigation.current.value).toEqual({ url: '/a', owner: 'v1' })
    expect(navigation.consumeRevalidation()).toBe(true)
    expect(navigation.consumeRevalidation()).toBe(false)

    // 重验证走 refresh:拉取新鲜文档并无感替换。
    expect((await navigation.refresh('/a')).kind).toBe('ready')
    expect(navigation.current.value).toEqual({ url: '/a', owner: 'v2' })
    expect(fetches).toEqual(['/a', '/b', '/a'])

    navigation.dispose()
  })

  it('clearCached forces the next visit to fetch', async () => {
    const { navigation, fetches } = cachingCoordinator()

    await navigation.prepare('/a', { force: true })
    navigation.commit('/a')
    await navigation.prepare('/b', { force: true })
    navigation.commit('/b')

    navigation.clearCached()
    expect(await navigation.prepare('/a', { force: true })).toEqual({ kind: 'ready' })
    expect(fetches).toEqual(['/a', '/b', '/a'])
    navigation.commit('/a')
    expect(navigation.consumeRevalidation()).toBe(false)

    navigation.dispose()
  })

  it('the boot document commits without requesting a revalidation', async () => {
    const fetches: string[] = []
    const navigation = createNavigationCoordinator<TestDocument>({
      codec: {
        parse: value => value as TestDocument,
        url: document => document.url,
      },
      initial: { data: { url: '/', owner: 'boot' }, url: '/' },
      async fetcher(url) {
        fetches.push(url)
        return { kind: 'render', status: 200, snapshot: { url: '/', owner: 'fresh' } }
      },
    })

    expect(await navigation.prepare('/')).toEqual({ kind: 'ready' })
    expect(navigation.commit('/')).toBe(true)
    expect(navigation.consumeRevalidation()).toBe(false)
    expect(fetches).toEqual([])

    navigation.dispose()
  })
})
