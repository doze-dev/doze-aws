import fs from 'node:fs';
import path from 'node:path';
import type { APIRequestContext, Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { createFunction, createQueue, postForm } from '../fixtures/api';
import { ACCOUNT_ID, ORIGIN } from '../playwright.config';

// What the independent re-audit of the SQS console (2026-10-16) found. Each
// test here was written and run red before the fix it holds. The key bar's
// states are in shell.spec.ts ("the key bar").

const ROOT = new URL('/', ORIGIN).toString();

async function sqsCall(request: APIRequestContext, target: string, body: object) {
  const res = await request.post(ROOT, {
    headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': `AmazonSQS.${target}` },
    data: JSON.stringify(body),
  });
  return res.json();
}

const queueUrl = (name: string) => `${ROOT}${ACCOUNT_ID}/${name}`;

/** The colour a state is drawn in on this theme, read from the stylesheet's own token. */
const tokenColour = (page: Page, token: string) =>
  page.evaluate((t) => {
    const probe = document.createElement('span');
    probe.style.color = `var(${t})`;
    document.body.appendChild(probe);
    const c = getComputedStyle(probe).color;
    probe.remove();
    return c;
  }, token);

async function openComposer(page: Page) {
  await page.locator('.dock-bar').click();
  await expect(page.locator('.sqs-compose')).toBeVisible();
}

test.describe('configuration edit', () => {
  test('"After receives" applies with the dead-letter queue left on keep, and a refused count is shown where it was typed', async ({
    page, request, uniqueName, waitForToast,
  }) => {
    const name = await createQueue(request, uniqueName('e2e-aud-count'), { dlqMode: 'new', maxReceive: 3 });
    const policy = async () => {
      const out = await sqsCall(request, 'GetQueueAttributes', { QueueUrl: queueUrl(name), AttributeNames: ['RedrivePolicy'] });
      return JSON.parse(out.Attributes.RedrivePolicy) as { deadLetterTargetArn: string; maxReceiveCount: number };
    };
    const was = await policy();
    expect(was.maxReceiveCount).toBe(3);

    await page.goto(`sqs/${name}?tab=config`);
    await page.getByRole('button', { name: 'Edit settings' }).click();
    await expect(page.locator('#sqs-config select[name=dlq]')).toHaveValue('keep');
    await page.locator('#sqs-config input[name=max_receive]').fill('7');
    await page.getByRole('button', { name: 'Save settings' }).click();
    await waitForToast();
    const now = await policy();
    expect(now.maxReceiveCount).toBe(7);
    expect(now.deadLetterTargetArn).toBe(was.deadLetterTargetArn);
    await expect(page.locator('.q-cfg .q-r', { hasText: 'Dead-letter queue' })).toContainText('received 7 times');

    // Sqs-21: the service refuses 5000, inline, in the refusal's colour, and what was typed stays.
    await page.getByRole('button', { name: 'Edit settings' }).click();
    await page.locator('#sqs-config input[name=max_receive]').fill('5000');
    await page.getByRole('button', { name: 'Save settings' }).click();
    const err = page.locator('#sqs-config .err').first();
    await expect(err).toContainText('InvalidAttributeValue');
    await expect(err).toContainText('maxReceiveCount');
    await expect(err.locator('.err-h')).toHaveCSS('color', await tokenColour(page, '--refused'));
    await expect(page.locator('#sqs-config input[name=max_receive]')).toHaveValue('5000');
    expect((await policy()).maxReceiveCount).toBe(7);
  });
});

test.describe('a disabled event source mapping', () => {
  test('is drawn as disabled, not as a reader', async ({ page, request, uniqueName }) => {
    const queue = await createQueue(request, uniqueName('e2e-aud-off'), { visibility: 30 });
    const fn = uniqueName('e2e-aud-off-fn');
    const empty = path.resolve(__dirname, `../.tmp/sqs-off-${fn}`);
    fs.mkdirSync(empty, { recursive: true });
    await createFunction(request, fn, { code: empty, timeout: 60 });
    const res = await request.post(`${ROOT}2015-03-31/event-source-mappings`, {
      headers: { 'Content-Type': 'application/json' },
      data: JSON.stringify({ FunctionName: fn, EventSourceArn: `arn:aws:sqs:us-east-1:${ACCOUNT_ID}:${queue}`, BatchSize: 5, Enabled: false }),
    });
    expect(res.status()).toBe(202);
    expect((await res.json()).State).toBe('Disabled');

    // The strip under the name: the function is there, as disabled, and nothing reads.
    await expect(async () => {
      await page.goto(`sqs/${queue}`);
      await expect(page.locator('.conn[data-nb] a.nb-link', { hasText: fn })).toBeVisible({ timeout: 500 });
    }).toPass({ timeout: 8000 });
    await expect(page.locator('.conn[data-nb] .nb-lbl', { hasText: 'read by' })).toHaveCount(0);
    await expect(page.locator('.conn[data-nb] .nb-lbl', { hasText: 'disabled' })).toBeVisible();

    // The Configuration tab claims no reader, and so no verdict on its timeout.
    await page.goto(`sqs/${queue}?tab=config`);
    await expect(page.locator('.stack-readers')).toHaveCount(0);
    await expect(page.locator('body')).not.toContainText('so one run finishes before the message returns');
  });
});

test.describe('an enabled event source mapping', () => {
  test('is still "read by", with the reader callout', async ({ page, request, uniqueName }) => {
    const queue = await createQueue(request, uniqueName('e2e-aud-on'), { visibility: 600 });
    const fn = uniqueName('e2e-aud-on-fn');
    const empty = path.resolve(__dirname, `../.tmp/sqs-on-${fn}`);
    fs.mkdirSync(empty, { recursive: true });
    await createFunction(request, fn, { code: empty, timeout: 60 });
    await postForm(request, `lambda/${fn}/add-mapping`, { queue, batch: 5 });
    await expect(async () => {
      await page.goto(`sqs/${queue}`);
      await expect(page.locator('.conn[data-nb] .nb-lbl', { hasText: 'read by' })).toBeVisible({ timeout: 500 });
    }).toPass({ timeout: 8000 });
    await expect(page.locator('.conn[data-nb] .nb-lbl', { hasText: 'disabled' })).toHaveCount(0);
    await page.goto(`sqs/${queue}?tab=config`);
    await expect(page.locator('.stack-readers .q-callout.ok', { hasText: fn })).toContainText('so one run finishes before the message returns');
  });
});

test.describe('the composer after a refusal', () => {
  test('outlines the body in the refusal\'s colour', async ({ page, request, uniqueName, setEditor }) => {
    const name = await createQueue(request, uniqueName('e2e-aud-outline'));
    await page.goto(`sqs/${name}`);
    await openComposer(page);
    const body = page.locator('.sqs-compose .CodeMirror').first();
    const before = await body.evaluate((e) => getComputedStyle(e).borderTopColor);
    await setEditor('textarea[name=body][data-editor]', 'bad ￾ char');
    await page.locator('.sqs-compose').getByRole('button', { name: 'Send' }).click();
    await expect(page.locator('.sqs-compose .err').first()).toContainText('InvalidMessageContents');
    const refused = await tokenColour(page, '--refused');
    expect(refused).not.toBe(before);
    // It may be mid-transition the first time it is read.
    await expect.poll(() => body.evaluate((e) => getComputedStyle(e).borderTopColor)).toBe(refused);
  });
});

test.describe('Consume sends what was typed', () => {
  for (const [field, value, param] of [['max', '11', 'MaxNumberOfMessages'], ['max', '0', 'MaxNumberOfMessages'], ['wait', '99', 'WaitTimeSeconds']] as const) {
    test(`${field} ${value} is refused by the service, inline, and stays typed`, async ({ page, request, uniqueName }) => {
      const name = await createQueue(request, uniqueName('e2e-aud-consume'));
      await postForm(request, `sqs/${name}/send`, { body: 'kept' });
      await page.goto(`sqs/${name}?tab=consume`);
      await page.locator(`#sqs-consume input[name=${field}]`).fill(value);
      await page.getByRole('button', { name: 'Receive' }).click();
      const err = page.locator('#sqs-consume .err').first();
      await expect(err).toContainText(param);
      await expect(page.locator(`#sqs-consume input[name=${field}]`)).toHaveValue(value);
      // Nothing was received on the way.
      await expect(page.locator('.rcv-row')).toHaveCount(0);
      const out = await sqsCall(request, 'GetQueueAttributes', { QueueUrl: queueUrl(name), AttributeNames: ['ApproximateNumberOfMessages'] });
      expect(out.Attributes.ApproximateNumberOfMessages).toBe('1');
    });
  }
});

test.describe('one tab stop per select', () => {
  const visible = (page: Page) =>
    page.evaluate(() => {
      const el = document.activeElement as HTMLElement | null;
      if (!el || el === document.body) return { ok: true, why: 'the end of the page' };
      const r = el.getBoundingClientRect();
      const s = getComputedStyle(el);
      const native = el.classList.contains('ds-native');
      // The create form's segmented controls are visually-hidden radios whose
      // label is the control (sr-only), which is the pattern, not a hidden stop.
      const sizeOk = el.classList.contains('sr-only') || (r.width > 8 && r.height > 8);
      return { ok: !native && sizeOk && s.opacity !== '0' && s.visibility !== 'hidden', why: `${el.tagName}.${el.className} ${Math.round(r.width)}x${Math.round(r.height)} opacity ${s.opacity}` };
    });

  test('the redrive dialog opens with a visible focus inside it, and Tab finds no invisible stop', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-aud-redrive'), { dlqMode: 'new' });
    const dlq = `${name}-dlq`;
    await postForm(request, `sqs/${dlq}/send`, { body: 'one' });
    await page.goto(`sqs/${dlq}`);
    await page.getByRole('button', { name: /^Redrive/ }).first().click();
    const dialog = page.getByRole('dialog', { name: 'Redrive messages' });
    await expect(dialog).toBeVisible();
    await expect.poll(() => page.evaluate(() => !!document.activeElement?.closest('[role=dialog]'))).toBe(true);
    const first = await visible(page);
    expect(first.ok, `the first focus is ${first.why}`).toBe(true);
    for (let i = 0; i < 6; i++) {
      await page.keyboard.press('Tab');
      const at = await visible(page);
      expect(at.ok, `Tab ${i + 1} stops on ${at.why}`).toBe(true);
    }
  });

  for (const [what, url] of [['the configuration form', 'sqs/{q}?tab=config'], ['the create form', 'sqs/create']] as const) {
    test(`Tab through ${what} never rests on the hidden native select`, async ({ page, request, uniqueName }) => {
      const q = await createQueue(request, uniqueName('e2e-aud-tabs'));
      await page.goto(url.replace('{q}', q));
      if (url.includes('config')) await page.getByRole('button', { name: 'Edit settings' }).click();
      // Under load the read view can still be on screen for a frame after the click;
      // a Tab pressed then rests on its copy buttons, which are about to be hidden.
      if (url.includes('config')) await expect(page.locator('#sqs-config .q-ids')).toBeHidden();
      await page.locator(url.includes('config') ? '#sqs-config input[name=visibility]' : 'input[name=name]').focus();
      for (let i = 0; i < 40; i++) {
        await page.keyboard.press('Tab');
        const at = await visible(page);
        expect(at.ok, `Tab ${i + 1} stops on ${at.why}`).toBe(true);
      }
    });
  }

  test('a select on another service has the same one stop', async ({ page, request, uniqueName }) => {
    const topic = uniqueName('e2e-aud-sel');
    await postForm(request, 'sns/create', { name: topic });
    await page.goto(`sns/${topic}`);
    const ds = page.locator('.ds').filter({ has: page.locator('select[name="protocol"]') });
    await ds.locator('.ds-trigger').focus();
    await page.keyboard.press('Shift+Tab');
    await page.keyboard.press('Tab');
    await expect(ds.locator('.ds-trigger')).toBeFocused();
    await page.keyboard.press('Tab');
    expect((await visible(page)).ok).toBe(true);
    await expect(ds.locator('select.ds-native')).not.toBeFocused();
  });
});

