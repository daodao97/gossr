<route lang="yaml">
meta:
  ssrData: false
  nav:
    label: About
    order: 20
</route>

<template>
  <main>
    <Teleport to="head">
      <title>About · gossr</title>
    </Teleport>
    <h1>About</h1>
    <p>This route is discovered automatically and opts out of SSR data fetching.</p>
  </main>
</template>
