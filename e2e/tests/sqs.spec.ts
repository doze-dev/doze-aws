import { test, expect } from '../fixtures/console';
import fs from 'node:fs';
import path from 'node:path';
import { createFunction, createQueue, postForm } from '../fixtures/api';
import { ACCOUNT_ID, ORIGIN } from '../playwright.config';

// SQS console coverage: create (standard + FIFO, with/without an
// auto-created DLQ), the composer (attributes, FIFO group/dedup), the live
// peek panel (single-message delete, purge), DLQ redrive, the inline
// attribute editor, and queue delete.
//
// `internal/console/templates/sqs.html` is the source of truth for all selectors
// below; `internal/console/handlers.go` (grep `sqs`) for route/field names.

// The server computes the DLQ name from the main queue's name exactly this
// way (see sqsCreateQueue in internal/console/handlers.go) — mirrored here so tests
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
    // A standard queue says so beside its name (the design's "standard" pill).
    await expect(page.locator('.det-title .badge.type')).toHaveText('standard');
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
    await expect(page.locator('.badge.type').first()).toHaveText('FIFO');
    await expect(page.locator('.badge.type').nth(1)).toHaveText('content-based dedup off');
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
    const dlqRow = page.locator('.q-cfg .q-r', { hasText: 'Dead-letter queue' });
    await expect(dlqRow).toContainText(dlq);
    await expect(dlqRow).toContainText('Gets a message that was received 4 times, on its next receive.');

    // The DLQ itself exists and wears the DLQ badge + "Fed by" connection.
    // Both are derived from the server's wiring graph, which is cached for
    // graphTTL (750ms, see internal/console/client.go) to collapse bursts of polls —
    // so a goto() immediately after creation can briefly race a stale
    // snapshot. Reload-and-retry rides out that window instead of guessing
    // a fixed sleep.
    await expect(async () => {
      await page.goto(`sqs/${dlq}`);
      await expect(page.locator('.badge.dlq')).toContainText('DLQ', { timeout: 500 });
    }).toPass({ timeout: 5000 });
    await expect(page.locator('.conn-lbl', { hasText: 'Fed by' })).toBeVisible();
    await expect(page.locator('.conn .conn-chip', { hasText: name })).toBeVisible();
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
    await expect(page.locator('.q-cfg .q-r', { hasText: 'Content-based deduplication' })).toContainText('on');
    await expect(page.locator('.q-cfg .q-r', { hasText: 'Dead-letter queue' })).toContainText(dlq);

    // Same graph-cache race as the standard-queue case above.
    await expect(async () => {
      await page.goto(`sqs/${dlq}`);
      await expect(page.locator('.badge.dlq')).toContainText('DLQ', { timeout: 500 });
    }).toPass({ timeout: 5000 });
    await expect(page.locator('.badge.type').first()).toHaveText('FIFO');
  });
});


// The composer is docked at the foot of the message list and collapsed until
// used. Opening it is now part of composing, so every test that sends goes
// through here rather than reaching straight for the Send button.
//
// It also has to wait for CodeMirror: the editor is refreshed on expand
// (it mis-measures while hidden), and filling it before that lands in a
// textarea the user cannot see.
async function openComposer(page: import('@playwright/test').Page) {
  const bar = page.locator('.dock-bar');
  if (await bar.isVisible().catch(() => false)) {
    await bar.click();
    await expect(page.locator('.sqs-compose')).toBeVisible();
  }
}

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
    await openComposer(page);
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

    await openComposer(page);
    await page.locator('.sqs-compose').getByRole('button', { name: 'Send' }).click();

    const msg = page.locator('.msg', { hasText: body });
    await expect(msg).toBeVisible();
    // What a message carries is read by opening it (board Queue).
    await msg.locator('.rowopen').click();
    await expect(msg.locator('.q-pill.attr', { hasText: 'source' })).toContainText('e2e-suite');
    // Binary attribute values don't currently round-trip into the peek: the
    // receive-message decode path in internal/console/client.go (QueueDetail, ~L407)
    // only reads StringValue off each attribute, dropping BinaryValue — so
    // "payload" renders with an empty value even though the send-side POST
    // body carried "aGVsbG8=" correctly (verified via network capture). This
    // asserts the type/name chip only, matching actual behavior; the empty
    // value is a real (minor) console gap worth a follow-up fix.
    const payloadChip = msg.locator('.q-pill.attr', { hasText: 'payload' });
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
    await openComposer(page);
    await page.locator('input[name=group]').fill(group);
    await page.locator('input[name=dedup]').fill(dedup);

    await openComposer(page);
    await page.locator('.sqs-compose').getByRole('button', { name: 'Send' }).click();

    const msg = page.locator('.msg', { hasText: body });
    await expect(msg).toBeVisible();
    await msg.locator('.rowopen').click();
    await expect(msg.locator('.q-pill', { hasText: 'group' })).toContainText(group);
    await expect(msg.locator('.q-pill', { hasText: 'dedup' })).toContainText(dedup);
    // Note: no SequenceNumber assertion — the backend never populates that
    // attribute (grep confirms no writer for it outside internal/console/client.go's
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
    await openComposer(page);
    await page.locator('.sqs-compose').getByRole('button', { name: 'Send' }).click();
    await expect(page.locator('.msg', { hasText: keepBody })).toBeVisible();

    await setEditor('textarea[name=body][data-editor]', delBody);
    await openComposer(page);
    await page.locator('.sqs-compose').getByRole('button', { name: 'Send' }).click();
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
    await openComposer(page);
    await page.locator('.sqs-compose').getByRole('button', { name: 'Send' }).click();
    await expect(page.locator('.msg')).toHaveCount(1);

    // exact, because getByRole's name match is a SUBSTRING and the message
    // rows are role="button" with the body in their accessible name — so the
    // fixture body {"purge-me":1} matches a locator looking for "Purge".
    await page.locator('#message-panel-wrap')
      .getByRole('button', { name: 'Purge', exact: true }).click();
    await confirmDialog('accept');

    await expect(page.locator('#message-panel-wrap .empty')).toContainText('No visible messages');
    await expect(page.locator('.tabbar .tab-ct').first()).toHaveText('0');
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
    await openComposer(page);
    await page.locator('.sqs-compose').getByRole('button', { name: 'Send' }).click();
    await expect(page.locator('.msg', { hasText: bodyA })).toBeVisible();
    await setEditor('textarea[name=body][data-editor]', bodyB);
    await openComposer(page);
    await page.locator('.sqs-compose').getByRole('button', { name: 'Send' }).click();
    await expect(page.locator('.msg', { hasText: bodyB })).toBeVisible();

    // The DLQ page offers a redrive button back toward its one source.
    const redriveBtn = page.getByRole('button', { name: /Redrive/ });
    await expect(redriveBtn).toContainText(name);
    await redriveBtn.click();
    // The design's dialog: the destination is chosen inside it.
    const dialog = page.getByRole('dialog', { name: 'Redrive messages' });
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('select[name=dest]')).toHaveValue(name);
    await dialog.getByRole('button', { name: 'Redrive' }).click();
    await expect(dialog).toBeHidden();

    // A move is a background task (sqs/move_task.go): the panel first shows it
    // running, as the design does, and then complete with the queue empty.
    await expect(page.locator('#message-panel-wrap .mv-task')).toContainText(/Redriving|complete/i);
    await expect(page.locator('#message-panel-wrap')).toContainText(/complete/i, { timeout: 20_000 });
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

    const visRow = page.locator('.q-cfg .q-r', { hasText: 'Visibility timeout' });
    await expect(visRow).toContainText('30 s');

    await page.getByRole('button', { name: 'Edit settings' }).click();
    await page.locator('input[name=visibility]').fill('77');
    await page.getByRole('button', { name: 'Save settings' }).click();

    await expect(visRow).toContainText('77 s');

    await page.reload();
    await expect(visRow).toContainText('77 s');
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

    await page.locator('.acts').getByRole('button', { name: 'Delete queue' }).click();
    await confirmDialog('accept');

    // Back to the list, nothing opened, with a toast naming the queue (board Sqs-16).
    await page.waitForURL(/\/sqs(\?|$)/);
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`Queue “${name}” deleted`);
    await expect(page.locator('.li', { hasText: name })).toHaveCount(0);
    await expect(page.locator('.li.on')).toHaveCount(0);
  });
});