test.describe('the composer bar under a long list', () => {
  test('rows do not show through the gap under the bar', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-aud-dock'));
    for (let i = 0; i < 12; i++) await postForm(request, `sqs/${name}/send`, { body: `{"n":${i},"pad":"${'x'.repeat(30)}"}` });
    await page.setViewportSize({ width: 1440, height: 620 });
    await page.goto(`sqs/${name}`);
    await expect(page.locator('.msg')).toHaveCount(12, { timeout: 15_000 });
    // Scroll so rows are passing under the bar, and read what is on top at every pixel row of it and below.
    for (const at of [0.3, 0.6]) {
      await page.evaluate((f) => {
        const b = document.querySelector('.det-b') as HTMLElement;
        b.scrollTop = (b.scrollHeight - b.clientHeight) * f;
      }, at);
      const leaks = await page.evaluate(() => {
        const dock = document.querySelector('.composer-dock') as HTMLElement;
        const scroller = document.querySelector('.det-b') as HTMLElement;
        const d = dock.getBoundingClientRect(), s = scroller.getBoundingClientRect();
        const x = d.left + d.width / 2;
        const out: string[] = [];
        for (let y = Math.ceil(d.top) + 1; y < Math.floor(s.bottom) - 1; y += 2) {
          const top = document.elementFromPoint(x, y);
          if (top && !dock.contains(top) && top.closest('.msg, .q-table, .q-bar')) out.push(`y=${y} shows ${top.className}`);
        }
        return out;
      });
      expect(leaks, `scrolled to ${at}`).toEqual([]);
    }
  });
});

