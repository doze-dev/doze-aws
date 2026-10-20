import * as fs from 'node:fs';
import * as path from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect } from '../fixtures/console';
import { createQueue } from '../fixtures/api';

// The fidelity dialog (boards Fidelity and Kms-19, foundation D-c): "what is
// real here", on every service. It is read from docs/SUPPORT.md when it is
// opened: the tiers counted, the table of operations, and the ledger's
// "Differences from AWS" as cards. These drive it as a person does, on every
// service's page, and hold what the harness's text check cannot: that every
// service has one, that nothing is read until it is opened, how it sits at both
// widths, and that every way out of it works.

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const SUPPORT = fs.readFileSync(path.resolve(__dirname, '../../docs/SUPPORT.md'), 'utf8');

/** The rail's name for each service, and the ledger section(s) it covers. */
const SERVICES: { key: string; label: string; ledgers: string[] }[] = [
  { key: 's3', label: 'S3', ledgers: ['s3'] },
  { key: 'ddb', label: 'DynamoDB', ledgers: ['dynamodb'] },
  { key: 'sqs', label: 'SQS', ledgers: ['sqs'] },
  { key: 'sns', label: 'SNS', ledgers: ['sns'] },
  { key: 'eb', label: 'EventBridge', ledgers: ['eventbridge'] },
  { key: 'kinesis', label: 'Kinesis', ledgers: ['kinesis'] },
  { key: 'lambda', label: 'Lambda', ledgers: ['lambda'] },
  { key: 'apigw', label: 'API Gateway', ledgers: ['apigateway', 'apigatewayv2'] },
  { key: 'sfn', label: 'Step Functions', ledgers: ['stepfunctions'] },
  { key: 'logs', label: 'CloudWatch Logs', ledgers: ['logs'] },
  { key: 'cw', label: 'CloudWatch', ledgers: ['cloudwatch'] },
  { key: 'kms', label: 'KMS', ledgers: ['kms'] },
  { key: 'sm', label: 'Secrets Manager', ledgers: ['secretsmanager'] },
  { key: 'ssm', label: 'Parameter Store', ledgers: ['ssm'] },
  { key: 'iam', label: 'IAM', ledgers: ['iam'] },
  { key: 'cfn', label: 'CloudFormation', ledgers: ['cloudformation'] },
];

/** The bullets of a ledger's "### Differences from AWS", as the ledger has them. */
function differencesOf(ledger: string): number {
  const at = SUPPORT.indexOf(`<!-- svc:${ledger} -->`);
  if (at < 0) throw new Error(`no ledger ${ledger}`);
  const rest = SUPPORT.slice(at + 1);
  const next = rest.indexOf('<!-- svc:');
  const section = next < 0 ? rest : rest.slice(0, next);
  const h = section.indexOf('\n### Differences from AWS');
  if (h < 0) throw new Error(`${ledger} has no Differences from AWS`);
  const body = section.slice(h + 1).split('\n').slice(1);
  const end = body.findIndex((l) => l.startsWith('### '));
  return (end < 0 ? body : body.slice(0, end)).filter((l) => l.startsWith('- ')).length;
}

