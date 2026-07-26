import { computed } from 'vue'
import type { ComputedRef } from 'vue'

import { pageDataOf } from './envelope.js'
import type { PageDataUnion, StandardPageData } from './envelope.js'
import { useNavigation } from './use-navigation.js'

// 标准信封宿主的页面数据访问:非阻塞导航下页面立即挂载,文档未提交时
// 返回占位,提交后响应式填充。宿主只需绑定自己的 kind→data 映射与占位。
export function useStandardPageData<Map extends { [K in keyof Map]: object }, Kind extends keyof Map & string>(
  kind: Kind,
  placeholder: Map[Kind],
): ComputedRef<Map[Kind]> {
  const navigation = useNavigation<StandardPageData<object, PageDataUnion<Map>>>()
  return computed(() => pageDataOf<Map, Kind>(navigation.current.value, kind) ?? placeholder)
}
