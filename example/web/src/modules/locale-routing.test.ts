import { describe, expect, it } from 'vitest'

import {
  localizeMenuTarget,
  parseLocaleRouteState,
  stripLocalePrefix,
  switchLocaleTarget,
} from './locale-routing'
import { parsePathTarget } from './navigation'

describe('locale routing', () => {
  it('parses targets without losing query or hash', () => {
    expect(parsePathTarget(' docs?q=vue#api ')).toEqual({
      pathname: '/docs',
      search: '?q=vue',
      hash: '#api',
    })
    expect(parsePathTarget('')).toEqual({ pathname: '/', search: '', hash: '' })
  })

  it('recognizes and removes only supported locale prefixes', () => {
    expect(parseLocaleRouteState('/zh/seo-demo')).toEqual({ locale: 'zh', explicit: true })
    expect(parseLocaleRouteState('/seo-demo')).toEqual({ locale: 'en', explicit: false })
    expect(stripLocalePrefix('/zh/seo-demo')).toBe('/seo-demo')
    expect(stripLocalePrefix('/unknown/seo-demo')).toBe('/unknown/seo-demo')
  })

  it('preserves query and hash while switching locale', () => {
    expect(switchLocaleTarget('/seo-demo?title=Vue#result', 'zh')).toBe('/zh/seo-demo?title=Vue#result')
    expect(switchLocaleTarget('/zh/seo-demo?title=Vue#result', 'en')).toBe('/seo-demo?title=Vue#result')
  })

  it('keeps the implicit default locale unprefixed', () => {
    const target = parsePathTarget('/seo-demo?title=Vue')
    expect(localizeMenuTarget(target, { locale: 'en', explicit: false })).toBe('/seo-demo?title=Vue')
    expect(localizeMenuTarget(target, { locale: 'zh', explicit: true })).toBe('/zh/seo-demo?title=Vue')
  })
})
