import type { LocationQueryRaw, Router } from 'vue-router'

import type { MessageKey } from './i18n'

export interface NavigationRouteMeta {
  labelKey: MessageKey
  order: number
  query?: LocationQueryRaw
  to?: string
}

declare module 'vue-router' {
  interface RouteMeta {
    layout?: string
    nav?: NavigationRouteMeta
  }
}

export interface ParsedPathTarget {
  pathname: string
  search: string
  hash: string
}

export interface BaseNavigationLink {
  labelKey: MessageKey
  order: number
  target: ParsedPathTarget
}

const navigationCache = new WeakMap<Router, readonly BaseNavigationLink[]>()

// vue-router/vite 生成的路由表是唯一来源；这里只读取页面的 meta.nav，
// 并按 router 实例缓存，避免每次 SSR 重扫和解析路由。
export function navigationLinksFor(router: Router): readonly BaseNavigationLink[] {
  const cached = navigationCache.get(router)
  if (cached)
    return cached

  const links = router.getRoutes()
    .flatMap<BaseNavigationLink>((record) => {
      const nav = record.meta.nav
      if (!nav || record.aliasOf)
        return []

      let fullPath: string
      if (nav.to) {
        fullPath = router.resolve(nav.to).fullPath
      }
      else {
        fullPath = router.resolve({ path: record.path, query: nav.query }).fullPath
      }

      return [{
        labelKey: nav.labelKey,
        order: nav.order,
        target: parsePathTarget(fullPath),
      }]
    })
    .sort((left, right) => left.order - right.order || left.target.pathname.localeCompare(right.target.pathname))

  navigationCache.set(router, links)
  return links
}

export function parsePathTarget(rawTarget: string): ParsedPathTarget {
  const target = rawTarget.trim()
  if (!target)
    return { pathname: '/', search: '', hash: '' }

  let pathAndQuery = target
  let hash = ''
  const hashIndex = target.indexOf('#')
  if (hashIndex >= 0) {
    pathAndQuery = target.slice(0, hashIndex)
    hash = target.slice(hashIndex)
  }

  let pathname = pathAndQuery
  let search = ''
  const queryIndex = pathAndQuery.indexOf('?')
  if (queryIndex >= 0) {
    pathname = pathAndQuery.slice(0, queryIndex)
    search = pathAndQuery.slice(queryIndex)
  }

  if (!pathname)
    pathname = '/'
  if (!pathname.startsWith('/'))
    pathname = `/${pathname}`

  return { pathname, search, hash }
}
