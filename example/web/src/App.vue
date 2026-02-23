<script setup lang="ts">
import { Fragment, computed } from 'vue'
import type { Component } from 'vue'
import { RouterView, useRoute } from 'vue-router'

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
</script>

<template>
  <RouterView v-slot="{ Component }">
    <component :is="currentLayout">
      <component :is="Component" v-if="Component" />
    </component>
  </RouterView>
</template>
