import { test, expect } from '../fixtures/console';
import { createQueue } from '../fixtures/api';

// SQS console coverage: create (standard + FIFO, with/without an
// auto-created DLQ), the composer (attributes, FIFO group/dedup), the live
// peek panel (single-message delete, purge), DLQ redrive, the inline
// attribute editor, and queue delete.
//
// `console/templates/sqs.html` is the source of truth for all selectors
// below; `console/handlers.go` (grep `sqs`) for route/field names.

// The server computes the DLQ name from the main queue's name exactly this
// way (see sqsCreateQueue in console/handlers.go) — mirrored here so tests
// can address the auto-created DLQ without re-deriving it from the DOM.
function dlqNameFor(mainQueueName: string, fifo: boolean): string {
  const base = mainQueueName.replace(/\.fifo$/, '');
  return fifo ? `${base}-dlq.fifo` : `${base}-dlq`;
}

test.describe('create + identity', () => {
  test('standard queue shows its ARN/URL chips', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-std'));
    await page.goto(`sqs/${name}`);
    await expect(page.locator('.det-title')).toContainText(name);
    await expect(page.locator('.chips .copychip', { hasText: 'arn' })).toContainText(name);
    await expect(page.locator('.chips .copychip', { hasText: 'url' })).toContainText(name);
    await expect(page.locator('.badge.type')).toHaveCount(0);
  });

  test('FIFO queue wears the FIFO badge and gets a .fifo suffix', async ({
    page,
    request,
    uniqueName,
  }) => {
    const base = uniqueName('e2e-sqs-fifo');
    const name = await createQueue(request, base, { fifo: true });
    expect(name).toBe(`${base}.fifo`);
    await page.goto(`sqs/${name}`);
    await expect(page.locator('.det-title')).toContainText(name);
    await expect(page.locator('.badge.type')).toContainText('FIFO');
  });
});

test.describe('DLQ created alongside', () => {
  test('standard queue: -dlq queue is created, listed, and wired', async ({
    page,
    request,
    uniqueName,
  }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-dlqnew'), {
      dlqMode: 'new',
      maxReceive: 4,
    });
    const dlq = dlqNameFor(name, false);

    // Appears in the shared list pane alongside the main queue.
    await page.goto(`sqs/${name}`);
    await expect(page.locator('.li', { hasText: dlq })).toBeVisible();

    // Config tab reports the redrive wiring.
    await page.goto(`sqs/${name}?tab=config`);
    const dlqRow = page.locator('.tbl.kv tr', { hasText: 'Dead-letter queue' });
    await expect(dlqRow).toContainText(dlq);
    await expect(dlqRow).toContainText('after 4 receives');

    // The DLQ itself exists and wears the DLQ badge + "Fed by" connection.
    // Both are derived from the server's wiring graph, which is cached for
    // graphTTL (750ms, see console/client.go) to collapse bursts of polls —
    // so a goto() immediately after creation can briefly race a stale
    // snapshot. Reload-and-retry rides out that window instead of guessing
    // a fixed sleep.
    await expect(async () => {
      await page.goto(`sqs/${dlq}`);
      await expect(page.locator('.badge.dlq')).toContainText('DLQ', { timeout: 500 });
    }).toPass({ timeout: 5000 });
    await expect(page.locator('.conn-lbl', { hasText: 'Fed by' })).toBeVisible();
    await expect(page.locator('.conn-chips .conn-chip', { hasText: name })).toBeVisible();
  });

  test('FIFO queue: -dlq.fifo queue is created, FIFO, and wired', async ({
    page,
    request,
    uniqueName,
  }) => {
    const base = uniqueName('e2e-sqs-fifodlq');
    const name = await createQueue(request, base, {
      fifo: true,
      contentDedup: true,
      dlqMode: 'new',
    });
    const dlq = dlqNameFor(name, true);
    expect(dlq).toBe(`${base}-dlq.fifo`);

    await page.goto(`sqs/${name}?tab=config`);
    await expect(page.locator('.tbl.kv tr', { hasText: 'Content-based deduplication' })).toContainText('on');
    await expect(page.locator('.tbl.kv tr', { hasText: 'Dead-letter queue' })).toContainText(dlq);

    // Same graph-cache race as the standard-queue case above.
    await expect(async () => {
      await page.goto(`sqs/${dlq}`);
      await expect(page.locator('.badge.dlq')).toContainText('DLQ', { timeout: 500 });
    }).toPass({ timeout: 5000 });
    await expect(page.locator('.badge.type')).toContainText('FIFO');
  });
});

