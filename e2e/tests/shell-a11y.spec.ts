import { test, expect } from '../fixtures/console';
import { createQueue } from '../fixtures/api';
import { fileURLToPath } from 'node:url';

// What the audit sweep (audit/crawl.ts) found in the shell and the shell now
// holds: axe over the places the shell draws, in both themes at the two widths
// the console is built for. The sweep needs a seeded stack and minutes; this
// is the part of it that must never come back.
//
//   color-contrast: the dimmed "New" on a create page (opacity halved its label)
//   scrollable-region-focusable: the dock's call list and the fidelity dialog's
//     body scroll, and a keyboard could not reach either
//   heading-order: the dialogs' headings, checked open

const AXE = fileURLToPath(new URL('../node_modules/axe-core/axe.min.js', import.meta.url));

async function violations(page: any) {
  await page.addScriptTag({ path: AXE });
  const res = await page.evaluate(async () => (window as any).axe.run(document, { resultTypes: ['violations'] }));
  return res.violations
    .filter((v: any) => v.impact !== 'minor')
    .map((v: any) => `${v.id} (${v.nodes.length}): ${v.nodes.slice(0, 3).map((n: any) => n.target.join(' ')).join(' | ')}`);
}

for (const theme of ['light', 'dark']) {
  for (const width of [1440, 1024]) {
    test.describe(`${theme} at ${width}`, () => {
      test.use({ viewport: { width, height: width === 1440 ? 900 : 768 } });
      test.beforeEach(async ({ page }) => {
        await page.addInitScript((t) => { try { localStorage.setItem('theme', t); } catch {} }, theme);
      });

      test('the create page, with its New dimmed, has nothing for axe', async ({ page }) => {
        await page.goto('sqs/create');
        const nw = page.locator('.listpane .new-link');
        await expect(nw).toHaveAttribute('aria-disabled', 'true');
        await expect(nw).toHaveAttribute('aria-current', 'page');
        expect(await violations(page)).toEqual([]);
      });

      test('a queue with its dock, and the fidelity dialog open, have nothing for axe', async ({ page, request, uniqueName }) => {
        const q = await createQueue(request, uniqueName('e2e-a11y'));
        await page.goto(`sqs/${q}`);
        // The dock lists the create call, so its list is there to scroll.
        await page.locator('.info-btn').first().click();
        await expect(page.locator('.info-body table, .info-body .tbl').first()).toBeVisible();
        expect(await violations(page)).toEqual([]);
      });

      test('the confirm dialog and the palette have nothing for axe', async ({ page, request, uniqueName }) => {
        const q = await createQueue(request, uniqueName('e2e-a11y'));
        await page.goto(`sqs/${q}`);
        await page.locator('button.danger', { hasText: /^Delete/ }).first().click();
        await expect(page.locator('#confirm-title')).toBeVisible();
        // Both overlays fade in; axe reads the colours of a half-drawn one.
        await page.waitForTimeout(500);
        expect(await violations(page)).toEqual([]);
        await page.keyboard.press('Escape');
        await page.keyboard.press('Control+k');
        await expect(page.locator('#pal-q')).toBeVisible();
        await page.waitForTimeout(500);
        expect(await violations(page)).toEqual([]);
      });
    });
  }
}

test.describe('the switcher at 1024', () => {
  test.use({ viewport: { width: 1024, height: 768 } });
  for (const theme of ['light', 'dark']) {
    test(`${theme}: closed and open, nothing for axe`, async ({ page, request, uniqueName }) => {
      await page.addInitScript((t) => { try { localStorage.setItem('theme', t); } catch {} }, theme);
      const q = await createQueue(request, uniqueName('e2e-a11y'));
      await page.goto(`sqs/${q}`);
      expect(await violations(page)).toEqual([]);
      await page.keyboard.press('/');
      await expect(page.locator('.listpane .lp-switch')).toHaveAttribute('aria-expanded', 'true');
      expect(await violations(page)).toEqual([]);
    });
  }
});
