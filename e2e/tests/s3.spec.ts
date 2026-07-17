import { test, expect } from '../fixtures/console';
import { createBucket, createQueue } from '../fixtures/api';
import type { Page, Locator } from '@playwright/test';

// S3 is the deepest surface in the console: buckets, objects, versions,
// presigned links, copy/move, notifications, and the CORS/lifecycle JSON
// editors. Each describe block below is self-contained (its own bucket via
// uniqueName) so it can run in parallel with the rest of the suite.

// Uploads via the real hidden <input type=file name=file>. setInputFiles
// fires the change event the Alpine handler listens for directly — no need
// to click the visible "Upload" button first (see shell.spec.ts).
async function uploadViaUI(page: Page, content: string, name: string, mimeType = 'text/plain') {
  await page
    .locator('input[type=file][name=file]')
    .setInputFiles({ name, mimeType, buffer: Buffer.from(content) });
}

// Opens the object drawer by clicking the row's name cell and waits for the
// HeadObject-backed partial to render.
async function openDrawer(page: Page, name: string) {
  await page.locator('.cell-name.link', { hasText: name }).click();
  await expect(page.locator('#drawer-inner .drawer-h')).toContainText(name);
}

// The versions list lives deep in the drawer's internal `.drawer-b`
// scroll container (overflow-y: auto inside a position:fixed aside).
// Playwright's own scrollIntoViewIfNeeded() doesn't reliably bring rows at
// the very bottom of that nested container into the actionability
// viewport check, so scroll explicitly before interacting with them.
async function clickInDrawer(locator: Locator) {
  await locator.evaluate((el) => el.scrollIntoView({ block: 'center' }));
  await locator.click();
}

test.describe('create bucket', () => {
  test('with versioning + object-lock creates and shows identity chips', async ({
    page,
    uniqueName,
  }) => {
    const bucket = uniqueName('e2e-s3-create');
    await page.goto('s3/create');
    await page.locator('input[name=name]').fill(bucket);
    // Scope by the checkbox's own `name` attribute rather than filtering
    // `.opt-row` by visible text — the object-lock row's description text
    // ("...implies versioning") makes a `hasText: 'Versioning'` filter match
    // both rows (hasText is a case-insensitive substring match). Click the
    // wrapping `label.switch` rather than .check()-ing the input directly —
    // the styled `.track`/`.thumb` decoration sits visually on top of the
    // (invisible) checkbox and intercepts pointer events aimed at it, but a
    // native <label> click toggles its associated input regardless.
    await page.locator('label.switch:has(input[name=versioning])').click();
    await page.locator('label.switch:has(input[name=object_lock])').click();
    await expect(page.locator('input[name=versioning]')).toBeChecked();
    await expect(page.locator('input[name=object_lock]')).toBeChecked();
    await page.getByRole('button', { name: 'Create bucket' }).click();
    // The redirect lands on the detail page with a `?flash=...` query string,
    // so don't anchor the regex with a bare `$`.
    await page.waitForURL(new RegExp(`/s3/${bucket}(\\?|$)`));

    await expect(page.locator('.li[href]', { hasText: bucket })).toBeVisible();
    await expect(page.locator('.chips')).toContainText(`arn:aws:s3:::${bucket}`);
    await expect(page.locator('.chips')).toContainText(`s3://${bucket}`);
  });
});

test.describe('upload object', () => {
  test('an uploaded file appears in the object table', async ({ page, request, uniqueName }) => {
    const bucket = uniqueName('e2e-s3-upload');
    await createBucket(request, bucket);
    await page.goto(`s3/${bucket}`);

    await uploadViaUI(page, 'hello world', 'hello.txt');
    await expect(page.locator('#object-table')).toContainText('hello.txt');
  });
});

test.describe('object drawer', () => {
  test('shows HeadObject-style metadata', async ({ page, request, uniqueName }) => {
    const bucket = uniqueName('e2e-s3-meta');
    await createBucket(request, bucket);
    await page.goto(`s3/${bucket}`);
    await uploadViaUI(page, 'hello world', 'hello.txt', 'text/plain');

    await openDrawer(page, 'hello.txt');
    const metaList = page.locator('#drawer-inner .meta-list');
    await expect(metaList.locator('.meta-row', { hasText: 'Type' }).locator('.v')).toContainText(
      'text/plain'
    );
    await expect(metaList.locator('.meta-row', { hasText: 'Size' }).locator('.v')).toContainText(
      '11 B'
    );
    await expect(
      metaList.locator('.meta-row', { hasText: 'ETag' }).locator('.v')
    ).not.toHaveText('');
    await expect(
      metaList.locator('.meta-row', { hasText: 'Modified' }).locator('.v')
    ).not.toHaveText('');
    await expect(
      metaList.locator('.meta-row', { hasText: 'Storage class' }).locator('.v')
    ).not.toHaveText('');
  });
});

