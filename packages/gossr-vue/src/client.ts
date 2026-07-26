import { attachCleanupError } from './lifecycle.js'
import { parsePageData } from './page-data.js'
import { createApplicationRuntime } from './runtime.js'
import {
  documentURLFromRouter,
  navigationURLsMatch,
} from './url.js'
import type { ParsedPageData } from './page-data.js'
import type {
  GossrAppDefinition,
} from './types.js'
import type { GossrApplicationRuntime } from './runtime.js'

const bootElementID = '__GOSSR_BOOT__'

export async function bootstrapClient<PageData>(
  definition: GossrAppDefinition<PageData>,
): Promise<GossrApplicationRuntime<PageData>> {
  const bootElement = document.querySelector<HTMLScriptElement>(`#${bootElementID}`)
  let runtime: GossrApplicationRuntime<PageData> | undefined

  try {
    const browserURL = currentBrowserURL()
    const bootPageData = readBootPageData(definition, bootElement)
    const initial = bootPageData && navigationURLsMatch(bootPageData.url, browserURL)
      ? bootPageData
      : undefined
    if (bootPageData && !initial) {
      console.error(
        `[ssr] boot page URL mismatch: expected ${browserURL}, received ${bootPageData.url}`,
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

export function readBootPageData<PageData>(
  definition: GossrAppDefinition<PageData>,
  bootElement = document.querySelector<HTMLScriptElement>(`#${bootElementID}`),
): ParsedPageData<PageData> | undefined {
  if (!bootElement?.textContent)
    return undefined

  try {
    const value: unknown = JSON.parse(bootElement.textContent)
    return parsePageData(definition.pageData, value, 'Boot')
  }
  catch (error) {
    console.error('[ssr] boot page document is invalid', error)
    return undefined
  }
}

export function shouldHydrateApp<PageData>(
  initial: ParsedPageData<PageData> | undefined,
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

async function requireInitialNavigation<PageData>(
  runtime: GossrApplicationRuntime<PageData>,
) {
  if (!runtime.initialNavigation)
    throw new Error('Client runtime did not create an initial navigation')
  await runtime.initialNavigation
}
