import { test, expect } from '../fixtures/console';
import { postForm } from '../fixtures/api';

// SSM Parameter Store: path-tree grouping in the list pane, SecureString
// client-side masking (value_workspace, shared with Secrets Manager), new
// versions + diff, labels, and delete. Every parameter name is path-style
// (`/e2e-ssm-{suffix}/leaf`) so siblings created by a test share one folder
// in the ssm-group tree without colliding with other specs/agents running
// against the same server.

test.describe('path tree', () => {
  test('groups siblings under one folder; filter gates the whole group and each leaf', async ({
    page,
    request,
    uniqueName,
  }) => {
    const dir = '/' + uniqueName('e2e-ssm');
    const plainName = `${dir}/plain`;
    const secretName = `${dir}/secret`;
    await postForm(request, 'ssm/create', { name: plainName, type: 'String', value: 'hello-plain' });
    await postForm(request, 'ssm/create', { name: secretName, type: 'SecureString', value: 'hello-secret' });

    await page.goto('ssm');
    const group = page.locator('.ssm-group', { hasText: dir });
    await expect(group).toBeVisible();
    const plainLeaf = group.locator('.ssm-leaf', { hasText: 'plain' });
    const secretLeaf = group.locator('.ssm-leaf', { hasText: 'secret' });
    await expect(plainLeaf).toBeVisible();
    await expect(secretLeaf).toBeVisible();

    // Filtering on a fragment that only the "plain" leaf's full name contains:
    // the group stays visible (one of its names matches) but the "secret"
    // leaf's own x-show gate hides just that leaf — a per-leaf gate nested
    // inside a whole-group gate, not a single flat filter.
    const filter = page.locator('.listpane .filter input');
    await filter.fill(`${dir}/plain`);
    await expect(group).toBeVisible();
    await expect(plainLeaf).toBeVisible();
    await expect(secretLeaf).toBeHidden();

    // Filtering on something neither leaf's name contains hides the whole
    // folder header too, not just the (already-hidden) leaves.
    await filter.fill('zzz-no-such-parameter-anywhere');
    await expect(group).toBeHidden();
  });
});

test.describe('value masking', () => {
  test('SecureString is masked until revealed; String shows its value directly', async ({
    page,
    request,
    uniqueName,
  }) => {
    const dir = '/' + uniqueName('e2e-ssm');
    const plainName = `${dir}/view-plain`;
    const secretName = `${dir}/view-secret`;
    const plainValue = 'plaintext-config-value';
    const secretValue = 'super-secret-passphrase-42';
    await postForm(request, 'ssm/create', { name: plainName, type: 'String', value: plainValue });
    await postForm(request, 'ssm/create', { name: secretName, type: 'SecureString', value: secretValue });

    // Plain String: no mask/reveal UI at all, value renders directly.
    await page.goto('ssm/param?name=' + encodeURIComponent(plainName));
    await expect(page.locator('.ws-view')).toHaveText(plainValue);
    await expect(page.getByRole('button', { name: 'Reveal' })).toHaveCount(0);
    await expect(page.locator('.ws-view .mk')).toHaveCount(0);

    // SecureString: masked span shown by default. Reveal is client-side — the
    // real value is already in the DOM (inside the x-show="reveal" span),
    // just hidden via Alpine's inline display:none, not absent. Assert that
    // precisely: hidden-but-present before the click, visible after.
    await page.goto('ssm/param?name=' + encodeURIComponent(secretName));
    const maskedSpan = page.locator('.ws-view .mk');
    const revealSpan = page.locator('.ws-view span[x-show="reveal"]');
    await expect(maskedSpan).toBeVisible();
    await expect(maskedSpan).not.toHaveText(secretValue);
    await expect(revealSpan).toBeHidden();
    await expect(revealSpan).toHaveText(secretValue);

    await page.getByRole('button', { name: 'Reveal' }).click();
    await expect(revealSpan).toBeVisible();
    await expect(revealSpan).toHaveText(secretValue);
    await expect(maskedSpan).toBeHidden();
  });
});

