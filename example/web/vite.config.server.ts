import { defineConfig, mergeConfig } from 'vite'

import baseConfig from './vite.config'

export default defineConfig((env) => {
  const resolvedBaseConfig = typeof baseConfig === 'function' ? baseConfig(env) : baseConfig

  return mergeConfig(resolvedBaseConfig, {
    build: {
      target: 'es2020',
      rollupOptions: {
        input: {
          server: 'src/entry-server.ts',
        },
        output: {
          format: 'cjs',
          entryFileNames: '[name].js',
          inlineDynamicImports: true,
        },
      },
    },
  })
})
