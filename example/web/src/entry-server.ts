import { renderToString } from '@vue/server-renderer'

import { makeApp } from '~/main'
import type { SsrState } from '~/composables/useSsrData'

export async function render(url: string) {
  const initialState: SsrState = (globalThis as any).__SSR_DATA__ ?? {}
  const { app, router } = makeApp(initialState)
  await router.push(url)

  const ctx: any = {}

  ;(globalThis as any).__SSR_HEAD__ = ''
  const html = await renderToString(app, ctx)
  const head = typeof ctx.teleports?.head === 'string' ? ctx.teleports.head : ''
  ;(globalThis as any).__SSR_HEAD__ = head

  return html
}

async function ssrRender(url: string) {
  return await render(url)
}

(globalThis as any).ssrRender = ssrRender
