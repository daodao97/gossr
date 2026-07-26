import { isThenable } from './lifecycle.js'
import { canonicalNavigationURL } from './url.js'
import type { PageDataCodec } from './types.js'

export interface ParsedPageData<PageData> {
  data: PageData
  url: string
}

export function parsePageData<PageData>(
  codec: PageDataCodec<PageData>,
  value: unknown,
  source: string,
): ParsedPageData<PageData> {
  const data: unknown = codec.parse(value)
  if (isThenable(data))
    throw new Error(`${source} document codec returned a Promise/thenable`)

  const rawURL = codec.url(data as PageData)
  if (typeof rawURL !== 'string')
    throw new Error(`${source} document codec returned a non-string URL`)

  return {
    data: data as PageData,
    url: canonicalNavigationURL(rawURL),
  }
}
