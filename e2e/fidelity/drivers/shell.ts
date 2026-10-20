// The boards that belong to no service: the shell, the wire, the palette, the
// states, the dark theme. Driven cheaply, against the harbour fixture, so the
// pictures exist and the structural check can run. These are compared whole
// (the chrome is not hidden, as it is for a service's boards), which is why
// most of them differ today: plan/03-shell-fidelity.md is that comparison, and
// each difference there is one this check will print until the shell is built.
//
// Boards with nothing to drive say so, with a reason, as `skip`.

import type { Ctx, DriverTable } from './types';
import { FIFO, LOYALTY, MAIN } from '../harbour';
import { drivers as chrome } from './shell-chrome';

const wire = (c: Ctx) => c.page.goto(c.url(''));

/**
 * The wire as the boards draw it: the harbour shop and the afternoon of calls
 * (e2e/fidelity/wire.ts), on an instance of its own so the calls the other
 * boards make do not appear on it. The boards' own refusal (a
 * MessageDeduplicationId on a standard queue) is the one the fixture makes.
 */
async function wireAfternoon(c: Ctx) {
  const w = await c.instance('wire');
  await c.page.goto(w.url(''));
  // The page is live and the rows arrive on its first poll; the long poll that
  // waited twenty seconds was awaited by the fixture, so it is already there.
  await c.page.waitForSelector('.tr-row');
  await c.settle(600);
  return w;
}

/** The row of a call made to MAIN that the queue's mapping read: the SendMessage that has a child. */
const orderRow = (c: Ctx) =>
  c.page
    .locator('.tr-row:not(.tr-child)', { hasText: 'SendMessage' })
    .filter({ hasText: MAIN })
    .filter({ has: c.page.locator('.st.served') })
    .first();

export const drivers: DriverTable = {
  // Anatomy, Create, Dark, Narrow, Palette, States and Teaching: shell-chrome.ts.
  ...chrome,

  // The wire: the console's home.
  Main: {
    async drive(c) {
      await wireAfternoon(c);
    },
  },

  Connect: {
    async drive(c) {
      await c.page.goto(c.url('connect'));
      await c.page.getByRole('button', { name: 'Run get-caller-identity' }).click();
      await c.page.locator('#cn-result .pill.ok').waitFor();
    },
  },

  // The "what is real here" dialog, opened from the info button beside the open
  // queue's name (D12). The board's list holds the loyalty queue and its
  // dead-letter queue; the harbour fixture has the first only, so the second is
  // made (on the spare copy, which no other shell board reads).
  Fidelity: {
    async drive(c) {
      const f = await c.instance('fresh');
      const r = await f.sqs('CreateQueue', { QueueName: 'harbour-loyalty-points-dlq' });
      if (r.status !== 200) throw new Error(`CreateQueue harbour-loyalty-points-dlq -> ${r.status}`);
      await c.page.goto(f.url(`sqs/${LOYALTY}`));
      await c.page.locator('.det-title .info-btn').click();
      await c.page.locator('.fid-card').first().waitFor();
      await c.settle(400);
    },
  },

  // The first-run wire: nothing has called yet.
  'Wire-Empty': {
    async drive(c) {
      // The board is drawn on another shop ("shop"), which nothing has called.
      const e = await c.instance('empty');
      await c.page.goto(e.url(''));
      await c.page.waitForSelector('.wire-first');
    },
  },

  // The wire with one call opened: a served call and the work it caused. Calls
  // made after it was opened are what the "paused · N new" pill counts.
  'Wire-Inspect': {
    async drive(c) {
      const w = await wireAfternoon(c);
      await orderRow(c).locator('.rowopen').click();
      await c.page.waitForSelector('#t-drawer-inner .td-h');
      for (let n = 0; n < 3; n++) await w.sqs('GetQueueUrl', { QueueName: LOYALTY });
      // The count is asked for every 1.5 seconds while the feed is paused.
      await c.page.waitForFunction(() => /paused · [1-9]/.test(document.querySelector('.wire-acts')?.textContent ?? ''));
      await c.settle(300);
      await c.capture();
      // The board's card for the function the order set off carries what it
      // printed. Here the mapping's invoke is a row of its own (an SDK's send
      // carries nothing that says the invoke came of it), and what it printed is
      // on its Response tab; that is a second picture of the same board.
      await c.page.locator('.tr-row', { hasText: 'harbour-order-validator' }).first().locator('.rowopen').click();
      await c.page.waitForSelector('#t-drawer-inner .td-title:text("Invoke")');
      await c.page.locator('.td-tabs').getByRole('tab', { name: 'Response' }).click();
      await c.capture('invoke');
    },
    note: 'A second picture (page-Wire-Inspect-invoke.png) is the validator\'s own inspector, which holds the lines it printed; the board puts them on the send\'s card.',
  },

  // The wire filtered to what was refused, with the refused call opened.
  'Wire-Refused': {
    async drive(c) {
      await wireAfternoon(c);
      await c.page.getByRole('button', { name: 'Refused', exact: true }).click();
      await c.page.locator('.tr-row.refused .rowopen').first().click();
      await c.page.waitForSelector('#t-drawer-inner .td-callout');
      await c.settle(300);
    },
  },

  // The console's own poll failing: the page is up, and doze-aws is not
  // answering. The browser is told every request to the console fails to
  // connect, which is what a stopped process looks like from the page.
  'Bad-Down': {
    async drive(c) {
      // The board's queue is a quiet one with a dead-letter queue beside it and
      // twelve messages waiting; a copy of the shop, so the default one keeps its
      // queues.
      const f = await c.instance('fresh');
      await f.sqs('CreateQueue', { QueueName: 'harbour-loyalty-points-dlq' });
      for (let n = 0; n < 12; n++)
        await f.sqs('SendMessage', { QueueUrl: f.queueUrl(LOYALTY), MessageBody: JSON.stringify({ customer: 'c-2302', points: 40 }) });
      await c.page.goto(f.url(`sqs/${LOYALTY}`));
      await c.settle(600);
      await c.page.route(/\/_console\//, (r) => r.abort('connectionrefused'));
      // The counts poll runs every five seconds; the banner follows the first miss.
      await c.page.waitForSelector('#down-banner:not([hidden])', { timeout: 15000 });
      await c.settle(300);
    },
    note: 'The page is left up while every request to the console is refused at the browser, which is how it sees a stopped doze-aws; the e2e spec stops the real process.',
  },

  // The map: the wiring graph, drawn while it fits. The instance is wired as
  // the board draws it (mapfixture.ts), with the statements bucket read so the
  // wire out of it is dark.
  Map: {
    async drive(c) {
      const m = await c.instance('map');
      await c.page.goto(m.url('map'));
    },
  },

  // The same page at scale: 41 wired resources in 11 flows, seeded from a spec.
  'Map-Large': {
    async drive(c) {
      const m = await c.instance('mapLarge');
      await c.page.goto(m.url('map'));
    },
  },

  // Boards with nothing to drive, and why. A skip is shown in the report.
  Grounding: { skip: 'A document board (what was grounded in code and what is new), not a screen: there is no page state to drive.' },
};
