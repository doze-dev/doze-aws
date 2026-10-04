import type { Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';

// API keys and usage plans: the controls apigateway.spec.ts's gate tests
// never touch — disabling and re-enabling a key, a plan created with no stage
// and given one afterwards, its detail, detaching a key, and deleting the
// plan and the key. The page is instance-global, so every locator is scoped
// to this test's own uniquely named key or plan.

type ToastFn = (opts?: { kind?: 'ok' | 'err' }) => Promise<string>;

/** A REST API with a MOCK GET on the root, deployed to `stage` — the least
 *  a usage plan can cover. Arranged through the same UI the user would use. */
async function deployedApi(page: Page, waitForToast: ToastFn, name: string, stage: string) {
  await page.goto('apigw/create');
  await page.locator('input[name="name"]').fill(name);
  await page.getByRole('button', { name: 'Create API' }).click();
  await page.waitForURL(/\/apigw\/[a-z0-9]+$/);
  await waitForToast();
  const url = page.url();
  await page.locator('button[title="Add method on /"]').click();
  const mDlg = page.locator('.overlay[aria-label="Add method"]');
  await mDlg.locator('select[name="verb"]').selectOption('GET');
  await mDlg.getByRole('button', { name: 'Add method' }).click();
  await waitForToast();
  await page.locator('#apigw-routes a:has(.chip:text("GET"))').click();
  const panel = page.locator('#method-out');
  await panel.locator('select[name="type"]').selectOption('MOCK');
  await panel.getByRole('button', { name: 'Save integration' }).click();
  await waitForToast();
  await page.goto(url + '?tab=stages');
  await page.locator('input[name="stage"]').fill(stage);
  await page.getByRole('button', { name: 'Deploy API' }).click();
  await waitForToast();
  return url;
}

test.describe('API keys and usage plans', () => {
  test('toggle a key, give a plan a stage, detach, and delete both', async ({
    page,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const apiName = uniqueName('e2e-keys-api');
    await deployedApi(page, waitForToast, apiName, 'v1');

    await page.goto('apigw-keys');
    const body = page.locator('#apigw-keys-body');
    const keysPanel = body.locator('.split > .panel').first();
    const keyName = uniqueName('partner');
    const planName = uniqueName('plan');

    await body.locator('form[hx-post$="/apigw-keys/create"] input[name="name"]').fill(keyName);
    await body.getByRole('button', { name: 'Create key' }).click();
    await waitForToast();
    const keyRow = () => keysPanel.locator('table.tbl tbody tr', { hasText: keyName });
    await expect(keyRow().locator('.badge', { hasText: 'enabled' })).toBeVisible();

    // Disable, then enable again.
    await keyRow().getByRole('button', { name: 'Disable' }).click();
    expect(await waitForToast()).toContain('API key updated');
    await expect(keyRow().locator('.badge', { hasText: 'disabled' })).toBeVisible();
    await keyRow().getByRole('button', { name: 'Enable' }).click();
    expect(await waitForToast()).toContain('API key updated');
    await expect(keyRow().locator('.badge.state-on', { hasText: 'enabled' })).toBeVisible();

    // A plan covering no stage yet.
    await body.locator('form[hx-post$="/plans/create"] input[name="name"]').fill(planName);
    await body.locator('form[hx-post$="/plans/create"] select[name="stage"]').selectOption('');
    await body.getByRole('button', { name: 'Create plan' }).click();
    await waitForToast();
    const plan = () => body.locator('.sub-panel', { hasText: planName });
    await expect(plan()).toContainText('none — no method is opened by this plan');

    // Give it the stage from its own Add stage form.
    await plan().locator('form[hx-post$="/add-stage"] select[name="stage"]').selectOption({ label: `${apiName} · v1` });
    await plan().getByRole('button', { name: 'Add stage' }).click();
    expect(await waitForToast()).toContain('Stage added to the plan');
    await expect(plan().locator('.chip', { hasText: `${apiName} · v1` })).toBeVisible();
    await expect(plan()).not.toContainText('none — no method is opened');

    // The plan's name opens its detail (GetUsagePlan).
    await plan().locator('button.linkish', { hasText: planName }).click();
    const detail = page.locator('#apigw-plan-detail');
    await expect(detail).toContainText(`Usage plan ${planName}`);
    await expect(detail).toContainText('id');

    // Attach the key, then detach it from its chip.
    await plan().locator('select[name="key"]').selectOption({ label: keyName });
    await plan().getByRole('button', { name: 'Attach key' }).click();
    await waitForToast();
    const keyChip = () => plan().locator('.chip', { hasText: keyName });
    await expect(keyChip()).toBeVisible();
    await keyChip().getByRole('button').click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Key detached');
    await expect(keyChip()).toHaveCount(0);

    // Delete the plan, behind a confirm.
    await plan().getByRole('button', { name: 'Delete plan' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Usage plan deleted');
    await expect(plan()).toHaveCount(0);

    // Delete the key, behind a confirm.
    await keyRow().getByRole('button', { name: 'Delete key' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('API key deleted');
    await expect(keyRow()).toHaveCount(0);

    // AWS-side: a fresh load agrees.
    await page.reload();
    await expect(page.locator('#apigw-keys-body')).not.toContainText(keyName);
    await expect(page.locator('#apigw-keys-body')).not.toContainText(planName);
  });
});
