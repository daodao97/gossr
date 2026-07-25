import { fileURLToPath, URL } from 'node:url'

import { defineConfig, mergeConfig } from 'vite'

import { gossrVuePreset } from '@daodao97/gossr-vue/vite'

export default defineConfig(() =>
  mergeConfig(
    gossrVuePreset({
      sourceDir: fileURLToPath(new URL('./src', import.meta.url)),
    }),
    {
      server: {
        host: '127.0.0.1',
        port: 3333,
        proxy: {
          '/_ssr/data': {
            target: 'http://127.0.0.1:8080',
            // Keep the public Vite origin for gossr's same-origin check.
            changeOrigin: false,
          },
        },
      },
    },
  ),
)
