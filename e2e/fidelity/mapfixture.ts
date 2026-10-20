// The fixtures for the Map and Map-Large boards: the wiring the boards draw,
// made on a booted instance, and the calls that make some of it hot.
//
// The map is drawn from the wiring graph, so the fixture is wiring, seeded
// programmatically from a spec rather than listed call by call: the large one
// is 41 wired resources in 11 flows and 4 unwired, and it is the arithmetic of
// the board (plan/design/boards/Map-Large.dc.html).
//
// Resources are made through the console's own create routes (they bust the
// wiring graph and, going in-process, are not on the wire) and the AWS API
// where the console has no route (a mapping from a stream or a table). The
// wire is then cleared, and the few reads that make a flow "hot" are made
// through the gateway, so the ring holds exactly those calls.

import { join } from 'node:path';
import { E2E_DIR } from './server';
import type { Inst } from './drivers/types';

const CODE = join(E2E_DIR, 'fixtures', 'lambda-handler');

export interface Wiring {
  buckets: string[];
  queues: { name: string; dlq?: string }[];
  topics: string[];
  streams: string[];
  tables: string[]; // each with a stream
  functions: string[];
  rules: string[];
  /** bucket notifies, topic -> queue, rule -> arn-ish, queue/stream/table -> function */
  notify: [bucket: string, dest: `${'sqs' | 'sns' | 'lambda'}:${string}`][];
  subs: [topic: string, queue: string][];
  targets: [rule: string, kind: 'sqs' | 'sns' | 'lambda', name: string][];
  esm: [source: { kind: 'sqs' | 'kinesis' | 'ddb'; name: string }, fn: string][];
  /** What is touched after the wire is cleared: reads, which change nothing. */
  hot: { queue?: string; bucket?: string; rule?: string }[];
}

/** Map.dc.html: ten wired resources in three flows, and one unwired. */
export const SMALL: Wiring = {
  buckets: ['harbour-loyalty-statements-prod', 'harbour-assets'],
  queues: [
    { name: 'harbour-loyalty-points-dlq' },
    { name: 'harbour-loyalty-points-prod', dlq: 'harbour-loyalty-points-dlq' },
    { name: 'harbour-alerts-email' },
  ],
  topics: ['harbour-alerts'],
  streams: ['harbour-clickstream'],
  tables: ['harbour-sessions'],
  functions: ['harbour-stock-forecast', 'harbour-sessionise'],
  rules: ['nightly-forecast'],
  notify: [['harbour-loyalty-statements-prod', 'sqs:harbour-loyalty-points-prod']],
  subs: [['harbour-alerts', 'harbour-alerts-email']],
  targets: [['nightly-forecast', 'lambda', 'harbour-stock-forecast']],
  esm: [
    [{ kind: 'sqs', name: 'harbour-loyalty-points-prod' }, 'harbour-stock-forecast'],
    [{ kind: 'kinesis', name: 'harbour-clickstream' }, 'harbour-sessionise'],
    [{ kind: 'ddb', name: 'harbour-sessions' }, 'harbour-sessionise'],
  ],
  // The statements bucket is read: the wire out of it (its notification) is dark.
  hot: [{ bucket: 'harbour-loyalty-statements-prod' }],
};

