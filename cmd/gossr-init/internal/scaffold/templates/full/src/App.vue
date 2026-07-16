<script setup lang="ts">
import DefaultLayout from '~/layouts/default.vue'
</script>

<template>
  <DefaultLayout>
    <RouterView />
  </DefaultLayout>
</template>

<style>
body {
  margin: 0;
  color: #172033;
  background: #f7f8fb;
  font: 16px/1.5 system-ui, sans-serif;
}

a {
  color: #3157d5;
}
</style>
