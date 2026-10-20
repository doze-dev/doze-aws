// The standard fixture: "harbour", the made-up shop the design boards are
// drawn from. Seeded entirely through AWS calls (and the console's own create
// route for the one thing, a Lambda function, that has no AWS call without a
// zip). What it builds is what the SQS boards show: a queue with a dead-letter
// queue, a FIFO queue, three messages past maxReceiveCount in the DLQ, two in
// flight and three waiting on the main queue, tags, a resource policy, an
// event source mapping and an SNS subscription for the neighbours strip.
//
// The event source mapping is DISABLED in the standard fixture: an enabled one
// reads the queue, and a queue that is being read has no messages waiting.
// The boards that draw the reader ("read by", "In this stack", the reader's
// warning under the settings: Queue, Sqs-17, Sqs-18) need an ENABLED mapping and
// the waiting messages together, and that state is real on AWS too (a reader
// busy with one batch while others wait). `reader: true` builds it: the
// function is a real one (the fixture bootstrap, built once), the mapping is
// enabled before the waiting messages are sent, and the first message it reads
// is a hold ({"sleep": ms}) that keeps the reader inside one invocation. The
// held message is the second of the two in flight (the first is received by
// the seed). The hold lasts HOLD_MS; a board must be driven inside it.
//
// Services other than SQS add their own seed here when their stage needs it.

import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { E2E_DIR, TMP_DIR } from './server';
import type { Inst } from './drivers/types';

export const MAIN = 'harbour-checkout-orders';
export const DLQ = 'harbour-checkout-orders-dlq';
export const FIFO = 'harbour-depot-dispatch.fifo';
export const LOYALTY = 'harbour-loyalty-points-prod';
export const FUNCTION = 'harbour-order-validator';

/** How long the reader stays inside the invocation that holds it. The function's
 *  timeout is 30 s (the boards print it), so the hold is shorter than that. */
export const HOLD_MS = 25_000;

/** The fixture bootstrap, built once per run into e2e/.tmp, as a function's code. */
let bootstrapDir: string | undefined;
export function builtFunctionCode(): string {
  if (bootstrapDir) return bootstrapDir;
  const dir = join(TMP_DIR, 'fidelity-lambda');
  mkdirSync(dir, { recursive: true });
  const r = spawnSync('go', ['build', '-o', join(dir, 'bootstrap'), '.'], {
    cwd: join(E2E_DIR, 'fixtures', 'lambda-handler'),
    env: { ...process.env, GOWORK: 'off' },
    stdio: ['ignore', 'inherit', 'inherit'],
  });
  if (r.status !== 0 || !existsSync(join(dir, 'bootstrap'))) throw new Error('go build of e2e/fixtures/lambda-handler failed');
  return (bootstrapDir = dir);
}

function must(r: { status: number; body: any }, what: string) {
  if (r.status !== 200) throw new Error(`seed: ${what} -> ${r.status} ${JSON.stringify(r.body).slice(0, 200)}`);
  return r;
}

export interface HarbourOptions {
  /** The directory the order validator's code is. Default: the e2e Lambda
   *  fixture's source, which is enough for a function that is only listed; the
   *  wire boards need one that runs (wire.ts builds it). */
  handlerDir?: string;
  /** The mapping is enabled and the reader is held; see the header. */
  reader?: boolean;
}

