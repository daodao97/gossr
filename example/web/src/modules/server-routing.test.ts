import { describe, expect, it } from 'vitest'

import { createAppRouter, resolveServerRenderURL } from '../main'

describe('server auth routing', () => {
  it('keeps public routes on the fast path', () => {
    const router = createAppRouter()
    expect(resolveServerRenderURL(router, '/seo-demo?title=SSR', {})).toBe('/seo-demo?title=SSR')
  })

  it('redirects every static protected route form for anonymous requests', () => {
    const router = createAppRouter()
    expect(resolveServerRenderURL(router, '/protected', {})).toBe('/session-demo?next=/protected')
    expect(resolveServerRenderURL(router, '/protected/', {})).toBe('/session-demo?next=/protected/')
    expect(resolveServerRenderURL(router, '/zh/protected?tab=profile', {}))
      .toBe('/zh/session-demo?next=/zh/protected?tab=profile')
  })

  it('does not redirect an authenticated request', () => {
    const router = createAppRouter()
    const state = { session: { user: { email: 'user@example.com' } } }
    expect(resolveServerRenderURL(router, '/protected?tab=profile', state))
      .toBe('/protected?tab=profile')
  })

  it('invalidates the policy when routes are added and removed', () => {
    const router = createAppRouter()

    // Build the initial policy before mutating the router.
    expect(resolveServerRenderURL(router, '/dynamic-protected', {})).toBe('/dynamic-protected')

    const remove = router.addRoute({
      path: '/dynamic-protected',
      name: 'dynamic-protected',
      component: { template: '<div />' },
      meta: { requiresAuth: true },
    })
    expect(resolveServerRenderURL(router, '/dynamic-protected', {}))
      .toBe('/session-demo?next=/dynamic-protected')

    remove()
    expect(resolveServerRenderURL(router, '/dynamic-protected', {})).toBe('/dynamic-protected')
  })

  it('invalidates the policy when a named route is removed', () => {
    const router = createAppRouter()
    router.addRoute({
      path: '/temporary-protected',
      name: 'temporary-protected',
      component: { template: '<div />' },
      meta: { requiresAuth: true },
    })
    expect(resolveServerRenderURL(router, '/temporary-protected', {}))
      .toBe('/session-demo?next=/temporary-protected')

    router.removeRoute('temporary-protected')
    expect(resolveServerRenderURL(router, '/temporary-protected', {})).toBe('/temporary-protected')
  })

  it('supports parent addRoute overloads and clearRoutes invalidation', () => {
    const router = createAppRouter()
    router.addRoute({
      path: '/dynamic-parent',
      name: 'dynamic-parent',
      component: { template: '<router-view />' },
    })
    router.addRoute('dynamic-parent', {
      path: 'protected-child',
      name: 'dynamic-protected-child',
      component: { template: '<div />' },
      meta: { requiresAuth: true },
    })
    expect(resolveServerRenderURL(router, '/dynamic-parent/protected-child', {}))
      .toBe('/session-demo?next=/dynamic-parent/protected-child')

    router.clearRoutes()
    expect(resolveServerRenderURL(router, '/dynamic-parent/protected-child', {}))
      .toBe('/dynamic-parent/protected-child')
  })
})
