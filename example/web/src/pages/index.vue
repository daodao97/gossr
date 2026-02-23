<route lang="yaml">
alias:
  - /en
  - /zh
meta:
  layout: home
</route>

<script setup lang="ts">
import { computed } from 'vue'

import { useLocaleText } from '~/composables/useLocaleText'
import { useSsrData } from '~/composables/useSsrData'

interface ExamplePayload {
  message?: string
  path?: string
  query?: string
  generatedAt?: string
}

const payload = useSsrData<ExamplePayload>()
const urlArg = computed(() => payload.value.path ?? '-')
const { t } = useLocaleText()
</script>

<template>
  <section class="card">
    <h2>{{ t('page.home.title') }}</h2>
    <p><strong>{{ t('common.field.message') }}:</strong> {{ payload.message ?? t('common.empty') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ payload.path ?? '-' }}</p>
    <p><strong>{{ t('common.field.query') }}:</strong> {{ payload.query ?? '-' }}</p>
    <p><strong>{{ t('common.field.generatedAt') }}:</strong> {{ payload.generatedAt ?? '-' }}</p>
    <p><strong>{{ t('common.field.urlArg') }}:</strong> {{ urlArg }}</p>
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
