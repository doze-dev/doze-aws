import type { APIRequestContext, Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { createKey, postForm } from '../fixtures/api';

// Kinesis was the only implemented service with a full console surface and no
// browser coverage at all — create, shards, put, read, reshard, delete, none of
// it exercised. demo/seed.ts drives the SDK side; this covers the pages.
//
// The three things worth asserting here are the ones that are specific to a
// log rather than to a store:
//
//   a record read back is the record that was put, through the shard it
//   actually landed in;
//   the footer states what the read LOOKED AT rather than implying it saw
//   everything, which is the page's own honesty mechanism (kinesis.html's
//   comment says so) and is exactly the kind of thing that rots silently;
//   a batch under one partition key goes to one shard, which is why the put
//   form suffixes the key — a batch that did not spread would demonstrate
//   nothing about distribution.

test.describe('Kinesis streams', () => {
  test('create a stream, put a record, and read it back from its shard', async ({
    page,
    uniqueName,
    waitForToast,
    setEditor,
  }) => {
    const stream = uniqueName('e2e-kinesis');

    await page.goto('kinesis/create');
    await page.locator('input[name="name"]').fill(stream);
    await page.locator('input[name="shards"]').fill('2');
    await page.getByRole('button', { name: 'Create stream' }).click();
    await page.waitForURL(new RegExp(`/kinesis/${stream}$`));
    expect(await waitForToast()).toContain('Stream created');

    // Both shards are listed, and the list pane shows the shard count.
    const shardRows = page.locator('.tbl tbody tr', { hasText: /shardId-/ });
    await expect(shardRows).toHaveCount(2);

    // Put one record with a distinctive body.
    const body = `{"e2e":"${stream}","n":1}`;
    await page.locator('input[name="partitionKey"]').fill('device-7');
    await setEditor('textarea[name="data"]', body);
    await page.getByRole('button', { name: 'Put', exact: true }).click();
    await waitForToast();

    // Read it back on the Records tab. The data column is truncated for long
    // values, so match on the stream name inside the body rather than the
    // whole string.
    await page.goto(`kinesis/${stream}/records`);
    const row = page.locator('#k-records .tbl tbody tr', { hasText: 'device-7' });
    await expect(row).toHaveCount(1);
    await expect(row).toContainText(stream);

    // The footer is the page's honesty mechanism: it says what was read, not
    // what exists. With no shard selected it reports the merge across both.
    await expect(page.locator('#k-records .tbl-foot')).toContainText('2 shards merged');
  });

  test('a batch suffixes the partition key so it is not all one shard', async ({
    page,
    uniqueName,
    waitForToast,
    setEditor,
  }) => {
    const stream = uniqueName('e2e-kinesis-batch');
    await page.goto('kinesis/create');
    await page.locator('input[name="name"]').fill(stream);
    await page.locator('input[name="shards"]').fill('2');
    await page.getByRole('button', { name: 'Create stream' }).click();
    await page.waitForURL(new RegExp(`/kinesis/${stream}$`));
    await waitForToast();

    // count > 1 is PutRecords, not a loop — and the handler suffixes the
    // partition key per record precisely so the batch spreads across shards.
    await page.locator('input[name="partitionKey"]').fill('batch');
    await setEditor('textarea[name="data"]', '{"batched":true}');
    await page.locator('input[name="count"]').fill('12');
    await page.getByRole('button', { name: 'Put', exact: true }).click();
    await waitForToast();

    await page.goto(`kinesis/${stream}/records`);
    const rows = page.locator('#k-records .tbl tbody tr', { hasText: 'batch-' });
    await expect(rows).toHaveCount(12);

    // The suffixing is the deterministic part, so that is what gets asserted:
    // twelve records, twelve DISTINCT keys, batch-1 … batch-12. Which shard
    // each one hashes to is MD5's business, not this test's — asserting a
    // particular split would be asserting the hash function.
    const keys = await page
      .locator('#k-records .tbl tbody tr td:nth-child(3)')
      .allTextContents();
    const trimmed = keys.map((s) => s.trim()).filter(Boolean);
    expect(new Set(trimmed).size).toBe(12);
    expect([...trimmed].sort()).toEqual(
      Array.from({ length: 12 }, (_, i) => `batch-${i + 1}`).sort()
    );
  });

  test('delete asks first, then removes the stream', async ({
    page,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const stream = uniqueName('e2e-kinesis-del');
    await page.goto('kinesis/create');
    await page.locator('input[name="name"]').fill(stream);
    await page.getByRole('button', { name: 'Create stream' }).click();
    await page.waitForURL(new RegExp(`/kinesis/${stream}$`));
    await waitForToast();

    // The button carries hx-confirm, but the console replaces htmx's native
    // window.confirm with its own #confirm overlay — so this goes through the
    // confirmDialog fixture, not page.on('dialog').
    await page.getByRole('button', { name: 'Delete' }).click();
    await expect(page.locator('#confirm-msg')).toContainText(stream);
    await confirmDialog('accept');

    await page.waitForURL(/\/kinesis$/);
    await expect(page.locator('.listpane .li .nm', { hasText: stream })).toHaveCount(0);
  });
});

// ---- Route coverage: tabs, the record explorer, and the Details tab ----
//
// Streams are ARRANGED through the console's own create/put routes (postForm),
// which the tests above already drive through the form. Every action under
// test below is a click on the page that offers it.

async function createStream(request: APIRequestContext, name: string, shards = 1) {
  await postForm(request, 'kinesis/create', { name, shards });
  return name;
}

async function putRecord(
  request: APIRequestContext,
  stream: string,
  partitionKey: string,
  data: string
) {
  await postForm(request, `kinesis/${stream}/put`, { partitionKey, data });
}

/** The shard table's rows on the Shards tab (header excluded). */
const shardRows = (page: Page) => page.locator('.tbl tbody tr', { hasText: /shardId-/ });

test.describe('Kinesis stream tabs', () => {
  test('the Details and Tags tabs are routes of their own, and tags save', async ({
    page,
    request,
    uniqueName,
  }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-tabs'));
    await page.goto(`kinesis/${stream}`);

    await page.getByRole('link', { name: 'Details', exact: true }).click();
    await page.waitForURL(new RegExp(`/kinesis/${stream}/details$`));
    await expect(page.getByText('Capacity mode')).toBeVisible();

    await page.getByRole('link', { name: 'Tags', exact: true }).click();
    await page.waitForURL(new RegExp(`/kinesis/${stream}/tags$`));

    // The panel loads lazily (hx-trigger=load) — wait for the editor itself.
    const editor = page.locator('#tag-editor');
    await expect(editor.getByText('No tags yet.')).toBeVisible();
    await editor.getByRole('button', { name: 'Add tag' }).click();
    await editor.locator('input[name="tag_key"]').last().fill('team');
    await editor.locator('input[name="tag_val"]').last().fill(stream);
    await editor.getByRole('button', { name: 'Save changes' }).click();
    await expect(editor.getByText('Unsaved changes')).toBeHidden();

    // Persisted service-side: a reload shows it.
    await page.reload();
    await expect(page.locator('#tag-editor input[name="tag_val"]')).toHaveValue(stream);
  });
});

test.describe('Kinesis record explorer', () => {
  test('a partition-key filter reads only the shard that key routes to', async ({
    page,
    request,
    uniqueName,
  }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-q'), 2);
    await putRecord(request, stream, 'alpha', '{"who":"alpha"}');
    await putRecord(request, stream, 'beta', '{"who":"beta"}');

    await page.goto(`kinesis/${stream}/records`);
    const rows = page.locator('#k-records .tbl tbody tr');
    await expect(rows.filter({ hasText: 'alpha' })).toHaveCount(1);
    await expect(rows.filter({ hasText: 'beta' })).toHaveCount(1);

    await page.getByRole('button', { name: 'Filters' }).click();
    await page.locator('input[name="pk"]').fill('alpha');
    await page.getByRole('button', { name: 'Read', exact: true }).click();

    await expect(page.locator('#k-records .tbl-foot')).toContainText('by partition key');
    await expect(page.locator('#k-records .tbl tbody tr', { hasText: 'alpha' })).toHaveCount(1);
    await expect(page.locator('#k-records .tbl tbody tr', { hasText: 'beta' })).toHaveCount(0);
  });

  // Regression (fixed in 1.0): the "Full" button sits after 400 chars of nowrap text inside a
  // max-width td.trunc, so it is clipped out of view and the cell intercepts
  // the click (with an unbroken payload the whole Data cell renders blank).
  test('a truncated record expands whole in the drawer', async ({
    page,
    request,
    uniqueName,
  }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-full'));
    // Over the listing's 400-byte preview, with a tail the listing cannot show.
    const tail = `END-${stream}`;
    const body = JSON.stringify({
      readings: Array.from({ length: 40 }, (_, i) => ({ sensor: `s-${i}`, temp: 20 + i })),
      tail,
    });
    await putRecord(request, stream, 'big', body);

    await page.goto(`kinesis/${stream}/records`);
    const row = page.locator('#k-records .tbl tbody tr', { hasText: 'big' });
    await expect(row).not.toContainText(tail);
    await row.getByRole('button', { name: 'Full' }).click();

    const drawer = page.locator('aside.drawer');
    await expect(drawer).toBeVisible();
    await expect(drawer.locator('.drawer-h')).toContainText('Record');
    await expect(drawer.locator('.code-out pre')).toContainText(tail);
    await expect(drawer).toContainText('big');
  });
});

test.describe('Kinesis resharding', () => {
  // Each stream carries a record before it is resharded. A closed shard's
  // EndingSequenceNumber is the stream's last sequence, and a stream that has
  // never been written has none — see the fixme at the end of this block.

  test('Split closes the parent and opens two children', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-split'));
    await putRecord(request, stream, 'k', '{"before":"split"}');
    await page.goto(`kinesis/${stream}`);
    await expect(shardRows(page)).toHaveCount(1);

    await shardRows(page).first().getByRole('button', { name: 'Split' }).click();
    await expect(page.locator('#confirm-msg')).toContainText('Split shardId-000000000000 in two');
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Split shardId-000000000000');

    await expect(shardRows(page)).toHaveCount(3);
    const parent = shardRows(page).filter({ hasText: 'closed' });
    await expect(parent).toHaveCount(1);
    await expect(parent).toContainText('shardId-000000000000');
    await expect(parent.getByRole('button', { name: 'Split' })).toHaveCount(0);
    await expect(shardRows(page).filter({ hasText: 'split of shardId-000000000000' })).toHaveCount(2);
  });

  test('Merge folds an adjacent pair into one child', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-merge'), 2);
    await putRecord(request, stream, 'k', '{"before":"merge"}');
    await page.goto(`kinesis/${stream}/details`);
    await expect(page.locator('select[name="pair"] option')).toHaveCount(1);

    await page.getByRole('button', { name: 'Merge' }).click();
    await expect(page.locator('#confirm-msg')).toContainText('Merge these two shards');
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Merged shardId-000000000000 + shardId-000000000001');

    await page.waitForURL(new RegExp(`/kinesis/${stream}(\\?|$)`));
    await expect(shardRows(page)).toHaveCount(3);
    await expect(shardRows(page).filter({ hasText: 'closed' })).toHaveCount(2);
    await expect(
      shardRows(page).filter({ hasText: 'merge of shardId-000000000000 + shardId-000000000001' })
    ).toHaveCount(1);

    // With one open shard left there is nothing adjacent to merge.
    await page.goto(`kinesis/${stream}/details`);
    await expect(page.getByText('Nothing to merge')).toBeVisible();
  });

  test('Shard count re-tiles the key space', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-scale'));
    await putRecord(request, stream, 'k', '{"before":"scale"}');
    await page.goto(`kinesis/${stream}/details`);
    await page.locator('input[name="shards"]').fill('2');
    await page.locator('form:has(input[name="shards"])').getByRole('button', { name: 'Apply' }).click();
    await expect(page.locator('#confirm-msg')).toContainText('Rescale');
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Scaled to 2 shards');

    await page.waitForURL(new RegExp(`/kinesis/${stream}(\\?|$)`));
    // The one original shard closes; two open children tile the space.
    await expect(shardRows(page).filter({ hasText: 'closed' })).toHaveCount(1);
    await expect(shardRows(page).filter({ hasNotText: 'closed' })).toHaveCount(2);
  });

  // Regression (fixed in 1.0): kinesis/reshard.go closeShard leaves EndSeq=0 on a never-written
  // stream and shardWire (kinesis/records.go:276) then omits
  // EndingSequenceNumber, so the closed parent reads as open (still 100% of
  // the key space, still offering Split) in ListShards and in the console.
  test('splitting a stream that was never written still closes the parent', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-split0'));
    await page.goto(`kinesis/${stream}`);
    await shardRows(page).first().getByRole('button', { name: 'Split' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Split shardId-000000000000');
    await expect(shardRows(page)).toHaveCount(3);
    await expect(shardRows(page).filter({ hasText: 'closed' })).toHaveCount(1);
  });
});

