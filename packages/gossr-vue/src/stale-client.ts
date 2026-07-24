import { canonicalNavigationURL } from './url'

export function recoverStaleClientRoute(
  appId: string,
  error: unknown,
  targetURL: string,
) {
  if (typeof window === 'undefined' || !isStaleClientAssetError(error))
    return false

  let safeTargetURL: string
  try {
    safeTargetURL = canonicalNavigationURL(targetURL)
  }
  catch {
    return false
  }

  const storageKey = staleClientReloadKey(appId)
  try {
    if (window.sessionStorage.getItem(storageKey) === safeTargetURL) {
      window.sessionStorage.removeItem(storageKey)
      return false
    }
    window.sessionStorage.setItem(storageKey, safeTargetURL)
  }
  catch {
    return false
  }

  window.location.assign(safeTargetURL)
  return true
}

export function clearStaleClientRecovery(appId: string) {
  if (typeof window === 'undefined')
    return

  try {
    window.sessionStorage.removeItem(staleClientReloadKey(appId))
  }
  catch {
    // Storage can be unavailable in restricted browser contexts.
  }
}

function staleClientReloadKey(appId: string) {
  return `${appId}:stale-client-route`
}

function isStaleClientAssetError(error: unknown) {
  const name = error instanceof Error ? error.name : ''
  const message = error instanceof Error ? error.message : String(error)
  return name === 'ChunkLoadError'
    || /failed to fetch dynamically imported module/i.test(message)
    || /error loading dynamically imported module/i.test(message)
    || /importing a module script failed/i.test(message)
    || /loading chunk \S+ failed/i.test(message)
}
