import { test, expect } from '../fixtures/console';

// API Gateway console coverage: the build-an-API path end to end — create,
// grow the resource tree, declare a method, wire a MOCK integration with a
// response pair, deploy to a stage, and call it through the execute-api
// plane. MOCK keeps the spec self-contained: no Lambda fixture needed.

test.describe('API Gateway console', () => {
  test('build, wire, deploy, and invoke an API', async ({ page, uniqueName, waitForToast }) => {
    const name = uniqueName('e2e-apigw');

    await page.goto('apigw/create');
    await page.locator('input[name="name"]').fill(name);
    await page.getByRole('button', { name: 'Create API' }).click();
    await page.waitForURL(/\/apigw\/[a-z0-9]+$/);
    // The flash rides the same feedback sequence the toast fixture tracks —
    // consume it through the fixture so later toast asserts see fresh entries.
    let toast = await waitForToast();
    expect(toast).toContain('API created');

    // Grow the tree: /items under the root, via the dialog.
    await page.locator('button[title="Add child resource under /"]').click();
    const resDlg = page.locator('.overlay[aria-label="Add resource"]');
    await expect(resDlg).toBeVisible();
    await resDlg.locator('input[name="part"]').fill('items');
    await resDlg.getByRole('button', { name: 'Add resource' }).click();
    toast = await waitForToast();
    expect(toast).toContain('Resource /items added');
    await expect(page.locator('#apigw-routes b', { hasText: '/items' })).toBeVisible();

    // Declare GET on it — flagged unwired until an integration exists.
    await page.locator('button[title="Add method on /items"]').click();
    const mDlg = page.locator('.overlay[aria-label="Add method"]');
    await expect(mDlg).toBeVisible();
    await mDlg.locator('select[name="verb"]').selectOption('GET');
    await mDlg.getByRole('button', { name: 'Add method' }).click();
    toast = await waitForToast();
    expect(toast).toContain('GET added');
    await expect(page.locator('#apigw-routes .chip.bad', { hasText: 'unwired' })).toBeVisible();

    // The method panel: wire MOCK, declare the 200 pair with a body template.
    await page.locator('#apigw-routes a:has(.chip:text("GET"))').click();
    const panel = page.locator('#method-out');
    await expect(panel.locator('.panel')).toBeVisible();
    await panel.locator('select[name="type"]').selectOption('MOCK');
    await panel.getByRole('button', { name: 'Save integration' }).click();
    toast = await waitForToast();
    expect(toast).toContain('Integration saved');

    await panel.locator('form:has(button:has-text("Declare")) input[name="status"]').fill('200');
    await panel.getByRole('button', { name: 'Declare' }).click();
    toast = await waitForToast();
    expect(toast).toContain('Response 200 declared');
    await panel.locator('form:has(button:has-text("Map")) input[name="status"]').fill('200');
    await panel.locator('input[name="template"]').fill('{"ok": true}');
    await panel.getByRole('button', { name: 'Map' }).click();
    toast = await waitForToast();
    expect(toast).toContain('Response 200 declared');

    // Deploy from the header button, then through the stages form.
    await page.locator('.acts a', { hasText: 'Deploy' }).click();
    await page.waitForURL(/tab=stages/);
    await page.locator('input[name="stage"]').fill('dev');
    await page.getByRole('button', { name: 'Deploy API' }).click();
    await expect(page.locator('#flashbar')).toContainText('Deployed to dev');
    await expect(page.locator('tbody td', { hasText: 'dev' }).first()).toBeVisible();

    // The proof: a request through the execute-api plane gets the MOCK's
    // template back with the declared status.
    await page.locator('.tabbar a', { hasText: 'Invoke' }).click();
    await page.waitForURL(/tab=invoke/);
    await page.locator('input[name="path"]').fill('/items');
    await page.getByRole('button', { name: 'Send' }).click();
    const result = page.locator('#apigw-result');
    await expect(result.locator('.chip', { hasText: '200' })).toBeVisible();
    await expect(result).toContainText('"ok"');

    // Settings: rename round-trips.
    await page.locator('.tabbar a', { hasText: 'Settings' }).click();
    await page.locator('input[name="name"]').fill(`${name}-v2`);
    await page.getByRole('button', { name: 'Save settings' }).click();
    await expect(page.locator('#flashbar')).toContainText('API settings saved');
    await expect(page.locator('.det-title')).toContainText(`${name}-v2`);
  });
});
