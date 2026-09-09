import { test, expect } from '../fixtures/console';

// HTTP API (apigatewayv2) console coverage: create one from the shared
// create page, add a URL route and a $default stage, call it through the
// execute-api plane from the Invoke tab, and edit CORS on Settings. A URL
// target nothing listens on keeps the spec self-contained: the 502 proves the
// route was taken, where a 404 would mean it was not.

test.describe('HTTP API console', () => {
  test('create, route, stage, invoke and configure CORS', async ({ page, uniqueName, waitForToast, confirmDialog }) => {
    const name = uniqueName('e2e-http');

    await page.goto('apigw/create');
    await page.locator('input[name="protocol"][value="HTTP"]').check();
    await page.locator('input[name="name"]').fill(name);
    await page.getByRole('button', { name: 'Create API' }).click();
    await page.waitForURL(/\/apigw-http\/[a-z0-9]+$/);
    let toast = await waitForToast();
    expect(toast).toContain('HTTP API created');
    const apiURL = page.url();

    // The sidebar badges it, the page says no routes yet.
    await expect(page.locator('.li.on .badge', { hasText: 'HTTP' })).toBeVisible();
    await expect(page.locator('#apigw-http-routes')).toContainText('No routes');

    // A route to a URL.
    const form = page.locator('form[hx-post$="/add-route"]');
    await form.locator('select[name="method"]').selectOption('GET');
    await form.locator('input[name="path"]').fill('/items/{id}');
    await form.locator('select:not([name])').selectOption('url');
    await form.locator('input[name="url"]').fill('http://127.0.0.1:1/{proxy}');
    await form.getByRole('button', { name: 'Add route' }).click();
    toast = await waitForToast();
    expect(toast).toContain('Route GET /items/{id} added');
    await expect(page.locator('#apigw-http-routes td', { hasText: '/items/{id}' })).toBeVisible();

    // A $default stage that auto-deploys.
    await page.goto(apiURL + '?tab=stages');
    await page.locator('form[hx-post$="/create-stage"] input[name="name"]').fill('$default');
    await page.locator('form[hx-post$="/create-stage"]').getByRole('button', { name: 'Create stage' }).click();
    toast = await waitForToast();
    expect(toast).toContain('Stage $default created');
    await expect(page.locator('table.tbl td.mono', { hasText: '$default' })).toBeVisible();

    // Invoke through the execute-api plane: the route matches, the backend
    // does not answer, so a 502 — not the 404 of an unrouted path.
    await page.goto(apiURL + '?tab=invoke');
    await page.locator('input[name="path"]').fill('/items/7');
    await page.getByRole('button', { name: 'Send' }).click();
    await expect(page.locator('#apigw-result .chip', { hasText: '502' })).toBeVisible();
    await page.locator('input[name="path"]').fill('/nothing/here');
    await page.locator('select[name="method"]').selectOption('DELETE');
    await page.getByRole('button', { name: 'Send' }).click();
    await expect(page.locator('#apigw-result .chip', { hasText: '404' })).toBeVisible();

    // CORS on Settings round-trips.
    await page.goto(apiURL + '?tab=settings');
    await page.locator('input[name="cors_origins"]').fill('https://app.example');
    await page.locator('input[name="cors_methods"]').fill('GET,POST');
    await page.locator('input[name="cors_max_age"]').fill('60');
    await page.getByRole('button', { name: 'Save settings' }).click();
    toast = await waitForToast();
    expect(toast).toContain('API settings saved');
    await expect(page.locator('input[name="cors_origins"]')).toHaveValue('https://app.example');
    await expect(page.locator('.chips .chip', { hasText: 'CORS on' })).toBeVisible();

    // Delete it behind the styled confirm; the list no longer carries it.
    await page.locator('.acts').getByRole('button', { name: 'Delete' }).click();
    await confirmDialog('accept');
    await page.waitForURL(/\/apigw(\?|$)/);
    await expect(page.locator('#flashbar')).toContainText('HTTP API deleted');
    await expect(page.locator('.li .nm', { hasText: name })).toHaveCount(0);
  });
});
