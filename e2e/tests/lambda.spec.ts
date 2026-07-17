import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { createQueue, postForm } from '../fixtures/api';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// waitForToast's `.last()` locator is only race-free when at most one toast
// is alive at a time — if two toast-producing actions fire back-to-back with
// no navigation between them, the previous toast can still be visible when
// we look for the next one. Draining the current one first (as kms.spec.ts
// does) makes the next waitForToast() call unambiguous.
async function waitToastGone(page: Page) {
  await expect(page.locator('.toast:not(.err)').last()).toBeHidden({ timeout: 8000 });
}

// Lambda console coverage: create-from-UI against a REAL invokable function
// (a hand-rolled Runtime Interface Client "bootstrap", built below), the
// "at a glance" panel, synchronous invoke + the warm/idle runtime badge,
// config edit persistence, an SQS event-source-mapping trigger, the function
// URL create/delete surface, async (Event) invoke, and function delete.
//
// `console/templates/lambda.html` is the source of truth for selectors;
// `console/handlers_lambda.go` / `lambda/*.go` for routes and semantics.
//
// This spec drives one function through its whole lifecycle in order, so
// tests run serially (fullyParallel is the project default, which would
// otherwise let these race each other over the same function/queue/log file).
test.describe.configure({ mode: 'serial' });

// ---- the fixture handler: a real Lambda Runtime Interface Client ----
//
// internal/lambdaruntime (see its package doc) runs a "provided.al2"
// function by exec'ing a `bootstrap` binary in the code directory with
// AWS_LAMBDA_RUNTIME_API pointing at a loopback Runtime API server. This
// fixture (source in e2e/fixtures/lambda-handler/main.go) speaks that
// protocol for real and echoes the event back as
// {"echoed": <event>, "requestId": "<id>"}, plus appends one JSONL line per
// invocation to invocations.log in its own directory — the only way to
// observe an invocation that has no other externally visible side effect
// (e.g. one delivered asynchronously via an SQS trigger).
//
// The binary is a build artifact and must never be committed: it's built
// here into e2e/.tmp/lambda-fixture/ (already gitignored), keeping
// e2e/fixtures/ free of build output. See fixtures/lambda-handler/README.md.
const FIXTURE_SRC = path.resolve(__dirname, '../fixtures/lambda-handler');
const FIXTURE_DIR = path.resolve(__dirname, '../.tmp/lambda-fixture');
const LOG_FILE = path.join(FIXTURE_DIR, 'invocations.log');

test.beforeAll(() => {
  fs.mkdirSync(FIXTURE_DIR, { recursive: true });
  // GOWORK=off: this repo sits inside a local multi-repo go.work (see
  // playwright.config.ts's webServer command, which does the same) — without
  // it, Go's workspace mode refuses to build a directory whose go.mod isn't
  // listed in that workspace's `use` block.
  execFileSync('go', ['build', '-o', path.join(FIXTURE_DIR, 'bootstrap'), '.'], {
    cwd: FIXTURE_SRC,
    env: { ...process.env, GOWORK: 'off' },
  });
  // Start every run from a clean invocation log so marker-based assertions
  // below can't match a stale line from a previous run against this
  // persistent, shared data dir.
  fs.rmSync(LOG_FILE, { force: true });
});

/** Polls invocations.log until it contains `marker`, proving an invocation
 *  actually reached the fixture handler even when the console UI has
 *  nothing else to show (async / trigger-driven invokes). */
async function waitForLogMarker(marker: string, timeoutMs = 15000) {
  await expect
    .poll(
      () => (fs.existsSync(LOG_FILE) ? fs.readFileSync(LOG_FILE, 'utf8') : ''),
      { timeout: timeoutMs, message: `invocations.log never contained ${marker}` }
    )
    .toContain(marker);
}

