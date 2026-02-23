<route lang="yaml">
alias:
  - /en/hi/vue
  - /zh/hi/vue
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

const payload = useSsrData<ExamplePayload>()
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
    <p><strong>{{ t('common.field.message') }}:</strong> {{ payload.message ?? t('common.empty') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ payload.path ?? '-' }}</p>
    <p><strong>{{ t('common.field.query') }}:</strong> {{ payload.query ?? '-' }}</p>
    <p><strong>{{ t('common.field.generatedAt') }}:</strong> {{ payload.generatedAt ?? '-' }}</p>
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
