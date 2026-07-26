#!/usr/bin/env node
// gossr-build: gossr 宿主的标准前端构建管线。
//
//   gossr-build [build] [--debug] [--smoke-snapshot p] [--smoke-expect s]...
//   gossr-build verify [--debug]
//   gossr-build smoke  [--smoke-snapshot p] [--smoke-expect s]...
//
// build 流程:临时目录 staging → client/server 双 Vite 构建 → manifest +
// 内容摘要 → 产物体检 → goja 渲染冒烟 → 原子发布(失败回滚) → 复验。
// 在宿主 web 目录(cwd)内运行;冒烟经 `go run github.com/daodao97/gossr/cmd/gossr-smoke`
// 执行,要求 cwd 位于依赖 gossr 的 Go module 内(版本随宿主 go.mod 锁定)。
import { createHash } from 'node:crypto'
import { spawn } from 'node:child_process'
import {
  mkdir,
  mkdtemp,
  open,
  readFile,
  readdir,
  rename,
  rm,
  stat,
  writeFile,
} from 'node:fs/promises'
import { isAbsolute, relative, resolve, sep } from 'node:path'
import process from 'node:process'

const WEB_ROOT = process.cwd()
const DIST_ROOT = resolve(WEB_ROOT, 'dist')
const WORK_ROOT = resolve(WEB_ROOT, 'node_modules/.cache/gossr-web-bundle')
const LOCK_PATH = resolve(WORK_ROOT, 'build.lock')

const SCHEMA_VERSION = 2
const BUNDLE_VARIANTS = new Set(['production', 'debug'])
const SSR_SMOKE_TIMEOUT_MS = 30_000
const SSR_SMOKE_FORCE_KILL_MS = 5_000
// 输入摘要覆盖会影响产物的宿主源码;不存在的条目自动跳过。
const INPUT_FILES = [
  'index.html',
  'package.json',
  'pnpm-lock.yaml',
  'pnpm-workspace.yaml',
  'tsconfig.json',
  'vite.config.ts',
  'vite.config.server.ts',
]
const INPUT_DIRS = ['public', 'scripts', 'src', 'testdata']

function parseArgs(argv) {
  const options = {
    command: 'build',
    debug: false,
    smokeSnapshot: '',
    smokeExpects: [],
    smokeURL: '/__gossr_smoke__',
    smokeBinary: '',
  }
  const rest = [...argv]
  if (rest[0] && !rest[0].startsWith('--')) {
    options.command = rest.shift()
    if (!['build', 'verify', 'smoke'].includes(options.command))
      throw new Error(`unknown gossr-build command: ${options.command}`)
  }
  while (rest.length > 0) {
    const argument = rest.shift()
    const take = () => {
      const value = rest.shift()
      if (value === undefined || value.startsWith('--'))
        throw new Error(`${argument} requires a value`)
      return value
    }
    if (argument === '--debug')
      options.debug = true
    else if (argument === '--smoke-snapshot')
      options.smokeSnapshot = take()
    else if (argument === '--smoke-expect')
      options.smokeExpects.push(take())
    else if (argument === '--smoke-url')
      options.smokeURL = take()
    else if (argument === '--smoke-bin')
      options.smokeBinary = take()
    else
      throw new Error(`unknown gossr-build option: ${argument}`)
  }
  if (options.smokeBinary && !isAbsolute(options.smokeBinary))
    throw new Error('--smoke-bin requires an absolute executable path')
  return options
}

function toPosix(path) {
  return path.split(sep).join('/')
}

async function exists(path) {
  try {
    await stat(path)
    return true
  }
  catch {
    return false
  }
}

async function filesBelow(root) {
  const result = []

  async function visit(directory) {
    const entries = await readdir(directory, { withFileTypes: true })
    entries.sort((left, right) => left.name.localeCompare(right.name))
    for (const entry of entries) {
      const path = resolve(directory, entry.name)
      if (entry.isDirectory())
        await visit(path)
      else if (entry.isFile())
        result.push(path)
    }
  }

  await visit(root)
  return result
}

async function digestFiles(root, files) {
  const hash = createHash('sha256')
  for (const path of files.sort()) {
    hash.update(toPosix(relative(root, path)))
    hash.update('\0')
    hash.update(await readFile(path))
    hash.update('\0')
  }
  return hash.digest('hex')
}

function requireBundleVariant(variant) {
  if (!BUNDLE_VARIANTS.has(variant))
    throw new Error(`unsupported web bundle variant: ${variant}`)
  return variant
}

