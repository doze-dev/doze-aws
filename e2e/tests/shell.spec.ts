import { test, expect } from '../fixtures/console';
import { createBucket } from '../fixtures/api';

// Console-chrome behaviors that live outside #workspace and so must survive
// every htmx swap: theme, rail, palette, confirm dialogs, toasts, keyboard
// nav, and boosted navigation. Uses S3 as an incidental action surface for
// the confirm/toast tests — this spec is about the chrome, not S3.

test.describe('theme', () => {
  test('toggles and persists across reload', async ({ page }) => {
    await page.goto('');
    const html = page.locator('html');
    const before = await html.getAttribute('data-theme');
    await page.locator('#theme-toggle').click();
    const after = await html.getAttribute('data-theme');
    expect(after).not.toBe(before);
    await page.reload();
    await expect(html).toHaveAttribute('data-theme', after!);
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

test.describe('htmx-boosted navigation', () => {
  test('rail links swap #workspace and back/forward work', async ({ page }) => {
    await page.goto('');
    await page.locator('.rail .ri', { hasText: 'SQS' }).click();
    await page.waitForURL(/\/sqs$/);
    await expect(page.locator('.rail .ri.on', { hasText: 'SQS' })).toBeVisible();

    await page.goBack();
    await page.waitForURL((url) => !/\/sqs$/.test(url.pathname));

    await page.goForward();
    await page.waitForURL(/\/sqs$/);
  });
});
