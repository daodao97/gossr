const absoluteURLPattern = /^[a-z][a-z\d+.-]*:/i
const invalidRawCharacterPattern = /[\u0000-\u0020\u007f\\]/
const invalidDecodedCharacterPattern = /[\u0000-\u001f\u007f\\]/
const invalidPercentPattern = /%(?![\da-f]{2})/i

export function canonicalNavigationURL(rawURL: string) {
  if (typeof rawURL !== 'string')
    throw new Error('Navigation URL must be a string')
  if (rawURL === '')
    throw new Error('Navigation URL must not be empty')
  if (rawURL !== rawURL.trim() || invalidRawCharacterPattern.test(rawURL))
    throw new Error('Navigation URL contains unsafe characters')
  if (absoluteURLPattern.test(rawURL) || rawURL.startsWith('//'))
    throw new Error(`Navigation URL must be same-origin: ${rawURL}`)
  if (rawURL.includes('#'))
    throw new Error('Navigation URL must not contain a fragment')

  const queryIndex = rawURL.indexOf('?')
  const rawPath = queryIndex >= 0 ? rawURL.slice(0, queryIndex) : rawURL
  const rawQuery = queryIndex >= 0 ? rawURL.slice(queryIndex + 1) : ''
  const path = rawPath

  if (path === '' || !path.startsWith('/'))
    throw new Error(`Navigation URL must start with "/": ${rawURL}`)
  if (invalidPercentPattern.test(path) || invalidPercentPattern.test(rawQuery))
    throw new Error('Navigation URL contains an invalid percent escape')

  const decodedPath = decodeURLPart(path)
  if (
    !decodedPath.startsWith('/')
    || decodedPath.startsWith('//')
    || invalidDecodedCharacterPattern.test(decodedPath)
  ) {
    throw new Error('Navigation URL contains an unsafe encoded path')
  }
  for (const segment of decodedPath.split('/')) {
    if (segment === '.' || segment === '..')
      throw new Error('Navigation URL contains a dot path segment')
  }

  if (rawQuery) {
    const decodedQuery = decodeURLPart(rawQuery)
    if (invalidDecodedCharacterPattern.test(decodedQuery))
      throw new Error('Navigation URL contains an unsafe encoded character')
  }

  return queryIndex >= 0 ? `${path}?${rawQuery}` : path
}

/**
 * Converts Vue Router's browser-facing fullPath into the path/query identity
 * used by SSR documents and the navigation data endpoint.
 *
 * A URL fragment is client-only and is never sent to Go. Keep
 * canonicalNavigationURL() strict for wire values; router-facing code must
 * cross this adapter before comparing, preparing, committing, or refreshing a
 * page document.
 */
export function documentURLFromRouter(fullPath: string) {
  if (typeof fullPath !== 'string')
    throw new Error('Router URL must be a string')

  const fragmentIndex = fullPath.indexOf('#')
  const documentURL = fragmentIndex >= 0
    ? fullPath.slice(0, fragmentIndex)
    : fullPath
  return canonicalNavigationURL(documentURL)
}

export function safeNavigationURL(value: unknown, fallback = '/') {
  const safeFallback = canonicalNavigationURL(fallback)
  if (typeof value !== 'string')
    return safeFallback

  try {
    return canonicalNavigationURL(value)
  }
  catch {
    return safeFallback
  }
}

export function navigationURLsMatch(left: string, right: string) {
  const normalizedLeft = canonicalNavigationURL(left)
  const normalizedRight = canonicalNavigationURL(right)
  if (normalizedLeft === normalizedRight)
    return true

  // Vue Router intentionally normalizes an empty query away. Preserve
  // ForceQuery in the wire document, but treat `/path` and `/path?` as the
  // same route when comparing with router.currentRoute.
  return withoutEmptyQuery(normalizedLeft) === withoutEmptyQuery(normalizedRight)
}

function withoutEmptyQuery(value: string) {
  return value.endsWith('?') ? value.slice(0, -1) : value
}

function decodeURLPart(value: string) {
  try {
    return decodeURIComponent(value)
  }
  catch {
    throw new Error('Navigation URL contains an invalid percent escape')
  }
}
