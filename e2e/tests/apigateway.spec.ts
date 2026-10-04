import { test, expect } from '../fixtures/console';
import { ORIGIN } from '../playwright.config';

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

    // Something has to answer the method before the API will deploy: a
    // method with no integration is refused, here as on AWS. This test used
    // to deploy with nothing behind it.
    await page.locator('#method-out select[name="type"]').selectOption('MOCK');
    await page.locator('#method-out').getByRole('button', { name: 'Save integration' }).click();
    await waitForToast();

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
    // Scoped to the keys panel, and to tbody: hasText matches DESCENDANT text,
    // so an unscoped `table.tbl tr` also matches rows in the plans panel and
    // the <option> text inside its stage <select> — both of which carry key
    // names on this instance-global page. The same fix is applied below at the
    // API-key-enforcement test; it was written there and never back-ported.
    const keysPanel = body().locator('.split > .panel').first();
    const keyRow = keysPanel.locator('table.tbl tbody tr', { hasText: key });
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

    const invoke = `${ORIGIN}/_aws/execute-api/${apiID}/v1/`;

    // No key: Forbidden. This is the assertion the whole feature exists for.
    const bare = await request.get(invoke);
    expect(bare.status()).toBe(403);
    expect(await bare.text()).toContain('Forbidden');

    // A key that exists but belongs to no plan covering this stage is still
    // refused — the plan, not the key, is what admits a request.
    const keyName = uniqueName('partner');
    // Unique, like every other identifier this suite creates. It was the one
    // hard-coded value in a suite whose whole isolation model is uniqueName(),
    // one line below a uniqueName() call — and with retries: 2 in CI, a retry
    // re-creates a key with a value that already exists, so the retry that was
    // meant to absorb flakiness GUARANTEED the failure instead. The prefix is
    // long enough that the value clears AWS's 20-character minimum.
    const keyValue = uniqueName('e2e-key-value-0123456789');
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

// ---------------------------------------------------------------------------
// Editing and tearing down what the build tests above only ever add: every
// structural delete/rename on the route tree, both halves of the response
// contract, the stage/deployment lifecycle, authorizer detail/TTL/delete, and
// deleting the API itself. Arrangement goes through the same UI (there is no
// API-side arrange helper for REST APIs), the action under test is always a
// click on the control a user would use.

type ToastFn = (opts?: { kind?: 'ok' | 'err' }) => Promise<string>;

async function newRestApi(page: import('@playwright/test').Page, waitForToast: ToastFn, name: string) {
  await page.goto('apigw/create');
  await page.locator('input[name="name"]').fill(name);
  await page.getByRole('button', { name: 'Create API' }).click();
  await page.waitForURL(/\/apigw\/[a-z0-9]+$/);
  await waitForToast();
  const url = page.url();
  return { url, id: url.split('/').pop()! };
}

/** A GET on the root answered by MOCK — the least an API needs to deploy. */
async function addMockRootGet(page: import('@playwright/test').Page, waitForToast: ToastFn) {
  await page.locator('button[title="Add method on /"]').click();
  const mDlg = page.locator('.overlay[aria-label="Add method"]');
  await mDlg.locator('select[name="verb"]').selectOption('GET');
  await mDlg.getByRole('button', { name: 'Add method' }).click();
  await waitForToast();
  await page.locator('#apigw-routes a:has(.chip:text("GET"))').first().click();
  const panel = page.locator('#method-out');
  await panel.locator('select[name="type"]').selectOption('MOCK');
  await panel.getByRole('button', { name: 'Save integration' }).click();
  await waitForToast();
}