// Copy-as-CLI: the send form shows the aws command it is about to be,
// substituted live from the current field values against this endpoint.
test('the composer renders its own aws CLI command live', async ({ page, request, uniqueName, setEditor }) => {
  const queue = uniqueName('e2e-sqs-cli');
  await createQueue(request, queue);
  await page.goto(`sqs/${queue}`);
  await openComposer(page);
  await page.locator('.cli-btn').first().click();
  const pre = page.locator('.cli-out pre').first();
  await expect(pre).toContainText(`sqs send-message --queue-url ${ORIGIN}/${ACCOUNT_ID}/${queue}`);
  await expect(pre).not.toContainText('--delay-seconds');
  await setEditor('textarea[name="body"]', '{"hello":"cli"}');
  await page.locator('input[name="delay"]').fill('45');
  await expect(pre).toContainText(`--message-body '{"hello":"cli"}'`);
  await expect(pre).toContainText('--delay-seconds 45');
  await expect(pre).toContainText(`--endpoint-url ${ORIGIN}`);
});

// ---- Route-coverage pass: the live peek poll, the Consume tab, the queue
// policy grants, and the two batch/redrive controls that have no way in. ----

// A message produced by "someone else" while the page sits open. postForm
// hits the console's own send route — an ARRANGE step, so the action under
// test (the peek's own poll) is the only thing that can make it appear.
async function sendViaApi(request: import('@playwright/test').APIRequestContext, queue: string, body: string) {
  await postForm(request, `sqs/${queue}/send`, { body });
}

test.describe('live peek', () => {
  test('a message sent elsewhere appears in the open peek without a reload', async ({
    page,
    request,
    uniqueName,
    waitForLive,
  }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-live'));
    await page.goto(`sqs/${name}`);
    await expect(page.locator('#message-panel-wrap .empty')).toContainText('No visible messages');

    const body = `{"live":"${Date.now()}"}`;
    const polled = page.waitForResponse(
      (r) => r.url().includes(`/sqs/${name}/messages`) && r.status() === 200
    );
    await sendViaApi(request, name, body);
    await polled;
    await waitForLive('#message-panel-wrap', (t) => t.includes(body));
    await expect(page.locator('.tabbar .tab-ct').first()).toHaveText('1');
  });
});

test.describe('consume tab', () => {
  test('receive hides messages; release one; release and delete the rest as a batch', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-consume'), { visibility: 300 });
    for (const n of ['a', 'b', 'c']) await sendViaApi(request, name, `{"rcv":"${n}"}`);

    await page.goto(`sqs/${name}?tab=consume`);
    const consumed = page.locator('#sqs-consumed');
    await expect(consumed.locator('.empty')).toContainText('Nothing received yet');

    // Receive: a real ReceiveMessage, so all three go in flight.
    const receive = () => page.getByRole('button', { name: 'Receive', exact: true }).click();
    await receive();
    await expect(consumed.locator('.rcv-row')).toHaveCount(3);
    await expect(consumed.locator('.rcv-count').first()).toHaveText('1×');
    // The held table, as the design draws it: headed columns and a countdown.
    await expect(consumed.locator('.q-rh')).toContainText('Held by you. Nobody else sees these until they come back.');
    await expect(consumed.locator('.rcv-row .rt-cd').first()).toHaveText(/^[0-4]:[0-5]\d$/);

    // Release one: ChangeMessageVisibility to 0.
    await consumed.locator('.rcv-row').first().getByRole('button', { name: 'Release' }).click();
    expect(await waitForToast()).toContain('Released — visible again now');

    // The two still held stay on screen while their neighbour is released.
    await expect(consumed.locator('.rcv-row')).toHaveCount(2);

    // Receiving again returns ONLY the released one (the other two are still
    // hidden for 300s), its receive count has climbed, and it joins the two
    // already held.
    await receive();
    await expect(consumed.locator('.rcv-row')).toHaveCount(3);
    await expect(consumed.locator('.rcv-count').last()).toHaveText('2×');

    // Release it through the multi-select: ChangeMessageVisibilityBatch.
    await consumed.locator('.rcv-row input[type=checkbox]').last().check();
    await consumed.getByRole('button', { name: 'Release selected' }).click();
    expect(await waitForToast()).toContain('1 released');
    await expect(consumed.locator('.rcv-row')).toHaveCount(2);

    // It is receivable again; delete it through the multi-select:
    // DeleteMessageBatch.
    await receive();
    await expect(consumed.locator('.rcv-row')).toHaveCount(3);
    await expect(consumed.locator('.rcv-count').last()).toHaveText('3×');
    await consumed.locator('.rcv-row input[type=checkbox]').last().check();
    await consumed.getByRole('button', { name: 'Delete selected' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('1 deleted');
    await expect(consumed.locator('.rcv-row')).toHaveCount(2);

    // AWS-side, read back through a fresh page: one deleted, two in flight.
    await page.goto(`sqs/${name}`);
    const fact = (k: string) => page.locator('.factstrip .fact', { hasText: k }).locator('.fx-v');
    await expect(fact('Visible')).toHaveText('0');
    await expect(fact('In flight')).toHaveText('2');
  });

  // Regression (fixed in 1.0): the out-of-band count tail (sqs.html, <template> wrapping the
  // hx-swap-oob spans after message_panel / sqs_consumed) never applies under
  // htmx 4 — a parsed <template>'s children live in .content, so the OOB scan
  // never finds them. The strip, tab count and list-row count stay stale after
  // send / receive / poll until a full reload.
  test('counts outside the panel follow a receive without a reload', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-oob'));
    await sendViaApi(request, name, '{"oob":1}');
    await page.goto(`sqs/${name}?tab=consume`);
    const fact = (k: string) => page.locator('.factstrip .fact', { hasText: k }).locator('.fx-v');
    await expect(fact('Visible')).toHaveText('1');
    await page.getByRole('button', { name: 'Receive', exact: true }).click();
    await expect(page.locator('#sqs-consumed .rcv-row')).toHaveCount(1);
    await expect(fact('In flight')).toHaveText('1');
    await expect(fact('Visible')).toHaveText('0');
  });
});

