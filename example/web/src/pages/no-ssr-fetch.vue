<route lang="yaml">
alias:
  - /en/no-ssr-fetch
  - /zh/no-ssr-fetch
meta:
  layout: home
  ssrData: false
</route>

<script setup lang="ts">
import { useRoute } from 'vue-router'

import { useLocaleText } from '~/composables/useLocaleText'

const route = useRoute()
const { t } = useLocaleText()
</script>

<template>
  <section class="card">
    <h2>{{ t('page.noFetch.title') }}</h2>
    <p>{{ t('page.noFetch.desc') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ route.fullPath }}</p>
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
