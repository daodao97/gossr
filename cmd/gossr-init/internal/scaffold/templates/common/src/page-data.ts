// The page data is produced by the Go PageResolver. Keep the Go struct and
// this type in sync; per-page shape is guaranteed by Go's type system, so the
// codec only needs a light structural check.
export interface PageData {
  schema_version: 1
  url: string
  page: {
    kind: string
    message: string
    generated_at: string
  }
}

export function parsePageData(value: unknown): PageData {
  if (!isRecord(value) || value.schema_version !== 1 || typeof value.url !== 'string')
    throw new Error('invalid page data')
  if (!isRecord(value.page) || typeof value.page.kind !== 'string')
    throw new Error('invalid page data page')
  return value as unknown as PageData
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