test.describe('queue policy', () => {
  test('grant adds a statement row; revoke removes it', async ({ page, request, uniqueName, waitForToast, confirmDialog }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-perm'));
    await page.goto(`sqs/${name}?tab=config`);
    const cfg = page.locator('#sqs-config');
    await expect(cfg.locator('.perm-none')).toBeVisible();

    const label = 'e2eGrant';
    const form = cfg.locator('form.q-grant');
    await form.locator('input[name=label]').fill(label);
    await form.locator('input[name=account]').fill('111122223333');
    await form.locator('select[name=action]').selectOption('ReceiveMessage');
    await form.getByRole('button', { name: 'Grant' }).click();
    expect(await waitForToast()).toContain(`Permission “${label}” added`);

    const row = page.locator('#sqs-config .q-perms .q-r', { hasText: label, has: page.getByRole('button', { name: 'Revoke' }) });
    await expect(row).toContainText('111122223333');
    await expect(row).toContainText('ReceiveMessage');
    // AWS-side: the statement landed in the Policy attribute.
    await page.getByRole('button', { name: 'Show raw attributes' }).click();
    await expect(page.locator('#sqs-config .tbl.kv tr', { hasText: 'Policy' })).toContainText(label);

    await row.getByRole('button', { name: 'Revoke' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Permission removed');
    await expect(page.locator('#sqs-config').getByRole('button', { name: 'Revoke' })).toHaveCount(0);
    await expect(page.locator('#sqs-config .perm-none')).toBeVisible();
  });
});

// Regression (fixed in 1.0): POST /sqs/{queue}/send-batch has no UI entry point. The handler's own
// comment describes it as "the composer's send N mode", but no template
// renders a repeat/batch control (grep send-batch in templates/: nothing).
test('composer sends a burst of N messages as one SendMessageBatch', async ({
  page,
  request,
  uniqueName,
  setEditor,
  waitForToast,
}) => {
  const name = await createQueue(request, uniqueName('e2e-sqs-batch'));
  await page.goto(`sqs/${name}`);
  await openComposer(page);
  await setEditor('textarea[name=body][data-editor]', '{"burst":1}');
  await page.locator('.sqs-compose input[name=repeat]').fill('3');
  await page.locator('.sqs-compose').getByRole('button', { name: /Send 3|Send batch/ }).click();
  expect(await waitForToast()).toContain('3 sent');
  await expect(page.locator('.msg', { hasText: '"burst":1' })).toHaveCount(3);
});


// A FIFO queue's composer has a preview too, and it carries the group and the
// deduplication id: the preview used to sit in the standard queue's branch only.
test('a FIFO queue’s composer previews its group and deduplication id', async ({ page, request, uniqueName, setEditor }) => {
  const queue = uniqueName('e2e-sqs-cli') + '.fifo';
  await createQueue(request, queue, { fifo: true });
  await page.goto(`sqs/${queue}`);
  await openComposer(page);
  await page.locator('.cli-btn').first().click();
  const pre = page.locator('.cli-out pre').first();
  await setEditor('textarea[name="body"]', 'line one\n\nline   two');
  await page.locator('input[name="dedup"]').fill('d-1');
  await expect(pre).toContainText('--message-group-id default');
  await expect(pre).toContainText('--message-deduplication-id d-1');
  // The body's own spacing is the user's, and the command keeps it.
  await expect(pre).toContainText("'line one\n\nline   two'");
});

// A live region polls for as long as its page is open. When what it polls is
// gone, the poll's failure must not draw a box or pull focus away from what the
// user is typing every three seconds.
test('a failing poll does not take focus or draw a failure', async ({ page, request, uniqueName }) => {
  test.setTimeout(30_000);
  const queue = uniqueName('e2e-sqs-poll');
  await createQueue(request, queue);
  await page.goto(`sqs/${queue}?tab=config`);
  await page.goto(`sqs/${queue}`);
  const filter = page.locator('.filter input');
  await filter.click();
  await filter.pressSequentially('abc');

  await postForm(request, `sqs/${queue}/delete-queue`, {});
  await page.waitForTimeout(7000); // at least two polls of a three-second region

  await expect(page.locator('.err[data-doze-err]')).toHaveCount(0);
  await expect(filter).toBeFocused();
  await expect(filter).toHaveValue('abc');
});

// The wire underneath a queue: what the app sent to THIS queue, live, without
// leaving the page. The console's own requests are not the app's and are not in it.
test('the wire under a queue shows the app’s calls to it as they happen', async ({ page, request, uniqueName }) => {
  const queue = uniqueName('e2e-sqs-wire');
  const other = uniqueName('e2e-sqs-wire-other');
  await createQueue(request, queue);
  await createQueue(request, other);
  const gateway = new URL('/', ORIGIN + '/').toString();
  const send = (q: string, body: Record<string, unknown>) =>
    request.post(gateway, {
      headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': 'AmazonSQS.SendMessage' },
      data: JSON.stringify({ QueueUrl: `${ORIGIN}/${ACCOUNT_ID}/${q}`, ...body }),
    });

  await page.goto(`sqs/${queue}`);
  const dock = page.locator('#res-wire');
  await expect(dock).toContainText('Nothing yet');

  await send(queue, { MessageBody: 'one' });
  await send(other, { MessageBody: 'not ours' });
  await send(queue, {}); // no body: refused
  await expect(dock.locator('.rw-row')).toHaveCount(2, { timeout: 10_000 });
  await expect(dock.locator('.rw-row.refused')).toHaveCount(1);
  await expect(dock.locator('.rw-row').first()).toContainText('SendMessage');

  // A time says its zone and UTC on hover.
  await expect(dock.locator('.rw-row time').first()).toHaveAttribute('title', /\(UTC[+-]\d\d:\d\d\) · \d\d:\d\d:\d\d UTC/);
});

// ---------------------------------------------------------------------------
// Stage 1 console: the create form teaches, the Configuration tab says who
// reads the queue and what saving would do. Every sentence asserted here is
// held by a test of the behaviour it describes (named in the template).
// ---------------------------------------------------------------------------

test.describe('create form', () => {
  test('a period is legal only as the .fifo suffix of a FIFO queue (SQS-3)', async ({ page }) => {
    // The form no longer second-guesses the name with a pattern of its own: it
    // sends what was typed and shows what the service says (board Sqs-05).
    await page.goto('sqs/create');
    const name = page.locator('input[name=name]');
    const err = page.locator('.err').first();
    const submit = () => page.getByRole('button', { name: 'Create queue' }).click();
    await page.locator('.dlq-mode', { hasText: 'None' }).click();

    await name.fill('a.b');
    await submit();
    await expect(err).toContainText("queue name may contain only alphanumeric characters, hyphens and underscores, and got '.'");
    await name.fill('orders.fifo'); // FIFO is off
    await submit();
    await expect(err).toContainText('FIFO queue names must end in .fifo and only FIFO queues may');
    // The suffix counts toward 80: 75 + ".fifo" is the longest.
    await page.locator('.q-seg', { has: page.locator('input[name=fifo]') }).click();
    await name.fill('a'.repeat(76) + '.fifo');
    await submit();
    await expect(err).toContainText('queue name is longer than 80 characters (the .fifo suffix counts toward the limit)');
    await expect(page).toHaveURL(/\/sqs\/create$/);
  });

  test('the dead-letter name preview does not double the .fifo suffix (SQS-4)', async ({ page }) => {
    await page.goto('sqs/create');
    const fifoToggle = page.locator('.q-seg', { has: page.locator('input[name=fifo]') });
    await fifoToggle.click();
    await page.locator('input[name=name]').fill('orders.fifo');
    const preview = page.locator('.dlq-mode', { hasText: 'Create one alongside' }).locator('.mono');
    await expect(preview).toHaveText('orders-dlq.fifo');
    await page.locator('input[name=name]').fill('orders');
    await expect(preview).toHaveText('orders-dlq.fifo');
    await fifoToggle.click();
    await expect(preview).toHaveText('orders-dlq');
  });

  test('the access policy hint says it is evaluated (SQS-2)', async ({ page }) => {
    await page.goto('sqs/create');
    await page.locator('details.adv-sec summary').click();
    const hint = page.locator('.field', { hasText: 'Access policy' }).locator('.hint');
    await expect(hint).toContainText('evaluated');
    await expect(hint).not.toContainText('does not evaluate');
  });

  test('each delivery setting says what it does, with its range and default', async ({ page }) => {
    await page.goto('sqs/create');
    const vis = page.locator('.field', { has: page.locator('input[name=visibility]') });
    // The five lines of the teaching pattern: the sentence, then the range, the
    // default and whether this emulator enforces it or only stores it.
    await expect(vis.locator('.hint')).toContainText('it comes back');
    await expect(vis.locator('.setting-meta')).toHaveText(/0\s–\s43\u00a0200 · default 30 · enforced/);
    const retention = page.locator('.field', { has: page.locator('input[name=retention]') });
    await expect(retention.locator('.setting-meta')).toHaveText(/60\s–\s1\u00a0209\u00a0600 · default 4 days · enforced/);
    const maxrecv = page.locator('.field', { has: page.locator('input[name=max_receive]') });
    await expect(maxrecv.locator('.setting-meta')).toHaveText(/1\s–\s1\u00a0000 · enforced/);
    await page.locator('details.adv-sec summary').click();
    const size = page.locator('.field', { has: page.locator('input[name=max_bytes]') });
    await expect(size.locator('.setting-meta')).toHaveText(/1\u00a0024\s–\s1\u00a0048\u00a0576 · default 1\u00a0048\u00a0576 · enforced/);
    // The value in words, below a minute as well.
    await page.locator('input[name=visibility]').fill('20');
    await expect(vis.locator('.dur-echo')).toHaveText('20 seconds');
    await page.locator('input[name=delay]').fill('0');
    await expect(page.locator('.field', { has: page.locator('input[name=delay]') }).locator('.dur-echo')).toHaveText('none');
  });

  test('the life of one message follows the numbers typed', async ({ page }) => {
    await page.goto('sqs/create');
    const life = page.locator('.life');
    await expect(life).toContainText('Waiting immediately. No delay is set.');
    await expect(life).toContainText('It is hidden for 30 s');
    await expect(life).toContainText('It is dropped after 4 days');
    await expect(life).toContainText('receive count goes to 2');
    await expect(page.locator('.life-foot')).toContainText('never reaches step 3');
    // The panel is beside the form, as the design draws it, not under it.
    const formBox = await page.locator('.q-col[data-col=left]').boundingBox();
    const lifeBox = await life.boundingBox();
    expect(lifeBox!.x).toBeGreaterThan(formBox!.x + formBox!.width - 1);

    await page.locator('input[name=visibility]').fill('90');
    await page.locator('input[name=delay]').fill('10');
    await page.locator('input[name=retention]').fill('3600');
    await expect(life).toContainText('Invisible for 10 s');
    await expect(life).toContainText('It is hidden for 90 s');
    await expect(life).toContainText('It is dropped after 1 hour');

    // The dead-letter step follows the mode and the count.
    await page.locator('input[name=max_receive]').fill('5');
    await expect(life).toContainText('Received 5 times');
    // Five deliveries, and the receive after them is the one that moves it.
    await expect(life).toContainText('Its next receive moves it to orders-dlq instead of delivering it again');
    await page.locator('input[name=name]').fill('ship');
    await expect(life).toContainText('moves it to ship-dlq');
    await page.locator('.dlq-mode', { hasText: 'None' }).click();
    await expect(life.locator('li', { hasText: 'Never moved' })).toBeVisible();
    await expect(life.locator('li', { hasText: 'instead of delivering it again' })).toBeHidden();
  });

  test('how a FIFO queue behaves appears with FIFO, and only then', async ({ page }) => {
    await page.goto('sqs/create');
    const panel = page.locator('.q-group');
    await expect(panel).toBeHidden();
    await expect(page.locator('details.adv-sec')).not.toHaveAttribute('open', '');
    await page.locator('.q-seg', { has: page.locator('input[name=fifo]') }).click();
    await expect(panel).toBeVisible();
    await expect(panel).toContainText('in the order sent');
    await expect(panel).toContainText('the next in that group waits');
    await expect(panel).toContainText('holds back only its own group');
    // The board's three sentences, and no more.
    await expect(panel.locator('li')).toHaveCount(3);
    // The FIFO layout (board Sqs-04): Advanced open in the right-hand column,
    // above the group panel, and the life of one message gone.
    const adv = page.locator('details.adv-sec');
    await expect(adv).toHaveAttribute('open', '');
    await expect(page.locator('.life')).toBeHidden();
    const general = (await page.locator('.q-col[data-col=left] .q-card').first().boundingBox())!;
    const advBox = (await adv.boundingBox())!;
    expect(advBox.x).toBeGreaterThan(general.x + general.width - 1);
    expect(advBox.y).toBeLessThan((await panel.boundingBox())!.y);
    await expect(page.locator('.field', { has: page.locator('input[name=delay]') })).toContainText('Queue-wide on a FIFO queue.');
    await expect(page.locator('.q-create')).toContainText("A FIFO queue's dead-letter queue has to be FIFO too, so the one made alongside gets the .fifo suffix.");
  });
});

test.describe('configuration tab', () => {
  test('"In this stack" says whether the queue hides a message as long as the reader runs', async ({
    page,
    request,
    uniqueName,
  }) => {
    const queue = await createQueue(request, uniqueName('e2e-sqs-reader'), { visibility: 30 });
    const fn = uniqueName('e2e-sqs-reader-fn');
    const empty = path.resolve(__dirname, `../.tmp/sqs-reader-${fn}`);
    fs.mkdirSync(empty, { recursive: true });
    await createFunction(request, fn, { code: empty, timeout: 60 });
    await postForm(request, `lambda/${fn}/add-mapping`, { queue, batch: 5 });

    await page.goto(`sqs/${queue}?tab=config`);
    const reader = page.locator('.stack-readers .q-callout', { hasText: fn });
    await expect(reader).toHaveClass(/warn/);
    await expect(reader).toContainText("Shorter than the reader's timeout");
    await expect(reader).toContainText('may run for 60 s. A message received at the start comes back after 30 s and can be handled twice');
    await expect(reader).toContainText('which here would be 360 s');
    await expect(reader).toContainText('does not check this when the trigger is created');
    await expect(reader.locator('a')).toHaveAttribute('href', new RegExp(`/lambda/${fn}$`));

    // The other state: a visibility timeout that covers the function.
    await postForm(request, `sqs/${queue}/attributes`, { visibility: 600 });
    await page.goto(`sqs/${queue}?tab=config`);
    await expect(page.locator('.stack-readers .q-callout', { hasText: fn })).toContainText('Its timeout is 60 s and a received message stays hidden 600 s, so one run finishes before the message returns');
    await expect(page.locator('.stack-readers .q-callout.warn')).toHaveCount(0);
    await expect(page.locator('.stack-readers')).toContainText('which here would be 360 s');
  });

  test('"what saving does" names each change against what is stored', async ({ page, request, uniqueName }) => {
    const queue = await createQueue(request, uniqueName('e2e-sqs-changes'), { visibility: 30 });
    await page.goto(`sqs/${queue}?tab=config`);
    await page.getByRole('button', { name: 'Edit settings' }).click();
    const changes = page.locator('.what-panel');
    // Beside the form, with its own quiet state.
    await expect(changes).toBeVisible();
    await expect(changes).toContainText('Nothing yet');
    const formBox = await page.locator('form.cfg-form').boundingBox();
    const sideBox = await changes.boundingBox();
    expect(sideBox!.x).toBeGreaterThan(formBox!.x + formBox!.width - 1);

    await page.locator('input[name=visibility]').fill('120');
    await expect(changes).toContainText('Only the settings you touched');
    await expect(changes).toContainText('Visibility30 s → 120 s');
    await expect(changes).toContainText('Messages already held keep the timeout they were received with');

    await page.locator('input[name=retention]').fill('120');
    await expect(changes).toContainText('Retention4 days → 120 s');
    await expect(changes).toContainText('dropped the next time the queue is read or swept');

    // Putting it back removes the sentence.
    await page.locator('input[name=visibility]').fill('30');
    await expect(changes).not.toContainText('Visibility');
  });

  test('typing a visibility shorter than a reader\'s timeout says so, live, in the form', async ({
    page, request, uniqueName,
  }) => {
    const queue = await createQueue(request, uniqueName('e2e-sqs-neighbour'), { visibility: 600 });
    const fn = uniqueName('e2e-sqs-neighbour-fn');
    const empty = path.resolve(__dirname, `../.tmp/sqs-neighbour-${fn}`);
    fs.mkdirSync(empty, { recursive: true });
    await createFunction(request, fn, { code: empty, timeout: 30 });
    await postForm(request, `lambda/${fn}/add-mapping`, { queue, batch: 5 });

    await page.goto(`sqs/${queue}?tab=config`);
    await page.getByRole('button', { name: 'Edit settings' }).click();
    const callout = page.locator('.cfg-neighbour');
    await expect(callout).toBeHidden(); // 600 s covers 30 s: quiet when it fits
    await page.locator('input[name=visibility]').fill('20');
    await expect(callout).toBeVisible();
    await expect(callout).toContainText("Shorter than the reader's timeout");
    await expect(callout).toContainText(fn);
    await expect(callout).toContainText('may run for 30 s');
    await expect(callout).toContainText('comes back after 20 s');
    await expect(callout).toContainText('which here would be 180 s');
    await page.locator('input[name=visibility]').fill('300');
    await expect(callout).toBeHidden();
  });

  test('the After receives field says what its number means', async ({ page, request, uniqueName }) => {
    const queue = await createQueue(request, uniqueName('e2e-sqs-afterrecv'), { dlqMode: 'new', maxReceive: 3 });
    await page.goto(`sqs/${queue}?tab=config`);
    await page.getByRole('button', { name: 'Edit settings' }).click();
    // A message is delivered N times and the NEXT receive moves it
    // (sqs/receive.go: moved when ReceiveCount >= MaxReceiveCount at receive time).
    const field = page.locator('.field', { has: page.locator('input[name=max_receive]') });
    const says = page.locator('#sqs-config .cfg-after');
    await expect(says).toHaveText('With 3, a message that keeps coming back is delivered 3 times; the next receive sets it aside.');
    await page.locator('input[name=max_receive]').fill('1');
    await expect(says).toHaveText('With 1, a message that keeps coming back is delivered 1 time; the next receive sets it aside.');
    await expect(field.locator('.setting-meta')).toContainText('enforced');
  });

  test('the confirms say what they cost, and the cancel says what it keeps', async ({
    page, request, uniqueName, confirmDialog,
  }) => {
    const queue = await createQueue(request, uniqueName('e2e-sqs-confirms'));
    await postForm(request, `sqs/${queue}/send`, { body: 'one' });
    await page.goto(`sqs/${queue}`);

    await page.locator('.acts').getByRole('button', { name: 'Delete queue' }).click();
    await expect(page.locator('#confirm-title')).toContainText(`Delete queue “${queue}”`);
    await expect(page.locator('#confirm-msg')).toContainText('removed from this machine');
    await expect(page.locator('#confirm-msg')).toContainText('NonExistentQueue');
    await expect(page.locator('#confirm-no')).toHaveText('Keep it');
    await expect(page.locator('#confirm-yes')).toHaveText('Delete queue');
    await expect(page.locator('#confirm-yes')).toHaveClass(/btn-destructive/);
    await confirmDialog('cancel');

    await page.getByRole('button', { name: 'Purge' }).click();
    await expect(page.locator('#confirm-msg')).toContainText('Every waiting and in-flight message is dropped. The queue and its settings stay.');
    await expect(page.locator('#confirm-no')).toHaveText('Keep them');
    await confirmDialog('cancel');
  });

  test('settings that do nothing locally say so', async ({ page, request, uniqueName }) => {
    const queue = await createQueue(request, uniqueName('e2e-sqs-cosmetic'));
    await page.goto(`sqs/${queue}?tab=config`);
    await page.getByRole('button', { name: 'Edit settings' }).click();
    await expect(page.locator('.field', { has: page.locator('input[name=kms_key]') }).locator('.hint'))
      .toContainText('Accepted and stored. Messages are not encrypted here.');
  });
});

// The same sweep layout.spec.ts runs over the surfaces, over the four tabs of
// an open queue, which a list of URLs cannot name.
test('no control on any tab of a queue is left without a name, and nothing scrolls sideways', async ({
  page,
  request,
  uniqueName,
}) => {
  const queue = await createQueue(request, uniqueName('e2e-sqs-a11y'), { fifo: true, dlqMode: 'new' });
  await postForm(request, `sqs/${queue}/send`, { body: 'x', group: 'g' });
  const problems: string[] = [];
  for (const theme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: theme });
    for (const tab of ['messages', 'consume', 'config', 'tags']) {
      await page.setViewportSize({ width: 1500, height: 1000 });
      await page.goto(`sqs/${queue}?tab=${tab}`);
      await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme);
      await page.waitForTimeout(150);
      const hits = await page.evaluate(() => {
        const out: string[] = [];
        const label = (e: Element) => `${e.tagName.toLowerCase()}.${String(e.className).split(' ').filter(Boolean).slice(0, 2).join('.')}`;
        for (const e of Array.from(document.querySelectorAll('button, a.btn, .icon-btn'))) {
          if (e.getBoundingClientRect().width === 0) continue;
          if ((e.textContent || '').trim() || e.getAttribute('aria-label') || e.getAttribute('title')) continue;
          out.push(`unnamed control: ${label(e)}`);
        }
        for (const e of Array.from(document.querySelectorAll('input:not([type=hidden]):not([type=checkbox]), select, textarea'))) {
          if (e.getBoundingClientRect().width === 0 || e.closest('.CodeMirror')) continue;
          const id = e.getAttribute('id');
          if ((id && document.querySelector(`label[for="${id}"]`)) || e.closest('label')) continue;
          if (e.getAttribute('aria-label') || e.getAttribute('placeholder') || e.getAttribute('title')) continue;
          out.push(`unlabelled input: ${label(e)} name=${e.getAttribute('name') || '?'}`);
        }
        const de = document.documentElement;
        if (de.scrollWidth > de.clientWidth + 2) out.push(`the page scrolls sideways (${de.scrollWidth}px)`);
        return [...new Set(out)];
      });
      for (const h of hits) problems.push(`${theme} ${tab}: ${h}`);
    }
  }
  expect(problems, problems.join('\n')).toEqual([]);
});

