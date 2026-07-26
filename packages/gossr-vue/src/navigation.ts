import { shallowRef } from 'vue'
import type { Ref } from 'vue'

import { parsePageData } from './page-data.js'
import { canonicalNavigationURL, navigationURLsMatch } from './url.js'
import type {
  PageDataCodec,
  NavigationOutcome,
} from './types.js'
import type { ParsedPageData } from './page-data.js'

export type NavigationPreparation =
  | { kind: 'ready' }
  | { kind: 'redirect', status: number, location: string }
  | { kind: 'cancelled' }
  | { kind: 'error', status?: number, code?: string, error: Error }

export type NavigationFetcher = (
  url: string,
  signal: AbortSignal,
) => Promise<unknown>

interface StagedDocument<PageData> extends ParsedPageData<PageData> {
  id: number
  targetURL: string
  fromCache?: boolean
}

interface ActiveNavigation {
  id: number
  kind: 'route' | 'refresh'
  url: string
  controller: AbortController
  promise: Promise<NavigationPreparation>
}

interface NavigationOptions<PageData> {
  codec: PageDataCodec<PageData>
  initial?: ParsedPageData<PageData>
  fetcher?: NavigationFetcher
}

export interface ManagedNavigationCoordinator<PageData> {
  current: Readonly<Ref<PageData | undefined>>
  currentURL: Readonly<Ref<string | undefined>>
  loading: Readonly<Ref<boolean>>
  error: Readonly<Ref<Error | null>>
  prepare: (url: string, options?: { force?: boolean }) => Promise<NavigationPreparation>
  commit: (url: string) => boolean
  refresh: (url: string) => Promise<NavigationPreparation>
  /** True exactly once after a commit served from the page cache. */
  consumeRevalidation: () => boolean
  clearCached: () => void
  cancelRoute: () => void
  dispose: () => void
}

// 已提交文档的 URL 级缓存上限。缓存只为消除"回到访问过的页面出骨架",
// 命中后总是后台重验证,所以上限只是内存兜底,不是新鲜度参数。
const cachedPageLimit = 20

