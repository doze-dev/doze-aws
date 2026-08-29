import { test, expect } from '../fixtures/console';
import type { Page } from '@playwright/test';
import { createQueue, createTable, createKey } from '../fixtures/api';

// waitForToast's `.last()` locator is only race-free when at most one toast
// is alive at a time — the "Tag saved" toast from the add can still be
// visible (toasts self-remove after 3.2s/6s) when we immediately trigger the
// remove's toast, so `.last()` can report the stale one as "visible" before
// the new one lands. Drain the current toast before firing the next
// toast-producing action (same pattern as kms.spec.ts).
async function waitToastGone(page: Page) {
  await expect(page.locator('.toast:not(.err)').last()).toBeHidden({ timeout: 8000 });
}

// The shared tag editor (console/client_tags.go + templates/panes.html's
// tags_panel/tag_editor) is ONE generic UI fanning out to per-service wire
// formats: SQS TagQueue, DDB TagResource (Key/Value), KMS TagResource
// (TagKey/TagValue), SM TagResource+DescribeSecret, Lambda REST tags,
// SNS/EB Query-XML. This spec drives the same add/verify/remove cycle
// through three of those shapes (SQS, DDB, KMS) to prove the dispatcher
// routes correctly per service, not just that "some tag UI works somewhere".
// S3 has its own separate bucket-tags UI (not this component) and is out of
// scope here.
//
// The panel is lazy-loaded (`hx-trigger="load"` on a container with no id;
// its response swaps in `<div id="tag-editor">`), so every test waits on
// `#tag-editor` becoming visible rather than just the outer container being
// attached. Each service also gates the panel behind a different tab/query
// param: SQS needs `?tab=config`, DDB needs `?tab=details`, KMS needs none.

// The editor is a draft: rows live in Alpine, nothing reaches the service until
// Save, and Cancel restores. So the assertions changed shape with it — the old
// version expected a per-row "Tag saved" toast and a <tr> per tag.
async function addAndRemoveTag(
  page: import('@playwright/test').Page,
  waitForToast: (opts?: { kind?: 'ok' | 'err' }) => Promise<string>,
  tagKey: string,
  tagValue: string
) {
  const tagEditor = page.locator('#tag-editor');
  await expect(tagEditor).toBeVisible();

  // Add a row and fill it. Nothing has reached the service yet.
  await tagEditor.getByRole('button', { name: 'Add tag' }).click();
  const newRow = tagEditor.locator('.tag-row').last();
  await newRow.locator('input[name="tag_key"]').fill(tagKey);
  await newRow.locator('input[name="tag_val"]').fill(tagValue);

  // Save is gated on dirty, so it is only clickable once something changed.
  const save = tagEditor.getByRole('button', { name: 'Save changes' });
  await expect(save).toBeEnabled();
  await save.click();
  const addToast = await waitForToast();
  expect(addToast).toMatch(/Tags saved/);
  await waitToastGone(page);

  // Round-tripped: the row comes back from the service, not from local state.
  //
  // Read the VALUE, not a [value="..."] attribute selector. Alpine's x-model
  // assigns the input's value PROPERTY and never writes the attribute, so an
  // attribute selector matches nothing however correct the page is.
  const keyValues = () =>
    tagEditor.locator('input[name="tag_key"]').evaluateAll((els) =>
      els.map((e) => (e as HTMLInputElement).value)
    );
  await expect.poll(keyValues).toContain(tagKey);

  const rowIndex = (await keyValues()).indexOf(tagKey);
  await expect(tagEditor.locator('input[name="tag_val"]').nth(rowIndex)).toHaveValue(tagValue);

  // Remove it and save again — removal is part of the same draft.
  await tagEditor.locator('.tag-row').nth(rowIndex).getByRole('button', { name: 'Remove this tag' }).click();
  await tagEditor.getByRole('button', { name: 'Save changes' }).click();
  const removeToast = await waitForToast();
  expect(removeToast).toMatch(/removed/);
  await expect.poll(keyValues).not.toContain(tagKey);
}

test.describe('shared tag editor', () => {
  test('SQS: add and remove a tag via TagQueue/UntagQueue', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const queue = await createQueue(request, uniqueName('e2e-tags-sqs'));
    const tagKey = uniqueName('e2e-tag-key');

    await page.goto(`sqs/${queue}?tab=tags`);
    await addAndRemoveTag(page, waitForToast, tagKey, 'sqs-value');
  });

  test('DynamoDB: add and remove a tag via TagResource (Key/Value)', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const table = await createTable(request, uniqueName('e2e-tags-ddb'));
    const tagKey = uniqueName('e2e-tag-key');

    await page.goto(`ddb/${table}?tab=tags`);
    await addAndRemoveTag(page, waitForToast, tagKey, 'ddb-value');
  });

  test('KMS: add and remove a tag via TagResource (TagKey/TagValue)', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const keyId = await createKey(request, { alias: uniqueName('e2e-tags-kms') });
    const tagKey = uniqueName('e2e-tag-key');

    await page.goto(`kms/${keyId}`);
    await addAndRemoveTag(page, waitForToast, tagKey, 'kms-value');
  });
});
