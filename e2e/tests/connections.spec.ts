import * as path from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { postForm, createTopic, createQueue, createFunction } from '../fixtures/api';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Per-resource "Connections" strip: the shared {{define "connections"}}
// partial in console/templates/panes.html renders a resource's 1-hop wiring
// as "Fed by" (Conn.Upstream) / "Drains to" (Conn.Downstream) chip columns,
// each chip service-colored (--svc-<svc>) and linking to the neighbor's own
// detail page. Backed by backend.Neighbors() in console/client_flow.go,
// which scans the full wiring graph for edges whose From/To match this
// node's id and reports the edge Kind ("sub", "target", "esm", ...).
//
// Confirmed via the templates which detail pages actually include the
// strip: SNS (sns.html), SQS (sqs.html), S3 (s3.html), and EventBridge rule
// (eb.html) all call {{template "connections" ...}} verbatim. The Lambda
// function page (lambda.html) does NOT use this shared partial — it renders
// the identical Neighborhood data (same Conn/Neighbor shape) through its own
// bespoke "lam-ov" triggers→function→destinations diagram instead (`.lo-card`
// chips under "Triggers"/"Destinations" headers, built by handlers_lambda.go's
// lambdaDiagram()). So the Lambda leg below asserts `.lo-card`, not
// `.conn-chip` — everything else asserts the shared `.conn-chip` strip.
//
// backend.nodeURL() always points an "eb" neighbor at
// "/eb/default/rule/{name}" regardless of which bus the rule actually lives
// on (a graph-node-id quirk, not something this spec should exercise) — so
// the EventBridge rule below is deliberately created on the "default" bus,
// keeping the rule's real URL and its neighbors' chip hrefs consistent.
//
// This server is shared with many other specs and already has substantial
// cross-service wiring, so every assertion here is scoped to freshly
// created, uniquely named resources — never a total/exact count.

const queueARN = (name: string) => `arn:aws:sqs:us-east-1:000000000000:${name}`;

// A Lambda function only needs a code path that exists on disk to be
// *created* (lambda/functions.go's materializeCode does an os.Stat on
// Code.S3Key at create time); it doesn't need a real handler unless
// invoked. This spec never invokes, so the fixture handler's *source*
// directory (no build step) is enough to satisfy that check.
const LAMBDA_CODE_DIR = path.resolve(__dirname, '../fixtures/lambda-handler');

// backend.graphCached (console/client.go) memoizes the full wiring graph that
// Neighbors() reads from, with a 750ms TTL shared across every request this
// long-lived, multi-spec server receives — a deliberate perf tradeoff (it
// collapses a burst of Flows-page polls into one crawl) but it means a
// detail-page GET landing within that window of our arrange POSTs above can
// legitimately observe a pre-wiring snapshot. Poll-reload past that window
// instead of asserting on a single page load, the same "no fixed tick" spirit
// as the console fixture's own `waitForLive` (which polls a live htmx region;
// this polls a plain server-rendered page since the Connections strip isn't
// htmx-live).
async function gotoWired(page: Page, urlPath: string, isWired: () => Promise<boolean>) {
  await expect(async () => {
    await page.goto(urlPath);
    expect(await isWired()).toBe(true);
  }).toPass({ timeout: 5000, intervals: [200, 400, 800] });
}

// One connected chain, arranged via the API in a single serial test, then
// visited node-by-node so each detail page's own Connections strip can be
// asserted against its real neighbors:
//
//   topic --sub--> queue1
//   rule  --target--> queue2      (EventBridge "default" bus)
//   queue3 --esm--> fn            (SQS-triggered Lambda event source mapping)
test.describe.configure({ mode: 'serial' });

