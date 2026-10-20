import type { APIRequestContext, Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { BASE_URL } from '../playwright.config';

// A2: "Refused here" and "Not built" link the operation's row in the fidelity
// dialog (and a not-built one links docs/not-built.md). A link opens the dialog
// scrolled to the row with the row marked; the same link, followed from a page
// that has no dialog for the service, opens the service's page and then the
// dialog at the row.
//
// The not-built call is Kinesis's SubscribeToShard, a stub the ledger records
// (tier S) and the service answers with the legacy stub stamp, which the console
// reads as Not built.

const GATEWAY_ROOT = new URL('/', BASE_URL).toString();
const NOT_BUILT_DOC = 'https://github.com/doze-dev/doze-aws/blob/main/docs/not-built.md';

async function subscribeToShard(request: APIRequestContext) {
  return request.post(GATEWAY_ROOT, {
    headers: { 'Content-Type': 'application/x-amz-json-1.1', 'X-Amz-Target': 'Kinesis_20131202.SubscribeToShard' },
    data: JSON.stringify({ ConsumerARN: 'arn:aws:kinesis:us-east-1:000000000000:stream/s/consumer/c:1', ShardId: 'shardId-000000000000', StartingPosition: { Type: 'LATEST' } }),
  });
}

/** The dialog is open on the row for `op`, which is marked, the only one marked, and on screen. */
async function expectOpenAtRow(page: Page, op: string, tier: string) {
  const dialog = page.getByRole('dialog', { name: 'Kinesis fidelity' });
  await expect(dialog).toBeVisible();
  const hit = dialog.locator('tr.fid-hit');
  await expect(hit).toHaveCount(1);
  await expect(hit).toHaveAttribute('aria-current', 'true');
  await expect(hit.locator(`.fid-opn[data-op="${op}"]`)).toHaveText(op);
  await expect(hit.locator('.pill')).toHaveText(tier);
  // Scrolled to: the row is inside the dialog's scroll region and in the window.
  await expect(hit).toBeInViewport();
  const inside = await hit.evaluate((tr) => {
    const body = tr.closest('.fid-b')!.getBoundingClientRect();
    const r = tr.getBoundingClientRect();
    return r.top >= body.top - 1 && r.bottom <= body.bottom + 1;
  });
  expect(inside, 'the marked row is within the dialog body, not behind its header or foot').toBe(true);
  return dialog;
}

test.describe('the ledger row a refusal points at', () => {
  test('the wire inspector explains a not-built call and links its row and the document', async ({ page, request }) => {
    const res = await subscribeToShard(request);
    expect(res.status()).toBe(400);
    await page.goto('traffic');
    const row = page.locator('#traffic-feed .tr-row', { hasText: 'SubscribeToShard' }).first();
    await expect(row).toBeVisible();
    // The feed replaces its rows on a changed tick: click until the inspector shows the call.
    const d = page.locator('#t-drawer-inner');
    await expect(async () => {
      await row.locator('.act').click({ timeout: 2000 });
      await expect(d.locator('.td-title')).toHaveText('SubscribeToShard', { timeout: 2000 });
    }).toPass({ timeout: 15000 });

    await expect(d.locator('.td-h .st')).toHaveText('400 not built');
    const why = d.locator('.td-callout .note');
    await expect(why).toContainText('Nothing is wrong with the request.');
    const rowLink = why.getByRole('link', { name: 'The ledger row for SubscribeToShard →' });
    await expect(rowLink).toHaveAttribute('href', '/_console/kinesis?fidelity=SubscribeToShard');
    const doc = why.getByRole('link', { name: /docs\/not-built\.md →$/ });
    await expect(doc).toHaveAttribute('href', new RegExp(`^${NOT_BUILT_DOC.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(#declined|#not-yet)?$`));
    await expect(doc).toHaveAttribute('target', '_blank');

    // A click opens the inspector's own dialog at the row; the page does not navigate.
    const before = page.url();
    await rowLink.click();
    const dialog = await expectOpenAtRow(page, 'SubscribeToShard', 'S');
    expect(page.url()).toBe(before);
    // The row says what the ledger says of it.
    await expect(dialog.locator('tr.fid-hit')).toContainText('enhanced fan-out delivers over an HTTP/2 event stream');
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();

    // The ledger card's own button opens the same dialog at the same row.
    await d.getByRole('button', { name: 'The Kinesis ledger' }).click();
    await expectOpenAtRow(page, 'SubscribeToShard', 'S');
  });

  test('followed to a service page, the link opens its dialog at the row and leaves the address clean', async ({ page }) => {
    await page.goto('kinesis?fidelity=SubscribeToShard');
    await expectOpenAtRow(page, 'SubscribeToShard', 'S');
    expect(new URL(page.url()).searchParams.has('fidelity')).toBe(false);
    // Closed and reloaded, it stays closed: the address no longer asks.
    await page.keyboard.press('Escape');
    await page.reload();
    await expect(page.getByRole('dialog', { name: 'Kinesis fidelity' })).toBeHidden();
  });

  test('a service name with no operation opens its ledger without marking a row', async ({ page }) => {
    await page.goto('kinesis?fidelity=');
    const dialog = page.getByRole('dialog', { name: 'Kinesis fidelity' });
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('.fid-table')).toBeVisible();
    await expect(dialog.locator('tr.fid-hit')).toHaveCount(0);
  });

  test('the marked row reads in both themes', async ({ page }) => {
    for (const theme of ['light', 'dark']) {
      await page.emulateMedia({ colorScheme: theme as 'light' | 'dark' });
      await page.goto('kinesis?fidelity=SubscribeToShard');
      const dialog = await expectOpenAtRow(page, 'SubscribeToShard', 'S');
      const [mark, plain] = await Promise.all([
        dialog.locator('tr.fid-hit td').first().evaluate((e) => getComputedStyle(e).backgroundColor),
        dialog.locator('tr:not(.fid-hit) td').first().evaluate((e) => getComputedStyle(e).backgroundColor),
      ]);
      expect(mark, `the marked row is not painted like the others in the ${theme} theme`).not.toBe(plain);
    }
  });
});
