<script setup lang="ts">
import { useRouter } from 'vue-router'

interface NavigationItem {
  active: boolean
  label: string
  to: string
}

defineProps<{
  links: readonly NavigationItem[]
}>()

const router = useRouter()

function navigate(event: MouseEvent) {
  if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.altKey || event.ctrlKey || event.shiftKey)
    return

  const target = event.target
  if (!(target instanceof Element))
    return

  const anchor = target.closest<HTMLAnchorElement>('a[data-app-navigation-link]')
  const href = anchor?.getAttribute('href')
  if (!href)
    return

  event.preventDefault()
  void router.push(href)
}
</script>

<template>
  <nav class="links" @click="navigate">
    <a
      v-for="link in links"
      :key="link.to"
      :href="link.to"
      :class="{ active: link.active }"
      :aria-current="link.active ? 'page' : undefined"
      data-app-navigation-link
    >
      {{ link.label }}
    </a>
  </nav>
</template>

<style scoped>
.links {
  display: flex;
  gap: 12px;
  margin-top: 20px;
  flex-wrap: wrap;
  margin-bottom: 20px;
}

.links a {
  color: #2563eb;
  text-decoration: none;
}

.links a:hover {
  text-decoration: underline;
}

.links a.active {
  color: #1d4ed8;
  font-weight: 600;
  text-decoration: underline;
  text-underline-offset: 2px;
}
</style>
