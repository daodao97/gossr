import { readFileSync } from 'node:fs'

import { describe, expect, it } from 'vitest'

import {
  canonicalNavigationURL,
  documentURLFromRouter,
  navigationURLsMatch,
  safeNavigationURL,
} from './url'

interface NavigationURLCorpus {
  valid: Array<{ name: string, input: string, canonical: string }>
  invalid: Array<{ name: string, input: string }>
}

const corpus = JSON.parse(readFileSync(
  new URL('../../../testdata/navigation_urls.json', import.meta.url),
  'utf8',
)) as NavigationURLCorpus

describe('canonicalNavigationURL', () => {
  it.each(corpus.valid)(
    'preserves shared safe target $name',
    ({ input, canonical }) => {
      expect(canonicalNavigationURL(input)).toBe(canonical)
    },
  )

  it.each(corpus.invalid)(
    'rejects shared unsafe target $name',
    ({ input }) => {
      expect(() => canonicalNavigationURL(input)).toThrow()
    },
  )

  it('preserves ForceQuery while matching Vue Router normalization', () => {
    expect(canonicalNavigationURL('/foo?')).toBe('/foo?')
    expect(navigationURLsMatch('/foo?', '/foo')).toBe(true)
    expect(navigationURLsMatch('/foo?x=', '/foo')).toBe(false)
  })

  it('uses a validated local fallback for unsafe untrusted values', () => {
    expect(safeNavigationURL('/dashboard?tab=usage', '/dashboard'))
      .toBe('/dashboard?tab=usage')
    expect(safeNavigationURL('/\\evil.example/path', '/dashboard'))
      .toBe('/dashboard')
    expect(safeNavigationURL('/%5Cevil.example/path', '/dashboard'))
      .toBe('/dashboard')
    expect(safeNavigationURL(['//evil.example'], '/dashboard'))
      .toBe('/dashboard')
  })
})

describe('documentURLFromRouter', () => {
  it.each([
    {
      name: 'hash-only route state',
      fullPath: '/dashboard#usage',
      expected: '/dashboard',
    },
    {
      name: 'query followed by a fragment',
      fullPath: '/dashboard?tab=usage#models',
      expected: '/dashboard?tab=usage',
    },
    {
      name: 'encoded hash inside the query',
      fullPath: '/search?q=%23models#results',
      expected: '/search?q=%23models',
    },
    {
      name: 'force query before a fragment',
      fullPath: '/search?#results',
      expected: '/search?',
    },
  ])('maps $name to its document URL', ({ fullPath, expected }) => {
    expect(documentURLFromRouter(fullPath)).toBe(expected)
  })

  it('keeps the wire validator strict while adapting router comparisons', () => {
    expect(() => canonicalNavigationURL('/dashboard?tab=usage#models'))
      .toThrow('must not contain a fragment')
    expect(() => navigationURLsMatch(
      '/dashboard?tab=usage',
      '/dashboard?tab=usage#models',
    )).toThrow('must not contain a fragment')

    expect(navigationURLsMatch(
      '/dashboard?tab=usage',
      documentURLFromRouter('/dashboard?tab=usage#models'),
    )).toBe(true)
    expect(navigationURLsMatch(
      '/dashboard?tab=other',
      documentURLFromRouter('/dashboard?tab=usage#models'),
    )).toBe(false)
  })

  it.each([
    '//evil.example/path#models',
    '/%2e%2e%2fsecret#models',
    '/a%5Cb#models',
  ])('still rejects an unsafe document portion in %s', (fullPath) => {
    expect(() => documentURLFromRouter(fullPath)).toThrow()
  })
})
