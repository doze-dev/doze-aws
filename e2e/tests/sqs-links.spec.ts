import { test, expect } from '../fixtures/console';
import type { Page } from '@playwright/test';
import { createQueue } from '../fixtures/api';
import { ACCOUNT_ID, ORIGIN } from '../playwright.config';

// A 404 on "/sqs%2F<dead-letter queue>" was seen once in a link crawl of the
// console and never reproduced (plan/services/sqs.md, SQS-54). This is that
// crawl, kept, in a browser: every link on every SQS page, in each state a
// queue can be in, resolved the way the browser resolves it (a relative href
// from the page it is on, a link a script wrote) and fetched; none may be
// refused with a 404 (or any 4xx/5xx), and none may name "/sqs%2F". The
// server-rendered half is internal/console/sqs_links_test.go.

const SQS = `${ORIGIN}/`;

/** A /_console/sqs/<name> link, to a queue that is not one of `own`, that is refused because the queue is gone:
 *  the name is in the stack's listing no longer. A name this test made is never excused. */
async function vanishedElsewhere(request: import('@playwright/test').APIRequestContext, link: string, own: string[]): Promise<boolean> {
  const m = new URL(link).pathname.match(/^\/_console\/sqs\/([^/]+)$/);
  if (!m) return false;
  const name = decodeURIComponent(m[1]);
  if (own.includes(name) || /^e2e-lk-/.test(name)) return false;
  const again = await request.get(link);
  if (again.status() !== 400) return false;
  const list = await (await request.get(`${ORIGIN}/_console/sqs`)).text();
  return !list.includes(`data-name="${name}"`);
}

async function sqsCall(request: import('@playwright/test').APIRequestContext, op: string, body: Record<string, unknown>) {
  const r = await request.post(SQS, {
    headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': `AmazonSQS.${op}` },
    data: JSON.stringify(body),
  });
  expect(r.status(), `${op}: ${await r.text()}`).toBe(200);
  return r.json();
}

/** Every internal link, hx-get and live region on the page as it is now, as the browser resolves it. */
async function linksOn(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out = new Set<string>();
    for (const e of Array.from(document.querySelectorAll('a[href], [hx-get], [data-live]'))) {
      if (e.tagName === 'FORM') continue; // sent with the form's fields, not bare
      const raw = e.getAttribute('href') ?? e.getAttribute('hx-get') ?? e.getAttribute('data-live');
      if (!raw || raw.startsWith('#') || /^(javascript|mailto|tel):/.test(raw)) continue;
      try {
        const u = new URL(raw, document.baseURI);
        if (u.origin !== location.origin || !u.pathname.startsWith('/_console/') || u.pathname.startsWith('/_console/static/')) continue;
        u.hash = '';
        out.add(u.href);
      } catch {
        /* not a URL: not a link */
      }
    }
    return [...out];
  });
}

/** What can be opened on a queue page that shows more links: the composer, the edit form, the redrive dialog. */
async function openWhatOpens(page: Page) {
  for (const opener of [
    page.locator('.dock-bar'),
    page.getByRole('button', { name: 'Edit settings' }),
    page.getByRole('button', { name: /^Redrive/ }).first(),
    page.getByRole('button', { name: /Show raw attributes/ }),
  ]) {
    // Only what is on this page: a missing control is not waited for.
    if ((await opener.count()) === 0) continue;
    await opener.click({ timeout: 2000 }).catch(() => {});
    await page.keyboard.press('Escape').catch(() => {});
  }
}

