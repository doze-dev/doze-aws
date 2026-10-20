import { fileURLToPath } from 'node:url';
import type { APIRequestContext, Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { postForm, createTopic, createQueue } from '../fixtures/api';
import { ACCOUNT_ID, ORIGIN } from '../playwright.config';

// The map (boards Map and Map-Large; internal/console/map.go): the wiring
// graph as flows. This server is shared with every other spec, so what is
// asserted is always about this spec's own, uniquely named resources: the
// flow page draws one flow whatever else is wired, and enough flows are made
// here to put the whole map past the point where it stops drawing wires.
//
// The graph is cached for a few seconds and a call made through the gateway
// does not bust it, so each assertion polls a fresh page load.

const AXE = fileURLToPath(new URL('../node_modules/axe-core/axe.min.js', import.meta.url));

async function violations(page: Page) {
  await page.evaluate(() =>
    Promise.all(document.getAnimations().filter((a) => a.effect?.getComputedTiming().iterations !== Infinity).map((a) => a.finished.catch(() => {}))),
  );
  await page.addScriptTag({ path: AXE });
  const res = await page.evaluate(async () => (window as any).axe.run(document, { resultTypes: ['violations'] }));
  return res.violations
    .filter((v: any) => v.impact !== 'minor')
    .map((v: any) => `${v.id} (${v.nodes.length}): ${v.nodes.slice(0, 3).map((n: any) => n.target.join(' ')).join(' | ')}`);
}

const queueARN = (name: string) => `arn:aws:sqs:us-east-1:000000000000:${name}`;

/** topic --sub--> queue --redrive--> dlq, made through the console's routes. */
async function arrange(request: APIRequestContext, name: string) {
  const dlq = await createQueue(request, `${name}-dlq`);
  const queue = await createQueue(request, `${name}-q`, { dlqMode: 'existing', dlqExisting: dlq, maxReceive: 5 });
  const topic = await createTopic(request, `${name}-t`);
  await postForm(request, `sns/${topic}/subscribe`, { protocol: 'sqs', endpoint: queueARN(queue) });
  return { topic, queue, dlq };
}

/** A read through the gateway, so the recorder holds a call that names the queue. */
async function touch(request: APIRequestContext, queue: string) {
  const r = await request.post(`${ORIGIN}/`, {
    headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': 'AmazonSQS.GetQueueAttributes' },
    data: { QueueUrl: `${ORIGIN}/${ACCOUNT_ID}/${queue}`, AttributeNames: ['All'] },
  });
  expect(r.ok()).toBe(true);
}

const flowURL = (id: string) => `map?flow=${encodeURIComponent(id)}`;

test.describe('one flow, drawn', () => {
  test('boxes are links with their icons, wires are labelled, and a call to an end makes its wires dark', async ({ page, request, uniqueName }) => {
    const { topic, queue, dlq } = await arrange(request, uniqueName('e2e-map'));

    await expect(async () => {
      await touch(request, queue);
      await page.goto(flowURL(`sns:${topic}`));
      await expect(page.locator('.map-box')).toHaveCount(3);
    }).toPass({ timeout: 15000, intervals: [300, 600, 1200] });

    // Each node: a link to its page, with its service's AWS icon.
    for (const [svc, name] of [['sns', topic], ['sqs', queue], ['sqs', dlq]] as const) {
      const box = page.locator('a.map-box', { hasText: name });
      await expect(box).toHaveAttribute('href', `/_console/${svc}/${name}`);
      await expect(box.locator('img.aws-ic')).toHaveAttribute('src', new RegExp(`/aws/${svc}\\.svg$`));
    }
    // Each wire: the kind of wiring it is, and the flow named for its source.
    await expect(page.locator('.map-label', { hasText: /^sub$/ })).toHaveCount(1);
    await expect(page.locator('.map-label', { hasText: /^redrive$/ })).toHaveCount(1);
    await expect(page.locator('.map-flow-h')).toHaveText(new RegExp(`^Flow 1 · from ${topic}`));
    // The legend names all seven wires.
    for (const kind of ['notify', 'sub', 'target', 'esm', 'redrive', 'dlq', 'dest']) {
      await expect(page.locator('.map-key .mono', { hasText: new RegExp(`^${kind}$`) })).toHaveCount(1);
    }
    // The queue was read: the wires at either end of it are dark, the flow is hot.
    await expect(page.locator('path.map-wire.hot')).toHaveCount(2);
    await expect(page.locator('.map-hot')).toHaveText('hot');
    // For a reader who cannot see the lines.
    await expect(page.locator('ul.sr-only li')).toHaveText([`${topic} to ${queue}, sub`, `${queue} to ${dlq}, redrive`]);
  });

  test('a flow nobody has called is not hot, and a resource that is not wired is not a flow', async ({ page, request, uniqueName }) => {
    const { topic, queue } = await arrange(request, uniqueName('e2e-map-cold'));
    await expect(async () => {
      await page.goto(flowURL(`sns:${topic}`));
      await expect(page.locator('.map-box')).toHaveCount(3);
    }).toPass({ timeout: 15000, intervals: [300, 600, 1200] });
    // The calls that made it are the console's own, which the wire does not hold.
    await expect(page.locator('path.map-wire.hot')).toHaveCount(0);
    await expect(page.locator('.map-hot')).toHaveCount(0);

    const bare = await createQueue(request, uniqueName('e2e-map-bare'));
    await page.goto(flowURL(`sqs:${bare}`));
    await expect(page.locator('.map-gone')).toContainText('That flow is not on the map any more.');
    expect(queue).toBeTruthy();
  });

  test('the keyboard reaches a box and Enter opens the resource', async ({ page, request, uniqueName }) => {
    const { topic, queue } = await arrange(request, uniqueName('e2e-map-kb'));
    await expect(async () => {
      await page.goto(flowURL(`sns:${topic}`));
      await expect(page.locator('.map-box')).toHaveCount(3);
    }).toPass({ timeout: 15000, intervals: [300, 600, 1200] });
    // Tab until a box has the focus: the region of wires, then each box in turn.
    const box = page.locator('a.map-box', { hasText: queue });
    for (let i = 0; i < 40; i++) {
      await page.keyboard.press('Tab');
      if (await box.evaluate((el) => el === document.activeElement)) break;
    }
    await expect(box).toBeFocused();
    await page.keyboard.press('Enter');
    await page.waitForURL(new RegExp(`/sqs/${queue}$`));
    await expect(page.locator('.det-title')).toContainText(queue);
  });
});

for (const theme of ['light', 'dark']) {
  for (const width of [1440, 1024]) {
    test.describe(`axe, ${theme} at ${width}`, () => {
      test.use({ viewport: { width, height: width === 1440 ? 900 : 768 } });
      test.beforeEach(async ({ page }) => {
        await page.addInitScript((t) => { try { localStorage.setItem('theme', t); } catch {} }, theme);
      });

      test('a drawn flow and the cards have nothing for axe', async ({ page, request, uniqueName }) => {
        const { topic, queue } = await arrange(request, uniqueName('e2e-map-axe'));
        await expect(async () => {
          await touch(request, queue);
          await page.goto(flowURL(`sns:${topic}`));
          await expect(page.locator('.map-box')).toHaveCount(3);
          await expect(page.locator('.map-hot')).toHaveCount(1);
        }).toPass({ timeout: 15000, intervals: [300, 600, 1200] });
        expect(await violations(page)).toEqual([]);
      });

      test('the page past twelve resources (cards, the fold, hot only, find) has nothing for axe', async ({ page, request, uniqueName }) => {
        // Five flows of three are fifteen wired resources: cards, whatever else is wired.
        const prefix = uniqueName('e2e-map-axe-cards');
        const flows = [] as Awaited<ReturnType<typeof arrange>>[];
        for (let i = 0; i < 5; i++) flows.push(await arrange(request, `${prefix}-${i}`));
        await expect(async () => {
          await touch(request, flows[0]!.queue);
          await page.goto(`map?q=${prefix}`);
          await expect(page.locator('a.map-card')).toHaveCount(5);
          await expect(page.locator('a.map-card.hot')).toHaveCount(1);
        }).toPass({ timeout: 20000, intervals: [400, 800, 1600] });
        expect(await violations(page)).toEqual([]);
        await page.goto('map?hot=1');
        await expect(page.locator('.map-seg a.on')).toHaveText('Hot only');
        expect(await violations(page)).toEqual([]);
        await page.goto('map');
        await expect(page.locator('.map-scale')).toBeVisible();
        expect(await violations(page)).toEqual([]);
      });

      test('a flow wider than the page scrolls in a region the keyboard can reach', async ({ page, request, uniqueName }) => {
        // Four queues, each the dead-letter queue of the one before: four columns.
        const n = uniqueName('e2e-map-wide');
        const q3 = await createQueue(request, `${n}-3`);
        const q2 = await createQueue(request, `${n}-2`, { dlqMode: 'existing', dlqExisting: q3, maxReceive: 5 });
        const q1 = await createQueue(request, `${n}-1`, { dlqMode: 'existing', dlqExisting: q2, maxReceive: 5 });
        const q0 = await createQueue(request, `${n}-0`, { dlqMode: 'existing', dlqExisting: q1, maxReceive: 5 });
        await expect(async () => {
          await page.goto(flowURL(`sqs:${q0}`));
          await expect(page.locator('.map-box')).toHaveCount(4);
        }).toPass({ timeout: 15000, intervals: [300, 600, 1200] });
        const wrap = page.locator('.map-wrap');
        expect(await wrap.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
        await expect(wrap).toHaveAttribute('tabindex', '0');
        await expect(wrap).toHaveAttribute('role', 'region');
        expect(await violations(page)).toEqual([]);
      });
    });
  }
}

test.describe('at scale', () => {
  // Seven flows of three put the map past twelve wired resources, whatever else
  // the shared server holds.
  test('past twelve resources a flow is a card; find, hot only and the fold work from the keyboard', async ({ page, request, uniqueName }) => {
    const prefix = uniqueName('e2e-scale');
    const flows = [] as Awaited<ReturnType<typeof arrange>>[];
    for (let i = 0; i < 7; i++) flows.push(await arrange(request, `${prefix}-${i}`));
    const hot = flows[3]!;

    await expect(async () => {
      await touch(request, hot.queue);
      await page.goto(`map?q=${prefix}`);
      await expect(page.locator('a.map-card')).toHaveCount(7);
    }).toPass({ timeout: 20000, intervals: [400, 800, 1600] });

    // The page says why it stopped drawing, and what a card is.
    await expect(page.locator('.map-scale')).toHaveText('Past 12 resources the page stops drawing wires. A flow is one card; open it to see its wires.');
    await expect(page.locator('.map-note')).toContainText('7 flows match');
    // A card: the flow, its size, and its first resources by kind.
    const card = page.locator('a.map-card', { hasText: `from ${prefix}-3-t` });
    await expect(card.locator('.map-count')).toHaveText(/^3 resources · hot$/);
    await expect(card.locator('.map-line')).toHaveText([`SNS ${hot.topic}`, `→ SQS ${hot.queue}`, `→ SQS ${hot.dlq}`]);
    await expect(page.locator('a.map-card.hot')).toHaveCount(1);

    // "/" goes to the find box; typing and Enter narrow to one flow.
    await page.goto('map');
    await expect(page.locator('a.map-card').first()).toBeVisible();
    await page.keyboard.press('/');
    await expect(page.locator('.map-find input')).toBeFocused();
    await page.keyboard.type(`${prefix}-5-q`);
    await page.keyboard.press('Enter');
    await page.waitForURL(/[?&]q=/);
    await expect(page.locator('a.map-card')).toHaveCount(1);
    await expect(page.locator('a.map-card .map-title')).toHaveText(`from ${prefix}-5-t`);

    // Hot only: the flow that was read, and not the others.
    await expect(async () => {
      await touch(request, hot.queue);
      await page.goto(`map?hot=1&q=${prefix}`);
      await expect(page.locator('a.map-card')).toHaveCount(1);
    }).toPass({ timeout: 15000, intervals: [400, 800, 1600] });
    await expect(page.locator('a.map-card .map-title')).toHaveText(`from ${prefix}-3-t`);
    await expect(page.locator('.map-seg a.on')).toHaveText('Hot only');

    // Open the flow: its wires, drawn, from the card.
    await page.locator('a.map-card').focus();
    await page.keyboard.press('Enter');
    await page.waitForURL(/[?&]flow=/);
    await expect(page.locator('.map-box')).toHaveCount(3);
    await expect(page.locator('.map-flow-h')).toContainText(`from ${hot.topic}`);
  });

  test('the fold: seven cards and "N more flows", and Show them shows them', async ({ page, request, uniqueName }) => {
    const prefix = uniqueName('e2e-fold');
    for (let i = 0; i < 9; i++) await arrange(request, `${prefix}-${i}`);
    await expect(async () => {
      await page.goto('map');
      await expect(page.locator('.map-fold')).toBeVisible();
    }).toPass({ timeout: 20000, intervals: [400, 800, 1600] });
    await expect(page.locator('a.map-card')).toHaveCount(7);
    await expect(page.locator('.map-fold .map-title')).toHaveText(/^\d+ more flows$/);
    await expect(page.locator('.map-fold .map-count')).toHaveText(/^of \d+( to \d+)? resources each$/);
    await page.locator('.map-fold a', { hasText: 'Show them' }).click();
    await page.waitForURL(/[?&]all=1/);
    await expect(page.locator('.map-fold')).toHaveCount(0);
    expect(await page.locator('a.map-card').count()).toBeGreaterThan(7);
  });
});

test.describe('reaching the map', () => {
  test('from the rail', async ({ page }) => {
    await page.goto('');
    const link = page.locator('nav.rail a.ri', { hasText: 'The map' });
    await expect(link).toHaveAttribute('href', '/_console/map');
    await link.click();
    await page.waitForURL(/\/_console\/map$/);
    await expect(page.locator('.det-title')).toContainText('The map');
    await expect(page.locator('nav.rail a.ri.on')).toHaveAttribute('aria-label', 'The map');
    await page.reload();
    await expect(page).toHaveTitle(/^The map — doze-aws$/);
  });

  test('from the palette, by name', async ({ page, openPalette }) => {
    await page.goto('');
    await openPalette();
    await page.fill('#pal-q', 'the map');
    const item = page.locator('#pal-list .pal-item', { hasText: 'The map' }).first();
    await expect(item).toBeVisible();
    await page.keyboard.press('Enter');
    await page.waitForURL(/\/_console\/map$/);
  });

  test('the key bar names what the page answers to', async ({ page, request, uniqueName }) => {
    const { topic } = await arrange(request, uniqueName('e2e-map-keys'));
    await expect(async () => {
      await page.goto(flowURL(`sns:${topic}`));
      await expect(page.locator('.map-box')).toHaveCount(3);
    }).toPass({ timeout: 15000, intervals: [300, 600, 1200] });
    await expect(page.locator('#keybar')).toContainText('open the resource');
    await expect(page.locator('#keybar')).toContainText('jump or act');
  });
});
