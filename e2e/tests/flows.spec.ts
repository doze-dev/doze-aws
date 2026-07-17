import type { APIRequestContext } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { postForm, createTopic, createQueue, createBucket } from '../fixtures/api';

// Flows is the console's home surface (`/` maps to it): a live-polled wiring
// map (#flow-canvas, data-live -> /flows.json every 4s) that unions the graph
// of SNS subs, EventBridge targets, Lambda ESMs/destinations, SQS redrive,
// and S3 notifications into one bordered card per connected component
// (console/client_flow.go's layoutFlows), plus a "Not connected" card for
// singleton (unwired) resources. See console/templates/flows.html and
// console/client_flow.go for the exact markup/labels asserted below.
//
// This server is shared with many other specs and already has substantial
// cross-service wiring on it, so every assertion here is scoped to a
// freshly created, uniquely named resource — never a total/exact count.

// Local-AWS identity convention (awsident.ARN): the SNS subscribe form's
// endpoint <select> renders exactly this ARN shape as each <option>'s value
// (see console/templates/sns.html), so arranging a subscription via the API
// needs to match it. Mirrors the helper in sns.spec.ts.
const queueARN = (name: string) => `arn:aws:sqs:us-east-1:000000000000:${name}`;

async function subscribeQueue(request: APIRequestContext, topic: string, queueName: string) {
  await postForm(request, `sns/${topic}/subscribe`, {
    protocol: 'sqs',
    endpoint: queueARN(queueName),
  });
}

test.describe('Flows home', () => {
  test('renders the live wiring canvas with chip summary and legend', async ({ page }) => {
    await page.goto('');

    // .first(): a known quirk of this build — no template declares
    // hx-ext="morph" anywhere, so the data-live poll's default
    // "morph:outerHTML" swap silently falls back to htmx's plain innerHTML
    // swap once a poll actually lands a change, nesting a second
    // id="flow-canvas" node inside the original. That only matters once the
    // 4s poll has fired with a real change (see the two tests below, which
    // route around it entirely by waiting on a non-id selector); here it's
    // just cheap insurance since this test's assertions run well inside
    // that window.
    const canvas = page.locator('#flow-canvas').first();
    await expect(canvas).toBeVisible();
    await expect(canvas).toHaveAttribute('data-live', '/_console/flows.json');
    await expect(canvas).toHaveAttribute('data-live-ms', '4000');

    // Summary chips: flow(s), resources, connections — values grow over time
    // on this shared server, so only assert the labels are present, never
    // exact counts.
    const chips = canvas.locator('.chips > .chip');
    await expect(chips.filter({ hasText: /flows?$/ }).first()).toBeVisible();
    await expect(chips.filter({ hasText: 'resources' }).first()).toBeVisible();
    await expect(chips.filter({ hasText: 'connections' }).first()).toBeVisible();

    // Edge-kind legend: one entry per FlowEdge.Kind the graph can render.
    const legend = canvas.locator('.fl-legend');
    await expect(legend).toBeVisible();
    for (const label of ['sub', 'target', 'mapping', 'destination', 'notify', 'redrive']) {
      await expect(legend.locator('span', { hasText: label })).toBeVisible();
    }
  });

  test('a fresh SNS to SQS subscription appears live as its own flow card', async ({
    page,
    request,
    uniqueName,
    waitForLive,
  }) => {
    const topic = uniqueName('e2e-flow-topic');
    const queue = uniqueName('e2e-flow-queue');
    await createTopic(request, topic);
    await createQueue(request, queue);

    // Load the Flows page BEFORE wiring the two together, so the assertion
    // below genuinely exercises the live poll (#flow-canvas's data-live
    // refresh), not just a fresh server-rendered page.
    await page.goto('');
    await expect(page.locator('#flow-canvas').first()).toBeVisible();

    await subscribeQueue(request, topic, queue);

    // Poll on .flow-grid, not #flow-canvas: this build never declares
    // hx-ext="morph" anywhere, so the live poll's default "morph:outerHTML"
    // swap silently degrades to htmx's plain innerHTML swap the first time
    // it actually lands a change — nesting a second id="flow-canvas" node
    // inside the original rather than replacing it. That breaks any
    // *id*-scoped locator (Playwright's strict mode rejects the 2 matches),
    // but .flow-grid (a class, rendered fresh once per swap) always stays
    // singular, so waiting on it sidesteps the bug entirely.
    await waitForLive('.flow-grid', (text) => text.includes(topic) && text.includes(queue));

    // flowLabel() names a flow after its upstream source; a topic with no
    // incoming edge in its own flow wins over the queue, so the card header
    // reads "{topic} flow". Scope to non-unwired cards so this never
    // accidentally matches the "Not connected" card.
    const card = page.locator('.flow-card:not(.unwired)', { hasText: topic });
    await expect(card).toBeVisible();
    await expect(card).toContainText(queue);

    // Node anchors carry the real console URL for that resource.
    const topicNode = card.locator('a.fl-node', { hasText: topic });
    await expect(topicNode).toHaveAttribute('href', `/_console/sns/${topic}`);
    const queueNode = card.locator('a.fl-node', { hasText: queue });
    await expect(queueNode).toHaveAttribute('href', `/_console/sqs/${queue}`);

    // Exactly one "sub" edge for this brand-new topic/queue pair.
    await expect(card.locator('path.fl-k-sub')).toHaveCount(1);
  });

  test('an unwired bucket shows up in the Not connected section, live', async ({
    page,
    request,
    uniqueName,
    waitForLive,
  }) => {
    const bucket = uniqueName('e2e-flow-bucket');

    // Load Flows first, then create the bucket, so this also exercises the
    // live poll rather than only a fresh server render.
    await page.goto('');
    await expect(page.locator('#flow-canvas').first()).toBeVisible();

    await createBucket(request, bucket);

    // See the sibling test above for why this polls .flow-grid rather than
    // #flow-canvas (a real morph-swap-nesting quirk in this build).
    await waitForLive('.flow-grid', (text) => text.includes(bucket));

    const unwiredCard = page.locator('.flow-card.unwired');
    await expect(unwiredCard).toBeVisible();
    await expect(unwiredCard.locator('.flow-card-h')).toContainText('Not connected');

    const chip = unwiredCard.locator('a.unwired-chip', { hasText: bucket });
    await expect(chip).toBeVisible();
    await expect(chip).toHaveAttribute('href', `/_console/s3/${bucket}`);
    await expect(chip.locator('.uc-sv')).toHaveText('s3');
  });
});