async function computeInputDigest(variant) {
  requireBundleVariant(variant)
  const files = []
  for (const file of INPUT_FILES) {
    const path = resolve(WEB_ROOT, file)
    if (await exists(path))
      files.push(path)
  }
  for (const directory of INPUT_DIRS) {
    const path = resolve(WEB_ROOT, directory)
    if (await exists(path))
      files.push(...await filesBelow(path))
  }
  const sourceDigest = await digestFiles(WEB_ROOT, files)
  return createHash('sha256')
    .update(`gossr-web-bundle:${variant}\0${sourceDigest}`)
    .digest('hex')
}

async function treeDigest(root) {
  return await digestFiles(root, await filesBelow(root))
}

async function requireFile(path, label) {
  let info
  try {
    info = await stat(path)
  }
  catch {
    throw new Error(`${label} is missing: ${path}`)
  }
  if (!info.isFile() || info.size === 0)
    throw new Error(`${label} is empty: ${path}`)
}

async function verifyClientAssets(bundleRoot, indexHTML) {
  const clientRoot = resolve(bundleRoot, 'client')
  const references = indexHTML.matchAll(/(?:src|href)="([^"]+)"/g)
  for (const [, rawReference] of references) {
    if (/^(?:[a-z]+:|\/\/|#|data:)/i.test(rawReference))
      continue
    const reference = rawReference.split(/[?#]/, 1)[0].replace(/^\/+/, '')
    if (!reference)
      continue
    await requireFile(resolve(clientRoot, reference), `client asset ${rawReference}`)
  }
}

function verifyServerSource(source) {
  if (!source.includes('ssrRender') || !source.includes('__GOSSR_RENDER_ABI__'))
    throw new Error('server bundle does not expose the gossr render ABI')
  if (!source.includes('__ssrInlineRender'))
    throw new Error('server bundle is not a true Vue SSR compilation')
  if (source.slice(0, 1024).includes('Object.defineProperty(exports'))
    throw new Error('server bundle expects a CommonJS exports global')
  if (/\brequire\s*\(/.test(source))
    throw new Error('server bundle contains Node require() calls')
  if (/\bBuffer\b/.test(source))
    throw new Error('server bundle contains the Node Buffer global')
  if (/\bglobal\./.test(source))
    throw new Error('server bundle contains the Node global object')
}

async function writeBundleManifest(bundleRoot, variant) {
  requireBundleVariant(variant)
  const inputDigest = await computeInputDigest(variant)
  const clientDigest = await treeDigest(resolve(bundleRoot, 'client'))
  const serverDigest = await treeDigest(resolve(bundleRoot, 'server'))
  const artifactDigest = createHash('sha256')
    .update([
      `schema:${SCHEMA_VERSION}`,
      `variant:${variant}`,
      `input:${inputDigest}`,
      `client:${clientDigest}`,
      `server:${serverDigest}`,
    ].join('\0'))
    .digest('hex')
  const manifest = {
    schemaVersion: SCHEMA_VERSION,
    variant,
    buildId: artifactDigest.slice(0, 16),
    inputDigest,
    client: { entry: 'client/index.html', digest: clientDigest },
    server: { entry: 'server/server.js', digest: serverDigest },
  }
  await writeFile(
    resolve(bundleRoot, 'bundle.json'),
    `${JSON.stringify(manifest, null, 2)}\n`,
  )
  return manifest
}

async function verifyBundle(bundleRoot, expectedVariant) {
  requireBundleVariant(expectedVariant)
  const manifestPath = resolve(bundleRoot, 'bundle.json')
  const clientEntry = resolve(bundleRoot, 'client/index.html')
  const serverEntry = resolve(bundleRoot, 'server/server.js')

  await requireFile(manifestPath, 'bundle manifest')
  await requireFile(clientEntry, 'client entry')
  await requireFile(serverEntry, 'server entry')

  const manifest = JSON.parse(await readFile(manifestPath, 'utf8'))
  if (manifest.schemaVersion !== SCHEMA_VERSION)
    throw new Error(`unsupported bundle schema: ${manifest.schemaVersion}`)
  if (manifest.variant !== expectedVariant) {
    throw new Error(
      `web bundle variant is ${manifest.variant ?? 'unknown'}, expected ${expectedVariant}`,
    )
  }

  const inputDigest = await computeInputDigest(expectedVariant)
  if (manifest.inputDigest !== inputDigest)
    throw new Error('web sources changed after this bundle was built')

  const clientDigest = await treeDigest(resolve(bundleRoot, 'client'))
  const serverDigest = await treeDigest(resolve(bundleRoot, 'server'))
  if (manifest.client?.entry !== 'client/index.html' || manifest.client?.digest !== clientDigest)
    throw new Error('client bundle does not match bundle.json')
  if (manifest.server?.entry !== 'server/server.js' || manifest.server?.digest !== serverDigest)
    throw new Error('server bundle does not match bundle.json')
  const artifactDigest = createHash('sha256')
    .update([
      `schema:${SCHEMA_VERSION}`,
      `variant:${expectedVariant}`,
      `input:${inputDigest}`,
      `client:${clientDigest}`,
      `server:${serverDigest}`,
    ].join('\0'))
    .digest('hex')
  if (manifest.buildId !== artifactDigest.slice(0, 16))
    throw new Error('web bundle build ID does not match its artifacts')

  const indexHTML = await readFile(clientEntry, 'utf8')
  await verifyClientAssets(bundleRoot, indexHTML)

  const serverSource = await readFile(serverEntry, 'utf8')
  verifyServerSource(serverSource)

  const serverFiles = await filesBelow(resolve(bundleRoot, 'server'))
  const browserAssets = serverFiles.filter(path => /\.(?:css|woff2?|ttf|otf)$/i.test(path))
  if (browserAssets.length > 0)
    throw new Error(`server bundle contains browser-only assets: ${browserAssets.join(', ')}`)

  return manifest
}

function killChildProcess(child, signal) {
  if (child.exitCode !== null || child.signalCode !== null)
    return
  if (process.platform === 'win32') {
    child.kill()
    return
  }
  try {
    // 冒烟可能经 `go run` 启动;按进程组终止,避免编译出的子进程逃逸。
    process.kill(-child.pid, signal)
  }
  catch {
    child.kill(signal)
  }
}

function runSSRBundleSmoke(serverEntry, options) {
  if (!options.smokeSnapshot)
    throw new Error('--smoke-snapshot is required (JSON snapshot for ssrRender)')
  const command = options.smokeBinary
    || (process.platform === 'win32' ? 'go.exe' : 'go')
  const smokeArgs = [
    '-bundle', serverEntry,
    '-snapshot', resolve(WEB_ROOT, options.smokeSnapshot),
    '-url', options.smokeURL,
  ]
  for (const expected of options.smokeExpects)
    smokeArgs.push('-expect', expected)
  const args = options.smokeBinary
    ? smokeArgs
    : ['run', 'github.com/daodao97/gossr/cmd/gossr-smoke', ...smokeArgs]
  const childEnv = { ...process.env, GOJA_POOL_SIZE: '1' }

  return new Promise((resolvePromise, reject) => {
    const child = spawn(command, args, {
      cwd: WEB_ROOT,
      env: childEnv,
      stdio: 'inherit',
      detached: process.platform !== 'win32',
    })
    let timedOut = false
    let forceKillTimer

    const timeout = setTimeout(() => {
      timedOut = true
      killChildProcess(child, 'SIGTERM')
      forceKillTimer = setTimeout(
        () => killChildProcess(child, 'SIGKILL'),
        SSR_SMOKE_FORCE_KILL_MS,
      )
      forceKillTimer.unref()
    }, SSR_SMOKE_TIMEOUT_MS)
    timeout.unref()

    const clearTimers = () => {
      clearTimeout(timeout)
      if (forceKillTimer)
        clearTimeout(forceKillTimer)
    }

    child.once('error', (error) => {
      clearTimers()
      reject(error)
    })
    child.once('exit', (code, signal) => {
      clearTimers()
      if (timedOut)
        reject(new Error(`staged SSR smoke exceeded ${SSR_SMOKE_TIMEOUT_MS / 1000}s and was terminated`))
      else if (code === 0)
        resolvePromise()
      else
        reject(new Error(`staged SSR smoke failed (${signal ?? `exit ${code}`})`))
    })
  })
}

function processIsRunning(pid) {
  if (!Number.isInteger(pid) || pid <= 0)
    return false
  try {
    process.kill(pid, 0)
    return true
  }
  catch (error) {
    return error?.code === 'EPERM'
  }
}

async function acquireLock() {
  await mkdir(WORK_ROOT, { recursive: true })
  for (let attempt = 0; attempt < 2; attempt += 1) {
    try {
      const handle = await open(LOCK_PATH, 'wx')
      await handle.writeFile(JSON.stringify({ pid: process.pid, startedAt: Date.now() }))
      await handle.close()
      return
    }
    catch (error) {
      if (error?.code !== 'EEXIST')
        throw error
      let owner
      try {
        owner = JSON.parse(await readFile(LOCK_PATH, 'utf8'))
      }
      catch {
        owner = null
      }
      if (processIsRunning(owner?.pid))
        throw new Error(`another web bundle build is running (pid ${owner.pid})`)
      await rm(LOCK_PATH, { force: true })
    }
  }
  throw new Error('could not acquire the web bundle build lock')
}

function runVite(args, debug) {
  const command = process.platform === 'win32' ? 'pnpm.cmd' : 'pnpm'
  const childEnv = {
    ...process.env,
    // 两个变体都是生产 Vue 构建;variant 只控制水合诊断。
    NODE_ENV: 'production',
    // 文件路由插件在一次性构建里也会装 watcher;轮询避免 CI/macOS 的
    // 平台 watcher 耗尽。
    CHOKIDAR_USEPOLLING: process.env.CHOKIDAR_USEPOLLING ?? '1',
    CHOKIDAR_INTERVAL: process.env.CHOKIDAR_INTERVAL ?? '1000',
    // 命令行 variant 是权威;环境变量不得把生产构建变成 debug。
    VUE_HYDRATION_DEBUG: debug ? '1' : '0',
  }

  return new Promise((resolvePromise, reject) => {
    const child = spawn(command, ['exec', 'vite', 'build', ...args], {
      cwd: WEB_ROOT,
      env: childEnv,
      stdio: 'inherit',
    })
    child.once('error', reject)
    child.once('exit', (code, signal) => {
      if (code === 0)
        resolvePromise()
      else
        reject(new Error(`Vite build failed (${signal ?? `exit ${code}`})`))
    })
  })
}

async function publish(stageRoot) {
  const backupRoot = resolve(WORK_ROOT, `backup-${process.pid}-${Date.now()}`)
  let previousBundle = false
  try {
    await rename(DIST_ROOT, backupRoot)
    previousBundle = true
  }
  catch (error) {
    if (error?.code !== 'ENOENT')
      throw error
  }
  try {
    await rename(stageRoot, DIST_ROOT)
  }
  catch (error) {
    if (previousBundle)
      await rename(backupRoot, DIST_ROOT)
    throw error
  }
  return { backupRoot, previousBundle }
}

async function finishPublication(publication) {
  if (publication.previousBundle)
    await rm(publication.backupRoot, { recursive: true, force: true })
}

async function rollbackPublication(publication) {
  const rejectedRoot = resolve(WORK_ROOT, `rejected-${process.pid}-${Date.now()}`)
  await rename(DIST_ROOT, rejectedRoot)
  if (publication.previousBundle) {
    try {
      await rename(publication.backupRoot, DIST_ROOT)
    }
    catch (error) {
      await rename(rejectedRoot, DIST_ROOT)
      throw error
    }
  }
  await rm(rejectedRoot, { recursive: true, force: true })
}

async function buildCommand(options) {
  const variant = options.debug ? 'debug' : 'production'
  await acquireLock()
  let stageRoot

  try {
    stageRoot = await mkdtemp(resolve(WORK_ROOT, 'stage-'))
    await runVite(['--outDir', resolve(stageRoot, 'client'), '--emptyOutDir'], options.debug)
    await runVite([
      '--config',
      'vite.config.server.ts',
      '--outDir',
      resolve(stageRoot, 'server'),
      '--emptyOutDir',
    ], options.debug)

    const manifest = await writeBundleManifest(stageRoot, variant)
    await verifyBundle(stageRoot, variant)
    await runSSRBundleSmoke(resolve(stageRoot, 'server/server.js'), options)
    const publication = await publish(stageRoot)

    try {
      await verifyBundle(DIST_ROOT, variant)
    }
    catch (error) {
      await rollbackPublication(publication)
      throw error
    }

    await finishPublication(publication)
    console.log(`web bundle ${manifest.buildId} built and verified`)
  }
  finally {
    if (stageRoot)
      await rm(stageRoot, { recursive: true, force: true })
    await rm(LOCK_PATH, { force: true })
  }
}

async function main() {
  const options = parseArgs(process.argv.slice(2))
  if (options.command === 'verify') {
    await verifyBundle(DIST_ROOT, options.debug ? 'debug' : 'production')
    console.log('web bundle verified')
    return
  }
  if (options.command === 'smoke') {
    await runSSRBundleSmoke(resolve(DIST_ROOT, 'server/server.js'), options)
    return
  }
  await buildCommand(options)
}

main().catch((error) => {
  console.error(error instanceof Error ? error.message : error)
  process.exitCode = 1
})
