import type { APIRequestContext } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { createTopic, createQueue, postForm } from '../fixtures/api';

// Local-AWS identity convention (awsident.ARN): the subscribe form's
// endpoint <select> renders exactly this ARN shape as each <option>'s
// value, so arranging a subscription via the API needs to match it.
const queueARN = (name: string) => `arn:aws:sqs:us-east-1:000000000000:${name}`;

/** Arrange-only: subscribes a queue to a topic via the same Query-API route
 * the UI's "Subscribe" form posts to, for specs whose scenario isn't the
 * subscribe flow itself (subscribe-flow coverage lives in its own test). */
async function subscribeQueue(
  request: APIRequestContext,
  topic: string,
  queueName: string,
  opts?: { policy?: string; raw?: boolean }
) {
  await postForm(request, `sns/${topic}/subscribe`, {
    protocol: 'sqs',
    endpoint: queueARN(queueName),
    policy: opts?.policy,
    raw: opts?.raw,
  });
}

test.describe('SNS console', () => {
  test('subscribing a queue to a topic through the UI', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const topic = uniqueName('e2e-sns-topic');
    const queue = uniqueName('e2e-sns-queue');
    await createTopic(request, topic);
    await createQueue(request, queue);

    await page.goto(`sns/${topic}`);
    await expect(page.locator('#sns-subs .badge').first()).toHaveText('0');

    // The "add subscriber" row: protocol defaults to sqs, whose <select
    // name=endpoint> is populated with every queue's console-convention ARN.
    const addForm = page.locator('#sns-subs form:has(select[name="protocol"])');
    // Two <select name=endpoint> exist (sqs and lambda variants, toggled via
    // x-show); only the enabled one belongs to the active (sqs) protocol.
    await addForm.locator('select[name="endpoint"]:not([disabled])').selectOption({ label: queue });
    await addForm.locator('button[type=submit]').click();

    const toastMsg = await waitForToast();
    expect(toastMsg).toMatch(/Subscription created/);

    const subItem = page.locator('.sub-item', { hasText: queue });
    await expect(subItem).toBeVisible();
    await expect(subItem.locator('.badge.type')).toHaveText('sqs');
    await expect(page.locator('#sns-subs .badge').first()).toHaveText('1');
  });

  test('publish fans out to a subscriber and lands in its queue', async ({
    page,
    request,
    uniqueName,
    setEditor,
    waitForLive,
  }) => {
    const topic = uniqueName('e2e-sns-topic');
    const queue = uniqueName('e2e-sns-queue');
    await createTopic(request, topic);
    await createQueue(request, queue);
    await subscribeQueue(request, topic, queue);

    const marker = uniqueName('marker');
    await page.goto(`sns/${topic}`);

    await setEditor('textarea[name="message"]', JSON.stringify({ marker }));
    const publishForm = page.locator('form:has(textarea[name="message"])');
    await publishForm.locator('button', { hasText: 'Add' }).click();
    const attrRow = publishForm.locator('.attr-row').first();
    await attrRow.locator('input').first().fill('eventType');
    await attrRow.locator('input').nth(1).fill('OrderPlaced');
    await publishForm.locator('button[type=submit]').click();

    // The fan-out receipt: SNS→SQS delivery is synchronous server-side, so
    // by the time this partial swaps in, the message already sits in the
    // subscriber queue.
    const receipt = page.locator('#sns-receipt');
    await expect(receipt).toContainText('fanned out to 1 subscriber');
    await expect(receipt).toContainText(queue);

    await page.goto(`sqs/${queue}`);
    await waitForLive('#message-panel-wrap', (text) => text.includes(marker));
    await expect(page.locator('#message-panel-wrap')).toContainText(marker);
  });

  test('filter policy delivers only the matching message', async ({
    page,
    request,
    uniqueName,
    setEditor,
    waitForLive,
    waitForToast,
  }) => {
    const topic = uniqueName('e2e-sns-topic');
    const queue = uniqueName('e2e-sns-queue');
    await createTopic(request, topic);
    await createQueue(request, queue);
    await subscribeQueue(request, topic, queue);

    await page.goto(`sns/${topic}`);
    const subItem = page.locator('.sub-item', { hasText: queue });
    await subItem.locator('button[title="Delivery settings"]').click();
    // Scope to .sub-cfg: the "add subscriber" form below also has a
    // textarea[name=policy] (its own optional filter-at-subscribe-time
    // field), so the bare attribute selector is ambiguous.
    await setEditor('.sub-cfg textarea[name="policy"]', JSON.stringify({ eventType: ['OrderPlaced'] }));
    await subItem.locator('.sub-cfg button:has-text("Save filter")').click();
    const filterToast = await waitForToast();
    expect(filterToast).toMatch(/Filter policy saved/);

    // Publish fresh from a reloaded page each time so the message/attr
    // editors never carry state over from the previous publish.
    async function publish(marker: string, eventType: string) {
      await page.goto(`sns/${topic}`);
      await setEditor('textarea[name="message"]', JSON.stringify({ marker }));
      const publishForm = page.locator('form:has(textarea[name="message"])');
      await publishForm.locator('button', { hasText: 'Add' }).click();
      const row = publishForm.locator('.attr-row').first();
      await row.locator('input').first().fill('eventType');
      await row.locator('input').nth(1).fill(eventType);
      await publishForm.locator('button[type=submit]').click();
      await expect(page.locator('#sns-receipt')).toContainText('Published', { timeout: 8000 });
    }

    const markerMatch = uniqueName('match');
    const markerMiss = uniqueName('miss');
    const markerSentinel = uniqueName('sentinel');

    await publish(markerMatch, 'OrderPlaced'); // matches the filter
    await publish(markerMiss, 'OrderShipped'); // does not match — filtered out
    await publish(markerSentinel, 'OrderPlaced'); // matches — proves delivery kept working after the miss

    await page.goto(`sqs/${queue}`);
    await waitForLive('#message-panel-wrap', (text) => text.includes(markerSentinel));
    const panel = page.locator('#message-panel-wrap');
    await expect(panel).toContainText(markerMatch);
    await expect(panel).not.toContainText(markerMiss);
  });

  test('raw delivery toggle strips the SNS envelope and persists', async ({
    page,
    request,
    uniqueName,
    setEditor,
    waitForLive,
    waitForToast,
  }) => {
    const topic = uniqueName('e2e-sns-topic');
    const queue = uniqueName('e2e-sns-queue');
    await createTopic(request, topic);
    await createQueue(request, queue);
    await subscribeQueue(request, topic, queue);

    async function publish(marker: string) {
      await page.goto(`sns/${topic}`);
      await setEditor('textarea[name="message"]', JSON.stringify({ marker }));
      const publishForm = page.locator('form:has(textarea[name="message"])');
      await publishForm.locator('button[type=submit]').click();
      await expect(page.locator('#sns-receipt')).toContainText('Published', { timeout: 8000 });
    }

    // Before toggling raw delivery: the SQS message body is the full SNS
    // JSON envelope (Type/MessageId/TopicArn/Message/...).
    const markerEnvelope = uniqueName('envelope');
    await publish(markerEnvelope);
    await page.goto(`sqs/${queue}`);
    await waitForLive('#message-panel-wrap', (text) => text.includes(markerEnvelope));
    const envelopeMsg = page.locator('.msg', { hasText: markerEnvelope });
    await expect(envelopeMsg).toContainText('Notification');

    // Flip raw delivery on for this subscription.
    await page.goto(`sns/${topic}`);
    const subItem = page.locator('.sub-item', { hasText: queue });
    await subItem.locator('button[title="Delivery settings"]').click();
    // The checkbox is visually a switch (track+thumb overlay it), so click
    // the wrapping <label> — the browser routes that to the real input,
    // same as a user clicking the visible switch would.
    await subItem.locator('label.switch').click();
    const rawToast = await waitForToast();
    expect(rawToast).toMatch(/Raw delivery on/);

    // After: the SQS message body is the bare message, no envelope fields.
    const markerRaw = uniqueName('raw');
    await publish(markerRaw);
    await page.goto(`sqs/${queue}`);
    await waitForLive('#message-panel-wrap', (text) => text.includes(markerRaw));
    const rawMsg = page.locator('.msg', { hasText: markerRaw });
    await expect(rawMsg).not.toContainText('Notification');

    // The toggle's effect is also visible (and persists across reload) as
    // the "raw" badge on the subscription row, independent of message shape.
    await page.goto(`sns/${topic}`);
    await expect(page.locator('.sub-item', { hasText: queue }).locator('.badge', { hasText: 'raw' })).toBeVisible();
    await page.reload();
    await expect(page.locator('.sub-item', { hasText: queue }).locator('.badge', { hasText: 'raw' })).toBeVisible();
  });

  test('unsubscribe and delete the topic', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    waitForToast,
  }) => {
    const topic = uniqueName('e2e-sns-topic');
    const queue = uniqueName('e2e-sns-queue');
    await createTopic(request, topic);
    await createQueue(request, queue);
    await subscribeQueue(request, topic, queue);

    await page.goto(`sns/${topic}`);
    const subItem = page.locator('.sub-item', { hasText: queue });
    await expect(subItem).toBeVisible();

    await subItem.locator(`button[title="Unsubscribe ${queue}"]`).click();
    await expect(page.locator('#confirm-msg')).toContainText(queue);
    await confirmDialog('accept');
    const unsubToast = await waitForToast();
    expect(unsubToast).toMatch(/Subscription removed/);
    // Scope to the subscriber rows, not all of #sns-subs — the "add
    // subscriber" form's queue <select> still lists this (undeleted) queue.
    await expect(page.locator('.sub-item', { hasText: queue })).toHaveCount(0);

    await page.locator('.acts').getByRole('button', { name: 'Delete' }).click();
    await expect(page.locator('#confirm-msg')).toContainText(topic);
    await confirmDialog('accept');
    await page.waitForURL(/\/sns(\?|$)/);
    await expect(page.locator('#flashbar')).toContainText('Topic deleted');
  });
});