test.describe('the list pane', () => {
  test('is 262 wide beside the detail, and the head still fits at 1101', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-aud-width-with-a-rather-long-queue-name'));
    for (const width of [1440, 1101]) {
      await page.setViewportSize({ width, height: 800 });
      await page.goto(`sqs/${name}`);
      expect(Math.round((await page.locator('.listpane').boundingBox())!.width)).toBe(262);
      const fits = await page.evaluate(() => {
        const over = Array.from(document.querySelectorAll('.listpane .lp-h, .listpane .li')).filter((e) => e.scrollWidth > e.clientWidth + 1 && getComputedStyle(e).overflow === 'visible');
        return { page: document.documentElement.scrollWidth <= window.innerWidth, over: over.map((e) => e.className) };
      });
      expect(fits.page, `the page scrolls sideways at ${width}`).toBe(true);
      expect(fits.over).toEqual([]);
    }
  });
});

test.describe('the States board', () => {
  test('a send toasts "Sent to <queue>"', async ({ page, request, uniqueName, waitForToast }) => {
    const name = await createQueue(request, uniqueName('e2e-aud-toast'));
    await page.goto(`sqs/${name}`);
    await openComposer(page);
    await page.locator('.sqs-compose .CodeMirror').click();
    await page.keyboard.type('hello');
    await page.locator('.sqs-compose').getByRole('button', { name: 'Send' }).click();
    expect(await waitForToast()).toContain(`Sent to ${name}`);
  });

  test('the empty Messages state offers "Send one now", which opens the composer', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-aud-empty'));
    await page.goto(`sqs/${name}`);
    const empty = page.locator('.q-emptycard');
    await expect(empty).toContainText('No visible messages.');
    await expect(empty).toContainText('Send one below — it lands here.');
    await empty.getByRole('button', { name: 'Send one now' }).click();
    await expect(page.locator('.sqs-compose')).toBeVisible();
    await expect(page.locator('.sqs-compose .CodeMirror-focused')).toHaveCount(1);
  });

  test('New is dimmed while a queue is being made, and only then', async ({ page }) => {
    await page.goto('sqs/create');
    const n = page.locator('.listpane .new-link');
    await expect(n).toHaveClass(/is-current/);
    await expect(n).toHaveAttribute('aria-disabled', 'true');
    await page.goto('sqs');
    await expect(page.locator('.listpane .new-link')).not.toHaveClass(/is-current/);
  });
});

