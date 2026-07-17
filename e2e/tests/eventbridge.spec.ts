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

      await page.goto(`eb/${bus}/create-rule`);
      await page.locator('input[name="name"]').fill(scheduleRuleName);
      await page.locator('input[name="schedule"]').fill('rate(5 minutes)');
      // Pattern textarea intentionally left blank — this "Phase 8" case used
      // to be rejected server-side; now it must succeed as a schedule-only rule.
      await page.getByRole('button', { name: 'Create rule' }).click();
      await page.waitForURL(new RegExp(`/eb/${bus}/rule/${scheduleRuleName}(\\?|$)`));
      await expect(page.locator('#flashbar')).toContainText(scheduleRuleName);
      await expect(page.locator('.sec-title').first()).toContainText('Schedule');
      await expect(page.locator('.code-out pre')).toContainText('rate(5 minutes)');
    });

    await test.step('7: archive captures events and replay redelivers to the target', async () => {
      await page.goto(`eb/${bus}`);
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
      // and what replay will redeliver a second time.
      await page.locator('input[name="source"]').fill('orders');
      await page.locator('input[name="detail_type"]').fill('OrderCreated');
      await setEditor('#eb-event-form textarea[name="detail"]', MATCHING_DETAIL);
      await publishEvent(page);

      await page.goto(`sqs/${queue}`);
      const beforeReplay = await page.locator('#message-panel-wrap .msg').count();
      expect(beforeReplay).toBeGreaterThanOrEqual(2); // the two direct publishes above

      await page.goto(`eb/${bus}`);
      const archiveRow = page.locator('#eb-archives tr', { hasText: archiveName });
      await archiveRow.getByRole('button', { name: /Replay/ }).click();
      const replayToast = await waitForToast();
      expect(replayToast).toMatch(/Replaying/i);

      const replayRow = page.locator('#eb-archives tr', { hasText: archiveName }).last();
      await expect(replayRow.locator('.badge')).toHaveText('COMPLETED');

      // Replay is synchronous server-side (eventbridge/archive_actions.go
      // startReplay calls matchAndDispatch inline before responding), so the
      // redelivered event has already landed by the time the toast above
      // resolved — no wait needed, just re-check the count went up.
      await page.goto(`sqs/${queue}`);
      const afterReplay = await page.locator('#message-panel-wrap .msg').count();
      expect(afterReplay).toBeGreaterThan(beforeReplay);

      // Clean up the archive.
      await page.goto(`eb/${bus}`);
      await page
        .locator('#eb-archives tr', { hasText: archiveName })
        .getByRole('button', { name: 'Delete archive' })
        .click();
      await confirmDialog('accept');
      await waitForToast();
      // Scope to the Archives panel specifically — the sibling Replays panel
      // still legitimately shows a row named "{archiveName}-replay-…".
      await expect(page.locator('#eb-archives > .panel').first()).not.toContainText(
        archiveName
      );
    });

    await test.step('8: delete the rules, then the bus', async () => {
      await page.goto(`eb/${bus}/rule/${ruleName}`);
      await page.getByRole('button', { name: 'Delete' }).click();
      await confirmDialog('accept');
      await page.waitForURL(new RegExp(`/eb/${bus}(\\?|$)`));
      await expect(page.locator('#eb-rules')).not.toContainText(ruleName);

      await page.goto(`eb/${bus}/rule/${scheduleRuleName}`);
      await page.getByRole('button', { name: 'Delete' }).click();
      await confirmDialog('accept');
      await page.waitForURL(new RegExp(`/eb/${bus}(\\?|$)`));
      await expect(page.locator('#eb-rules')).not.toContainText(scheduleRuleName);

      // The console has no wired-up "delete bus" button (handlers_eb.go's
      // ebDeleteBus exists and is routed at POST /eb/{bus}/delete-bus, but no
      // template calls it — grepped "delete-bus" across console/templates
      // and found nothing), so this last piece of cleanup goes through the
      // same POST a UI button would use, via the API helper rather than a
      // click, since there's no element to click.
      await postForm(request, `eb/${bus}/delete-bus`, {});
    });
  });
});