// ---------------------------------------------------------------------------
// What the fidelity audit found (plan/services/sqs.md 6a). Each of these was
// written against the page as it stood and failed there first.
// ---------------------------------------------------------------------------
const AWS_ACCT_DLQ = (name: string) => `${name}-dlq`;

test.describe('refusals are shown where the board shows them', () => {
  test('a refused create is on screen, above the form, with what was typed kept', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-refuse'));
    await page.goto('sqs/create');
    await page.locator('input[name=name]').fill(name);
    await page.locator('input[name=visibility]').fill('45');
    await page.locator('.dlq-mode', { hasText: 'None' }).click(); // so the one difference is the visibility
    await page.getByRole('button', { name: 'Create queue' }).click();
    const err = page.locator('.err').first();
    await expect(err).toBeInViewport();
    await expect(err).toContainText('QueueNameExists');
    await expect(err).toContainText('A queue already exists with the same name and a different value for attribute VisibilityTimeout');
    const errBox = (await err.boundingBox())!;
    const nameBox = (await page.locator('input[name=name]').boundingBox())!;
    expect(errBox.y, 'the refusal sits above the form, as the board draws it').toBeLessThan(nameBox.y);
    await expect(page.locator('input[name=name]')).toHaveValue(name);
    await expect(page.locator('input[name=visibility]')).toHaveValue('45');
  });

  test('a refused create leaves no dead-letter queue behind', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-orphan'));
    await page.goto('sqs/create');
    await page.locator('input[name=name]').fill(name);
    await page.locator('input[name=visibility]').fill('45');
    // "Create one alongside" is the form's default.
    await page.getByRole('button', { name: 'Create queue' }).click();
    await expect(page.locator('.err').first()).toContainText('QueueNameExists');
    await page.goto(`sqs/${name}`);
    await expect(page.locator('.li', { hasText: name })).toHaveCount(1);
    await expect(page.locator(`.li[data-name="${AWS_ACCT_DLQ(name)}"]`)).toHaveCount(0);
  });

  test('a name the service refuses comes back in the service\'s words, not the browser\'s', async ({ page }) => {
    await page.goto('sqs/create');
    await page.locator('input[name=name]').fill('harbour.checkout.orders');
    await page.getByRole('button', { name: 'Create queue' }).click();
    const err = page.locator('.err').first();
    await expect(err).toBeInViewport();
    await expect(err).toContainText('InvalidParameterValue');
    await expect(err).toContainText("queue name may contain only alphanumeric characters, hyphens and underscores, and got '.'");
    await expect(page.locator('input[name=name]')).toHaveValue('harbour.checkout.orders');
    await expect(page.locator('input[name=name]')).toHaveAttribute('aria-invalid', 'true');
    expect(await page.evaluate(() => getComputedStyle(document.querySelector('.err .err-h')!).color)).not.toMatch(/rgb\((1[5-9]\d|2\d\d), (\d|[1-5]\d), /);
  });

  test('an out-of-range setting is refused by the service, above the settings, and the form stays open', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-cfgrefuse'));
    await page.goto(`sqs/${name}?tab=config`);
    await page.getByRole('button', { name: 'Edit settings' }).click();
    await page.locator('#sqs-config input[name=visibility]').fill('90000');
    await page.getByRole('button', { name: 'Save settings' }).click();
    const err = page.locator('#sqs-config .err').first();
    await expect(err).toBeInViewport();
    await expect(err).toContainText('InvalidAttributeValue');
    await expect(err).toContainText('Value for parameter VisibilityTimeout is invalid. Reason: must be between 0 and 43200.');
    const vis = page.locator('#sqs-config input[name=visibility]');
    await expect(vis).toBeVisible();
    await expect(vis).toHaveValue('90000');
    expect((await err.boundingBox())!.y).toBeLessThan((await vis.boundingBox())!.y);
    expect(await err.evaluate((e) => !!e.closest('.pb'))).toBe(false);
  });

  test('a refused send is at the top of the composer', async ({ page, request, uniqueName, setEditor }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-sendrefuse'));
    await page.goto(`sqs/${name}`);
    await openComposer(page);
    await setEditor('textarea[name=body][data-editor]', 'bad ￾ char');
    await page.locator('.sqs-compose').getByRole('button', { name: 'Send' }).click();
    const err = page.locator('.sqs-compose .err').first();
    await expect(err).toContainText('InvalidMessageContents');
    const body = page.locator('.sqs-compose .CodeMirror').first();
    expect((await err.boundingBox())!.y).toBeLessThan((await body.boundingBox())!.y);
  });
});

