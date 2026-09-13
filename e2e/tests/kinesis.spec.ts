import { test, expect } from '../fixtures/console';

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