export function createNavigationCoordinator<PageData>(
  options: NavigationOptions<PageData>,
): ManagedNavigationCoordinator<PageData> {
  const current = shallowRef<PageData | undefined>(options.initial?.data)
  const currentURL = shallowRef(options.initial?.url)
  const loading = shallowRef(false)
  const error = shallowRef<Error | null>(null)
  const staged = shallowRef<StagedDocument<PageData>>()
  const cachedPages = new Map<string, ParsedPageData<PageData>>()
  let active: ActiveNavigation | undefined
  let sequence = 0
  let disposed = false
  let revalidationPending = false

  if (options.initial)
    rememberPage(options.initial)

  function rememberPage(parsed: ParsedPageData<PageData>) {
    if (!options.fetcher)
      return
    cachedPages.delete(parsed.url)
    cachedPages.set(parsed.url, parsed)
    if (cachedPages.size > cachedPageLimit) {
      const oldest = cachedPages.keys().next().value
      if (oldest !== undefined)
        cachedPages.delete(oldest)
    }
  }

  function prepare(
    rawURL: string,
    prepareOptions: { force?: boolean } = {},
  ): Promise<NavigationPreparation> {
    if (disposed)
      return Promise.resolve({ kind: 'cancelled' })

    const url = canonicalNavigationURL(rawURL)
    if (
      active?.kind === 'route'
      && navigationURLsMatch(active.url, url)
    ) {
      return active.promise
    }
    if (
      staged.value?.id === sequence
      && navigationURLsMatch(staged.value.targetURL, url)
    ) {
      return Promise.resolve({ kind: 'ready' })
    }

    return startNavigation(url, prepareOptions.force === true, 'route')
  }

  function startNavigation(
    url: string,
    force: boolean,
    kind: ActiveNavigation['kind'],
  ): Promise<NavigationPreparation> {
    const id = ++sequence

    active?.controller.abort()
    active = undefined
    loading.value = false
    staged.value = undefined
    error.value = null

    if (
      !force
      && current.value !== undefined
      && currentURL.value !== undefined
      && navigationURLsMatch(currentURL.value, url)
    ) {
      staged.value = {
        id,
        url: currentURL.value,
        targetURL: url,
        data: current.value,
      }
      return Promise.resolve({ kind: 'ready' })
    }

    // 命中缓存的页面立即就绪(回到访问过的页面不出骨架),提交后由
    // runtime 层触发一次公开 refresh 做后台重验证:新鲜文档无感替换,
    // 服务端裁决变成重定向/错误时走正常的路由替换或整页兜底。
    if (kind === 'route') {
      const remembered = options.fetcher ? cachedPages.get(url) : undefined
      if (remembered) {
        staged.value = {
          id,
          targetURL: url,
          fromCache: true,
          ...remembered,
        }
        return Promise.resolve({ kind: 'ready' })
      }
    }

    if (!options.fetcher) {
      const cause = new Error(`No page document is available for ${url}`)
      error.value = cause
      return Promise.resolve({ kind: 'error', error: cause })
    }

    const controller = new AbortController()
    loading.value = true

    const promise = (async (): Promise<NavigationPreparation> => {
      try {
        const value = await options.fetcher!(url, controller.signal)
        if (controller.signal.aborted || id !== sequence || disposed)
          return { kind: 'cancelled' }

        const outcome = decodeNavigationOutcome(value)
        if (outcome.kind === 'redirect') {
          return {
            kind: 'redirect',
            status: outcome.status,
            location: canonicalNavigationURL(outcome.location),
          }
        }
        if (outcome.kind === 'error') {
          const cause = new Error(outcome.message || '页面数据加载失败')
          error.value = cause
          return {
            kind: 'error',
            status: outcome.status,
            code: outcome.code,
            error: cause,
          }
        }

        let parsed: ParsedPageData<PageData>
        try {
          parsed = parsePageData(options.codec, outcome.snapshot, 'Navigation response')
        }
        catch (reason) {
          const cause = asError(reason, 'Navigation response contains an invalid page document')
          error.value = cause
          return { kind: 'error', status: outcome.status, error: cause }
        }

        if (parsed.url !== url) {
          const cause = new Error(
            `Page document URL mismatch: expected ${url}, received ${parsed.url}`,
          )
          error.value = cause
          return { kind: 'error', status: outcome.status, error: cause }
        }

        staged.value = {
          id,
          targetURL: url,
          ...parsed,
        }
        return { kind: 'ready' }
      }
      catch (reason) {
        if (
          controller.signal.aborted
          || id !== sequence
          || disposed
          || isAbortError(reason)
        ) {
          return { kind: 'cancelled' }
        }

        const cause = asError(reason, '页面数据加载失败')
        error.value = cause
        return { kind: 'error', error: cause }
      }
      finally {
        finishNavigation(id)
      }
    })()

    active = { id, kind, url, controller, promise }
    return promise
  }

  function finishNavigation(id: number) {
    if (active?.id !== id)
      return

    active = undefined
    loading.value = false
  }

  function commit(rawURL: string) {
    if (disposed)
      return false

    const url = canonicalNavigationURL(rawURL)
    const candidate = staged.value
    if (!candidate || candidate.id !== sequence || candidate.targetURL !== url)
      return false

    current.value = candidate.data
    currentURL.value = candidate.url
    staged.value = undefined
    error.value = null
    rememberPage({ data: candidate.data, url: candidate.url })
    if (candidate.fromCache)
      revalidationPending = true
    return true
  }

  function consumeRevalidation() {
    const pending = revalidationPending
    revalidationPending = false
    return pending
  }

  function clearCached() {
    cachedPages.clear()
  }

  async function refresh(rawURL: string) {
    if (disposed || active?.kind === 'route')
      return { kind: 'cancelled' } satisfies NavigationPreparation

    const url = canonicalNavigationURL(rawURL)
    const result = await startNavigation(url, true, 'refresh')
    if (result.kind === 'ready' && !commit(rawURL))
      return { kind: 'cancelled' } satisfies NavigationPreparation
    return result
  }

  function cancelRoute() {
    if (active?.kind !== 'route')
      return

    sequence += 1
    active.controller.abort()
    active = undefined
    staged.value = undefined
    loading.value = false
  }

  function dispose() {
    if (disposed)
      return

    disposed = true
    sequence += 1
    active?.controller.abort()
    active = undefined
    staged.value = undefined
    cachedPages.clear()
    revalidationPending = false
    loading.value = false
  }

  return {
    current,
    currentURL,
    loading,
    error,
    prepare,
    commit,
    refresh,
    consumeRevalidation,
    clearCached,
    cancelRoute,
    dispose,
  }
}

