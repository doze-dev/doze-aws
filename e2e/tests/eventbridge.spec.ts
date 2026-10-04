import { test, expect } from '../fixtures/console';
import { createBus, createQueue, postForm } from '../fixtures/api';

// EventBridge: bus -> rule (pattern match) -> SQS target, the live test-event
// matcher, real delivery via PutEvents, enable/disable, schedule-only rules
// (no pattern), and archive/replay. One long flow because most steps build on
// state (bus, rule, target) created by the previous one — see shell.spec.ts's
// "styled confirm + toasts" test for the same shared-state style.
//
// The rule pattern matches on detail.orderId == "A-1042" (this also happens
// to be the #eb-event-form Detail textarea's own placeholder value).
const PATTERN = JSON.stringify({ detail: { orderId: ['A-1042'] } });
const MATCHING_DETAIL = JSON.stringify({ orderId: 'A-1042' });
const NON_MATCHING_DETAIL = JSON.stringify({ orderId: 'Z-9999' });

// Clicking "Publish event" and then waitForToast() is racy when two publishes
// happen close together (e.g. archive-then-publish in step 7): the toast from
// the PRIOR action can still be visible, so waitForToast()'s `.last()` may
// resolve instantly against the stale toast instead of the new one — letting
// the test read the queue before delivery (which is synchronous server-side,
// eventbridge/actions.go's matchAndDispatch) has actually happened. Waiting
// on the real /test-event response removes the race entirely.
async function publishEvent(page: import('@playwright/test').Page) {
  const [resp] = await Promise.all([
    page.waitForResponse((r) => r.url().includes('/test-event') && r.request().method() === 'POST'),
    page.getByRole('button', { name: 'Publish event' }).click(),
  ]);
  expect(resp.ok()).toBeTruthy();
}

