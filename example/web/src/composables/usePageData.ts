import type {
  PageData,
  SessionPayload,
} from '~/page-data'

import { computed } from 'vue'
import type { ComputedRef } from 'vue'
import { useNavigation as useGossrNavigation } from '@daodao97/gossr-vue'
import type { NavigationCoordinator } from '@daodao97/gossr-vue'

export function useNavigation(): NavigationCoordinator<PageData> {
  return useGossrNavigation<PageData>()
}

export function usePage(): ComputedRef<PageData['page'] | undefined> {
  const navigation = useNavigation()
  return computed(() => navigation.current.value?.page)
}

export function useSession(): ComputedRef<SessionPayload | null> {
  const navigation = useNavigation()
  return computed(() => navigation.current.value?.session ?? null)
}
