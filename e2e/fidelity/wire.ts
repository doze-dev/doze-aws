// The wire boards' fixture (Main, Wire-Inspect, Wire-Refused, Bad-Down): the
// harbour shop, and the afternoon the boards were drawn from.
//
// seedHarbour makes the shop's queues. The wire boards also draw calls to a
// bucket, a table, a secret, a key, a stack, an alarm that is firing and two
// functions, one of which throws. Each is made here through the same AWS calls
// an app would make, and then the ring is cleared and the calls of the board's
// afternoon are made in order, so the wire page shows what a person looking at
// it would have seen and not forty calls of set-up.
//
// Nothing on the page is typed in by this file. It makes calls; the page shows
// what the recorder and the product say of them.

import { spawnSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { E2E_DIR } from './server';
import type { Inst } from './drivers/types';
import { FIFO, LOYALTY, MAIN, seedHarbour } from './harbour';

// AWS does not deliver a bucket's notifications to a FIFO queue (it refuses to
// configure one), so the board's "harbour-depot-dispatch.fifo" is a standard queue here.
const DISPATCH = 'harbour-depot-dispatch';

const HANDLER_SRC = join(E2E_DIR, 'fidelity', 'fixtures', 'harbour-handler');
export const HANDLER_DIR = join(E2E_DIR, '.tmp', 'harbour-handler');

/** Builds the Lambda runtime client the two functions run (once per run). */
export function buildHandler(): string {
  mkdirSync(HANDLER_DIR, { recursive: true });
  const r = spawnSync('go', ['build', '-o', join(HANDLER_DIR, 'bootstrap'), '.'], {
    cwd: HANDLER_SRC,
    env: { ...process.env, GOWORK: 'off' },
    stdio: ['ignore', 'inherit', 'inherit'],
  });
  if (r.status !== 0) throw new Error('go build of the harbour handler failed');
  return HANDLER_DIR;
}

type Res = { status: number; text: string; json: any };

async function send(url: string, init: RequestInit): Promise<Res> {
  const r = await fetch(url, init);
  const text = await r.text();
  let json: any;
  try {
    json = JSON.parse(text);
  } catch {
    json = undefined;
  }
  return { status: r.status, text, json };
}

/** A JSON-protocol call: `json(i, 'DynamoDB_20120810.PutItem', '1.0', {...})`. */
export const jsonCall = (i: Inst, target: string, version: '1.0' | '1.1', body: Record<string, unknown>) =>
  send(`${i.server.origin}/`, {
    method: 'POST',
    headers: { 'Content-Type': `application/x-amz-json-${version}`, 'X-Amz-Target': target },
    body: JSON.stringify(body),
  });

/** A Query-protocol call. */
export const queryCall = (i: Inst, version: string, params: Record<string, string>) =>
  send(`${i.server.origin}/`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ ...params, Version: version }).toString(),
  });

const rest = (i: Inst, method: string, path: string, body?: string, type?: string) =>
  send(`${i.server.origin}${path}`, { method, headers: type ? { 'Content-Type': type } : {}, body });

