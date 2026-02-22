import { ref } from 'vue'
import type { Ref } from 'vue'

export type SupportedLocale = 'en' | 'zh'

export const availableLocales: SupportedLocale[] = ['en', 'zh']
export const defaultLocale: SupportedLocale = 'en'

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
