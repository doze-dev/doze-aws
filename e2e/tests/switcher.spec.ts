import type { Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { createQueue } from '../fixtures/api';

// 1100 px and below: the list pane is a name switcher (board Narrow, decision D5
// in plan/03-shell-fidelity.md). The pane stops being a column; a strip above the
// detail shows the open resource's name, "1 of 4 queues", and opens the list as a
// menu with the filter and the keys the switcher owns. The detail gets the
// width the pane held. At 1101 and above the pane is the column it always was.

async function threeQueues(request: any, uniqueName: (p: string) => string) {
  const base = uniqueName('e2e-sw');
  const names: string[] = [];
  for (const s of ['a', 'b', 'c']) names.push(await createQueue(request, `${base}-${s}`));
  return { base, names };
}

const widthOf = async (page: Page, sel: string) => Math.round((await page.locator(sel).first().boundingBox())!.width);

for (const width of [1100, 1024, 900]) {
  test.describe(`at ${width}`, () => {
    test.use({ viewport: { width, height: 800 } });

    test('the list pane is not drawn; the switcher shows the open name and the detail has the width', async ({ page, request, uniqueName }) => {
      const { names } = await threeQueues(request, uniqueName);
      await page.goto(`sqs/${names[1]}`);
      const sw = page.locator('.listpane .lp-switch');
      await expect(sw).toBeVisible();
      await expect(sw).toContainText(names[1]);
      await expect(sw).toHaveAttribute('aria-expanded', 'false');
      // Closed, no row is drawn and the pane is a strip, not a column.
      await expect(page.locator('.listpane .li').first()).toBeHidden();
      const strip = (await page.locator('.listpane').boundingBox())!;
      expect(strip.height).toBeLessThan(64);
      expect(Math.round(strip.width)).toBeGreaterThan(width - 120);
      // The detail is the whole window less the rail (56 px at these widths).
      expect(await widthOf(page, '.detail')).toBe(width - 56);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    });

    test('it says where you are: "n of m", in the service\'s own word', async ({ page, request, uniqueName }) => {
      const { names } = await threeQueues(request, uniqueName);
      await page.goto(`sqs/${names[2]}`);
      await expect(page.locator('.listpane .lp-sw-of')).toHaveText(/^\d+ of \d+ queues$/);
      await page.goto('sqs');
      // Nowhere in particular: the count, with nothing to place.
      await expect(page.locator('.listpane .lp-sw-of')).toHaveText(/^\d+ queues?$/);
    });

    test('it opens as a menu, filters, and navigates with the keys it owns', async ({ page, request, uniqueName }) => {
      const { base, names } = await threeQueues(request, uniqueName);
      await page.goto(`sqs/${names[0]}`);
      const sw = page.locator('.listpane .lp-switch');
      await sw.click();
      await expect(sw).toHaveAttribute('aria-expanded', 'true');
      const filter = page.locator('.listpane .filter input');
      await expect(filter).toBeFocused();
      await expect(page.locator('.listpane .li', { hasText: names[2] })).toBeVisible();
      // The menu floats over the detail: it does not push it down.
      const menu = (await page.locator('.listpane .lp-menu').boundingBox())!;
      expect(menu.width).toBeLessThan(420);
      expect(menu.y + menu.height).toBeLessThanOrEqual(800);
      // The filter narrows it.
      await filter.fill(`${base}-c`);
      await expect(page.locator('.listpane .li:visible')).toHaveCount(1);
      await expect(page.locator('.listpane .li:visible')).toContainText(names[2]);
      // Escape closes and gives the page back.
      await page.keyboard.press('Escape');
      await expect(sw).toHaveAttribute('aria-expanded', 'false');
      await expect(page.locator('.listpane .li').first()).toBeHidden();
      // / opens it again with the filter focused (the filter was cleared).
      await page.locator('body').click({ position: { x: 5, y: 5 } });
      await page.keyboard.press('/');
      await expect(sw).toHaveAttribute('aria-expanded', 'true');
      await expect(filter).toBeFocused();
      await filter.fill(base);
      await page.keyboard.press('Escape');
      await expect(sw).toHaveAttribute('aria-expanded', 'false');
    });

    test('j k and Enter move over the menu and open the one under the cursor', async ({ page, request, uniqueName }) => {
      const { base, names } = await threeQueues(request, uniqueName);
      await page.goto(`sqs/${names[0]}`);
      // Closed, j opens the menu with a cursor on the first row.
      await page.locator('body').click({ position: { x: 5, y: 5 } });
      await page.keyboard.press('j');
      await expect(page.locator('.listpane .lp-switch')).toHaveAttribute('aria-expanded', 'true');
      await expect(page.locator('.listpane .li.cursor')).toHaveCount(1);
      // Narrow to the three of this test, then walk to the last one.
      await page.locator('.listpane .filter input').fill(base);
      await expect(page.locator('.listpane .li:visible')).toHaveCount(3);
      await page.locator('.listpane .filter input').blur();
      for (let i = 0; i < 6; i++) await page.keyboard.press('j');
      await page.keyboard.press('Enter');
      await expect(page).toHaveURL(new RegExp(`${names[2]}$`));
      await expect(page.locator('.listpane .lp-switch')).toContainText(names[2]);
      // The menu closed itself on the way.
      await expect(page.locator('.listpane .lp-switch')).toHaveAttribute('aria-expanded', 'false');
    });

    test('a click on a row opens that resource; a click outside closes the menu', async ({ page, request, uniqueName }) => {
      const { names } = await threeQueues(request, uniqueName);
      await page.goto(`sqs/${names[0]}`);
      await page.locator('.listpane .lp-switch').click();
      await page.locator('.listpane .li', { hasText: names[1] }).click();
      await expect(page).toHaveURL(new RegExp(`${names[1]}$`));
      await page.locator('.listpane .lp-switch').click();
      await expect(page.locator('.listpane .lp-switch')).toHaveAttribute('aria-expanded', 'true');
      await page.mouse.click(width - 40, 400);
      await expect(page.locator('.listpane .lp-switch')).toHaveAttribute('aria-expanded', 'false');
    });

    test('New and the filter are reachable from the strip and the menu; the info button stays', async ({ page, request, uniqueName }) => {
      const { names } = await threeQueues(request, uniqueName);
      await page.goto(`sqs/${names[0]}`);
      await expect(page.locator('.listpane .new-link')).toBeVisible();
      await expect(page.locator('.listpane .new-link')).toHaveText('New');
    });
  });
}

// The strip's words come from the rows, so a row's badge must not leak into the
// name ("default default" on the default bus) and a page of the service is not
// counted as one of its members (IAM's access log).
test.describe('what the strip says', () => {
  test.use({ viewport: { width: 1024, height: 768 } });
  test('a badge in the row is not part of the name', async ({ page }) => {
    await page.goto('eb/default');
    await expect(page.locator('.listpane .lp-sw-name')).toHaveText('default');
  });
  test('IAM: a page is named but not counted, a principal is both', async ({ page }) => {
    await page.goto('iam');
    await expect(page.locator('.listpane .lp-sw-name')).toHaveText('Access log');
    await expect(page.locator('.listpane .lp-sw-of')).toHaveText(/^\d+ principals?$/);
    await page.goto('iam/account');
    await expect(page.locator('.listpane .lp-sw-name')).toHaveText('Account');
  });
});

test.describe('at 1101 and above the pane is the column', () => {
  test.use({ viewport: { width: 1101, height: 800 } });
  test('232 px wide, the rows drawn, no switcher', async ({ page, request, uniqueName }) => {
    const { names } = await threeQueues(request, uniqueName);
    await page.goto(`sqs/${names[1]}`);
    await expect(page.locator('.listpane .lp-switch')).toBeHidden();
    expect(await widthOf(page, '.listpane')).toBe(232);
    await expect(page.locator('.listpane .li', { hasText: names[2] })).toBeVisible();
    await expect(page.locator('.listpane .li.on')).toContainText(names[1]);
  });

  test('the keys are the list cursor, as before', async ({ page, request, uniqueName }) => {
    const { names } = await threeQueues(request, uniqueName);
    await page.goto(`sqs/${names[1]}`);
    await page.locator('body').click({ position: { x: 5, y: 5 } });
    await page.keyboard.press('j');
    await expect(page.locator('.listpane .li.cursor')).toHaveCount(1);
  });
});

// The 900 px rule that never applied (a 168 px pane, overridden by the 232 px
// rule below it) is gone: 900 behaves as 1024 does, and at 901 and 1024 the
// detail has the same share of the window.
test.describe('900 is not a different layout from 1024', () => {
  test('the pane is a strip at both, and no sideways scroll at either', async ({ page, request, uniqueName }) => {
    const { names } = await threeQueues(request, uniqueName);
    for (const w of [900, 768, 1024]) {
      await page.setViewportSize({ width: w, height: 800 });
      await page.goto(`sqs/${names[0]}`);
      const pane = (await page.locator('.listpane').boundingBox())!;
      expect(pane.height, `${w}: strip`).toBeLessThan(64);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${w}: sideways`).toBe(true);
    }
  });
});
