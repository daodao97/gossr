import { computed } from 'vue'
import type { ComputedRef } from 'vue'

import { useNavigation as useGossrNavigation } from '@daodao97/gossr-vue'
import type { NavigationCoordinator } from '@daodao97/gossr-vue'
import type { PageData } from '~/page-data'

// 框架在运行时自动 provide;这里只把泛型绑定到应用的 PageData。
export function useNavigation(): NavigationCoordinator<PageData> {
  return useGossrNavigation<PageData>()
}

export function usePage(): ComputedRef<PageData['page'] | undefined> {
  const navigation = useNavigation()
  return computed(() => navigation.current.value?.page)
}
