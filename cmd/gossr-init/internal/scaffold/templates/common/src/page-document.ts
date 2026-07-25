// The page document is produced by the Go PageResolver. Keep the Go struct and
// this type in sync; per-page shape is guaranteed by Go's type system, so this
// codec only needs a light structural check.
export interface PageDocument {
  schema_version: 1
  url: string
  page: {
    kind: string
    message: string
    generated_at: string
  }
}

export function parsePageDocument(value: unknown): PageDocument {
  if (!isRecord(value) || value.schema_version !== 1 || typeof value.url !== 'string')
    throw new Error('invalid page document')
  if (!isRecord(value.page) || typeof value.page.kind !== 'string')
    throw new Error('invalid page document page')
  return value as unknown as PageDocument
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
