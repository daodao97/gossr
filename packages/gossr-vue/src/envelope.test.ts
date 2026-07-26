import { describe, expect, it } from 'vitest'

import { parsePageData } from './page-data'
import { isStandardPageData, standardPageDataCodec } from './envelope'

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
    expect(isStandardPageData(baseDocument)).toBe(true)
  })

  it('accepts a nullable viewer and a present viewer', () => {
    expect(isStandardPageData(documentWith({ viewer: null }))).toBe(true)
    expect(isStandardPageData(documentWith({ viewer: { user: {} } }))).toBe(true)
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
    expect(isStandardPageData(value)).toBe(false)
  })

  it('honors a custom schema version', () => {
    expect(isStandardPageData(documentWith({ schema_version: 2 }), 2)).toBe(true)
    expect(isStandardPageData(baseDocument, 2)).toBe(false)
  })

  it('standardPageDataCodec parses valid documents and throws on skew', () => {
    const codec = standardPageDataCodec<typeof baseDocument>()
    expect(codec.parse(structuredClone(baseDocument)).page.kind).toBe('login')
    expect(() => codec.parse(documentWith({ schema_version: 2 })))
      .toThrowError('standard gossr page document')
  })

  it('unsafe URLs are rejected downstream by parsePageData', () => {
    const codec = standardPageDataCodec<typeof baseDocument>()
    expect(() => parsePageData(codec, documentWith({ url: '//evil.example/path' }), 'Test'))
      .toThrowError()
    expect(parsePageData(codec, structuredClone(baseDocument), 'Test').url)
      .toBe('/login?next=%2Fdashboard')
  })
})
