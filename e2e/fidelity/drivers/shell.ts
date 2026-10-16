// The boards that belong to no service: the shell, the wire, the palette, the
// states, the dark theme. Driven cheaply, against the harbour fixture, so the
// pictures exist and the structural check can run. These are compared whole
// (the chrome is not hidden, as it is for a service's boards), which is why
// most of them differ today: plan/03-shell-fidelity.md is that comparison, and
// each difference there is one this check will print until the shell is built.
//
// Boards with nothing to drive say so, with a reason, as `skip`.

import type { Ctx, DriverTable } from './types';
import { DLQ, FIFO, LOYALTY, MAIN } from '../harbour';

const wire = (c: Ctx) => c.page.goto(c.url(''));

/** The wire, with the traffic harbour's seed made. */
async function wireWithRefusal(c: Ctx) {
  // A refused call. The boards' own example (a MessageGroupId sent to a
  // standard queue) is not a refusal any more: standard queues take a group id
  // (fair queues), so the call that is refused here is a FIFO send without one.
  await c.sqs('SendMessage', { QueueUrl: c.queueUrl(FIFO), MessageBody: JSON.stringify({ orderId: 'HRB-2026-04810' }) });
  await wire(c);
  await c.settle(800);
}

export const drivers: DriverTable = {
  // The annotated queue page: the eight parts of a resource page.
  Anatomy: {
    async drive(c) {
      await c.page.goto(c.url(`sqs/${MAIN}`));
    },
    note: 'The board is an annotated queue page (numbered parts and a "rule" panel); the page is a queue page.',
  },

  // The wire: the console's home.
  Main: {
    async drive(c) {
      await wireWithRefusal(c);
    },
  },

  Create: {
    async drive(c) {
      await c.page.goto(c.url('sqs/create'));
      await c.page.fill('input[name=name]', 'harbour-returns');
    },
    note: 'The board is a minimal create form with a "call this makes" panel; the page is the SQS create page, which has more.',
  },

  // The wire in the dark theme.
  Dark: {
    async drive(c) {
      await c.page.goto(c.url(''));
      await c.page.evaluate(() => localStorage.setItem('theme', 'dark'));
      await c.page.reload();
      await c.settle(800);
    },
    note: 'Dark is driven by setting the theme in localStorage and reloading; the board is a dark rendering of the wire.',
  },

  // Below 1024 px the console says it is not built for that width.
  Narrow: {
    async drive(c) {
      await c.page.setViewportSize({ width: 700, height: 900 });
      await c.page.goto(c.url(`sqs/${MAIN}`));
    },
    note: 'The page is rendered at 700 by 900, a narrow window; the board draws a phone frame inside a 1440 by 900 canvas.',
  },

  Palette: {
    async drive(c) {
      await wire(c);
      await c.page.locator('#palette-open').click();
      await c.page.fill('#pal-q', 'checkout');
      await c.settle(400);
    },
  },

  // Six moments, each with one treatment. The page has each; one frame cannot
  // hold them all, so several pictures are taken and their texts joined.
  States: {
    async drive(c) {
      const p = c.page;
      // It worked: a toast. A send from the composer on the quiet queue.
      await p.goto(c.url(`sqs/${LOYALTY}`));
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
      // It cannot be undone: a confirm.
      await p.goto(c.url(`sqs/${LOYALTY}`));
      await p.getByRole('button', { name: 'Purge' }).click();
      await c.capture('confirm');
      await p.keyboard.press('Escape');
      // There is nothing here: an empty list.
      await p.goto(c.url('sqs/' + DLQ));
      await c.settle(300);
    },
    note: 'Toast, inline refusal and confirm are captured as separate pictures (page-States-refusal.png, -confirm.png); the "taking a while" bar and the four answer chips are not driven.',
  },

  // One setting, taken apart: the five teaching lines, on the form field they describe.
  Teaching: {
    async drive(c) {
      const p = c.page;
      await p.goto(c.url(`sqs/${MAIN}?tab=config`));
      await p.getByRole('button', { name: 'Edit settings' }).click();
      await p.fill('#sqs-config input[name=visibility]', '30');
      await p.locator('#sqs-config input[name=visibility]').blur();
    },
  },

  Connect: {
    async drive(c) {
      await c.page.goto(c.url('connect'));
    },
  },

  // The "what is real here" dialog, opened from the list pane's info button.
  Fidelity: {
    async drive(c) {
      await c.page.goto(c.url('sqs'));
      await c.page.locator('.info-btn').first().click();
      await c.settle(400);
    },
  },

  // The first-run wire: nothing has called yet.
  'Wire-Empty': {
    async drive(c) {
      const e = await c.instance('empty');
      await c.page.goto(e.url(''));
    },
  },

  // The wire with one call opened.
  'Wire-Inspect': {
    async drive(c) {
      await wire(c);
      await c.page.locator('.tr-row .rowopen').first().click();
      await c.settle(500);
    },
  },

  // The wire with a refused call opened.
  'Wire-Refused': {
    async drive(c) {
      await wireWithRefusal(c);
      await c.page.locator('.tr-row.refused .rowopen').first().click();
      await c.settle(500);
    },
  },

  // Boards with nothing to drive, and why. A skip is shown in the report.
  Map: { skip: 'There is no map page: foundation D ("The map" is `new`, built from the wiring graph). Nothing to drive until it exists.' },
  'Map-Large': { skip: 'There is no map page (foundation D); this is the same board at scale.' },
  'Bad-Down': { skip: 'The "cannot reach doze-aws" banner is not built (foundation D); it is the console\'s own poll failing, browser-side.' },
  Grounding: { skip: 'A document board (what was grounded in code and what is new), not a screen: there is no page state to drive.' },
};
