import { fileURLToPath, URL } from 'node:url'

import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import vueRouter from 'vue-router/vite'

export default defineConfig(({ command }) => ({
  plugins: [
    vueRouter({
      routesFolder: 'src/pages',
      dts: 'src/typed-router.d.ts',
      watch: command === 'serve',
    }),
    vue(),
  ],
  resolve: {
    alias: {
      '~': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
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
    },
  },
  build: {
    target: 'es2020',
  },
}))
