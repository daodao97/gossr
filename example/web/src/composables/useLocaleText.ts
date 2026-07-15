import { computed, inject } from 'vue'
import type { ComputedRef, InjectionKey } from 'vue'
import { useRoute } from 'vue-router'
import type { Router } from 'vue-router'

import {
  localeFromPath,
  translate,
  type MessageKey,
  type MessageParams,
  type SupportedLocale,
} from '~/modules/i18n'

export interface LocaleTextContext {
  locale: ComputedRef<SupportedLocale>
  t: (key: MessageKey, params?: MessageParams) => string
}

export const localeTextKey: InjectionKey<LocaleTextContext> = Symbol('locale-text')

function createLocaleText(readPath: () => string): LocaleTextContext {
  const locale = computed<SupportedLocale>(() => localeFromPath(readPath()))
  return {
    locale,
    t: (key: MessageKey, params?: MessageParams) => translate(locale.value, key, params),
  }
}

export function createLocaleTextContext(router: Router): LocaleTextContext {
  return createLocaleText(() => router.currentRoute.value.path)
}

export function useLocaleText(): LocaleTextContext {
  const shared = inject(localeTextKey, null)
  if (shared)
    return shared

  const route = useRoute()
  return createLocaleText(() => route.path)
}
