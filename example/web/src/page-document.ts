export interface SessionUser {
  id?: string
  name?: string
  email?: string
  provider?: string
}

export interface SessionPayload {
  user?: SessionUser
}

export interface PageDocument {
  schema_version: 1
  url: string
  locale: string
  session: SessionPayload | null
  page: {
    kind: string
    message: string
    generated_at: string
  }
}

// 文档由同源 Go resolver 产出;这里只做结构轻校验,per-page 形状
// 由 Go 类型系统保证。
export function parsePageDocument(value: unknown): PageDocument {
  if (!isRecord(value) || value.schema_version !== 1 || typeof value.url !== 'string')
    throw new Error('invalid page document')
  if (typeof value.locale !== 'string')
    throw new Error('invalid page document locale')
  if (value.session !== null && !isRecord(value.session))
    throw new Error('invalid page document session')
  if (!isRecord(value.page) || typeof value.page.kind !== 'string')
    throw new Error('invalid page document page')
  return value as unknown as PageDocument
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
