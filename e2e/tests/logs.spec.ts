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
