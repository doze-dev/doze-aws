import { test, expect } from '../fixtures/console';
import { createQueue, createTopic } from '../fixtures/api';

// static/select.js replaces the browser's dropdown with a listbox the console
// can style. The native <select> stays in the DOM and stays the thing that
// posts, so these tests check the seam: that picking in the new UI writes
// through to the old element, and that everything built on that element —
// forms, htmx serialization, Alpine's x-model — still sees what it expects.
test.describe('custom select', () => {
  test('picking an option writes through to the native select', async ({ page, request, uniqueName }) => {
    const topic = uniqueName('e2e-sel');
    await createTopic(request, topic);
    await page.goto(`sns/${topic}`);

    const ds = page.locator('.ds').filter({ has: page.locator('select[name="protocol"]') });
    const native = ds.locator('select[name="protocol"]');
    await expect(native).toHaveValue('sqs');

    await ds.locator('.ds-trigger').click();
    await expect(ds.locator('.ds-pop')).toBeVisible();
    await ds.locator('.ds-opt', { hasText: 'lambda' }).click();

    // The native element is what the form posts, so it is what has to change.
    await expect(native).toHaveValue('lambda');
    await expect(ds.locator('.ds-trigger')).toContainText('lambda');
    await expect(ds.locator('.ds-pop')).toBeHidden();
  });

  test('the keyboard drives it', async ({ page, request, uniqueName }) => {
    const topic = uniqueName('e2e-selkb');
    await createTopic(request, topic);
    await page.goto(`sns/${topic}`);

    const ds = page.locator('.ds').filter({ has: page.locator('select[name="protocol"]') });
    await ds.locator('.ds-trigger').focus();
    await page.keyboard.press('Enter');
    await expect(ds.locator('.ds-pop')).toBeVisible();

    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Enter');
    await expect(ds.locator('select[name="protocol"]')).toHaveValue('lambda');

    // Escape closes without changing anything.
    await ds.locator('.ds-trigger').focus();
    await page.keyboard.press('ArrowDown');
    await expect(ds.locator('.ds-pop')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(ds.locator('.ds-pop')).toBeHidden();
    await expect(ds.locator('select[name="protocol"]')).toHaveValue('lambda');
  });

  test("a filter appears exactly when the list is long enough to need one", async ({ page }) => {
    await page.goto("eb/default/create-rule");
    // Asserted as a rule over every enabled control on the page rather than
    // one named select: which list is long is data-dependent, the threshold is
    // not.
    const all = page.locator(".ds");
    const n = await all.count();
    let checked = 0;
    for (let i = 0; i < n; i++) {
      const ds = all.nth(i);
      if (await ds.locator(".ds-trigger").isDisabled()) continue;
      const options = await ds.locator("select").evaluate((s) => (s as HTMLSelectElement).options.length);
      await ds.locator(".ds-trigger").click();
      const hasFilter = await ds.locator(".ds-filter").isVisible();
      expect(hasFilter, `${options} options`).toBe(options >= 8);
      await page.keyboard.press("Escape");
      checked++;
    }
    expect(checked, "no enabled select to check").toBeGreaterThan(0);
  });

  test('the native select still carries the form', async ({ page, request, uniqueName }) => {
    const topic = uniqueName('e2e-selform');
    const queue = uniqueName('e2e-selq');
    await createTopic(request, topic);
    await createQueue(request, queue);
    await page.goto(`sns/${topic}`);

    // Subscribe through the custom UI end to end: the subscription only appears
    // if the form posted the value the listbox wrote.
    const proto = page.locator('.ds').filter({ has: page.locator('select[name="protocol"]') });
    await proto.locator('.ds-trigger').click();
    await proto.locator('.ds-opt', { hasText: 'sqs' }).click();

    // One endpoint select per protocol, switched by x-show, so only the one
    // matching the protocol just chosen is on screen.
    const endpoint = page
      .locator('.sub-add .ds:visible')
      .filter({ has: page.locator('select[name="endpoint"]') });
    await endpoint.locator('.ds-trigger').click();
    await endpoint.locator('.ds-opt', { hasText: queue }).first().click();

    await page.getByRole('button', { name: 'Subscribe' }).click();
    await expect(page.locator('#sns-subs')).toContainText(queue);
  });
});