test.describe('every link on the SQS pages opens', () => {
  test('in each state a queue can be in', async ({ page, request, uniqueName }) => {
    test.setTimeout(240_000); // the full suite runs this beside forty other specs
    // A queue with a dead-letter queue made alongside, one that names an existing
    // one, a FIFO queue with its own, a name at the edge of the rule, and a bare one.
    const orders = await createQueue(request, uniqueName('e2e-lk-orders'), { dlqMode: 'new', maxReceive: 2, visibility: 30 });
    const audit = await createQueue(request, uniqueName('e2e-lk-audit'), { dlqMode: 'existing', dlqExisting: `${orders}-dlq`, maxReceive: 3 });
    const fifo = await createQueue(request, uniqueName('e2e-lk-depot'), { fifo: true, dlqMode: 'new' });
    const long = await createQueue(request, 'e2e-lk-' + 'n'.repeat(60), { dlqMode: 'none' });
    const bare = await createQueue(request, uniqueName('e2e-lk-bare'), { dlqMode: 'none' });
    const url = (q: string) => `${ORIGIN}/${ACCOUNT_ID}/${q}`;

    // Messages waiting, held, and received past maxReceiveCount (so the dead-letter queue has rows and
    // a "fed by" line), and a finished move task (the redrive card's row and its "Open" link).
    for (let i = 0; i < 3; i++) await sqsCall(request, 'SendMessage', { QueueUrl: url(orders), MessageBody: JSON.stringify({ n: i }) });
    for (let i = 0; i < 3; i++) await sqsCall(request, 'ReceiveMessage', { QueueUrl: url(orders), MaxNumberOfMessages: 10, VisibilityTimeout: 0 });
    await sqsCall(request, 'SendMessage', { QueueUrl: url(orders), MessageBody: 'held' });
    await sqsCall(request, 'ReceiveMessage', { QueueUrl: url(orders), MaxNumberOfMessages: 1, VisibilityTimeout: 600 });
    await sqsCall(request, 'SendMessage', { QueueUrl: url(fifo), MessageBody: 'v17', MessageGroupId: 'g', MessageDeduplicationId: 'd1' });
    const dest = await createQueue(request, uniqueName('e2e-lk-dest'), { dlqMode: 'none' });
    await sqsCall(request, 'StartMessageMoveTask', {
      SourceArn: `arn:aws:sqs:us-east-1:${ACCOUNT_ID}:${orders}-dlq`,
      DestinationArn: `arn:aws:sqs:us-east-1:${ACCOUNT_ID}:${dest}`,
    }).catch(() => {});
    // A topic feeding a queue, for the neighbours strip.
    const topic = uniqueName('e2e-lk-topic');
    await request.post(SQS, { form: { Action: 'CreateTopic', Name: topic, Version: '2010-03-31' } });
    await request.post(SQS, {
      form: { Action: 'Subscribe', TopicArn: `arn:aws:sns:us-east-1:${ACCOUNT_ID}:${topic}`, Protocol: 'sqs', Endpoint: `arn:aws:sqs:us-east-1:${ACCOUNT_ID}:${orders}`, Version: '2010-03-31' },
    });
    await page.waitForTimeout(600); // the move task is paced

    const queues = [orders, `${orders}-dlq`, audit, fifo, `${fifo.replace(/\.fifo$/, '')}-dlq.fifo`, long, bare, dest];
    const start: string[] = [`${ORIGIN}/_console/sqs`, `${ORIGIN}/_console/sqs/create`];
    for (const q of queues) {
      start.push(`${ORIGIN}/_console/sqs/${encodeURIComponent(q)}`);
      for (const tab of ['consume', 'config', 'tags']) start.push(`${ORIGIN}/_console/sqs/${encodeURIComponent(q)}?tab=${tab}`);
    }

    const fetched = new Map<string, number>(); // target -> status
    const bad: string[] = [];
    const visited = new Set<string>();
    const queue = [...start];
    const sqsPage = (u: string) => new URL(u).pathname.startsWith('/_console/sqs') && !/\/(messages|work)\b/.test(new URL(u).pathname);

    while (queue.length > 0 && visited.size < 150) {
      const target = queue.shift()!;
      if (visited.has(target)) continue;
      visited.add(target);
      await page.goto(target);
      await page.waitForTimeout(200); // the page is server-rendered; scripts that write links have run
      const collected = new Set(await linksOn(page));
      if (sqsPage(target)) {
        await openWhatOpens(page);
        for (const l of await linksOn(page)) collected.add(l);
      }
      for (const link of collected) {
        if (fetched.has(link)) continue;
        const r = await request.get(link, { maxRedirects: 5 });
        fetched.set(link, r.status());
        // The queue list is the whole stack's, and the suite runs other specs beside this one
        // that make and delete queues of their own: a link to a queue that is not one of this
        // test's, and that has gone since the list was drawn, answers 400 QueueDoesNotExist and is
        // not a broken link (the list lent the name, and the same page for it is fetched again
        // below to prove it is gone and not mis-spelled).
        if (r.status() === 400 && (await vanishedElsewhere(request, link, queues))) continue;
        if (r.status() >= 400) bad.push(`${r.status()} ${link}\n  found on ${target}`);
        // A page of SQS's own is crawled for more links, whether or not a tab led to it.
        if (r.status() === 200 && sqsPage(link) && !visited.has(link)) queue.push(link);
      }
    }

    const sqsEscaped = [...fetched.keys(), ...visited].filter((u) => /\/_console\/sqs%2[Ff]/.test(u));
    expect(sqsEscaped, 'no link has the slash after /sqs escaped').toEqual([]);
    expect(bad, `links that did not open:\n${bad.join('\n')}`).toEqual([]);
    // The crawl reached the pages it is meant to: each queue's own page, its three tabs, the create page.
    for (const q of queues) expect([...fetched.keys(), ...visited].some((u) => new URL(u).pathname === `/_console/sqs/${q}`), `a link to ${q}`).toBe(true);
    expect(fetched.size, 'the crawl followed a real number of links').toBeGreaterThan(40);
    test.info().annotations.push({ type: 'crawl', description: `${visited.size} pages visited, ${fetched.size} links fetched, none refused` });
    console.log(`sqs links: ${visited.size} pages visited, ${fetched.size} links fetched`);
  });
});
