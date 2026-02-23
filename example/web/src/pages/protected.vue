<route lang="yaml">
alias:
  - /en/protected
  - /zh/protected
meta:
  layout: home
  requiresAuth: true
</route>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import { useLocaleText } from '~/composables/useLocaleText'
import { useSsrData } from '~/composables/useSsrData'

interface SessionUser {
  id?: string
  name?: string
  email?: string
  provider?: string
}

interface SessionPayload {
  user?: SessionUser
}

interface ExamplePayload {
  session?: SessionPayload
}

const route = useRoute()
const payload = useSsrData<ExamplePayload>()
const { t } = useLocaleText()
const user = computed(() => payload.value.session?.user ?? null)
</script>

<template>
  <section class="card secure">
    <h2>{{ t('page.protected.title') }}</h2>
    <p>{{ t('page.protected.desc') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ route.fullPath }}</p>
    <p><strong>{{ t('common.field.userName') }}:</strong> {{ user?.name ?? '-' }}</p>
    <p><strong>{{ t('common.field.userEmail') }}:</strong> {{ user?.email ?? '-' }}</p>
  </section>
</template>

<style scoped>
.card {
  border-radius: 12px;
  padding: 16px;
}

.secure {
  border: 1px solid #60a5fa;
  background: #eff6ff;
}

.card h2 {
  margin: 0 0 10px;
}

.card p {
  margin: 8px 0;
}
</style>
