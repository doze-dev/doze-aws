import type { APIRequestContext, Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { PRIVATE_CONSOLE, PRIVATE_ORIGIN, privateSqs, startPrivate, type PrivateServer } from '../fixtures/private';
import { ACCOUNT_ID, BASE_URL, REGION } from '../playwright.config';

// The wire page as the boards Main, Wire-Inspect, Wire-Refused, Wire-Empty and
// Bad-Down draw it: the header and the filters, a call opened with its tabs, a
// refusal explained, the feed paused while a call is read, the first-run page,
// and the console's own poll failing.
//
// Two kinds of test. The ones about a call or a filter run on the suite's shared
// server with names of their own. The ones that need a stack nobody else is
// calling, or one that is stopped, run on a server of the spec's own
// (fixtures/private.ts), serially.

const GATEWAY_ROOT = new URL('/', BASE_URL).toString();

async function aws(request: APIRequestContext, target: string, body: Record<string, unknown>) {
  return request.post(GATEWAY_ROOT, {
    headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': target },
    data: JSON.stringify(body),
  });
}

/** The feed polls every 1.5 s and a changed tick replaces the rows: wait for one quiet tick before clicking. */
async function waitWireQuiet(page: Page) {
  const feed = page.locator('#traffic-feed');
  await expect(async () => {
    const before = await feed.getAttribute('data-hash');
    await page.waitForTimeout(1700);
    expect(await feed.getAttribute('data-hash')).toBe(before);
  }).toPass({ timeout: 20000 });
}

test.describe('the wire page', () => {
  test('says what it is, and how many calls a minute it is seeing', async ({ page, request, uniqueName }) => {
    const q = uniqueName('e2e-wire-head');
    expect((await aws(request, 'AmazonSQS.CreateQueue', { QueueName: q })).ok()).toBeTruthy();
    await page.goto('traffic');

    await expect(page.locator('.wire-t .det-title')).toHaveText('The wire');
    await expect(page.locator('.wire-t .det-sub')).toHaveText(
      "Every AWS call your app made against this endpoint, newest first. The console's own calls are left out.",
    );
    // The pill carries a real figure: the calls that started in the last minute.
    await expect(page.locator('.wire-acts .pill.ok')).toHaveText(/live · \d+ a minute/);
    await expect(page.locator('.wire-f .note')).toHaveText('the last 500 calls are kept');
    // The table's columns are the board's.
    await expect(page.locator('.tr-head')).toHaveText(/WHEN.*CALL.*ON.*ANSWER.*MS.*TIME/i);
    // And the rail says the wire is live.
    await expect(page.locator('.rail [data-wire-note]')).toHaveText('live');
  });

  test('a call shows its time as a lane, its answer in a word, and a long poll as a wait', async ({ page, request, uniqueName }) => {
    const q = uniqueName('e2e-wire-lane');
    await aws(request, 'AmazonSQS.CreateQueue', { QueueName: q });
    // A receive that waits a second on an empty queue: time spent waiting, not working.
    await aws(request, 'AmazonSQS.ReceiveMessage', { QueueUrl: `${GATEWAY_ROOT}${ACCOUNT_ID}/${q}`, WaitTimeSeconds: 1 });
    await page.goto('traffic');
    await expect(page.locator('#traffic-feed .tr-row', { hasText: 'ReceiveMessage' }).filter({ hasText: q }).first()).toBeVisible();

    const create = page.locator('#traffic-feed .tr-row', { hasText: 'CreateQueue' }).filter({ hasText: q }).first();
    await expect(create.locator('.lane > span')).toHaveCount(1);
    const wait = page.locator('#traffic-feed .tr-row', { hasText: 'ReceiveMessage' }).filter({ hasText: q }).first();
    await expect(wait.locator('.dur')).toHaveText(/^1 s$/);
    await expect(wait.locator('.lane > span')).toHaveCount(0);
  });

  test('the Refused filter shows only what was refused, and says what a refusal is', async ({ page, request, uniqueName }) => {
    const q = uniqueName('e2e-wire-refused');
    await aws(request, 'AmazonSQS.CreateQueue', { QueueName: q });
    // A deduplication id belongs to FIFO queues: refused on a standard one.
    const refused = await aws(request, 'AmazonSQS.SendMessage', {
      QueueUrl: `${GATEWAY_ROOT}${ACCOUNT_ID}/${q}`,
      MessageBody: '{"orderId":"HRB-1"}',
      MessageDeduplicationId: 'harbour-orders',
    });
    expect(refused.status()).toBe(400);
    await page.goto('traffic');
    const create = page.locator('#traffic-feed .tr-row', { hasText: 'CreateQueue' }).filter({ hasText: q });
    const send = page.locator('#traffic-feed .tr-row', { hasText: 'SendMessage' }).filter({ hasText: q });
    await expect(send).toBeVisible();
    await waitWireQuiet(page);

    await page.getByRole('button', { name: 'Refused', exact: true }).click();
    await expect(send).toBeVisible();
    await expect(create).toBeHidden();
    // The refusal's own sentence is on the row, and the teaching line is under the table.
    await expect(send.locator('.tr-why-t')).toContainText('MessageDeduplicationId');
    await expect(page.locator('.wire-refnote')).toContainText(
      'A refusal is doze-aws declining a request it would not accept from AWS either, so it is the answer your app will get after it is deployed.',
    );
    await expect(page.locator('.wire-refnote')).toContainText(/(One call|\d+ calls) in this window (was|were) refused\./);
  });
});

test.describe('the inspector', () => {
  test('a refused call is explained from the call, the queue and the ledger', async ({ page, request, uniqueName, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const q = uniqueName('e2e-wire-explain');
    await aws(request, 'AmazonSQS.CreateQueue', { QueueName: q });
    await aws(request, 'AmazonSQS.SendMessage', {
      QueueUrl: `${GATEWAY_ROOT}${ACCOUNT_ID}/${q}`,
      MessageBody: '{"orderId":"HRB-2026-04810"}',
      MessageDeduplicationId: 'harbour-orders',
    });
    await page.goto('traffic');
    const row = page.locator('#traffic-feed .tr-row.refused', { hasText: q }).first();
    await expect(row).toBeVisible();
    await waitWireQuiet(page);
    await row.locator('.act').click();

    const d = page.locator('#t-drawer-inner');
    // The real code and the real sentence, in the state's callout.
    await expect(d.locator('.td-callout.td-refused .m.b')).toHaveText('InvalidParameterValue');
    await expect(d.locator('.td-callout.td-refused')).toContainText('MessageDeduplicationId');
    await expect(d.locator('.td-h .st')).toHaveText('400 refused');
    // The request, as it was sent, with its members in colour.
    await expect(d.locator('pre.code').first()).toContainText('"MessageDeduplicationId": "harbour-orders"');
    await expect(d.locator('pre.code .kw').first()).toBeVisible();
    // The queue: its name is a link to its page, it is standard, and the sentence is said of that.
    await expect(d.getByText('The queue it was sent to')).toBeVisible();
    await expect(d.locator('.td-res a')).toHaveText(q);
    await expect(d.locator('.td-res .pill')).toHaveText('standard');
    await expect(d.getByText('has no FIFO attributes')).toBeVisible();
    await expect(d.getByText('The request names a parameter that only FIFO queues take, and this queue is standard.')).toBeVisible();
    // The ledger's row for the operation, and the one link to the fidelity table.
    await expect(d.getByText('The same operation, in the ledger')).toBeVisible();
    await expect(d.locator('.td-led .m.b')).toHaveText('SendMessage');
    await expect(d.locator('.td-led .pill')).toHaveText('F · functional');
    await d.getByRole('button', { name: 'The SQS ledger' }).click();
    const dialog = page.getByRole('dialog', { name: 'service fidelity' });
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('.info-body')).toContainText('SendMessage');
    await dialog.getByRole('button', { name: 'Close' }).click();
    await expect(dialog).toBeHidden();

    // No tabs: the reason is the whole of the answer.
    await expect(d.getByRole('tablist')).toHaveCount(0);

    // Copy as curl and Copy the error copy what they say.
    await d.getByRole('button', { name: 'Copy as curl' }).click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toMatch(/^curl -X POST 'http:\/\/127\.0\.0\.1:\d+\/'/);
    await d.getByRole('button', { name: 'Copy the error' }).click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toMatch(/^InvalidParameterValue: .*MessageDeduplicationId/);
    // The queue's name goes to the queue.
    await d.locator('.td-res a').click();
    await page.waitForURL(new RegExp(`/sqs/${q}$`));
  });

  test('a served call has three tabs, and the request is readable', async ({ page, request, uniqueName }) => {
    const q = uniqueName('e2e-wire-tabs');
    await aws(request, 'AmazonSQS.CreateQueue', { QueueName: q });
    await page.goto('traffic');
    const row = page.locator('#traffic-feed .tr-row', { hasText: 'CreateQueue' }).filter({ hasText: q }).first();
    await expect(row).toBeVisible();
    await waitWireQuiet(page);
    await row.locator('.act').click();

    const d = page.locator('#t-drawer-inner');
    await expect(d.locator('.td-h .st')).toHaveText('200');
    await expect(d.locator('.td-meta')).toContainText(/\d\d:\d\d:\d\d\.\d{3} · [\d.]+ ms · request id [0-9a-f-]{36}/);
    const tabs = d.getByRole('tablist');
    await expect(tabs.getByRole('tab', { name: 'Request' })).toHaveAttribute('aria-selected', 'true');
    // The request tab: where, which operation, the body, the command.
    await expect(d.locator('.td-kv')).toContainText('POST /');
    await expect(d.locator('.td-kv')).toContainText('AmazonSQS.CreateQueue');
    await expect(d.locator('.td-kv')).toContainText('application/x-amz-json-1.0');
    await expect(d.locator('pre.code').first()).toContainText(q);
    await expect(d.locator('pre.code').nth(1)).toContainText("curl -X POST 'http://");
    await expect(d.getByText('secrets are masked, long bodies cut')).toBeVisible();

    // The response tab replaces it.
    await tabs.getByRole('tab', { name: 'Response' }).click();
    await expect(d.locator('pre.code:visible').first()).toContainText('QueueUrl');
    await expect(d.getByText('The call, as curl')).toBeHidden();
    // What it caused: nothing, said plainly, with the rule for what is nested.
    await tabs.getByRole('tab', { name: 'What it caused' }).click();
    await expect(d.getByText('Nothing. This call set no other work of doze-aws\'s own off.')).toBeVisible();
    await expect(d.getByText('Only work doze-aws did itself is nested here.')).toBeVisible();
    await expect(tabs.getByRole('tab', { name: 'What it caused' }).locator('.m')).toHaveText('0');
  });

  test('the work a call caused is nested under it and says what emitted it', async ({ page, request, uniqueName }) => {
    // A bucket that tells a queue about every object: the put, and the send it
    // caused, which says it came of the bucket's notification.
    const bucket = uniqueName('e2e-wire-cascade');
    const q = uniqueName('e2e-wire-cascade-q');
    expect((await aws(request, 'AmazonSQS.CreateQueue', { QueueName: q })).ok()).toBeTruthy();
    expect((await request.put(`${GATEWAY_ROOT}${bucket}`)).ok()).toBeTruthy();
    const arn = `arn:aws:sqs:${REGION}:${ACCOUNT_ID}:${q}`;
    expect(
      (
        await request.put(`${GATEWAY_ROOT}${bucket}?notification`, {
          headers: { 'Content-Type': 'application/xml' },
          data: `<NotificationConfiguration><QueueConfiguration><Queue>${arn}</Queue><Event>s3:ObjectCreated:*</Event></QueueConfiguration></NotificationConfiguration>`,
        })
      ).ok(),
    ).toBeTruthy();
    expect((await request.put(`${GATEWAY_ROOT}${bucket}/receipt.pdf`, { data: 'x', headers: { 'Content-Type': 'application/pdf' } })).ok()).toBeTruthy();

    await page.goto('traffic');
    const child = page.locator('#traffic-feed .tr-row.tr-child', { hasText: q }).first();
    await expect(child).toBeVisible({ timeout: 15000 });
    await expect(child.locator('.tr-via')).toHaveText('via s3:ObjectCreated:Put');
    await expect(child.locator('.tr-branch')).toHaveText('└');
    // And the put's own inspector counts it, and the tab lists it.
    const put = page.locator('#traffic-feed .tr-row:not(.tr-child)', { hasText: 'PutObject' }).filter({ hasText: bucket }).first();
    await waitWireQuiet(page);
    await put.locator('.act').click();
    const d = page.locator('#t-drawer-inner');
    await expect(d.getByRole('tab', { name: /What it caused/ }).locator('.m')).toHaveText('1');
    await expect(d.locator('.td-caused')).toContainText(`SendMessage ${q}`);
    await expect(d.locator('.td-caused')).toContainText('via s3:ObjectCreated:Put');
  });
});

// ---- on a server of the spec's own ----

test.describe('on a stack of its own', () => {
  test.describe.configure({ mode: 'serial' });
  let server: PrivateServer;
  test.beforeAll(async () => {
    server = await startPrivate('wire');
  });
  test.afterAll(async () => {
    await server?.stop();
  });

  test('a stack nobody has called is the first-run page, and the first call ends it', async ({ page, context }) => {
    // Nothing exists and nothing is recorded: this server was started a moment ago.
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await page.goto(PRIVATE_CONSOLE);
    await expect(page.getByRole('heading', { name: 'Nothing has called yet.' })).toBeVisible();
    await expect(page.locator('.wf-lede')).toHaveText(`doze-aws is listening on 127.0.0.1:${new URL(PRIVATE_ORIGIN).port}. The first call your code makes shows up here.`);
    await expect(page.locator('.rail [data-wire-note]')).toHaveText('quiet');
    // The block to point code at it: this instance's endpoint and region.
    const block = page.locator('.wire-first pre.code');
    await expect(block).toHaveText(
      `export AWS_ENDPOINT_URL=${PRIVATE_ORIGIN}\nexport AWS_ACCESS_KEY_ID=test\nexport AWS_SECRET_ACCESS_KEY=test\nexport AWS_REGION=us-east-1`,
    );
    await page.getByRole('button', { name: 'Copy', exact: true }).click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(`AWS_ENDPOINT_URL=${PRIVATE_ORIGIN}`);
    // The ways to make something from here go to the create pages.
    for (const [label, path] of [
      ['Create a bucket', 's3/create'],
      ['Create a queue', 'sqs/create'],
      ['Create a table', 'ddb/create'],
      ['Deploy a CloudFormation template', 'cfn/create'],
    ]) {
      await expect(page.getByRole('link', { name: label })).toHaveAttribute('href', `/_console/${path}`);
    }
    await expect(page.getByText('waiting for a call…')).toBeVisible();
    await expect(page.getByText('There is none here.')).toBeVisible();

    // The first call ends it: the page becomes the wire without a reload.
    await privateSqs(PRIVATE_ORIGIN, 'ListQueues', {});
    await expect(page.locator('.wire-first')).toHaveCount(0, { timeout: 10000 });
    await expect(page.locator('.tr-row', { hasText: 'ListQueues' }).first()).toBeVisible();
    await expect(page.locator('.rail [data-wire-note]')).toHaveText('live', { timeout: 10000 });
  });

  test('opening a call pauses the feed and counts what arrives; Resume catches up', async ({ page }) => {
    const first = 'e2e-wire-p-first';
    await privateSqs(PRIVATE_ORIGIN, 'CreateQueue', { QueueName: first });
    await page.goto(PRIVATE_CONSOLE + 'traffic');
    const row = page.locator('#traffic-feed .tr-row', { hasText: first }).first();
    await expect(row).toBeVisible();
    await waitWireQuiet(page);

    await row.locator('.act').click();
    const pill = page.locator('.wire-acts .pill.warn');
    await expect(pill).toBeVisible();
    await expect(pill).toHaveText(/paused · 0\s*new/);

    // Calls made while it is open are counted and are not drawn.
    await privateSqs(PRIVATE_ORIGIN, 'CreateQueue', { QueueName: 'e2e-wire-p-second' });
    await privateSqs(PRIVATE_ORIGIN, 'CreateQueue', { QueueName: 'e2e-wire-p-third' });
    await expect(pill).toHaveText(/paused · 2\s*new/, { timeout: 10000 });
    await page.waitForTimeout(2000);
    await expect(page.locator('#traffic-feed .tr-row', { hasText: 'e2e-wire-p-second' })).toHaveCount(0);

    // Resume is the feed again: the pill says live and the calls are there. The
    // call that is open stays open.
    await page.getByRole('button', { name: 'Resume' }).click();
    await expect(page.locator('.wire-acts .pill.ok')).toBeVisible();
    await expect(page.locator('#traffic-feed .tr-row', { hasText: 'e2e-wire-p-second' })).toBeVisible({ timeout: 10000 });
    await expect(page.locator('.t-drawer')).toBeVisible();
    await expect(page.locator('.tr-row.sel')).toHaveCount(1);

    // Closing a call that paused the feed resumes it; pausing on purpose is kept.
    await page.keyboard.press('Escape');
    await expect(page.locator('.t-drawer')).toBeHidden();
    await page.getByRole('button', { name: 'Pause', exact: true }).click();
    await expect(page.locator('.wire-acts .pill.warn')).toBeVisible();
    await row.locator('.ts').click();
    await page.keyboard.press('Escape');
    await expect(page.locator('.wire-acts .pill.warn')).toBeVisible();
    await page.getByRole('button', { name: 'Resume' }).click();
  });

  test('"Needs a look" is a dead-letter queue, a firing alarm, what was refused and a function error', async ({ page }) => {
    const o = PRIVATE_ORIGIN;
    await privateSqs(o, 'CreateQueue', { QueueName: 'e2e-wire-n-dlq' });
    await privateSqs(o, 'CreateQueue', {
      QueueName: 'e2e-wire-n-main',
      Attributes: { RedrivePolicy: JSON.stringify({ deadLetterTargetArn: `arn:aws:sqs:us-east-1:000000000000:e2e-wire-n-dlq`, maxReceiveCount: 3 }) },
    });
    await privateSqs(o, 'SendMessage', { QueueUrl: `${o}/000000000000/e2e-wire-n-dlq`, MessageBody: '{"n":1}' });
    // A refusal, and an alarm that is firing.
    await privateSqs(o, 'SendMessage', { QueueUrl: `${o}/000000000000/e2e-wire-n-main`, MessageBody: 'x', MessageDeduplicationId: 'd' });
    const alarm = async (op: string, body: object) =>
      fetch(`${o}/`, { method: 'POST', headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': `GraniteServiceVersion20100801.${op}` }, body: JSON.stringify(body) });
    await alarm('PutMetricAlarm', { AlarmName: 'e2e-wire-n-alarm', Namespace: 'Shop', MetricName: 'Declines', Statistic: 'Sum', Period: 60, EvaluationPeriods: 1, Threshold: 5, ComparisonOperator: 'GreaterThanThreshold', TreatMissingData: 'breaching' });
    await alarm('SetAlarmState', { AlarmName: 'e2e-wire-n-alarm', StateValue: 'ALARM', StateReason: 'over' });

    await page.goto(PRIVATE_CONSOLE + 'traffic');
    const needs = page.locator('.wire-aside');
    await expect(needs.getByText('Needs a look')).toBeVisible();
    // Each callout is a link to the thing, or is plain, as the board draws them.
    const dlq = needs.locator('a.wire-callout.warn', { hasText: 'e2e-wire-n-dlq holds 1 message' });
    await expect(dlq).toBeVisible();
    await expect(dlq).toContainText('Messages land here after 3 failed receives.');
    await expect(dlq).toHaveAttribute('href', '/_console/sqs/e2e-wire-n-dlq');
    const firing = needs.locator('a.wire-callout.bad', { hasText: '1 alarm is firing' });
    await expect(firing).toContainText(/e2e-wire-n-alarm, since \d\d:\d\d\./);
    await expect(needs.locator('.wire-callout.deny', { hasText: '1 call was refused' })).toContainText('The same request would fail on AWS.');
    // The window's figures count the ring.
    const figures = needs.locator('.wire-kv');
    await expect(figures).toContainText('Calls');
    await expect(figures.locator('.m.b.deny')).toHaveText('1');
    // The rail's dots agree: the queue's service has one, so does CloudWatch.
    await expect(page.locator('#dot-sqs')).toHaveClass(/warn/);
  });

  test('the console says when it cannot reach doze-aws, and takes it back when it can', async ({ page }) => {
    await privateSqs(PRIVATE_ORIGIN, 'CreateQueue', { QueueName: 'e2e-wire-down' });
    await page.goto(PRIVATE_CONSOLE + 'sqs/e2e-wire-down');
    await expect(page.locator('.det-title')).toContainText('e2e-wire-down');
    const banner = page.locator('#down-banner');
    await expect(banner).toBeHidden();
    await expect(page.locator('.tb-right .down-pill')).toBeHidden();

    // The process stops. The page is open and polling.
    await server.stop();
    await expect(banner).toBeVisible({ timeout: 20000 });
    await expect(banner).toContainText(`The console cannot reach doze-aws on 127.0.0.1:${new URL(PRIVATE_ORIGIN).port}`);
    await expect(banner).toContainText('What is on screen is the last thing it heard. Start doze-aws again and this page picks up on its next poll.');
    // The top bar says it in its own words in place of the endpoint, and the key
    // bar says when it last heard.
    await expect(page.locator('.tb-right .down-pill')).toHaveText('not answering');
    await expect(page.locator('.tb-right .pill:not(.down-pill)').first()).toBeHidden();
    await expect(page.locator('#keybar')).toHaveText(/^last heard \d\d:\d\d:\d\d$/);
    // What is on screen is the last thing heard, and is not to be acted on.
    await expect(page.locator('#workspace')).toHaveAttribute('inert', '');
    // "Try now" asks again, and is still refused.
    await page.getByRole('button', { name: 'Try now' }).click();
    await page.waitForTimeout(500);
    await expect(banner).toBeVisible();

    // It comes back. The page picks it up on its next poll, with no reload.
    await server.start();
    await page.getByRole('button', { name: 'Try now' }).click();
    await expect(banner).toBeHidden({ timeout: 20000 });
    await expect(page.locator('.tb-right .down-pill')).toBeHidden();
    await expect(page.locator('#workspace')).not.toHaveAttribute('inert', '');
    await expect(page.locator('#keybar')).not.toHaveText(/last heard/);
  });

  test('a page that was not asked to try finds it again by itself', async ({ page }) => {
    await page.goto(PRIVATE_CONSOLE + 'traffic');
    await expect(page.locator('#down-banner')).toBeHidden();
    await server.stop();
    await expect(page.locator('#down-banner')).toBeVisible({ timeout: 20000 });
    await server.start();
    // No click: the page's own poll finds it.
    await expect(page.locator('#down-banner')).toBeHidden({ timeout: 20000 });
  });
});
