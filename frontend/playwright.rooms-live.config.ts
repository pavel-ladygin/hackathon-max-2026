import { defineConfig, devices } from '@playwright/test'

const baseURL = process.env.PLAYWRIGHT_BASE_URL ?? 'http://127.0.0.1:4173'
const port = new URL(baseURL).port
export default defineConfig({
  testDir: './tests/e2e',
  testMatch: /room-rounds-(live|empty-live)\.spec\.ts/,
  workers: 1,
  timeout: 90_000,
  outputDir: process.env.PLAYWRIGHT_OUTPUT_DIR ?? './test-results/rooms-live',
  reporter: [['line']],
  use: {
    baseURL,
    reducedMotion: 'reduce',
    trace: 'retain-on-failure',
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE } : {},
  },
  webServer: {
    command: `npm run dev -- --host 127.0.0.1 --port ${port} --strictPort`,
    env: { VITE_YANDEX_MAPS_API_KEY: 'e2e-test-key' },
    url: baseURL,
    reuseExistingServer: true,
  },
  projects: [
    { name: 'mobile-chrome', use: { ...devices['Pixel 7'] } },
    { name: 'desktop-chrome', use: { ...devices['Desktop Chrome'] } },
  ],
})
