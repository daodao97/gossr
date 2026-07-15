import {
  defaultLocale,
  isSupportedLocale,
  type SupportedLocale,
} from './i18n'

interface LocationParts {
  hash: string
  pathname: string
  search: string
}

interface LocaleStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

interface StartupDocument {
  querySelector(selector: string): { innerHTML: string } | null
}

export function shouldHydrateDocument(document: StartupDocument): boolean {
  if (document.querySelector('meta[name="ssr-error-id"]'))
    return false

  const appRoot = document.querySelector('#app')
  return appRoot !== null && appRoot.innerHTML.trim().length > 0
}

export function readSavedLocale(storage: LocaleStorage): SupportedLocale | null {
  try {
    const saved = storage.getItem('locale')
    return saved && isSupportedLocale(saved) ? saved : null
  }
  catch {
    return null
  }
}

export function saveLocale(storage: LocaleStorage, locale: SupportedLocale): void {
  try {
    storage.setItem('locale', locale)
  }
  catch {
    // Storage can be unavailable in privacy modes; locale still works in-memory.
  }
}

// The first client render must follow the server URL exactly. A saved locale is
// applied only after hydration so Vue never compares different-language trees.
export function preferredLocalePathAfterHydration(
  location: LocationParts,
  savedLocale: SupportedLocale | null,
): string | null {
  if (!savedLocale || savedLocale === defaultLocale || hasLocaleSegment(location.pathname))
    return null

  const pathname = location.pathname === '/' ? '' : location.pathname
  return `/${savedLocale}${pathname}${location.search}${location.hash}`
}

function hasLocaleSegment(pathname: string): boolean {
  const firstSegment = pathname.replace(/^\/+/, '').split('/')[0]
  return isSupportedLocale(firstSegment)
}