test.describe('the fidelity dialog, on every service', () => {
  for (const s of SERVICES) {
    test(`${s.label}: titled for it, its table, and the ledger's differences as cards`, async ({ page }) => {
      await page.goto(s.key);
      await page.locator('.info-btn').first().click();
      const dlg = page.getByRole('dialog', { name: `${s.label} fidelity` });
      await expect(dlg).toBeVisible();
      await expect(dlg.locator('.fid-title')).toHaveText(`${s.label} · what is real here`);
      await expect(dlg.getByText('What the emulator genuinely implements (functional), accepts without local effect (cosmetic), and refuses honestly (stub).')).toBeVisible();

      // The tiers, counted; the table, one row an operation family.
      await expect(dlg.locator('.fid-tiers .pill.ok')).toHaveText(/^F\s+\d+ functional$/);
      await expect(dlg.locator('.fid-table tbody tr').first()).toBeVisible();
      expect(await dlg.locator('.fid-table tbody tr').count()).toBeGreaterThan(2);
      for (const th of ['Tier', 'Operations', 'Notes']) await expect(dlg.locator('.fid-table thead').first()).toContainText(th);
      // Every row carries its tier, and what it says (a family whose ledger note
      // is empty draws a dash, never nothing).
      for (const row of await dlg.locator('.fid-table tbody tr').all()) {
        await expect(row.locator('td').nth(0)).toHaveText(/^(F|C|S|—)$/);
        await expect(row.locator('td').nth(1)).not.toHaveText('');
        await expect(row.locator('td').nth(2)).not.toHaveText('');
      }

      // "Differences from AWS": one card per bullet the ledger lists, whole.
      await expect(dlg.getByRole('heading', { name: 'Differences from AWS' }).first()).toBeVisible();
      const want = s.ledgers.map(differencesOf).reduce((a, b) => a + b, 0);
      await expect(dlg.locator('.fid-callout')).toHaveCount(want);

      await page.getByRole('button', { name: 'Close', exact: true }).last().click();
      await expect(dlg).toBeHidden();
    });
  }

  test('API Gateway names both kinds of API, each with its own table and differences', async ({ page }) => {
    await page.goto('apigw');
    await page.locator('.info-btn').first().click();
    const dlg = page.getByRole('dialog', { name: 'API Gateway fidelity' });
    await expect(dlg.getByRole('heading', { name: 'REST APIs' })).toBeVisible();
    await expect(dlg.getByRole('heading', { name: 'HTTP APIs' })).toBeVisible();
    await expect(dlg.locator('.fid-table')).toHaveCount(2);
    await expect(dlg.getByRole('heading', { name: 'Differences from AWS' })).toHaveCount(2);
  });

  test("the emulator's own calls are in the table, last, with a dash for a tier", async ({ page }) => {
    await page.goto('sqs');
    await page.locator('.info-btn').first().click();
    const rows = page.locator('.fid-table tbody tr');
    await expect(rows.first()).toBeVisible();
    const last = rows.last();
    await expect(last.locator('td').nth(0)).toHaveText('—');
    await expect(last.locator('td').nth(1)).toHaveText('DozePeek');
    await expect(last.locator('td').nth(2)).toContainText('doze extension: read-only full-queue inspection');
  });

  test('a stub and a cosmetic row are counted and drawn in their own tiers', async ({ page }) => {
    await page.goto('kms');
    await page.locator('.info-btn').first().click();
    await expect(page.locator('.fid-tiers .pill', { hasText: 'stub' })).toHaveText(/^S\s+2 stub$/);
    const stubs = page.locator('.fid-table tbody tr', { has: page.locator('td:first-child', { hasText: /^S$/ }) });
    await expect(stubs).toHaveCount(2);
    await expect(stubs.first()).toContainText('Grants');
    // The ledger's S3 rows have cosmetic ones: C is warm, F green, S neutral.
    await page.goto('s3');
    await page.locator('.info-btn').first().click();
    await expect(page.locator('.fid-tiers .pill.warn')).toHaveText(/^C\s+\d+ cosmetic$/);
    expect(await page.locator('.fid-table tbody tr td:first-child .pill.warn').count()).toBeGreaterThan(0);
  });
});

