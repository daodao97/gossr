import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    include: ['src/**/*.test.ts', 'vite/**/*.test.ts'],
    restoreMocks: true,
  },
})
