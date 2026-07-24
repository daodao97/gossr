import { computed, shallowRef } from 'vue'

import { parseDocument } from './document'
import { canonicalNavigationURL, navigationURLsMatch } from './url'
import type {
  DocumentCodec,
  NavigationCoordinator,
  NavigationOutcome,
  NavigationPreparation,
} from './types'
import type { ParsedDocument } from './document'

export type NavigationFetcher = (
  url: string,
  signal: AbortSignal,
) => Promise<unknown>

interface StagedDocument<Document> extends ParsedDocument<Document> {
  id: number
  targetURL: string
}

interface ActiveNavigation {
  id: number
  controller: AbortController
}

interface NavigationOptions<Document> {
  codec: DocumentCodec<Document>
  initial?: ParsedDocument<Document>
  fetcher?: NavigationFetcher
}

export interface ManagedNavigationCoordinator<Document>
  extends NavigationCoordinator<Document> {
  dispose: () => void
}

export function createNavigationCoordinator<Document>(
  options: NavigationOptions<Document>,
): ManagedNavigationCoordinator<Document> {
  const current = shallowRef<Document | undefined>(options.initial?.document)
  const currentURL = shallowRef(options.initial?.url)
  const loading = shallowRef(false)
  const error = shallowRef<Error | null>(null)
  const staged = shallowRef<StagedDocument<Document>>()
  let active: ActiveNavigation | undefined
  let sequence = 0
  let disposed = false

  async function prepare(
    rawURL: string,
    prepareOptions: { force?: boolean } = {},
  ): Promise<NavigationPreparation> {
    if (disposed)
      return { kind: 'cancelled' }

    const url = canonicalNavigationURL(rawURL)
    const id = ++sequence

    active?.controller.abort()
    active = undefined
    loading.value = false
    staged.value = undefined
    error.value = null

    if (
      !prepareOptions.force
      && current.value !== undefined
      && currentURL.value !== undefined
      && navigationURLsMatch(currentURL.value, url)
    ) {
      staged.value = {
        id,
        url: currentURL.value,
        targetURL: url,
        document: current.value,
      }
      return { kind: 'ready' }
    }

    if (!options.fetcher) {
      const cause = new Error(`No page document is available for ${url}`)
      error.value = cause
      return { kind: 'error', error: cause }
    }

    const controller = new AbortController()
    active = { id, controller }
    loading.value = true

    try {
      const value = await options.fetcher(url, controller.signal)
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

      let parsed: ParsedDocument<Document>
      try {
        parsed = parseDocument(options.codec, outcome.snapshot, 'Navigation response')
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
      if (active?.id === id) {
        active = undefined
        loading.value = false
      }
    }
  }

  function commit(rawURL: string) {
    if (disposed)
      return false

    const url = canonicalNavigationURL(rawURL)
    const candidate = staged.value
    if (!candidate || candidate.id !== sequence || candidate.targetURL !== url)
      return false

    current.value = candidate.document
    currentURL.value = candidate.url
    staged.value = undefined
    error.value = null
    return true
  }

  async function refresh(rawURL: string) {
    const result = await prepare(rawURL, { force: true })
    if (result.kind === 'ready' && !commit(rawURL))
      return { kind: 'cancelled' } satisfies NavigationPreparation
    return result
  }

  function dispose() {
    if (disposed)
      return

    disposed = true
    sequence += 1
    active?.controller.abort()
    active = undefined
    staged.value = undefined
    loading.value = false
  }

  return {
    current,
    loading,
    error,
    stagedURL: computed(() => staged.value?.url),
    prepare,
    commit,
    refresh,
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