test.describe('the fidelity dialog, opened and closed', () => {
  test('nothing is read until it is opened, and a second opening reads nothing more', async ({ page }) => {
    const asked: string[] = [];
    page.on('request', (r) => {
      if (/\/_console\/info\//.test(r.url())) asked.push(r.url());
    });
    await page.goto('ddb');
    await page.waitForLoadState('networkidle');
    expect(asked, 'a page load must not read the ledger').toEqual([]);
    await expect(page.locator('.fid-card')).toHaveCount(0);

    await page.locator('.info-btn').first().click();
    await expect(page.locator('.fid-card').first()).toBeVisible();
    expect(asked).toHaveLength(1);
    expect(asked[0]).toMatch(/\/_console\/info\/ddb$/);
    await page.keyboard.press('Escape');
    await page.locator('.info-btn').first().click();
    await expect(page.locator('.fid-card').first()).toBeVisible();
    expect(asked).toHaveLength(1);
  });

  test('every way out works: esc, the esc button, Close, a click outside', async ({ page }) => {
    await page.goto('sns');
    const dlg = page.getByRole('dialog', { name: 'SNS fidelity' });
    const open = async () => {
      await page.locator('.info-btn').first().click();
      await expect(dlg).toBeVisible();
    };
    await open();
    await page.keyboard.press('Escape');
    await expect(dlg).toBeHidden();

    await open();
    await dlg.locator('.fid-esc').click();
    await expect(dlg).toBeHidden();

    await open();
    await dlg.getByRole('button', { name: 'Close', exact: true }).last().click();
    await expect(dlg).toBeHidden();

    await open();
    await page.mouse.click(8, 8);
    await expect(dlg).toBeHidden();
    // Focus is back where it was opened from.
    await expect(page.locator('.info-btn').first()).toBeFocused();
  });

  test('is 940 wide at 1440 and fits the window at 1024, the body scrolling and Close staying', async ({ page }) => {
    // The dialog pops in; a box measured mid-pop is a scaled one.
    await page.emulateMedia({ reducedMotion: 'reduce' });
    for (const w of [1440, 1024]) {
      await page.setViewportSize({ width: w, height: 900 });
      await page.goto('sqs');
      await page.locator('.info-btn').first().click();
      const card = page.locator('.dialog.fid');
      await expect(page.locator('.fid-card').first()).toBeVisible();
      const b = (await card.boundingBox())!;
      expect(b.width, `${w}: width`).toBeLessThanOrEqual(940);
      if (w === 1440) expect(Math.round(b.width), '1440: width').toBe(940);
      expect(b.x, `${w}: left edge`).toBeGreaterThanOrEqual(0);
      expect(b.x + b.width, `${w}: right edge`).toBeLessThanOrEqual(w);
      expect(b.y, `${w}: top`).toBeGreaterThanOrEqual(0);
      expect(b.height, `${w}: height`).toBeLessThanOrEqual(760);
      expect(b.y + b.height, `${w}: bottom`).toBeLessThanOrEqual(900);
      // SQS's ledger is longer than the dialog: the body scrolls, and the
      // title above it and the Close below it are still on screen.
      const body = page.locator('.fid-b');
      expect(await body.evaluate((e) => e.scrollHeight > e.clientHeight)).toBe(true);
      await expect(page.locator('.fid-title')).toBeInViewport();
      await expect(card.getByRole('button', { name: 'Close', exact: true }).last()).toBeInViewport();
      await page.keyboard.press('Escape');
    }
  });

  test('no note is clipped: a long one wraps in its column rather than running out of the dialog', async ({ page }) => {
    await page.goto('sqs');
    await page.locator('.info-btn').first().click();
    await expect(page.locator('.fid-card').first()).toBeVisible();
    const clipped = await page.locator('.fid-table td, .fid-callout').evaluateAll((els) =>
      els.filter((e) => e.scrollWidth > e.clientWidth + 1).map((e) => (e.textContent ?? '').slice(0, 40)),
    );
    expect(clipped).toEqual([]);
    // The longest ledger note, StartMessageMoveTask's, is whole on the page.
    await expect(page.locator('.fid-table tbody tr', { hasText: 'StartMessageMoveTask' })).toContainText('a missing queue is ResourceNotFoundException');
  });
});

test.describe('where the info button is', () => {
  test('in the list header with nothing open, beside the name once a queue is', async ({ page, request, uniqueName }) => {
    const q = await createQueue(request, uniqueName('e2e-fid'));
    await page.goto('sqs');
    await expect(page.locator('.listpane .lp-h .info-btn')).toHaveCount(1);
    await expect(page.locator('.det-title .info-btn')).toHaveCount(0);

    await page.goto(`sqs/${q}`);
    await expect(page.locator('.listpane .lp-h .info-btn')).toHaveCount(0);
    const btn = page.locator('.det-title .info-btn');
    await expect(btn).toHaveCount(1);
    await expect(btn).toHaveAccessibleName('What of SQS is real here');
    // Beside the name: on the title's line, after the name's text.
    const nameBox = (await page.locator('.det-title').boundingBox())!;
    const btnBox = (await btn.boundingBox())!;
    expect(btnBox.y).toBeGreaterThanOrEqual(nameBox.y);
    expect(btnBox.y + btnBox.height).toBeLessThanOrEqual(nameBox.y + nameBox.height);
    await expect(page.getByRole('heading', { level: 1 })).not.toContainText('what is real here');

    await btn.click();
    await expect(page.locator('.fid-title')).toHaveText('SQS · what is real here');
    await page.keyboard.press('Escape');
    await expect(page.locator('.fid-title')).toBeHidden();
    await expect(btn).toBeFocused();
  });
});
