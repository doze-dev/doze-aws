// The shell boards that are the page's chrome and its rules rather than a
// screen of their own: Anatomy, Create, Dark, Narrow, Palette, States, Teaching.
// (Main, Wire-*, Connect, Fidelity and the maps are drivers of their own, in
// shell.ts.)
//
// Each driver puts the real page into the state its board draws, with the data
// the board's text says. What the board says that no page could say (the "rule"
// panels, the numbered callouts) is annotation and is allow-listed with its
// reason in allow/<Board>.txt; what the page does not say and could is built.

import type { Ctx, DriverTable } from './types';
import { DLQ, FIFO, LOYALTY, MAIN } from '../harbour';
import { PRODUCTS, RECEIPTS, enableTrigger, seedShell } from '../harbour-shell';

const orderBody = (id: string, lines: number, pence?: number) =>
  JSON.stringify(pence === undefined ? { orderId: id, lines } : { orderId: id, lines, totalPence: pence });

/**
 * The queue as Narrow draws it: nothing waiting, two messages held
 * (04812 already received once, so its receive makes it "2 of 3"; 04811 on its
 * first), and the Lambda trigger ENABLED, so the strip says "read by". Done on a
 * scratch copy of the fixture, in the order that keeps it true: held first, and
 * only then the trigger, because an enabled trigger reads what is visible.
 */
async function heldQueue(c: Ctx, bodies: { first: string; second: string }) {
  const o = await c.instance('own');
  const U = o.queueUrl(MAIN);
  const purge = await o.sqs('PurgeQueue', { QueueUrl: U });
  if (purge.status !== 200) throw new Error(`purge: ${JSON.stringify(purge.body)}`);
  await o.sqs('SendMessage', { QueueUrl: U, MessageBody: bodies.second });
  await o.sqs('ReceiveMessage', { QueueUrl: U, MaxNumberOfMessages: 10, VisibilityTimeout: 0 });
  await o.sqs('SendMessage', { QueueUrl: U, MessageBody: bodies.first });
  // The console receives both: they are held by this page, as the Consume tab says.
  const p = c.page;
  await p.goto(o.url(`sqs/${MAIN}?tab=consume`));
  await p.getByRole('button', { name: 'Receive' }).click();
  await p.locator('.rcv-row').nth(1).waitFor();
  await enableTrigger(o);
  await p.goto(o.url(`sqs/${MAIN}?tab=consume`));
  await p.locator('.rcv-row').nth(1).waitFor();
  return o;
}

