import { spawn } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { createBucket, createQueue, postForm } from '../fixtures/api';
import { BASE_URL, ORIGIN } from '../playwright.config';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

/** A second doze-aws with nothing in it, for a page that is about having
 *  nothing. It is the binary the suite already built, on the next-but-six port
 *  of the run's own, with a data directory of its own. */
async function freshInstance(): Promise<{ url: string; stop: () => Promise<void> }> {
  const base = Number(new URL(ORIGIN).port);
  const port = base + 7;
  const dir = path.resolve(__dirname, `../.tmp/data-fresh-${port}`);
  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(dir, { recursive: true });
  const child = spawn(path.resolve(__dirname, `../.tmp/bin/doze-aws-${base}`), ['--listen', `127.0.0.1:${port}`, '--data-dir', dir, '--console'], { cwd: dir, stdio: 'ignore' });
  const url = `http://127.0.0.1:${port}/_console/`;
  for (let i = 0; i < 100; i++) {
    if (await fetch(url).then((r) => r.ok, () => false)) break;
    await new Promise((r) => setTimeout(r, 100));
  }
  return {
    url,
    stop: async () => {
      child.kill();
      fs.rmSync(dir, { recursive: true, force: true });
    },
  };
}

// Console-chrome behaviors that live outside #workspace and so must survive
// every htmx swap: theme, rail, palette, confirm dialogs, toasts, keyboard
// nav, and boosted navigation. Uses S3 as an incidental action surface for
// the confirm/toast tests — this spec is about the chrome, not S3.

test.describe('theme', () => {
  test('selects a mode and persists across reload', async ({ page }) => {
    // #theme-toggle was a two-state flip; the control is the three-button
    // #appearance group now (light / system / dark), where "system" is the
    // ABSENCE of data-theme so the OS preference can answer via CSS alone.
    await page.goto('');
    const html = page.locator('html');
    await page.locator('#appearance button[data-mode="dark"]').click();
    await expect(html).toHaveAttribute('data-theme', 'dark');
    await page.reload();
    await expect(html).toHaveAttribute('data-theme', 'dark');
    // Back to system: the attribute goes away entirely rather than flipping.
    await page.locator('#appearance button[data-mode="system"]').click();
    await expect(html).not.toHaveAttribute('data-theme');
    await page.reload();
    await expect(html).not.toHaveAttribute('data-theme');
  });
});

test.describe('rail', () => {
  test('collapses via button and the [ shortcut, persists across reload', async ({ page }) => {
    await page.goto('');
    const html = page.locator('html');
    await expect(html).not.toHaveAttribute('data-rail', 'slim');

    await page.locator('#rail-toggle').click();
    await expect(html).toHaveAttribute('data-rail', 'slim');
    await page.reload();
    await expect(html).toHaveAttribute('data-rail', 'slim');

    // '[' toggles too, but only fires when focus isn't in an input/textarea.
    await page.locator('body').click();
    await page.keyboard.press('[');
    await expect(html).not.toHaveAttribute('data-rail', 'slim');
  });
});

// The shell's top bar and rail are the design's (plan/design: Main, Anatomy,
// Sqs-02-Pick; plan/03-shell-fidelity.md 2.1, 2.2, D1, D9). What is asserted is
// what is DRAWN, measured at the design's 1440 by 900.
const GATEWAY_ROOT = new URL('/', BASE_URL).toString();

const railWidth = (page: Page) =>
  page.locator('.rail').evaluate((el) => Math.round(el.getBoundingClientRect().width));
const box = (page: Page, sel: string) =>
  page.locator(sel).first().evaluate((el) => {
    const r = el.getBoundingClientRect();
    return { w: Math.round(r.width), h: Math.round(r.height), x: Math.round(r.x) };
  });

test.describe('top bar', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
  });

  test('is 44 high, with the brand and instance name, a 420 jump box, the endpoint, the region', async ({ page }) => {
    await page.goto('sqs');
    expect((await box(page, '.topbar')).h).toBe(44);
    const brand = page.locator('.topbar .brand');
    await expect(brand).toContainText('doze-aws');
    await expect(brand.locator('.inst')).toHaveText(/\S/); // the instance's name, whatever it is called
    await expect(brand).not.toContainText('console');

    const jump = page.locator('.topbar .tb-palette');
    await expect(jump).toContainText('Jump to a resource, or do something…');
    await expect(jump).toContainText('⌘K');
    expect((await box(page, '.topbar .tb-palette')).w).toBe(420);

    await expect(page.locator('.topbar .pill .live')).toBeVisible();
    await expect(page.locator('.topbar')).toContainText('127.0.0.1');
    // The region is a static chip: no board draws a selector and a selector
    // would be a control that cannot work.
    await expect(page.locator('.topbar .pill', { hasText: /^\s*[a-z]{2}-[a-z]+-\d\s*$/ })).toBeVisible();
    await expect(page.locator('.topbar select')).toHaveCount(0);
  });

  test('at 1024 it is the Narrow board: no instance name, the endpoint as :port, a 300 jump box', async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 768 });
    await page.goto('sqs');
    await expect(page.locator('.topbar .brand .inst')).toBeHidden();
    await expect(page.locator('.topbar .pill', { has: page.locator('.live') })).toHaveText(/^\s*:\d+\s*$/, { useInnerText: true });
    await expect(page.locator('.topbar .tb-palette')).toContainText('Jump or act…');
    expect((await box(page, '.topbar .tb-palette')).w).toBe(300);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  });

  test('the IAM mode is on every page, in one wording', async ({ page }) => {
    for (const path of ['', 'traffic', 'sqs', 's3', 'iam', 'connect', 'sqs/create', 'no-such-page']) {
      await page.goto(path);
      await expect(page.locator('.topbar .pill.iam'), path || '(home)').toHaveText(/^IAM (soft|enforce|off)$/);
    }
  });

  test('the theme switch is a compact control at the right end', async ({ page }) => {
    await page.goto('sqs');
    const right = await box(page, '.topbar .appearance');
    expect(right.x + right.w).toBeGreaterThan(1440 - 40);
    expect(right.h).toBeLessThanOrEqual(28);
  });

  test('the jump box opens the palette', async ({ page, openPalette }) => {
    await page.goto('sqs');
    await openPalette();
    await expect(page.locator('#pal-q')).toBeFocused();
  });
});

