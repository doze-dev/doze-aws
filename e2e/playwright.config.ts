import { defineConfig, devices } from '@playwright/test';

// The suite runs with --listen, so it needs no DNS and claims no .doze name —
// which is what makes it safe in CI and on a laptop at the same time.
//
// The port is deliberately off 4566 so it never collides with a dev instance
// already running on the machine. It is not "the default port" any more:
// doze-aws answers on its name by default and binds no address at all.
//
// E2E_PORT moves it, so two runs (one spec each, say) can share a machine;
// each port gets its own binary and data dir.
const PORT = Number(process.env.E2E_PORT ?? 14566);
// Every console route a request lands on is written here; route-gate.ts
// reads it after the run.
export const ROUTES_FILE = `.tmp/routes-${PORT}.txt`;
// E2E_ACCOUNT_ID and E2E_REGION boot the instance under another identity, so a
// page that writes the default into an ARN, a URL or a test event shows up as a
// failure here rather than in somebody else's account (plan item B2, A8). Unset
// is the default identity, which is what every ordinary run uses.
const IDENTITY_FLAGS =
  (process.env.E2E_ACCOUNT_ID ? ` --account-id ${process.env.E2E_ACCOUNT_ID}` : '') +
  (process.env.E2E_REGION ? ` --region ${process.env.E2E_REGION}` : '');
// The identity the instance was booted with, for a test that asserts what a page
// prints. Tests that merely send an ARN in are unaffected: it comes back as sent.
export const ACCOUNT_ID = process.env.E2E_ACCOUNT_ID ?? '000000000000';
export const REGION = process.env.E2E_REGION ?? 'us-east-1';
export const ORIGIN = `http://127.0.0.1:${PORT}`;
export const BASE_URL = `${ORIGIN}/_console/`;

export default defineConfig({
  testDir: './tests',
  globalSetup: './route-gate.ts',
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
      // -tags e2e records which console routes were reached (route-gate.ts).
      `sh -c "cd .. && GOWORK=off go build -tags e2e -o e2e/.tmp/bin/doze-aws-${PORT} ./cmd/doze-aws && ` +
      // A FRESH data dir every run. It used to persist, so resources piled up
      // across runs and tests began interfering with each other — the failing
      // set shifted between identical runs, and three KMS tests "failed" purely
      // from accumulated state. A suite whose result depends on how many times
      // it has been run before cannot tell you anything.
      `cd e2e/.tmp && rm -rf data-${PORT} routes-${PORT}.txt && mkdir -p data-${PORT} && ` +
      `DOZE_E2E_ROUTES=routes-${PORT}.txt ./bin/doze-aws-${PORT} --listen 127.0.0.1:${PORT} --data-dir data-${PORT} --console${IDENTITY_FLAGS}"`,
    url: BASE_URL,
    // Never reuse. The data dir is wiped in the boot command, so a reused
    // server KEEPS its state and the wipe never runs — resources accumulated
    // across runs until drawer-open flows started failing nondeterministically
    // (a different S3 test each run). A rebuild costs ~5s; a flaky suite costs
    // every diagnosis that trusts it.
    reuseExistingServer: false,
    timeout: 60_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
});
