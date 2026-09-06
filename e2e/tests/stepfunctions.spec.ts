import { test, expect } from '../fixtures/console';
import {
  createActivity,
  createStateMachine,
  startExecution,
} from '../fixtures/api';

// A machine that finishes on its own: Pass → Succeed. Executions run on the
// engine's driver goroutine (stepfunctions/engine.go), so unlike EventBridge's
// inline dispatch nothing here has "already landed" by the time a response
// comes back — every assertion on an execution's state polls.
const DEFINITION = JSON.stringify({
  StartAt: 'Prepare',
  States: {
    Prepare: { Type: 'Pass', Result: { ready: true }, ResultPath: '$.prep', Next: 'Done' },
    Done: { Type: 'Succeed' },
  },
});

// A machine that waits long enough to still be RUNNING when stop arrives.
const SLOW = JSON.stringify({
  StartAt: 'Hold',
  States: { Hold: { Type: 'Wait', Seconds: 300, End: true } },
});

test.describe('Step Functions console', () => {
  test('creating a machine, starting it, and reading its history', async ({
    page,
    request,
    uniqueName,
    setEditor,
    waitForToast,
    waitForLive,
  }) => {
    const machine = uniqueName('e2e-sfn');

    await test.step('validate refuses a broken definition, then accepts a fixed one', async () => {
      await page.goto('sfn/create');
      await page.locator('input[name="name"]').fill(machine);
      // The analyser reports every problem in one round trip; StartAt naming
      // a state that does not exist is the classic one.
      await setEditor('textarea[name="definition"]', '{"StartAt":"Nope","States":{}}');
      await page.getByRole('button', { name: 'Validate' }).click();
      await expect(page.locator('#sfn-validate-out')).toContainText('problem');

      await setEditor('textarea[name="definition"]', DEFINITION);
      await page.getByRole('button', { name: 'Validate' }).click();
      await expect(page.locator('#sfn-validate-out')).toContainText('valid');
    });

    await test.step('create lands on the machine page', async () => {
      await page.getByRole('button', { name: 'Create state machine' }).click();
      const msg = await waitForToast();
      expect(msg).toMatch(/created/);
      await expect(page).toHaveURL(new RegExp(`/sfn/${machine}$`));
      await expect(page.locator('#sfn-executions')).toContainText('No executions yet');
    });

    await test.step('start an execution and watch it finish', async () => {
      await page.goto(`sfn/${machine}?tab=start`);
      await page.locator('input[name="name"]').fill('run-1');
      await setEditor('textarea[name="input"]', '{"orderId":"A-1"}');
      await page.getByRole('button', { name: 'Start' }).click();
      await expect(page).toHaveURL(new RegExp(`/sfn/${machine}/execution/run-1$`));

      // The history is a live region: it polls until the status is terminal
      // and then pauses itself (data-live-paused). Pass → Succeed takes the
      // engine one tick, so this is about the poll landing, not the work.
      await waitForLive('#sfn-history', (t) => t.includes('SUCCEEDED'));
      const history = page.locator('#sfn-history');
      await expect(history).toContainText('ExecutionStarted');
      await expect(history).toContainText('PassStateEntered');
      await expect(history).toContainText('Prepare');
      await expect(history).toContainText('ExecutionSucceeded');
      await expect(history).toHaveAttribute('data-live-paused', '1');
    });

    await test.step('input and output are shown, and the output carries the Pass result', async () => {
      await page.goto(`sfn/${machine}/execution/run-1?tab=io`);
      const body = page.locator('.det-b');
      await expect(body).toContainText('A-1');
      await expect(body).toContainText('"ready": true');
    });

    await test.step('the executions list shows the finished run', async () => {
      await page.goto(`sfn/${machine}`);
      const row = page.locator('#sfn-executions tr', { hasText: 'run-1' });
      await expect(row).toBeVisible();
      await expect(row.locator('.badge[data-status]')).toHaveText('SUCCEEDED');
    });
  });

  test('stopping a running execution ends it ABORTED with the given error', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    waitForLive,
  }) => {
    const machine = uniqueName('e2e-sfn-slow');
    await createStateMachine(request, machine, SLOW);

    await page.goto(`sfn/${machine}?tab=start`);
    await page.locator('input[name="name"]').fill('run-1');
    await page.getByRole('button', { name: 'Start' }).click();
    await expect(page).toHaveURL(new RegExp(`/sfn/${machine}/execution/run-1$`));
    await waitForLive('#sfn-history', (t) => t.includes('WaitStateEntered'));

    await page.getByRole('button', { name: 'Stop' }).click();
    await confirmDialog('accept');
    // Not asserted through the toast: the "started" toast from a moment ago
    // is still on screen for 3.2s and waitForToast would hand that one back.
    // The status badge is the fact; expect() retries until the redirect lands.
    await expect(page.locator('#sum-sfn-exec-status .badge')).toHaveText('ABORTED');
    // Stop is a redirect, not a live tick, so the history region re-renders
    // paused with the abort recorded.
    await expect(page.locator('#sfn-history')).toContainText('ExecutionAborted');
  });

  test('editing the definition does not change an execution that already froze it', async ({
    page,
    request,
    uniqueName,
    setEditor,
    waitForToast,
    waitForLive,
  }) => {
    const machine = uniqueName('e2e-sfn-edit');
    await createStateMachine(request, machine, DEFINITION);

    await page.goto(`sfn/${machine}?tab=start`);
    await page.locator('input[name="name"]').fill('before-edit');
    await page.getByRole('button', { name: 'Start' }).click();
    await waitForLive('#sfn-history', (t) => t.includes('SUCCEEDED'));

    await page.goto(`sfn/${machine}?tab=definition`);
    await setEditor('textarea[name="definition"]', DEFINITION.replace('"ready":true', '"ready":false'));
    await page.getByRole('button', { name: 'Save definition' }).click();
    const msg = await waitForToast();
    expect(msg).toMatch(/running executions keep/);

    // DescribeStateMachineForExecution answers from the frozen snapshot.
    await page.goto(`sfn/${machine}/execution/before-edit?tab=definition`);
    await expect(page.locator('.det-b')).toContainText('"ready": true');
    await page.goto(`sfn/${machine}?tab=definition`);
    await expect(page.locator('textarea[name="definition"]')).toHaveValue(/"ready": false/);
  });

  test('publishing a version, aliasing it, and starting through the alias', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    waitForLive,
  }) => {
    const machine = uniqueName('e2e-sfn-ver');
    await createStateMachine(request, machine, DEFINITION);

    await test.step('publish freezes the definition as version 1', async () => {
      await page.goto(`sfn/${machine}?tab=versions`);
      await expect(page.locator('.det-b')).toContainText('No versions yet');
      await page.locator('form[hx-post$="/publish"] input[name="description"]').fill('first cut');
      await page.getByRole('button', { name: 'Publish version' }).click();
      const msg = await waitForToast();
      expect(msg).toMatch(/Published version 1/);
      const row = page.locator('.tbl tr', { hasText: 'first cut' });
      await expect(row).toBeVisible();
      // Clicking the version describes the version ARN: the frozen definition.
      await row.getByRole('button', { name: 'v1' }).click();
      await expect(page.locator('#sfn-version-out')).toContainText('"ready": true');
    });

    await test.step('create an alias on it', async () => {
      const form = page.locator('form[hx-post$="/alias/create"]');
      await form.locator('input[name="name"]').fill('PROD');
      await form.locator('input[name="description"]').fill('what callers pin');
      await page.getByRole('button', { name: 'Create alias' }).click();
      const msg = await waitForToast();
      expect(msg).toMatch(/Alias “PROD” created/);
      const row = page.locator('.tbl tr', { hasText: 'PROD' });
      await expect(row).toContainText('v1 100%');
      await expect(row).toContainText('what callers pin');
    });

    await test.step('start through the alias; the execution says so', async () => {
      await page.goto(`sfn/${machine}?tab=start`);
      await page.locator('select[name="target"]').selectOption('PROD');
      await page.locator('input[name="name"]').fill('via-prod');
      await page.getByRole('button', { name: 'Start' }).click();
      await expect(page).toHaveURL(new RegExp(`/sfn/${machine}/execution/via-prod$`));
      await expect(page.locator('.factstrip')).toContainText('alias PROD');
      await waitForLive('#sfn-history', (t) => t.includes('SUCCEEDED'));
    });
  });

  test('an Express machine runs synchronously and shows the result in place', async ({
    page,
    request,
    uniqueName,
    setEditor,
  }) => {
    const machine = uniqueName('e2e-sfn-express');
    await createStateMachine(request, machine, DEFINITION, { type: 'EXPRESS' });

    // No executions to list for Express — the page says so instead of
    // rendering an empty table that could never fill.
    await page.goto(`sfn/${machine}`);
    await expect(page.locator('.det-b')).toContainText('Express executions leave no record');

    await page.goto(`sfn/${machine}?tab=start`);
    await setEditor('textarea[name="input"]', '{"orderId":"X-9"}');
    await page.getByRole('button', { name: 'Run synchronously' }).click();
    const out = page.locator('#sfn-sync-out');
    await expect(out.locator('.badge[data-status]')).toHaveText('SUCCEEDED');
    await expect(out).toContainText('"ready": true');
    await expect(out).toContainText('X-9');
    await expect(out).toContainText('Billed');
  });

  test('redriving an aborted execution runs it again', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    waitForLive,
  }) => {
    const machine = uniqueName('e2e-sfn-redrive');
    await createStateMachine(request, machine, SLOW);
    await startExecution(request, machine, 'run-1');

    await page.goto(`sfn/${machine}/execution/run-1`);
    await waitForLive('#sfn-history', (t) => t.includes('WaitStateEntered'));
    // Running: no Redrive. Stopped: Redrive.
    await expect(page.getByRole('button', { name: 'Redrive' })).toHaveCount(0);
    await page.getByRole('button', { name: 'Stop' }).click();
    await confirmDialog('accept');
    await expect(page.locator('#sum-sfn-exec-status .badge')).toHaveText('ABORTED');

    await page.getByRole('button', { name: 'Redrive' }).click();
    await confirmDialog('accept');
    await expect(page.locator('#sum-sfn-exec-status .badge')).toHaveText('RUNNING');
    await expect(page.locator('#sum-sfn-exec-redrives')).toContainText('1×');
    await waitForLive('#sfn-history', (t) => t.includes('ExecutionRedriven'));
    // The history polls again — the region is no longer paused.
    await expect(page.locator('#sfn-history')).not.toHaveAttribute('data-live-paused', '1');
  });

  test('being the worker: take a task from an activity and answer it', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    waitForLive,
  }) => {
    const activity = uniqueName('e2e-approve');
    await test.step('create the activity from its page', async () => {
      await page.goto('sfn/activities');
      await page.locator('form[hx-post$="/activities/create"] input[name="name"]').fill(activity);
      await page.getByRole('button', { name: 'Create' }).click();
      const msg = await waitForToast();
      expect(msg).toMatch(/created/);
      await expect(page.locator('.tbl')).toContainText(`:activity:${activity}`);
    });

    // A machine whose one Task is the activity; its execution parks there.
    const arn = await createActivity(request, activity); // idempotent on name
    const machine = uniqueName('e2e-sfn-act');
    await createStateMachine(
      request,
      machine,
      JSON.stringify({
        StartAt: 'Work',
        States: { Work: { Type: 'Task', Resource: arn, ResultPath: '$.decision', End: true } },
      })
    );
    await startExecution(request, machine, 'order-7', { input: '{"orderId":"O-7"}' });

    await test.step('take the task, then send success with the token filled in', async () => {
      const row = page.locator('.tbl tr', { hasText: activity });
      await row.getByRole('button', { name: 'Take a task' }).click();
      const out = page.locator('#sfn-task-out');
      await expect(out).toContainText(`Task from ${activity}`, { timeout: 10000 });
      await expect(out).toContainText('O-7');
      await expect(out.locator('input[name="token"]')).not.toHaveValue('');
      await out.getByRole('button', { name: 'Send' }).click();
      await expect(out).toContainText('Task succeeded');
    });

    await test.step('the execution moved on', async () => {
      await page.goto(`sfn/${machine}/execution/order-7`);
      await waitForLive('#sfn-history', (t) => t.includes('SUCCEEDED'));
      await expect(page.locator('#sfn-history')).toContainText('ActivitySucceeded');
    });
  });
});
