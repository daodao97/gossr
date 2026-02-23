<route lang="yaml">
alias:
  - /en/session-demo
  - /zh/session-demo
meta:
  layout: home
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
  session_token?: string
  user?: SessionUser
}

interface ExamplePayload {
  message?: string
  path?: string
  query?: string
  generatedAt?: string
  session?: SessionPayload
}

const payload = useSsrData<ExamplePayload>()
const route = useRoute()
const { t } = useLocaleText()
const user = computed(() => payload.value.session?.user)
const isLoggedIn = computed(() => !!user.value?.email)
const nextPath = computed(() => route.path || '/session-demo')
const loginURL = computed(() => `/demo/session/login?next=${encodeURIComponent(nextPath.value)}`)
const logoutURL = computed(() => `/demo/session/logout?next=${encodeURIComponent(nextPath.value)}`)
</script>

<template>
  <section class="card">
    <h2>{{ t('page.session.title') }}</h2>
    <p><strong>{{ t('common.field.message') }}:</strong> {{ payload.message ?? t('common.empty') }}</p>
    <p><strong>{{ t('common.field.path') }}:</strong> {{ payload.path ?? '-' }}</p>

    <p v-if="isLoggedIn"><strong>{{ t('common.field.status') }}:</strong> {{ t('page.session.loggedIn') }}</p>
    <p v-else><strong>{{ t('common.field.status') }}:</strong> {{ t('page.session.loggedOut') }}</p>

    <template v-if="user">
      <p><strong>{{ t('common.field.userId') }}:</strong> {{ user.id ?? '-' }}</p>
      <p><strong>{{ t('common.field.userName') }}:</strong> {{ user.name ?? '-' }}</p>
      <p><strong>{{ t('common.field.userEmail') }}:</strong> {{ user.email ?? '-' }}</p>
      <p><strong>{{ t('common.field.userProvider') }}:</strong> {{ user.provider ?? '-' }}</p>
    </template>

    <div class="actions">
      <a class="btn" :href="loginURL">{{ t('page.session.setDemo') }}</a>
      <a class="btn ghost" :href="logoutURL">{{ t('page.session.clearDemo') }}</a>
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
  padding: 8px 12px;
  border-radius: 8px;
  background: #1d4ed8;
  color: #fff;
  text-decoration: none;
}

.btn.ghost {
  background: #e5e7eb;
  color: #111827;
}

.btn:hover {
  opacity: 0.9;
}
</style>
