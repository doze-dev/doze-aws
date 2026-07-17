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

async function addAndRemoveTag(
  page: import('@playwright/test').Page,
  waitForToast: (opts?: { kind?: 'ok' | 'err' }) => Promise<string>,
  tagKey: string,
  tagValue: string
) {
  const tagEditor = page.locator('#tag-editor');
  await expect(tagEditor).toBeVisible();

  // Add the tag via the shared tag-row-form.
  await tagEditor.locator('.tag-row-form input[name="key"]').fill(tagKey);
  await tagEditor.locator('.tag-row-form input[name="value"]').fill(tagValue);
  await tagEditor.locator('.tag-row-form').getByRole('button', { name: 'Add' }).click();
  const addToast = await waitForToast();
  expect(addToast).toMatch(/Tag saved/);
  await waitToastGone(page);

  // Verify it round-tripped into the rendered tag list (not just the toast).
  const row = tagEditor.locator('tr', { hasText: tagKey });
  await expect(row).toBeVisible();
  await expect(row).toContainText(tagValue);

  // Remove it and verify it's gone from the rendered list.
  await row.getByRole('button', { name: 'Remove tag' }).click();
  const removeToast = await waitForToast();
  expect(removeToast).toMatch(/Tag removed/);
  await expect(tagEditor.locator('tr', { hasText: tagKey })).toHaveCount(0);
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

    await page.goto(`sqs/${queue}?tab=config`);
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

    await page.goto(`ddb/${table}?tab=details`);
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
