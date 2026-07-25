import { computed, inject } from 'vue'
import type { ComputedRef, InjectionKey } from 'vue'

import type { NavigationCoordinator } from '@daodao97/gossr-vue'
import type { PageDocument } from '~/page-document'

export const navigationKey: InjectionKey<NavigationCoordinator<PageDocument>> = Symbol('navigation')

export function useNavigation(): NavigationCoordinator<PageDocument> {
  const navigation = inject(navigationKey)
  if (!navigation)
    throw new Error('navigation is not provided')
  return navigation
}

export function usePage(): ComputedRef<PageDocument['page'] | undefined> {
  const navigation = useNavigation()
  return computed(() => navigation.current.value?.page)
}
