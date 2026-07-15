<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import AppNavigation from '~/components/AppNavigation.vue'
import { useLocaleText } from '~/composables/useLocaleText'
import { availableLocales } from '~/modules/i18n'
import {
  localizeMenuTarget,
  parseLocaleRouteState,
  stripLocalePrefix,
  switchLocaleTarget,
} from '~/modules/locale-routing'
import { navigationLinksFor } from '~/modules/navigation'

const route = useRoute()
const router = useRouter()
const { t } = useLocaleText()
const baseNavigationLinks = navigationLinksFor(router)

const localeState = computed(() => parseLocaleRouteState(route.path))
const currentNormalizedPath = computed(() => stripLocalePrefix(route.path))
const links = computed(() => {
  return baseNavigationLinks.map(link => ({
    to: localizeMenuTarget(link.target, localeState.value),
    label: t(link.labelKey),
    active: link.target.pathname === currentNormalizedPath.value,
  }))
})

const localeLinks = computed(() => {
  return availableLocales.map(locale => ({
    label: locale.toUpperCase(),
    to: switchLocaleTarget(route.fullPath, locale),
    active: locale === localeState.value.locale,
  }))
})
</script>

<template>
  <main class="page">
    <img class="brand-image" src="/logo.webp" alt="gossr logo">
    <h1>{{ t('layout.title') }}</h1>
    <p class="subtitle">{{ t('layout.subtitle') }}</p>

    <nav class="locale-switch">
      <a
        v-for="locale in localeLinks"
        :key="locale.label"
        :href="locale.to"
        :class="{ active: locale.active }"
      >
        {{ locale.label }}
      </a>
    </nav>

    <AppNavigation :links="links" />

    <slot />
  </main>
</template>

<style scoped>
.page {
  max-width: 680px;
  margin: 40px auto;
  padding: 24px;
  font-family: Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  color: #111827;
}

.brand-image {
  display: block;
  width: 120px;
  height: 120px;
  margin: 0 auto 12px;
  object-fit: cover;
}

h1 {
  margin: 0 0 8px;
  font-size: 32px;
  text-align: center;
}

.subtitle {
  margin: 0 0 24px;
  color: #4b5563;
  text-align: center;
}

.locale-switch {
  display: inline-flex;
  gap: 8px;
  margin: 0 0 12px;
}

.locale-switch a {
  border: 1px solid #d1d5db;
  border-radius: 999px;
  padding: 4px 10px;
  color: #111827;
  text-decoration: none;
  font-size: 12px;
}

.locale-switch a.active {
  border-color: #2563eb;
  background: #eff6ff;
  color: #1d4ed8;
}

</style>