test.describe('Kinesis stream configuration', () => {
  test('Retention applies from the Shards tab and shows in the header', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-ret'));
    await page.goto(`kinesis/${stream}`);
    await expect(page.locator('.chips .chip', { hasText: '24h retention' })).toBeVisible();

    await page.locator('input[name="hours"]').fill('48');
    await page.locator('form:has(input[name="hours"])').getByRole('button', { name: 'Apply' }).click();
    expect(await waitForToast()).toContain('Retention set');
    await expect(page.locator('.chips .chip', { hasText: '48h retention' })).toBeVisible();
  });

  test('Capacity mode switches to on-demand and hands back the shard layout', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-mode'));
    await page.goto(`kinesis/${stream}/details`);
    await page.locator('select[name="mode"]').selectOption('ON_DEMAND');
    await page.getByRole('button', { name: 'Set mode' }).click();
    expect(await waitForToast()).toContain('Stream mode set to ON_DEMAND');

    await expect(page.locator('.chips .chip', { hasText: 'ON_DEMAND' })).toBeVisible();
    await expect(page.getByText('Shard layout is managed for you')).toBeVisible();
    await expect(page.locator('select[name="mode"]')).toHaveValue('ON_DEMAND');
  });

  test('Encryption records a KMS key', async ({ page, request, uniqueName, waitForToast }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-enc'));
    const keyId = await createKey(request, { description: stream });

    await page.goto(`kinesis/${stream}/details`);
    const form = page.locator('form:has(select[name="key"])');
    await expect(form.locator('.chip')).toHaveText('NONE');

    await form.locator('select[name="key"]').selectOption(keyId);
    await form.getByRole('button', { name: 'Apply' }).click();
    expect(await waitForToast()).toContain(`Encryption set to ${keyId}`);
    await expect(form.locator('.chip')).toHaveText('KMS');
    await expect(form.locator('select[name="key"]')).toHaveValue(keyId);
  });

  // Regression (fixed in 1.0): choosing "none" sends StopStreamEncryption without KeyId
  // (internal/console/client_kinesis.go:769 StopEncryption), which the service
  // model rejects — 400 "Value null at 'keyId'", so encryption can't be cleared.
  test('Encryption can be cleared back to none', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-enc0'));
    const keyId = await createKey(request, { description: stream });
    await page.goto(`kinesis/${stream}/details`);
    const form = page.locator('form:has(select[name="key"])');
    await form.locator('select[name="key"]').selectOption(keyId);
    await form.getByRole('button', { name: 'Apply' }).click();
    await waitForToast();
    await expect(form.locator('.chip')).toHaveText('KMS');

    await form.locator('select[name="key"]').selectOption('');
    await form.getByRole('button', { name: 'Apply' }).click();
    expect(await waitForToast()).toContain('Encryption cleared');
    await expect(form.locator('.chip')).toHaveText('NONE');
  });

  test('Shard-level metrics round-trip', async ({ page, request, uniqueName, waitForToast }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-met'));
    await page.goto(`kinesis/${stream}/details`);
    const form = page.locator('form:has(input[name="metric"])');
    const incoming = form.locator('input[name="metric"][value="IncomingBytes"]');
    await expect(incoming).not.toBeChecked();

    await incoming.check();
    await form.getByRole('button', { name: 'Apply' }).click();
    expect(await waitForToast()).toContain('Shard-level metrics updated');

    await page.reload();
    await expect(
      page.locator('form:has(input[name="metric"]) input[value="IncomingBytes"]')
    ).toBeChecked();
    await expect(
      page.locator('form:has(input[name="metric"]) input[value="OutgoingBytes"]')
    ).not.toBeChecked();
  });

  test('a consumer registers, lists, and deregisters', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-cons'));
    const consumer = uniqueName('worker');
    await page.goto(`kinesis/${stream}/details`);
    await expect(page.getByText('No consumers registered.')).toBeVisible();

    await page.locator('form:has(input[name="name"][placeholder="analytics-worker"]) input').fill(consumer);
    await page.getByRole('button', { name: 'Register' }).click();
    expect(await waitForToast()).toContain('Consumer registered');

    const row = page.locator('.tbl tbody tr', { hasText: consumer });
    await expect(row).toHaveCount(1);
    await expect(row).toContainText(`${stream}/consumer/${consumer}`);

    await row.getByRole('button', { name: 'Deregister' }).click();
    await expect(page.locator('#confirm-msg')).toContainText(consumer);
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Consumer deregistered');
    await expect(page.locator('.tbl tbody tr', { hasText: consumer })).toHaveCount(0);
  });

  test('a resource policy saves, reads back, and an empty one removes it', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    setEditor,
  }) => {
    const stream = await createStream(request, uniqueName('e2e-kin-pol'));
    const sid = uniqueName('Sid').replace(/-/g, '');
    const policy = JSON.stringify({
      Version: '2012-10-17',
      Statement: [
        {
          Sid: sid,
          Effect: 'Allow',
          Principal: { AWS: 'arn:aws:iam::000000000000:root' },
          Action: 'kinesis:GetRecords',
          Resource: `arn:aws:kinesis:us-east-1:000000000000:stream/${stream}`,
        },
      ],
    });

    await page.goto(`kinesis/${stream}/details`);
    const form = page.locator('form:has(textarea[name="policy"])');
    await setEditor('form:has(textarea[name="policy"]) textarea[name="policy"]', policy);
    await form.getByRole('button', { name: 'Save' }).click();
    expect(await waitForToast()).toContain('Resource policy saved');

    await page.reload();
    await expect(page.locator('textarea[name="policy"]')).toHaveValue(new RegExp(sid));

    await setEditor('form:has(textarea[name="policy"]) textarea[name="policy"]', '');
    await page.locator('form:has(textarea[name="policy"])').getByRole('button', { name: 'Save' }).click();
    expect(await waitForToast()).toContain('Resource policy removed');
    await page.reload();
    await expect(page.locator('textarea[name="policy"]')).not.toHaveValue(new RegExp(sid));
  });
});