test.describe('API Gateway editing', () => {
  test('responses, integration, method and resource each come apart from the UI', async ({
    page,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    await newRestApi(page, waitForToast, uniqueName('e2e-apigw-edit'));
    const routes = page.locator('#apigw-routes');

    // /orders with a GET, MOCK-wired, both response halves declared.
    await page.locator('button[title="Add child resource under /"]').click();
    const resDlg = page.locator('.overlay[aria-label="Add resource"]');
    await resDlg.locator('input[name="part"]').fill('orders');
    await resDlg.getByRole('button', { name: 'Add resource' }).click();
    await waitForToast();
    await page.locator('button[title="Add method on /orders"]').click();
    const mDlg = page.locator('.overlay[aria-label="Add method"]');
    await mDlg.locator('select[name="verb"]').selectOption('GET');
    await mDlg.getByRole('button', { name: 'Add method' }).click();
    await waitForToast();
    await routes.locator('a:has(.chip:text("GET"))').click();
    const panel = page.locator('#method-out');
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

    // Integration response 200: the trash in its table row.
    const intTable = panel.locator('table.tbl:has(th:text("Template"))');
    await expect(intTable.locator('tbody tr', { hasText: '200' })).toBeVisible();
    await intTable.getByRole('button', { name: 'Remove 200' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Response 200 removed');
    await expect(panel).toContainText('none — proxy integrations pass');

    // Method response 200: the × on its chip.
    const chips = panel.locator('.chips');
    await expect(chips.locator('.badge', { hasText: '200' })).toBeVisible();
    // Its accessible name is the "×" glyph, not the title, so address it by title.
    await chips.locator('button[title="Remove 200"]').click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Response 200 removed');
    await expect(chips).toContainText('none declared');

    // The integration, behind a confirm: the method stays, unwired.
    await panel.getByRole('button', { name: 'Remove integration' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Integration removed');
    await expect(panel).toContainText('Unwired');
    await expect(panel.getByRole('button', { name: 'Remove integration' })).toHaveCount(0);

    // The method row's trash, behind a confirm.
    await routes.getByRole('button', { name: 'Delete GET on /orders' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Method deleted');
    await expect(routes.locator('.chip', { hasText: 'GET' })).toHaveCount(0);

    // Rename /orders to /purchases through its dialog.
    await routes.getByRole('button', { name: 'Rename /orders' }).click();
    const renDlg = page.locator('.overlay[aria-label="Rename resource"]');
    await expect(renDlg).toBeVisible();
    await renDlg.locator('input[name="part"]').fill('purchases');
    await renDlg.getByRole('button', { name: 'Rename' }).click();
    expect(await waitForToast()).toContain('Resource renamed');
    await expect(renDlg).toBeHidden();
    await expect(routes.locator('b', { hasText: '/purchases' })).toBeVisible();
    await expect(routes.locator('b', { hasText: '/orders' })).toHaveCount(0);

    // And delete it, behind a confirm.
    await routes.getByRole('button', { name: 'Delete /purchases' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Resource deleted');
    await expect(routes.locator('b', { hasText: '/purchases' })).toHaveCount(0);
    await expect(routes.locator('b', { hasText: /^\/$/ })).toBeVisible();
  });

  test('a stage from an old deployment, repointed, deleted; then the deployment goes', async ({
    page,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const { url } = await newRestApi(page, waitForToast, uniqueName('e2e-apigw-stg'));
    await addMockRootGet(page, waitForToast);

    // Two deployments to dev: the older one is what "rollback" points at.
    await page.goto(url + '?tab=stages');
    for (const desc of ['first', 'second']) {
      await page.locator('input[name="stage"]').fill('dev');
      await page.locator('input[name="description"]').fill(desc);
      await page.getByRole('button', { name: 'Deploy API' }).click();
      expect(await waitForToast()).toContain('Deployed to dev');
    }
    const depTable = page.locator('table.tbl:has(th:text("Description"))');
    const idOf = async (desc: string) =>
      (await depTable.locator('tbody tr', { hasText: desc }).locator('td').first().textContent())!.trim();
    const older = await idOf('first');
    const newer = await idOf('second');
    expect(older).not.toBe(newer);

    // Create a stage from the old deployment.
    const createForm = page.locator('form[hx-post$="/create-stage"]');
    await createForm.locator('input[name="name"]').fill('rollback');
    await createForm.locator('select[name="deployment"]').selectOption(older);
    await createForm.getByRole('button', { name: 'Create stage' }).click();
    expect(await waitForToast()).toContain('Stage rollback created');
    const stageRow = page.locator('table.tbl tbody tr', { has: page.locator('td.mono', { hasText: /^rollback$/ }) });
    await expect(stageRow.locator('select[name="deployment"]')).toHaveValue(older);

    // Repoint it: the select submits on change.
    // Declined, the picker goes back to what the stage actually serves.
    await stageRow.locator('select[name="deployment"]').selectOption(newer);
    await confirmDialog('cancel');
    await expect(stageRow.locator('select[name="deployment"]')).toHaveValue(older);
    await stageRow.locator('select[name="deployment"]').selectOption(newer);
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Stage rollback repointed');
    await expect(stageRow.locator('select[name="deployment"]')).toHaveValue(newer);

    // Delete the stage, behind a confirm.
    await stageRow.getByRole('button', { name: 'Delete stage rollback' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Stage rollback deleted');
    await expect(stageRow).toHaveCount(0);

    // The older deployment now has no stage on it; delete its record.
    await depTable.getByRole('button', { name: `Delete deployment ${older}` }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Deployment deleted');
    await expect(depTable.locator('tbody tr', { hasText: older })).toHaveCount(0);
    await expect(depTable.locator('tbody tr', { hasText: newer })).toBeVisible();
  });

  test('an authorizer opens its detail, takes a new TTL, and is deleted', async ({
    page,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const { url } = await newRestApi(page, waitForToast, uniqueName('e2e-apigw-auth'));
    await page.goto(url + '?tab=settings');
    const auths = page.locator('#apigw-authorizers');
    const authName = uniqueName('gate');
    await auths.locator('input[name="name"]').fill(authName);
    await auths.locator('input[name="function"]').fill('gatekeeper');
    await auths.locator('input[name="source"]').fill('X-Token');
    await auths.getByRole('button', { name: 'Add authorizer' }).click();
    await waitForToast();

    // The name opens GetAuthorizer's detail under the table.
    await auths.locator('button.linkish', { hasText: authName }).click();
    const detail = page.locator('#apigw-authorizer-detail');
    await expect(detail).toContainText('gatekeeper');
    await expect(detail).toContainText('X-Token');
    await expect(detail).toContainText('300 s');

    // Set TTL from the row.
    const row = page.locator('#apigw-authorizers tbody tr', { hasText: authName });
    await row.locator('input[name="ttl"]').fill('60');
    await row.getByRole('button', { name: 'Set TTL' }).click();
    expect(await waitForToast()).toContain('Authorizer updated');
    await expect(page.locator('#apigw-authorizers tbody tr', { hasText: authName }).locator('input[name="ttl"]')).toHaveValue('60');
    // The AWS side agrees: reopen the detail.
    await page.locator('#apigw-authorizers button.linkish', { hasText: authName }).click();
    await expect(page.locator('#apigw-authorizer-detail')).toContainText('60 s');

    // Delete, behind a confirm.
    await page.locator('#apigw-authorizers tbody tr', { hasText: authName }).getByRole('button', { name: 'Delete authorizer' }).click();
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Authorizer deleted');
    await expect(page.locator('#apigw-authorizers')).toContainText('No authorizers');
  });

  test('the header Delete removes the API', async ({ page, uniqueName, waitForToast, confirmDialog }) => {
    const name = uniqueName('e2e-apigw-del');
    const { id } = await newRestApi(page, waitForToast, name);
    await page.locator('.acts').getByRole('button', { name: 'Delete' }).click();
    await confirmDialog('accept');
    await page.waitForURL(/\/apigw(\?|$)/);
    expect(await waitForToast()).toContain('API deleted');
    await expect(page.locator('.li .nm', { hasText: name })).toHaveCount(0);
    // And it is gone, not just unlisted.
    const res = await page.request.get(`apigw/${id}`);
    expect(res.ok()).toBe(false);
  });
});

test.describe('API Gateway deployment guard', () => {
  // Regression (fixed in 1.0): REST DeleteDeployment (apigateway/control.go, the DELETE case under
  // /restapis/{id}/deployments/{dep}) deletes a deployment a stage still
  // serves; AWS answers BadRequestException "Active stages pointing to this
  // deployment must be moved or deleted" (the v2 plane already does). The
  // console's confirm copy ("A stage still pointing at it keeps serving")
  // documents the wrong behaviour.
  test('deleting the deployment a stage serves is refused', async ({
    page,
    uniqueName,
    waitForToast,
    confirmDialog,
  }) => {
    const { url } = await newRestApi(page, waitForToast, uniqueName('e2e-apigw-depg'));
    await addMockRootGet(page, waitForToast);
    await page.goto(url + '?tab=stages');
    await page.locator('input[name="stage"]').fill('dev');
    await page.getByRole('button', { name: 'Deploy API' }).click();
    await waitForToast();
    const depTable = page.locator('table.tbl:has(th:text("Description"))');
    const dep = (await depTable.locator('tbody tr').first().locator('td').first().textContent())!.trim();
    await depTable.getByRole('button', { name: `Delete deployment ${dep}` }).click();
    await confirmDialog('accept');
    await expect(page.locator('.err[role="alert"]').last()).toContainText('Active stages');
    await page.reload();
    await expect(depTable.locator('tbody tr', { hasText: dep })).toBeVisible();
  });
});
