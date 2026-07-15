import { renderToString } from '@vue/server-renderer'

import { createAppRouter, makeApp } from '~/main'
import type { SsrState } from '~/composables/useSsrData'

const router = createAppRouter()

async function render(url: string) {
  const initialState: SsrState = (globalThis as any).__SSR_DATA__ ?? {}
  const { app, dispose } = makeApp(initialState, { router })
  try {
    await router.replace(url)

    if (shouldSimulateSlowSSR(url))
      blockFor(3500)

    const ctx: any = {}

    ;(globalThis as any).__SSR_HEAD__ = ''
    const html = await renderToString(app, ctx)
    const head = typeof ctx.teleports?.head === 'string' ? ctx.teleports.head : ''
    ;(globalThis as any).__SSR_HEAD__ = head

    return html
  }
  finally {
    dispose()
  }
}

function ssrRender(url: string) {
  return render(url)
}

(globalThis as any).ssrRender = ssrRender

function shouldSimulateSlowSSR(rawURL: string): boolean {
  const pathname = rawURL.split('#')[0].split('?')[0]
  return /^(\/(en|zh))?\/slow-ssr$/.test(pathname)
}

function blockFor(ms: number): void {
  const deadline = Date.now() + ms
  let iterations = 0
  while (Date.now() < deadline) {
    iterations++
  }
  // Keep the loop observable so production minifiers cannot remove it.
  ;(globalThis as any).__SSR_SLOW_ITERATIONS__ = iterations
}