test.describe('lifecycle', () => {
  let fnName: string;
  let queueName: string;

  test('creates a function via the UI form and shows its config before any invoke', async ({
    page,
    uniqueName,
  }) => {
    fnName = uniqueName('lam');

    await page.goto('lambda/create');
    await page.locator('input[name="name"]').fill(fnName);
    // Runtime defaults to the first <option>, provided.al2 (Go / any
    // binary); Handler defaults to "bootstrap" — both left as-is.
    await page.locator('input[name="code"]').fill(FIXTURE_DIR);
    await page.locator('input[name="timeout"]').fill('20');
    await page.locator('input[name="memory"]').fill('256');
    await page.getByRole('button', { name: 'Create function' }).click();

    await page.waitForURL(new RegExp(`/lambda/${fnName}(\\?|$)`));

    // Listed in the shared sidebar.
    await expect(page.locator('.li', { hasText: fnName })).toBeVisible();
    await expect(page.locator('.det-title')).toContainText(fnName);
    await expect(page.locator('.badge.type')).toContainText('provided.al2');

    // "At a glance" panel — populated before any invoke has happened.
    const glance = page.locator('#invoke-result');
    await expect(glance.locator('.tbl.kv tr', { hasText: 'Runtime' })).toContainText(
      'provided.al2'
    );
    await expect(glance.locator('.tbl.kv tr', { hasText: 'Handler' })).toContainText('bootstrap');
    await expect(glance.locator('.tbl.kv tr', { hasText: 'Timeout' })).toContainText('20 s');
    await expect(glance.locator('.tbl.kv tr', { hasText: 'Memory' })).toContainText('256 MB');
    await expect(glance.locator('.tbl.kv tr', { hasText: 'Triggers' })).toContainText('none');

    // No process has run yet — the runtime badge reads Idle/cold.
    const badge = page.locator('#lambda-rt');
    await expect(badge).toHaveClass(/rt-cold/);
    await expect(badge).toContainText('Idle');
  });

  test('invokes synchronously, echoes the payload, and the runtime badge goes warm and stays warm', async ({
    page,
    setEditor,
    waitForLive,
  }) => {
    await page.goto(`lambda/${fnName}`);

    const payloadSelector = 'textarea[name=payload][data-editor]';
    await setEditor(payloadSelector, JSON.stringify({ hello: 'world' }));
    await page.getByRole('button', { name: 'Invoke' }).click();

    const result = page.locator('#invoke-result');
    // Cold start: spawning the process for the first time gets extra budget.
    await expect(result.locator('.co-h')).toContainText('succeeded', { timeout: 20000 });
    await expect(result.locator('.co-h')).toContainText(/\d/); // a duration is shown
    await expect(result.locator('.co-h')).toContainText(/\d+(\.\d+)?(ms|s)/);
    await expect(result.locator('pre').first()).toContainText('"hello": "world"');
    await expect(result.locator('pre').first()).toContainText('"requestId"');

    // The process is now warm — the live-polled badge (data-live, 3s) morphs
    // from Idle/rt-cold to Warm/rt-warm without a reload.
    await waitForLive('#lambda-rt', (text) => text.includes('Warm'), { timeout: 10000 });
    const badge = page.locator('#lambda-rt');
    await expect(badge).toHaveClass(/rt-warm/);
    await expect(badge).toHaveAttribute('title', /warm process/);

    // Invoke again: this is the SAME process (no cold start to observe a
    // second time) — the badge stays warm, demonstrating reuse rather than
    // a respawn per invoke.
    await setEditor(payloadSelector, JSON.stringify({ hello: 'again' }));
    await page.getByRole('button', { name: 'Invoke' }).click();
    await expect(result.locator('.co-h')).toContainText('succeeded', { timeout: 10000 });
    await expect(result.locator('pre').first()).toContainText('"hello": "again"');
    await expect(badge).toHaveClass(/rt-warm/);
    await expect(badge).toContainText('Warm');
  });

  test('edits configuration and the new values persist across reload', async ({ page, waitForToast }) => {
    await page.goto(`lambda/${fnName}?tab=config`);

    await page.locator('input[name="timeout"]').fill('45');
    await page.locator('input[name="memory"]').fill('320');
    await page.getByRole('button', { name: 'Add variable' }).click();
    const row = page.locator('.attr-row').last();
    await row.locator('input[name="env_key"]').fill('GREETING');
    await row.locator('input[name="env_val"]').fill('hi');

    await page.getByRole('button', { name: 'Save configuration' }).click();
    const toast = await waitForToast();
    expect(toast).toContain('Configuration saved');

    // Reflected immediately in the swapped partial... (env rows are plain
    // <input>s with no text nodes, so assert on .value, not textContent)
    await expect(page.locator('input[name="timeout"]')).toHaveValue('45');
    await expect(page.locator('input[name="memory"]')).toHaveValue('320');
    await expect(page.locator('input[name="env_key"]')).toHaveValue('GREETING');
    await expect(page.locator('input[name="env_val"]')).toHaveValue('hi');

    // ...and still there after a full reload (proves it round-tripped
    // through the store, not just the in-page Alpine state).
    await page.reload();
    await expect(page.locator('input[name="timeout"]')).toHaveValue('45');
    await expect(page.locator('input[name="memory"]')).toHaveValue('320');
    const keyInputs = page.locator('input[name="env_key"]');
    await expect(keyInputs).toHaveCount(1);
    await expect(keyInputs.first()).toHaveValue('GREETING');
    await expect(page.locator('input[name="env_val"]').first()).toHaveValue('hi');
  });

  test('an SQS trigger invokes the function asynchronously', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    queueName = await createQueue(request, uniqueName('lam-trig'));

    await page.goto(`lambda/${fnName}?tab=triggers`);
    await expect(page.locator('#lambda-triggers')).toContainText('No triggers yet');

    await page.locator('select[name="queue"]').selectOption(queueName);
    await page.locator('input[name="batch"]').fill('5');
    await page.getByRole('button', { name: 'Add trigger' }).click();
    const addToast = await waitForToast();
    expect(addToast).toContain('Trigger added');

    const triggers = page.locator('#lambda-triggers');
    await expect(triggers).toContainText(queueName);
    await expect(triggers.locator('td.right.num')).toContainText('5');
    await expect(triggers.locator('.badge.state-on')).toContainText('Enabled');

    // The "at a glance" panel on the Test tab also reflects the new trigger.
    await page.goto(`lambda/${fnName}`);
    await expect(
      page.locator('#invoke-result .tbl.kv tr', { hasText: 'Triggers' })
    ).toContainText('1 event source mapping');

    // Send a message straight to the queue (bypassing the console's own SQS
    // send UI, which sqs.spec.ts already covers) and prove the poller picked
    // it up and invoked the function — the only observable evidence is the
    // fixture handler's own invocation log, since an async/ESM invoke has no
    // return payload for the console to show.
    const marker = `esm-trigger-${Date.now()}`;
    await postForm(request, `sqs/${queueName}/send`, { body: JSON.stringify({ marker }) });
    await waitForLogMarker(marker);
    const logged = fs.readFileSync(LOG_FILE, 'utf8');
    const line = logged.split('\n').find((l) => l.includes(marker))!;
    const rec = JSON.parse(line);
    // The runtime wraps SQS deliveries as a Records batch (real Lambda's SQS
    // event shape) — confirm the message body made it all the way through.
    expect(JSON.stringify(rec.event)).toContain('"eventSource":"aws:sqs"');
    expect(JSON.stringify(rec.event)).toContain(marker);
  });

  test('creates and removes a function URL', async ({ page, waitForToast }) => {
    await page.goto(`lambda/${fnName}?tab=config`);

    await expect(page.getByText('No function URL.')).toBeVisible();
    await page.getByRole('button', { name: 'Create function URL' }).click();
    const createToast = await waitForToast();
    expect(createToast).toContain('Function URL created');

    // doze-aws shapes this as a real AWS function-URL string
    // (http://{name}.lambda-url.local/) for UI/API parity, but — unlike the
    // AWS builtins' shared :80 ingress documented for the `core` daemon —
    // this standalone doze-aws server has no Host-routed listener for
    // *.lambda-url.local (confirmed: no handler anywhere keys off r.Host).
    // It exists purely as a config-surface value here, so this test asserts
    // the URL is shown/removed correctly rather than actually invoking it
    // over HTTP, which nothing in this codebase serves yet.
    const urlText = page.locator('.copy-row .mono');
    await expect(urlText).toHaveText(new RegExp(`http://${fnName}\\.lambda-url\\.local/`));
    await waitToastGone(page);

    await page.getByRole('button', { name: 'Remove URL' }).click();
    await page.locator('#confirm-yes').click();
    const removeToast = await waitForToast();
    expect(removeToast).toContain('Function URL removed');
    await expect(page.getByText('No function URL.')).toBeVisible();
  });

  test('async (Event) invoke returns an accepted receipt, not a result', async ({
    page,
    setEditor,
  }) => {
    await page.goto(`lambda/${fnName}`);

    const marker = `async-invoke-${Date.now()}`;
    await setEditor('textarea[name=payload][data-editor]', JSON.stringify({ marker }));
    await page.locator('.mini-check input[type="checkbox"]').check();
    await page.getByRole('button', { name: 'Send event' }).click();

    const result = page.locator('#invoke-result');
    await expect(result).toContainText('Event accepted', { timeout: 10000 });
    await expect(result).toContainText('queued for async execution');
    // No synchronous payload/duration panel for an async invoke.
    await expect(result.locator('.co-h')).toHaveCount(0);

    // It still actually ran — just asynchronously, off the request.
    await waitForLogMarker(marker);
  });

  test('deletes the trigger, then the function', async ({ page, waitForToast, confirmDialog }) => {
    await page.goto(`lambda/${fnName}?tab=triggers`);
    await page.getByTitle('Remove trigger').click();
    await confirmDialog('accept');
    const triggerToast = await waitForToast();
    expect(triggerToast).toContain('Trigger removed');
    await expect(page.locator('#lambda-triggers')).toContainText('No triggers yet');

    await page.goto(`lambda/${fnName}`);
    await page.getByRole('button', { name: 'Delete' }).click();
    await confirmDialog('accept');
    // Unlike most delete routes, lambdaDelete fires an HX-Trigger toast on
    // the SAME response as HX-Redirect rather than a flash-query-param
    // redirect — the navigation away can beat the toast onto the screen, so
    // (matching sqs.spec.ts's/s3.spec.ts's own delete-and-redirect tests)
    // this asserts the redirect and list removal, not toast text.
    await page.waitForURL(/\/lambda$/);
    await expect(page.locator('.li', { hasText: fnName })).toHaveCount(0);
  });
});
