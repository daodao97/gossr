<script setup lang="ts">
import { Fragment, computed } from 'vue'
import type { Component } from 'vue'
import { RouterView, useRoute } from 'vue-router'

import { useSsrData } from '~/composables/useSsrData'

const route = useRoute()
const layoutModules = import.meta.glob('./layouts/*.vue', {
  eager: true,
  import: 'default',
}) as Record<string, Component>

const layouts = Object.fromEntries(
  Object.entries(layoutModules).map(([path, component]) => {
    const layoutName = path.split('/').pop()?.replace('.vue', '') ?? path
    return [layoutName, component]
  }),
) as Record<string, Component>

// 未配置 layout 或 layout 不存在时，直接渲染页面。
const currentLayout = computed(() => {
  const layoutName = route.meta.layout
  if (typeof layoutName !== 'string' || layoutName === 'default')
    return Fragment

  return layouts[layoutName] ?? Fragment
})

const ssrState = useSsrData<{ __ssrFetchLoading?: boolean }>()
const isSsrFetchLoading = computed(() => ssrState.value.__ssrFetchLoading === true)
</script>

<template>
  <div v-if="isSsrFetchLoading" class="global-fetch-loading" />
  <RouterView v-slot="{ Component }">
    <component :is="currentLayout">
      <component :is="Component" v-if="Component" />
    </component>
  </RouterView>
</template>

<style scoped>
.global-fetch-loading {
  position: fixed;
  top: 0;
  left: 0;
  width: 100%;
  height: 3px;
  z-index: 9999;
  pointer-events: none;
  background: linear-gradient(90deg, #22d3ee 0%, #2563eb 100%);
  transform-origin: left;
  animation: global-fetch-pulse 1.2s ease-in-out infinite;
}

@keyframes global-fetch-pulse {
  0% {
    transform: scaleX(0.15);
    opacity: 0.65;
  }
  50% {
    transform: scaleX(0.8);
    opacity: 1;
  }
  100% {
    transform: scaleX(0.15);
    opacity: 0.65;
  }
}
</style>
