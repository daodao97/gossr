<script setup lang="ts">
import { computed } from 'vue'

import { useSsrData } from '~/composables/useSsrData'

interface ExamplePayload {
  message?: string
  path?: string
  query?: string
  generatedAt?: string
}

const payload = useSsrData<ExamplePayload>()

const links = [
  { label: 'Home', href: '/' },
  { label: 'Hi gopher', href: '/hi/gopher' },
  { label: 'Hi vue + title', href: '/hi/vue?title=Ms.' },
]

const urlArg = computed(() => payload.value.path ?? '-')
</script>

<template>
  <main class="page">
    <h1>gossr + Vue SSR minimal</h1>
    <p class="subtitle">Rendered by Go, hydrated by Vue.</p>

    <section class="card">
      <p><strong>message:</strong> {{ payload.message ?? 'empty' }}</p>
      <p><strong>path:</strong> {{ payload.path ?? '-' }}</p>
      <p><strong>query:</strong> {{ payload.query ?? '-' }}</p>
      <p><strong>generatedAt:</strong> {{ payload.generatedAt ?? '-' }}</p>
      <p><strong>url arg:</strong> {{ urlArg }}</p>
    </section>

    <nav class="links">
      <a v-for="link in links" :key="link.href" :href="link.href">{{ link.label }}</a>
    </nav>
  </main>
</template>

<style scoped>
.page {
  max-width: 680px;
  margin: 40px auto;
  padding: 24px;
  font-family: Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  color: #111827;
}

h1 {
  margin: 0 0 8px;
  font-size: 32px;
}

.subtitle {
  margin: 0 0 24px;
  color: #4b5563;
}

.card {
  border: 1px solid #e5e7eb;
  border-radius: 12px;
  padding: 16px;
  background: #fafafa;
}

.card p {
  margin: 8px 0;
}

.links {
  display: flex;
  gap: 12px;
  margin-top: 20px;
  flex-wrap: wrap;
}

.links a {
  color: #2563eb;
  text-decoration: none;
}

.links a:hover {
  text-decoration: underline;
}
</style>
