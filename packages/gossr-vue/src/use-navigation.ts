import { inject } from 'vue'
import type { InjectionKey } from 'vue'

import type { NavigationCoordinator } from './types.js'

// createApplicationRuntime 在挂载前自动 provide;宿主组件直接
// useNavigation<PageData>() 取用,不需要自建 InjectionKey 与 provide 接线。
export const navigationInjectionKey: InjectionKey<NavigationCoordinator<unknown>>
  = Symbol('gossr-navigation')

export function useNavigation<PageData = unknown>(): NavigationCoordinator<PageData> {
  const navigation = inject(navigationInjectionKey)
  if (!navigation)
    throw new Error('useNavigation() requires a gossr application runtime')
  return navigation as NavigationCoordinator<PageData>
}
