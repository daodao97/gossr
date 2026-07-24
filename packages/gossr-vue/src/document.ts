import { isThenable } from './lifecycle'
import { canonicalNavigationURL } from './url'
import type { DocumentCodec } from './types'

export interface ParsedDocument<Document> {
  document: Document
  url: string
}

export function parseDocument<Document>(
  codec: DocumentCodec<Document>,
  value: unknown,
  source: string,
): ParsedDocument<Document> {
  const document: unknown = codec.parse(value)
  if (isThenable(document))
    throw new Error(`${source} document codec returned a Promise/thenable`)

  const rawURL = codec.url(document as Document)
  if (typeof rawURL !== 'string')
    throw new Error(`${source} document codec returned a non-string URL`)

  return {
    document: document as Document,
    url: canonicalNavigationURL(rawURL),
  }
}
