import type { APIRequestContext } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { createBucket } from '../fixtures/api';
import { BASE_URL } from '../playwright.config';

// Traffic only records calls that hit the raw AWS-protocol gateway. That
// gateway is mounted at "/" on the SAME host:port as the console (see
// cmd/doze-aws/main.go: `mux.Handle("/", rec)` alongside `mux.Handle("/_console/", con)`),
// wrapped by console.Recorder. The console's own UI actions go through an
// in-process client (peers.InProcess) straight to the stack and never touch
// the recorder — so they must NOT show up here (see the first describe block).
// BASE_URL is "http://host:port/_console/"; strip that down to the root to
// reach the raw gateway.
const GATEWAY_ROOT = new URL('/', BASE_URL).toString();

/** Sends a raw AWS JSON-protocol request straight to the gateway root, the
 *  exact shape confirmed against console/console_test.go's
 *  TestTrafficRecorder (X-Amz-Target header + application/x-amz-json-1.x
 *  Content-Type + JSON body — no console path involved). */
async function rawAwsJson(
  request: APIRequestContext,
  target: string,
  body: Record<string, unknown>,
  jsonVersion: '1.0' | '1.1' = '1.0'
) {
  return request.post(GATEWAY_ROOT, {
    headers: {
      'Content-Type': `application/x-amz-json-${jsonVersion}`,
      'X-Amz-Target': target,
    },
    data: JSON.stringify(body),
  });
}

// Locates a traffic row (the per-entry `x-data="{open:false}"` wrapper) by
// text anywhere inside it — including its collapsed `.tr-detail` body, whose
// text is present in the DOM (just display:none via x-show) so .textContent()
// / hasText matching sees it without needing to click the row open.

// The feed polls every 1.5s and a changed tick REPLACES the rows, so a click
// racing a swap lands on a node that leaves the DOM before htmx acts on it.
// Wait for one quiet tick — same hash across a full poll interval — before
// interacting with a row.
async function waitWireQuiet(page: import('@playwright/test').Page) {
  const feed = page.locator('#traffic-feed');
  await expect(async () => {
    const before = await feed.getAttribute('data-hash');
    await page.waitForTimeout(1700);
    expect(await feed.getAttribute('data-hash')).toBe(before);
  }).toPass({ timeout: 15000 });
}

function trafficRow(page: import('@playwright/test').Page, text: string) {
  // .tr-row, not [x-data]: the rows carry no x-data of their own (the Alpine
  // scope lives on the feed wrapper), so the old selector matched ONE big
  // wrapper for any text. Field assertions inside it worked by accident —
  // first row in document order — but clicking it clicked the wrapper's
  // centre, which is not the row, and the inspector never opened.
  return page.locator('#traffic-feed .tr-row', { hasText: text }).first();
}

test.describe('console actions stay out of Traffic', () => {
  test('a bucket created through the console API does not appear in the feed', async ({
    page,
    request,
    uniqueName,
  }) => {
    const bucket = uniqueName('e2e-traffic-hidden');
    // postForm -> POST /_console/s3/create, exactly what the real create-bucket
    // form submits. This is the in-process path, not the raw gateway.
    await createBucket(request, bucket);

    await page.goto('traffic');
    // #traffic-feed's own partial re-emits an element with the same id at its
    // root, so the client's morph-poll self-nests one extra `#traffic-feed`
    // inside the original on the very first tick (verified: count goes 1 -> 2
    // after tick one and stays there) — a pre-existing quirk of this page,
    // not something this spec introduces. `.first()` sidesteps the resulting
    // strict-mode ambiguity; its content is a superset of the nested copy's,
    // so it's still the right target for both assertions below.
    const feed = page.locator('#traffic-feed').first();
    await expect(feed).toBeVisible();
    // #traffic-feed polls every 1500ms (data-live-ms). Wait out a couple of
    // ticks so the bucket has had every chance to show up if it were going
    // to, before asserting it never does.
    await page.waitForResponse((res) => res.url().includes('/traffic/feed'));
    await page.waitForResponse((res) => res.url().includes('/traffic/feed'));
    await expect(feed).not.toContainText(bucket);
  });
});