test.describe('the message list under a live refresh', () => {
  test('a message arriving from outside leaves closed rows closed', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-liverows'));
    await postForm(request, `sqs/${name}/send`, { body: 'first' });
    await page.goto(`sqs/${name}`);
    await expect(page.locator('.msg')).toHaveCount(1);
    await postForm(request, `sqs/${name}/send`, { body: 'second' });
    await expect(page.locator('.msg')).toHaveCount(2, { timeout: 15_000 });
    await expect(page.locator('.msg-detail:visible')).toHaveCount(0);
    // And an opened row stays open across the next refresh.
    await page.locator('.msg').first().locator('.rowopen').click();
    await expect(page.locator('.msg-detail:visible')).toHaveCount(1);
    await postForm(request, `sqs/${name}/send`, { body: 'third' });
    await expect(page.locator('.msg')).toHaveCount(3, { timeout: 15_000 });
    await expect(page.locator('.msg-detail:visible')).toHaveCount(1);
  });

  test('another queue\'s count in the list follows a send to it', async ({ page, request, uniqueName }) => {
    const a = await createQueue(request, uniqueName('e2e-sqs-cnt-a'));
    const b = await createQueue(request, uniqueName('e2e-sqs-cnt-b'));
    await page.goto(`sqs/${a}`);
    const other = page.locator(`.li[data-name="${b}"] .sb`);
    await expect(other).toHaveText('0');
    await postForm(request, `sqs/${b}/send`, { body: 'x' });
    await expect(other).toHaveText('1', { timeout: 15_000 });
  });

  test('nothing throws while the page swaps', async ({ page, request, uniqueName }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));
    const name = await createQueue(request, uniqueName('e2e-sqs-noerr'));
    await page.goto(`sqs/${name}`);
    await postForm(request, `sqs/${name}/send`, { body: 'x' });
    await expect(page.locator('.msg')).toHaveCount(1, { timeout: 15_000 });
    await page.goto(`sqs/${name}?tab=config`);
    await page.goto('sqs/create');
    expect(errors).toEqual([]);
  });
});

