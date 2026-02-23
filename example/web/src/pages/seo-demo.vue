<route lang="yaml">
alias:
  - /en/seo-demo
  - /zh/seo-demo
meta:
  layout: home
</route>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import { useLocaleText } from '~/composables/useLocaleText'
import { useSsrData } from '~/composables/useSsrData'

interface ExamplePayload {
  message?: string
  path?: string
  query?: string
  generatedAt?: string
}

const route = useRoute()
const payload = useSsrData<ExamplePayload>()
const { t } = useLocaleText()

const seoTitle = computed(() => {
  const queryTitle = route.query.title
  if (typeof queryTitle === 'string' && queryTitle.trim() !== '')
    return queryTitle.trim()
  return t('page.seo.defaultTitle')
})

const seoDescription = computed(() => {
  const generatedAt = payload.value.generatedAt ?? '-'
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
    <p><strong>{{ t('common.field.message') }}:</strong> {{ payload.message ?? t('common.empty') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ payload.path ?? '-' }}</p>
    <p><strong>{{ t('common.field.query') }}:</strong> {{ payload.query ?? '-' }}</p>
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
