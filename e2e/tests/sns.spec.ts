import type { APIRequestContext } from '@playwright/test';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
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
    await page.goto(`sns/${topic}?tab=publish`);

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
    // The receipt stopped saying "fanned out to N" and started saying
    // "delivered to N of M" when it began evaluating filter policies for real
    // — a count of subscriptions is not a count of deliveries.
    await expect(receipt).toContainText('delivered to 1 of 1 subscriber');
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
      await page.goto(`sns/${topic}?tab=publish`);
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
      await page.goto(`sns/${topic}?tab=publish`);
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
    // The peek no longer shows the envelope's raw "Type": "Notification" —
    // summarize() RECOGNISES an SNS envelope and renders it as an "sns · topic"
    // chip with the inner message unwrapped. The chip is itself the proof the
    // envelope arrived: it can only appear when there is one to recognise.
    const envelopeMsg = page.locator('.msg', { hasText: markerEnvelope });
    await expect(envelopeMsg.locator('.evt')).toContainText('sns');

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
    // A raw delivery has no envelope, so there is nothing for the summariser
    // to recognise — no sns chip, just the payload.
    const rawMsg = page.locator('.msg', { hasText: markerRaw });
    await expect(rawMsg.locator('.evt', { hasText: 'sns' })).toHaveCount(0);

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

// ---- Route coverage: confirmation, and the Details tab's writes ----

/**
 * A throwaway HTTP endpoint for an http subscription. doze-aws POSTs the
 * SubscriptionConfirmation (Token included) here, exactly as SNS would, so the
 * test can paste the token into the console the way a user copies it from
 * their webhook's log.
 */
async function confirmationEndpoint() {
  const tokens: string[] = [];
  const server: Server = createServer((req, res) => {
    let body = '';
    req.on('data', (c) => (body += c));
    req.on('end', () => {
      try {
        const msg = JSON.parse(body);
        if (msg.Type === 'SubscriptionConfirmation' && msg.Token) tokens.push(msg.Token);
      } catch {
        // not a confirmation — ignore
      }
      res.writeHead(200).end();
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;
  return {
    url: `http://127.0.0.1:${port}/hook`,
    async token() {
      await expect.poll(() => tokens.length, { timeout: 10_000 }).toBeGreaterThan(0);
      return tokens[0];
    },
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  };
}

test.describe('SNS subscription confirmation', () => {
  test('a pending http subscription is confirmed by pasting its token', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const topic = uniqueName('e2e-sns-confirm');
    await createTopic(request, topic);
    const hook = await confirmationEndpoint();
    try {
      await postForm(request, `sns/${topic}/subscribe`, { protocol: 'http', endpoint: hook.url });
      const token = await hook.token();

      await page.goto(`sns/${topic}`);
      const pendingPanel = page.locator('.confirm-panel');
      await expect(pendingPanel).toBeVisible();
      await expect(pendingPanel.locator('.badge')).toHaveText('1');
      const sub = page.locator('.sub-item', { hasText: hook.url });
      await expect(sub.locator('.badge', { hasText: 'pending' })).toBeVisible();

      await pendingPanel.locator('input[name="token"]').fill(token);
      await pendingPanel.getByRole('button', { name: 'Confirm' }).click();
      expect(await waitForToast()).toContain('Subscription confirmed');

      await expect(page.locator('.confirm-panel')).toHaveCount(0);
      const confirmed = page.locator('.sub-item', { hasText: hook.url });
      await expect(confirmed).toBeVisible();
      await expect(confirmed.locator('.badge', { hasText: 'pending' })).toHaveCount(0);
      // A confirmed subscription gets its delivery settings back.
      await expect(confirmed.locator('button[title="Delivery settings"]')).toBeVisible();
    } finally {
      await hook.close();
    }
  });
});

test.describe('SNS topic details', () => {
  test('setting DisplayName stores it and the attribute table shows it', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const topic = uniqueName('e2e-sns-attr');
    await createTopic(request, topic);
    const display = `Display ${topic}`;

    await page.goto(`sns/${topic}`);
    await page.getByRole('link', { name: 'Details', exact: true }).click();
    await page.waitForURL(/tab=details/);

    const form = page.locator('form:has(select[name="name"])');
    await form.locator('select[name="name"]').selectOption('DisplayName');
    await form.locator('input[name="value"]').fill(display);
    await form.getByRole('button', { name: 'Set' }).click();
    expect(await waitForToast()).toContain('Attribute “DisplayName” stored');

    const row = page.locator('.tbl.kv tr', { has: page.locator('td', { hasText: /^DisplayName$/ }) });
    await expect(row).toContainText(display);
  });

  test('a permission is granted, listed, and revoked', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const topic = uniqueName('e2e-sns-perm');
    await createTopic(request, topic);
    const label = uniqueName('grant');

    await page.goto(`sns/${topic}?tab=details`);
    const form = page.locator('form:has(input[name="label"])');
    await form.locator('input[name="label"]').fill(label);
    await form.locator('input[name="account"]').fill('111122223333');
    await form.locator('select[name="action"]').selectOption('Publish');
    await form.getByRole('button', { name: 'Grant' }).click();
    expect(await waitForToast()).toContain(`Permission “${label}” added`);

    // Scoped to the grants table: the attribute table above also lists the
    // Policy document, which carries the label as its Sid.
    const grants = page.locator('.tbl:has(th:text-is("Sid")) tbody tr');
    const row = grants.filter({ hasText: label });
    await expect(row).toHaveCount(1);
    await expect(row).toContainText('111122223333');
    await expect(row).toContainText('Publish');
    // Written into the topic's stored Policy attribute (GetTopicAttributes).
    await expect(page.locator('.tbl.kv tr', { hasText: label })).toHaveCount(1);

    await row.getByRole('button', { name: 'Revoke' }).click();
    expect(await waitForToast()).toContain('Permission removed');
    await expect(grants.filter({ hasText: label })).toHaveCount(0);
    // Gone from the stored Policy document too, not just the grants table.
    await expect(page.locator('.tbl.kv tr', { hasText: label })).toHaveCount(0);
  });

  test('a data protection policy is stored and handed back', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    setEditor,
  }) => {
    const topic = uniqueName('e2e-sns-dpp');
    await createTopic(request, topic);
    const policyName = uniqueName('redact');
    const policy = JSON.stringify({ Name: policyName, Description: '', Version: '2021-06-01', Statement: [] });

    await page.goto(`sns/${topic}?tab=details`);
    const sel = 'form[hx-post$="/data-protection"] textarea[name="policy"]';
    await setEditor(sel, policy);
    await page.getByRole('button', { name: 'Store policy' }).click();
    expect(await waitForToast()).toContain('Data protection policy stored');

    await page.reload();
    await expect(page.locator(sel)).toHaveValue(new RegExp(policyName));
  });
});