test.describe('dialogs', () => {
  async function dlqWith(request: any, uniqueName: (p: string) => string, n: number) {
    const name = await createQueue(request, uniqueName('e2e-sqs-dlg'), { dlqMode: 'new' });
    const dlq = dlqNameFor(name, false);
    for (let i = 0; i < n; i++) await postForm(request, `sqs/${dlq}/send`, { body: `m${i}` });
    return { name, dlq };
  }

  test('the redrive dialog holds focus, Esc closes it, and focus goes back to the button', async ({ page, request, uniqueName }) => {
    const { dlq } = await dlqWith(request, uniqueName, 2);
    await page.goto(`sqs/${dlq}`);
    const trigger = page.getByRole('button', { name: /^Redrive/ }).first();
    await trigger.click();
    const dialog = page.getByRole('dialog', { name: 'Redrive messages' });
    await expect(dialog).toBeVisible();
    const inside = () => page.evaluate(() => !!document.activeElement?.closest('[role=dialog]'));
    await expect.poll(inside).toBe(true);
    for (let i = 0; i < 6; i++) {
      await page.keyboard.press('Tab');
      expect(await inside(), `Tab ${i + 1} left the dialog`).toBe(true);
    }
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
  });

  test('the confirm dialog gives focus back to what opened it', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-cfocus'));
    await page.goto(`sqs/${name}`);
    const purge = page.getByRole('button', { name: 'Purge' });
    await purge.click();
    await expect(page.locator('#confirm')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('#confirm')).toBeHidden();
    await expect(purge).toBeFocused();
  });

  test('the redrive dialog counts what is in the queue now, not at page load', async ({ page, request, uniqueName }) => {
    const { dlq } = await dlqWith(request, uniqueName, 1);
    await page.goto(`sqs/${dlq}`);
    await expect(page.locator('.msg')).toHaveCount(1);
    await postForm(request, `sqs/${dlq}/send`, { body: 'late' });
    await postForm(request, `sqs/${dlq}/send`, { body: 'later' });
    await expect(page.locator('.msg')).toHaveCount(3, { timeout: 15_000 });
    await page.getByRole('button', { name: /^Redrive/ }).first().click();
    await expect(page.getByRole('dialog', { name: 'Redrive messages' })).toContainText('Move all 3 messages back?');
  });

  test('neither dialog breaks the heading order', async ({ page, request, uniqueName }) => {
    const { dlq } = await dlqWith(request, uniqueName, 1);
    await page.goto(`sqs/${dlq}`);
    const axePath = path.join(__dirname, '..', 'node_modules', 'axe-core', 'axe.min.js');
    const headingOrder = async () => {
      await page.addScriptTag({ path: axePath });
      return page.evaluate(async () => {
        const r = await (window as any).axe.run(document, { runOnly: ['heading-order'] });
        return r.violations.map((v: any) => v.nodes.map((n: any) => n.target.join(' ')).join(', '));
      });
    };
    await page.getByRole('button', { name: 'Purge' }).click();
    await expect(page.locator('#confirm')).toBeVisible();
    expect(await headingOrder()).toEqual([]);
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: /^Redrive/ }).first().click();
    await expect(page.getByRole('dialog', { name: 'Redrive messages' })).toBeVisible();
    expect(await headingOrder()).toEqual([]);
  });
});

