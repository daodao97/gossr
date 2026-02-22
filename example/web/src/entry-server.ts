import { renderToString } from '@vue/server-renderer'

import { makeApp } from './main'
import type { ExamplePayload } from './main'

export async function render(url: string) {
  const initialState: ExamplePayload = (globalThis as any).__SSR_DATA__ ?? {}
  const app = makeApp(url, initialState)

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

;(globalThis as any).ssrRender = ssrRender