/** Map-Large.dc.html: 41 wired resources in 11 flows, four unwired, three flows hot. */
export function large(): Wiring {
  const w: Wiring = {
    buckets: [],
    queues: [],
    topics: [],
    streams: [],
    tables: [],
    functions: [],
    rules: [],
    notify: [],
    subs: [],
    targets: [],
    esm: [],
    hot: [],
  };
  const q = (name: string, dlq?: string) => void w.queues.push({ name, dlq });
  // 1. statements: 5 (the bucket, the queue, its function and DLQ, the schedule). Hot.
  w.buckets.push('harbour-loyalty-statements-prod');
  q('harbour-loyalty-points-dlq');
  q('harbour-loyalty-points-prod', 'harbour-loyalty-points-dlq');
  w.functions.push('harbour-stock-forecast');
  w.rules.push('nightly-forecast');
  w.notify.push(['harbour-loyalty-statements-prod', 'sqs:harbour-loyalty-points-prod']);
  w.esm.push([{ kind: 'sqs', name: 'harbour-loyalty-points-prod' }, 'harbour-stock-forecast']);
  w.targets.push(['nightly-forecast', 'lambda', 'harbour-stock-forecast']);
  w.hot.push({ queue: 'harbour-loyalty-points-prod' });
  // 2. orders: 9, from a rule. Hot.
  w.rules.push('orders-placed');
  w.functions.push('harbour-fulfil', 'harbour-order-mailer');
  q('harbour-fulfil-dlq');
  q('harbour-fulfil-prod', 'harbour-fulfil-dlq');
  q('harbour-order-audit-dlq');
  q('harbour-order-audit', 'harbour-order-audit-dlq');
  q('harbour-order-mail');
  w.topics.push('harbour-order-events');
  w.targets.push(['orders-placed', 'lambda', 'harbour-fulfil'], ['orders-placed', 'sqs', 'harbour-fulfil-prod'], ['orders-placed', 'sqs', 'harbour-order-audit'], ['orders-placed', 'sns', 'harbour-order-events']);
  w.subs.push(['harbour-order-events', 'harbour-order-mail']);
  w.esm.push([{ kind: 'sqs', name: 'harbour-order-mail' }, 'harbour-order-mailer']);
  w.hot.push({ rule: 'orders-placed' });
  // 3. uploads: 3. Hot.
  w.buckets.push('harbour-uploads');
  q('harbour-thumbnails');
  w.functions.push('harbour-thumbnail');
  w.notify.push(['harbour-uploads', 'sqs:harbour-thumbnails']);
  w.esm.push([{ kind: 'sqs', name: 'harbour-thumbnails' }, 'harbour-thumbnail']);
  w.hot.push({ bucket: 'harbour-uploads' });
  // 4. alerts: 2.
  w.topics.push('harbour-alerts');
  q('harbour-alerts-email');
  w.subs.push(['harbour-alerts', 'harbour-alerts-email']);
  // 5. clickstream: 3, a stream and a table into one function.
  w.streams.push('harbour-clickstream');
  w.tables.push('harbour-sessions');
  w.functions.push('harbour-sessionise');
  w.esm.push([{ kind: 'kinesis', name: 'harbour-clickstream' }, 'harbour-sessionise'], [{ kind: 'ddb', name: 'harbour-sessions' }, 'harbour-sessionise']);
  // 6. reports: 5, a bucket to a topic to three queues.
  w.buckets.push('harbour-reports');
  w.topics.push('harbour-report-ready');
  for (const n of ['mail', 'pdf', 'zip']) {
    q(`harbour-report-${n}`);
    w.subs.push(['harbour-report-ready', `harbour-report-${n}`]);
  }
  w.notify.push(['harbour-reports', 'sns:harbour-report-ready']);
  // 7. billing: 6, a queue with a chain of dead-letter queues and a second source.
  q('harbour-billing-graveyard');
  q('harbour-billing-parked', 'harbour-billing-graveyard');
  q('harbour-billing-dlq', 'harbour-billing-parked');
  q('harbour-billing-in', 'harbour-billing-dlq');
  q('harbour-billing-manual');
  w.functions.push('harbour-billing');
  w.esm.push([{ kind: 'sqs', name: 'harbour-billing-in' }, 'harbour-billing'], [{ kind: 'sqs', name: 'harbour-billing-manual' }, 'harbour-billing']);
  // 8 to 11. four small flows of two, which fold.
  w.topics.push('harbour-bulk-a');
  q('harbour-bulk-a-q');
  w.subs.push(['harbour-bulk-a', 'harbour-bulk-a-q']);
  w.buckets.push('harbour-logs-archive');
  q('harbour-logs-q');
  w.notify.push(['harbour-logs-archive', 'sqs:harbour-logs-q']);
  w.rules.push('weekly-digest');
  q('harbour-digest-q');
  w.targets.push(['weekly-digest', 'sqs', 'harbour-digest-q']);
  w.topics.push('harbour-nightly-export');
  q('harbour-export-q');
  w.subs.push(['harbour-nightly-export', 'harbour-export-q']);
  // Wired to nothing: 4.
  w.buckets.push('harbour-assets');
  w.tables.push('harbour-archive');
  w.functions.push('harbour-hello');
  w.topics.push('test-topic');
  return w;
}

function must(r: Response, what: string) {
  if (r.status >= 400) throw new Error(`map fixture: ${what} -> ${r.status}`);
}

async function aws(i: Inst, target: string, body: unknown, ct = 'application/x-amz-json-1.1') {
  const r = await fetch(`${i.server.origin}/`, { method: 'POST', headers: { 'Content-Type': ct, 'X-Amz-Target': target }, body: JSON.stringify(body) });
  const text = await r.text();
  if (r.status >= 400) throw new Error(`map fixture: ${target} -> ${r.status} ${text.slice(0, 200)}`);
  return JSON.parse(text);
}

