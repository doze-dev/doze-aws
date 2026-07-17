import { defineConfig, devices } from '@playwright/test';

// Deliberately off the default 4566 so this never collides with a dev
// instance of doze-aws already running on the machine.
const PORT = 14566;
export const BASE_URL = `http://127.0.0.1:${PORT}/_console/`;

export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 4 : undefined,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',

  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },

  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],

  // Builds the real doze-aws binary and boots it on a fixed port against an
  // isolated data dir. `cd e2e/.tmp` before exec so the process's CWD never
  // sees a stray stack.yaml/doze-aws.toml that doze-aws auto-loads.
  webServer: {
    command:
      'sh -c "cd .. && GOWORK=off go build -o e2e/.tmp/bin/doze-aws ./cmd/doze-aws && ' +
      'cd e2e/.tmp && mkdir -p data && ' +
      `./bin/doze-aws --listen 127.0.0.1:${PORT} --data-dir data --console"`,
    url: BASE_URL,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
});
