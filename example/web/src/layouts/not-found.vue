<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import { useLocaleText } from '~/composables/useLocaleText'

const { locale, t } = useLocaleText()
const homePath = computed(() => (locale.value === 'zh' ? '/zh' : '/'))
</script>

<template>
  <main class="not-found-layout">
    <section class="shell">
      <p class="eyebrow">{{ t('layout.notFound.eyebrow') }}</p>
      <slot />
      <RouterLink class="home-link" :to="homePath">{{ t('layout.notFound.backHome') }}</RouterLink>
    </section>
  </main>
</template>

<style scoped>
.not-found-layout {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 24px;
  background: radial-gradient(circle at 20% 20%, #fee2e2 0%, #fff 45%);
  font-family: Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
}

.shell {
  width: min(560px, 100%);
  padding: 28px;
  border: 1px solid #fecaca;
  border-radius: 16px;
  background: rgba(255, 255, 255, 0.96);
  box-shadow: 0 18px 45px rgba(239, 68, 68, 0.12);
}

.eyebrow {
  margin: 0 0 16px;
  font-size: 13px;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: #991b1b;
}

.home-link {
  display: inline-block;
  margin-top: 18px;
  color: #b91c1c;
  text-decoration: none;
  font-weight: 600;
}

.home-link:hover {
  text-decoration: underline;
}
</style>
