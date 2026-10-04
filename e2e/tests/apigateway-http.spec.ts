import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect } from '../fixtures/console';
import { createFunction } from '../fixtures/api';

// ES modules have no __dirname.
const __dirname = path.dirname(fileURLToPath(import.meta.url));

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
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('HTTP API deleted');
    await expect(page.locator('.li .nm', { hasText: name })).toHaveCount(0);
  });
});

// ---------------------------------------------------------------------------
// The HTTP API's teardown and secondary controls: deleting a route, a stage
// with auto-deploy off deployed by hand and then deleted, and a REQUEST
// authorizer created and deleted from Settings. A real function is arranged
// through the API helper because the authorizer form only offers existing
// functions; everything under test is clicked.

const LAMBDA_CODE_DIR = path.resolve(__dirname, '../fixtures/lambda-handler');

async function newHttpApi(
  page: import('@playwright/test').Page,
  waitForToast: (opts?: { kind?: 'ok' | 'err' }) => Promise<string>,
  name: string
) {
  await page.goto('apigw/create');
  await page.locator('input[name="protocol"][value="HTTP"]').check();
  await page.locator('input[name="name"]').fill(name);
  await page.getByRole('button', { name: 'Create API' }).click();
  await page.waitForURL(/\/apigw-http\/[a-z0-9]+$/);
  await waitForToast();
  return page.url();
}

test.describe('HTTP API editing', () => {
  test('a route is deleted from its row', async ({ page, uniqueName, waitForToast, confirmDialog }) => {
    await newHttpApi(page, waitForToast, uniqueName('e2e-http-rt'));
    const form = page.locator('form[hx-post$="/add-route"]');
    await form.locator('select[name="method"]').selectOption('POST');
    await form.locator('input[name="path"]').fill('/orders');
    await form.locator('select:not([name])').selectOption('url');
    await form.locator('input[name="url"]').fill('http://127.0.0.1:1/orders');
    await form.getByRole('button', { name: 'Add route' }).click();
    await waitForToast();
    const routes = page.locator('#apigw-http-routes');
    await expect(routes.locator('td.mono', { hasText: /^\/orders$/ })).toBeVisible();

    await routes.getByRole('button', { name: 'Delete route POST /orders' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Route removed');
    await expect(routes.locator('td.mono', { hasText: /^\/orders$/ })).toHaveCount(0);
    await expect(routes).toContainText('No routes');
  });

  test('a hand-deployed stage is deployed, then deleted', async ({ page, uniqueName, waitForToast, confirmDialog }) => {
    const apiURL = await newHttpApi(page, waitForToast, uniqueName('e2e-http-stg'));
    await page.goto(apiURL + '?tab=stages');
    const create = page.locator('form[hx-post$="/create-stage"]');
    await create.locator('input[name="name"]').fill('beta');
    await create.locator('select[name="auto_deploy"]').selectOption('off');
    await create.getByRole('button', { name: 'Create stage' }).click();
    expect(await waitForToast()).toContain('Stage beta created');
    const row = () => page.locator('table.tbl tbody tr', { has: page.locator('td.mono', { hasText: /^beta$/ }) });
    // Auto-deploy off: no deployment yet.
    await expect(row().locator('td').nth(3)).toHaveText('none');

    const deploy = page.locator('form[hx-post$="/deploy"]');
    await deploy.locator('select[name="stage"]').selectOption('beta');
    await deploy.getByRole('button', { name: 'Deploy now' }).click();
    expect(await waitForToast()).toContain('Deployed to beta');
    await expect(row().locator('td').nth(3)).not.toHaveText('none');

    await row().getByRole('button', { name: 'Delete stage beta' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Stage deleted');
    await expect(row()).toHaveCount(0);
    await expect(page.locator('table.tbl')).toContainText('No stages');
  });

  test('an authorizer is created and deleted from Settings', async ({
    page,
    request,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const fn = uniqueName('e2e-http-authfn');
    await createFunction(request, fn, { code: LAMBDA_CODE_DIR });
    const apiURL = await newHttpApi(page, waitForToast, uniqueName('e2e-http-auth'));
    await page.goto(apiURL + '?tab=settings');
    const authName = uniqueName('gate');
    const form = page.locator('form[hx-post$="/create-authorizer"]');
    await form.locator('input[name="name"]').fill(authName);
    await form.locator('select[name="lambda"]').selectOption(fn);
    await form.locator('input[name="header"]').fill('X-Token');
    await form.locator('input[name="ttl"]').fill('30');
    await form.getByRole('button', { name: 'Create authorizer' }).click();
    expect(await waitForToast()).toContain('Authorizer created');
    const row = () => page.locator('table.tbl tbody tr', { hasText: authName });
    await expect(row()).toContainText(fn);
    await expect(row()).toContainText('X-Token');
    await expect(row()).toContainText('30s');

    // The routes tab now offers it on the add-route form.
    await page.goto(apiURL);
    await expect(page.locator('form[hx-post$="/add-route"] select[name="authorizer"] option', { hasText: authName })).toHaveCount(1);

    await page.goto(apiURL + '?tab=settings');
    await row().getByRole('button', { name: `Delete authorizer ${authName}` }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Authorizer deleted');
    await expect(row()).toHaveCount(0);
    await expect(page.locator('table.tbl')).toContainText('No authorizers');
  });
});
