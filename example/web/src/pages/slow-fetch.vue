<route lang="yaml">
alias:
  - /en/slow-fetch
  - /zh/slow-fetch
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

const fetchPath = computed(() => {
  const fullPath = route.fullPath.split('#')[0]
  return `/_ssr/data${fullPath}`
})

const fetchCurl = computed(() => {
  return `curl -H "X-SSR-Fetch: 1" "http://127.0.0.1:8080${fetchPath.value}"`
})
</script>

<template>
  <section class="card info">
    <h2>{{ t('page.slowFetch.title') }}</h2>
    <p>{{ t('page.slowFetch.desc1') }}</p>
    <p>{{ t('page.slowFetch.desc2') }}</p>
    <p><strong>{{ t('page.slowFetch.fetchPath') }}:</strong> {{ fetchPath }}</p>
    <p><strong>{{ t('common.field.message') }}:</strong> {{ payload.message ?? t('common.empty') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ payload.path ?? '-' }}</p>
    <p><strong>{{ t('common.field.generatedAt') }}:</strong> {{ payload.generatedAt ?? '-' }}</p>
    <p class="tip">{{ t('page.slowFetch.tip') }}</p>
    <pre class="command">{{ fetchCurl }}</pre>
  </section>
</template>

<style scoped>
.card {
  border-radius: 12px;
  padding: 16px;
}

.info {
  border: 1px solid #0891b2;
  background: #ecfeff;
}

.card h2 {
  margin: 0 0 10px;
}

.card p {
  margin: 8px 0;
}

.tip {
  color: #155e75;
}

.command {
  margin: 10px 0 0;
  padding: 10px;
  border-radius: 8px;
  background: #cffafe;
  color: #083344;
  overflow-x: auto;
}
</style>