test.describe('rail by context', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
  });

  test('is labelled on the wire (196) and marks only inside a service (56)', async ({ page }) => {
    await page.goto('');
    expect(await railWidth(page)).toBe(196);
    await expect(page.locator('.rail .ri', { hasText: 'SQS' }).locator('.ri-nm')).toBeVisible();
    await expect(page.locator('.rail .rgroup', { hasText: 'Messaging' })).toBeVisible();

    await page.goto('sqs');
    expect(await railWidth(page)).toBe(56);
    await expect(page.locator('.rail .ri', { hasText: 'SQS' }).locator('.ri-nm')).toBeHidden();
    // Marks, not letters: each service keeps its AWS icon.
    await expect(page.locator('.rail .ri .aws-ic').first()).toBeVisible();

    for (const path of ['s3', 'connect', 'sqs/create', 'iam']) {
      await page.goto(path);
      expect(await railWidth(page), path).toBe(56);
    }
  });

  test('a boosted navigation changes the default without a reload', async ({ page }) => {
    await page.goto('');
    expect(await railWidth(page)).toBe(196);
    await page.locator('.rail .ri', { hasText: 'SQS' }).click();
    await page.waitForURL(/\/sqs$/);
    await expect.poll(() => railWidth(page)).toBe(56);
    await page.locator('.rail .ri', { hasText: 'The wire' }).click();
    await page.waitForURL((u) => !/\/sqs$/.test(u.pathname));
    await expect.poll(() => railWidth(page)).toBe(196);
  });

  test("the user's choice wins over the default, everywhere, and persists", async ({ page }) => {
    await page.goto('sqs');
    expect(await railWidth(page)).toBe(56);
    await page.locator('#rail-toggle').click();
    await expect.poll(() => railWidth(page)).toBe(196);
    // across a reload, and onto another service page
    await page.reload();
    expect(await railWidth(page)).toBe(196);
    await page.goto('s3');
    expect(await railWidth(page)).toBe(196);

    // and the other way round on the wire
    await page.evaluate(() => localStorage.removeItem('rail'));
    await page.goto('');
    expect(await railWidth(page)).toBe(196);
    await page.locator('#rail-toggle').click();
    await expect.poll(() => railWidth(page)).toBe(56);
    await page.reload();
    expect(await railWidth(page)).toBe(56);
    await page.goto('sqs');
    expect(await railWidth(page)).toBe(56);
  });

  test('narrow windows slim the rail by default, and an expanded rail stays', async ({ page }) => {
    await page.setViewportSize({ width: 1100, height: 800 });
    await page.goto('');
    expect(await railWidth(page)).toBe(56);
    await page.setViewportSize({ width: 1440, height: 900 });
    await expect.poll(() => railWidth(page)).toBe(196);
    await page.setViewportSize({ width: 1100, height: 800 });
    await page.locator('#rail-toggle').click(); // the user expands it at 1100
    await expect.poll(() => railWidth(page)).toBe(196);
    await page.reload();
    expect(await railWidth(page)).toBe(196);
  });

  test('a service name flies out beside its mark on hover and on focus', async ({ page }) => {
    await page.goto('sqs');
    const sqs = page.locator('.rail .ri', { hasText: 'SQS' });
    await expect(sqs.locator('.ri-fly')).toBeHidden();
    await sqs.hover();
    await expect(sqs.locator('.ri-fly')).toBeVisible();
    await expect(sqs.locator('.ri-fly')).toHaveText('SQS');
    await page.mouse.move(700, 500);
    await expect(sqs.locator('.ri-fly')).toBeHidden();
    // and by keyboard: Tab until the SQS link has focus
    for (let i = 0; i < 60; i++) {
      await page.keyboard.press('Tab');
      if (await sqs.evaluate((el) => el === document.activeElement)) break;
    }
    await expect(sqs.locator('.ri-fly')).toBeVisible();
  });

  test('on the wire, an open call collapses the rail to marks', async ({ page, request, uniqueName }) => {
    const name = uniqueName('e2e-rail');
    await request.post(GATEWAY_ROOT, {
      headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': 'AmazonSQS.CreateQueue' },
      data: JSON.stringify({ QueueName: name }),
    });
    await page.goto('');
    expect(await railWidth(page)).toBe(196);
    const row = page.locator('#traffic-feed .tr-row', { hasText: name }).first();
    // Click the row's time, not its middle: once the inspector is open the row
    // is narrower and its middle is the resource link, which navigates.
    await expect(async () => {
      await row.locator('.ts').click({ timeout: 2000 });
      await expect(page.locator('.t-drawer')).toBeVisible({ timeout: 1000 });
    }).toPass({ timeout: 15000 });
    await expect.poll(() => railWidth(page)).toBe(56);
    await row.locator('.ts').click(); // close it again
    await expect.poll(() => railWidth(page)).toBe(196);
  });

  test("an open call does not override the user's expanded rail", async ({ page, request, uniqueName }) => {
    const name = uniqueName('e2e-rail-user');
    await request.post(GATEWAY_ROOT, {
      headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': 'AmazonSQS.CreateQueue' },
      data: JSON.stringify({ QueueName: name }),
    });
    await page.addInitScript(() => localStorage.setItem('rail', 'wide'));
    await page.goto('');
    const row = page.locator('#traffic-feed .tr-row', { hasText: name }).first();
    await expect(async () => {
      await row.locator('.ts').click({ timeout: 2000 });
      await expect(page.locator('.t-drawer')).toBeVisible({ timeout: 1000 });
    }).toPass({ timeout: 15000 });
    expect(await railWidth(page)).toBe(196);
  });

  test('a refused call does not put a dot on its service', async ({ page, request }) => {
    await request.post(GATEWAY_ROOT, {
      headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': 'DynamoDB_20120810.DescribeTable' },
      data: JSON.stringify({ TableName: 'e2e-rail-no-such-table' }),
    });
    await page.goto('');
    // A service answering no to a bad input is doing its job; a dot is for a
    // failure (5xx) or a call a policy denied, and for the things that are
    // wrong on their own (an alarm firing, a message in a dead-letter queue).
    await expect(page.locator('.rail .ri', { hasText: 'DynamoDB' }).locator('.ri-dot')).not.toHaveClass(/warn|bad/);
    // The wire has none of its own: only services have dots.
    await expect(page.locator('.rail .ri', { hasText: 'Connect' }).locator('.ri-dot')).toHaveCount(0);
  });
});

