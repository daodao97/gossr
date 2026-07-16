<script setup lang="ts">
import { navigationItems } from '~/modules/navigation'
</script>

<template>
  <div class="shell">
    <header>
      <strong>gossr</strong>
      <nav aria-label="Main navigation">
        <RouterLink v-for="item in navigationItems" :key="item.to" :to="item.to">
          {{ item.label }}
        </RouterLink>
      </nav>
    </header>
    <slot />
  </div>
</template>

<style scoped>
.shell {
  width: min(800px, calc(100% - 32px));
  margin: 48px auto;
}

header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 32px;
}

nav {
  display: flex;
  gap: 16px;
}
</style>