// The third audit (2026-10-16, after the second round's fixes).

test.describe('under a dialog', () => {
  test('the key bar names only what the dialog answers to, and j, k and / stay quiet behind it', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-aud-dlg'), { dlqMode: 'new' });
    const dlq = `${name}-dlq`;
    await postForm(request, `sqs/${dlq}/send`, { body: 'one' });
    await page.goto(`sqs/${dlq}`);
    const bar = page.locator('#keybar');
    await expect(bar).toContainText('j');
    await page.getByRole('button', { name: /^Redrive/ }).first().click();
    await expect(page.getByRole('dialog', { name: 'Redrive messages' })).toBeVisible();
    await expect(bar).not.toContainText('⌘⏎');
    await expect(bar).not.toContainText('/');
    await expect(bar).toContainText('esc');
    // The page behind keeps its cursor and its focus.
    const behind = () => page.evaluate(() => ({
      cursor: document.querySelectorAll('.listpane .li.cursor').length,
      filter: document.activeElement?.closest('.listpane .filter') ? 1 : 0,
    }));
    const before = await behind();
    await page.keyboard.press('j');
    await page.keyboard.press('k');
    await page.keyboard.press('/');
    expect(await behind()).toEqual(before);
    // And the bar comes back when the dialog goes.
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog', { name: 'Redrive messages' })).toBeHidden();
    await expect(bar).toContainText('⌘K');
    await expect(bar).toContainText('j');
  });

  test('a delete confirm does the same', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-aud-dlg2'));
    await page.goto(`sqs/${name}`);
    await page.getByRole('button', { name: /^Delete/ }).first().click();
    await expect(page.locator('#confirm')).toBeVisible();
    await expect(page.locator('#keybar')).not.toContainText('⌘⏎');
    await page.keyboard.press('/');
    expect(await page.evaluate(() => !!document.activeElement?.closest('.listpane .filter'))).toBe(false);
    await page.locator('#confirm-no').click();
    await expect(page.locator('#keybar')).toContainText('j');
  });
});

