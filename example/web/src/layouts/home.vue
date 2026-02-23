<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useRoute } from 'vue-router'

import { useLocaleText } from '~/composables/useLocaleText'
import { availableLocales, defaultLocale, isSupportedLocale, type MessageKey, type SupportedLocale } from '~/modules/i18n'

const route = useRoute()
const { t } = useLocaleText()

const baseLinks: { labelKey: MessageKey, to: string }[] = [
  { labelKey: 'layout.nav.home', to: '/' },
  { labelKey: 'layout.nav.hiGopher', to: '/hi/gopher' },
  { labelKey: 'layout.nav.hiVue', to: '/hi/vue?title=Ms.' },
  { labelKey: 'layout.nav.seo', to: '/seo-demo?title=SSR%20SEO%20Title' },
  { labelKey: 'layout.nav.session', to: '/session-demo' },
  { labelKey: 'layout.nav.protected', to: '/protected' },
  { labelKey: 'layout.nav.slow', to: '/slow-ssr' },
  { labelKey: 'layout.nav.slowFetch', to: '/slow-fetch' },
  { labelKey: 'layout.nav.noFetch', to: '/no-ssr-fetch' },
  { labelKey: 'layout.nav.notFound', to: '/404' },
]

interface LocaleState {
  locale: SupportedLocale
  explicit: boolean
}

const localeState = computed(() => parseLocaleState(route.path))
const currentNormalizedPath = computed(() => stripLocalePrefix(route.path))
const links = computed(() => {
  return baseLinks.map(link => ({
    ...link,
    to: localizeMenuTarget(link.to, localeState.value),
    label: t(link.labelKey),
    active: stripLocalePrefix(parsePathTarget(link.to).pathname) === currentNormalizedPath.value,
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

function localizeMenuTarget(rawTarget: string, state: LocaleState): string {
  const parsed = parsePathTarget(rawTarget)
  const normalizedPath = stripLocalePrefix(parsed.pathname)

  // 默认 locale 且 URL 本身不带 locale 前缀时，导航保持无前缀。
  if (!state.explicit && state.locale === defaultLocale)
    return `${normalizedPath}${parsed.search}${parsed.hash}`

  const localizedPath = withLocalePrefix(state.locale, normalizedPath)
  return `${localizedPath}${parsed.search}${parsed.hash}`
}

function switchLocaleTarget(rawTarget: string, locale: SupportedLocale): string {
  const parsed = parsePathTarget(rawTarget)
  const normalizedPath = stripLocalePrefix(parsed.pathname)
  const localizedPath = locale === defaultLocale ? normalizedPath : withLocalePrefix(locale, normalizedPath)
  return `${localizedPath}${parsed.search}${parsed.hash}`
}

interface ParsedPathTarget {
  pathname: string
  search: string
  hash: string
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

    <nav class="links">
      <RouterLink
        v-for="link in links"
        :key="link.to"
        :to="link.to"
        :class="{ active: link.active }"
      >
        {{ link.label }}
      </RouterLink>
    </nav>

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
