import { defineConfig } from '@playwright/test';

const BASE_URL = process.env.GONACOS_URL || 'http://127.0.0.1:18848';

export default defineConfig({
  testDir: './specs',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  retries: 0,
  workers: 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: BASE_URL,
    headless: true,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'desktop-chromium',
      use: { browserName: 'chromium', viewport: { width: 1280, height: 720 } },
    },
  ],
  webServer: {
    command: process.env.GONACOS_BINARY
      ? `${process.env.GONACOS_BINARY} serve 127.0.0.1:18848`
      : '/tmp/gonacos-test serve 127.0.0.1:18848',
    url: `${BASE_URL}/v3/console/health/readiness`,
    reuseExistingServer: true,
    timeout: 30_000,
    cwd: process.env.GONACOS_CWD || process.cwd(),
  },
});