test.describe('versions', () => {
  test('a new value creates v2, the Versions tab lists both, and diff highlights the change', async ({
    page,
    request,
    uniqueName,
    setEditor,
    waitForToast,
  }) => {
    const dir = '/' + uniqueName('e2e-ssm');
    const name = `${dir}/versioned`;
    await postForm(request, 'ssm/create', { name, type: 'String', value: 'v1-value' });

    await page.goto('ssm/param?name=' + encodeURIComponent(name) + '&tab=edit');
    await setEditor('textarea[data-editor]', 'v2-value');
    await page.getByRole('button', { name: 'Save as v2' }).click();
    const t = await waitForToast();
    expect(t).toMatch(/New version saved/);

    await expect(page.locator('.det-title .badge', { hasText: 'v2' })).toBeVisible();

    await page.getByRole('link', { name: 'Versions', exact: true }).click();
    await expect(page.locator('.tbl tbody tr')).toHaveCount(2);

    await page
      .locator('.tbl tbody tr', { hasText: 'v1' })
      .getByRole('button', { name: 'Diff vs current' })
      .click();
    const diffBox = page.locator('#version-diff .diffbox');
    await expect(diffBox.locator('.dl.del')).toContainText('v1-value');
    await expect(diffBox.locator('.dl.add')).toContainText('v2-value');
  });
});

test.describe('labels', () => {
  test('labeling a version shows the label in that row’s Labels column', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const dir = '/' + uniqueName('e2e-ssm');
    const name = `${dir}/labeled`;
    await postForm(request, 'ssm/create', { name, type: 'String', value: 'v1-value' });

    await page.goto('ssm/param?name=' + encodeURIComponent(name) + '&tab=versions');
    const row = page.locator('.tbl tbody tr', { hasText: 'v1' });
    await row.locator('input[name="label"]').fill('e2e-prod');
    await row.getByRole('button', { name: 'Attach label' }).click();

    const toastMsg = await waitForToast();
    expect(toastMsg).toMatch(/Label\s*.e2e-prod.\s*applied/);

    await expect(
      page.locator('.tbl tbody tr', { hasText: 'v1' }).locator('.badge.type', { hasText: 'e2e-prod' })
    ).toBeVisible();
  });
});

test.describe('delete', () => {
  test('delete requires confirmation and removes the parameter', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    const dir = '/' + uniqueName('e2e-ssm');
    const name = `${dir}/deleteme`;
    await postForm(request, 'ssm/create', { name, type: 'String', value: 'to-be-deleted' });

    await page.goto('ssm/param?name=' + encodeURIComponent(name));
    await page.locator('.acts').getByRole('button', { name: 'Delete' }).click();
    await expect(page.locator('#confirm-msg')).toContainText(name);
    await confirmDialog('accept');

    await page.waitForURL(/\/ssm$/);
    await expect(page.locator('.ssm-group', { hasText: dir })).toHaveCount(0);
  });
});

// ---- Route coverage: the create page, the path tools, and unlabel ----

test.describe('create', () => {
  test('New opens the create page, and the form creates the parameter', async ({
    page,
    uniqueName,
    setEditor,
    waitForToast,
  }) => {
    const dir = '/' + uniqueName('e2e-ssm');
    const name = `${dir}/created`;
    const value = `db.${dir.slice(1)}.local:5432`;

    await page.goto('ssm');
    await page.locator('.listpane a.new-link').click();
    await page.waitForURL(/\/ssm\/create$/);
    await expect(page.getByText('Create parameter').first()).toBeVisible();

    await page.locator('input[name="name"]').fill(name);
    await page.locator('select[name="type"]').selectOption('StringList');
    await setEditor('textarea[name="value"]', value);
    await page.getByRole('button', { name: 'Create parameter' }).click();

    expect(await waitForToast()).toContain(`Parameter “${name}” created`);
    await page.waitForURL(/\/ssm\/param\?name=/);
    await expect(page.locator('.det-title')).toContainText(name);
    await expect(page.locator('.det-title .badge.type')).toHaveText('StringList');
    await expect(page.locator('.ws-view')).toHaveText(value);
    await expect(page.locator('.ssm-group', { hasText: dir })).toBeVisible();
  });
});

