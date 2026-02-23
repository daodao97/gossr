import { ref } from 'vue'
import type { Ref } from 'vue'
import en from '~/locales/en.json'
import zh from '~/locales/zh.json'

export type SupportedLocale = 'en' | 'zh'

export const availableLocales: SupportedLocale[] = ['en', 'zh']
export const defaultLocale: SupportedLocale = 'en'

type MessageDictionary = typeof en
const zhMessages: MessageDictionary = zh
const messages: Record<SupportedLocale, MessageDictionary> = {
  en,
  zh: zhMessages,
}

export type MessageKey = keyof MessageDictionary

export interface MessageParams {
  [key: string]: string | number
}

export interface I18nInstance {
  global: {
    locale: Ref<SupportedLocale>
  }
}

export function createI18nInstance(initialLocale: SupportedLocale = defaultLocale): I18nInstance {
  const locale = ref(isSupportedLocale(initialLocale) ? initialLocale : defaultLocale)
  return {
    global: {
      locale,
    },
  }
}

export function isSupportedLocale(locale: unknown): locale is SupportedLocale {
  return typeof locale === 'string' && availableLocales.includes(locale as SupportedLocale)
}

export function getLocaleRef(i18n: I18nInstance): Ref<SupportedLocale> {
  return i18n.global.locale
}

export function localeFromPath(pathname: string): SupportedLocale {
  const trimmed = pathname.replace(/^\/+/, '')
  const firstSegment = trimmed.split('/')[0]
  if (isSupportedLocale(firstSegment))
    return firstSegment
  return defaultLocale
}

export function translate(locale: SupportedLocale, key: MessageKey, params: MessageParams = {}): string {
  const dict = messages[locale] ?? messages[defaultLocale]
  const template = dict[key] ?? messages[defaultLocale][key] ?? key
  return template.replace(/\{(\w+)\}/g, (_, name: string) => {
    const value = params[name]
    return value === undefined ? '' : String(value)
  })
}
