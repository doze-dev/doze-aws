import path from 'node:path';
import { test, expect } from '../fixtures/console';
import { postForm, createFunction } from '../fixtures/api';

// CloudWatch Logs: the group page's Subscriptions tab puts a filter on a
// group, lists it as a link to the function it forwards to, and removes it.
const LAMBDA_CODE_DIR = path.resolve(__dirname, '../fixtures/lambda-handler');

test.describe('log group subscriptions', () => {
  test('subscribe a group to a function, then remove the filter', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    const group = '/app/' + uniqueName('e2e-logs');
    const fnName = uniqueName('e2e-logs-sink');
    await createFunction(request, fnName, { code: LAMBDA_CODE_DIR });
    await postForm(request, 'logs/create', { name: group, days: 7 });

    await page.goto(`logs/group?name=${encodeURIComponent(group)}&tab=subscriptions`);
    await expect(page.getByText('No subscription filters')).toBeVisible();
    await page.locator('input[name="filter"]').fill('errors');
    await page.locator('input[name="pattern"]').fill('ERROR');
    await page.locator('select[name="destination"]').selectOption({ label: `Lambda · ${fnName}` });
    await page.getByRole('button', { name: 'Subscribe' }).click();
    await page.waitForURL(/tab=subscriptions/);
    await expect(page.locator('#flashbar')).toContainText('errors');
    const row = page.locator('table.tbl tr', { hasText: 'errors' });
    await expect(row).toContainText('ERROR');
    await expect(row.locator('a.conn-chip')).toHaveAttribute('href', new RegExp(`/lambda/${fnName}`));

    await row.getByRole('button').click();
    await confirmDialog('accept');
    await page.waitForURL(/tab=subscriptions/);
    await expect(page.getByText('No subscription filters')).toBeVisible();
  });
});

// The Metric filters tab: a rule that turns matching lines into a CloudWatch
// metric, the pattern tester beside it, and removal.
test.describe('log group metric filters', () => {
  test('create a metric filter, test its pattern, then remove it', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    const group = '/app/' + uniqueName('e2e-mf');
    await postForm(request, 'logs/create', { name: group, days: 7 });

    await page.goto(`logs/group?name=${encodeURIComponent(group)}&tab=metrics`);
    await expect(page.getByText('No metric filters')).toBeVisible();

    // The tester answers before anything is stored, which is the point of it.
    await page.locator('textarea[name="samples"]').fill('ERROR boom\nINFO fine');
    await page.locator('form[hx-post$="test-metric-filter"] input[name="pattern"]').fill('ERROR');
    await page.getByRole('button', { name: 'Test' }).click();
    await expect(page.locator('#mf-test')).toContainText('1 of 2 lines matched');
    await expect(page.getByText('No metric filters')).toBeVisible();

    await page.locator('form[hx-post$="/logs/metric-filter"] input[name="filter"]').fill('error-count');
    await page.locator('form[hx-post$="/logs/metric-filter"] input[name="pattern"]').fill('ERROR');
    await page.locator('input[name="namespace"]').fill('E2E');
    await page.locator('input[name="metric"]').fill('Errors');
    await page.getByRole('button', { name: 'Create filter' }).click();
    await page.waitForURL(/tab=metrics/);
    await expect(page.locator('#flashbar')).toContainText('Errors');
    const row = page.locator('table.tbl tr', { hasText: 'error-count' });
    await expect(row).toContainText('E2E / Errors');

    await row.getByRole('button').click();
    await confirmDialog('accept');
    await page.waitForURL(/tab=metrics/);
    await expect(page.getByText('No metric filters')).toBeVisible();
  });
});