test.describe('raw gateway calls are recorded', () => {
  test('a raw SQS CreateQueue call appears with its service, action, and body', async ({
    page,
    request,
    uniqueName,
    waitForLive,
  }) => {
    const queueName = uniqueName('e2e-traffic-create');
    const res = await rawAwsJson(request, 'AmazonSQS.CreateQueue', { QueueName: queueName });
    expect(res.ok()).toBeTruthy();

    await page.goto('traffic');
    await waitForLive('#traffic-feed', (text) => text.includes(queueName));

    await waitWireQuiet(page);
    const row = trafficRow(page, queueName);
    await expect(row.locator('.act')).toHaveText('CreateQueue');
    await expect(row.locator('.svcb')).toHaveText('sqs');
    // The inspector is a drawer now, not an inline expand: clicking the row
    // fetches /traffic/entry into #t-drawer-inner.
    await row.click();
    await expect(page.locator('#t-drawer-inner pre').first()).toContainText(queueName);
  });
});

test.describe('filters', () => {
  test('only-errors and service filters narrow the visible rows', async ({
    page,
    request,
    uniqueName,
    waitForLive,
  }) => {
    const okQueue = uniqueName('e2e-traffic-ok');
    const missingQueue = uniqueName('e2e-traffic-missing');

    const okRes = await rawAwsJson(request, 'AmazonSQS.CreateQueue', { QueueName: okQueue });
    expect(okRes.ok()).toBeTruthy();
    // GetQueueUrl against a queue that was never created 400s
    // (AWS.SimpleQueueService.NonExistentQueue) — a real, gateway-produced error.
    const errRes = await rawAwsJson(request, 'AmazonSQS.GetQueueUrl', { QueueName: missingQueue });
    expect(errRes.status()).toBe(400);

    await page.goto('traffic');
    await waitForLive(
      '#traffic-feed',
      (text) => text.includes(okQueue) && text.includes(missingQueue)
    );

    const okRow = trafficRow(page, okQueue);
    const errRow = trafficRow(page, missingQueue);
    await expect(okRow).toBeVisible();
    await expect(errRow).toBeVisible();

    // "only errors": the successful CreateQueue row hides, the failing
    // GetQueueUrl row stays visible.
    const errCheckbox = page
      .locator('label.chip', { hasText: 'only errors' })
      .locator('input[type=checkbox]');
    await errCheckbox.check();
    await expect(okRow).toBeHidden();
    await expect(errRow).toBeVisible();
    await errCheckbox.uncheck();
    await expect(okRow).toBeVisible();

    // Service filter: a service neither row belongs to hides both; switching
    // back to sqs restores them.
    const svcSelect = page.locator('select.chip');
    await svcSelect.selectOption('kms');
    await expect(okRow).toBeHidden();
    await expect(errRow).toBeHidden();
    await svcSelect.selectOption('sqs');
    await expect(okRow).toBeVisible();
    await expect(errRow).toBeVisible();
  });
});

test.describe('secret redaction', () => {
  test('a SecretString value is masked in the captured request body', async ({
    page,
    request,
    uniqueName,
    waitForLive,
  }) => {
    const secretName = uniqueName('e2e-traffic-secret');
    const marker = uniqueName('plaintext-marker');
    const res = await rawAwsJson(
      request,
      'secretsmanager.CreateSecret',
      { Name: secretName, SecretString: marker },
      '1.1'
    );
    expect(res.ok()).toBeTruthy();

    await page.goto('traffic');
    await waitForLive('#traffic-feed', (text) => text.includes(secretName));

    await waitWireQuiet(page);
    const row = trafficRow(page, secretName);
    await expect(row.locator('.svcb')).toHaveText('sm');
    await row.click();
    await expect(page.locator('#t-drawer-inner pre').first()).toBeVisible();
    const body = await page.locator('#t-drawer-inner').textContent();
    expect(body).not.toContain(marker);
    expect(body).toContain('••••••'); // •••••• mask (redactKey's `mask` const)
    expect(body).toContain(secretName); // the non-secret Name field survives untouched
  });
});
