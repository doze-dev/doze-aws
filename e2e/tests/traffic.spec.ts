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

// In order, in one worker. The last test presses "clear", which empties the
// recorder's ring for every viewer; run beside the others it wiped rows they
// were still waiting to see (verified: two of them failed that way). 'default'
// rather than 'serial' so one failure does not skip the rest.
test.describe.configure({ mode: 'default' });

/** Sends a raw AWS JSON-protocol request straight to the gateway root, the
 *  exact shape confirmed against internal/console/console_test.go's
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

// The feed polls every 1.5 s. It is morphed in place, not replaced (a row node held across a
// changed tick is still in the document: measured), so a click cannot land on a node a tick
// removes. A quiet tick (the same hash across a poll interval) is still the calmest moment to
// read a row, so it is waited for, but only for a few seconds: the instance is shared with every
// other spec in a full run, whose calls keep the hash moving, and waiting for a quiet that never
// comes failed the test (a SecretString redaction check, 15 s, in a loaded full run) for a
// condition the test does not depend on.
async function waitWireQuiet(page: import('@playwright/test').Page) {
  const feed = page.locator('#traffic-feed');
  await expect(async () => {
    const before = await feed.getAttribute('data-hash');
    await page.waitForTimeout(1700);
    expect(await feed.getAttribute('data-hash')).toBe(before);
  }).toPass({ timeout: 5000 }).catch(() => {});
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
    // `.first()` is belt and braces now. #traffic-feed's partial re-emits an
    // element with the same id at its root, and the live poll used to nest one
    // extra `#traffic-feed` inside the original on the first tick (count went
    // 1 -> 2 and stayed there) because the swap style fell through to
    // innerHTML — morphing was configured but had never actually activated.
    // htmx 4 makes outerMorph a built-in swap style with nothing to activate,
    // and the count now stays at 1 across ticks (re-verified). Kept because it
    // costs nothing and the id really is duplicated in the response.
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
    // Not row.click(): the row centre is the resource LINK now, and clicking
    // it navigates to the queue page instead of opening the inspector.
    await row.locator('.act').click();
    await expect(page.locator('#t-drawer-inner pre').first()).toContainText(queueName);
  });
});

test.describe('one colour per state', () => {
  // The inspector's status chip was red for any status of 400 or more, so a
  // refusal (purple) had a red chip above its own purple panel. The chip, the
  // row and the panel are one state and one colour, and the row says the state
  // in a word.
  test('a refused call is refused on its row, its chip and its panel', async ({
    page,
    request,
    uniqueName,
    waitForLive,
  }) => {
    const queueName = uniqueName('e2e-traffic-state');
    expect((await rawAwsJson(request, 'AmazonSQS.CreateQueue', { QueueName: queueName })).ok()).toBeTruthy();
    // A send with no body: SQS refuses it for its input.
    const refused = await rawAwsJson(request, 'AmazonSQS.SendMessage', {
      QueueUrl: `${GATEWAY_ROOT}000000000000/${queueName}`,
    });
    expect(refused.status()).toBe(400);

    await page.goto('traffic');
    await waitForLive('#traffic-feed', (text) => text.includes('SendMessage') && text.includes(queueName));
    await waitWireQuiet(page);

    const row = page.locator('#traffic-feed .tr-row.refused', { hasText: queueName }).first();
    await expect(row.locator('.st')).toHaveClass(/\brefused\b/);
    // The word is on the row beside the status (board Main: "400 refused").
    await expect(row.locator('.st')).toHaveText('400 refused');

    await row.locator('.act').click();
    const chip = page.locator('#t-drawer-inner .td-h .st');
    await expect(chip).toHaveText('400 refused');
    await expect(chip).toHaveClass(/\brefused\b/);
    await expect(page.locator('#t-drawer-inner .td-meta .err')).toHaveCount(0);

    // The same colour, read off the page: chip, row chip and the reason's heading.
    const colour = (sel: string) => page.locator(sel).first().evaluate((el) => getComputedStyle(el).color);
    const chipColour = await colour('#t-drawer-inner .td-h .st');
    expect(chipColour).toBe(await row.locator('.st').evaluate((el) => getComputedStyle(el).color));
    // The reason's panel is the same state, and its code is in the state's colour too.
    await expect(page.locator('#t-drawer-inner .td-callout.td-refused')).toBeVisible();

    // And it is not the colour of a failure, nor of a call that was served.
    const servedRow = page.locator('#traffic-feed .tr-row', { hasText: 'CreateQueue' }).filter({ hasText: queueName }).first();
    await expect(servedRow.locator('.st')).toHaveClass(/\bserved\b/);
    await expect(servedRow.locator('.st')).toHaveText('200');
    expect(chipColour).not.toBe(await servedRow.locator('.st').evaluate((el) => getComputedStyle(el).color));
  });
});

test.describe('filters', () => {
  test('the answer filter and the service menu narrow the visible rows', async ({
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
    // And a call to another service, for the service filter to tell apart.
    const bucket = uniqueName('e2e-traffic-bucket');
    expect((await request.put(`${GATEWAY_ROOT}${bucket}`)).ok()).toBeTruthy();

    await page.goto('traffic');
    await waitForLive(
      '#traffic-feed',
      (text) => text.includes(okQueue) && text.includes(missingQueue) && text.includes(bucket)
    );

    const okRow = trafficRow(page, okQueue);
    const errRow = trafficRow(page, missingQueue);
    const bucketRow = trafficRow(page, bucket);
    await expect(okRow).toBeVisible();
    await expect(errRow).toBeVisible();
    await expect(bucketRow).toBeVisible();

    // Errors: the successful CreateQueue row hides, the failing GetQueueUrl row
    // stays visible. All brings it back.
    const seg = page.getByRole('group', { name: 'Filter by answer' });
    await seg.getByRole('button', { name: 'Errors' }).click();
    await expect(okRow).toBeHidden();
    await expect(errRow).toBeVisible();
    await seg.getByRole('button', { name: 'All' }).click();
    await expect(okRow).toBeVisible();

    // Service filter: a service neither row belongs to hides both, and the
    // service they do belong to hides the one that does not. The menu lists the
    // services that are in the window, each with its calls.
    const menu = page.getByRole('button', { name: 'Filter by service' });
    await menu.click();
    await page.getByRole('menuitem', { name: /^S3/ }).click();
    await expect(menu).toContainText('S3');
    await expect(okRow).toBeHidden();
    await expect(errRow).toBeHidden();
    await expect(bucketRow).toBeVisible();
    await menu.click();
    await page.getByRole('menuitem', { name: /^SQS/ }).click();
    await expect(menu).toContainText('SQS');
    await expect(okRow).toBeVisible();
    await expect(errRow).toBeVisible();
    await expect(bucketRow).toBeHidden();
    await menu.click();
    await page.getByRole('menuitem', { name: 'every service' }).click();
    await expect(menu).toContainText('every service');
    await expect(bucketRow).toBeVisible();
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
    await row.locator('.act').click();
    await expect(page.locator('#t-drawer-inner pre').first()).toBeVisible();
    const body = await page.locator('#t-drawer-inner').textContent();
    expect(body).not.toContain(marker);
    expect(body).toContain('••••••'); // •••••• mask (redactKey's `mask` const)
    expect(body).toContain(secretName); // the non-secret Name field survives untouched
  });
});

// "clear" empties the recorder's ring for everyone. Only this file reads the
// feed, so the other tests here are what it could disturb; this test asserts
// only on its own row disappearing, never on an empty feed, so a sibling's
// call landing afterwards cannot fail it.
test.describe('clear', () => {
  test('Clear empties the feed and recorded calls stay gone', async ({
    page,
    request,
    uniqueName,
    waitForLive,
    confirmDialog,
  }) => {
    const queueName = uniqueName('e2e-traffic-clear');
    const res = await rawAwsJson(request, 'AmazonSQS.CreateQueue', { QueueName: queueName });
    expect(res.ok()).toBeTruthy();

    await page.goto('traffic');
    await waitForLive('#traffic-feed', (text) => text.includes(queueName));

    const [resp] = await Promise.all([
      page.waitForResponse((r) => r.url().includes('/traffic/clear') && r.request().method() === 'POST'),
      (async () => {
        await page.getByRole('button', { name: 'Clear', exact: true }).click();
        await confirmDialog('accept');
      })(),
    ]);
    expect(resp.ok()).toBeTruthy();
    await expect(page.locator('#traffic-feed').first()).not.toContainText(queueName);

    // Not just a repaint: a fresh load of the page still lacks the call.
    await page.reload();
    await expect(page.locator('#traffic-feed').first()).toBeVisible();
    await expect(page.locator('#traffic-feed').first()).not.toContainText(queueName);
  });
});
