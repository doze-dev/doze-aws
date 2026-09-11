import type { Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { postForm, createTable } from '../fixtures/api';

// Toasts stack and self-remove after ~3.2s; waitForToast() grabs whichever is
// `.last()` in the DOM at the moment it's called. Two toast-producing actions
// fired back to back can race: the previous toast may still be visible when
// we ask for the next one, so we'd read stale text. Draining first (bounded
// by the toast's own auto-remove timeout) guarantees the next waitForToast()
// call observes a fresh one.
async function drainToasts(page: Page) {
  await expect(page.locator('.toast:not(.err)')).toHaveCount(0, { timeout: 5000 });
}

// DynamoDB console coverage: table creation (partition+sort key, GSI, TTL) via
// the real UI form, item CRUD through the plain-JSON editor and the item
// drawer, the Scan/Query/PartiQL explorer modes, cursor-paged "load more",
// and post-create GSI/TTL editing on the Details tab.

test.describe('create table', () => {
  test('via the UI form with sort key, GSI, and TTL', async ({ page, uniqueName }) => {
    const table = uniqueName('e2e-ddb-create');
    await page.goto('ddb/create');

    await page.locator('input[name=name]').fill(table);
    await page.locator('input[name=hash_key]').fill('pk');

    // Enable the sort-key fields (hidden behind an Alpine switch until toggled).
    await page.locator('.opt-row', { hasText: 'Sort key' }).locator('.switch').click();
    await page.locator('input[name=range_key]').fill('sk');

    // Add one GSI row.
    await page.getByRole('button', { name: 'Add index' }).click();
    const gsiRow = page.locator('.gsi-row');
    await gsiRow.locator('input[name=gsi_name]').fill('by-name');
    await gsiRow.locator('input[name=gsi_hash]').fill('name');

    // Enable TTL.
    await page.locator('.opt-row', { hasText: 'Auto-expire items' }).locator('.switch').click();
    await page.locator('input[name=ttl_attr]').fill('expiresAt');

    await page.getByRole('button', { name: 'Create table' }).click();
    await page.waitForURL(new RegExp(`/ddb/${table}(\\?|$)`));
    await expect(page.locator('#flashbar')).toContainText('created');

    await page.goto(`ddb/${table}?tab=details`);
    const details = page.locator('#ddb-details');
    await expect(details).toContainText('sk (S)');
    await expect(details).toContainText('by-name');
    await expect(details).toContainText('name (S)');
    await expect(details).toContainText('expiresAt');
  });
});

test.describe('items', () => {
  // The add-item editor opens prefilled with the table's key schema, and a
  // CodeMirror built inside a hidden dialog measures its gutter as zero — so
  // without a refresh on reveal the line numbers render on top of the first
  // characters of every line. That went unseen for as long as the editor
  // opened empty, because an empty editor has nothing to overlap.
  //
  // Asserting geometry rather than text is the point: the content was always
  // correct, it was the layout that was wrong.
  test('opens prefilled with the key schema, and the gutter does not sit on the text', async ({
    page,
    request,
    uniqueName,
  }) => {
    const table = await createTable(request, uniqueName('e2e-ddb-gutter'), {
      rangeKey: 'ts',
      rangeType: 'N',
    });
    await page.goto(`ddb/${table}`);
    await page.locator('.acts').getByRole('button', { name: 'Add item' }).click();

    const geo = await page.evaluate(() => {
      const ta = document.querySelector('#ddb-item-editor') as HTMLTextAreaElement & {
        __cm?: any;
      };
      const cm = ta.__cm;
      const wrap = cm.getWrapperElement() as HTMLElement;
      const gutter = wrap.querySelector('.CodeMirror-gutters') as HTMLElement;
      const lines = wrap.querySelector('.CodeMirror-lines') as HTMLElement;
      return {
        value: cm.getValue(),
        gutterWidth: Math.round(gutter.getBoundingClientRect().width),
        textOffset: Math.round(
          lines.getBoundingClientRect().left - gutter.getBoundingClientRect().left
        ),
      };
    });

    // A string key is quoted and a numeric key is not: DynamoDB refuses a
    // number sent as a string, so the types are the useful half of the prefill.
    expect(geo.value).toBe('{\n  "pk": "",\n  "ts": 0\n}');
    // The text has to start past the gutter, not on top of it.
    expect(geo.gutterWidth).toBeGreaterThan(20);
    expect(geo.textOffset).toBeGreaterThanOrEqual(geo.gutterWidth - 1);
  });

  test('add via JSON editor, view/edit/delete via the drawer', async ({
    page,
    request,
    uniqueName,
    setEditor,
    waitForToast,
    confirmDialog,
  }) => {
    const table = await createTable(request, uniqueName('e2e-ddb-item'), { rangeKey: 'sk' });
    await page.goto(`ddb/${table}`);

    // Add an item via the plain-JSON editor.
    await page.locator('.acts').getByRole('button', { name: 'Add item' }).click();
    await setEditor('#ddb-item-editor', JSON.stringify({ pk: 'a', sk: '1', name: 'hello' }));
    await page.getByRole('button', { name: 'Save item' }).click();
    const savedToast = await waitForToast();
    expect(savedToast).toMatch(/saved/i);
    await drainToasts(page);

    const row = page.locator('#ddb-items tbody tr').first();
    await expect(row).toContainText('a');
    await expect(row.locator('td.trunc')).toContainText('hello');

    // Open the item drawer and check its contents.
    await row.locator('td.trunc').click();
    const drawerPre = page.locator('#drawer-inner pre');
    await expect(drawerPre).toContainText('"name": "hello"');
    await expect(drawerPre).toContainText('"pk": "a"');
    await expect(drawerPre).toContainText('"sk": "1"');

    // Edit from the drawer: it prefills the put dialog via dozeEditor.set in
    // an Alpine $nextTick — wait for that prefill to land before overwriting
    // it, or our overwrite can race the app's own populate and get clobbered.
    await page.locator('#dw-edit').click();
    await expect(page.locator('#ddb-item-editor')).toHaveValue(/"name": "hello"/);
    await setEditor('#ddb-item-editor', JSON.stringify({ pk: 'a', sk: '1', name: 'hello2' }));
    await page.getByRole('button', { name: 'Save item' }).click();
    const editedToast = await waitForToast();
    expect(editedToast).toMatch(/saved/i);
    await drainToasts(page);
    await expect(page.locator('#ddb-items td.trunc')).toContainText('hello2');

    // Delete the item.
    await page.getByRole('button', { name: 'Delete item' }).click();
    await confirmDialog('accept');
    const deletedToast = await waitForToast();
    expect(deletedToast).toMatch(/deleted/i);
    await expect(page.locator('#ddb-items')).toContainText('No items yet');
  });
});

test.describe('explorer modes', () => {
  test('Scan, Query (via GSI), and PartiQL all return the right rows', async ({
    page,
    request,
    uniqueName,
    setEditor,
  }) => {
    const table = uniqueName('e2e-ddb-explore');
    // Arrange directly via the create route (bypassing the limited createTable
    // helper) to get a GSI without going through the UI form.
    await postForm(request, 'ddb/create', {
      name: table,
      hash_key: 'pk',
      hash_type: 'S',
      range_key: 'sk',
      range_type: 'S',
      gsi_name: 'by-name',
      gsi_hash: 'name',
      gsi_hash_type: 'S',
    });

    const items = [
      { pk: 'cust-1', sk: 'o1', name: 'alice' },
      { pk: 'cust-2', sk: 'o1', name: 'bob' },
      { pk: 'cust-3', sk: 'o1', name: 'carol' },
    ];
    for (const item of items) {
      await postForm(request, `ddb/${table}/put`, { item: JSON.stringify(item) });
    }

    await page.goto(`ddb/${table}`);

    // Scan (default): all 3 items.
    await expect(page.locator('#ddb-items tbody tr:not(.load-more)')).toHaveCount(3);
    await expect(page.locator('#ddb-items')).toContainText('cust-1');
    await expect(page.locator('#ddb-items')).toContainText('cust-2');
    await expect(page.locator('#ddb-items')).toContainText('cust-3');

    // Query via the GSI: only the item whose name === 'alice'.
    await page.locator('.seg button', { hasText: 'Query' }).click();
    await page.locator('.explorer-form:visible select[name=index]').selectOption('by-name');
    await page.locator('.explorer-form:visible input[name=pk]').fill('alice');
    await page.locator('.explorer-form:visible button[type=submit]').click();
    await expect(page.locator('#ddb-items tbody tr:not(.load-more)')).toHaveCount(1);
    await expect(page.locator('#ddb-items')).toContainText('cust-1');
    await expect(page.locator('#ddb-items')).not.toContainText('cust-2');

    // PartiQL: select everything back out.
    await page.locator('.seg button', { hasText: 'PartiQL' }).click();
    await setEditor('textarea[data-editor="sql"]', `SELECT * FROM "${table}"`);
    await page.locator('.explorer-form:visible button[type=submit]').click();
    await expect(page.locator('#ddb-items tbody tr:not(.load-more)')).toHaveCount(3);
  });
});

test.describe('point reads, batch ops, and in-place updates', () => {
  test('Get mode, update expression, and bulk delete (batch + transactional)', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    waitForToast,
  }) => {
    const table = uniqueName('e2e-ddb-batch');
    await postForm(request, 'ddb/create', {
      name: table,
      hash_key: 'pk',
      hash_type: 'S',
      range_key: 'sk',
      range_type: 'S',
    });
    for (let i = 1; i <= 4; i++) {
      await postForm(request, `ddb/${table}/put`, {
        item: JSON.stringify({ pk: `u-${i}`, sk: `s-${i}`, logins: i }),
      });
    }
    await page.goto(`ddb/${table}`);
    const rows = page.locator('#ddb-items tbody tr:not(.load-more)');
    await expect(rows).toHaveCount(4);

    // Get mode: the point read needs both key parts and returns exactly one.
    await page.locator('.seg button', { hasText: 'Get' }).click();
    const getForm = page.locator('.explorer-form:visible');
    await getForm.locator('input[name=pk]').fill('u-2');
    await getForm.locator('input[name=sk]').fill('s-2');
    await getForm.locator('button[type=submit]').click();
    await expect(rows).toHaveCount(1);
    await expect(page.locator('#ddb-items')).toContainText('u-2');

    // Update expression via the drawer: SET arithmetic edits in place.
    await rows.first().click();
    await page.locator('#dw-upd').click();
    const dlg = page.locator('.overlay[aria-label="Update item"]');
    await expect(dlg).toBeVisible();
    await expect(dlg.locator('pre')).toContainText('u-2'); // key prefilled
    await dlg.locator('input[name=expr]').fill('SET logins = logins + :n');
    // The :binding sprouts a typed value row, same contract as filter values.
    await dlg.locator('.fv-row select').selectOption('N');
    await dlg.locator('.fv-row input').fill('100');
    await dlg.getByRole('button', { name: 'Update item' }).click();
    const updToast = await waitForToast();
    expect(updToast).toMatch(/Item updated/);
    // The table re-scans after the update; the new value is visible.
    await expect(page.locator('#ddb-items')).toContainText('102');

    // Multi-select: the bulk bar appears only once something is picked.
    const bar = page.locator('.bulkbar');
    await expect(bar).toBeHidden();
    await page.locator('tbody input.rowck').nth(0).click();
    await page.locator('tbody input.rowck').nth(1).click();
    await expect(bar).toBeVisible();
    await expect(bar.locator('.bb-n')).toContainText('2');

    // Plain bulk delete = BatchWriteItem.
    await drainToasts(page);
    await bar.getByRole('button', { name: 'Delete selected' }).click();
    await confirmDialog('accept');
    const batchToast = await waitForToast();
    expect(batchToast).toMatch(/2 items deleted/);
    await expect(rows).toHaveCount(2);

    // Transactional bulk delete = TransactWriteItems (all-or-nothing).
    await page.locator('thead .ck-col input').click(); // select-all
    await expect(bar.locator('.bb-n')).toContainText('2');
    await bar.locator('label.bb-txn input').click();
    await drainToasts(page);
    await bar.getByRole('button', { name: 'Delete selected' }).click();
    await confirmDialog('accept');
    const txToast = await waitForToast();
    expect(txToast).toMatch(/2 items deleted/);
    await expect(page.locator('#ddb-items')).toContainText('No items yet');
  });
});

