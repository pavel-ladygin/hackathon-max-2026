import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './tests/e2e',
  testMatch: 'motion.spec.ts',
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  retries: 0,
  use: {
    baseURL: 'http://127.0.0.1:4173',
    trace: 'on',
    video: 'on',
  },
  webServer: {
    command: 'VITE_YANDEX_MAPS_API_KEY=e2e-motion npm run dev -- --host 127.0.0.1',
    url: 'http://127.0.0.1:4173',
    reuseExistingServer: true,
  },
  projects: [
    { name: 'motion-mobile', use: { ...devices['Pixel 7'] } },
    { name: 'motion-desktop', use: { ...devices['Desktop Chrome'] } },
  ],
})