test.describe('command palette', () => {
  test('opens, filters, navigates, and closes on Escape', async ({ page, openPalette }) => {
    await page.goto('');
    await openPalette();

    // Query "Parameter Store" (a fixed nav label containing a SPACE) rather
    // than a single-word service name: every uniqueName()-prefixed
    // resource this whole suite creates is kebab/dash-style with no
    // spaces (e.g. e2e-s3-*, e2e-traffic-*), so a query containing a space
    // can never substring-match a dynamic resource name, however much
    // state has accumulated on this long-lived shared server. (An earlier
    // version queried "S3", then "Traffic" — both got shadowed once a
    // sibling spec started creating same-named-substring resources of its
    // own; this is the collision-proof fix.)
    await page.locator('#pal-q').fill('Parameter Store');
    const navItem = page.locator('.pal-item', { hasText: 'Parameter Store' });
    await expect(navItem).toBeVisible();
    await expect(page.locator('.pal-item')).toHaveCount(1); // unambiguous

    // Select via keyboard (Enter does `location.href = item.url` in
    // shell.js), not a synthetic mouse click on the item: Playwright's
    // synthetic click on this animated overlay's link reliably lands on
    // the right element (confirmed via elementFromPoint) but doesn't
    // trigger navigation — a native DOM .click() does, so this is a real
    // Chromium/Playwright interaction quirk with the overlay's open
    // animation, not a selector or app bug. Keyboard selection is also
    // the more representative command-palette interaction anyway.
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('ArrowUp');
    await page.keyboard.press('Enter');
    await page.waitForURL(/\/ssm$/);

    await page.locator('#palette-open').click();
    await expect(page.locator('#palette')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('#palette')).toBeHidden();
  });

  // The Palette board (plan/design/boards/Palette.dc.html), drawn at 1440 by
  // 900: a dialog 700 wide with the query and a count of matches on one line,
  // the results under captions ("Go to", "Create"), the matched letters
  // underlined, the selected row carrying its ⏎, and a footer with the sentence
  // about an ARN and the legend of keys.
  test('is the board’s dialog: 700 wide, a count, captions, the match underlined, a footer legend', async ({
    page,
    request,
    uniqueName,
    openPalette,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    const queue = await createQueue(request, uniqueName('zqpal'));
    await page.goto('sqs');
    await openPalette();
    // Not yet a name: the first letters of one, so a create can offer itself.
    const typed = queue.slice(0, -1);
    await page.locator('#pal-q').fill(typed);

    // Layout boxes, not the painted ones: the dialog pops in over 140ms.
    const box = await page.locator('.palette').evaluate((e) => ({ w: e.offsetWidth, x: e.offsetLeft, y: e.offsetTop }));
    expect(box).toEqual({ w: 700, x: 370, y: 120 });

    // The label the board's input carries, for a reader who cannot see it.
    await expect(page.locator('#palette label', { hasText: 'Jump or act' })).toHaveCount(1);

    // One resource and the create that takes its name: two matches.
    await expect(page.locator('#pal-count')).toHaveText('2 matches');
    await expect(page.locator('.pal-cap')).toHaveText(['Go to', 'Create']);
    const row = page.locator('.pal-item', { hasText: 'queue' }).first();
    await expect(row.locator('.pal-name')).toContainText(queue);
    // What was typed is what is underlined, and no more of the name.
    await expect(row.locator('.pal-hit')).toHaveText(typed);
    await expect(row.locator('.pal-hit')).toHaveCSS('text-decoration-line', 'underline');
    // The selected row names its key; the others do not.
    await expect(page.locator('.pal-item.sel .k')).toHaveText('⏎');
    await expect(page.locator('.pal-item .k')).toHaveCount(1);

    // The footer: the sentence the page had no advertisement for, and the legend.
    const foot = page.locator('.pal-foot');
    await expect(foot).toContainText('Paste an ARN or a queue URL and it goes straight to the resource.');
    await expect(foot.locator('.k')).toHaveText(['↑', '↓', '⏎', 'esc']);
    await expect(foot).toContainText('move');
    await expect(foot).toContainText('close');
  });

  test('says how many match, one or none', async ({ page, request, uniqueName, openPalette }) => {
    await page.goto('');
    await openPalette();
    await page.locator('#pal-q').fill('Parameter Store');
    await expect(page.locator('#pal-count')).toHaveText('1 match');
    await page.locator('#pal-q').fill('zzzzzz nothing is called this zzzzzz');
    await expect(page.locator('#pal-count')).toHaveText('0 matches');
    await expect(page.locator('.pal-empty')).toBeVisible();
    // Nothing typed: no count to give.
    await page.locator('#pal-q').fill('');
    await expect(page.locator('#pal-count')).toHaveText('');
  });

  test('underlines the letters of a loose match, and only those', async ({ page, openPalette }) => {
    await page.goto('');
    await openPalette();
    // Seven letters that are in "Parameter Store" in this order and not together.
    await page.locator('#pal-q').fill('pmtrstr');
    const row = page.locator('.pal-item', { hasText: 'Parameter Store' });
    await expect(row).toBeVisible();
    const hit = await row.locator('.pal-hit').allTextContents();
    expect(hit.join('').toLowerCase()).toBe('pmtrstr');
    expect(hit.length).toBeGreaterThan(1); // separate runs, not one block
  });

  // "create queue harbour-x" offers a create that carries the name, and follows
  // it to the create page with the name already in the field.
  test('a typed create offers the create with the name typed, and Enter opens it filled in', async ({ page, uniqueName, openPalette }) => {
    const name = uniqueName('zqtyped');
    await page.goto('');
    await openPalette();
    await page.locator('#pal-q').fill(`create queue ${name}`);
    await expect(page.locator('.pal-cap')).toHaveText(['Create']);
    const item = page.locator('.pal-item');
    await expect(item).toHaveCount(1);
    await expect(item).toContainText(`New queue called ${name}`);
    await expect(item.locator('.mono')).toHaveText(name);
    await expect(item).toHaveAttribute('href', new RegExp(`/sqs/create\\?name=${name}$`));
    await expect(page.locator('#pal-count')).toHaveText('1 match');
    await page.keyboard.press('Enter');
    await page.waitForURL(new RegExp(`/sqs/create\\?name=${name}$`));
    await expect(page.locator('input[name=name]')).toHaveValue(name);
  });

  test('a name that matches a queue offers a new queue of that name, one key away from the match', async ({
    page,
    request,
    uniqueName,
    openPalette,
  }) => {
    const queue = await createQueue(request, uniqueName('zqnear'));
    const typed = queue.slice(0, -1); // not a queue yet: a prefix of one
    await page.goto('');
    await openPalette();
    await page.locator('#pal-q').fill(typed);
    await expect(page.locator('.pal-cap')).toHaveText(['Go to', 'Create']);
    await expect(page.locator('#pal-count')).toHaveText('2 matches');
    const make = page.locator('.pal-item', { hasText: 'New queue called' });
    await expect(make).toHaveCount(1);
    await expect(make.locator('.mono')).toHaveText(typed);
    // The keys cross the caption: down once from the queue is the create.
    await expect(page.locator('.pal-item.sel')).toContainText(queue);
    await page.keyboard.press('ArrowDown');
    await expect(page.locator('.pal-item.sel')).toContainText('New queue called');
    await expect(page.locator('.pal-item.sel .k')).toHaveText('⏎');
    await page.keyboard.press('ArrowUp');
    await expect(page.locator('.pal-item.sel')).toContainText(queue);
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Enter');
    await page.waitForURL(new RegExp(`/sqs/create\\?name=${typed}$`));
    await expect(page.locator('input[name=name]')).toHaveValue(typed);
  });

  test('offers no create for a name that is already there, or for a thing that does not exist', async ({
    page,
    request,
    uniqueName,
    openPalette,
  }) => {
    const queue = await createQueue(request, uniqueName('zqhave'));
    await page.goto('');
    await openPalette();
    // The whole name of a queue that exists: going there is the one thing to do.
    await page.locator('#pal-q').fill(queue);
    await expect(page.locator('.pal-item', { hasText: 'New queue called' })).toHaveCount(0);
    await expect(page.locator('.pal-cap')).toHaveText(['Go to']);
    // A noun the console does not make.
    await page.locator('#pal-q').fill('create gizmo zqzq');
    await expect(page.locator('.pal-item')).toHaveCount(0);
    await expect(page.locator('.pal-empty')).toBeVisible();
    // The plain verb still finds the create, with no name to carry.
    await page.locator('#pal-q').fill('create queue');
    await expect(page.locator('.pal-item', { hasText: 'Create queue' })).toBeVisible();
    await expect(page.locator('.pal-cap', { hasText: 'Create' })).toBeVisible();
  });

  test('each typed create goes to its own service’s form', async ({ page, uniqueName, openPalette }) => {
    const name = uniqueName('zqmany');
    for (const [typed, path] of [
      [`create bucket ${name}`, `s3/create`],
      [`new topic ${name}`, `sns/create`],
      [`create state machine ${name}`, `sfn/create`],
      [`create log group ${name}`, `logs/create`],
    ]) {
      await page.goto('');
      await openPalette();
      await page.locator('#pal-q').fill(typed);
      await page.keyboard.press('Enter');
      await page.waitForURL(new RegExp(`/${path}\\?name=${name}$`));
      await expect(page.locator('input[name=name]')).toHaveValue(name);
    }
  });
});

test.describe('styled confirm + toasts', () => {
  test('cancel aborts, confirm proceeds, and success/error both toast', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    waitForToast,
  }) => {
    const bucket = uniqueName('e2e-shell');
    await createBucket(request, bucket);

    await page.goto(`s3/${bucket}`);
    const deleteBtn = page.locator('.acts').getByRole('button', { name: 'Delete' });

    // Cancel: dialog closes, bucket still exists (no navigation away).
    await deleteBtn.click();
    await expect(page.locator('#confirm-title')).toContainText(bucket);
    await confirmDialog('cancel');
    await expect(page).toHaveURL(new RegExp(`/s3/${bucket}$`));

    // Upload an object via the hidden file input (no need to click the
    // visible "Upload" button first — setInputFiles works on hidden inputs
    // and fires the real change event the Alpine handler listens for).
    await page
      .locator('input[type=file][name=file]')
      .setInputFiles({ name: 'hello.txt', mimeType: 'text/plain', buffer: Buffer.from('hello') });
    const uploadToast = await waitForToast();
    expect(uploadToast).toMatch(/Uploaded hello\.txt/);

    // Confirm on a NON-empty bucket: the server rejects it (BucketNotEmpty). A
    // refusal from a header button is drawn under the header, whole; it is not
    // only a toast.
    await deleteBtn.click();
    await confirmDialog('accept');
    await expect(page.locator('.det-h + .err[data-doze-err]')).toContainText('BucketNotEmpty');

    // Now delete the object first, then the (now-empty) bucket succeeds and
    // redirects to the list with a flash banner (not a toast). The row's
    // delete button is icon-only (accessible name from its title attr).
    await page
      .locator('tr', { hasText: 'hello.txt' })
      .getByRole('button', { name: 'Delete' })
      .click();
    await confirmDialog('accept');
    await expect(page.locator('#object-table')).not.toContainText('hello.txt');

    await deleteBtn.click();
    await confirmDialog('accept');
    await page.waitForURL(/\/s3(\?|$)/);
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('Bucket deleted');
  });
});