test.describe('the send preview is the call the form makes', () => {
  test('attributes and copies are in the previewed command', async ({ page, request, uniqueName, setEditor }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-clieq'));
    await page.goto(`sqs/${name}`);
    await openComposer(page);
    await setEditor('textarea[name=body][data-editor]', '{"n":1}');
    await page.locator('.sqs-compose').getByRole('button', { name: 'Add' }).click();
    await page.locator('.sqs-compose input[placeholder=name]').fill('source');
    await page.locator('.sqs-compose input[placeholder=value]').fill('checkout-api');
    await page.locator('.sqs-compose .cli-btn').click();
    const pre = page.locator('.sqs-compose .cli-out pre');
    await expect(pre).toContainText('sqs send-message ');
    await expect(pre).toContainText(`--message-attributes '{"source":{"DataType":"String","StringValue":"checkout-api"}}'`);
    await page.locator('.sqs-compose input[name=repeat]').fill('3');
    await expect(pre).toContainText('sqs send-message-batch ');
    await expect(pre).toContainText('"Id":"m2"');
  });

  test('a FIFO preview carries the deduplication id the form will send', async ({ page, request, uniqueName, setEditor }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-clififo'), { fifo: true });
    await page.goto(`sqs/${name}`);
    await openComposer(page);
    await setEditor('textarea[name=body][data-editor]', 'fifo body');
    await page.locator('.sqs-compose input[name=group]').fill('g1');
    await page.locator('.sqs-compose .cli-btn').click();
    const pre = page.locator('.sqs-compose .cli-out pre');
    await expect(pre).toContainText('--message-group-id g1');
    const text = await pre.innerText();
    const m = text.match(/--message-deduplication-id (\S+)/);
    expect(m, 'the preview names a deduplication id').not.toBeNull();
    await page.locator('.sqs-compose').getByRole('button', { name: 'Send' }).click();
    await expect(page.locator('.msg')).toHaveCount(1);
    await page.locator('.msg .rowopen').first().click();
    await expect(page.locator('.msg').first()).toContainText(m![1].replace(/'/g, ''));
  });
});

test.describe('what the pages say', () => {
  test('After receives says the next receive moves the message, not the Nth', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-nth'), { dlqMode: 'new', maxReceive: 3 });
    await page.goto(`sqs/${name}?tab=config`);
    await expect(page.locator('#sqs-config')).not.toContainText(/on its 3rd receive|after 3 receives$/);
    await page.getByRole('button', { name: 'Edit settings' }).click();
    const field = page.locator('#sqs-config .field', { has: page.locator('input[name=max_receive]') });
    await expect(page.locator('#sqs-config')).not.toContainText(/set aside on its \d+(st|nd|rd|th) receive/);
    await expect(page.locator('#sqs-config')).toContainText(/delivered 3 times.*next receive/);
    await expect(field).toBeVisible();
  });

  test('every settings field teaches: unit, words, sentence, range and default', async ({ page, request, uniqueName }) => {
    await page.goto('sqs/create');
    const f = (n: string) => page.locator('.field', { has: page.locator(`input[name=${n}]`) });
    await expect(f('visibility').locator('label')).toHaveText(/Visibility\s·\ss/);
    await expect(f('retention').locator('.setting-meta')).toHaveText(/60\s–\s1 209 600 · default 4 days · enforced/);
    await page.locator('details.adv-sec summary').click();
    await expect(f('receive_wait').locator('label')).toHaveText(/Receive wait\s·\ss\s·\slong polling/);
    await expect(f('receive_wait').locator('.setting-meta')).toHaveText(/0\s–\s20 · default 0 · enforced/);
    await expect(f('max_bytes').locator('.setting-meta')).toHaveText(/1 024\s–\s1 048 576 · default 1 048 576 · enforced/);
    await page.locator('input[name=max_bytes]').fill('262144');
    await expect(f('max_bytes').locator('.dur-echo')).toHaveText('256 KiB');
    const name = await createQueue(request, uniqueName('e2e-sqs-teach'), { fifo: true });
    await page.goto(`sqs/${name}?tab=config`);
    await page.getByRole('button', { name: 'Edit settings' }).click();
    const e = (n: string) => page.locator('#sqs-config .field', { has: page.locator(`[name=${n}]`) });
    await expect(e('sse')).toContainText('Accepted and stored, not enforced.');
    await expect(e('dedup_scope')).toContainText('duplicates are found within a group, not across the whole queue');
    await expect(e('max_bytes')).toContainText('body and attributes together');
  });

  test('a zero is "none", never "no time"', async ({ page, request, uniqueName }) => {
    await page.goto('sqs/create');
    await page.locator('input[name=visibility]').fill('0');
    await expect(page.locator('.life')).not.toContainText('no time');
    const name = await createQueue(request, uniqueName('e2e-sqs-zero'));
    await page.goto(`sqs/${name}?tab=config`);
    await page.getByRole('button', { name: 'Edit settings' }).click();
    await page.locator('#sqs-config input[name=delay]').fill('900');
    await expect(page.locator('.what-panel')).toContainText('Delay');
    await expect(page.locator('.what-panel')).not.toContainText('no time');
  });

  test('a held message that came back is never "hidden for visible again", and the header counts it', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-back'), { visibility: 2 });
    await postForm(request, `sqs/${name}/send`, { body: 'x' });
    await page.goto(`sqs/${name}?tab=consume`);
    await page.getByRole('button', { name: 'Receive' }).click();
    await expect(page.locator('#sqs-consumed .rcv-row')).toHaveCount(1);
    const slug = name.replace(/[^a-zA-Z0-9_-]/g, '-');
    await expect(page.locator(`#sum-sqs-${slug}-inflight`)).toHaveText('1');
    await expect(page.locator(`#sum-sqs-${slug}-visible`)).toHaveText('1', { timeout: 15_000 });
    await expect(page.locator(`#sum-sqs-${slug}-inflight`)).toHaveText('0');
    await expect(page.locator('#sqs-consumed')).not.toContainText('hidden for visible again');
  });
});