export async function seedWiring(i: Inst, w: Wiring): Promise<void> {
  const { origin, account, region } = i.server;
  const con = (path: string, form: Record<string, string>) =>
    fetch(`${i.server.console}${path}`, { method: 'POST', redirect: 'manual', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: new URLSearchParams(form) });
  const post = async (path: string, form: Record<string, string>) => must(await con(path, form), path);
  const arn = (svc: string, rest: string) => `arn:aws:${svc}:${region}:${account}:${rest}`;
  const target = (kind: string, name: string) => (kind === 'lambda' ? arn('lambda', `function:${name}`) : arn(kind, name));

  for (const name of w.buckets) await post('s3/create', { name });
  // A dead-letter queue is made before the queue that names it.
  for (const q of [...w.queues].sort((a, b) => Number(!!a.dlq) - Number(!!b.dlq))) {
    await post('sqs/create', { name: q.name, ...(q.dlq ? { dlq: q.dlq, max_receive: '5' } : {}) });
  }
  for (const name of w.topics) await post('sns/create', { name });
  for (const name of w.streams) await post('kinesis/create', { name, shards: '1' });
  const streamArn: Record<string, string> = {};
  for (const name of w.tables) {
    const t = await aws(i, 'DynamoDB_20120810.CreateTable', {
      TableName: name,
      AttributeDefinitions: [{ AttributeName: 'pk', AttributeType: 'S' }],
      KeySchema: [{ AttributeName: 'pk', KeyType: 'HASH' }],
      BillingMode: 'PAY_PER_REQUEST',
      ...(name === 'harbour-archive' ? {} : { StreamSpecification: { StreamEnabled: true, StreamViewType: 'NEW_AND_OLD_IMAGES' } }),
    }, 'application/x-amz-json-1.0');
    streamArn[name] = t.TableDescription.LatestStreamArn;
  }
  for (const name of w.functions) await post('lambda/create', { name, runtime: 'provided.al2', handler: 'bootstrap', code: CODE, timeout: '30' });
  for (const name of w.rules) await post('eb/default/create-rule', { name, pattern: JSON.stringify({ source: [`harbour.${name}`] }) });

  for (const [topic, queue] of w.subs) await post(`sns/${topic}/subscribe`, { protocol: 'sqs', endpoint: arn('sqs', queue) });
  for (const [rule, kind, name] of w.targets) await post(`eb/default/rule/${rule}/add-target`, { arn: target(kind, name) });
  for (const [bucket, dest] of w.notify) await post(`s3/${bucket}/notify-add`, { dest, event: 's3:ObjectCreated:*' });
  for (const [src, fn] of w.esm) {
    if (src.kind === 'sqs') {
      await post(`lambda/${fn}/add-mapping`, { queue: src.name, batch: '10' });
      continue;
    }
    const arnOf = src.kind === 'kinesis' ? arn('kinesis', `stream/${src.name}`) : streamArn[src.name]!;
    const r = await fetch(`${origin}/2015-03-31/event-source-mappings/`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ EventSourceArn: arnOf, FunctionName: fn, StartingPosition: 'LATEST', BatchSize: 10 }),
    });
    must(r, `event source mapping ${src.name} -> ${fn}`);
  }
}

/** Clear the wire, then make the reads that name the hot resources. */
export async function heat(i: Inst, w: Wiring): Promise<void> {
  must(await fetch(`${i.server.console}traffic/clear`, { method: 'POST' }), 'traffic/clear');
  for (const h of w.hot) {
    if (h.queue) await i.sqs('GetQueueAttributes', { QueueUrl: i.queueUrl(h.queue), AttributeNames: ['All'] });
    if (h.bucket) must(await fetch(`${i.server.origin}/${h.bucket}?list-type=2`), `list ${h.bucket}`);
    if (h.rule) await aws(i, 'AWSEvents.DescribeRule', { Name: h.rule });
  }
}

/** Wait until the map says what the fixture makes it say (the graph is cached
 *  for a few seconds, and a mapping made through the API does not bust it). */
export async function settled(i: Inst, want: string[]): Promise<void> {
  const deadline = Date.now() + 20_000;
  let last = '';
  for (;;) {
    last = await (await fetch(`${i.server.console}map`)).text();
    const text = last.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ');
    if (want.every((w) => text.includes(w))) return;
    if (Date.now() > deadline) throw new Error(`the map never said ${JSON.stringify(want)}: ${text.slice(0, 600)}`);
    await Bun.sleep(300);
  }
}

export async function seedMap(i: Inst): Promise<void> {
  await seedWiring(i, SMALL);
  await heat(i, SMALL);
  await settled(i, ['10 resources', '3 flows', '1 unwired']);
}

export async function seedMapLarge(i: Inst): Promise<void> {
  const w = large();
  await seedWiring(i, w);
  await heat(i, w);
  await settled(i, ['41 resources', '11 flows', '4 unwired', '3 flows carried traffic recently']);
}
