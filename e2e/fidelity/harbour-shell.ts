// What the shell boards (Palette, Dark, States, Anatomy, Narrow) draw that is
// not a queue: the calls that other services' traffic leaves on the wire, and
// the resources they were made to. Seeded through AWS calls like the rest of
// the harbour fixture, tolerantly: a seed that fails warns and the boards that
// needed it say what is missing, because a fixture that throws takes every
// board with it.
//
// Palette draws, under its dialog, "PutObject harbour-receipts/2026/HRB-2026-04811.pdf"
// and "Query harbour-products"; States draws a DynamoDB Scan taking a while.

import type { Inst } from './drivers/types';

/** A JSON-protocol call (DynamoDB 1.0). */
async function json10(i: Inst, target: string, body: Record<string, unknown>): Promise<{ status: number; body: any }> {
  const r = await fetch(`${i.server.origin}/`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': target },
    body: JSON.stringify(body),
  });
  const t = await r.text();
  try {
    return { status: r.status, body: JSON.parse(t) };
  } catch {
    return { status: r.status, body: t };
  }
}

/**
 * Turn the fixture's Lambda trigger on, so the neighbours strip says "read by"
 * (a disabled mapping is "mapping disabled", and is right to be). Do it AFTER
 * the queue's messages are held or gone: an enabled trigger receives whatever
 * is visible and deletes it, which is what a reader is for.
 */
export async function enableTrigger(i: Inst, fn = 'harbour-order-validator'): Promise<void> {
  const list = await fetch(`${i.server.origin}/2015-03-31/event-source-mappings/?FunctionName=${fn}`);
  const body: any = await list.json();
  const m = body.EventSourceMappings?.[0];
  if (!m) throw new Error(`no event source mapping for ${fn}`);
  const r = await fetch(`${i.server.origin}/2015-03-31/event-source-mappings/${m.UUID}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ Enabled: true }),
  });
  if (!r.ok) throw new Error(`enable trigger -> ${r.status}`);
  // The console draws its wiring from a snapshot that is a few seconds old; wait
  // until a queue page says the function reads it, or say it never did.
  const page = i.url('sqs/harbour-checkout-orders');
  const until = Date.now() + 20_000;
  for (;;) {
    const t = await (await fetch(page)).text();
    if (t.includes('<span class="nb-lbl">read by</span>')) return;
    if (Date.now() > until) throw new Error('the queue page still says "mapping disabled" 20 s after the trigger was enabled');
    await new Promise((res) => setTimeout(res, 500));
  }
}

export const RECEIPTS = 'harbour-receipts';
export const RECEIPT_KEY = '2026/HRB-2026-04811.pdf';
export const PRODUCTS = 'harbour-products';

const seeded = new Set<string>();

/** Once per instance; a second board that needs it finds it there. */
export async function seedShell(i: Inst, warn: (s: string) => void): Promise<void> {
  if (seeded.has(i.server.origin)) return;
  seeded.add(i.server.origin);
  try {
    // S3, path style: PUT /bucket, then PUT /bucket/key.
    let r = await fetch(`${i.server.origin}/${RECEIPTS}`, { method: 'PUT' });
    if (!r.ok && r.status !== 409) warn(`seed: CreateBucket ${RECEIPTS} -> ${r.status}`);
    r = await fetch(`${i.server.origin}/${RECEIPTS}/${RECEIPT_KEY}`, { method: 'PUT', headers: { 'Content-Type': 'application/pdf' }, body: '%PDF-1.4 harbour receipt HRB-2026-04811\n' });
    if (!r.ok) warn(`seed: PutObject ${RECEIPT_KEY} -> ${r.status}`);

    // DynamoDB: a products table with two items and a Query over one of them.
    const make = await json10(i, 'DynamoDB_20120810.CreateTable', {
      TableName: PRODUCTS,
      AttributeDefinitions: [{ AttributeName: 'sku', AttributeType: 'S' }],
      KeySchema: [{ AttributeName: 'sku', KeyType: 'HASH' }],
      BillingMode: 'PAY_PER_REQUEST',
    });
    if (make.status !== 200 && make.status !== 400) warn(`seed: CreateTable ${PRODUCTS} -> ${make.status}`);
    for (const [sku, name] of [['AMB-COF-227', 'Amber coffee, 227 g'], ['AMB-TEA-100', 'Amber tea, 100 g']] as const)
      await json10(i, 'DynamoDB_20120810.PutItem', { TableName: PRODUCTS, Item: { sku: { S: sku }, name: { S: name } } });
    const q = await json10(i, 'DynamoDB_20120810.Query', {
      TableName: PRODUCTS,
      KeyConditionExpression: 'sku = :s',
      ExpressionAttributeValues: { ':s': { S: 'AMB-COF-227' } },
    });
    if (q.status !== 200) warn(`seed: Query ${PRODUCTS} -> ${q.status}`);
  } catch (e) {
    warn(`seed: shell extras skipped: ${e}`);
  }
}