test.describe('configuration edit on a queue with no dead-letter queue', () => {
  test('offers no count it would drop', async ({ page, request, uniqueName, waitForToast }) => {
    const name = await createQueue(request, uniqueName('e2e-aud-nodlq'), { dlqMode: 'none' });
    const other = await createQueue(request, uniqueName('e2e-aud-nodlq-t'), { dlqMode: 'none' });
    await page.goto(`sqs/${name}?tab=config`);
    await page.getByRole('button', { name: 'Edit settings' }).click();
    await expect(page.locator('#sqs-config select[name=dlq]')).toHaveValue('keep');
    await expect(page.locator('#sqs-config input[name=max_receive]')).toBeDisabled();
    await expect(page.locator('#sqs-config .cfg-after')).toBeHidden();
    // Choosing a queue to dead-letter to makes the count real.
    await page.locator('#sqs-config select[name=dlq]').selectOption(other);
    await expect(page.locator('#sqs-config input[name=max_receive]')).toBeEnabled();
    await expect(page.locator('#sqs-config .cfg-after')).toBeVisible();
    // And choosing none switches it off again.
    await page.locator('#sqs-config select[name=dlq]').selectOption('none');
    await expect(page.locator('#sqs-config input[name=max_receive]')).toBeDisabled();
  });
});

test.describe('the wire page', () => {
  test('shows a lone slow call when it finishes, with nothing else running', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-aud-slow'));
    await page.goto('');
    await expect(page.locator('#traffic-feed')).toBeVisible();
    expect(await page.locator('#traffic-feed').innerText()).not.toContain(name);
    // A long-polling receive on an empty queue takes its full wait.
    await sqsCall(request, 'ReceiveMessage', { QueueUrl: queueUrl(name), WaitTimeSeconds: 2 });
    await expect.poll(async () => (await page.locator('#traffic-feed').innerText()).includes(name), { timeout: 8000 }).toBe(true);
  });
});

test.describe('the not-found page', () => {
  test('reads as sentences', async ({ page }) => {
    await page.goto('no-such-page-here');
    const empty = page.locator('.empty');
    await expect(empty).toContainText('That address does not exist.');
    // The jump hint is one line, not a line per fragment.
    const h = await empty.locator('span', { hasText: 'to jump to anything' }).first().evaluate((e) => e.getBoundingClientRect().height);
    expect(h).toBeLessThan(40);
  });
});
