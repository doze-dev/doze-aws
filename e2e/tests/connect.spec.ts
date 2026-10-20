import { test, expect } from '../fixtures/console';
import { ACCOUNT_ID, ORIGIN, REGION } from '../playwright.config';

// Connect (board Connect): the endpoint written out for the AWS CLI profile, the
// environment, Terraform and the deploy tools, as three columns of cards, with
// the region this instance runs in and the address the page was reached on, and
// a check that runs STS GetCallerIdentity and says who answered. The same spec
// runs under a second identity (E2E_ACCOUNT_ID, E2E_REGION), where a default
// written into any block fails it.

const ENDPOINT = ORIGIN; // the page is reached at ORIGIN, so that is the address it must write

const BLOCKS: { name: string; text: string }[] = [
  { name: '~/.aws/config', text: `[profile doze]\nregion = ${REGION}\noutput = json\nendpoint_url = ${ENDPOINT}` },
  { name: '~/.aws/credentials', text: '[doze]\naws_access_key_id = test\naws_secret_access_key = test' },
  { name: 'shell', text: `export AWS_ENDPOINT_URL=${ENDPOINT}\nexport AWS_ACCESS_KEY_ID=test\nexport AWS_SECRET_ACCESS_KEY=test\nexport AWS_REGION=${REGION}` },
  {
    name: 'provider.tf',
    text: [
      'provider "aws" {',
      `  region                      = "${REGION}"`,
      '  access_key                  = "test"',
      '  secret_key                  = "test"',
      '  skip_credentials_validation = true',
      '  skip_requesting_account_id  = true',
      '  s3_use_path_style           = true',
      '',
      '  endpoints {',
      ...['s3      ', 'sqs     ', 'sns     ', 'dynamodb', 'lambda  '].map((s) => `    ${s.trimEnd().padEnd(8)} = "${ENDPOINT}"`),
      '  }',
      '}',
    ].join('\n'),
  },
  { name: 'deploy', text: 'aws cloudformation deploy \\\n  --template-file template.yaml --stack-name shop\nsam deploy --stack-name shop --s3-bucket artifacts\ncdk bootstrap && cdk deploy' },
];

test.describe('Connect', () => {
  test.beforeEach(async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']).catch(() => {});
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('connect');
  });

  test('the endpoint, and the sentences the board has', async ({ page }) => {
    await expect(page.getByRole('heading', { level: 1 })).toContainText('Connect');
    await expect(page.locator('.det-title .pill.mono')).toHaveText(ENDPOINT);
    for (const t of [
      'Point your tooling at this endpoint. Every block is the same fact in a different dialect, built from the address you reached the console on.',
      'A named profile keeps later commands short and leaves your real AWS credentials untouched.',
      'endpoint_url in a profile needs AWS CLI v2.13 or newer. On anything older, use the environment block.',
      'No files touched. Every AWS SDK reads these.',
      'There is no doze-specific file format. These read the environment and need no further setup.',
      "Runs STS GetCallerIdentity against this stack. It does not appear on the wire — that tail is your app's traffic only.",
    ])
      await expect(page.getByText(t, { exact: true })).toBeVisible();
    for (const h of ['AWS CLI profile', 'Environment', 'Terraform', 'Deploy with what you already have', 'Check it works'])
      await expect(page.getByRole('heading', { name: h, exact: true })).toBeVisible();
    // What the page used to add and the board does not draw.
    await expect(page.getByText('aws --profile doze')).toHaveCount(0);
  });

  test('every block says this instance: its address, its region, its credentials', async ({ page }) => {
    const cards = page.locator('.cn-card:has(.cn-code)');
    await expect(cards).toHaveCount(BLOCKS.length);
    for (const [i, b] of BLOCKS.entries()) {
      // Blocks are named in caps on the board; the name is the text, the caps are CSS.
      const card = cards.nth(i);
      await expect(card.locator('.cn-cap > span')).toHaveText(b.name);
      expect(await card.locator('.cn-code').textContent(), b.name).toBe(b.text);
    }
    // No default of any other identity is written into the page.
    const html = await page.locator('.cn-grid').innerText();
    if (REGION !== 'us-east-1') expect(html).not.toContain('us-east-1');
    expect(html).not.toContain('127.0.0.1:4566');
  });

  test('Copy copies what the block shows, and says so', async ({ page, waitForToast }) => {
    const cards = page.locator('.cn-card:has(.cn-code)');
    for (const [i, b] of BLOCKS.entries()) {
      const card = cards.nth(i);
      await page.evaluate(() => navigator.clipboard.writeText('(nothing yet)'));
      await card.getByRole('button', { name: `Copy ${b.name}`, exact: true }).click();
      expect(await waitForToast(), b.name).toContain('Copied');
      expect(await page.evaluate(() => navigator.clipboard.readText()), b.name).toBe(b.text);
    }
  });

  test('Run get-caller-identity names who answered, and keeps its sentence', async ({ page, request }) => {
    const note = page.locator('.cn-verify .cn-note');
    await expect(page.locator('#cn-result')).toBeHidden();
    await page.getByRole('button', { name: 'Run get-caller-identity' }).click();
    const result = page.locator('#cn-result');
    await expect(result.locator('.pill.ok')).toHaveText('Connected');
    await expect(result.locator('.cn-arn')).toHaveText(`arn:aws:iam::${ACCOUNT_ID}:user/test`);
    await expect(note).toContainText('It does not appear on the wire');
    // A second run answers the same (it replaces, it does not stack).
    await page.getByRole('button', { name: 'Run get-caller-identity' }).click();
    await expect(result.locator('.pill.ok')).toHaveCount(1);

    // And it is not on the wire, as the note says: the tail is the app's traffic only.
    const feed = await request.get(ORIGIN + '/_console/traffic/feed');
    expect(feed.status()).toBe(200);
    expect(await feed.text()).not.toContain('GetCallerIdentity');
  });

  test('is three columns at 1440 and two at 1024, with nothing clipped or spilling', async ({ page }) => {
    const xs = async () => page.locator('.cn-sec').evaluateAll((cs) => cs.map((c) => Math.round(c.getBoundingClientRect().x)));
    const x3 = await xs();
    expect(new Set(x3).size, `1440: columns at ${x3}`).toBe(3);

    await page.setViewportSize({ width: 1024, height: 800 });
    await page.goto('connect');
    const x2 = await xs();
    expect(new Set(x2).size, `1024: columns at ${x2}`).toBe(2);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    // A card stays inside the pane; a long line scrolls inside its well, not out of the card.
    const spill = await page.locator('.cn-card').evaluateAll((cs) =>
      cs.filter((c) => c.scrollWidth > c.clientWidth + 1 || c.getBoundingClientRect().right > window.innerWidth).length,
    );
    expect(spill).toBe(0);
  });
});
