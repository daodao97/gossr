/** @vitest-environment happy-dom */

import { createSSRApp, h } from 'vue'
import { describe, expect, it } from 'vitest'

describe('Vue SSR hydration contract', () => {
  it('reuses an existing SSR element instead of remounting it', () => {
    document.body.innerHTML = '<div id="app"><main id="content">SSR content</main></div>'
    const serverElement = document.querySelector('#content')
    const app = createSSRApp({
      render: () => h('main', { id: 'content' }, 'SSR content'),
    })

    app.mount('#app')
    expect(document.querySelector('#content')).toBe(serverElement)
    app.unmount()
  })
})
