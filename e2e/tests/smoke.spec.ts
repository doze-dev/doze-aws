import { test, expect } from '@playwright/test';

// Validates the harness itself: the binary builds, boots on the fixed port,
// and the console shell renders. Real coverage lives in the other specs.
//
// NOTE: baseURL is `http://host:port/_console/`. Playwright resolves a
// leading-slash goto() against the *origin*, not baseURL's path — so
// `goto('/')` lands on the raw gateway root, not the console. Always goto()
// with a path relative to baseURL and NO leading slash (goto('') for the
// console home, goto('s3') for /_console/s3, etc).
test('console shell renders at /_console/', async ({ page }) => {
  await page.goto('');
  await expect(page.locator('.rail')).toBeVisible();
  await expect(page).toHaveTitle(/doze-aws/i);
});
