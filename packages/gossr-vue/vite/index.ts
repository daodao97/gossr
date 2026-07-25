import { createRequire } from 'node:module'
import { dirname, isAbsolute, resolve } from 'node:path'

import vue from '@vitejs/plugin-vue'
import type { UserConfig } from 'vite'
import vueRouter from 'vue-router/vite'

const require = createRequire(import.meta.url)
const GOJA_BUILD_TARGET = 'es2020'

function packageFile(packageName: string, relativePath: string): string {
  return resolve(dirname(require.resolve(`${packageName}/package.json`)), relativePath)
}

/**
 * Vue conventions shared by the browser and SSR builds.
 *
 * Application Vite configs should layer product-specific plugins, aliases and
 * dev-server behavior on top of this preset.
 */
export interface GossrVuePresetOptions {
  sourceDir: string
  routesDir?: string
  typedRoutesFile?: string
}

export function gossrVuePreset(options: GossrVuePresetOptions): UserConfig {
  const sourceDir = requireAbsolutePath(options.sourceDir, 'sourceDir')
  const routesFolder = options.routesDir === undefined
    ? resolve(sourceDir, 'pages')
    : requireAbsolutePath(options.routesDir, 'routesDir')
  const dts = options.typedRoutesFile === undefined
    ? resolve(sourceDir, 'typed-router.d.ts')
    : requireAbsolutePath(options.typedRoutesFile, 'typedRoutesFile')

  return {
    plugins: [
      vueRouter({
        routesFolder,
        routeBlockLang: 'json',
        dts,
      }),
      vue(),
    ],
    resolve: {
      alias: {
        '~': sourceDir,
      },
      // 当 gossr-vue 通过 workspace/file 链接安装时,它与应用可能各带一份
      // vue/vue-router。两份 vue-router 的 injection key 不相等,SSR 会静默
      // 渲染出空文档,dedupe 保证全局单实例。
      dedupe: ['vue', 'vue-router', '@vue/server-renderer'],
    },
    build: {
      target: GOJA_BUILD_TARGET,
    },
    define: {
      // Production Vue normally strips the node-level hydration mismatch detail.
      // The debug bundle keeps it without leaking that switch into application config.
      __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: JSON.stringify(
        process.env.VUE_HYDRATION_DEBUG === '1',
      ),
    },
  }
}

export interface GossrGojaSsrPresetOptions {
  entry: string
  entryName?: string
}

/**
 * Produces one self-contained CommonJS file that can execute inside Goja.
 */
export function gossrGojaSsrPreset(
  options: GossrGojaSsrPresetOptions,
): UserConfig {
  const entry = requireAbsolutePath(options.entry, 'entry')
  const entryName = options.entryName ?? 'server'

  return {
    define: {
      'process.env.NODE_ENV': JSON.stringify('production'),
      global: 'globalThis',
    },
    // Goja has no Node module loader, so every SSR runtime dependency must be
    // bundled. These aliases also force Vue's real SSR-capable ESM builds.
    ssr: {
      noExternal: true,
    },
    resolve: {
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
      target: GOJA_BUILD_TARGET,
      minify: 'oxc',
      rollupOptions: {
        input: {
          [entryName]: entry,
        },
        output: {
          format: 'cjs',
          entryFileNames: '[name].js',
          codeSplitting: false,
        },
      },
    },
  }
}

function requireAbsolutePath(value: string, name: string) {
  if (!isAbsolute(value))
    throw new Error(`gossr Vue preset ${name} must be an absolute path`)
  return value
}