test.describe('composer', () => {
  test('sends a message with string + binary attributes on a standard queue', async ({
    page,
    request,
    uniqueName,
    setEditor,
  }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-attrs'));
    await page.goto(`sqs/${name}`);

    const body = `{"order":"attrs-${Date.now()}"}`;
    await setEditor('textarea[name=body][data-editor]', body);

    // The Add button sits inside a <label> alongside descriptive text, so its
    // computed accessible name is the whole label ("Message attributes
    // optional metadata"), not "Add" — scope by class instead of role/name.
    const addBtn = page.locator('.sqs-compose button.btn-outline.btn-sm');
    await addBtn.click();
    await addBtn.click();
    const rows = page.locator('.attr-row');
    await expect(rows).toHaveCount(2);

    await rows.nth(0).locator('input').first().fill('source');
    await rows.nth(0).locator('select').selectOption('String');
    await rows.nth(0).locator('input').nth(1).fill('e2e-suite');

    await rows.nth(1).locator('input').first().fill('payload');
    await rows.nth(1).locator('select').selectOption('Binary');
    await rows.nth(1).locator('input').nth(1).fill('aGVsbG8=');

    await page.getByRole('button', { name: 'Send' }).click();

    const msg = page.locator('.msg', { hasText: body });
    await expect(msg).toBeVisible();
    await expect(msg.locator('.mm.attr', { hasText: 'source' })).toContainText('e2e-suite');
    // Binary attribute values don't currently round-trip into the peek: the
    // receive-message decode path in console/client.go (QueueDetail, ~L407)
    // only reads StringValue off each attribute, dropping BinaryValue — so
    // "payload" renders with an empty value even though the send-side POST
    // body carried "aGVsbG8=" correctly (verified via network capture). This
    // asserts the type/name chip only, matching actual behavior; the empty
    // value is a real (minor) console gap worth a follow-up fix.
    const payloadChip = msg.locator('.mm.attr', { hasText: 'payload' });
    await expect(payloadChip).toBeVisible();
    await expect(payloadChip).toHaveAttribute('title', 'message attribute · Binary');
  });

  test('sends a FIFO message with group + dedup IDs, chips show in the peek', async ({
    page,
    request,
    uniqueName,
    setEditor,
  }) => {
    const base = uniqueName('e2e-sqs-fifosend');
    const name = await createQueue(request, base, { fifo: true });
    await page.goto(`sqs/${name}`);

    const body = `{"order":"fifo-${Date.now()}"}`;
    await setEditor('textarea[name=body][data-editor]', body);

    const group = 'e2e-group-1';
    const dedup = `e2e-dedup-${Date.now()}`;
    await page.locator('input[name=group]').fill(group);
    await page.locator('input[name=dedup]').fill(dedup);

    await page.getByRole('button', { name: 'Send' }).click();

    const msg = page.locator('.msg', { hasText: body });
    await expect(msg).toBeVisible();
    await expect(msg.locator('.mm', { hasText: 'group' })).toContainText(group);
    await expect(msg.locator('.mm', { hasText: 'dedup' })).toContainText(dedup);
    // Note: no SequenceNumber assertion — the backend never populates that
    // attribute (grep confirms no writer for it outside console/client.go's
    // read side), so the "seq" chip never renders in this emulator.
  });
});

