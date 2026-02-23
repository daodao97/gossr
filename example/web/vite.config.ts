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
      '/__ssr_fetch': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    target: 'es2020',
  },
}))
