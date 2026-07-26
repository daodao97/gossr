<route lang="yaml">
alias:
  - /en/protected
  - /zh/protected
meta:
  layout: home
  nav:
    labelKey: layout.nav.protected
    order: 60
</route>

<script setup lang="ts">
import { useRoute } from 'vue-router'

import { useLocaleText } from '~/composables/useLocaleText'
import { useSession } from '~/composables/usePageData'

const route = useRoute()
const session = useSession()
const { t } = useLocaleText()
</script>

<template>
  <section class="card secure">
    <h2>{{ t('page.protected.title') }}</h2>
    <p>{{ t('page.protected.desc') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ route.fullPath }}</p>
    <p><strong>{{ t('common.field.userName') }}:</strong> {{ session?.user?.name ?? '-' }}</p>
    <p><strong>{{ t('common.field.userEmail') }}:</strong> {{ session?.user?.email ?? '-' }}</p>
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
