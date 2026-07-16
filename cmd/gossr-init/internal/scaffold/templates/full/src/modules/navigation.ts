import { routes } from 'vue-router/auto-routes'
import type { RouteRecordRaw } from 'vue-router'

interface NavigationItem {
  label: string
  order: number
  to: string
}

function collect(records: readonly RouteRecordRaw[], parentPath = ''): NavigationItem[] {
  return records.flatMap((route) => {
    const path = route.path.startsWith('/') ? route.path : `${parentPath}/${route.path}`
    const metadata = route.meta?.nav as { label?: string, order?: number, to?: string } | undefined
    const own = metadata?.label
      ? [{ label: metadata.label, order: metadata.order ?? 100, to: metadata.to ?? path }]
      : []
    return own.concat(route.children ? collect(route.children, path) : [])
  })
}

export const navigationItems = collect(routes).sort((left, right) => left.order - right.order)
