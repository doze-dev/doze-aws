import { test, expect } from '../fixtures/console';
import type { Page } from '@playwright/test';
import { createKey } from '../fixtures/api';

// waitForToast's `.last()` locator is only race-free when at most one toast
// is alive at a time — if two toast-producing actions fire back-to-back
// (no navigation between them), the previous toast can still be visible
// when we look for the next one, and `.last()` happily reports it as
// "visible" before the real new toast has even been appended. Toasts
// self-remove after 3.2s (ok) / 6s (err) per console/static/shell.js, so
// draining the current one first makes the next waitForToast() call
// unambiguous.
async function waitToastGone(page: Page) {
  await expect(page.locator('.toast:not(.err)').last()).toBeHidden({ timeout: 8000 });
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
