import { attachCleanupError } from './lifecycle'
import { parseDocument } from './document'
import { createApplicationRuntime } from './runtime'
import {
  documentURLFromRouter,
  navigationURLsMatch,
} from './url'
import type { ParsedDocument } from './document'
import type {
  GossrAppDefinition,
} from './types'
import type { GossrApplicationRuntime } from './runtime'

const bootElementID = '__GOSSR_BOOT__'

export async function bootstrapClient<Document>(
  definition: GossrAppDefinition<Document>,
): Promise<GossrApplicationRuntime<Document>> {
  const bootElement = document.querySelector<HTMLScriptElement>(`#${bootElementID}`)
  let runtime: GossrApplicationRuntime<Document> | undefined

  try {
    const browserURL = currentBrowserURL()
    const bootDocument = readBootDocument(definition, bootElement)
    const initial = bootDocument && navigationURLsMatch(bootDocument.url, browserURL)
      ? bootDocument
      : undefined
    if (bootDocument && !initial) {
      console.error(
        `[ssr] boot page URL mismatch: expected ${browserURL}, received ${bootDocument.url}`,
      )
    }

    const appRoot = document.querySelector<HTMLElement>('#app')
    if (!appRoot)
      throw new Error('Cannot bootstrap gossr: #app is missing')

    let hydrating = appRoot.dataset.ssr === 'true' && initial !== undefined
    runtime = createApplicationRuntime(definition, {
      platform: 'client',
      initial,
      hydrate: hydrating,
    })
    await requireInitialNavigation(runtime)

    if (
      initial
      && !navigationURLsMatch(
        documentURLFromRouter(runtime.router.currentRoute.value.fullPath),
        initial.url,
      )
    ) {
      // A route-level redirect changed the target after the boot document was
      // accepted. Never hydrate old markup into the redirected route.
      runtime.dispose()
      runtime = createApplicationRuntime(definition, {
        platform: 'client',
        hydrate: false,
      })
      hydrating = false
      await requireInitialNavigation(runtime)
    }

    const ssrRoot = hydrating ? appRoot.firstElementChild : null
    runtime.mount(appRoot)

    if (ssrRoot && appRoot.firstElementChild !== ssrRoot)
      console.error('[ssr] hydration failed: SSR markup was discarded')

    return runtime
  }
  catch (error) {
    let secondary: unknown
    try {
      runtime?.dispose()
    }
    catch (cleanupFailure) {
      secondary = cleanupFailure
    }
    attachCleanupError(error, secondary)
    throw error
  }
  finally {
    bootElement?.remove()
  }
}

export function readBootDocument<Document>(
  definition: GossrAppDefinition<Document>,
  bootElement = document.querySelector<HTMLScriptElement>(`#${bootElementID}`),
): ParsedDocument<Document> | undefined {
  if (!bootElement?.textContent)
    return undefined

  try {
    const value: unknown = JSON.parse(bootElement.textContent)
    return parseDocument(definition.document, value, 'Boot')
  }
  catch (error) {
    console.error('[ssr] boot page document is invalid', error)
    return undefined
  }
}

export function shouldHydrateApp<Document>(
  initial: ParsedDocument<Document> | undefined,
) {
  const appRoot = document.querySelector<HTMLElement>('#app')
  return appRoot?.dataset.ssr === 'true'
    && initial !== undefined
    && navigationURLsMatch(initial.url, currentBrowserURL())
}

function currentBrowserURL() {
  const relative = window.location.href.startsWith(window.location.origin)
    ? window.location.href.slice(window.location.origin.length)
    : `${window.location.pathname}${window.location.search}${window.location.hash}`
  return documentURLFromRouter(relative || '/')
}

async function requireInitialNavigation<Document>(
  runtime: GossrApplicationRuntime<Document>,
) {
  if (!runtime.initialNavigation)
    throw new Error('Client runtime did not create an initial navigation')
  await runtime.initialNavigation
}
