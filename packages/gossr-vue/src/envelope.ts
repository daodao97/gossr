import type { PageDataCodec } from './types.js'

export interface StandardPageDataContext {
  site_origin: string
  locale: string
  time_zone: string
  now: string
}

/**
 * The standard gossr page-document envelope: a versioned URL-addressed
 * document carrying shared request context, an optional viewer, and one
 * kind-discriminated page payload. Hosts that adopt it get envelope
 * validation and version-skew recovery from the framework; hosts with a
 * different document shape keep supplying their own codec.
 */
export interface StandardPageData {
  schema_version: number
  url: string
  context: StandardPageDataContext
  viewer: Record<string, unknown> | null
  page: { kind: string, data: Record<string, unknown> }
}

/**
 * Structural envelope guard. It deliberately validates structure and
 * schema_version only: per-kind payload shapes are guaranteed by the host's
 * server-side type system and are not re-validated in the client. URL
 * validity is owned by parsePageData, which canonicalizes codec.url() next.
 */
export function isStandardPageData(
  value: unknown,
  schemaVersion = 1,
): value is StandardPageData {
  if (!isRecord(value) || value.schema_version !== schemaVersion || typeof value.url !== 'string')
    return false

  const context = value.context
  if (
    !isRecord(context)
    || typeof context.site_origin !== 'string'
    || typeof context.locale !== 'string'
    || typeof context.time_zone !== 'string'
    || typeof context.now !== 'string'
  ) {
    return false
  }
  if (value.viewer !== null && !isRecord(value.viewer))
    return false

  const page = value.page
  return isRecord(page) && typeof page.kind === 'string' && isRecord(page.data)
}

/**
 * Ready-made codec for standard-envelope documents. A structurally unknown
 * document means this client build is outdated (deploy skew): parse throws,
 * and the runtime falls back to a full browser navigation, which loads the
 * current bundle and self-heals.
 */
export function standardPageDataCodec<PageData extends { url: string }>(
  schemaVersion = 1,
): PageDataCodec<PageData> {
  return {
    parse(value) {
      if (!isStandardPageData(value, schemaVersion))
        throw new Error('value is not a standard gossr page document')
      return value as unknown as PageData
    },
    url(document) {
      return document.url
    },
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
