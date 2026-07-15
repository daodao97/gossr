import { describe, expect, it } from 'vitest'

import {
  preferredLocalePathAfterHydration,
  readSavedLocale,
  saveLocale,
  shouldHydrateDocument,
} from './client-startup'

describe('client locale startup', () => {
  it('defers a saved non-default locale until after hydration', () => {
    expect(preferredLocalePathAfterHydration(
      { pathname: '/', search: '?from=ssr', hash: '#top' },
      'zh',
    )).toBe('/zh?from=ssr#top')
  })

  it('does not override an explicit locale or redirect to the default locale', () => {
    expect(preferredLocalePathAfterHydration(
      { pathname: '/en/hi/vue', search: '', hash: '' },
      'zh',
    )).toBeNull()
    expect(preferredLocalePathAfterHydration(
      { pathname: '/hi/vue', search: '', hash: '' },
      'en',
    )).toBeNull()
  })

  it('survives disabled browser storage', () => {
    const storage = {
      getItem(): string | null {
        throw new Error('storage disabled')
      },
      setItem(): void {
        throw new Error('storage disabled')
      },
    }

    expect(readSavedLocale(storage)).toBeNull()
    expect(() => saveLocale(storage, 'zh')).not.toThrow()
  })

  it('hydrates only a successful non-empty SSR document', () => {
    const documentWith = (html: string, failed = false) => ({
      querySelector(selector: string) {
        if (selector === 'meta[name="ssr-error-id"]')
          return failed ? { innerHTML: '' } : null
        if (selector === '#app')
          return { innerHTML: html }
        return null
      },
    })

    expect(shouldHydrateDocument(documentWith('<main>SSR</main>'))).toBe(true)
    expect(shouldHydrateDocument(documentWith(''))).toBe(false)
    expect(shouldHydrateDocument(documentWith('<main>fallback</main>', true))).toBe(false)
  })
})
