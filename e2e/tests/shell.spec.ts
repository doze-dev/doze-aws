import { test, expect } from '../fixtures/console';
import { createBucket, createQueue, postForm } from '../fixtures/api';
import { BASE_URL } from '../playwright.config';

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
    await expect(page.locator('#confirm-msg')).toContainText(bucket);
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

    // Confirm on a NON-empty bucket: the server rejects it (BucketNotEmpty),
    // htmx:responseError fires client-side, and an error toast appears.
    await deleteBtn.click();
    await confirmDialog('accept');
    const errToast = await waitForToast({ kind: 'err' });
    expect(errToast.length).toBeGreaterThan(0);

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
    await expect(page.locator('#flashbar')).toContainText('Bucket deleted');
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

// Asks the page to go back or forward. The traversal destroys the execution context
// the call ran in, which is the call working, not failing.
const history = (page: import('@playwright/test').Page, dir: 'back' | 'forward') =>
  page.evaluate((d) => window.history[d](), dir).catch(() => {});

test.describe('htmx-boosted navigation', () => {
  test('rail links swap #workspace and back/forward work', async ({ page }) => {
    await page.goto('');
    await page.locator('.rail .ri', { hasText: 'SQS' }).click();
    await page.waitForURL(/\/sqs$/);
    await expect(page.locator('.rail .ri.on', { hasText: 'SQS' })).toBeVisible();

    // (history.back() from the page: htmx reloads after a traversal, and Playwright's
    // goBack() reports the load it supersedes as an aborted navigation.)
    await page.evaluate(() => { (window as any).__beforeTraversal = true; });
    await history(page, 'back');
    await page.waitForURL((url) => !/\/sqs$/.test(url.pathname));
    await page.waitForFunction(() => !(window as any).__beforeTraversal);

    await page.evaluate(() => { (window as any).__beforeTraversal = true; });
    await history(page, 'forward');
    await page.waitForURL(/\/sqs$/);
    await page.waitForFunction(() => !(window as any).__beforeTraversal);
  });
});

// Back and Forward are the first thing a person tries after following a link. A
// history restore that replaced <body>'s contents would detach every node shell.js
// holds (the confirm dialog, the palette, the appearance buttons) and leave the page
// looking right and doing nothing, so the check is on what the chrome DOES afterwards,
// not on the URL.
test.describe('history navigation', () => {
  test('after Back and Forward the dialogs, palette and appearance control still work', async ({
    page, request, uniqueName, openPalette, confirmDialog,
  }) => {
    const bucket = await createBucket(request, uniqueName('hist'));
    await page.goto(`s3/${bucket}`);
    await expect(page.getByRole('button', { name: 'Delete' }).first()).toBeVisible();

    // A history traversal is followed by the page reloading itself (that is the
    // point: see htmx-config in layout.html). page.goBack() waits for the traversal's
    // own load, which the reload then supersedes and Playwright reports as an aborted
    // navigation, so the traversal is asked for from the page and the step waits for
    // the URL and then for the reload to settle.
    const traverse = async (go: () => Promise<unknown>, url: RegExp) => {
      await page.evaluate(() => { (window as any).__beforeTraversal = true; });
      await go();
      await page.waitForURL(url);
      // The URL changes first and the reload follows a moment later; carrying on in
      // between would act on a page that is about to be replaced. The marker is gone
      // once the reload has happened.
      await page.waitForFunction(() => !(window as any).__beforeTraversal);
      await page.waitForLoadState('load');
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
    await page.locator('#rail-toggle').click();
    await expect(page.locator('html')).toHaveAttribute('data-rail', 'slim');
    await page.locator('#rail-toggle').click();

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