function must(r: Res, what: string): Res {
  if (r.status >= 300) throw new Error(`wire seed: ${what} -> ${r.status} ${r.text.slice(0, 200)}`);
  return r;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

const TEMPLATE = (rate: string) =>
  JSON.stringify({ Resources: { Rate: { Type: 'AWS::SSM::Parameter', Properties: { Name: '/harbour/loyalty/rate', Type: 'String', Value: rate } } } });

/** What the shop has, before the afternoon the boards show. */
async function seedShop(i: Inst, warn: (s: string) => void): Promise<string> {
  const { server } = i;
  // The table the order validator writes to, and the bucket whose uploads tell
  // the dispatch queue (a FIFO queue) that a receipt landed.
  must(
    await jsonCall(i, 'DynamoDB_20120810.CreateTable', '1.0', {
      TableName: 'harbour-orders',
      AttributeDefinitions: [{ AttributeName: 'orderId', AttributeType: 'S' }],
      KeySchema: [{ AttributeName: 'orderId', KeyType: 'HASH' }],
      BillingMode: 'PAY_PER_REQUEST',
    }),
    'create table',
  );
  // The rail draws the shop with five buckets and five tables (Main): the ones
  // the afternoon uses, and four more of each that it does not touch.
  for (const t of ['harbour-customers', 'harbour-inventory', 'harbour-prices', 'harbour-sessions'])
    must(
      await jsonCall(i, 'DynamoDB_20120810.CreateTable', '1.0', {
        TableName: t,
        AttributeDefinitions: [{ AttributeName: 'id', AttributeType: 'S' }],
        KeySchema: [{ AttributeName: 'id', KeyType: 'HASH' }],
        BillingMode: 'PAY_PER_REQUEST',
      }),
      `create table ${t}`,
    );
  for (const b of ['harbour-exports', 'harbour-assets', 'harbour-backups', 'harbour-archive']) must(await rest(i, 'PUT', `/${b}`), `create bucket ${b}`);
  must(await i.sqs('CreateQueue', { QueueName: DISPATCH }), 'dispatch queue');
  must(await rest(i, 'PUT', '/harbour-receipts'), 'create bucket');
  must(
    await rest(
      i,
      'PUT',
      '/harbour-receipts?notification',
      `<NotificationConfiguration><QueueConfiguration><Queue>${i.queueArn(DISPATCH)}</Queue><Event>s3:ObjectCreated:*</Event></QueueConfiguration></NotificationConfiguration>`,
      'application/xml',
    ),
    'bucket notification',
  );
  // A secret, and a key with an alias, for the two calls the app makes to them.
  must(await jsonCall(i, 'secretsmanager.CreateSecret', '1.1', { Name: 'harbour/db', SecretString: '{"host":"db.harbour.local"}' }), 'secret');
  const key = must(await jsonCall(i, 'TrentService.CreateKey', '1.1', { Description: 'harbour app key' }), 'key');
  must(await jsonCall(i, 'TrentService.CreateAlias', '1.1', { AliasName: 'alias/harbour-app', TargetKeyId: key.json.KeyMetadata.KeyId }), 'alias');
  // Something sealed with it, for the app to open (the Decrypt of the afternoon).
  const sealed = must(
    await jsonCall(i, 'TrentService.Encrypt', '1.1', { KeyId: 'alias/harbour-app', Plaintext: Buffer.from('harbour').toString('base64') }),
    'encrypt',
  );
  // Parameter Store holds nine (Main's rail): the stack's, below, and eight of the shop's.
  for (const p of ['currency', 'region', 'tax-rate', 'free-shipping-over', 'depot', 'support-email', 'feature-returns', 'feature-loyalty'])
    must(await jsonCall(i, 'AmazonSSM.PutParameter', '1.1', { Name: `/harbour/${p}`, Value: p, Type: 'String' }), `parameter ${p}`);
  // The rail draws six log groups (Main): /harbour/checkout is the harbour seed's, the two
  // functions' groups appear when they run, and these are the other three.
  for (const name of ['/harbour/depot', '/harbour/payments', '/harbour/storefront']) {
    const g = await fetch(`${server.origin}/_console/logs/create`, {
      method: 'POST',
      redirect: 'manual',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({ name }),
    });
    if (g.status >= 400) warn(`wire seed: log group ${name} -> ${g.status}`);
  }
  // A stack, to be updated.
  must(await queryCall(i, '2010-05-15', { Action: 'CreateStack', StackName: 'harbour-loyalty', TemplateBody: TEMPLATE('1') }), 'create stack');
  // Three alarms, one of them firing (the "Needs a look" panel's second callout).
  for (const name of ['harbour-payment-declines', 'harbour-queue-age', 'harbour-lambda-errors'])
    must(
      await jsonCall(i, 'GraniteServiceVersion20100801.PutMetricAlarm', '1.0', {
        AlarmName: name, Namespace: 'Harbour', MetricName: name.replace('harbour-', ''),
        Statistic: 'Sum', Period: 60, EvaluationPeriods: 1, Threshold: 5, ComparisonOperator: 'GreaterThanThreshold',
        // No metric is being put, and an alarm with no data goes to INSUFFICIENT_DATA a
        // few seconds after it is set. Treated as breaching, it stays in ALARM.
        TreatMissingData: name === 'harbour-payment-declines' ? 'breaching' : 'notBreaching',
      }),
      `alarm ${name}`,
    );
  must(
    await jsonCall(i, 'GraniteServiceVersion20100801.SetAlarmState', '1.0', {
      AlarmName: 'harbour-payment-declines', StateValue: 'ALARM', StateReason: 'declines went over 5 in a minute',
    }),
    'alarm state',
  );
  // The second function, the one an app invokes directly (the validator is the
  // first, made by seedHarbour and read from the queue).
  const r = await fetch(`${server.origin}/_console/lambda/create`, {
    method: 'POST',
    redirect: 'manual',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ name: 'harbour-price-calculator', runtime: 'provided.al2', handler: 'bootstrap', code: HANDLER_DIR, timeout: '30' }),
  });
  if (r.status >= 400) warn(`wire seed: price calculator -> ${r.status}`);
  return sealed.json.CiphertextBlob as string;
}

export interface WireOptions {
  /**
   * The Dark board's afternoon: the instance enforces IAM, a bucket policy
   * denies reads of one product image, and the afternoon reads it once (a 403
   * the page shows as "403 denied"). Only on an instance booted with
   * --iam-mode enforce: in soft mode the read would go through.
   */
  denyRead?: boolean;
}

const IMAGES = 'harbour-product-images';
const IMAGE_KEY = 'catalogue/AMB-COF-227.png';

/**
 * The afternoon. The validator's event source mapping is enabled last, so the
 * messages the harbour seed left on the queue are read before the ring is
 * cleared and not after.
 */
