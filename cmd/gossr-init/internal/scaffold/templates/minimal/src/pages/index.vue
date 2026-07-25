<script setup lang="ts">
import { usePage } from '~/composables'

const page = usePage()
</script>

<template>
  <main>
    <h1>gossr is ready</h1>
    <p>{{ page?.message || 'Return a page document from the Go resolver.' }}</p>
    <small>Generated at: {{ page?.generated_at ?? '-' }}</small>
  </main>
</template>
