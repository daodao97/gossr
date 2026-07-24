import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

import { gossrGojaSsrPreset, gossrVuePreset } from './index'

describe('gossrVuePreset', () => {
  it('anchors consumer aliases and generated routes to the supplied source directory', () => {
    const sourceDir = resolve('/consumer', 'src')
    const config = gossrVuePreset({ sourceDir })

    expect(config.resolve).toMatchObject({
      alias: {
        '~': sourceDir,
      },
    })
    expect(config.plugins).toHaveLength(2)
  })

  it('rejects a relative consumer source directory', () => {
    expect(() => gossrVuePreset({ sourceDir: 'src' }))
      .toThrow('sourceDir must be an absolute path')
  })
})

describe('gossrGojaSsrPreset', () => {
  it('uses an absolute consumer entry for the server bundle', () => {
    const entry = resolve('/consumer', 'scripts/entry-server.ts')
    const config = gossrGojaSsrPreset({ entry })

    expect(config.build).toMatchObject({
      rollupOptions: {
        input: {
          server: entry,
        },
      },
    })
  })

  it('rejects a relative server entry', () => {
    expect(() => gossrGojaSsrPreset({ entry: 'scripts/entry-server.ts' }))
      .toThrow('entry must be an absolute path')
  })
})
