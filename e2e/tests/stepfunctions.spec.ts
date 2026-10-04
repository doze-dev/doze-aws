import { test, expect } from '../fixtures/console';
import {
  createActivity,
  createAlias,
  createStateMachine,
  postForm,
  publishVersion,
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

    await test.step('a history row opens into a band without moving the columns', async () => {
      await page.goto(`sfn/${machine}/execution/run-1`);
      const head = page.locator('.sfn-hist thead th');
      const before = await head.evaluateAll((ths) => ths.map((th) => th.getBoundingClientRect().width));
      const entered = page.locator('.sfn-hist tr.sfn-x', { hasText: 'PassStateEntered' });
      await entered.click();
      const band = page.locator('.sfn-hist tr.sfn-det:visible');
      await expect(band).toHaveCount(1);
      await expect(band).toContainText('"orderId": "A-1"');
      const after = await head.evaluateAll((ths) => ths.map((th) => th.getBoundingClientRect().width));
      expect(after).toEqual(before);
      // Time and elapsed read as a clock and a span, not "1 h ago".
      await expect(entered.locator('td').nth(4)).toHaveText(/^\d\d:\d\d:\d\d\.\d{3}$/);
      await expect(entered.locator('td').nth(5)).toContainText('+');
      await entered.click();
      await expect(page.locator('.sfn-hist tr.sfn-det:visible')).toHaveCount(0);
    });

    await test.step('the graph pans, zooms about the pointer, and fits', async () => {
      await page.goto(`sfn/${machine}/execution/run-1?tab=graph`);
      const view = page.locator('svg.graph g.gv');
      await expect(view).toHaveAttribute('transform', /scale\(/);
      const fitted = await view.getAttribute('transform');
      const wrap = page.locator('.graph-wrap');
      const box = (await wrap.boundingBox())!;
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
      await page.mouse.wheel(0, -300);
      await expect(view).not.toHaveAttribute('transform', fitted!);
      const zoomed = await view.getAttribute('transform');
      await page.mouse.down();
      await page.mouse.move(box.x + box.width / 2 - 80, box.y + box.height / 2 - 40, { steps: 6 });
      await page.mouse.up();
      await expect(view).not.toHaveAttribute('transform', zoomed!);
      // The drag that just ended is not a click on the node under it.
      await expect(page.locator('.gn.gn-sel')).toHaveCount(0);
      await page.locator('[data-graph-act="fit"]').click();
      await expect(view).toHaveAttribute('transform', fitted!);
      // A click on a state selects it and lights its history rows.
      await page.locator('.gn[data-state="Prepare"]').click();
      await expect(page.locator('.gn[data-state="Prepare"]')).toHaveClass(/gn-sel/);
      await expect(page.locator('.sfn-hist tr.hl[data-state="Prepare"]').first()).toBeVisible();
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

// ---- route coverage: the controls the tests above do not press ----

// Pass → Wait briefly → Succeed: RUNNING long enough for a page to open on it,
// short enough that the live regions watch it finish.
const BRIEF = JSON.stringify({
  StartAt: 'Prepare',
  States: {
    Prepare: { Type: 'Pass', Next: 'Hold' },
    Hold: { Type: 'Wait', Seconds: 3, Next: 'Done' },
    Done: { Type: 'Succeed' },
  },
});

test.describe('Step Functions console: live regions', () => {
  test('the executions list fills in an execution started elsewhere', async ({
    page,
    request,
    uniqueName,
  }) => {
    const machine = uniqueName('e2e-sfn-live');
    await createStateMachine(request, machine, DEFINITION);

    await page.goto(`sfn/${machine}`);
    await expect(page.locator('#sfn-executions')).toContainText('No executions yet');
    // Started behind the page's back: only the poll can bring it in.
    await startExecution(request, machine, 'from-sdk');
    const row = page.locator('#sfn-executions tr', { hasText: 'from-sdk' });
    await expect(row).toBeVisible({ timeout: 15000 });
    await expect(row.locator('.badge[data-status]')).toHaveText('SUCCEEDED', { timeout: 15000 });
  });

  test('an Express machine’s Logs tab tails the history it vends', async ({
    page,
    request,
    uniqueName,
  }) => {
    const machine = uniqueName('e2e-sfn-xlogs');
    await createStateMachine(request, machine, DEFINITION, { type: 'EXPRESS' });

    await page.goto(`sfn/${machine}?tab=logs`);
    await expect(page.locator('.det-b .panel-h')).toContainText(`/aws/vendedlogs/states/${machine}`);
    const tail = page.locator('#log-tail');
    await expect(tail).toBeVisible();
    await expect(tail).not.toContainText('ExecutionSucceeded');
    // A synchronous run from elsewhere; the tail's poll picks its lines up.
    await postForm(request, `sfn/${machine}/start-sync`, { input: '{"orderId":"L-1"}' });
    await expect(tail).toContainText('ExecutionSucceeded', { timeout: 15000 });
  });

  test('the execution graph colours states in as the execution moves', async ({
    page,
    request,
    uniqueName,
  }) => {
    const machine = uniqueName('e2e-sfn-gr');
    await createStateMachine(request, machine, BRIEF);
    await startExecution(request, machine, 'run-1');

    await page.goto(`sfn/${machine}/execution/run-1?tab=graph`);
    // Opened mid-flight: Done has not been entered yet.
    await expect(page.locator('.gn[data-state="Hold"]')).toHaveClass(/gn-running/);
    await expect(page.locator('.gn[data-state="Done"]')).not.toHaveClass(/gn-succeeded/);
    // Only the graph's poll can repaint it.
    await expect(page.locator('.gn[data-state="Done"]')).toHaveClass(/gn-succeeded/, { timeout: 15000 });
    await expect(page.locator('.gn[data-state="Hold"]')).toHaveClass(/gn-succeeded/);
    await expect(page.locator('#sfn-graph')).toHaveAttribute('data-live-paused', '1');
  });
});

test.describe('Step Functions console: machine management', () => {
  test('deleting a machine returns to the list without it', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    waitForToast,
  }) => {
    const machine = uniqueName('e2e-sfn-del');
    await createStateMachine(request, machine, DEFINITION);

    await page.goto(`sfn/${machine}`);
    await page.getByRole('button', { name: 'Delete' }).click();
    await confirmDialog('accept');
    const msg = await waitForToast();
    expect(msg).toMatch(/State machine deleted/);
    await page.waitForURL(/\/sfn$/);
    await expect(page.locator('.listpane')).not.toContainText(machine);
  });

  test('testing one state of the definition shows its output and next state', async ({
    page,
    request,
    uniqueName,
    setEditor,
  }) => {
    const machine = uniqueName('e2e-sfn-ts');
    await createStateMachine(request, machine, DEFINITION);

    await page.goto(`sfn/${machine}?tab=definition`);
    await page.locator('select[name="state"]').selectOption('Prepare');
    await page.locator('select[name="level"]').selectOption('DEBUG');
    await setEditor('form[hx-post$="/test-state"] textarea[name="input"]', '{"orderId":"T-1"}');
    await page.getByRole('button', { name: 'Run state' }).click();
    const out = page.locator('#sfn-test-out');
    await expect(out.locator('.badge[data-status]')).toHaveText('SUCCEEDED');
    await expect(out.locator('.fact', { hasText: 'Next' })).toContainText('Done');
    await expect(out).toContainText('"ready": true');
    await expect(out).toContainText('T-1');
    await expect(out).toContainText('Inspection');
  });

  test('versions and aliases: re-route an alias, delete it, then delete the version', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    waitForToast,
  }) => {
    const machine = uniqueName('e2e-sfn-va');
    await createStateMachine(request, machine, DEFINITION);
    await publishVersion(request, machine, 'one');
    // A second revision, so the second publish is a second version.
    await postForm(request, `sfn/${machine}/definition`, {
      definition: DEFINITION.replace('"ready":true', '"ready":false'),
    });
    await publishVersion(request, machine, 'two');
    await createAlias(request, machine, 'LIVE', [{ version: 1 }]);

    await page.goto(`sfn/${machine}?tab=versions`);
    const aliasRow = page.locator('.tbl tr', { hasText: 'LIVE' });
    await expect(aliasRow).toContainText('v1 100%');

    await test.step('edit the routing inline to point at v2', async () => {
      await aliasRow.getByRole('button', { name: 'Edit routing of LIVE' }).click();
      const form = aliasRow.locator('form');
      await expect(form).toBeVisible();
      await form.locator('input[name="v1"]').fill('2');
      await form.getByRole('button', { name: 'Save' }).click();
      const msg = await waitForToast();
      expect(msg).toMatch(/Alias “LIVE” updated/);
      await expect(page.locator('.tbl tr', { hasText: 'LIVE' })).toContainText('v2 100%');
    });

    await test.step('v1 is free now: delete it', async () => {
      await page.getByRole('button', { name: 'Delete version 1' }).click();
      await confirmDialog('accept');
      const msg = await waitForToast();
      expect(msg).toMatch(/Version 1 deleted/);
      await expect(page.getByRole('button', { name: 'v1', exact: true })).toHaveCount(0);
      await expect(page.getByRole('button', { name: 'v2', exact: true })).toBeVisible();
    });

    await test.step('delete the alias', async () => {
      await page.getByRole('button', { name: 'Delete alias LIVE' }).click();
      await confirmDialog('accept');
      const msg = await waitForToast();
      expect(msg).toMatch(/Alias “LIVE” deleted/);
      await expect(page.getByText('No aliases.')).toBeVisible();
    });
  });

  test('deleting an activity removes it from the list', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    waitForToast,
  }) => {
    const activity = uniqueName('e2e-act-del');
    await createActivity(request, activity);

    await page.goto('sfn/activities');
    await expect(page.locator('.tbl tr', { hasText: activity })).toBeVisible();
    await page.getByRole('button', { name: `Delete activity ${activity}` }).click();
    await confirmDialog('accept');
    const msg = await waitForToast();
    expect(msg).toMatch(new RegExp(`Activity “${activity}” deleted`));
    await expect(page.locator('.tbl tr', { hasText: activity })).toHaveCount(0);
  });
});

