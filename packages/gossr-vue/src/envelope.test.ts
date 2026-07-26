import { describe, expect, it } from 'vitest'

import { parseDocument } from './document'
import { isStandardPageDocument, standardDocumentCodec } from './envelope'

const baseDocument = {
  schema_version: 1,
  url: '/login?next=%2Fdashboard',
  context: {
    site_origin: 'https://example.test',
    locale: 'zh-CN',
    time_zone: 'Asia/Shanghai',
    now: '2026-07-24T03:04:05Z',
  },
  viewer: null,
  page: {
    kind: 'login',
    data: {},
  },
}

function documentWith(overrides: Record<string, unknown>) {
  return { ...structuredClone(baseDocument), ...overrides }
}

describe('standard page-document envelope', () => {
  it('accepts a minimal valid document', () => {
    expect(isStandardPageDocument(baseDocument)).toBe(true)
  })

  it('accepts a nullable viewer and a present viewer', () => {
    expect(isStandardPageDocument(documentWith({ viewer: null }))).toBe(true)
    expect(isStandardPageDocument(documentWith({ viewer: { user: {} } }))).toBe(true)
  })

  it.each([
    ['null document', null],
    ['array document', []],
    ['unsupported schema', documentWith({ schema_version: 2 })],
    ['non-string URL', documentWith({ url: 7 })],
    ['missing context', documentWith({ context: undefined })],
    ['non-string context field', documentWith({
      context: { site_origin: 'https://example.test', locale: 'zh-CN', time_zone: 'Asia/Shanghai', now: 7 },
    })],
    ['non-object viewer', documentWith({ viewer: 'viewer' })],
    ['array page data', documentWith({ page: { kind: 'login', data: [] } })],
    ['null page data', documentWith({ page: { kind: 'login', data: null } })],
    ['non-string page kind', documentWith({ page: { kind: 7, data: {} } })],
  ])('rejects %s', (_name, value) => {
    expect(isStandardPageDocument(value)).toBe(false)
  })

  it('honors a custom schema version', () => {
    expect(isStandardPageDocument(documentWith({ schema_version: 2 }), 2)).toBe(true)
    expect(isStandardPageDocument(baseDocument, 2)).toBe(false)
  })

  it('standardDocumentCodec parses valid documents and throws on skew', () => {
    const codec = standardDocumentCodec<typeof baseDocument>()
    expect(codec.parse(structuredClone(baseDocument)).page.kind).toBe('login')
    expect(() => codec.parse(documentWith({ schema_version: 2 })))
      .toThrowError('standard gossr page document')
  })

  it('unsafe URLs are rejected downstream by parseDocument', () => {
    const codec = standardDocumentCodec<typeof baseDocument>()
    expect(() => parseDocument(codec, documentWith({ url: '//evil.example/path' }), 'Test'))
      .toThrowError()
    expect(parseDocument(codec, structuredClone(baseDocument), 'Test').url)
      .toBe('/login?next=%2Fdashboard')
  })
})
