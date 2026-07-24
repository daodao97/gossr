import type { GossrAppDefinition, GossrAppOptions } from './types'

const appIDPattern = /^[a-z\d](?:[a-z\d._-]{0,62}[a-z\d])?$/i

export function defineGossrApp<Document>(
  options: GossrAppOptions<Document>,
): GossrAppDefinition<Document> {
  if (!appIDPattern.test(options.appId))
    throw new Error('gossr appId must be 1-64 URL/storage-safe characters')
  if (!options.root)
    throw new Error('gossr root component is required')
  if (!Array.isArray(options.routes))
    throw new Error('gossr routes must be an array')
  if (
    !options.document
    || typeof options.document.parse !== 'function'
    || typeof options.document.url !== 'function'
  ) {
    throw new Error('gossr document codec must provide parse() and url()')
  }
  if (options.setup !== undefined && typeof options.setup !== 'function')
    throw new Error('gossr setup must be a function')

  return Object.freeze({
    appId: options.appId,
    root: options.root,
    routes: options.routes,
    document: options.document,
    setup: options.setup,
  })
}