test.describe('pagination', () => {
  test('"load more" appends rows instead of resetting the page', async ({
    page,
    request,
    uniqueName,
  }) => {
    const table = await createTable(request, uniqueName('e2e-ddb-page'));
    for (let i = 0; i < 5; i++) {
      await postForm(request, `ddb/${table}/put`, {
        item: JSON.stringify({ pk: `item-${i}`, n: i }),
      });
    }

    await page.goto(`ddb/${table}`);
    // Request a small page size so 5 items span more than one page.
    await page.locator('.explorer-form:visible input[name=limit]').fill('2');
    await page.locator('.explorer-form:visible button[type=submit]').click();

    await expect(page.locator('#ddb-items tbody tr:not(.load-more)')).toHaveCount(2);
    await expect(page.locator('tr.load-more')).toBeVisible();

    await page.locator('tr.load-more button').click();

    // The follow-up request has no page-size cap of its own, so it pulls in
    // every remaining item in one shot — the table grows, it doesn't reset.
    await expect(page.locator('#ddb-items tbody tr:not(.load-more)')).toHaveCount(5);
    await expect(page.locator('tr.load-more')).toHaveCount(0);
  });
});

test.describe('post-create editing', () => {
  test('add a second GSI, delete one, and toggle TTL off/on', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const table = uniqueName('e2e-ddb-edit');
    await postForm(request, 'ddb/create', {
      name: table,
      hash_key: 'pk',
      hash_type: 'S',
      gsi_name: 'by-name',
      gsi_hash: 'name',
      gsi_hash_type: 'S',
    });

    await page.goto(`ddb/${table}?tab=details`);
    const details = page.locator('#ddb-details');
    await expect(details).toContainText('by-name');

    // Add a second GSI.
    await page.locator('input[name=gsi_name]').fill('by-count');
    await page.locator('input[name=gsi_hash]').fill('count');
    await page.getByRole('button', { name: 'Add index' }).click();
    const addedToast = await waitForToast();
    expect(addedToast).toMatch(/added/i);
    await drainToasts(page);
    await expect(details).toContainText('by-count');
    await expect(details).toContainText('by-name');

    // Delete the first GSI.
    await page
      .locator('tr', { hasText: 'by-name' })
      .getByRole('button', { name: 'Delete index' })
      .click();
    await confirmDialog('accept');
    const removedToast = await waitForToast();
    expect(removedToast).toMatch(/removed/i);
    await drainToasts(page);
    await expect(details).not.toContainText('by-name');
    await expect(details).toContainText('by-count');

    // Enable TTL.
    await page.locator('input[name=attr]').fill('expiresAt');
    await page.getByRole('button', { name: 'Enable TTL' }).click();
    const enabledToast = await waitForToast();
    expect(enabledToast).toMatch(/updated/i);
    await drainToasts(page);
    await expect(details).toContainText('expiresAt');

    // Disable TTL.
    await page.getByRole('button', { name: 'Disable' }).click();
    await confirmDialog('accept');
    const disabledToast = await waitForToast();
    expect(disabledToast).toMatch(/updated/i);
    await expect(page.locator('input[name=attr]')).toBeVisible();
  });
});