test.describe('Connections strip', () => {
  let topic: string;
  let queue1: string;
  let ruleName: string;
  let queue2: string;
  let queue3: string;
  let fnName: string;

  test('arrange: SNS->SQS sub, EB rule->SQS target, SQS->Lambda ESM', async ({
    request,
    uniqueName,
  }) => {
    topic = await createTopic(request, uniqueName('e2e-conn-topic'));
    queue1 = await createQueue(request, uniqueName('e2e-conn-q1'));
    await postForm(request, `sns/${topic}/subscribe`, {
      protocol: 'sqs',
      endpoint: queueARN(queue1),
    });

    queue2 = await createQueue(request, uniqueName('e2e-conn-q2'));
    ruleName = uniqueName('e2e-conn-rule');
    await postForm(request, 'eb/default/create-rule', {
      name: ruleName,
      pattern: JSON.stringify({ source: ['e2e.connections'] }),
    });
    await postForm(request, `eb/default/rule/${ruleName}/add-target`, {
      arn: queueARN(queue2),
    });

    queue3 = await createQueue(request, uniqueName('e2e-conn-q3'));
    fnName = uniqueName('e2e-conn-fn');
    await createFunction(request, fnName, { code: LAMBDA_CODE_DIR });
    await postForm(request, `lambda/${fnName}/add-mapping`, { queue: queue3, batch: 1 });
  });

  test('SNS topic page: "Drains to" shows the subscribed SQS queue', async ({ page }) => {
    const chip = page.locator('.conn-col.conn-out .conn-chip', { hasText: queue1 });
    await gotoWired(page, `sns/${topic}`, () => chip.isVisible());

    await expect(chip.locator('.cc-k')).toHaveText('sub');
    await expect(chip).toHaveAttribute('href', `/_console/sqs/${queue1}`);
    // Service-colored bar: sqs neighbor from the SNS topic's downstream side.
    await expect(chip.locator('.cc-bar')).toHaveAttribute('style', /--svc-sqs/);

    // "Fed by" is empty for a freshly created topic with no publishers wired.
    const upCol = page.locator('.conn-col:not(.conn-out)');
    await expect(upCol.locator('.conn-chip', { hasText: topic })).toHaveCount(0);
  });

  test('SQS queue1 page: "Fed by" shows the SNS topic', async ({ page }) => {
    const chip = page.locator('.conn-col:not(.conn-out) .conn-chip', { hasText: topic });
    await gotoWired(page, `sqs/${queue1}`, () => chip.isVisible());

    await expect(chip.locator('.cc-k')).toHaveText('sub');
    await expect(chip).toHaveAttribute('href', `/_console/sns/${topic}`);
    await expect(chip.locator('.cc-bar')).toHaveAttribute('style', /--svc-sns/);
  });

  test('EventBridge rule page: "Drains to" shows the target SQS queue', async ({ page }) => {
    const chip = page.locator('.conn-col.conn-out .conn-chip', { hasText: queue2 });
    await gotoWired(page, `eb/default/rule/${ruleName}`, () => chip.isVisible());

    await expect(chip.locator('.cc-k')).toHaveText('target');
    await expect(chip).toHaveAttribute('href', `/_console/sqs/${queue2}`);
  });

  test('SQS queue2 page: "Fed by" shows the EventBridge rule', async ({ page }) => {
    const chip = page.locator('.conn-col:not(.conn-out) .conn-chip', { hasText: ruleName });
    await gotoWired(page, `sqs/${queue2}`, () => chip.isVisible());

    await expect(chip.locator('.cc-k')).toHaveText('target');
    await expect(chip).toHaveAttribute('href', `/_console/eb/default/rule/${ruleName}`);
  });

  test('Lambda function page: Triggers shows the ESM-mapped SQS queue', async ({ page }) => {
    // Lambda renders its own "lam-ov" diagram (not the shared .conn-chip
    // strip) for the identical Neighborhood data — only real neighbors are
    // <a class="lo-card">; the "no triggers"/"no destinations" placeholders
    // are plain (unlinked) divs, so this selector is unambiguous.
    const card = page.locator('a.lo-card', { hasText: queue3 });
    await gotoWired(page, `lambda/${fnName}`, () => card.isVisible());

    await expect(card.locator('.lo-k')).toHaveText('sqs · event source');
    await expect(card).toHaveAttribute('href', `/_console/sqs/${queue3}`);
  });

  test('SQS queue3 page: "Drains to" shows the Lambda function', async ({ page }) => {
    const chip = page.locator('.conn-col.conn-out .conn-chip', { hasText: fnName });
    await gotoWired(page, `sqs/${queue3}`, () => chip.isVisible());

    await expect(chip.locator('.cc-k')).toHaveText('esm');
    await expect(chip).toHaveAttribute('href', `/_console/lambda/${fnName}`);
    await expect(chip.locator('.cc-bar')).toHaveAttribute('style', /--svc-lambda/);
  });

  test('clicking a connections chip navigates to the neighbor\'s real detail page', async ({
    page,
  }) => {
    const chip = page.locator('.conn-col.conn-out .conn-chip', { hasText: queue1 });
    await gotoWired(page, `sns/${topic}`, () => chip.isVisible());
    await chip.click();

    await page.waitForURL(new RegExp(`/sqs/${queue1}$`));
    // Confirm this is genuinely queue1's own detail page, not just any
    // navigation: its title names it, and its own Connections strip
    // reciprocally shows it's fed by the topic we came from.
    await expect(page.locator('.det-title')).toContainText(queue1);
    const backChip = page.locator('.conn-col:not(.conn-out) .conn-chip', { hasText: topic });
    await expect(backChip).toBeVisible();
    await expect(backChip).toHaveAttribute('href', `/_console/sns/${topic}`);
  });
});
