import { defineConfig, devices } from '@playwright/test'

const baseURL = process.env.GENERIC_E2E_BASE_URL

if (!baseURL) throw new Error('GENERIC_E2E_BASE_URL must point to the live Go handler test server')

export default defineConfig({
  testDir: './tests/e2e',
  testMatch: 'event-sources-live.spec.ts',
  fullyParallel: false,
  workers: 1,
  timeout: 45_000,
  use: { baseURL, trace: 'retain-on-failure' },
  projects: [{ name: 'desktop-chrome-live', use: { ...devices['Desktop Chrome'] } }],
})