export const drivers: DriverTable = {
  // The annotated queue page: the eight parts of a resource page. It draws the queue as
  // the service's own Queue board does (the Messages tab, three waiting, one message
  // opened in place), on the same copy of the fixture: an enabled mapping whose reader is
  // busy, so the messages are still waiting and the strip says "read by".
  Anatomy: {
    async drive(c) {
      const o = await c.instance('reader');
      // The board opens the order that has been received twice (04812), as Queue does.
      await o.sqs('ReceiveMessage', { QueueUrl: o.queueUrl(MAIN), MaxNumberOfMessages: 10, VisibilityTimeout: 0 });
      // The board's wire row: a send the service refuses (a dedup id on a standard queue).
      const refused = await o.sqs('SendMessage', {
        QueueUrl: o.queueUrl(MAIN),
        MessageBody: '{}',
        MessageDeduplicationId: 'x',
      });
      if (refused.status !== 400) throw new Error(`expected a refusal, got ${refused.status}`);
      await c.page.goto(o.url(`sqs/${MAIN}`));
      await c.page.locator('.msg:has-text("HRB-2026-04812") .rowopen').click();
      await c.settle(900);
    },
    note: 'The board is an annotated queue page (numbered parts and a "rule" panel). The page is the queue page on the Messages tab, drawn as Queue.dc.html draws it.',
  },

  // The Create board: a new queue, named, with the call it makes.
  Create: {
    async drive(c) {
      await c.page.goto(c.url('sqs/create'));
      await c.page.fill('input[name=name]', 'harbour-returns');
      await c.settle(300);
    },
    note: 'The board is the older, smaller create form; the SQS create page follows Sqs-03 and Sqs-04, which have more.',
  },

  // The wire in the dark theme, in IAM enforce mode (the board's pill says so), with the
  // afternoon the board lists on it: the wire boards' afternoon (wire.ts) plus a read a
  // bucket policy denies, on an instance that enforces.
  Dark: {
    async drive(c) {
      const e = await c.instance('wireEnforce');
      await c.page.goto(e.url(''));
      await c.page.evaluate(() => localStorage.setItem('theme', 'dark'));
      await c.page.reload();
      await c.page.waitForSelector('.tr-row');
      await c.settle(900);
    },
    note: 'Dark is driven by storing the theme and reloading, on the wire boards\' afternoon (wire.ts) on an instance that enforces IAM, with a denied read. The wire page itself (its header, table and right-hand panel) is another package\'s.',
  },

  // At 1024: the list pane is a name switcher, the sentence and the dock are short, the notice is in the key bar.
  Narrow: {
    async drive(c) {
      await c.page.setViewportSize({ width: 1024, height: 768 });
      const o = await heldQueue(c, { first: orderBody('HRB-2026-04811', 3, 4215), second: orderBody('HRB-2026-04812', 1, 675) });
      // The board's dock says one of the calls was refused.
      await o.sqs('SendMessage', { QueueUrl: o.queueUrl(MAIN), MessageBody: '{}', MessageDeduplicationId: 'x' });
      await c.page.goto(o.url(`sqs/${MAIN}?tab=consume`));
      await c.settle(900);
      await c.capture();
    },
    note: 'Rendered at 1024 by 768, the board\'s own size (the board sits in the top-left of a 1440 by 900 canvas). The board draws the Consume tab, as Sqs-11 does: the held table with Release and Delete on each row.',
  },

  // Seen from the wire, with a query typed.
  Palette: {
    async drive(c) {
      await seedShell(c, (s) => c.note(s));
      await c.page.goto(c.url(''));
      await c.page.locator('#palette-open').click();
      await c.page.fill('#pal-q', 'checkout');
      await c.settle(500);
    },
  },

  // Six moments, each with one treatment. One frame cannot hold them all, so several pictures are
  // taken and their texts joined.
  States: {
    async drive(c) {
      const p = c.page;
      await seedShell(c, (s) => c.note(s));
      // It worked: a toast. A send from the composer.
      await p.goto(c.url(`sqs/${MAIN}`));
      await p.locator('.dock-bar').click();
      await c.settle(300);
      await c.setEditor('textarea[name=body][data-editor]', '{ "orderId": "HRB-2026-04814" }');
      await p.locator('.sqs-compose').getByRole('button', { name: /^Send/ }).click();
      await c.settle(600);
      await c.capture();
      // It did not work: a refusal beside the control that caused it.
      await p.goto(c.url(`sqs/${MAIN}?tab=config`));
      await p.getByRole('button', { name: 'Edit settings' }).click();
      await p.fill('#sqs-config input[name=visibility]', '90000');
      await p.getByRole('button', { name: 'Save settings' }).click();
      await c.settle(700);
      await c.capture('refusal');
      // The answers, as a page that shows calls draws them: a refused call, a plain one.
      await c.sqs('SendMessage', { QueueUrl: c.queueUrl(MAIN), MessageBody: '{}', MessageDeduplicationId: 'x' });
      await p.goto(c.url(`sqs/${MAIN}`));
      await c.settle(900);
      await c.capture('answers');
      // Something broke: a function that threw. Its answer is on the wire (a 200 flagged in
      // X-Amz-Function-Error says "it threw" in place of the 200), so the picture is the wire
      // of the afternoon the wire boards use, which holds one such Invoke.
      const w = await c.instance('wire');
      await p.goto(w.url(''));
      await p.locator('.tr-row', { hasText: 'it threw' }).first().waitFor();
      await c.settle(300);
      await c.capture('threw');
      // A denied call (a policy said no): the queue's own policy denies a send, on the
      // instance that enforces, and the dock of the queue shows the call that was refused.
      const e = await c.instance('enforce');
      const deny = await e.sqs('SetQueueAttributes', {
        QueueUrl: e.queueUrl(MAIN),
        Attributes: { Policy: JSON.stringify({ Version: '2012-10-17', Statement: [{ Sid: 'DenySends', Effect: 'Deny', Principal: '*', Action: 'sqs:SendMessage', Resource: e.queueArn(MAIN) }] }) },
      });
      if (deny.status !== 200) throw new Error(`could not set the deny policy: ${deny.status}`);
      await e.sqs('SendMessage', { QueueUrl: e.queueUrl(MAIN), MessageBody: '{}' });
      await p.goto(e.url(`sqs/${MAIN}`));
      await c.settle(900);
      await c.capture('denied');
      // It cannot be undone: a confirm, on the board's own example (a bucket).
      await p.goto(c.url(`s3/${RECEIPTS}`));
      await p.getByRole('button', { name: /Delete/ }).first().click();
      await c.settle(300);
      await c.capture('confirm');
      await p.keyboard.press('Escape');
      // There is nothing here: an empty queue (nothing has been sent to this one).
      await p.goto(c.url(`sqs/${LOYALTY}`));
      await c.settle(500);
      await c.capture('empty');
      // It is taking a while: a thin bar once a request has run past 300 ms; the page underneath stays.
      await p.route('**/ddb/*/explore', async (r) => {
        await new Promise((res) => setTimeout(res, 4000));
        await r.continue();
      });
      await p.goto(c.url(`ddb/${PRODUCTS}`));
      await c.settle(500);
      await p.getByRole('button', { name: 'Scan' }).last().click({ noWaitAfter: true });
      await p.waitForTimeout(900);
      await c.capture('slow');
      await p.unroute('**/ddb/*/explore');
    },
    note: 'Toast, refusal, answers, confirm, empty and "taking a while" are separate pictures (page-States-<name>.png); their texts are joined for the check.',
  },

  // One setting, taken apart: the five teaching lines, on the form field they describe.
  Teaching: {
    async drive(c) {
      const o = await c.instance('own');
      await enableTrigger(o);
      const p = c.page;
      await p.goto(o.url(`sqs/${MAIN}?tab=config`));
      await p.getByRole('button', { name: 'Edit settings' }).click();
      await p.fill('#sqs-config input[name=visibility]', '60');
      await p.locator('#sqs-config input[name=visibility]').blur();
      await c.settle(400);
    },
  },
};