export async function seedHarbour(i: Inst, warn: (s: string) => void, opts: HarbourOptions = {}): Promise<void> {
  const { sqs, queueUrl: U, queueArn: A, server } = i;
  const reader = !!opts.reader;
  must(await sqs('CreateQueue', { QueueName: DLQ }), 'dlq');
  must(
    await sqs('CreateQueue', {
      QueueName: MAIN,
      Attributes: { VisibilityTimeout: '60', RedrivePolicy: JSON.stringify({ deadLetterTargetArn: A(DLQ), maxReceiveCount: 3 }) },
    }),
    'main queue',
  );
  must(await sqs('CreateQueue', { QueueName: FIFO, Attributes: { FifoQueue: 'true' } }), 'fifo queue');
  must(await sqs('CreateQueue', { QueueName: LOYALTY }), 'loyalty queue');

  const send = (id: string, lines: number, pence: number) =>
    sqs('SendMessage', {
      QueueUrl: U(MAIN),
      MessageBody: JSON.stringify({ orderId: id, lines, totalPence: pence }),
      MessageAttributes: { source: { DataType: 'String', StringValue: 'checkout-api' } },
    });
  // Three messages received past maxReceiveCount of 3 land in the DLQ.
  for (const [id, l, p] of [['HRB-2026-04790', 2, 3120], ['HRB-2026-04791', 1, 450], ['HRB-2026-04795', 4, 8800]] as const)
    must(await send(id, l, p), 'send');
  for (let n = 0; n < 4; n++) await sqs('ReceiveMessage', { QueueUrl: U(MAIN), MaxNumberOfMessages: 10, VisibilityTimeout: 0 });
  // Two in flight (held an hour), then three waiting, one of them received twice.
  // With a reader, one of the two is the message the reader holds, below.
  await send('HRB-2026-04809', 1, 100);
  if (!reader) await send('HRB-2026-04810', 1, 100);
  await sqs('ReceiveMessage', { QueueUrl: U(MAIN), MaxNumberOfMessages: 10, VisibilityTimeout: 3600 });

  // Neighbours: an SNS subscription and a Lambda event source mapping. The
  // strip on the queue page is drawn from the wiring graph, so these matter.
  // Failures warn instead of throwing: the SQS pages do not need them to load.
  let mapped = false;
  try {
    let r = await fetch(`${server.origin}/`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'Action=CreateTopic&Name=harbour-orders-topic&Version=2010-03-31',
    });
    if (!r.ok) warn(`seed: CreateTopic -> ${r.status}`);
    r = await fetch(`${server.origin}/`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({
        Action: 'Subscribe',
        TopicArn: `arn:aws:sns:${server.region}:${server.account}:harbour-orders-topic`,
        Protocol: 'sqs',
        Endpoint: A(MAIN),
        Version: '2010-03-31',
      }).toString(),
    });
    if (!r.ok) warn(`seed: Subscribe -> ${r.status}`);
    r = await fetch(`${server.origin}/_console/lambda/create`, {
      method: 'POST',
      redirect: 'manual',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({
        name: FUNCTION,
        runtime: 'provided.al2',
        handler: 'bootstrap',
        code: reader ? builtFunctionCode() : (opts.handlerDir ?? join(E2E_DIR, 'fixtures', 'lambda-handler')),
        timeout: '30',
      }),
    });
    if (r.status >= 400) warn(`seed: lambda create -> ${r.status}`);
    r = await fetch(`${server.origin}/2015-03-31/event-source-mappings/`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ EventSourceArn: A(MAIN), FunctionName: FUNCTION, Enabled: reader, BatchSize: 10 }),
    });
    if (!r.ok) warn(`seed: event source mapping -> ${r.status}`);
    mapped = r.ok;
    // The Palette board's third match for "checkout": a log group, so the board's
    // "4 matches" (three places to go and the create) is the page's too.
    r = await fetch(`${server.origin}/_console/logs/create`, {
      method: 'POST',
      redirect: 'manual',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({ name: '/harbour/checkout' }),
    });
    if (r.status >= 400) warn(`seed: log group -> ${r.status}`);
  } catch (e) {
    warn(`seed: neighbours skipped: ${e}`);
  }

  if (reader) {
    if (!mapped) throw new Error('seed: a reader was asked for and the mapping was not made');
    // The reader's first message holds it. Wait until it is in flight (the
    // seed's own receive is the other), so what is sent next stays waiting.
    must(await sqs('SendMessage', { QueueUrl: U(MAIN), MessageBody: JSON.stringify({ sleep: HOLD_MS }) }), 'hold');
    const deadline = Date.now() + 20_000;
    for (;;) {
      const a = await sqs('GetQueueAttributes', { QueueUrl: U(MAIN), AttributeNames: ['ApproximateNumberOfMessagesNotVisible'] });
      if (a.body?.Attributes?.ApproximateNumberOfMessagesNotVisible === '2') break;
      if (Date.now() > deadline) throw new Error(`seed: the reader never took the holding message: ${JSON.stringify(a.body)}`);
      await new Promise((r) => setTimeout(r, 100));
    }
  }

  // 04812 has been received once and come back: the Consume board's receive
  // makes it "2× of 3". (The Queue board draws it at "received 2 of 3"; that
  // driver adds the receive on its own copy.)
  await send('HRB-2026-04812', 1, 675);
  await sqs('ReceiveMessage', { QueueUrl: U(MAIN), MaxNumberOfMessages: 10, VisibilityTimeout: 0 });
  await send('HRB-2026-04811', 3, 4215);
  await send('HRB-2026-04813', 2, 1900);

  for (const v of ['V-17', 'V-18'])
    must(
      await sqs('SendMessage', { QueueUrl: U(FIFO), MessageBody: JSON.stringify({ van: v }), MessageGroupId: 'depot', MessageDeduplicationId: v }),
      'fifo send',
    );
  must(await sqs('TagQueue', { QueueUrl: U(MAIN), Tags: { team: 'checkout', env: 'local' } }), 'tags');
  must(await sqs('AddPermission', { QueueUrl: U(MAIN), Label: 'allow-orders-topic', AWSAccountIds: [server.account], Actions: ['SendMessage'] }), 'permission');
}
