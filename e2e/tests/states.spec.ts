import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { createBucket, createFunction, createQueue, postForm } from '../fixtures/api';
import { BASE_URL } from '../playwright.config';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const GATEWAY_ROOT = new URL('/', BASE_URL).toString();

// The six moments (plan/03-shell-fidelity.md 2.12, S8; board States). What is
// asserted here is what the board says, not what the page happened to do:
//
//   It did not work   the service's own refusal, inline, beside the control, in
//                     the Refused style, with what was typed still in the form.
//                     (A browser bubble that stops the submit is not that.)
//   Four answers      each with a word as well as a colour; "it threw" is a
//                     Lambda function that raised.
//   It cannot be undone  a card that names the thing, says what follows, and
//                     whose buttons are the "Keep it" and the verb.
//   It is taking a while  a thin bar once a request has taken longer than 300 ms.

// ---------------------------------------------------------------------------
// It did not work: novalidate (D8). Five services' create forms, each given a
// value its own constraint attributes (min, max, pattern) would have stopped in
// the browser. The request must reach the service, and the service's sentence
// is what is shown.
// ---------------------------------------------------------------------------

/** Submits the form and returns what came back: the refusal the page placed,
 *  and whether the request was ever made. */
async function submitAndExpectRefusal(page: Page, urlPart: string, button: string | RegExp) {
  const reached = page.waitForResponse((r) => r.url().includes(urlPart) && r.request().method() === 'POST');
  await page.getByRole('button', { name: button }).click();
  const res = await reached;
  // The request reached the service and the service said no.
  expect(res.status()).toBe(400);
  const err = page.locator('.err[data-doze-err]');
  await expect(err).toBeVisible();
  // Refused, never red: the Refused style, not the failure's.
  await expect(err).toHaveClass(/err-refused/);
  return err;
}

test.describe('a value the browser would have stopped reaches the service', () => {
  test('S3: a bucket name that breaks the pattern', async ({ page, uniqueName }) => {
    await page.goto('s3/create');
    const name = `AB-${uniqueName('x')}`; // uppercase: not in [a-z0-9.-]
    await page.locator('input[name="name"]').fill(name);
    const err = await submitAndExpectRefusal(page, '/s3/create', 'Create bucket');
    await expect(err).toContainText('InvalidBucketName');
    // The form keeps what was typed, and nothing was created.
    await expect(page.locator('input[name="name"]')).toHaveValue(name);
    await expect(page).toHaveURL(/s3\/create/);
  });

  test('SNS: a topic name with a space in it', async ({ page }) => {
    await page.goto('sns/create');
    await page.locator('input[name="name"]').fill('bad name!');
    const err = await submitAndExpectRefusal(page, '/sns/create', 'Create topic');
    await expect(err).toContainText('InvalidParameter');
    await expect(page.locator('input[name="name"]')).toHaveValue('bad name!');
  });

  test('Lambda: a memory size under the minimum (min=128 on the input)', async ({ page, uniqueName }) => {
    await page.goto('lambda/create');
    const name = uniqueName('lam-novalidate');
    await page.locator('input[name="name"]').fill(name);
    await page.locator('input[name="handler"]').fill('bootstrap');
    await page.locator('input[name="code"]').fill(path.resolve(__dirname, '../fixtures'));
    await page.locator('input[name="memory"]').fill('99');
    const err = await submitAndExpectRefusal(page, '/lambda/create', 'Create function');
    await expect(err).toContainText('InvalidParameterValueException');
    await expect(err).toContainText('memorySize');
    await expect(page.locator('input[name="memory"]')).toHaveValue('99');
    await expect(page.locator('input[name="name"]')).toHaveValue(name);
  });

  test('Kinesis: a stream name its pattern forbids', async ({ page }) => {
    await page.goto('kinesis/create');
    await page.locator('input[name="name"]').fill('bad name!');
    const err = await submitAndExpectRefusal(page, '/kinesis/create', /Create stream/);
    await expect(err).toContainText('ValidationException');
    await expect(page.locator('input[name="name"]')).toHaveValue('bad name!');
  });

  test('KMS: an alias with characters its pattern forbids', async ({ page }) => {
    await page.goto('kms/create');
    await page.locator('input[name="alias"]').fill('bad alias!');
    const err = await submitAndExpectRefusal(page, '/kms/create', 'Create key');
    await expect(err).toContainText(/alias/i);
    await expect(page.locator('input[name="alias"]')).toHaveValue('bad alias!');
  });

  test('Secrets Manager: a secret name it does not accept', async ({ page }) => {
    await page.goto('sm/create');
    await page.locator('input[name="name"]').fill('bad name!');
    const err = await submitAndExpectRefusal(page, '/sm/create', 'Store secret');
    await expect(err).toContainText('InvalidRequestException');
  });

  test('an empty required name is the service\'s to refuse, not the browser\'s', async ({ page }) => {
    await page.goto('sns/create');
    const err = await submitAndExpectRefusal(page, '/sns/create', 'Create topic');
    await expect(err).toContainText('InvalidParameter');
  });
});

