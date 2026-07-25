<route lang="yaml">
alias:
  - /en/session-demo
  - /zh/session-demo
meta:
  layout: home
  nav:
    labelKey: layout.nav.session
    order: 50
</route>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import { useLocaleText } from '~/composables/useLocaleText'
import { usePage, useSession } from '~/composables/usePageDocument'

const page = usePage()
const session = useSession()
const route = useRoute()
const { t } = useLocaleText()
const user = computed(() => session.value?.user)
const isLoggedIn = computed(() => !!user.value?.email)
const nextPath = computed(() => {
  const next = route.query.next
  if (typeof next === 'string' && next.startsWith('/') && !next.startsWith('//') && !next.includes('\\'))
    return next
  return route.path || '/session-demo'
})
</script>

<template>
  <section class="card">
    <h2>{{ t('page.session.title') }}</h2>
    <p><strong>{{ t('common.field.message') }}:</strong> {{ page?.message || t('common.empty') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ route.path }}</p>
    <p v-if="nextPath !== route.path"><strong>{{ t('common.field.next') }}:</strong> {{ nextPath }}</p>

    <p v-if="isLoggedIn"><strong>{{ t('common.field.status') }}:</strong> {{ t('page.session.loggedIn') }}</p>
    <p v-else><strong>{{ t('common.field.status') }}:</strong> {{ t('page.session.loggedOut') }}</p>

    <template v-if="user">
      <p><strong>{{ t('common.field.userId') }}:</strong> {{ user.id ?? '-' }}</p>
      <p><strong>{{ t('common.field.userName') }}:</strong> {{ user.name ?? '-' }}</p>
      <p><strong>{{ t('common.field.userEmail') }}:</strong> {{ user.email ?? '-' }}</p>
      <p><strong>{{ t('common.field.userProvider') }}:</strong> {{ user.provider ?? '-' }}</p>
    </template>

    <div class="actions">
      <form method="post" action="/demo/session/login">
        <input type="hidden" name="next" :value="nextPath">
        <button class="btn" type="submit">{{ t('page.session.setDemo') }}</button>
      </form>
      <form method="post" action="/demo/session/logout">
        <input type="hidden" name="next" :value="nextPath">
        <button class="btn ghost" type="submit">{{ t('page.session.clearDemo') }}</button>
      </form>
    </div>
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

.actions {
  margin-top: 14px;
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}

.btn {
  display: inline-block;
  border: 0;
  padding: 8px 12px;
  border-radius: 8px;
  background: #1d4ed8;
  color: #fff;
  text-decoration: none;
  font: inherit;
  cursor: pointer;
}

.btn.ghost {
  background: #e5e7eb;
  color: #111827;
}

.btn:hover {
  opacity: 0.9;
}
</style>
