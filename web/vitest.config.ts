import { defineConfig } from 'vitest/config'

// Unit tests only; the browser tests in e2e/ belong to Playwright.
export default defineConfig({
  test: { include: ['src/**/*.test.ts'] },
})