test.describe('keyboard list navigation', () => {
  test('/ focuses filter, j/k move cursor, Enter opens, c opens create', async ({
    page,
    request,
    uniqueName,
  }) => {
    const bucket = uniqueName('e2e-kbd');
    await createBucket(request, bucket);
    await page.goto('s3');

    await page.locator('body').click();
    await page.keyboard.press('/');
    await expect(page.locator('.listpane .filter input')).toBeFocused();
    await page.keyboard.type(bucket);
    await expect(page.locator(`.li[href]`, { hasText: bucket })).toBeVisible();

    // Clear the filter and blur before testing j/k/Enter (both are ignored
    // while focus is inside the filter input).
    await page.locator('.listpane .filter input').fill('');
    await page.locator('body').click();
    await page.keyboard.press('j');
    await page.keyboard.press('Enter');
    await page.waitForURL(/\/s3\/[^/]+$/);

    await page.goto('s3');
    await page.locator('body').click();
    await page.keyboard.press('c');
    await page.waitForURL(/\/s3\/create$/);
  });
});

const history = (page: import('@playwright/test').Page, dir: 'back' | 'forward') =>
  (dir === 'back' ? page.goBack() : page.goForward()).catch(() => {});

test.describe('htmx-boosted navigation', () => {
  test('rail links swap #workspace and back/forward work', async ({ page }) => {
    await page.goto('');
    await page.locator('.rail .ri', { hasText: 'SQS' }).click();
    await page.waitForURL(/\/sqs$/);
    await expect(page.locator('.rail .ri.on', { hasText: 'SQS' })).toBeVisible();

    await history(page, 'back');
    await page.waitForURL((url) => !/\/sqs$/.test(url.pathname));

    await history(page, 'forward');
    await page.waitForURL(/\/sqs$/);
  });
});

// Back and Forward are the first thing a person tries after following a link. A
// history restore that replaced <body>'s contents would detach every node shell.js
// held (the confirm dialog, the palette, the appearance buttons) and leave the page
// looking right and doing nothing, so the check is on what the chrome DOES afterwards,
// not on the URL.
test.describe('history navigation', () => {
  test('after Back and Forward the dialogs, palette and appearance control still work', async ({
    page, request, uniqueName, openPalette, confirmDialog,
  }) => {
    const bucket = await createBucket(request, uniqueName('hist'));
    await page.goto(`s3/${bucket}`);
    await expect(page.getByRole('button', { name: 'Delete' }).first()).toBeVisible();

    const traverse = async (go: () => Promise<unknown>, url: RegExp) => {
      await go();
      await page.waitForURL(url);
      await expect(page.locator('#workspace')).toBeVisible();
    };
    await page.locator('.rail .ri', { hasText: 'SQS' }).click();
    await page.waitForURL(/\/sqs$/);
    await traverse(() => history(page, 'back'), new RegExp(`/s3/${bucket}$`));
    await traverse(() => history(page, 'forward'), /\/sqs$/);
    await traverse(() => history(page, 'back'), new RegExp(`/s3/${bucket}$`));

    // The styled confirm dialog (it replaces the native one for every hx-confirm).
    await page.getByRole('button', { name: 'Delete' }).first().click();
    await expect(page.locator('#confirm')).toBeVisible();
    await confirmDialog('cancel');

    // The command palette.
    await openPalette();
    await page.keyboard.press('Escape');
    await expect(page.locator('#palette')).toBeHidden();

    // The appearance control, and the rail toggle.
    await page.locator('#appearance button[data-mode="dark"]').click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    await page.locator('#appearance button[data-mode="system"]').click();
    // This is a service page, so the rail starts as marks (slim): the first
    // press expands it, the second collapses it again.
    await page.locator('#rail-toggle').click();
    await expect(page.locator('html')).toHaveAttribute('data-rail', 'wide');
    await page.locator('#rail-toggle').click();
    await expect(page.locator('html')).toHaveAttribute('data-rail', 'slim');

    // And the page is still one page: a restore that nested a copy of the
    // workspace inside itself would leave two.
    await expect(page.locator('#workspace')).toHaveCount(1);
  });
});