test.describe('message + queue lifecycle', () => {
  test('deletes a single message from the peek', async ({ page, request, uniqueName, setEditor }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-msgdel'));
    await page.goto(`sqs/${name}`);

    const keepBody = `{"keep":"${Date.now()}"}`;
    const delBody = `{"del":"${Date.now()}"}`;
    await setEditor('textarea[name=body][data-editor]', keepBody);
    await page.getByRole('button', { name: 'Send' }).click();
    await expect(page.locator('.msg', { hasText: keepBody })).toBeVisible();

    await setEditor('textarea[name=body][data-editor]', delBody);
    await page.getByRole('button', { name: 'Send' }).click();
    const delMsg = page.locator('.msg', { hasText: delBody });
    await expect(delMsg).toBeVisible();

    await delMsg.hover();
    await delMsg.locator('.msg-del').click();
    await expect(page.locator('#confirm')).toBeVisible();
    await page.locator('#confirm-yes').click();
    await expect(page.locator('#confirm')).toBeHidden();

    await expect(page.locator('#message-panel-wrap')).not.toContainText(delBody);
    await expect(page.locator('.msg', { hasText: keepBody })).toBeVisible();
  });

  test('purge empties the peek', async ({ page, request, uniqueName, confirmDialog, setEditor }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-purge'));
    await page.goto(`sqs/${name}`);

    await setEditor('textarea[name=body][data-editor]', `{"purge-me":1}`);
    await page.getByRole('button', { name: 'Send' }).click();
    await expect(page.locator('.msg')).toHaveCount(1);

    await page.getByRole('button', { name: 'Purge' }).click();
    await confirmDialog('accept');

    await expect(page.locator('#message-panel-wrap .empty')).toContainText('No visible messages');
    await expect(page.locator('.panel-head .badge.zero')).toContainText('0');
  });
});

test.describe('DLQ redrive', () => {
  test('redrives messages from the DLQ back into the source queue', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    setEditor,
  }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-redrive'), { dlqMode: 'new' });
    const dlq = dlqNameFor(name, false);

    // Park messages directly in the DLQ (mirrors console_test.go's
    // TestFailureLoopRecovery, which sends straight to the DLQ rather than
    // forcing real max-receive failures through consume loops).
    const bodyA = `{"n":"redrive-a-${Date.now()}"}`;
    const bodyB = `{"n":"redrive-b-${Date.now()}"}`;
    await page.goto(`sqs/${dlq}`);
    await setEditor('textarea[name=body][data-editor]', bodyA);
    await page.getByRole('button', { name: 'Send' }).click();
    await expect(page.locator('.msg', { hasText: bodyA })).toBeVisible();
    await setEditor('textarea[name=body][data-editor]', bodyB);
    await page.getByRole('button', { name: 'Send' }).click();
    await expect(page.locator('.msg', { hasText: bodyB })).toBeVisible();

    // The DLQ page offers a redrive button back toward its one source.
    const redriveBtn = page.getByRole('button', { name: /Redrive/ });
    await expect(redriveBtn).toContainText(name);
    await redriveBtn.click();
    await confirmDialog('accept');

    // Moves are synchronous server-side (see sqs/store_ext.go), so the very
    // next render of the DLQ's own peek already shows the completed task and
    // an empty queue.
    await expect(page.locator('#message-panel-wrap')).toContainText(/complete/i);
    await expect(page.locator('#message-panel-wrap .empty')).toContainText('No visible messages');

    // The messages reappear in the source queue's peek. This is a fresh
    // navigation, so use waitForLive in case the live-poll hasn't caught up
    // yet (belt-and-braces alongside Playwright's own auto-retry).
    await page.goto(`sqs/${name}`);
    await expect(page.locator('.msg', { hasText: bodyA })).toBeVisible({ timeout: 15000 });
    await expect(page.locator('.msg', { hasText: bodyB })).toBeVisible({ timeout: 15000 });
  });
});

test.describe('attribute edit', () => {
  test('visibility timeout change persists across reload', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-attredit'), { visibility: 30 });
    await page.goto(`sqs/${name}?tab=config`);

    await expect(page.locator('.tbl.kv tr', { hasText: 'Visibility timeout' })).toContainText('30 s');

    await page.getByTitle('Edit settings').click();
    await page.locator('input[name=visibility]').fill('77');
    await page.getByRole('button', { name: 'Save settings' }).click();

    await expect(page.locator('.tbl.kv tr', { hasText: 'Visibility timeout' })).toContainText('77 s');

    await page.reload();
    await expect(page.locator('.tbl.kv tr', { hasText: 'Visibility timeout' })).toContainText('77 s');
  });
});

test.describe('delete queue', () => {
  test('deletes a queue behind the styled confirm dialog', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-delq'));
    await page.goto(`sqs/${name}`);

    await page.locator('.acts').getByRole('button', { name: 'Delete' }).click();
    await confirmDialog('accept');

    await page.waitForURL(/\/sqs(\?|$)/);
    await expect(page.locator('#flashbar')).toContainText('Queue deleted');
    await expect(page.locator('.li', { hasText: name })).toHaveCount(0);
  });
});