test.describe('Step Functions console: being the worker', () => {
  test('heartbeat a task taken from an activity', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const activity = uniqueName('e2e-hb-act');
    const arn = await createActivity(request, activity);
    const machine = uniqueName('e2e-sfn-hb');
    await createStateMachine(
      request,
      machine,
      JSON.stringify({
        StartAt: 'Work',
        States: { Work: { Type: 'Task', Resource: arn, HeartbeatSeconds: 60, End: true } },
      })
    );
    await startExecution(request, machine, 'hb-1');

    await page.goto('sfn/activities');
    await page.locator('.tbl tr', { hasText: activity }).getByRole('button', { name: 'Take a task' }).click();
    const out = page.locator('#sfn-task-out');
    await expect(out).toContainText(`Task from ${activity}`, { timeout: 10000 });
    // Taking a task toasts too; consume it so the next wait is the heartbeat's.
    expect(await waitForToast()).toMatch(/Took a task/);
    await out.getByRole('button', { name: 'Heartbeat' }).click();
    const msg = await waitForToast();
    expect(msg).toMatch(/Heartbeat sent/);
    // A heartbeat answers nothing: the task is still the worker's to finish.
    await expect(out).toContainText(`Task from ${activity}`);
    await out.getByRole('button', { name: 'Send' }).click();
    await expect(out).toContainText('Task succeeded');
  });

  test('a .waitForTaskToken task answered from the execution page: heartbeat, then failure', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    waitForLive,
  }) => {
    const machine = uniqueName('e2e-sfn-tok');
    // putEvents to the default bus needs nothing else to exist; the token
    // rides in the Parameters, which is where the history shows it.
    await createStateMachine(
      request,
      machine,
      JSON.stringify({
        StartAt: 'Ask',
        States: {
          Ask: {
            Type: 'Task',
            Resource: 'arn:aws:states:::events:putEvents.waitForTaskToken',
            Parameters: {
              Entries: [{ Source: 'e2e', DetailType: 'approval', Detail: { 'TaskToken.$': '$$.Task.Token' } }],
            },
            End: true,
          },
        },
      })
    );
    await startExecution(request, machine, 'ask-1');

    await page.goto(`sfn/${machine}/execution/ask-1`);
    await waitForLive('#sfn-history', (t) => t.includes('TaskSubmitted') || t.includes('TaskScheduled'));

    // The worker's view: open the TaskScheduled row, copy its token.
    const scheduled = page.locator('.sfn-hist tr.sfn-x', { hasText: 'TaskScheduled' });
    await scheduled.click();
    const tokenCell = page.locator('.sfn-hist tr.sfn-det:visible .sfn-det-token .sfn-det-body');
    await expect(tokenCell).not.toBeEmpty();
    const token = ((await tokenCell.textContent()) ?? '').trim();
    expect(token.length).toBeGreaterThan(10);

    const form = page.locator('#sfn-history form[hx-post$="/task-result"]');
    await form.locator('input[name="token"]').fill(token);
    await form.getByRole('button', { name: 'Heartbeat' }).click();
    let msg = await waitForToast();
    expect(msg).toMatch(/Heartbeat sent/);
    await expect(page.locator('#sum-sfn-exec-status .badge')).toHaveText('RUNNING');

    await form.getByRole('link', { name: 'Failure' }).click();
    await form.locator('input[name="error"]').fill('Human.Rejected');
    await form.locator('input[name="cause"]').fill('nope');
    await form.getByRole('button', { name: 'Send' }).click();
    msg = await waitForToast();
    expect(msg).toMatch(/Task failed/);
    await expect(page.locator('#sfn-history')).toContainText('FAILED');
    await expect(page.locator('#sfn-history')).toContainText('Human.Rejected');
  });

  test('a distributed Map run: raise its concurrency and list its children', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const machine = uniqueName('e2e-sfn-map');
    await createStateMachine(
      request,
      machine,
      JSON.stringify({
        StartAt: 'Fan',
        States: {
          Fan: {
            Type: 'Map', End: true, Label: 'items', MaxConcurrency: 1,
            ItemProcessor: {
              ProcessorConfig: { Mode: 'DISTRIBUTED', ExecutionType: 'STANDARD' },
              StartAt: 'Slow',
              States: { Slow: { Type: 'Wait', Seconds: 300, End: true } },
            },
          },
        },
      })
    );
    await startExecution(request, machine, 'fan-1', { input: '[1,2,3]' });

    try {
      await page.goto(`sfn/${machine}/execution/fan-1`);
      const run = page.locator('.sfn-maprun');
      await expect(run).toBeVisible({ timeout: 15000 });
      await expect(run.locator('[data-maprun-status]')).toHaveText('RUNNING');
      await expect(run.locator('input[name="max"]')).toHaveValue('1');

      await run.locator('input[name="max"]').fill('2');
      await run.getByRole('button', { name: 'Set concurrency' }).click();
      const msg = await waitForToast();
      expect(msg).toMatch(/Concurrency set to 2/);
      await expect(page.locator('.sfn-maprun input[name="max"]')).toHaveValue('2');

      await page.locator('.sfn-maprun').getByRole('button', { name: 'Child executions' }).click();
      const children = page.locator('#sfn-map-children');
      await expect(children).toContainText('Child executions');
      // Concurrency 2 now: a second child launches, and the list fills in.
      await expect(children.locator('tbody tr')).toHaveCount(2, { timeout: 15000 });
      await expect(children.locator('.badge[data-status]').first()).toHaveText('RUNNING');
    } finally {
      await postForm(request, `sfn/${machine}/execution/fan-1/stop`, {});
    }
  });
});