// A resource's name is data. The sidebar filter used to put it inside a script string
// ('{{.Name}}'.toLowerCase()), so a name with a quote in it ended the string and
// whatever followed ran. API Gateway names are free text, so one can really be this.
test.describe('names are data', () => {
  test('a name with quotes and script in it is inert, and still filterable', async ({ page, uniqueName }) => {
    const hostile = `${uniqueName('q')}'+(window.__pwned=1)+'"<b>`;
    await postForm(page.request, 'apigw/create', { name: hostile });
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));

    await page.goto('apigw');
    const row = page.locator('.listpane .li', { hasText: 'window.__pwned=1' });
    await expect(row).toBeVisible();

    const filter = page.locator('.listpane .filter input');
    await filter.fill("'+(window");
    await expect(row).toBeVisible();          // matches by what the name says...
    await filter.fill('no-such-name-anywhere');
    await expect(row).toBeHidden();            // ...and is hidden when it does not.
    await filter.fill('');
    await expect(row).toBeVisible();

    expect(await page.evaluate(() => (window as any).__pwned)).toBeUndefined();
    expect(errors).toEqual([]);
  });
});

test.describe('fidelity info panel', () => {
  test('opens from the pane header and renders the ledger tiers', async ({ page }) => {
    await page.goto('sqs');
    await page.locator('.info-btn').click();
    const body = page.locator('.info-body');
    await expect(body.locator('table')).toBeVisible();
    // The ledger's own rows, tiers spelled as chips.
    await expect(body).toContainText('functional');
    await expect(body).toContainText('SendMessage');
    await expect(page.locator('.dialog-h', { hasText: 'Fidelity' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(body).toBeHidden();
    // The console key maps onto the ledger filename where they differ.
    await page.goto('ddb');
    await page.locator('.info-btn').click();
    await expect(page.locator('.info-body')).toContainText('TransactWriteItems');
  });
});

test.describe('bare console prefix', () => {
  test('typing the console address without a trailing slash lands on the wire', async ({ page }) => {
    // ORIGIN + '/_console', no slash: the address a person types or pastes.
    // page.goto('') would add the slash (baseURL ends in one), so build it.
    //
    // This does NOT reach the console's own "GET {prefix}" route: the binary's
    // mux answers first with a 302 to /_console/ (cmd/doze-aws/main.go, the
    // RedirectHandler beside mux.Handle("/_console/", con)). The user-visible
    // promise — the bare address works — is what this test holds.
    const res = await page.goto(BASE_URL.replace(/\/$/, ''));
    expect(new URL(page.url()).pathname).toBe('/_console/');
    expect(res?.status()).toBe(200);
    // The wire is the console home; its rail item is lit and its feed renders.
    await expect(page.locator('.rail .ri.on')).toBeVisible();
    await expect(page.locator('#traffic-feed').first()).toBeVisible();
  });
});

test.describe('connect page', () => {
  test('reached from the rail, Run get-caller-identity reports the account', async ({ page }) => {
    await page.goto('');
    await page.locator('.rail .ri', { hasText: 'Connect' }).click();
    await page.waitForURL(/\/connect$/);
    const result = page.locator('#cn-result');
    await expect(result).toContainText('Runs STS GetCallerIdentity');

    await page.getByRole('button', { name: 'Run get-caller-identity' }).click();
    await expect(result.locator('.cn-ok')).toContainText('Connected');
    await expect(result.locator('.cn-arn')).toContainText(/^arn:aws:(iam|sts)::\d{12}:/);
    await expect(result).toContainText(/account \d{12}/);
  });
});

test.describe('palette resolves a pasted ARN', () => {
  // Regression (fixed in 1.0): /api/resolve answers with the queue's page, and maybeResolve (static/shell.js ~438) prepends it to palItems, but renderPal -> palFiltered then drops it because the ARN is not a substring/subsequence of "name kind svc" — the palette shows "Nothing matches".
  test('an ARN pasted into ⌘K offers its resource and Enter opens it', async ({
    page,
    request,
    uniqueName,
    openPalette,
  }) => {
    const queue = await createQueue(request, uniqueName('e2e-resolve-q'));
    const arn = `arn:aws:sqs:us-east-1:000000000000:${queue}`;

    await page.goto('');
    await openPalette();
    const resolved = page.waitForResponse((r) => r.url().includes('/api/resolve'));
    await page.locator('#pal-q').fill(arn);
    const ref = await (await resolved).json();
    // The server half works: the ARN resolves to the queue's page.
    expect(ref.u).toMatch(new RegExp(`/sqs/${queue}$`));

    const item = page.locator('.pal-item', { hasText: queue });
    await expect(item).toBeVisible();
    await expect(item).toHaveAttribute('href', new RegExp(`/sqs/${queue}$`));
    await page.keyboard.press('Enter');
    await page.waitForURL(new RegExp(`/sqs/${queue}$`));
    await expect(page.locator('.det-title')).toContainText(queue);
  });
});

test.describe('the confirm component', () => {
  // It names the thing (the title), says what follows (the sentence after the
  // question) and its button is the verb. A "Confirm" on a delete makes the
  // reader work out what they are confirming.
  test('names the bucket, says what the call site said, and the button is Delete', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    const bucket = uniqueName('e2e-confirm');
    await createBucket(request, bucket);
    await page.goto(`s3/${bucket}`);

    await page.locator('.acts').getByRole('button', { name: 'Delete' }).click();
    await expect(page.locator('#confirm-title')).toContainText(`Delete bucket “${bucket}”?`);
    await expect(page.locator('#confirm-msg')).toHaveText('It must be empty.');
    await expect(page.locator('#confirm-yes')).toHaveText('Delete');
    // A delete is destructive, so the verb is the boards' red outline (the
    // solid red button was the old chrome) and the cancel is "Keep it" (D11).
    await expect(page.locator('#confirm-yes')).toHaveClass(/\bdanger\b/);
    await expect(page.locator('#confirm-yes')).not.toHaveClass(/btn-destructive/);
    await expect(page.locator('#confirm-no')).toHaveText('Keep it');
    await confirmDialog('cancel');
  });
});

test.describe('a narrow window', () => {
  // Below 1024px the console says it is not built to fit; at desk width it says
  // nothing. Phones are declared unsupported.
  test('is told so below 1024px and not above', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('sqs');
    await expect(page.locator('.narrow-note')).toBeHidden();
    await page.setViewportSize({ width: 900, height: 700 });
    await expect(page.locator('.narrow-note')).toBeVisible();
    await expect(page.locator('.narrow-note')).toContainText('1024');
  });
});

// ---------------------------------------------------------------------------
// The key bar (plan/03-shell-fidelity.md 2.4, S5). The bar names the keys the
// page answers to, and it may name only keys that are bound: the audit found
// seven that did nothing and an Escape that did not close the wire's inspector.
// Every key a bar names is driven here, so the bar cannot lie again: a page
// that declares a key with no driver in this table fails the test, and so does
// a key whose driver finds nothing happened.
// ---------------------------------------------------------------------------

async function gatewayCall(request: import('@playwright/test').APIRequestContext, target: string, body: object) {
  return request.post(GATEWAY_ROOT, {
    headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': target },
    data: JSON.stringify(body),
  });
}

/** The bar as drawn: one entry per item, its keycaps and its words. */
async function readBar(page: Page) {
  return page.locator('#keybar > span').evaluateAll((items) =>
    items.map((it) => ({
      caps: Array.from(it.querySelectorAll('.k')).map((k) => k.textContent || ''),
      label: Array.from(it.childNodes).filter((n) => n.nodeType === 3).map((n) => n.textContent).join('').trim(),
    }))
  );
}
const capsOf = async (page: Page) => (await readBar(page)).flatMap((i) => i.caps);

const PALETTE_KEY = process.platform === 'darwin' ? 'Meta+k' : 'Control+k';
const SUBMIT_KEY = process.platform === 'darwin' ? 'Meta+Enter' : 'Control+Enter';

/** What pressing each key must do, per page kind. Keyed `kind:cap`. */
type Drive = (page: Page, env: Record<string, string>) => Promise<void>;
const DRIVERS: Record<string, Drive> = {
  // ---- a list page (SQS, with two queues) ----
  'list:j': async (page) => {
    await page.keyboard.press('j');
    await expect(page.locator('.listpane .li.cursor')).toHaveCount(1);
  },
  'list:k': async (page) => {
    await page.keyboard.press('j');
    await page.keyboard.press('j');
    const second = await page.locator('.listpane .li[href]').nth(1).getAttribute('href');
    await expect(page.locator('.listpane .li.cursor')).toHaveAttribute('href', second!);
    await page.keyboard.press('k');
    const first = await page.locator('.listpane .li[href]').nth(0).getAttribute('href');
    await expect(page.locator('.listpane .li.cursor')).toHaveAttribute('href', first!);
  },
  'list:⏎': async (page) => {
    await page.keyboard.press('j');
    await page.keyboard.press('Enter');
    await page.waitForURL(/\/sqs\/[^/]+$/);
  },
  'list:/': async (page) => {
    await page.keyboard.press('/');
    await expect(page.locator('.listpane .filter input')).toBeFocused();
  },
  'list:c': async (page) => {
    await page.keyboard.press('c');
    await page.waitForURL(/\/sqs\/create$/);
  },
  'list:⌘K': async (page) => {
    await page.keyboard.press(PALETTE_KEY);
    await expect(page.locator('#palette')).toBeVisible();
    await expect(page.locator('#pal-q')).toBeFocused();
  },

  // ---- a create page ----
  'create:tab': async (page) => {
    const first = page.locator('input[name=name]');
    await first.focus();
    await page.keyboard.press('Tab');
    await expect(first).not.toBeFocused();
    expect(await page.evaluate(() => !!document.activeElement?.closest('form'))).toBe(true);
  },
  'create:esc': async (page, env) => {
    // From a field, as typed into, and from nowhere in particular.
    await page.locator('input[name=name]').fill('never-made');
    await page.keyboard.press('Escape');
    await page.waitForURL(new RegExp(`/${env.svc}$`));
    await page.goto(`${env.svc}/create`);
    await page.locator('body').click({ position: { x: 5, y: 5 } });
    await page.keyboard.press('Escape');
    await page.waitForURL(new RegExp(`/${env.svc}$`));
  },
  'create:⌘K': async (page) => {
    await page.keyboard.press(PALETTE_KEY);
    await expect(page.locator('#palette')).toBeVisible();
  },

  // ---- a create page with the Create board's bar (every service but SQS) ----
  'make:⌘⏎': async (page, env) => {
    // From the field, and from nowhere in particular: the page is for making one thing.
    const name = env.made;
    await page.locator('input[name=name]').fill(name);
    await page.keyboard.press(SUBMIT_KEY);
    await page.waitForURL(new RegExp(`/${env.svc}/${name}`));
  },
  'make:tab': async (page) => {
    const first = page.locator('input[name=name]');
    await first.focus();
    await page.keyboard.press('Tab');
    await expect(first).not.toBeFocused();
  },
  'make:esc': async (page, env) => {
    await page.locator('input[name=name]').fill('never-made');
    await page.keyboard.press('Escape');
    await page.waitForURL(new RegExp(`/${env.svc}$`));
  },
  'make:⌘K': async (page) => {
    await page.keyboard.press(PALETTE_KEY);
    await expect(page.locator('#palette')).toBeVisible();
  },

  // ---- the wire, with calls on it ----
  'wire:j': async (page) => {
    await page.keyboard.press('j');
    await expect(page.locator('.tr-row.cursor')).toHaveCount(1);
  },
  'wire:k': async (page) => {
    await page.keyboard.press('j');
    await page.keyboard.press('j');
    const at = await page.locator('.tr-row.cursor').getAttribute('data-seq');
    await page.keyboard.press('k');
    await expect(page.locator('.tr-row.cursor')).not.toHaveAttribute('data-seq', at!);
  },
  'wire:⏎': async (page) => {
    await page.keyboard.press('j');
    await page.keyboard.press('Enter');
    await expect(page.locator('.t-drawer')).toBeVisible();
    await expect(page.locator('#t-drawer-inner .td-h')).toBeVisible();
  },
  'wire:/': async (page) => {
    await page.keyboard.press('/');
    // The visible one: select.js draws a button over the native select.
    await expect(page.locator('button[aria-label="Filter by service"]')).toBeFocused();
  },
  'wire:⌘K': async (page) => {
    await page.keyboard.press(PALETTE_KEY);
    await expect(page.locator('#palette')).toBeVisible();
  },

  // ---- the wire, with a call open ----
  'inspect:j': async (page) => {
    const was = await page.locator('.tr-row.sel').getAttribute('data-seq');
    await page.keyboard.press('j');
    await expect(page.locator('.tr-row.sel')).not.toHaveAttribute('data-seq', was!);
    await expect(page.locator('#t-drawer-inner .td-h')).toBeVisible();
  },
  'inspect:k': async (page) => {
    await page.keyboard.press('j');
    await page.keyboard.press('j');
    const was = await page.locator('.tr-row.sel').getAttribute('data-seq');
    await page.keyboard.press('k');
    await expect(page.locator('.tr-row.sel')).not.toHaveAttribute('data-seq', was!);
  },
  'inspect:c': async (page) => {
    await page.keyboard.press('c');
    await expect(page.locator('#toasts .toast').last()).toContainText('Copied');
  },
  'inspect:→': async (page, env) => {
    await page.keyboard.press('ArrowRight');
    await page.waitForURL(new RegExp(`/sqs/${env.queue}$`));
  },
  'inspect:esc': async (page) => {
    await page.keyboard.press('Escape');
    await expect(page.locator('.t-drawer')).toBeHidden();
  },
};

// ---- the SQS queue page, one set per state (boards Queue, Sqs-08, Sqs-10,
// Sqs-17, Sqs-18, Sqs-19, and Sqs-01 for a service with no queue) ----
const setEditorValue = (page: Page, value: string) =>
  page.evaluate((v) => (window as any).dozeEditor.set('textarea[name=body][data-editor]', v), value);
const clickAway = (page: Page) => page.locator('.det-title').click();
Object.assign(DRIVERS, {
  // The messages tab with the composer shut.
  'detail:j': DRIVERS['list:j'],
  'detail:k': DRIVERS['list:k'],
  'detail:/': DRIVERS['list:/'],
  'detail:⌘K': DRIVERS['list:⌘K'],
  'detail:⌘⏎': async (page) => {
    // The composer is shut and the key says "send": it opens the composer to write in.
    await expect(page.locator('.sqs-compose')).toBeHidden();
    await clickAway(page);
    await page.keyboard.press(SUBMIT_KEY);
    await expect(page.locator('.sqs-compose')).toBeVisible();
  },
  // The composer open.
  'compose:⌘⏎': async (page) => {
    await setEditorValue(page, '{"sent":"from the key"}');
    await page.locator('.sqs-compose .CodeMirror').click();
    await page.keyboard.press(SUBMIT_KEY);
    await expect(page.locator('.msg')).toHaveCount(1, { timeout: 10_000 });
  },
  'compose:esc': async (page) => {
    // From the editor, and from nowhere in particular: the key bar says esc closes it.
    await page.locator('.sqs-compose .CodeMirror').click();
    await page.keyboard.press('Escape');
    await expect(page.locator('.sqs-compose')).toBeHidden();
    await page.locator('.dock-bar').click();
    await expect(page.locator('.sqs-compose')).toBeVisible();
    await clickAway(page);
    await page.keyboard.press('Escape');
    await expect(page.locator('.sqs-compose')).toBeHidden();
  },
  'compose:⌘K': DRIVERS['list:⌘K'],
  // The Consume tab, with two messages waiting.
  'consume:⏎': async (page) => {
    await page.locator('#sqs-consume input[name=max]').fill('1');
    await page.locator('#sqs-consume input[name=max]').press('Enter');
    await expect(page.locator('.rcv-row')).toHaveCount(1, { timeout: 10_000 });
    // And from nowhere in particular, where the bar still says ⏎ receives.
    await clickAway(page);
    await page.keyboard.press('Enter');
    await expect(page.locator('.rcv-row')).toHaveCount(2, { timeout: 10_000 });
  },
  'consume:⌘K': DRIVERS['list:⌘K'],
  // The Configuration tab, read.
  'config:j': DRIVERS['list:j'],
  'config:k': DRIVERS['list:k'],
  'config:⌘K': DRIVERS['list:⌘K'],
  // The Configuration tab, being edited.
  'cfgedit:tab': async (page) => {
    const first = page.locator('#sqs-config input[name=visibility]');
    await first.focus();
    await page.keyboard.press('Tab');
    await expect(first).not.toBeFocused();
    expect(await page.evaluate(() => !!document.activeElement?.closest('#sqs-config form.cfg-form'))).toBe(true);
  },
  'cfgedit:esc': async (page) => {
    // The button that opened the form is hidden now, so focus is nowhere in it.
    await expect(page.locator('#sqs-config form.cfg-form')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('#sqs-config form.cfg-form')).toBeHidden();
    await page.getByRole('button', { name: 'Edit settings' }).click();
    await page.locator('#sqs-config input[name=visibility]').focus();
    await page.keyboard.press('Escape');
    await expect(page.locator('#sqs-config form.cfg-form')).toBeHidden();
  },
  'cfgedit:⌘K': DRIVERS['list:⌘K'],
  // The Tags tab.
  'tags:tab': async (page) => {
    const start = page.locator('.q-tags button, .q-tags input').first();
    await start.focus();
    await page.keyboard.press('Tab');
    await expect(start).not.toBeFocused();
  },
  'tags:⌘K': DRIVERS['list:⌘K'],
  // A service with no queue yet (a fresh instance): only the keys that do something.
  'empty:c': async (page) => {
    await page.keyboard.press('c');
    await page.waitForURL(/\/sqs\/create$/);
  },
  'empty:⌘K': DRIVERS['list:⌘K'],
});

/** The kinds of page the bar changes between, and the keys each names.
 *  `{queue}` in a url is a queue made for the test; `fresh` is a second
 *  instance with nothing in it; `setup` leaves the page in the state the kind
 *  is about; `seed` messages are sent first. */
const KINDS: Record<string, { url: string; caps: string[]; svc?: string; fresh?: boolean; seed?: number; setup?: (page: Page) => Promise<void> }> = {
  list: { url: 'sqs', caps: ['j', 'k', '⏎', '/', 'c', '⌘K'] },
  // Sqs-01-None: no queue, so no row to move over, open or filter.
  empty: { url: 'sqs', caps: ['c', '⌘K'], fresh: true },
  // The Queue board, without "⏎ open a message": there is no message cursor.
  detail: { url: 'sqs/{queue}', caps: ['j', 'k', '⌘⏎', '/', '⌘K'] },
  // Sqs-08
  compose: { url: 'sqs/{queue}', caps: ['⌘⏎', 'esc', '⌘K'], setup: async (page) => { await page.locator('.dock-bar').click(); await expect(page.locator('.sqs-compose')).toBeVisible(); } },
  // Sqs-10
  consume: { url: 'sqs/{queue}?tab=consume', caps: ['⏎', '⌘K'], seed: 2 },
  // Sqs-17
  config: { url: 'sqs/{queue}?tab=config', caps: ['j', 'k', '⌘K'] },
  // Sqs-18
  cfgedit: { url: 'sqs/{queue}?tab=config', caps: ['tab', 'esc', '⌘K'], setup: async (page) => { await page.getByRole('button', { name: 'Edit settings' }).click(); await expect(page.locator('#sqs-config form.cfg-form')).toBeVisible(); } },
  // Sqs-19
  tags: { url: 'sqs/{queue}?tab=tags', caps: ['tab', '⌘K'] },
  // Sqs-03-Create draws tab, esc, ⌘K; the Create board draws ⌘⏎ create first.
  create: { url: 'sqs/create', caps: ['tab', 'esc', '⌘K'] },
  make: { url: 's3/create', caps: ['⌘⏎', 'tab', 'esc', '⌘K'], svc: 's3' },
  wire: { url: 'traffic', caps: ['j', 'k', '⏎', '/', '⌘K'] },
  inspect: { url: 'traffic', caps: ['j', 'k', 'c', '→', 'esc'] },
};

test.describe('the key bar', () => {
  test("is a single 28px bar at the foot of the page, and its items are the design's", async ({ page, request, uniqueName }) => {
    await createQueue(request, uniqueName('e2e-keys'));
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('sqs');
    const bar = page.locator('#keybar');
    await expect(bar).toBeVisible();
    const box = (await bar.boundingBox())!;
    expect(Math.round(box.height)).toBe(28);
    expect(Math.round(box.y + box.height)).toBe(900);
    expect(Math.round(box.width)).toBe(1440);
    expect(await readBar(page)).toEqual([
      { caps: ['j', 'k'], label: 'queue' },
      { caps: ['⏎'], label: 'open' },
      { caps: ['/'], label: 'filter' },
      { caps: ['c'], label: 'new queue' },
      { caps: ['⌘K'], label: 'jump or act' },
    ]);
    // Sqs-03-Create's bar...
    await page.goto('sqs/create');
    expect(await readBar(page)).toEqual([
      { caps: ['tab'], label: 'next field' },
      { caps: ['esc'], label: 'cancel' },
      { caps: ['⌘K'], label: 'jump or act' },
    ]);
    // ...and the Create board's, on every other service's create page.
    await page.goto('s3/create');
    expect(await readBar(page)).toEqual([
      { caps: ['⌘⏎'], label: 'create' },
      { caps: ['tab'], label: 'next field' },
      { caps: ['esc'], label: 'cancel, nothing made' },
      { caps: ['⌘K'], label: 'jump or act' },
    ]);
  });

  test("each state of a queue's page names the keys its board draws, in the board's words", async ({ page, request, uniqueName }) => {
    const q = await createQueue(request, uniqueName('e2e-keys-states'));
    const jump = { caps: ['⌘K'], label: 'jump or act' };
    const next = { caps: ['tab'], label: 'next field' };
    await page.goto(`sqs/${q}`);
    await expect.poll(() => readBar(page)).toEqual([{ caps: ['j', 'k'], label: 'queue' }, { caps: ['⌘⏎'], label: 'send' }, { caps: ['/'], label: 'filter' }, jump]);
    await page.locator('.dock-bar').click();
    await expect.poll(() => readBar(page)).toEqual([{ caps: ['⌘⏎'], label: 'send' }, { caps: ['esc'], label: 'close' }, jump]);
    await page.goto(`sqs/${q}?tab=consume`);
    await expect.poll(() => readBar(page)).toEqual([{ caps: ['⏎'], label: 'receive' }, jump]);
    await page.goto(`sqs/${q}?tab=config`);
    await expect.poll(() => readBar(page)).toEqual([{ caps: ['j', 'k'], label: 'queue' }, jump]);
    await page.getByRole('button', { name: 'Edit settings' }).click();
    await expect.poll(() => readBar(page)).toEqual([next, { caps: ['esc'], label: 'cancel' }, jump]);
    await page.goto(`sqs/${q}?tab=tags`);
    await expect.poll(() => readBar(page)).toEqual([next, jump]);
    // Nothing in the list: only what has something to act on.
    const fresh = await freshInstance();
    try {
      await page.goto(fresh.url + 'sqs');
      await expect.poll(() => readBar(page)).toEqual([{ caps: ['c'], label: 'new queue' }, jump]);
    } finally {
      await fresh.stop();
    }
  });

  test('is the same bar, once, after a boosted navigation, and follows the page', async ({ page, request, uniqueName }) => {
    await createQueue(request, uniqueName('e2e-keys'));
    await page.goto('sqs');
    expect(await capsOf(page)).toEqual(['j', 'k', '⏎', '/', 'c', '⌘K']);
    await page.locator('.listpane .new-link').click();
    await page.waitForURL(/\/sqs\/create$/);
    await expect(page.locator('#keybar')).toHaveCount(1);
    await expect.poll(() => capsOf(page)).toEqual(['tab', 'esc', '⌘K']);
    await page.keyboard.press('Escape');
    await page.waitForURL(/\/sqs$/);
    await expect.poll(() => capsOf(page)).toEqual(['j', 'k', '⏎', '/', 'c', '⌘K']);
  });

  test('never names ?, and no list pane is left with the old key line', async ({ page }) => {
    for (const url of ['', 'sqs', 's3', 'iam', 'connect', 'sqs/create']) {
      await page.goto(url);
      expect(await capsOf(page)).not.toContain('?');
      await expect(page.locator('.lp-f')).toHaveCount(0);
    }
    // And pressing it does nothing (it was the palette's fallback on a page with no filter).
    await page.goto('connect');
    await page.keyboard.press('?');
    await expect(page.locator('#palette')).toBeHidden();
  });

  for (const [kind, spec] of Object.entries(KINDS)) {
    test(`every key the ${kind} page names does what it says`, async ({ page, request, context, uniqueName }) => {
      await context.grantPermissions(['clipboard-read', 'clipboard-write']).catch(() => {});
      const env: Record<string, string> = { svc: spec.svc ?? 'sqs', made: uniqueName('e2e-keys-made') };
      // A page about having nothing needs an instance with nothing: the shared
      // one has queues from every other spec in it.
      const fresh = spec.fresh ? await freshInstance() : null;
      let q = '';
      if (!fresh) {
        q = await createQueue(request, uniqueName('e2e-keys'));
        await createQueue(request, uniqueName('e2e-keys'));
        for (let i = 0; i < (spec.seed ?? 0); i++) await postForm(request, `sqs/${q}/send`, { body: `seed ${i}` });
      }
      env.queue = q;
      if (kind === 'wire' || kind === 'inspect') {
        for (let i = 0; i < 3; i++) {
          await gatewayCall(request, 'AmazonSQS.GetQueueAttributes', { QueueUrl: `${GATEWAY_ROOT}000000000000/${q}`, AttributeNames: ['All'] });
        }
      }
      const open = async () => {
        await page.goto(fresh ? fresh.url + spec.url : spec.url.replace('{queue}', q));
        if (spec.setup) await spec.setup(page);
        if (kind === 'wire' || kind === 'inspect') {
          await expect(page.locator('.tr-row').nth(2)).toBeVisible();
        }
        if (kind === 'inspect') {
          await page.locator('.tr-row', { hasText: q }).first().locator('.rowopen').click();
          await expect(page.locator('#t-drawer-inner .td-h')).toBeVisible();
          await expect(page.locator('.tr-row.sel')).toHaveCount(1);
        }
        await expect(page.locator('#keybar .k').first()).toBeVisible();
      };
      try {
        await open();
        // The bar says exactly these keys; a key added to a page is added here with its driver.
        await expect.poll(() => capsOf(page)).toEqual(spec.caps);
        for (const cap of spec.caps) {
          const drive = DRIVERS[`${kind}:${cap}`];
          expect(drive, `the ${kind} page names ${cap} and this spec has no driver for it`).toBeTruthy();
          await open();
          await drive(page, env);
        }
      } finally {
        await fresh?.stop();
      }
    });
  }

  test('the create page of every service cancels on Escape, from a field', async ({ page }) => {
    for (const svc of ['s3', 'ddb', 'sns', 'kms', 'ssm', 'sm', 'kinesis', 'lambda', 'logs', 'sfn']) {
      await page.goto(`${svc}/create`);
      const field = page.locator('.detail form input:visible:not([type=hidden]):not([type=checkbox]):not([type=radio])').first();
      await field.focus();
      await page.keyboard.press('Escape');
      await page.waitForURL(new RegExp(`/${svc}$`), { timeout: 5000 });
    }
  });

  test('Escape closes the dialog on top first, and only then leaves the form', async ({ page }) => {
    await page.goto('s3/create');
    await page.locator('.info-btn').click();
    await expect(page.locator('.info-body')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('.info-body')).toBeHidden();
    expect(page.url()).toMatch(/\/s3\/create$/);
  });
});