// ---------------------------------------------------------------------------
// It cannot be undone: the confirm (D11, A13, boards Sqs-16, S3-12, States).
// ---------------------------------------------------------------------------

test.describe('the confirm is the board\'s card', () => {
  test('a destructive confirm says "Keep it" and the verb is a red outline', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    const bucket = uniqueName('e2e-keep');
    await createBucket(request, bucket);
    await page.goto(`s3/${bucket}`);
    await page.locator('.acts').getByRole('button', { name: 'Delete' }).click();

    const box = page.locator('#confirm');
    await expect(box).toBeVisible();
    // The cancel word is set from the confirm's own reading of the verb, not at
    // the call site: this site does not carry data-confirm-cancel.
    await expect(page.locator('#confirm-no')).toHaveText('Keep it');
    await expect(page.locator('#confirm-yes')).toHaveText('Delete');

    // A card, not a dialog with a footer strip.
    await expect(box.locator('.dialog-f')).toHaveCount(0);
    // Outlined buttons: neither is a solid fill. The verb's outline is red; the
    // cancel's is not.
    const paint = (sel: string) =>
      page.locator(sel).evaluate((el) => {
        const s = getComputedStyle(el);
        return { bg: s.backgroundColor, fg: s.color, border: s.borderTopColor };
      });
    const yes = await paint('#confirm-yes');
    const no = await paint('#confirm-no');
    expect(yes.fg).toBe(yes.border); // outlined in the colour of its text
    expect(yes.bg).not.toBe(yes.fg); // not a solid red button
    expect(no.border).not.toBe(yes.border);
    await expect(page.locator('#confirm-yes')).not.toHaveClass(/btn-destructive/);

    // Order and place: Keep it, then the verb, on the right; the card is
    // centred in the window.
    const [k, d] = await Promise.all([
      page.locator('#confirm-no').boundingBox(),
      page.locator('#confirm-yes').boundingBox(),
    ]);
    expect(k!.x).toBeLessThan(d!.x);
    const card = (await page.locator('#confirm .dialog').boundingBox())!;
    const vp = page.viewportSize()!;
    expect(Math.abs(card.x + card.width / 2 - vp.width / 2)).toBeLessThan(4);
    expect(Math.abs(card.y + card.height / 2 - vp.height / 2)).toBeLessThan(vp.height * 0.2);
    await confirmDialog('cancel');
  });

  test('a confirm that loses nothing says Cancel, and its verb is not red', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    // Kinesis "Rescale the stream?" (verb Rescale: closes shards, loses no data).
    const stream = uniqueName('ks-cancel');
    await postForm(request, 'kinesis/create', { name: stream, shards: 1 });
    await page.goto(`kinesis/${stream}/details`);
    await page.locator('input[name="shards"]').fill('2');
    await page.locator('form:has(input[name="shards"])').getByRole('button', { name: 'Apply' }).click();
    await expect(page.locator('#confirm')).toBeVisible();
    await expect(page.locator('#confirm-no')).toHaveText('Cancel');
    await expect(page.locator('#confirm-yes')).toHaveText('Rescale');
    await confirmDialog('cancel');
  });

  test('a call site that names its own cancel word keeps it', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    const queue = await createQueue(request, uniqueName('e2e-purge'));
    await page.goto(`sqs/${queue}`);
    await page.getByRole('button', { name: 'Purge' }).click();
    await expect(page.locator('#confirm-no')).toHaveText('Keep them');
    await confirmDialog('cancel');
  });
});