test.describe('path tools', () => {
  test('a path query lists everything under it, masking SecureStrings', async ({
    page,
    request,
    uniqueName,
  }) => {
    const dir = '/' + uniqueName('e2e-ssm');
    await postForm(request, 'ssm/create', { name: `${dir}/a/host`, type: 'String', value: 'host-value' });
    await postForm(request, 'ssm/create', { name: `${dir}/b/pass`, type: 'SecureString', value: 'pass-value' });

    await page.goto('ssm');
    await page.locator('input[name="path"]').fill(dir);
    await page.getByRole('button', { name: 'List everything under it' }).click();

    const out = page.locator('#ssm-path-out');
    await expect(out.locator('.panel-h')).toContainText(dir);
    await expect(out.locator('.panel-h')).toContainText('2 parameters');
    // Recursive: both nested leaves come back.
    const host = out.locator('tbody tr', { hasText: `${dir}/a/host` });
    const pass = out.locator('tbody tr', { hasText: `${dir}/b/pass` });
    await expect(host).toContainText('host-value');
    await expect(pass).toContainText('SecureString');
    await expect(pass).not.toContainText('pass-value');
  });

  test('a path with nothing under it says so', async ({ page, request, uniqueName }) => {
    // The home pane's path tools only render once Parameter Store has any
    // parameter at all, so make sure one exists.
    const dir = '/' + uniqueName('e2e-ssm');
    await postForm(request, 'ssm/create', { name: `${dir}/x`, type: 'String', value: 'x' });
    const empty = '/' + uniqueName('e2e-ssm-none');

    await page.goto('ssm');
    await page.locator('input[name="path"]').fill(empty);
    await page.getByRole('button', { name: 'List everything under it' }).click();
    await expect(page.locator('#ssm-path-out')).toContainText(`Nothing under ${empty}`);
  });

  test('delete everything under a path asks first, then removes the whole folder', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    waitForToast,
  }) => {
    const dir = '/' + uniqueName('e2e-ssm');
    const keep = '/' + uniqueName('e2e-ssm-keep');
    await postForm(request, 'ssm/create', { name: `${dir}/one`, type: 'String', value: '1' });
    await postForm(request, 'ssm/create', { name: `${dir}/two`, type: 'String', value: '2' });
    await postForm(request, 'ssm/create', { name: `${keep}/one`, type: 'String', value: 'kept' });

    await page.goto('ssm');
    await page.locator('input[name="path"]').fill(dir);
    await page.getByRole('button', { name: 'List everything under it' }).click();
    await page.getByRole('button', { name: `Delete everything under ${dir}` }).click();
    await expect(page.locator('#confirm-msg')).toContainText(`Delete all 2 parameters under ${dir}`);
    await confirmDialog('accept');

    expect(await waitForToast()).toContain('2 parameters deleted');
    await expect(page.locator('.ssm-group', { hasText: dir })).toHaveCount(0);
    // A sibling path is untouched.
    await expect(page.locator('.ssm-group', { hasText: keep })).toBeVisible();
  });
});

test.describe('unlabel', () => {
  test('the × on a label removes it from that version', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const dir = '/' + uniqueName('e2e-ssm');
    const name = `${dir}/unlabeled`;
    await postForm(request, 'ssm/create', { name, type: 'String', value: 'v1-value' });
    await postForm(request, 'ssm/label', { name, version: 1, label: 'e2e-keep' });
    await postForm(request, 'ssm/label', { name, version: 1, label: 'e2e-drop' });

    await page.goto('ssm/param?name=' + encodeURIComponent(name) + '&tab=versions');
    const row = page.locator('.tbl tbody tr', { hasText: 'v1' });
    const drop = row.locator('.badge.type', { hasText: 'e2e-drop' });
    await expect(drop).toBeVisible();

    // The × has text content, so its accessible name is "×", not its title.
    await drop.getByTitle('Remove this label').click();
    expect(await waitForToast()).toMatch(/Label\s*.e2e-drop.\s*removed/);

    const after = page.locator('.tbl tbody tr', { hasText: 'v1' });
    await expect(after.locator('.badge.type', { hasText: 'e2e-drop' })).toHaveCount(0);
    await expect(after.locator('.badge.type', { hasText: 'e2e-keep' })).toBeVisible();

    // Persisted service-side, not just dropped from the swapped fragment.
    await page.reload();
    await expect(
      page.locator('.tbl tbody tr', { hasText: 'v1' }).locator('.badge.type', { hasText: 'e2e-drop' })
    ).toHaveCount(0);
  });
});