test.describe('presign / share link', () => {
  test('generates a URL that actually serves the uploaded content', async ({
    page,
    request,
    uniqueName,
  }) => {
    const bucket = uniqueName('e2e-s3-presign');
    await createBucket(request, bucket);
    await page.goto(`s3/${bucket}`);
    await uploadViaUI(page, 'presigned content', 'share.txt');

    await openDrawer(page, 'share.txt');
    await page
      .locator('.drawer-sec', { hasText: 'Share link' })
      .getByRole('button', { name: 'Generate' })
      .click();

    const shareLine = page.locator('#share-out .mono');
    await expect(shareLine).toBeVisible();
    // The visible text can be CSS-truncated; the full URL lives in the title
    // attribute (see s3_share_link template).
    const link = await shareLine.getAttribute('title');
    expect(link).toBeTruthy();

    const res = await request.get(link!);
    expect(res.ok()).toBeTruthy();
    expect(await res.text()).toBe('presigned content');
  });
});

test.describe('copy / move object', () => {
  test('copy creates a new key; move relocates and removes the original', async ({
    page,
    request,
    uniqueName,
  }) => {
    const bucket = uniqueName('e2e-s3-copy');
    await createBucket(request, bucket);
    await page.goto(`s3/${bucket}`);
    await uploadViaUI(page, 'copy me', 'orig.txt');

    await openDrawer(page, 'orig.txt');
    let copySec = page.locator('.drawer-sec', { hasText: 'Copy / move' });
    await copySec.locator('input[name=dst]').fill('copy.txt');
    await copySec.getByRole('button', { name: 'Copy', exact: true }).click();

    await expect(page.locator('#object-table')).toContainText('orig.txt');
    await expect(page.locator('#object-table')).toContainText('copy.txt');

    // Copy succeeded and closed the drawer (@htmx:after-request); reopen it
    // on the new key to move it.
    await openDrawer(page, 'copy.txt');
    copySec = page.locator('.drawer-sec', { hasText: 'Copy / move' });
    await copySec.locator('input[name=dst]').fill('moved.txt');
    await copySec.getByRole('button', { name: 'Move', exact: true }).click();

    await expect(page.locator('#object-table')).toContainText('moved.txt');
    await expect(page.locator('#object-table')).not.toContainText('copy.txt');
  });
});

test.describe('versions', () => {
  // The Versions section renders near the bottom of the object drawer's
  // internal `.drawer-b` scroll panel; at the default 720px viewport height
  // its action buttons sit just past the panel's own flex-computed height
  // (a flexbox min-height quirk means .drawer-b never grows a real
  // scrollbar for that overflow), which no amount of scrollIntoView can
  // work around. A taller viewport keeps the whole drawer content on
  // screen without depending on that scroll behavior.
  test.use({ viewport: { width: 1280, height: 1100 } });

  test('two uploads of the same key produce two versions; restore reverts current content', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const bucket = uniqueName('e2e-s3-versions');
    await createBucket(request, bucket, { versioning: true });
    await page.goto(`s3/${bucket}`);

    await uploadViaUI(page, 'version one', 'v.txt');
    await waitForToast();
    await uploadViaUI(page, 'version two', 'v.txt');
    await waitForToast();

    await openDrawer(page, 'v.txt');
    const versionsSec = page.locator('.drawer-sec', { hasText: 'Versions' });
    await expect(versionsSec.locator('.ver-row')).toHaveCount(2);

    const older = versionsSec.locator('.ver-row:not(.cur)');
    const restoreBtn = older.getByRole('button', { name: 'Make this version current again' });
    await restoreBtn.evaluate((el) => el.scrollIntoView({ block: 'center' }));
    // Wait for the restore-version response itself rather than a generic
    // toast: a toast from the immediately-preceding upload can still be on
    // screen when this click fires, and waitForToast()'s `.last()` lookup
    // would then resolve instantly against that stale (already-visible)
    // toast instead of the new one, letting the GET below race the actual
    // server-side restore.
    await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes('/restore-version') && r.request().method() === 'POST'
      ),
      restoreBtn.click(),
    ]);

    const res = await request.get(`s3/${bucket}/object?key=v.txt`);
    expect(await res.text()).toBe('version one');
  });
});

