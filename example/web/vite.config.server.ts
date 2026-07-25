import { fileURLToPath, URL } from 'node:url'

import { defineConfig, mergeConfig } from 'vite'

import { gossrGojaSsrPreset } from '@daodao97/gossr-vue/vite'

import baseConfig from './vite.config'

export default defineConfig((env) => {
  const resolvedBaseConfig = typeof baseConfig === 'function' ? baseConfig(env) : baseConfig

  return mergeConfig(
    resolvedBaseConfig,
    gossrGojaSsrPreset({
      entry: fileURLToPath(new URL('./scripts/entry-server.ts', import.meta.url)),
    }),
  )
})
