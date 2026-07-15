<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import AppNavigation from '~/components/AppNavigation.vue'
import { useLocaleText } from '~/composables/useLocaleText'
import { availableLocales, defaultLocale, isSupportedLocale, type SupportedLocale } from '~/modules/i18n'
import { navigationLinksFor, type ParsedPathTarget } from '~/modules/navigation'

const route = useRoute()
const router = useRouter()
const { t } = useLocaleText()
const baseNavigationLinks = navigationLinksFor(router)

interface LocaleState {
  locale: SupportedLocale
  explicit: boolean
}

const localeState = computed(() => parseLocaleState(route.path))
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

function parseLocaleState(path: string): LocaleState {
  const trimmed = path.replace(/^\/+/, '')
  const firstSegment = trimmed.split('/')[0]
  if (isSupportedLocale(firstSegment))
    return { locale: firstSegment, explicit: true }

  return { locale: defaultLocale, explicit: false }
}

function stripLocalePrefix(path: string): string {
  for (const locale of availableLocales) {
    if (path === `/${locale}`)
      return '/'
    const prefix = `/${locale}/`
    if (path.startsWith(prefix))
      return `/${path.slice(prefix.length)}`
  }
  return path || '/'
}

function withLocalePrefix(locale: SupportedLocale, normalizedPath: string): string {
  if (normalizedPath === '/')
    return `/${locale}`
  return `/${locale}${normalizedPath}`
}

function localizeMenuTarget(target: ParsedPathTarget, state: LocaleState): string {
  const normalizedPath = target.pathname

  // 默认 locale 且 URL 本身不带 locale 前缀时，导航保持无前缀。
  if (!state.explicit && state.locale === defaultLocale)
    return `${normalizedPath}${target.search}${target.hash}`

  const localizedPath = withLocalePrefix(state.locale, normalizedPath)
  return `${localizedPath}${target.search}${target.hash}`
}

function switchLocaleTarget(rawTarget: string, locale: SupportedLocale): string {
  const parsed = parsePathTarget(rawTarget)
  const normalizedPath = stripLocalePrefix(parsed.pathname)
  const localizedPath = locale === defaultLocale ? normalizedPath : withLocalePrefix(locale, normalizedPath)
  return `${localizedPath}${parsed.search}${parsed.hash}`
}

function parsePathTarget(rawTarget: string): ParsedPathTarget {
  const target = rawTarget.trim()
  if (!target)
    return { pathname: '/', search: '', hash: '' }

  let pathAndQuery = target
  let hash = ''

  const hashIndex = target.indexOf('#')
  if (hashIndex >= 0) {
    pathAndQuery = target.slice(0, hashIndex)
    hash = target.slice(hashIndex)
  }

  let pathname = pathAndQuery
  let search = ''
  const queryIndex = pathAndQuery.indexOf('?')
  if (queryIndex >= 0) {
    pathname = pathAndQuery.slice(0, queryIndex)
    search = pathAndQuery.slice(queryIndex)
  }

  if (!pathname)
    pathname = '/'
  if (!pathname.startsWith('/'))
    pathname = `/${pathname}`

  return { pathname, search, hash }
}
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