test.describe('delete flows', () => {
  test('delete object, then delete the now-empty bucket', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    // A plain (non-versioned) bucket: deleting the object actually removes
    // it, so the bucket becomes truly empty and the bucket-level delete
    // succeeds. (See the "delete a specific version" test below for why a
    // versioned bucket can't reuse this same flow.)
    const bucket = uniqueName('e2e-s3-delete');
    await createBucket(request, bucket);
    await page.goto(`s3/${bucket}`);
    await uploadViaUI(page, 'gone soon', 'v.txt');

    await page.locator('tr', { hasText: 'v.txt' }).getByRole('button', { name: 'Delete' }).click();
    await confirmDialog('accept');
    await expect(page.locator('#object-table')).not.toContainText('v.txt');

    // Bucket is now empty, so the bucket-level delete succeeds and redirects
    // to the list with a flash banner (not a toast — see shell.spec.ts).
    await page.locator('.acts').getByRole('button', { name: 'Delete' }).click();
    await confirmDialog('accept');
    await page.waitForURL(/\/s3(\?|$)/);
    await expect(page.locator('#flashbar')).toContainText('Bucket deleted');
  });

  test('delete a specific version, leaving the current version intact', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    waitForToast,
  }) => {
    // On a versioned bucket, a normal object delete (above) only writes a
    // delete marker rather than truly emptying the bucket (real S3
    // semantics — internal/s3store.DeleteBucket refuses while any version,
    // including delete markers, remains). So this test exercises permanent
    // per-version deletion directly instead of chaining into a bucket
    // delete.
    // Tall viewport: the drawer's Versions section overflows the default
    // 720px height (see the "versions" describe block above for why).
    await page.setViewportSize({ width: 1280, height: 1100 });

    const bucket = uniqueName('e2e-s3-delver');
    await createBucket(request, bucket, { versioning: true });
    await page.goto(`s3/${bucket}`);

    await uploadViaUI(page, 'version one', 'v.txt');
    await waitForToast();
    await uploadViaUI(page, 'version two', 'v.txt');
    await waitForToast();

    await openDrawer(page, 'v.txt');
    const versionsSec = page.locator('.drawer-sec', { hasText: 'Versions' });
    await expect(versionsSec.locator('.ver-row')).toHaveCount(2);

    const older = versionsSec.locator('.ver-row:not(.cur)');
    await clickInDrawer(older.getByRole('button', { name: 'Permanently delete this version' }));
    await confirmDialog('accept');
    await expect(versionsSec.locator('.ver-row')).toHaveCount(1);

    // The current version (v2) is untouched.
    const res = await request.get(`s3/${bucket}/object?key=v.txt`);
    expect(await res.text()).toBe('version two');
  });
});

test.describe('CORS and lifecycle editors', () => {
  test('valid JSON persists and is shown after a reload', async ({
    page,
    request,
    uniqueName,
    setEditor,
    waitForToast,
  }) => {
    const bucket = uniqueName('e2e-s3-cfg');
    await createBucket(request, bucket);
    await page.goto(`s3/${bucket}?tab=properties`);

    const corsSel = 'form[hx-post$="/cors"] textarea[data-editor="json"]';
    const lifecycleSel = 'form[hx-post$="/lifecycle"] textarea[data-editor="json"]';

    await setEditor(
      corsSel,
      '[{"AllowedOrigins":["http://localhost:3000"],"AllowedMethods":["GET","PUT"]}]'
    );
    await page.locator('form[hx-post$="/cors"]').getByRole('button', { name: 'Save CORS' }).click();
    await waitForToast();

    await setEditor(lifecycleSel, '[{"Prefix":"tmp/","ExpireDays":7}]');
    await page
      .locator('form[hx-post$="/lifecycle"]')
      .getByRole('button', { name: 'Save lifecycle' })
      .click();
    await waitForToast();

    await page.reload();
    await expect(page.locator(corsSel)).toHaveValue(/http:\/\/localhost:3000/);
    await expect(page.locator(lifecycleSel)).toHaveValue(/ExpireDays/);
  });
});

test.describe('bucket notification', () => {
  test('wiring a bucket -> SQS notification shows it in the notification list', async ({
    page,
    request,
    uniqueName,
  }) => {
    const bucket = uniqueName('e2e-s3-notify');
    const queue = await createQueue(request, uniqueName('e2e-s3-notify-q'));
    await createBucket(request, bucket);
    await page.goto(`s3/${bucket}?tab=properties`);

    await page.locator('.sub-add select[name=dest]').selectOption(`sqs:${queue}`);
    await page.locator('.sub-add').getByRole('button', { name: 'Wire' }).click();

    const notifySec = page.locator('#s3-notify');
    await expect(notifySec).toContainText(queue);
    await expect(notifySec.locator('.sub-row', { hasText: queue }).locator('.badge.type')).toContainText(
      'sqs'
    );
  });
});

test.describe('invalid bucket name', () => {
  test('server rejects a name that violates the bucket-name pattern', async ({
    request,
    uniqueName,
  }) => {
    // The create form's <input pattern="[a-z0-9.\-]{3,63}"> blocks native
    // submission of an invalid name in a real browser, so the only way to
    // exercise the SERVER's own validation (internal/s3store.validBucketName,
    // wired through CreateBucketFull -> c.fail) is to bypass the browser and
    // POST directly, the same way fixtures/api.ts's postForm does — but here
    // we assert the failure instead of throwing on it.
    const bad = `BAD-${uniqueName('x')}`; // uppercase letters violate the pattern
    const res = await request.post('s3/create', { form: { name: bad } });
    expect(res.status()).toBe(400);
    expect(await res.text()).toMatch(/not valid|InvalidBucketName/i);
  });
});
