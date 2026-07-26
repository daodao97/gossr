import { computed, inject } from 'vue'
import type { ComputedRef, InjectionKey } from 'vue'

import type { NavigationCoordinator } from '@daodao97/gossr-vue'
import type { PageData } from '~/page-data'

export const navigationKey: InjectionKey<NavigationCoordinator<PageData>> = Symbol('navigation')

export function useNavigation(): NavigationCoordinator<PageData> {
  const navigation = inject(navigationKey)
  if (!navigation)
    throw new Error('navigation is not provided')
  return navigation
}

export function usePage(): ComputedRef<PageData['page'] | undefined> {
  const navigation = useNavigation()
  return computed(() => navigation.current.value?.page)
}
