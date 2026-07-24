// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest'

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

    expect(recovered).toBe('retry-exhausted')
    expect(window.sessionStorage.length).toBe(0)
  })

  it('reloads a stale route at most once across consecutive boots', () => {
    const navigateDocument = vi.fn()
    const error = new Error('Failed to fetch dynamically imported module')

    expect(recoverStaleClientRoute(
      'reload-once',
      error,
      '/dashboard',
      navigateDocument,
    )).toBe('reloaded')
    expect(navigateDocument).toHaveBeenCalledOnce()
    expect(navigateDocument).toHaveBeenCalledWith('/dashboard')

    expect(recoverStaleClientRoute(
      'reload-once',
      error,
      '/dashboard',
      navigateDocument,
    )).toBe('retry-exhausted')
    expect(navigateDocument).toHaveBeenCalledOnce()
  })

  it('leaves non-stale router failures to the generic fallback', () => {
    expect(recoverStaleClientRoute(
      'ordinary-error',
      new Error('guard failed'),
      '/dashboard',
      vi.fn(),
    )).toBe('not-stale')
  })
})
