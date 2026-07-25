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
            // 保留浏览器访问 Vite 的 Host，使后端同源校验继续以
            // http://127.0.0.1:3333 为公开 origin；只改变连接目标。
            changeOrigin: false,
          },
          '/demo': {
            target: 'http://127.0.0.1:8080',
            changeOrigin: false,
          },
        },
      },
    },
  ),
)
