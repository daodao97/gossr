import { computed, inject, shallowRef } from 'vue'
import type { ComputedRef, ShallowRef } from 'vue'

export type SsrState = Record<string, unknown>

export interface SsrDataContext {
  state: ShallowRef<SsrState>
  setState: (value: SsrState) => void
}

export const ssrDataKey = Symbol('ssr-data')

export function createSsrDataContext(initialState: SsrState): SsrDataContext {
  const state = shallowRef<SsrState>({ ...initialState })

  const setState = (value: SsrState) => {
    state.value = value && typeof value === 'object' ? value : {}
  }

  return {
    state,
    setState,
  }
}

export function useSsrData<T extends object = SsrState>(): ComputedRef<T> {
  const ctx = inject<SsrDataContext | null>(ssrDataKey, null)
  if (!ctx)
    return computed(() => ({} as T))

  return computed(() => ctx.state.value as T)
}
