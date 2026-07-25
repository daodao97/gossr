import type { App, Component, Ref } from 'vue'
import type { RouteRecordRaw, Router } from 'vue-router'

export type GossrPlatform = 'server' | 'client'

export interface DocumentCodec<Document> {
  parse: (value: unknown) => Document
  url: (document: Document) => string
}

export type NavigationOutcome<Document> =
  | {
    kind: 'render'
    status: number
    snapshot: Document
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

export interface NavigationCoordinator<Document> {
  /**
   * The last committed page document. During a client navigation it keeps
   * pointing at the previous page (stale-while-loading) so layout chrome
   * such as the viewer never flickers; page content should gate on `ready`.
   */
  current: Readonly<Ref<Document | undefined>>
  /** True when `current` is the document for the active route. */
  ready: Readonly<Ref<boolean>>
  loading: Readonly<Ref<boolean>>
  error: Readonly<Ref<Error | null>>
  refresh: () => Promise<boolean>
}

export interface GossrSetupContext<Document> {
  app: App
  router: Router
  navigation: NavigationCoordinator<Document>
  platform: GossrPlatform
  onDispose: (disposer: () => void) => void
}

export interface GossrAppOptions<Document> {
  appId: string
  root: Component
  routes: Readonly<RouteRecordRaw[]>
  document: DocumentCodec<Document>
  setup?: (context: GossrSetupContext<Document>) => void
}

export interface GossrAppDefinition<Document> {
  readonly appId: string
  readonly root: Component
  readonly routes: Readonly<RouteRecordRaw[]>
  readonly document: DocumentCodec<Document>
  readonly setup?: (context: GossrSetupContext<Document>) => void
}

export interface SSRRenderInput {
  url: string
  snapshot: unknown
}

export interface SSRRenderResult {
  html: string
  head?: string
}
