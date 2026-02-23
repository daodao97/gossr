import { ref } from 'vue'
import type { Ref } from 'vue'

type MessageDictionary = Record<string, string>

const localeModules = import.meta.glob('../locales/*.json', {
  eager: true,
  import: 'default',
}) as Record<string, MessageDictionary>

function localeCodeFromPath(filePath: string): string {
  const fileName = filePath.split('/').pop() ?? ''
  return fileName.replace(/\.json$/i, '').trim().toLowerCase()
}

function buildMessages(): Record<string, MessageDictionary> {
  const result: Record<string, MessageDictionary> = {}
  for (const [filePath, dictionary] of Object.entries(localeModules)) {
    const locale = localeCodeFromPath(filePath)
    if (!locale)
      continue

    result[locale] = dictionary
  }
  return result
}

function resolveDefaultLocale(locales: string[]): string {
  if (locales.includes('en'))
    return 'en'
  if (locales.length > 0)
    return locales[0]
  return 'en'
}

function normalizeLocale(locale: string): string {
  return locale.trim().toLowerCase()
}

const messages = buildMessages()

export type SupportedLocale = string
export const availableLocales: SupportedLocale[] = Object.keys(messages).sort()
export const defaultLocale: SupportedLocale = resolveDefaultLocale(availableLocales)

const fallbackMessages: MessageDictionary = messages[defaultLocale] ?? {}

export type MessageKey = string

export interface MessageParams {
  [key: string]: string | number
}

export interface I18nInstance {
  global: {
    locale: Ref<SupportedLocale>
  }
}

export function createI18nInstance(initialLocale: SupportedLocale = defaultLocale): I18nInstance {
  const normalizedInitial = normalizeLocale(initialLocale)
  const locale = ref(isSupportedLocale(normalizedInitial) ? normalizedInitial : defaultLocale)
  return {
    global: {
      locale,
    },
  }
}

export function isSupportedLocale(locale: unknown): locale is SupportedLocale {
  if (typeof locale !== 'string')
    return false

  return availableLocales.includes(normalizeLocale(locale))
}

export function getLocaleRef(i18n: I18nInstance): Ref<SupportedLocale> {
  return i18n.global.locale
}

export function localeFromPath(pathname: string): SupportedLocale {
  const trimmed = pathname.replace(/^\/+/, '')
  const firstSegment = trimmed.split('/')[0]
  const normalized = normalizeLocale(firstSegment)
  if (isSupportedLocale(normalized))
    return normalized

  return defaultLocale
}

export function translate(locale: SupportedLocale, key: MessageKey, params: MessageParams = {}): string {
  const normalizedLocale = normalizeLocale(locale)
  const dict = messages[normalizedLocale] ?? fallbackMessages
  const template = dict[key] ?? fallbackMessages[key] ?? key
  return template.replace(/\{(\w+)\}/g, (_, name: string) => {
    const value = params[name]
    return value === undefined ? '' : String(value)
  })
}