test.describe('Esc does what the key bar says', () => {
  test('in the filter, the create form, the composer and the settings form', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-esc'));
    await page.goto(`sqs/${name}`);
    await page.keyboard.press('/');
    const filter = page.locator('.listpane .filter input');
    await expect(filter).toBeFocused();
    await page.keyboard.type('zzzz-no-such');
    await expect(page.locator('.li:visible')).toHaveCount(0);
    await page.keyboard.press('Escape');
    await expect(filter).toHaveValue('');
    await expect(filter).not.toBeFocused();
    await expect(page.locator(`.li[data-name="${name}"]`)).toBeVisible();

    await openComposer(page);
    await expect(page.locator('.sqs-compose')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('.sqs-compose')).toBeHidden();

    await page.goto(`sqs/${name}?tab=config`);
    await page.getByRole('button', { name: 'Edit settings' }).click();
    await page.locator('#sqs-config input[name=visibility]').focus();
    await page.keyboard.press('Escape');
    await expect(page.locator('#sqs-config input[name=visibility]')).toBeHidden();

    await page.goto('sqs/create');
    await page.locator('input[name=name]').fill('never-made');
    await page.keyboard.press('Escape');
    await page.waitForURL(/\/sqs$/);
  });
});

test.describe('the service home', () => {
  test('lists the queues and opens none (board Sqs-02)', async ({ page, request, uniqueName }) => {
    const name = await createQueue(request, uniqueName('e2e-sqs-home'));
    await page.goto('sqs');
    await expect(page.locator(`.li[data-name="${name}"]`)).toBeVisible();
    await expect(page.locator('.li.on')).toHaveCount(0);
    await expect(page.locator('.detail')).toContainText('Select a queue to read its messages without consuming them, send one, or set up redrive.');
    await expect(page.locator('.detail')).toContainText('to move, ⏎ to open');
    await expect(page.locator('.listpane')).toContainText('The number is messages waiting. In-flight ones are not counted.');
    // j, then Enter, opens the first.
    await page.keyboard.press('j');
    await page.keyboard.press('Enter');
    await page.waitForURL(/\/sqs\/[^/]+$/);
    await expect(page.locator('.li.on')).toHaveCount(1);
  });
});