test.describe('EventBridge', () => {
  test('bus, rule, targets, live matcher, delivery, toggle, schedule rule, archive/replay', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    confirmDialog,
    setEditor,
    waitForLive,
  }) => {
    const bus = uniqueName('e2e-eb-bus');
    const queue = uniqueName('e2e-eb-q');
    const ruleName = uniqueName('e2e-eb-rule');
    const scheduleRuleName = uniqueName('e2e-eb-sched');
    const archiveName = uniqueName('e2e-eb-arc');

    await createBus(request, bus);
    await createQueue(request, queue);

    await test.step('1-2: create a pattern rule via the UI and add the queue as a target', async () => {
      await page.goto(`eb/${bus}/create-rule`);
      await page.locator('input[name="name"]').fill(ruleName);
      await setEditor('textarea[name="pattern"]', PATTERN);
      await page.getByRole('button', { name: 'Create rule' }).click();
      await page.waitForURL(new RegExp(`/eb/${bus}/rule/${ruleName}(\\?|$)`));
      await expect(page.locator('#flashbar')).toContainText(ruleName);
      await expect(page.locator('#flashbar')).toContainText('created');

      // Add the queue as a target (separate form on the rule detail page).
      await page
        .locator('select[name="arn"]')
        .selectOption({ label: `SQS · ${queue}` });
      await page.getByRole('button', { name: 'Add target' }).click();
      await waitForToast();
      await expect(page.locator('#eb-targets')).toContainText(queue);
    });

    await test.step('3: live test-event matcher debounces and shows match/no-match verdicts', async () => {
      await page.goto(`eb/${bus}`);
      const ruleRow = page.locator('#eb-rules tr', { hasText: ruleName });

      await page.locator('input[name="source"]').fill('orders');
      await page.locator('input[name="detail_type"]').fill('OrderCreated');
      await setEditor('#eb-event-form textarea[name="detail"]', MATCHING_DETAIL);
      // No hardcoded sleep: expect() auto-retries past the 350ms debounce
      // (hx-trigger="input from:#eb-event-form delay:350ms") until the
      // /match response lands.
      await expect(ruleRow.locator('.badge')).toHaveText('match', { timeout: 5000 });

      await setEditor('#eb-event-form textarea[name="detail"]', NON_MATCHING_DETAIL);
      await expect(ruleRow.locator('.badge')).toHaveText('no match', { timeout: 5000 });
    });

    await test.step('4: publishing delivers the event to the matching rule\'s SQS target', async () => {
      // Re-set a matching detail (previous step left it non-matching) then
      // actually publish — a distinct action from the live matcher: the
      // matcher POSTs to /match, this submits the same form to /test-event
      // which calls the real PutEvents-equivalent (PutTestEvent).
      await setEditor('#eb-event-form textarea[name="detail"]', MATCHING_DETAIL);
      await publishEvent(page);
      const publishToast = await waitForToast();
      expect(publishToast).toMatch(/published/i);

      await page.goto(`sqs/${queue}`);
      await waitForLive('#message-panel-wrap', (text) => text.includes('A-1042'));
      await expect(page.locator('#message-panel-wrap .msg')).toHaveCount(1);
    });

    await test.step('5: disabling the rule stops delivery of matching events', async () => {
      // Toggle sends both an HX-Trigger toast AND an HX-Redirect; htmx treats
      // HX-Redirect as a hard `location.href` navigation (not a boosted
      // swap), which tears the page down before the toast has a chance to
      // render — so assert on the resulting state instead of the toast here.
      await page.goto(`eb/${bus}/rule/${ruleName}`);
      await page.getByRole('button', { name: 'Disable' }).click();
      await expect(page.locator('.det-title')).toContainText('DISABLED');

      // Publish another matching event while the rule is disabled.
      await page.goto(`eb/${bus}`);
      await page.locator('input[name="source"]').fill('orders');
      await page.locator('input[name="detail_type"]').fill('OrderCreated');
      await setEditor('#eb-event-form textarea[name="detail"]', MATCHING_DETAIL);
      await publishEvent(page);

      // Asserting an ABSENCE: target delivery for PutEvents is synchronous
      // in doze-aws (eventbridge/actions.go: matchAndDispatch runs inline,
      // no queueing), so by the time publishEvent()'s response resolves, a
      // disabled rule has already been skipped or not — a fresh navigation
      // reads the true post-publish state with no race. Still, add one short
      // explicit wait as a defensive margin against the SQS live-peek
      // panel's own 3s poll tick before we read it, since
      // we're proving a negative rather than waiting for a positive signal.
      await page.waitForTimeout(500);
      await page.goto(`sqs/${queue}`);
      await expect(page.locator('#message-panel-wrap .msg')).toHaveCount(1);
    });

    await test.step('6: schedule-expression rule (no pattern) creates successfully', async () => {
      // Re-enable the first rule so the archive/replay step below can still
      // observe real delivery through it.
      await page.goto(`eb/${bus}/rule/${ruleName}`);
      await page.getByRole('button', { name: 'Enable' }).click();
      await expect(page.locator('.det-title')).toContainText('ENABLED');

      // A schedule is the default bus's alone, here as on AWS. The form on a
      // custom bus does not offer one; it says so and points at the bus that
      // does. This step used to create a scheduled rule on the test's own bus.
      await page.goto(`eb/${bus}/create-rule`);
      await expect(page.locator('input[name="schedule"]')).toHaveCount(0);
      await page.getByRole('link', { name: 'Create one there' }).click();
      await page.waitForURL(/\/eb\/default\/create-rule$/);
      await page.locator('input[name="name"]').fill(scheduleRuleName);
      await page.locator('input[name="schedule"]').fill('rate(5 minutes)');
      // Pattern textarea intentionally left blank — this "Phase 8" case used
      // to be rejected server-side; now it must succeed as a schedule-only rule.
      await page.getByRole('button', { name: 'Create rule' }).click();
      await page.waitForURL(new RegExp(`/eb/default/rule/${scheduleRuleName}(\\?|$)`));
      await expect(page.locator('#flashbar')).toContainText(scheduleRuleName);
      await expect(page.locator('.sec-title').first()).toContainText('Schedule');
      await expect(page.locator('.code-out pre')).toContainText('rate(5 minutes)');
    });

    await test.step('7: archive captures events and replay redelivers to the target', async () => {
      // Archives and replays live on their own tab now — the bus page used to
      // render every panel at once.
      await page.goto(`eb/${bus}?tab=archives`);
      await page
        .locator('.tag-row-form input[name="name"]')
        .fill(archiveName);
      await page.locator('.tag-row-form').getByRole('button', { name: 'Archive' }).click();
      const archiveToast = await waitForToast();
      expect(archiveToast).toMatch(/created/i);
      await expect(page.locator('#eb-archives')).toContainText(archiveName);

      // Archives only capture events published AFTER they're created, so
      // publish a fresh matching event now (also delivers directly, since
      // the rule is enabled again) — this is what the archive will capture
      // and what replay will redeliver a second time. Back to the Rules tab:
      // the test-event form lives beside the rules it paints verdicts on.
      await page.goto(`eb/${bus}`);
      await page.locator('input[name="source"]').fill('orders');
      await page.locator('input[name="detail_type"]').fill('OrderCreated');
      await setEditor('#eb-event-form textarea[name="detail"]', MATCHING_DETAIL);
      await publishEvent(page);

      await page.goto(`sqs/${queue}`);
      const beforeReplay = await page.locator('#message-panel-wrap .msg').count();
      expect(beforeReplay).toBeGreaterThanOrEqual(2); // the two direct publishes above

      await page.goto(`eb/${bus}?tab=archives`);
      const archiveRow = page.locator('#eb-archives tr', { hasText: archiveName });
      await archiveRow.getByRole('button', { name: /Replay/ }).click();
      const replayToast = await waitForToast();
      expect(replayToast).toMatch(/Replaying/i);

      // #eb-replays, not #eb-archives: the two panels live in different columns
      // now, so starting a replay swaps Archives and repaints Replays out of
      // band.
      const replayRow = page.locator('#eb-replays tr', { hasText: archiveName }).last();
      await expect(replayRow.locator('.badge')).toHaveText('COMPLETED');

      // And Replays must still be where it belongs. Content alone does not
      // prove the out-of-band half worked: drop hx-swap-oob and htmx swaps the
      // whole response into #eb-archives instead, which moves the Replays panel
      // bodily into the left column. It still says COMPLETED there.
      await expect(page.locator('#eb-archives #eb-replays')).toHaveCount(0);
      const cols = await page.evaluate(() => {
        const l = document.querySelector('#eb-archives')!.getBoundingClientRect().left;
        const r = document.querySelector('#eb-replays')!.getBoundingClientRect().left;
        return { archivesLeft: Math.round(l), replaysLeft: Math.round(r) };
      });
      expect(cols.replaysLeft).toBeGreaterThan(cols.archivesLeft);

      // Replay is synchronous server-side (eventbridge/archive_actions.go
      // startReplay calls matchAndDispatch inline before responding), so the
      // redelivered event has already landed by the time the toast above
      // resolved — no wait needed, just re-check the count went up.
      await page.goto(`sqs/${queue}`);
      const afterReplay = await page.locator('#message-panel-wrap .msg').count();
      expect(afterReplay).toBeGreaterThan(beforeReplay);

      // Clean up the archive.
      await page.goto(`eb/${bus}?tab=archives`);
      await page
        .locator('#eb-archives tr', { hasText: archiveName })
        .getByRole('button', { name: 'Delete archive' })
        .click();
      await confirmDialog('accept');
      await waitForToast();
      // Scope to the Archives panel specifically — the Replays panel still
      // legitimately shows a row named "{archiveName}-replay-…". #eb-archives
      // IS that panel now rather than a wrapper around it and Replays, so the
      // scoping no longer needs a child selector to exclude its sibling.
      await expect(page.locator('#eb-archives')).not.toContainText(archiveName);
    });

    await test.step('8: delete the rules, then the bus', async () => {
      await page.goto(`eb/${bus}/rule/${ruleName}`);
      await page.getByRole('button', { name: 'Delete' }).click();
      await confirmDialog('accept');
      await page.waitForURL(new RegExp(`/eb/${bus}(\\?|$)`));
      await expect(page.locator('#eb-rules')).not.toContainText(ruleName);

      await page.goto(`eb/default/rule/${scheduleRuleName}`);
      await page.getByRole('button', { name: 'Delete' }).click();
      await confirmDialog('accept');
      await page.waitForURL(new RegExp(`/eb/default(\\?|$)`));
      await expect(page.locator('#eb-rules')).not.toContainText(scheduleRuleName);

      // The console has no wired-up "delete bus" button (handlers_eb.go's
      // ebDeleteBus exists and is routed at POST /eb/{bus}/delete-bus, but no
      // template calls it — grepped "delete-bus" across internal/console/templates
      // and found nothing), so this last piece of cleanup goes through the
      // same POST a UI button would use, via the API helper rather than a
      // click, since there's no element to click.
      await postForm(request, `eb/${bus}/delete-bus`, {});
    });
  });
});

