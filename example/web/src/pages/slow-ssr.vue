<route lang="yaml">
alias:
  - /en/slow-ssr
  - /zh/slow-ssr
meta:
  layout: home
</route>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
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
const ssrErrorID = ref('-')
const { t } = useLocaleText()

onMounted(() => {
  const meta = document.querySelector('meta[name="ssr-error-id"]')
  if (meta instanceof HTMLMetaElement && meta.content.trim() !== '')
    ssrErrorID.value = meta.content.trim()
})
</script>

<template>
  <section class="card warning">
    <h2>{{ t('page.slow.title') }}</h2>
    <p>{{ t('page.slow.desc1') }}</p>
    <p>{{ t('page.slow.desc2') }}</p>
    <p><strong>{{ t('common.field.ssrErrorId') }}:</strong> {{ ssrErrorID }}</p>
    <p><strong>{{ t('common.field.message') }}:</strong> {{ payload.message ?? t('common.empty') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ payload.path ?? '-' }}</p>
    <p><strong>{{ t('common.field.generatedAt') }}:</strong> {{ payload.generatedAt ?? '-' }}</p>

    <a class="link" :href="route.path" target="_blank" rel="noopener noreferrer">{{ t('page.slow.openCurrent', { path: route.path }) }}</a>
  </section>
</template>

<style scoped>
.card {
  border-radius: 12px;
  padding: 16px;
}

.warning {
  border: 1px solid #f59e0b;
  background: #fffbeb;
}

.card h2 {
  margin: 0 0 10px;
}

.card p {
  margin: 8px 0;
}

.link {
  display: inline-block;
  margin-top: 12px;
  color: #92400e;
  text-decoration: none;
  font-weight: 600;
}

.link:hover {
  text-decoration: underline;
}
</style>
