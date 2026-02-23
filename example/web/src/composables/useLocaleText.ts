import { computed } from 'vue'
import { useRoute } from 'vue-router'

import {
  localeFromPath,
  translate,
  type MessageKey,
  type MessageParams,
  type SupportedLocale,
} from '~/modules/i18n'

export function useLocaleText() {
  const route = useRoute()
  const locale = computed<SupportedLocale>(() => localeFromPath(route.path))
  const t = (key: MessageKey, params: MessageParams = {}) => {
    return translate(locale.value, key, params)
  }

  return {
    locale,
    t,
  }
}
