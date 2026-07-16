<route lang="yaml">
meta:
  nav:
    label: Home
    order: 10
</route>

<script setup lang="ts">
import { useSsrData } from '~/composables/useSsrData'

interface HomePayload {
  message?: string
  path?: string
}

const payload = useSsrData<HomePayload>()
</script>

<template>
  <main>
    <Teleport to="head">
      <meta name="description" content="A gossr application" />
    </Teleport>
    <h1>gossr is ready</h1>
    <p>{{ payload.message ?? 'Add a payload handler in the Go host.' }}</p>
    <small>Path: {{ payload.path ?? '/' }}</small>
  </main>
</template>