// ---------------------------------------------------------------------------
// Four answers, four colours: "it threw" (D7). A Lambda invoke whose function
// raised is a 200 with X-Amz-Function-Error; the wire says so in a word.
// ---------------------------------------------------------------------------

test.describe('it threw', () => {
  const SRC = path.resolve(__dirname, '../fixtures/lambda-handler');
  const DIR = path.resolve(__dirname, '../.tmp/lambda-fixture-states');

  test.beforeAll(() => {
    fs.mkdirSync(DIR, { recursive: true });
    execFileSync('go', ['build', '-o', path.join(DIR, 'bootstrap'), '.'], {
      cwd: SRC,
      env: { ...process.env, GOWORK: 'off' },
    });
  });

  test('a function that raised is a state of its own on the wire', async ({ page, request, uniqueName }) => {
    const fn = uniqueName('lam-threw');
    await createFunction(request, fn, { code: DIR, timeout: 10 });
    const why = `boom-${fn}`;

    const res = await request.post(`${GATEWAY_ROOT}2015-03-31/functions/${fn}/invocations`, {
      data: JSON.stringify({ raise: why }),
      headers: { 'Content-Type': 'application/json' },
      timeout: 30000,
    });
    // The service already says so: a 200, and the header carries the fact.
    expect(res.status()).toBe(200);
    expect(res.headers()['x-amz-function-error']).toBeTruthy();

    await page.goto('');
    const row = page.locator('.tr-row', { hasText: fn }).first();
    await expect(row).toBeVisible();
    // The word is on the row, as well as the colour.
    await expect(row.locator('.st')).toHaveText('it threw');
    await expect(row).toHaveClass(/threw/);
    await expect(row.locator('.st')).toHaveClass(/threw/);
    // And what it raised is on the row, beside the resource.
    await expect(row.locator('.tr-why-t')).toContainText(why);
    // "Needs a look" names the function and says how often, from the ring.
    const callout = page.locator('.wire-callout', { hasText: `${fn} answered with an error` });
    await expect(callout).toBeVisible();
    await expect(callout).toContainText(/An Invoke came back with a function error \d+ times? in the last 500 calls\./);

    // The inspector says what the function threw.
    // The feed re-renders while it polls, and a click that lands in the middle
    // of a swap opens nothing: click until the inspector is up (the pattern the
    // other wire specs use).
    const drawer = page.locator('#t-drawer-inner');
    await expect(async () => {
      await row.locator('.rowopen').click({ timeout: 2000 });
      await expect(drawer.locator('.td-callout.td-threw')).toBeVisible({ timeout: 1500 });
    }).toPass({ timeout: 15000 });
    await expect(drawer.locator('.td-callout.td-threw')).toContainText(why);
    // The chip in its header says it too.
    await expect(drawer.locator('.td-h .st')).toHaveText('it threw');

    // And it is an error for the Errors filter, though its status is 200.
    await page.getByRole('button', { name: 'Errors', exact: true }).click();
    await expect(row).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// It is taking a while: a thin bar after 300 ms, the page stays (board States).
// ---------------------------------------------------------------------------

test.describe('it is taking a while', () => {
  test('a slow mutation draws the bar after 300 ms and the page underneath stays', async ({
    page,
    uniqueName,
  }) => {
    await page.goto('s3/create');
    await page.locator('input[name="name"]').fill(uniqueName('e2e-slow'));
    let release: () => void = () => {};
    const held = new Promise<void>((r) => (release = r));
    await page.route('**/s3/create', async (route) => {
      await held;
      await route.continue();
    });
    await page.getByRole('button', { name: 'Create bucket' }).click();
    const bar = page.locator('.reqbar');
    await expect(bar).toHaveClass(/\bon\b/, { timeout: 3000 });
    // The form is still there, as it was: no blank page, no overlay.
    await expect(page.locator('input[name="name"]')).toBeVisible();
    release();
    await expect(page).toHaveURL(/s3\/e2e-slow/);
    await expect(bar).not.toHaveClass(/\bon\b/);
  });

  test('a quick one never flashes it', async ({ page, uniqueName }) => {
    await page.goto('s3/create');
    await page.locator('input[name="name"]').fill(uniqueName('e2e-fast'));
    await page.getByRole('button', { name: 'Create bucket' }).click();
    await expect(page).toHaveURL(/s3\/e2e-fast/);
    expect(await page.locator('.reqbar.on').count()).toBe(0);
  });
});
