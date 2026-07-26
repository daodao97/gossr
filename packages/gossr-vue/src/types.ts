import type { App, Component, Ref } from 'vue'
import type { RouteRecordRaw, Router } from 'vue-router'

export type GossrPlatform = 'server' | 'client'

export interface PageDataCodec<PageData> {
  parse: (value: unknown) => PageData
  url: (pageData: PageData) => string
}

export type NavigationOutcome<PageData> =
  | {
    kind: 'render'
    status: number
    snapshot: PageData
  }
  | {
    kind: 'redirect'
    status: number
    location: string
  }
  | {
    kind: 'error'
    status: number
    code: string
    message: string
  }

export interface NavigationCoordinator<PageData> {
  /**
   * The last committed page data. During a client navigation it keeps
   * pointing at the previous page (stale-while-loading) so layout chrome
   * such as the viewer never flickers; page content should gate on `ready`.
   */
  current: Readonly<Ref<PageData | undefined>>
  /** True when `current` is the page data for the active route. */
  ready: Readonly<Ref<boolean>>
  loading: Readonly<Ref<boolean>>
  error: Readonly<Ref<Error | null>>
  refresh: () => Promise<boolean>
  /**
   * Drops the committed-page cache used for instant back-navigation.
   * Hosts MUST call this when the signed-in identity changes (login,
   * logout, account switch) so no page from the previous session can be
   * served, even transiently.
   */
  clearCached: () => void
}

export interface GossrSetupContext<PageData> {
  app: App
  router: Router
  navigation: NavigationCoordinator<PageData>
  platform: GossrPlatform
  onDispose: (disposer: () => void) => void
}

export interface GossrAppOptions<PageData> {
  appId: string
  root: Component
  routes: Readonly<RouteRecordRaw[]>
  pageData: PageDataCodec<PageData>
  setup?: (context: GossrSetupContext<PageData>) => void
}

export interface GossrAppDefinition<PageData> {
  readonly appId: string
  readonly root: Component
  readonly routes: Readonly<RouteRecordRaw[]>
  readonly pageData: PageDataCodec<PageData>
  readonly setup?: (context: GossrSetupContext<PageData>) => void
}

export interface SSRRenderInput {
  url: string
  snapshot: unknown
}

export interface SSRRenderResult {
  html: string
  head?: string
}
