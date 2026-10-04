import { test, expect } from '../fixtures/console';
import type { Page } from '@playwright/test';
import { createKey } from '../fixtures/api';

// waitForToast's `.last()` locator is only race-free when at most one toast
// is alive at a time — if two toast-producing actions fire back-to-back
// (no navigation between them), the previous toast can still be visible
// when we look for the next one, and `.last()` happily reports it as
// "visible" before the real new toast has even been appended. Toasts
// self-remove after 7s (ok) / 10s (err) per internal/console/static/shell.js, so
// draining the current one first makes the next waitForToast() call
// unambiguous.
async function waitToastGone(page: Page) {
  await page.evaluate(() => document.querySelectorAll('.toast:not(.err)').forEach((e) => e.remove()));
}

// KMS console coverage: the crypto playground is usage-gated (a key gets
// exactly the operations its Usage allows), so this spec exercises three
// keys — ENCRYPT_DECRYPT, SIGN_VERIFY, GENERATE_VERIFY_MAC — plus the
// shared settings surface (enable/disable, rotation, aliases, deletion).

test.describe('ENCRYPT_DECRYPT key', () => {
  test('encrypt/decrypt round trip, settings, aliases, and deletion lifecycle', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const alias = uniqueName('e2e-kms');
    const keyId = await createKey(request, {
      spec: 'SYMMETRIC_DEFAULT',
      usage: 'ENCRYPT_DECRYPT',
      alias,
    });

    await page.goto(`kms/${keyId}`);
    // Note: the detail page's title always shows the raw key ID — DescribeKey
    // (unlike ListKeys) never populates the singular Key.Alias field used by
    // the title, only the plural Key.Aliases used by the Aliases panel below.
    await expect(page.locator('.det-title')).toContainText(keyId);
    await expect(page.locator('.sub-row', { hasText: alias })).toBeVisible();

    // --- Encrypt -> decrypt round trip via the "Decrypt this ->" button ---
    const plaintext = 'hello, doze';
    await page
      .locator('form:has(textarea[name="plaintext"])')
      .locator('textarea[name="plaintext"]')
      .fill(plaintext);
    await page
      .locator('form:has(textarea[name="plaintext"])')
      .getByRole('button', { name: 'Encrypt' })
      .click();

    const cryptoOut = page.locator('#kms-crypto-out');
    await expect(cryptoOut.locator('pre')).toBeVisible();
    const ciphertext = (await cryptoOut.locator('pre').textContent())?.trim();
    expect(ciphertext).toBeTruthy();
    expect(ciphertext).not.toBe(plaintext);

    await cryptoOut.getByRole('button', { name: 'Decrypt this →' }).click();
    // Same div, innerHTML-swapped — wait for the label to flip to Plaintext.
    await expect(cryptoOut).toContainText('Plaintext');
    const decrypted = (await cryptoOut.locator('pre').textContent())?.trim();
    expect(decrypted).toBe(plaintext);

    // --- Enable / disable toggle ---
    const badge = page.locator('.det-title .badge');
    await expect(badge).toHaveText('Enabled');
    const enabledSwitch = page.locator('.opt-row', { hasText: 'Enabled' }).locator('label.switch');
    await enabledSwitch.click();
    const disableToast = await waitForToast();
    expect(disableToast).toMatch(/Key disabled/);
    await expect(badge).toHaveText('Disabled');
    await waitToastGone(page);

    await enabledSwitch.click();
    const enableToast = await waitForToast();
    expect(enableToast).toMatch(/Key enabled/);
    await expect(badge).toHaveText('Enabled');
    await waitToastGone(page);

    // --- Automatic rotation toggle + rotate now ---
    const rotationRow = page.locator('.opt-row', { hasText: 'Automatic rotation' });
    const rotationInput = rotationRow.locator('input[type=checkbox]');
    await expect(rotationInput).not.toBeChecked();
    await rotationRow.locator('label.switch').click();
    const rotationOnToast = await waitForToast();
    expect(rotationOnToast).toMatch(/Automatic rotation enabled/);
    await expect(rotationInput).toBeChecked();

    // Persisted across reload.
    await page.reload();
    await expect(page.locator('.opt-row', { hasText: 'Automatic rotation' }).locator('input[type=checkbox]')).toBeChecked();

    await page.getByRole('button', { name: 'Rotate now' }).click();
    const rotateToast = await waitForToast();
    expect(rotateToast).toMatch(/Key material rotated/);
    await waitToastGone(page);
    // The rotation history (ListKeyRotations) shows the on-demand rotation.
    await expect(page.getByText(/Rotated on demand 1 time, last/)).toBeVisible();

    // --- Alias management ---
    // Scoped to the alias form specifically — the tags panel further down
    // also renders a `.tag-row-form` with its own "Add" button.
    const aliasForm = page.locator('form.tag-row-form:has(input[name="alias"])');
    const secondAlias = uniqueName('e2e-kms2');
    await aliasForm.locator('input[name="alias"]').fill(secondAlias);
    await aliasForm.getByRole('button', { name: 'Add' }).click();
    const addToast = await waitForToast();
    expect(addToast).toMatch(/Alias added/);
    await expect(page.locator('.sub-row', { hasText: secondAlias })).toBeVisible();
    await waitToastGone(page);

    const secondAliasRow = page.locator('.sub-row', { hasText: secondAlias });
    await secondAliasRow.locator('button[title="Remove alias"]').click();
    await page.locator('#confirm-yes').click();
    const removeToast = await waitForToast();
    expect(removeToast).toMatch(/Alias removed/);
    await expect(page.locator('.sub-row', { hasText: secondAlias })).toHaveCount(0);
    // Original alias survives.
    await expect(page.locator('.sub-row', { hasText: alias })).toBeVisible();

    // --- Schedule deletion, verify cancel-deletion affordance, then cancel ---
    await page.getByRole('button', { name: 'Schedule deletion' }).click();
    await expect(page.locator('#confirm')).toBeVisible();
    await page.locator('#confirm-yes').click();
    // schedule-deletion responds with HX-Redirect to the list page.
    await page.waitForURL(/\/kms(\?.*)?$/);

    await page.goto(`kms/${keyId}`);
    await expect(page.locator('.det-title .badge')).toHaveText('PendingDeletion');
    const cancelBtn = page.getByRole('button', { name: 'Cancel deletion' });
    await expect(cancelBtn).toBeVisible();
    // Rotate/schedule buttons are hidden while pending deletion.
    await expect(page.getByRole('button', { name: 'Schedule deletion' })).toHaveCount(0);

    await cancelBtn.click();
    const cancelToast = await waitForToast();
    expect(cancelToast).toMatch(/Deletion cancelled/);
    // Matches real KMS semantics: cancelling deletion leaves the key
    // Disabled (kms/actions_keys.go: "AWS leaves a cancelled key disabled")
    // — it doesn't jump back to Enabled on its own.
    await expect(page.locator('.det-title .badge')).toHaveText('Disabled');
    await expect(page.getByRole('button', { name: 'Schedule deletion' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Cancel deletion' })).toHaveCount(0);
  });
});

test.describe('SIGN_VERIFY key', () => {
  test('sign then one-click verify reports valid', async ({ page, request, uniqueName }) => {
    const alias = uniqueName('e2e-kms-sig');
    const keyId = await createKey(request, {
      spec: 'RSA_2048',
      usage: 'SIGN_VERIFY',
      alias,
    });

    await page.goto(`kms/${keyId}`);
    await expect(page.locator('.det-title')).toContainText(keyId);
    await expect(page.locator('.sub-row', { hasText: alias })).toBeVisible();

    const signForm = page.locator('form:has(textarea[name="message"])');
    await expect(signForm.locator('textarea[name="message"]')).toHaveValue('hello doze');
    await signForm.getByRole('button', { name: 'Sign →' }).click();

    const cryptoOut = page.locator('#kms-crypto-out');
    await expect(cryptoOut.locator('pre')).toBeVisible();
    const signature = (await cryptoOut.locator('pre').textContent())?.trim();
    expect(signature).toBeTruthy();

    await cryptoOut.getByRole('button', { name: 'Verify this signature →' }).click();
    await expect(cryptoOut).toContainText('signature valid');
  });
});

test.describe('GENERATE_VERIFY_MAC key', () => {
  test('generate MAC then one-click verify reports valid', async ({ page, request, uniqueName }) => {
    const alias = uniqueName('e2e-kms-mac');
    const keyId = await createKey(request, {
      spec: 'HMAC_256',
      usage: 'GENERATE_VERIFY_MAC',
      alias,
    });

    await page.goto(`kms/${keyId}`);
    await expect(page.locator('.det-title')).toContainText(keyId);
    await expect(page.locator('.sub-row', { hasText: alias })).toBeVisible();

    const macForm = page.locator('form:has(textarea[name="message"])');
    await expect(macForm.locator('textarea[name="message"]')).toHaveValue('hello doze');
    await macForm.getByRole('button', { name: 'Generate MAC →' }).click();

    const cryptoOut = page.locator('#kms-crypto-out');
    await expect(cryptoOut.locator('pre')).toBeVisible();
    const mac = (await cryptoOut.locator('pre').textContent())?.trim();
    expect(mac).toBeTruthy();

    await cryptoOut.getByRole('button', { name: 'Verify this MAC →' }).click();
    await expect(cryptoOut).toContainText('MAC valid');
  });
});

// ---------------------------------------------------------------------------
// The rest of the key page: the create form itself, description, key policy,
// GenerateRandom, ReEncrypt, GetPublicKey and UpdateAlias — each driven from
// its own control on the page.

test.describe('create key form', () => {
  test('New -> fill -> Create key lands on the detail page', async ({ page, request, uniqueName, waitForToast }) => {
    // At least one key so /kms renders the list pane rather than the empty state.
    await createKey(request, { alias: uniqueName('e2e-kms-seed') });
    const alias = uniqueName('e2e-kms-new');
    await page.goto('kms');
    await page.locator('.listpane .new-link').click();
    await page.waitForURL(/\/kms\/create$/);
    await page.locator('select[name="spec"]').selectOption('ECC_NIST_P256');
    await page.locator('select[name="usage"]').selectOption('SIGN_VERIFY');
    await page.locator('input[name="alias"]').fill(alias);
    await page.locator('input[name="description"]').fill('made by the e2e create form');
    await page.getByRole('button', { name: 'Create key' }).click();
    expect(await waitForToast()).toContain('Key created');
    await page.waitForURL(/\/kms\/[0-9a-f-]{36}$/);
    await expect(page.locator('.tbl.kv')).toContainText('ECC_NIST_P256');
    await expect(page.locator('.tbl.kv')).toContainText('SIGN_VERIFY');
    await expect(page.locator('.sub-row', { hasText: alias })).toBeVisible();
    await expect(page.locator('form[hx-post$="/description"] input[name="description"]')).toHaveValue(
      'made by the e2e create form'
    );
  });
});

test.describe('key settings', () => {
  test('description and key policy save and persist', async ({ page, request, uniqueName, waitForToast }) => {
    const keyId = await createKey(request, { alias: uniqueName('e2e-kms-set'), description: 'before' });
    await page.goto(`kms/${keyId}`);

    // --- Description (UpdateKeyDescription) ---
    const desc = page.locator('form[hx-post$="/description"]');
    await expect(desc.locator('input[name="description"]')).toHaveValue('before');
    await desc.locator('input[name="description"]').fill('after — edited in place');
    await desc.getByRole('button', { name: 'Save' }).click();
    expect(await waitForToast()).toContain('Description updated');
    await page.reload();
    await expect(page.locator('form[hx-post$="/description"] input[name="description"]')).toHaveValue(
      'after — edited in place'
    );

    // --- Key policy (PutKeyPolicy) ---
    const sid = uniqueName('E2ESid').replace(/-/g, '');
    const doc = JSON.stringify({
      Version: '2012-10-17',
      Statement: [
        { Sid: sid, Effect: 'Allow', Principal: { AWS: 'arn:aws:iam::000000000000:root' }, Action: 'kms:*', Resource: '*' },
      ],
    });
    const polPanel = page.locator('.panel', { has: page.locator('form[hx-post$="/policy"]') });
    await polPanel.locator('.panel-h').getByRole('button', { name: 'Edit' }).click();
    const sel = 'form[hx-post$="/policy"] textarea[name="document"]';
    await page.waitForFunction((s) => !!(document.querySelector(s) as any)?.__cm, sel);
    await page.evaluate(
      ([s, d]) => {
        const ta = document.querySelector(s)!;
        (window as any).dozeEditor.set(ta, d);
        ta.dispatchEvent(new Event('input', { bubbles: true }));
      },
      [sel, doc]
    );
    await polPanel.getByRole('button', { name: 'Save key policy' }).click();
    expect(await waitForToast()).toContain('Key policy saved');
    // GetKeyPolicy round-trips it: reload and the editor holds the new Sid.
    await page.reload();
    await expect(page.locator(sel)).toHaveValue(new RegExp(sid));
  });
});

test.describe('key-less and cross-key operations', () => {
  test('GenerateRandom returns the asked-for number of bytes', async ({ page, request, uniqueName }) => {
    const keyId = await createKey(request, { alias: uniqueName('e2e-kms-rnd') });
    await page.goto(`kms/${keyId}`);
    const form = page.locator('form[hx-post$="/kms/random"]');
    await form.locator('input[name="bytes"]').fill('16');
    await form.getByRole('button', { name: 'Generate' }).click();
    const out = page.locator('#kms-op-out');
    await expect(out).toContainText('Random bytes (base64)');
    const b64 = ((await out.locator('pre').textContent()) ?? '').trim();
    expect(Buffer.from(b64, 'base64').length).toBe(16);
  });

  test('ReEncrypt moves ciphertext to another key; it decrypts there', async ({ page, request, uniqueName }) => {
    const src = await createKey(request, { alias: uniqueName('e2e-kms-src') });
    const dst = await createKey(request, { alias: uniqueName('e2e-kms-dst') });
    const plaintext = `moved-${uniqueName('pt')}`;

    await page.goto(`kms/${src}`);
    const enc = page.locator('form:has(textarea[name="plaintext"])');
    await enc.locator('textarea[name="plaintext"]').fill(plaintext);
    await enc.getByRole('button', { name: 'Encrypt' }).click();
    const cryptoOut = page.locator('#kms-crypto-out pre');
    await expect(cryptoOut).toBeVisible();
    const ciphertext = ((await cryptoOut.textContent()) ?? '').trim();

    const re = page.locator('form[hx-post$="/reencrypt"]');
    await re.locator('textarea[name="ciphertext"]').fill(ciphertext);
    await re.locator('select[name="dest"]').selectOption(dst);
    await re.getByRole('button', { name: 'Re-encrypt' }).click();
    const out = page.locator('#kms-op-out');
    await expect(out).toContainText(`Ciphertext under ${dst}`);
    await expect(out).toContainText('The plaintext never came back');
    const moved = ((await out.locator('pre').textContent()) ?? '').trim();
    expect(moved).toBeTruthy();
    expect(moved).not.toBe(ciphertext);

    // The destination key decrypts it back to the original plaintext.
    await page.goto(`kms/${dst}`);
    const dec = page.locator('form:has(textarea[name="ciphertext"]):has(button:text-is("Decrypt"))');
    await dec.locator('textarea[name="ciphertext"]').fill(moved);
    await dec.getByRole('button', { name: 'Decrypt', exact: true }).click();
    await expect(page.locator('#kms-crypto-out')).toContainText('Plaintext');
    await expect(page.locator('#kms-crypto-out pre')).toHaveText(plaintext);
  });

  test('Export public key on a SIGN_VERIFY key', async ({ page, request, uniqueName }) => {
    const keyId = await createKey(request, { spec: 'RSA_2048', usage: 'SIGN_VERIFY', alias: uniqueName('e2e-kms-pub') });
    await page.goto(`kms/${keyId}`);
    await page.getByRole('button', { name: 'Export public key' }).click();
    const out = page.locator('#kms-op-out');
    await expect(out).toContainText('Public key (base64 DER)');
    const der = Buffer.from(((await out.locator('pre').textContent()) ?? '').trim(), 'base64');
    // An RSA-2048 SubjectPublicKeyInfo is a DER SEQUENCE of ~294 bytes.
    expect(der[0]).toBe(0x30);
    expect(der.length).toBeGreaterThan(250);
  });

  test('Repoint here moves an existing alias onto this key', async ({ page, request, uniqueName, waitForToast }) => {
    const alias = uniqueName('e2e-kms-mv');
    const from = await createKey(request, { alias });
    const to = await createKey(request, { alias: uniqueName('e2e-kms-to') });

    await page.goto(`kms/${to}`);
    await expect(page.locator('.sub-row', { hasText: alias })).toHaveCount(0);
    const form = page.locator('form[hx-post$="/update-alias"]');
    await form.locator('input[name="existing_alias"]').fill(`alias/${alias}`);
    await form.getByRole('button', { name: 'Repoint here' }).click();
    expect(await waitForToast()).toContain('now points here');
    await expect(page.locator('.sub-row', { hasText: alias })).toBeVisible();

    // …and it left the old key (one step, not delete-and-recreate).
    await page.goto(`kms/${from}`);
    await expect(page.locator('.det-title')).toContainText(from);
    await expect(page.locator('.sub-row', { hasText: alias })).toHaveCount(0);
  });
});
