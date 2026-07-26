<route lang="yaml">
alias:
  - /en/seo-demo
  - /zh/seo-demo
meta:
  layout: home
  nav:
    labelKey: layout.nav.seo
    order: 40
    query:
      title: SSR SEO Title
</route>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import { useLocaleText } from '~/composables/useLocaleText'
import { usePage } from '~/composables/usePageData'

const route = useRoute()
const page = usePage()
const { t } = useLocaleText()

const seoTitle = computed(() => {
  const queryTitle = route.query.title
  if (typeof queryTitle === 'string' && queryTitle.trim() !== '')
    return queryTitle.trim()
  return t('page.seo.defaultTitle')
})

const seoDescription = computed(() => {
  const generatedAt = page.value?.generated_at ?? '-'
  return t('page.seo.descTemplate', { generatedAt })
})
</script>

<template>
  <teleport to="head">
    <title>{{ seoTitle }}</title>
    <meta name="description" :content="seoDescription">
    <meta property="og:title" :content="seoTitle">
    <meta property="og:description" :content="seoDescription">
  </teleport>

  <section class="card">
    <h2>{{ t('page.seo.title') }}</h2>
    <p><strong>{{ t('common.field.title') }}:</strong> {{ seoTitle }}</p>
    <p><strong>{{ t('common.field.description') }}:</strong> {{ seoDescription }}</p>
    <p><strong>{{ t('common.field.message') }}:</strong> {{ page?.message || t('common.empty') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ route.path }}</p>
    <p><strong>{{ t('common.field.query') }}:</strong> {{ route.fullPath.includes('?') ? route.fullPath.split('?')[1] : '-' }}</p>
    <p class="tip">{{ t('page.seo.tip') }}</p>
  </section>
</template>

<style scoped>
.card {
  border: 1px solid #e5e7eb;
  border-radius: 12px;
  padding: 16px;
  background: #fafafa;
}

.card h2 {
  margin: 0 0 10px;
}

.card p {
  margin: 8px 0;
}

.tip {
  color: #4b5563;
}
</style>
