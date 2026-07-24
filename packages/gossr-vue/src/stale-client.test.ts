// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from 'vitest'

import { recoverStaleClientRoute } from './stale-client'

describe('stale client recovery', () => {
  beforeEach(() => {
    window.sessionStorage.clear()
  })

  it.each([
    '//evil.example/path',
    '/\\evil.example/path',
    '/%5Cevil.example/path',
    '/%2e%2e%2fsecret',
    '/page#fragment',
    '/page\u0000tail',
  ])('refuses an unsafe reload target %s', (target) => {
    const recovered = recoverStaleClientRoute(
      'security-test',
      new Error('Failed to fetch dynamically imported module'),
      target,
    )

    expect(recovered).toBe(false)
    expect(window.sessionStorage.length).toBe(0)
  })
})
