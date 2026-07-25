<route lang="yaml">
alias:
  - /en/hi/vue
  - /zh/hi/vue
meta:
  layout: home
  nav:
    labelKey: layout.nav.hiVue
    order: 30
    query:
      title: Ms.
</route>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import { useLocaleText } from '~/composables/useLocaleText'
import { usePage } from '~/composables/usePageDocument'

const page = usePage()
const route = useRoute()
const { t } = useLocaleText()
const title = computed(() => {
  const value = route.query.title
  return typeof value === 'string' && value.length > 0 ? value : t('page.hiVue.defaultTitle')
})
</script>

<template>
  <section class="card">
    <h2>{{ t('page.hiVue.title') }}</h2>
    <p><strong>{{ t('common.field.title') }}:</strong> {{ title }}</p>
    <p><strong>{{ t('common.field.message') }}:</strong> {{ page?.message || t('common.empty') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ route.path }}</p>
    <p><strong>{{ t('common.field.query') }}:</strong> {{ route.fullPath.includes('?') ? route.fullPath.split('?')[1] : '-' }}</p>
    <p><strong>{{ t('common.field.generatedAt') }}:</strong> {{ page?.generated_at ?? '-' }}</p>
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
