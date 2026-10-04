import path from 'node:path';
import { fileURLToPath } from 'node:url';
import type { APIRequestContext } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { postForm, createFunction } from '../fixtures/api';
import { ORIGIN } from '../playwright.config';

// ES modules have no __dirname.
const __dirname = path.dirname(fileURLToPath(import.meta.url));

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
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('errors');
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
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('Errors');
    const row = page.locator('table.tbl tr', { hasText: 'error-count' });
    await expect(row).toContainText('E2E / Errors');

    await row.getByRole('button').click();
    await confirmDialog('accept');
    await page.waitForURL(/tab=metrics/);
    await expect(page.getByText('No metric filters')).toBeVisible();
  });
});

// ---- the group lifecycle: create, tail, streams, retention, delete ----

/** Arrange-only: a CloudWatch Logs API call on the gateway (JSON 1.1). */
async function logsApi(request: APIRequestContext, op: string, body: object) {
  const res = await request.post(ORIGIN + '/', {
    headers: {
      'content-type': 'application/x-amz-json-1.1',
      'x-amz-target': `Logs_20140328.${op}`,
    },
    data: body,
  });
  expect(res.status(), await res.text()).toBe(200);
}

test.describe('log group lifecycle', () => {
  test('create a group from the palette, tail it, drop a stream, set retention, delete it', async ({
    page,
    request,
    uniqueName,
    openPalette,
    confirmDialog,
    waitForToast,
  }) => {
    const group = '/app/' + uniqueName('e2e-lg');
    const stream = uniqueName('worker');

    await test.step('create through the palette’s “Create log group”', async () => {
      // The list pane has no New link for logs; the palette is the way in.
      await page.goto('logs');
      await openPalette();
      await page.locator('#pal-q').fill('Create log group');
      await expect(page.locator('.pal-item', { hasText: 'Create log group' })).toBeVisible();
      await page.keyboard.press('Enter');
      await page.waitForURL(/\/logs\/create$/);
      await page.locator('input[name="name"]').fill(group);
      await page.locator('select[name="days"]').selectOption('7');
      await page.getByRole('button', { name: 'Create log group' }).click();
      await expect(page.locator("#toasts .toast:not(.err)").last()).toContainText(`Log group “${group}” created`);
      await page.waitForURL(/\/logs\/group\?name=/);
      await expect(page.locator('.det-title')).toContainText(group);
      await expect(page.locator('.det-title .badge')).toHaveText('7d retention');
    });

    await test.step('the tail picks up a line written after the page opened', async () => {
      const tail = page.locator('#log-tail');
      await expect(tail).toContainText('No lines in the last hour');
      await logsApi(request, 'CreateLogStream', { logGroupName: group, logStreamName: stream });
      const marker = `tail-${Date.now()}`;
      await logsApi(request, 'PutLogEvents', {
        logGroupName: group,
        logStreamName: stream,
        logEvents: [{ timestamp: Date.now(), message: `ERROR ${marker}` }],
      });
      await expect(tail).toContainText(marker, { timeout: 15000 });
      await expect(tail.locator('.log-count')).toContainText('1 line');
    });

    await test.step('delete the stream from the Streams tab', async () => {
      await page.getByRole('link', { name: 'Streams' }).click();
      await page.waitForURL(/tab=streams/);
      const row = page.locator('table.tbl tr', { hasText: stream });
      await expect(row).toBeVisible();
      await row.getByRole('button').click();
      await confirmDialog('accept');
      await expect(page.locator("#toasts .toast:not(.err)").last()).toContainText('Stream deleted');
      await expect(page.getByText('No streams yet.')).toBeVisible();
    });

    await test.step('change retention on the Settings tab', async () => {
      await page.getByRole('link', { name: 'Settings' }).click();
      await page.waitForURL(/tab=settings/);
      await page.locator('select[name="days"]').selectOption('30');
      await page.getByRole('button', { name: 'Save' }).click();
      await expect(page.locator("#toasts .toast:not(.err)").last()).toContainText(`Retention set to 30 days for ${group}`);
      await expect(page.locator('.det-title .badge')).toHaveText('30d retention');
      await expect(page.locator('select[name="days"]')).toHaveValue('30');

      await page.locator('select[name="days"]').selectOption('0');
      await page.getByRole('button', { name: 'Save' }).click();
      await expect(page.locator("#toasts .toast:not(.err)").last()).toContainText(`Retention cleared for ${group}`);
      await expect(page.locator('.det-title .badge')).toHaveText('default retention');
    });

    await test.step('delete the group', async () => {
      await page.locator('.det-title').getByRole('button', { name: 'Delete' }).click();
      await confirmDialog('accept');
      await expect(page.locator("#toasts .toast:not(.err)").last()).toContainText(`Log group “${group}” deleted`);
      await page.waitForURL(/\/logs$/);
      await expect(page.locator('.listpane')).not.toContainText(group);
    });
  });
});

test('the list pane offers New, like every other service', async ({ page }) => {
  await page.goto('logs');
  await page.locator('.listpane .new-link').click();
  await page.waitForURL(/\/logs\/create$/);
});
