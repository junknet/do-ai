import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './ui',
  timeout: 60_000,
  reporter: [['list']],
  use: {
    headless: true,
    viewport: { width: 1280, height: 900 },
  },
})
