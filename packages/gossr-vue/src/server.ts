import { renderToString } from '@vue/server-renderer'

import { parseDocument } from './document.js'
import { attachCleanupError } from './lifecycle.js'
import {
  createApplicationRuntime,
  navigateServerRuntime,
} from './runtime.js'
import { canonicalNavigationURL } from './url.js'
import type {
  DocumentCodec,
  GossrAppDefinition,
  SSRRenderInput,
  SSRRenderResult,
} from './types.js'
import type { ParsedDocument } from './document.js'

export function createSSRRenderer<Document>(
  definition: GossrAppDefinition<Document>,
) {
  return async function renderSSR(input: SSRRenderInput): Promise<SSRRenderResult> {
    const { target, parsed } = decodeSSRRenderInput(definition.document, input)

    const runtime = createApplicationRuntime(definition, {
      platform: 'server',
      initial: parsed,
      hydrate: true,
    })
    let result: SSRRenderResult | undefined
    let primary: unknown
    let hasPrimary = false

    try {
      await navigateServerRuntime(runtime, target, parsed.url)
      result = {
        html: await renderToString(runtime.app),
        head: '',
      }
    }
    catch (error) {
      hasPrimary = true
      primary = error
    }

    try {
      runtime.dispose()
    }
    catch (cleanupFailure) {
      if (hasPrimary)
        attachCleanupError(primary, cleanupFailure)
      else {
        hasPrimary = true
        primary = cleanupFailure
      }
    }

    if (hasPrimary)
      throw primary
    return result as SSRRenderResult
  }
}

export function decodeSSRRenderInput<Document>(
  codec: DocumentCodec<Document>,
  input: unknown,
): { target: string, parsed: ParsedDocument<Document> } {
  if (!isRecord(input) || typeof input.url !== 'string')
    throw new Error('SSR renderer received an invalid input')

  const target = canonicalNavigationURL(input.url)
  const parsed = parseDocument(codec, input.snapshot, 'SSR')
  if (parsed.url !== target)
    throw new Error('SSR renderer URL does not match its page document')
  return { target, parsed }
}

export function installGojaRenderABI<Document>(
  definition: GossrAppDefinition<Document>,
) {
  type SSRGlobal = typeof globalThis & {
    __GOSSR_RENDER_ABI__?: number
    ssrRender?: (input: SSRRenderInput) => Promise<SSRRenderResult>
  }

  const ssrGlobal = globalThis as SSRGlobal
  ssrGlobal.__GOSSR_RENDER_ABI__ = 2
  ssrGlobal.ssrRender = createSSRRenderer(definition)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
