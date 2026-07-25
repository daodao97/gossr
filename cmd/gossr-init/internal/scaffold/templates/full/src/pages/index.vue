<route lang="json">
{
  "meta": {
    "nav": {
      "label": "Home",
      "order": 10
    }
  }
}
</route>

<script setup lang="ts">
import { usePage } from '~/composables'

const page = usePage()
</script>

<template>
  <main>
    <Teleport to="head">
      <meta name="description" content="A gossr application" />
    </Teleport>
    <h1>gossr is ready</h1>
    <p>{{ page?.message || 'Return a page document from the Go resolver.' }}</p>
    <small>Generated at: {{ page?.generated_at ?? '-' }}</small>
  </main>
</template>
