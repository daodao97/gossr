import { createRequire } from 'node:module'
import { dirname, resolve } from 'node:path'

import { defineConfig, mergeConfig } from 'vite'

import baseConfig from './vite.config'

const require = createRequire(import.meta.url)

function packageFile(packageName: string, relativePath: string): string {
  return resolve(dirname(require.resolve(`${packageName}/package.json`)), relativePath)
}

export default defineConfig((env) => {
  const resolvedBaseConfig = typeof baseConfig === 'function' ? baseConfig(env) : baseConfig

  return mergeConfig(resolvedBaseConfig, {
    define: {
      'process.env.NODE_ENV': JSON.stringify('production'),
    },
    // 让 @vitejs/plugin-vue 生成 SSR 专用的 ssrRender 函数，避免在 Goja
    // 中为每个节点构建客户端虚拟 DOM。Goja 没有 Node require，因此依赖必须内联。
    ssr: {
      noExternal: true,
    },
    resolve: {
      // SSR mode normally selects Vue's Node CJS build, which imports node:stream.
      // Use the bundler ESM build so the generated bundle remains self-contained
      // and executable in Goja while still sharing the same bundled Vue runtime.
      alias: [
        {
          find: '@vue/server-renderer',
          replacement: packageFile(
            '@vue/server-renderer',
            'dist/server-renderer.esm-bundler.js',
          ),
        },
        {
          find: /^vue$/,
          replacement: packageFile('vue', 'dist/vue.runtime.esm-bundler.js'),
        },
      ],
    },
    build: {
      ssr: true,
      target: 'es2020',
      minify: 'oxc',
      rollupOptions: {
        input: {
          server: 'src/entry-server.ts',
        },
        output: {
          format: 'cjs',
          entryFileNames: '[name].js',
          codeSplitting: false,
        },
      },
    },
  })
})
