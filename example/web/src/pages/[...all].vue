<route lang="yaml">
meta:
  layout: not-found
  nav:
    labelKey: layout.nav.notFound
    order: 100
    to: /404
</route>

<script setup lang="ts">
import { useRoute } from 'vue-router'

import { useLocaleText } from '~/composables/useLocaleText'

const route = useRoute()
const { t } = useLocaleText()
</script>

<template>
  <section class="card">
    <h2>{{ t('page.notFound.title') }}</h2>
    <p>{{ t('page.notFound.desc') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ route.fullPath }}</p>
  </section>
</template>

<style scoped>
.card {
  border: 1px solid #fca5a5;
  border-radius: 12px;
  padding: 16px;
  background: #fef2f2;
}

.card h2 {
  margin: 0 0 10px;
}

.card p {
  margin: 8px 0;
}
</style>
