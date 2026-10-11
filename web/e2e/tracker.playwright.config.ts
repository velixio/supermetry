import { defineConfig, devices } from '@playwright/test'

const port = Number(process.env.SUPERMETRY_TRACKER_E2E_PORT ?? '28149')

export default defineConfig({
  testDir: '.',
  testMatch: 'tracker.spec.ts',
  workers: 1,
  reporter: 'list',
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    colorScheme: 'light',
    reducedMotion: 'reduce',
  },
  projects: [{ name: 'chromium', use: devices['Desktop Chrome'] }],
  webServer: {
    command: 'bun tracker-server.ts',
    url: `http://127.0.0.1:${port}/readyz`,
    reuseExistingServer: false,
  },
})
