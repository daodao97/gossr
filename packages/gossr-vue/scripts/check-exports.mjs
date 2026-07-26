const [
  runtime,
  client,
  server,
  testing,
  vite,
] = await Promise.all([
  import('@daodao97/gossr-vue'),
  import('@daodao97/gossr-vue/client'),
  import('@daodao97/gossr-vue/server'),
  import('@daodao97/gossr-vue/testing'),
  import('@daodao97/gossr-vue/vite'),
])

const expectedFunctions = [
  [runtime, 'defineGossrApp'],
  [client, 'bootstrapClient'],
  [server, 'installGojaRenderABI'],
  [testing, 'parsePageData'],
  [vite, 'gossrVuePreset'],
  [vite, 'gossrGojaSsrPreset'],
]

for (const [module, name] of expectedFunctions) {
  if (typeof module[name] !== 'function')
    throw new Error(`package export ${name} is unavailable`)
}
