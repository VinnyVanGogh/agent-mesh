import { defineConfig, devices } from '@playwright/test';

// Started by scripts/ui-e2e.sh, which exports the throwaway daemon's URL.
const baseURL = process.env.STAYPOINT_UI_BASE_URL;
if (!baseURL) {
  throw new Error('STAYPOINT_UI_BASE_URL is not set: run the suite via scripts/ui-e2e.sh');
}

const artifacts = process.env.STAYPOINT_UI_ARTIFACTS || 'artifacts';

export default defineConfig({
  testDir: './specs',
  outputDir: `${artifacts}/test-results`,
  // Specs share one daemon and seed their own uniquely named fixtures, but the
  // UI keeps polling and SSE state per page; serial keeps failures readable.
  workers: 1,
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: [
    ['list'],
    ['html', { outputFolder: `${artifacts}/html-report`, open: 'never' }],
  ],
  use: {
    baseURL,
    headless: true,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    video: 'off',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
