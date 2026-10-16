// SQS: one entry per board file in plan/design/boards (Sqs-*, Queue, Bad-DLQ).
// Adapted from the scripts the SQS stage's review wrote by hand
// (s01..s21, after.ts). The fixture is harbour.ts; each driver that changes
// state does so on a scratch queue, or puts it back, so the order of rows does
// not matter.

import type { Ctx, DriverTable } from './types';
import { DLQ, FIFO, MAIN } from '../harbour';

const q = (name: string, tab?: string) => `sqs/${name}${tab ? `?tab=${tab}` : ''}`;

async function openComposer(c: Ctx, queue: string) {
  await c.page.goto(c.url(q(queue)));
  await c.page.locator('.dock-bar').click();
  await c.settle(300);
}

/** Scroll everything to the top, so the picture starts where the board does. */
async function toTop(c: Ctx) {
  await c.page.evaluate(() => {
    window.scrollTo(0, 0);
    document.querySelectorAll<HTMLElement>('*').forEach((e) => {
      if (e.scrollTop) e.scrollTop = 0;
    });
  });
}

export const drivers: DriverTable = {
  'Sqs-01-None': {
    async drive(c) {
      // No queues at all: the "none yet" state is an instance with nothing in it.
      const e = await c.instance('empty');
      await c.page.goto(e.url('sqs'));
    },
  },

  'Sqs-02-Pick': {
    async drive(c) {
      await c.page.goto(c.url('sqs'));
    },
  },

  'Sqs-03-Create': {
    async drive(c) {
      await c.page.goto(c.url('sqs/create'));
      await c.page.fill('input[name=name]', 'harbour-returns');
    },
  },

  'Sqs-04-Create-FIFO': {
    async drive(c) {
      const p = c.page;
      await p.goto(c.url('sqs/create'));
      await p.fill('input[name=name]', 'harbour-depot-returns.fifo');
      await p.locator('.q-seg:has(input[name=fifo])').click();
      await c.settle(200);
      await p.locator('.q-seg:has(input[name=content_dedup])').click();
      await p.fill('input[name=visibility]', '60');
      await p.fill('input[name=max_receive]', '5');
      await p.fill('input[name=receive_wait]', '20');
      await p.selectOption('select[name=dedup_scope]', 'messageGroup');
      await p.selectOption('select[name=throughput_limit]', 'perMessageGroupId');
      await p.locator('input[name=receive_wait]').blur();
      await toTop(c);
    },
  },

  'Sqs-05-Create-Refused': {
    async drive(c) {
      const p = c.page;
      await p.goto(c.url('sqs/create'));
      await p.fill('input[name=name]', 'harbour.checkout.orders');
      await p.fill('input[name=visibility]', '30');
      await p.getByRole('button', { name: 'Create queue' }).click();
      await c.settle(600);
    },
  },

  'Sqs-08-Send': {
    async drive(c) {
      const p = c.page;
      await openComposer(c, MAIN);
      await c.setEditor('textarea[name=body][data-editor]', '{\n  "orderId": "HRB-2026-04814",\n  "lines": 2\n}');
      await p.locator('.sqs-compose').getByRole('button', { name: 'Add' }).click();
      await p.fill('.sqs-compose input[placeholder=name]', 'source');
      await p.fill('.sqs-compose input[placeholder=value]', 'checkout-api');
      await p.locator('.sqs-compose').scrollIntoViewIfNeeded();
      await c.capture();
      // The "as CLI" disclosure is part of the composer, not a separate board.
      await p.locator('.sqs-compose .cli-btn').click();
      await p.locator('.sqs-compose .cli-out').scrollIntoViewIfNeeded();
      await c.capture('cli');
    },
  },

  'Sqs-09-Send-Refused': {
    async drive(c) {
      const p = c.page;
      await openComposer(c, FIFO);
      // 1.3 MB: past the 1 MiB limit the model gives. The refusal comes from
      // the service; the padding makes the body 1 310 842 bytes, the figure the board prints.
      await c.setEditor('textarea[name=body][data-editor]', '{ "van": "V-17", "manifest": "' + 'x'.repeat(1_310_809) + '" }');
      await p.locator('.sqs-compose').getByRole('button', { name: /^Send/ }).click();
      await c.settle(900);
      // The megabyte of text is not a picture: put a short body back, as the board draws it.
      await c.setEditor('textarea[name=body][data-editor]', '{ "van": "V-17", "manifest": "…1.3 MB of text…" }');
      // Editing the body is what clears the refusal's outline, so the swap above
      // would draw the board's outlined body as an unmarked one. The refusal is
      // still on the page and the body is still the one that was refused: mark it
      // again, as the page had it before the picture was tidied.
      await p.evaluate(() => document.querySelector('.sqs-compose textarea[name=body]')?.setAttribute('aria-invalid', 'true'));
      await p.locator('.sqs-compose').scrollIntoViewIfNeeded();
    },
    note: 'The body is shortened after the send is refused, so the picture is readable; the refusal is the real one for 1.3 MB.',
  },

  'Sqs-10-Consume-Empty': {
    async drive(c) {
      await c.page.goto(c.url(q(MAIN, 'consume')));
    },
  },

  'Sqs-11-Consume-Held': {
    async drive(c) {
      const p = c.page;
      await p.goto(c.url(q(MAIN, 'consume')));
      await p.getByRole('button', { name: 'Receive' }).click();
      await c.settle(1500);
      await p.locator('.rcv-row input[type=checkbox]').nth(0).check();
      await p.locator('.rcv-row input[type=checkbox]').nth(1).check();
      await c.capture();
      // Put the queue back: release what the receive hid.
      for (let i = 0; i < 3; i++) await p.locator('.rcv-row input[type=checkbox]').nth(i).check().catch(() => {});
      await p.getByRole('button', { name: 'Release selected' }).click();
      await c.settle(600);
    },
  },

  'Sqs-14-Redrive-Confirm': {
    async drive(c) {
      // The board's dialog counts "2 queues" dead-lettering into this one and
      // offers harbour-returns as a destination: a second queue exists, and
      // it is set to dead-letter here. It is removed again afterwards.
      const r = await c.sqs('CreateQueue', {
        QueueName: 'harbour-returns',
        Attributes: { RedrivePolicy: JSON.stringify({ deadLetterTargetArn: c.queueArn(DLQ), maxReceiveCount: 3 }) },
      });
      if (r.status !== 200) throw new Error(`could not create harbour-returns: ${JSON.stringify(r.body)}`);
      try {
        await c.page.goto(c.url(q(DLQ)));
        await c.page.getByRole('button', { name: /^Redrive/ }).first().click();
        await c.settle(400);
        await c.capture();
      } finally {
        await c.sqs('DeleteQueue', { QueueUrl: c.queueUrl('harbour-returns') });
      }
    },
  },

  'Sqs-15-Redrive-Done': {
    async drive(c) {
      // The real DLQ is spent on this, so it runs on its own copy of the fixture.
      const f = await c.instance('fresh');
      // A failed task first: a task whose destination is deleted before its
      // first message moves (the service waits 500 ms before moving) fails.
      await f.sqs('CreateQueue', { QueueName: 'harbour-returns' });
      const st = await f.sqs('StartMessageMoveTask', { SourceArn: f.queueArn(DLQ), DestinationArn: f.queueArn('harbour-returns'), MaxNumberOfMessagesPerSecond: 1 });
      if (st.status !== 200) throw new Error(`could not start the move task: ${JSON.stringify(st.body)}`);
      await f.sqs('DeleteQueue', { QueueUrl: f.queueUrl('harbour-returns') });
      await c.settle(1200);

      const p = c.page;
      await p.goto(f.url(q(DLQ)));
      await p.getByRole('button', { name: /^Redrive/ }).first().click();
      await c.settle(500);
      await p.getByRole('dialog', { name: 'Redrive messages' }).getByRole('button', { name: 'Redrive' }).click();
      await c.settle(300);
      // Running is a state of its own, which the board only mentions ("Earlier, while it ran").
      await c.capture('running');
      await p.waitForFunction(() => /complete/i.test(document.body.innerText), null, { timeout: 20_000 });
      await c.settle(300);
    },
  },

  'Sqs-16-Confirms': {
    async drive(c) {
      const p = c.page;
      await p.goto(c.url(q(MAIN)));
      await p.locator('.det-h').getByRole('button', { name: /Delete queue/ }).click();
      await c.capture();
      await p.keyboard.press('Escape');
      await c.settle(200);
      await p.getByRole('button', { name: 'Purge' }).click();
      await c.capture('purge');
      await p.keyboard.press('Escape');
      await c.settle(200);
      await p.locator('.msg-del').first().click();
      await c.capture('delete-message');
      await p.keyboard.press('Escape');
    },
  },

  'Sqs-17-Config': {
    async drive(c) {
      await c.page.goto(c.url(q(MAIN, 'config')));
    },
  },

  'Sqs-18-Config-Edit': {
    async drive(c) {
      const p = c.page;
      await p.goto(c.url(q(MAIN, 'config')));
      await p.getByRole('button', { name: 'Edit settings' }).click();
      await p.fill('#sqs-config input[name=visibility]', '20');
      await p.locator('#sqs-config input[name=visibility]').blur();
    },
  },

  'Sqs-19-Tags': {
    async drive(c) {
      const p = c.page;
      await p.goto(c.url(q(MAIN, 'tags')));
      await c.settle(600);
      await p.getByRole('button', { name: /Add tag/ }).click();
    },
  },

  'Sqs-20-Denied': {
    async drive(c) {
      // Denial is the instance's IAM enforce mode plus a policy that says no.
      const e = await c.instance('enforce');
      const policy = {
        Version: '2012-10-17',
        Statement: [{ Sid: 'DenyConsoleSends', Effect: 'Deny', Principal: '*', Action: 'sqs:SendMessage', Resource: e.queueArn(MAIN) }],
      };
      const r = await e.sqs('SetQueueAttributes', { QueueUrl: e.queueUrl(MAIN), Attributes: { Policy: JSON.stringify(policy) } });
      if (r.status !== 200) throw new Error(`could not set the deny policy: ${r.status} ${JSON.stringify(r.body)}`);
      const p = c.page;
      await p.goto(e.url(q(MAIN)));
      await p.locator('.dock-bar').click();
      await c.settle(300);
      await c.setEditor('textarea[name=body][data-editor]', '{ "orderId": "HRB-2026-04814" }');
      await p.locator('.sqs-compose').getByRole('button', { name: /^Send/ }).click();
      await c.settle(900);
      await p.locator('.sqs-compose').scrollIntoViewIfNeeded();
    },
  },

  'Sqs-21-Config-Refused': {
    async drive(c) {
      const p = c.page;
      await p.goto(c.url(q(MAIN, 'config')));
      await p.getByRole('button', { name: 'Edit settings' }).click();
      await p.fill('#sqs-config input[name=visibility]', '90000');
      await p.getByRole('button', { name: 'Save settings' }).click();
      await c.settle(700);
      await p.evaluate(() => document.querySelector('.detail')?.scrollTo(0, 0));
    },
  },

  // The Messages tab with one message opened (the second, which has attributes).
  Queue: {
    async drive(c) {
      // The board opens the order that has been received twice (04812: "received
      // 2 of 3"). The fixture has it at one receive, so one more is made, on a
      // copy of the fixture that no other board reads the counts of.
      const f = await c.instance('fresh');
      await f.sqs('ReceiveMessage', { QueueUrl: f.queueUrl(MAIN), MaxNumberOfMessages: 10, VisibilityTimeout: 0 });
      await c.page.goto(f.url(q(MAIN)));
      await c.page.locator('.msg:has-text("HRB-2026-04812") .rowopen').click();
    },
  },

  // The dead-letter queue's Messages tab, with its redrive banner.
  'Bad-DLQ': {
    async drive(c) {
      await c.page.goto(c.url(q(DLQ)));
    },
  },
};
