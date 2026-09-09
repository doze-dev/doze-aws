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
    await page.locator('form[hx-post$="/update"] input[name="name"]').fill(`${name}-v2`);
    await page.getByRole('button', { name: 'Save settings' }).click();
    await expect(page.locator('#flashbar')).toContainText('API settings saved');
    await expect(page.locator('.det-title')).toContainText(`${name}-v2`);
  });
});

test.describe('API Gateway gates', () => {
  test('an authorizer on Settings is offered to a CUSTOM method; keys and plans wire on their own page', async ({
    page,
    uniqueName,
    waitForToast,
  }) => {
    const name = uniqueName('e2e-apigw-gate');
    await page.goto('apigw/create');
    await page.locator('input[name="name"]').fill(name);
    await page.getByRole('button', { name: 'Create API' }).click();
    await page.waitForURL(/\/apigw\/[a-z0-9]+$/);
    await waitForToast();
    const apiURL = page.url();

    // Settings: add a TOKEN authorizer backed by a function name.
    await page.locator('.tabbar a', { hasText: 'Settings' }).click();
    const authPanel = page.locator('#apigw-authorizers');
    await expect(authPanel).toContainText('No authorizers');
    await authPanel.locator('input[name="name"]').fill('gate');
    await authPanel.locator('input[name="function"]').fill('gatekeeper');
    await authPanel.getByRole('button', { name: 'Add authorizer' }).click();
    await waitForToast();
    await expect(page.locator('#apigw-authorizers table')).toContainText('gate');
    await expect(page.locator('#apigw-authorizers table')).toContainText('gatekeeper');

    // Routes: the add-method dialog offers it under CUSTOM and the method names it.
    await page.goto(apiURL);
    await page.locator('button[title="Add method on /"]').click();
    const mDlg = page.locator('.overlay[aria-label="Add method"]');
    await mDlg.locator('select[name="auth"]').selectOption('CUSTOM');
    await expect(mDlg.locator('select[name="authorizer"] option', { hasText: 'gate · TOKEN' })).toHaveCount(1);
    await mDlg.getByRole('button', { name: 'Add method' }).click();
    await waitForToast();
    await page.locator('#apigw-routes a:has(.chip:text("GET"))').click();
    await expect(page.locator('#method-out .sub')).toContainText('CUSTOM · authorizer');

    // Keys page: a key, a plan on the deployed stage, the key attached.
    await page.goto(apiURL + '?tab=stages');
    await page.locator('input[name="stage"]').fill('v1');
    await page.getByRole('button', { name: 'Deploy API' }).click();
    await expect(page.locator('#flashbar')).toContainText('v1');
    await page.locator('a', { hasText: 'API keys' }).first().click();
    await page.waitForURL(/apigw-keys/);
    const body = () => page.locator('#apigw-keys-body');
    const key = uniqueName('partner');
    const plan = uniqueName('partners');
    await body().locator('form[hx-post$="/apigw-keys/create"] input[name="name"]').fill(key);
    await body().getByRole('button', { name: 'Create key' }).click();
    await waitForToast();
    const keyRow = body().locator('table.tbl tr', { hasText: key });
    await expect(keyRow).toBeVisible();
    await keyRow.locator('button.linkish', { hasText: 'reveal' }).click();
    await expect(keyRow).not.toContainText('reveal');
    await body().locator('form[hx-post$="/plans/create"] input[name="name"]').fill(plan);
    await body().locator('form[hx-post$="/plans/create"] select[name="stage"]').selectOption({ label: `${name} · v1` });
    await body().getByRole('button', { name: 'Create plan' }).click();
    await waitForToast();
    const planPanel = body().locator('.sub-panel', { hasText: plan });
    await expect(planPanel).toBeVisible();
    await planPanel.locator('select[name="key"]').selectOption({ label: key });
    await planPanel.getByRole('button', { name: 'Attach key' }).click();
    await waitForToast();
    await expect(body().locator('.sub-panel', { hasText: plan }).locator('.chip', { hasText: key })).toBeVisible();
  });

  // The gate itself. Every assertion in the test above is on rendered DOM
  // text: the authorizer points at a function that is never created, the
  // CUSTOM method is never called, and the key and plan are created but no
  // request ever carries one. "x-api-key" appeared exactly once in the whole
  // suite — as the header NAME of an EventBridge connection — so nothing
  // anywhere sent a key to execute-api. This sends one.
  test('a keyed method is refused without x-api-key and served with it', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const name = uniqueName('e2e-apigw-key');
    await page.goto('apigw/create');
    await page.locator('input[name="name"]').fill(name);
    await page.getByRole('button', { name: 'Create API' }).click();
    await page.waitForURL(/\/apigw\/[a-z0-9]+$/);
    await waitForToast();
    const apiURL = page.url();
    const apiID = apiURL.split('/').pop()!;

    // A MOCK-backed GET on the root that requires a key.
    await page.locator('button[title="Add method on /"]').click();
    const mDlg = page.locator('.overlay[aria-label="Add method"]');
    await mDlg.locator('select[name="verb"]').selectOption('GET');
    await mDlg.locator('input[name="apikey"]').check();
    await mDlg.getByRole('button', { name: 'Add method' }).click();
    await waitForToast();

    const panel = page.locator('#method-out');
    await page.locator('#apigw-routes a:has(.chip:text("GET"))').click();
    await panel.locator('select[name="type"]').selectOption('MOCK');
    await panel.getByRole('button', { name: 'Save integration' }).click();
    await waitForToast();
    await panel.locator('form:has(button:has-text("Declare")) input[name="status"]').fill('200');
    await panel.getByRole('button', { name: 'Declare' }).click();
    await waitForToast();
    await panel.locator('form:has(button:has-text("Map")) input[name="status"]').fill('200');
    await panel.locator('input[name="template"]').fill('{"ok": true}');
    await panel.getByRole('button', { name: 'Map' }).click();
    await waitForToast();

    await page.goto(apiURL + '?tab=stages');
    await page.locator('input[name="stage"]').fill('v1');
    await page.getByRole('button', { name: 'Deploy API' }).click();
    await expect(page.locator('#flashbar')).toContainText('v1');

    const invoke = `http://127.0.0.1:14566/_aws/execute-api/${apiID}/v1/`;

    // No key: Forbidden. This is the assertion the whole feature exists for.
    const bare = await request.get(invoke);
    expect(bare.status()).toBe(403);
    expect(await bare.text()).toContain('Forbidden');

    // A key that exists but belongs to no plan covering this stage is still
    // refused — the plan, not the key, is what admits a request.
    const keyName = uniqueName('partner');
    const keyValue = 'e2e-key-value-0123456789';
    await page.goto('apigw-keys');
    const body = () => page.locator('#apigw-keys-body');
    await body().locator('form[hx-post$="/apigw-keys/create"] input[name="name"]').fill(keyName);
    await body().locator('form[hx-post$="/apigw-keys/create"] input[name="value"]').fill(keyValue);
    await body().getByRole('button', { name: 'Create key' }).click();
    await waitForToast();

    const unplanned = await request.get(invoke, { headers: { 'x-api-key': keyValue } });
    expect(unplanned.status()).toBe(403);

    // The value is masked in the list until revealed — the Go test checks
    // this and the e2e only ever checked that a button's label changed.
    // Scoped to the keys panel: a plan panel elsewhere on the page carries
    // key chips too, and matching on the name alone is ambiguous.
    const keysPanel = body().locator('.split > .panel').first();
    const keyRow = keysPanel.locator('table.tbl tbody tr', { hasText: keyName });
    await expect(keyRow).not.toContainText(keyValue);
    await keyRow.locator('button.linkish', { hasText: 'reveal' }).click();
    await expect(keysPanel.locator('table.tbl tbody tr', { hasText: keyName })).toContainText(keyValue);

    // Put it in a plan on this api+stage, and the same request is served.
    const plan = uniqueName('partners');
    await body().locator('form[hx-post$="/plans/create"] input[name="name"]').fill(plan);
    await body()
      .locator('form[hx-post$="/plans/create"] select[name="stage"]')
      .selectOption({ label: `${name} · v1` });
    await body().getByRole('button', { name: 'Create plan' }).click();
    await waitForToast();
    const planPanel = body().locator('.sub-panel', { hasText: plan });
    await planPanel.locator('select[name="key"]').selectOption({ label: keyName });
    await planPanel.getByRole('button', { name: 'Attach key' }).click();
    await waitForToast();

    const admitted = await request.get(invoke, { headers: { 'x-api-key': keyValue } });
    expect(admitted.status()).toBe(200);
    expect(await admitted.text()).toContain('"ok"');

    // And a wrong value is still refused, so the 200 above was the key.
    const wrong = await request.get(invoke, { headers: { 'x-api-key': 'not-the-key-0123456789' } });
    expect(wrong.status()).toBe(403);
  });
});