// The pattern builder on the rule-create page: rows build the pattern, the
// service's own TestEventPattern answers before any event has to flow, and
// the round-trip through the JSON tab is byte-identical.
test.describe('pattern builder', () => {
  test('build from rows, test both verdicts, round-trip', async ({ page }) => {
    await page.goto('eb/default/create-rule');
    const pb = page.locator('.pb');
    await expect(pb.locator('.pb-stmt')).toBeVisible();
    await pb.locator('input[placeholder^="field"]').fill('source');
    await pb.locator('.pb-cond input.mono').fill('orders');
    await pb.locator('.pb-cond button', { hasText: 'Add' }).click();

    await pb.locator('.pb-check input').fill('{"source":["orders"],"detail-type":["t"],"detail":{}}');
    await pb.locator('.pb-check button').click();
    await expect(page.locator('[id^="pat-out"]')).toContainText('MATCHES');
    await pb.locator('.pb-check input').fill('{"source":["billing"],"detail-type":["t"],"detail":{}}');
    await pb.locator('.pb-check button').click();
    await expect(page.locator('[id^="pat-out"]')).toContainText('No match');

    const tricky = { detail: { cpu: [{ numeric: ['>', 0, '<=', 100] }], state: ['running', { prefix: 'pend' }] } };
    await pb.locator('.ws-seg a', { hasText: 'JSON' }).click();
    await page.evaluate((d) => {
      const ta = document.querySelector('textarea[name="pattern"]');
      window.dozeEditor.set(ta, JSON.stringify(d, null, 2));
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    }, tricky);
    const before = await page.evaluate(() => document.querySelector('textarea[name="pattern"]').__cm.getValue());
    await pb.locator('.ws-seg a', { hasText: 'Builder' }).click();
    await expect(pb.locator('.pb-stmt').first()).toBeVisible();
    await pb.locator('.ws-seg a', { hasText: 'JSON' }).click();
    const after = await page.evaluate(() => document.querySelector('textarea[name="pattern"]').__cm.getValue());
    expect(after).toBe(before);
  });
});