export async function seedWire(i: Inst, warn: (s: string) => void, opts: WireOptions = {}): Promise<void> {
  buildHandler();
  await seedHarbour(i, warn, { handlerDir: HANDLER_DIR });
  // The shop's FIFO dispatch queue is the other boards' (the SQS pages). The
  // wire boards' dispatch queue is the one the bucket tells, which cannot be
  // FIFO, and the rail counts four queues either way.
  must(await i.sqs('DeleteQueue', { QueueUrl: i.queueUrl(FIFO) }), 'delete the FIFO queue');
  const blob = await seedShop(i, warn);
  if (opts.denyRead) {
    // Made before the ring is cleared, so none of it is on the wire. Under enforce a bucket's
    // notification needs the queue's own say-so, as on AWS: without this policy the receipt's
    // notification would be a second denied call of the afternoon.
    must(
      await i.sqs('SetQueueAttributes', {
        QueueUrl: i.queueUrl(DISPATCH),
        Attributes: {
          Policy: JSON.stringify({
            Version: '2012-10-17',
            Statement: [{ Sid: 'S3Notifications', Effect: 'Allow', Principal: { Service: 's3.amazonaws.com' }, Action: 'sqs:SendMessage', Resource: i.queueArn(DISPATCH) }],
          }),
        },
      }),
      'dispatch queue policy',
    );
    must(await rest(i, 'PUT', `/${IMAGES}`), 'create bucket images');
    must(await rest(i, 'PUT', `/${IMAGES}/${IMAGE_KEY}`, 'png', 'image/png'), 'put image');
    must(
      await rest(
        i,
        'PUT',
        `/${IMAGES}?policy`,
        JSON.stringify({
          Version: '2012-10-17',
          Statement: [{ Sid: 'DenyReads', Effect: 'Deny', Principal: '*', Action: 's3:GetObject', Resource: `arn:aws:s3:::${IMAGES}/*` }],
        }),
        'application/json',
      ),
      'bucket policy',
    );
  }

  const list = await rest(i, 'GET', '/2015-03-31/event-source-mappings/');
  const uuid = list.json?.EventSourceMappings?.[0]?.UUID;
  if (!uuid) throw new Error('wire seed: no event source mapping to enable');
  must(await rest(i, 'PUT', `/2015-03-31/event-source-mappings/${uuid}`, JSON.stringify({ Enabled: true }), 'application/json'), 'enable mapping');
  // Let the mapping read what is already there (the validator writes each order
  // to the table), then start the ring over.
  await sleep(4000);
  const cleared = await fetch(`${i.server.origin}/_console/traffic/clear`, { method: 'POST', headers: { 'HX-Request': 'true' } });
  if (!cleared.ok) throw new Error(`wire seed: clear -> ${cleared.status}`);

  // The calls, oldest first (the board reads newest first).
  must(await queryCall(i, '2010-05-15', { Action: 'UpdateStack', StackName: 'harbour-loyalty', TemplateBody: TEMPLATE('2') }), 'update stack');
  // A long poll on an empty queue: the call that waited twenty seconds. Started
  // now, it ends last and is still ordered by when it began.
  const longPoll = i.sqs('ReceiveMessage', { QueueUrl: i.queueUrl(LOYALTY), WaitTimeSeconds: 20 });
  await sleep(150);
  must(await jsonCall(i, 'TrentService.Decrypt', '1.1', { KeyId: 'alias/harbour-app', CiphertextBlob: blob }), 'decrypt');
  must(await jsonCall(i, 'secretsmanager.GetSecretValue', '1.1', { SecretId: 'harbour/db' }), 'get secret');
  // The price calculator is called without a unit_price and raises.
  await rest(i, 'POST', '/2015-03-31/functions/harbour-price-calculator/invocations', JSON.stringify({ price: { sku: 'HRB-1' } }), 'application/json');
  if (opts.denyRead) {
    // A read the bucket's policy refuses: after the function that threw, before the refused send.
    const denied = await rest(i, 'GET', `/${IMAGES}/${IMAGE_KEY}`);
    if (denied.status !== 403) throw new Error(`wire seed: the read of ${IMAGE_KEY} was not denied (${denied.status}); is the instance in IAM enforce mode?`);
  }
  // A send the service refuses: a deduplication id on a standard queue.
  await i.sqs('SendMessage', {
    QueueUrl: i.queueUrl(MAIN),
    MessageBody: JSON.stringify({ orderId: 'HRB-2026-04810' }),
    MessageDeduplicationId: 'harbour-orders',
  });
  // A receipt, which the bucket tells the dispatch queue about.
  must(await rest(i, 'PUT', '/harbour-receipts/2026/HRB-2026-04811.pdf', 'receipt', 'application/pdf'), 'put receipt');
  // An order, which the queue's event source mapping hands to the validator.
  must(await i.sqs('SendMessage', { QueueUrl: i.queueUrl(MAIN), MessageBody: JSON.stringify({ orderId: 'HRB-2026-04811', lines: 3 }) }), 'send order');
  // Wait for what the order set off (the validator runs, and writes its row) and
  // for the long poll to come back.
  await longPoll;
  await sleep(1500);
}
