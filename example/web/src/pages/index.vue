<route lang="yaml">
alias:
  - /en
  - /zh
meta:
  layout: home
  nav:
    labelKey: layout.nav.home
    order: 10
</route>

<script setup lang="ts">
import { useRoute } from 'vue-router'

import { useLocaleText } from '~/composables/useLocaleText'
import { usePage } from '~/composables/usePageDocument'

const route = useRoute()
const page = usePage()
const { t } = useLocaleText()
</script>

<template>
  <section class="card">
    <h2>{{ t('page.home.title') }}</h2>
    <p><strong>{{ t('common.field.message') }}:</strong> {{ page?.message || t('common.empty') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ route.path }}</p>
    <p><strong>{{ t('common.field.query') }}:</strong> {{ route.fullPath.includes('?') ? route.fullPath.split('?')[1] : '-' }}</p>
    <p><strong>{{ t('common.field.generatedAt') }}:</strong> {{ page?.generated_at ?? '-' }}</p>
    <p><strong>{{ t('common.field.urlArg') }}:</strong> {{ route.path }}</p>
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
</style>
