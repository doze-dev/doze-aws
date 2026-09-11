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

  // The option rows only exist while a popover is open, so any check that walks
  // the page at rest is blind to them.
  //
  // The fixture is built here rather than found on a page: which pages happen
  // to carry two selects at once is data-dependent, and a guard that quietly
  // finds only one is a guard that passes for the wrong reason. Two is the
  // minimum that can collide.
  //
  // What it guards: the first version of the component numbered its rows from
  // zero globally — dsopt-0, dsopt-1 — so any two open listboxes left colliding
  // ids behind. aria-activedescendant resolves an id against the whole
  // document, so a screen reader would have been pointed at a row belonging to
  // a different control.
  test('two open listboxes do not collide, and each announces itself', async ({ page }) => {
    await page.goto('');

    const report = await page.evaluate(() => {
      const host = document.createElement('div');
      host.id = 'ds-fixture';
      for (const name of ['alpha', 'beta']) {
        const s = document.createElement('select');
        s.name = name;
        s.title = name + ' picker';
        for (const v of ['one', 'two', 'three']) {
          const o = document.createElement('option');
          o.value = v;
          o.textContent = v;
          s.appendChild(o);
        }
        host.appendChild(s);
      }
      document.body.appendChild(host);
      (window as any).dozeSelect.upgradeAll(host);

      // Open both. Clicking the second would close the first, so the triggers
      // are opened directly — the rows are what matter, not the visibility.
      host.querySelectorAll('.ds-trigger').forEach((t) => (t as HTMLElement).click());

      const bad: string[] = [];
      const lists = Array.from(host.querySelectorAll('.ds-list'));
      const populated = lists.filter((l) => l.children.length).length;
      if (populated < 2) bad.push(`only ${populated} listbox(es) rendered rows; nothing was compared`);

      const seen = new Map<string, number>();
      for (const e of Array.from(document.querySelectorAll('[id]'))) {
        seen.set(e.id, (seen.get(e.id) ?? 0) + 1);
      }
      for (const [id, n] of seen) if (n > 1) bad.push(`#${id} appears ${n} times`);

      for (const t of Array.from(host.querySelectorAll('.ds-trigger'))) {
        if (t.getAttribute('aria-haspopup') !== 'listbox') bad.push('a trigger has no aria-haspopup=listbox');
        if (t.getAttribute('aria-expanded') === null) bad.push('a trigger has no aria-expanded');
        if (!((t.textContent || '').trim() || t.getAttribute('aria-label') || t.getAttribute('aria-labelledby'))) {
          bad.push('a trigger announces nothing');
        }
      }
      for (const l of lists) if (l.getAttribute('role') !== 'listbox') bad.push('a list is not role=listbox');

      // An active row has to resolve to a row in its OWN list, which is the
      // thing duplicate ids actually break.
      for (const l of lists) {
        const active = l.getAttribute('aria-activedescendant');
        if (!active) continue;
        const target = document.getElementById(active);
        if (!target || !l.contains(target)) bad.push(`aria-activedescendant ${active} resolves outside its list`);
      }

      host.remove();
      return bad;
    });

    expect(report, report.join('\n')).toEqual([]);
  });
});