test.describe('API destinations', () => {
  test('connection and destination created, described without the secret, offered as a rule target', async ({
    page,
    uniqueName,
    waitForToast,
    setEditor,
  }) => {
    const conn = uniqueName('e2e-conn');
    const dest = uniqueName('e2e-dest');
    await page.goto('eb/destinations');
    await expect(page.getByRole('heading', { name: 'Create connection' })).toBeVisible();

    const connForm = page.locator('form[hx-post$="/create-connection"]');
    await connForm.locator('input[name="name"]').fill(conn);
    await connForm.locator('select[name="auth_type"]').selectOption('API_KEY');
    await connForm.locator('input[name="api_key_name"]').fill('X-Api-Key');
    await connForm.locator('input[name="api_key_value"]').fill('hunter2');
    await connForm.getByRole('button', { name: 'Create connection' }).click();
    await waitForToast();
    await expect(page.locator('#eb-http-tables')).toContainText(conn);

    // Describe: the header name shows, the value never does.
    await page.locator('#eb-http-tables button.linkish', { hasText: conn }).click();
    await expect(page.locator('#eb-http-detail')).toContainText('X-Api-Key');
    await expect(page.locator('#eb-http-detail')).not.toContainText('hunter2');

    const destForm = page.locator('form[hx-post$="/create-destination"]');
    await destForm.locator('input[name="name"]').fill(dest);
    await destForm.locator('select[name="connection"]').selectOption({ label: `${conn} · API_KEY` });
    await destForm.locator('input[name="endpoint"]').fill('http://127.0.0.1:1/hooks/*');
    await destForm.getByRole('button', { name: 'Create destination' }).click();
    await waitForToast();
    await expect(page.locator('#eb-http-tables')).toContainText(dest);
    await expect(page.locator('#eb-http-tables')).toContainText('ACTIVE');

    // The rule page offers it as a target.
    const ruleName = uniqueName('e2e-eb-http-rule');
    await page.goto('eb/default/create-rule');
    await page.locator('input[name="name"]').fill(ruleName);
    await setEditor('textarea[name="pattern"]', PATTERN);
    await page.getByRole('button', { name: 'Create rule' }).click();
    await page.waitForURL(new RegExp(`/eb/default/rule/${ruleName}(\\?|$)`));
    await expect(
      page.locator('select[name="arn"] option', { hasText: `API destination · ${dest}` })
    ).toHaveCount(1);
  });
});

