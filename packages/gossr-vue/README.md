# @daodao97/gossr-vue

Vue SSR runtime and Vite presets for gossr. During the Git-dependency rollout,
pin a full commit and select this package directory:

```json
{
  "dependencies": {
    "@daodao97/gossr-vue": "github:daodao97/gossr#<commit>&path:/packages/gossr-vue"
  }
}
```

Application code does not create routers, histories, navigation fetchers, SSR
apps, or hydration flows directly.

The application surface is one `defineGossrApp()` call:

```ts
import { defineGossrApp } from '@daodao97/gossr-vue'

export default defineGossrApp({
  appId: 'my-app',
  root: App,
  routes,
  document: {
    parse: parsePageDocument,
    url: document => document.url,
  },
  setup({ app, router, navigation, onDispose }) {
    // Install application providers and guards.
    onDispose(() => {
      // Release request/client-owned business resources.
    })
  },
})
```

Client and server entries stay as thin adapters:

```ts
import { bootstrapClient } from '@daodao97/gossr-vue/client'
import { installGojaRenderABI } from '@daodao97/gossr-vue/server'
```

## Invariants

- Every SSR request owns a fresh Vue app, router, history, navigation
  coordinator, and business scope.
- `setup()` and registered disposers are synchronous. Returning a thenable is a
  controlled error; use Vue lifecycle hooks or an application service for
  asynchronous work.
- Page documents enter through the application codec exactly once. Their URL
  must match the strict same-origin target used by the router.
- Vue Router `fullPath` values cross `documentURLFromRouter()` before they
  reach the document/data boundary. Browser-only fragments remain available to
  the router but never weaken strict wire URL validation.
- Route records are cloned recursively with `sensitive: true`, including
  nested children. This matches Go's exact route casing without requiring
  per-page configuration. Vue Router's default `strict: false` remains intact,
  so one trailing slash continues to resolve to the same route.
- Navigation uses the gossr `render | redirect | error` envelope. The Vue
  renderer returns only `html` and `head`; HTTP status and redirects belong to
  the Go resolver.
- Hydration requires both the server marker and a valid boot document for the
  current URL. Any mismatch falls back to a cold mount.
- Cleanup order is Vue unmount, business disposers, framework hook
  registration, navigation coordinator, then router history.

Build-specific Vue and Goja settings are exported from
`@daodao97/gossr-vue/vite`. Both presets require consumer-owned absolute paths,
so aliases and entries can never accidentally resolve inside this package.
Product-specific plugins and dev-server behavior remain in the consuming
application's thin Vite config files.