export async function fetchNavigationOutcome(
  rawURL: string,
  signal: AbortSignal,
): Promise<unknown> {
  const url = canonicalNavigationURL(rawURL)
  const response = await fetch(`/_ssr/data${url}`, {
    credentials: 'same-origin',
    signal,
    headers: {
      Accept: 'application/json',
    },
  })

  let value: unknown
  try {
    value = await response.json()
  }
  catch {
    throw new Error(`页面数据加载失败 (${response.status})`)
  }

  if (!isRecord(value)) {
    throw new Error(`页面数据加载失败 (${response.status})`)
  }
  if (typeof value.message === 'string' && typeof value.kind !== 'string')
    throw new Error(value.message)
  return value
}

export function decodeNavigationOutcome(value: unknown): NavigationOutcome<unknown> {
  if (!isRecord(value) || typeof value.kind !== 'string')
    throw new Error('Navigation response has an invalid envelope')
  if (!isHTTPStatus(value.status))
    throw new Error('Navigation response has an invalid HTTP status')

  if (value.kind === 'render') {
    if (!isDocumentStatus(value.status))
      throw new Error('Navigation render has an invalid document status')
    return {
      kind: 'render',
      status: value.status,
      snapshot: value.snapshot,
    }
  }
  if (value.kind === 'redirect') {
    if (!isRedirectStatus(value.status))
      throw new Error('Navigation redirect has an invalid redirect status')
    if (typeof value.location !== 'string')
      throw new Error('Navigation redirect has no location')
    // Validate here. The coordinator canonicalizes once more when it consumes
    // the typed branch, keeping this parser free of application knowledge.
    canonicalNavigationURL(value.location)
    return {
      kind: 'redirect',
      status: value.status,
      location: value.location,
    }
  }
  if (value.kind === 'error') {
    if (value.status < 400)
      throw new Error('Navigation error has an invalid error status')
    if (typeof value.code !== 'string' || typeof value.message !== 'string')
      throw new Error('Navigation error has an invalid payload')
    return {
      kind: 'error',
      status: value.status,
      code: value.code,
      message: value.message,
    }
  }

  throw new Error(`Unsupported navigation outcome: ${value.kind}`)
}

const redirectStatuses = new Set([301, 302, 303, 307, 308])

function isDocumentStatus(status: number) {
  return (
    status >= 200
    && status <= 599
    && (status < 300 || status >= 400)
    && status !== 204
    && status !== 205
  )
}

function isRedirectStatus(status: number) {
  return redirectStatuses.has(status)
}

function isHTTPStatus(value: unknown): value is number {
  return (
    typeof value === 'number'
    && Number.isInteger(value)
    && value >= 100
    && value <= 599
  )
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isAbortError(reason: unknown) {
  return (
    typeof reason === 'object'
    && reason !== null
    && (reason as { name?: unknown }).name === 'AbortError'
  )
}

function asError(reason: unknown, fallback: string) {
  return reason instanceof Error ? reason : new Error(fallback)
}