// Routes a user reaches from the bus and rule pages that the long flow above
// never touches: creating a bus from the list pane, removing a rule's target,
// the Trace tab (reverse lookup + DescribeEventBus), and the archive/replay
// describe panels. Preconditions are arranged through the console's own create
// routes; every action under test is a click.
const sqsARN = (name: string) => `arn:aws:sqs:us-east-1:000000000000:${name}`;

test.describe('EventBridge bus and rule pages', () => {
  test('create a bus from the list pane\'s New link', async ({ page, request, uniqueName }) => {
    const bus = uniqueName('e2e-eb-newbus');
    await page.goto('eb');
    await page.locator('.listpane .new-link').click();
    await page.waitForURL(/\/eb\/create-bus$/);
    await expect(page.locator('.det-title')).toContainText('Create event bus');

    await page.getByLabel('Bus name').fill(bus);
    await page.getByRole('button', { name: 'Create event bus' }).click();
    await page.waitForURL(new RegExp(`/eb/${bus}(\\?|$)`));
    await expect(page.locator('#flashbar')).toContainText(bus);
    await expect(page.locator('.det-title')).toContainText(bus);
    // The list pane now carries it.
    await expect(page.locator('.listpane .li', { hasText: bus })).toBeVisible();

    await postForm(request, `eb/${bus}/delete-bus`, {});
  });

  test('remove a target from a rule', async ({ page, request, uniqueName, confirmDialog, waitForToast }) => {
    const bus = await createBus(request, uniqueName('e2e-eb-rt-bus'));
    const queue = await createQueue(request, uniqueName('e2e-eb-rt-q'));
    const rule = uniqueName('e2e-eb-rt-rule');
    await postForm(request, `eb/${bus}/create-rule`, { name: rule, pattern: PATTERN });
    await postForm(request, `eb/${bus}/rule/${rule}/add-target`, { arn: sqsARN(queue) });

    await page.goto(`eb/${bus}/rule/${rule}`);
    const targets = page.locator('#eb-targets');
    const row = targets.locator('tr', { hasText: queue });
    await expect(row).toBeVisible();

    // Cancel first: the target stays.
    await row.getByRole('button', { name: 'Remove target' }).click();
    await confirmDialog('cancel');
    await expect(row).toBeVisible();

    await row.getByRole('button', { name: 'Remove target' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toMatch(/Target removed/);
    // The first panel is the target list; the second is the Add-target form,
    // whose dropdown still (rightly) offers the queue.
    const list = targets.locator('.panel').first();
    await expect(list).not.toContainText(queue);
    await expect(list).toContainText('No targets');
    await expect(list.locator('.panel-h .badge')).toHaveText('0');

    // And the service agrees: a fresh load shows no target either.
    await page.reload();
    await expect(page.locator('#eb-targets .panel').first()).not.toContainText(queue);
  });

  test('Trace tab: which rules fire into a target, and describe the bus', async ({
    page,
    request,
    uniqueName,
  }) => {
    const bus = await createBus(request, uniqueName('e2e-eb-tr-bus'));
    const queue = await createQueue(request, uniqueName('e2e-eb-tr-q'));
    const stranger = await createQueue(request, uniqueName('e2e-eb-tr-none'));
    const rule = uniqueName('e2e-eb-tr-rule');
    await postForm(request, `eb/${bus}/create-rule`, { name: rule, pattern: PATTERN });
    await postForm(request, `eb/${bus}/rule/${rule}/add-target`, { arn: sqsARN(queue) });

    await page.goto(`eb/${bus}`);
    await page.locator('.tabbar a', { hasText: 'Trace' }).click();
    await page.waitForURL(/tab=trace/);

    const target = page.locator('input[name="target"]');
    const ask = page.getByRole('button', { name: 'Which rules fire into this?' });
    const byTarget = page.locator('#eb-by-target');

    await target.fill(sqsARN(queue));
    await ask.click();
    await expect(byTarget).toContainText('Rules firing into this target');
    const link = byTarget.getByRole('link', { name: rule });
    await expect(link).toBeVisible();

    // A queue no rule targets gets the explicit negative, not an empty panel.
    await target.fill(sqsARN(stranger));
    await ask.click();
    await expect(byTarget).toContainText('No rule on this bus targets');
    await expect(byTarget).toContainText(stranger);

    await page.getByRole('button', { name: 'Describe this bus' }).click();
    const detail = page.locator('#eb-detail');
    await expect(detail).toContainText(`Bus ${bus}`);
    await expect(detail).toContainText(`event-bus/${bus}`);

    // The rule link from the reverse lookup goes to the rule.
    await target.fill(sqsARN(queue));
    await ask.click();
    await byTarget.getByRole('link', { name: rule }).click();
    await page.waitForURL(new RegExp(`/eb/${bus}/rule/${rule}$`));
  });

  test('archive describe + edit, and replay describe', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const bus = await createBus(request, uniqueName('e2e-eb-ad-bus'));
    const archive = uniqueName('e2e-eb-ad-arc');
    await postForm(request, `eb/${bus}/create-archive`, { name: archive });
    await postForm(request, `eb/${bus}/replay`, { name: archive });

    await page.goto(`eb/${bus}?tab=archives`);
    await page.locator('#eb-archives').getByRole('button', { name: archive }).click();
    const panel = page.locator('#eb-archive-detail');
    await expect(panel).toBeVisible();
    await expect(panel.locator('.panel-h')).toContainText(archive);

    // Edit the retention and an event pattern, then save.
    await panel.locator('input[name="retention"]').fill('7');
    await panel.locator('.ws-seg a', { hasText: 'JSON' }).click();
    await setPattern(page, '#eb-archive-detail textarea[name="pattern"]', JSON.stringify({ source: ['orders'] }));
    await panel.getByRole('button', { name: 'Save archive' }).click();
    expect(await waitForToast()).toMatch(/Archive updated/);
    await expect(panel.locator('.panel-h')).toContainText('7 day retention');

    // A fresh describe shows what was stored, not what the form remembered.
    await page.reload();
    await page.locator('#eb-archives').getByRole('button', { name: archive }).click();
    await expect(page.locator('#eb-archive-detail .panel-h')).toContainText('7 day retention');
    await expect
      .poll(() =>
        page.evaluate(() => (document.querySelector('#eb-archive-detail textarea[name="pattern"]') as HTMLTextAreaElement).value)
      )
      .toContain('orders');

    // Replay detail: the replay row's name opens its describe panel.
    const replayBtn = page.locator('#eb-replays button.linkish', { hasText: archive }).first();
    const replayName = (await replayBtn.textContent())!.trim();
    await replayBtn.click();
    const detail = page.locator('#eb-detail');
    await expect(detail).toContainText(`Replay ${replayName}`);
    await expect(detail).toContainText('COMPLETED');
    await expect(detail).toContainText(archive);
  });
});

// The archive's pattern box is a pattern-builder textarea upgraded to
// CodeMirror; dozeEditor.set keeps both in sync, and an input event lets the
// builder's Alpine state see the change.
async function setPattern(page: import('@playwright/test').Page, selector: string, value: string) {
  await page.waitForFunction((sel) => !!(document.querySelector(sel) as any)?.__cm, selector);
  await page.evaluate(
    ([sel, val]) => {
      const ta = document.querySelector(sel) as HTMLTextAreaElement;
      (window as any).dozeEditor.set(ta, val);
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    },
    [selector, value]
  );
}
