// S3 and DynamoDB: the product catalogue, the customer book, and the buckets
// the pipeline writes into.

import { s3, ddb, ddbRaw } from '../lib/aws';
import { section, step, note, look } from '../lib/say';
import {
  CreateBucketCommand, PutObjectCommand, PutBucketVersioningCommand, PutBucketTaggingCommand,
  PutBucketCorsCommand, PutBucketLifecycleConfigurationCommand, PutBucketPolicyCommand,
  PutBucketWebsiteCommand, DeleteObjectCommand, PutObjectTaggingCommand, CopyObjectCommand,
  PutPublicAccessBlockCommand,
} from '@aws-sdk/client-s3';
import { CreateTableCommand, UpdateTimeToLiveCommand, TagResourceCommand } from '@aws-sdk/client-dynamodb';
import { PutCommand, UpdateCommand, TransactWriteCommand } from '@aws-sdk/lib-dynamodb';
import { PRODUCTS, CUSTOMERS, DEPOTS } from '../lib/data';

const SITE_INDEX = `<!doctype html>
<title>Harbour Groceries</title>
<h1>Harbour</h1>
<p>Fresh food, delivered across central Scotland. Slots from 8am.</p>
<p><a href="/delivery.html">Where we deliver</a></p>
`;

const SITE_DELIVERY = `<!doctype html>
<title>Where we deliver — Harbour</title>
<h1>Where we deliver</h1>
<ul><li>Edinburgh (EH)</li><li>Glasgow (G)</li><li>Fife (KY)</li>
<li>Falkirk &amp; Stirling (FK)</li><li>Lanarkshire (ML)</li></ul>
`;

export async function storage() {
  section('S3', 'product imagery, receipts, the public site, and a nightly export');

  const buckets: [string, string][] = [
    ['harbour-product-images', 'Everything the storefront renders'],
    ['harbour-receipts', 'One text receipt per order, written by the pipeline'],
    ['harbour-data-exports', 'Nightly extracts for the warehouse team'],
    ['harbour-www', 'The public marketing site'],
  ];
  for (const [name, why] of buckets) {
    await step(`created ${name}`, () => s3.send(new CreateBucketCommand({ Bucket: name })), 's3:CreateBucket');
    note(why);
  }

  await step('turned on versioning for receipts', () =>
    s3.send(new PutBucketVersioningCommand({
      Bucket: 'harbour-receipts', VersioningConfiguration: { Status: 'Enabled' },
    })), 's3:PutBucketVersioning');

  await step('tagged the buckets', async () => {
    for (const [name] of buckets) {
      await s3.send(new PutBucketTaggingCommand({
        Bucket: name,
        Tagging: { TagSet: [{ Key: 'team', Value: 'platform' }, { Key: 'env', Value: 'prod' }] },
      }));
    }
  }, 's3:PutBucketTagging');

  await step('allowed the storefront origin through CORS', () =>
    s3.send(new PutBucketCorsCommand({
      Bucket: 'harbour-product-images',
      CORSConfiguration: {
        CORSRules: [{
          AllowedOrigins: ['https://harbour.example.com', 'http://localhost:3000'],
          AllowedMethods: ['GET', 'HEAD'], AllowedHeaders: ['*'], MaxAgeSeconds: 3600,
        }],
      },
    })), 's3:PutBucketCors');

  await step('expired old exports after 30 days', () =>
    s3.send(new PutBucketLifecycleConfigurationCommand({
      Bucket: 'harbour-data-exports',
      LifecycleConfiguration: {
        Rules: [
          { ID: 'expire-nightly-extracts', Status: 'Enabled', Filter: { Prefix: 'nightly/' }, Expiration: { Days: 30 } },
          { ID: 'clean-up-failed-uploads', Status: 'Enabled', Filter: { Prefix: '' },
            AbortIncompleteMultipartUpload: { DaysAfterInitiation: 7 } },
        ],
      },
    })), 's3:PutBucketLifecycleConfiguration');

  await step('published the marketing site', async () => {
    await s3.send(new PutObjectCommand({ Bucket: 'harbour-www', Key: 'index.html', Body: SITE_INDEX, ContentType: 'text/html' }));
    await s3.send(new PutObjectCommand({ Bucket: 'harbour-www', Key: 'delivery.html', Body: SITE_DELIVERY, ContentType: 'text/html' }));
    await s3.send(new PutBucketWebsiteCommand({
      Bucket: 'harbour-www',
      WebsiteConfiguration: { IndexDocument: { Suffix: 'index.html' }, ErrorDocument: { Key: 'index.html' } },
    }));
  }, 's3:PutBucketWebsite');

  // Block Public Access is on by default and refuses a public bucket policy —
  // doze-aws enforces it exactly as AWS does, which is why this call has to
  // come first and why nobody makes a bucket public by accident.
  await step('lifted Block Public Access on the site bucket', () =>
    s3.send(new PutPublicAccessBlockCommand({
      Bucket: 'harbour-www',
      PublicAccessBlockConfiguration: {
        BlockPublicAcls: false, IgnorePublicAcls: false,
        BlockPublicPolicy: false, RestrictPublicBuckets: false,
      },
    })), 's3:PutPublicAccessBlock');

  await step('made the site bucket publicly readable', () =>
    s3.send(new PutBucketPolicyCommand({
      Bucket: 'harbour-www',
      Policy: JSON.stringify({
        Version: '2012-10-17',
        Statement: [{ Sid: 'PublicRead', Effect: 'Allow', Principal: '*', Action: 's3:GetObject',
          Resource: 'arn:aws:s3:::harbour-www/*' }],
      }, null, 2),
    })), 's3:PutBucketPolicy');

  await step('uploaded product imagery into department folders', async () => {
    for (const p of PRODUCTS) {
      const folder = p.dept.toLowerCase().replace(/[^a-z]+/g, '-').replace(/^-|-$/g, '');
      // A tiny but real 1×1 PNG, so the console has something with a content
      // type and a size rather than a text file pretending to be an image.
      const png = Buffer.from(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
        'base64'
      );
      await s3.send(new PutObjectCommand({
        Bucket: 'harbour-product-images', Key: `catalogue/${folder}/${p.sku}.png`,
        Body: png, ContentType: 'image/png',
        Metadata: { sku: p.sku, dept: p.dept },
      }));
    }
  }, 's3:PutObject ×15');

  await step('tagged one image for the seasonal campaign', () =>
    s3.send(new PutObjectTaggingCommand({
      Bucket: 'harbour-product-images', Key: 'catalogue/bakery/BAK-SRD-800.png',
      Tagging: { TagSet: [{ Key: 'campaign', Value: 'autumn-bakes' }, { Key: 'reviewed', Value: 'yes' }] },
    })), 's3:PutObjectTagging');

  await step('wrote a nightly CSV export', () =>
    s3.send(new PutObjectCommand({
      Bucket: 'harbour-data-exports', Key: 'nightly/2026-09-11/orders.csv',
      ContentType: 'text/csv',
      Body: ['order_id,placed_at,customer_id,total_pence,depot',
        'HRB-2026-04801,2026-09-11T06:12:04Z,CUS-10482,2340,DEP-EDI',
        'HRB-2026-04802,2026-09-11T07:45:19Z,CUS-10517,4185,DEP-GLA',
        'HRB-2026-04803,2026-09-11T09:02:55Z,CUS-10623,1875,DEP-EDI'].join('\n'),
    })), 's3:PutObject');

  await step('copied last night’s export aside, then deleted the copy', async () => {
    await s3.send(new CopyObjectCommand({
      Bucket: 'harbour-data-exports', Key: 'nightly/2026-09-10/orders.csv',
      CopySource: '/harbour-data-exports/nightly/2026-09-11/orders.csv',
    }));
    await s3.send(new DeleteObjectCommand({ Bucket: 'harbour-data-exports', Key: 'nightly/2026-09-10/orders.csv' }));
  }, 's3:CopyObject + DeleteObject');
  note('the delete leaves a marker in a versioned bucket, which the console shows');

  look('/_console/s3', 'folders, versioning, lifecycle, CORS and the bucket-policy builder');

  // --- DynamoDB -----------------------------------------------------------
  section('DynamoDB', 'the catalogue, the customer book, orders and a TTL-backed basket');

  await step('created harbour-products', () =>
    ddbRaw.send(new CreateTableCommand({
      TableName: 'harbour-products',
      BillingMode: 'PAY_PER_REQUEST',
      KeySchema: [{ AttributeName: 'sku', KeyType: 'HASH' }],
      AttributeDefinitions: [
        { AttributeName: 'sku', AttributeType: 'S' },
        { AttributeName: 'dept', AttributeType: 'S' },
        { AttributeName: 'priceGbp', AttributeType: 'N' },
      ],
      GlobalSecondaryIndexes: [{
        IndexName: 'by-dept-price',
        KeySchema: [{ AttributeName: 'dept', KeyType: 'HASH' }, { AttributeName: 'priceGbp', KeyType: 'RANGE' }],
        Projection: { ProjectionType: 'ALL' },
      }],
    })), 'dynamodb:CreateTable');

  await step('created harbour-orders with a sort key', () =>
    ddbRaw.send(new CreateTableCommand({
      TableName: 'harbour-orders',
      BillingMode: 'PAY_PER_REQUEST',
      KeySchema: [
        { AttributeName: 'customerId', KeyType: 'HASH' },
        { AttributeName: 'placedAt', KeyType: 'RANGE' },
      ],
      AttributeDefinitions: [
        { AttributeName: 'customerId', AttributeType: 'S' },
        { AttributeName: 'placedAt', AttributeType: 'S' },
        { AttributeName: 'status', AttributeType: 'S' },
      ],
      GlobalSecondaryIndexes: [{
        IndexName: 'by-status',
        KeySchema: [{ AttributeName: 'status', KeyType: 'HASH' }, { AttributeName: 'placedAt', KeyType: 'RANGE' }],
        Projection: { ProjectionType: 'ALL' },
      }],
      StreamSpecification: { StreamEnabled: true, StreamViewType: 'NEW_AND_OLD_IMAGES' },
    })), 'dynamodb:CreateTable');

  await step('created harbour-customers', () =>
    ddbRaw.send(new CreateTableCommand({
      TableName: 'harbour-customers',
      BillingMode: 'PAY_PER_REQUEST',
      KeySchema: [{ AttributeName: 'customerId', KeyType: 'HASH' }],
      AttributeDefinitions: [{ AttributeName: 'customerId', AttributeType: 'S' }],
    })), 'dynamodb:CreateTable');

  await step('created harbour-baskets, which expire', () =>
    ddbRaw.send(new CreateTableCommand({
      TableName: 'harbour-baskets',
      BillingMode: 'PAY_PER_REQUEST',
      KeySchema: [{ AttributeName: 'basketId', KeyType: 'HASH' }],
      AttributeDefinitions: [{ AttributeName: 'basketId', AttributeType: 'S' }],
    })), 'dynamodb:CreateTable');

  await step('set an abandoned-basket TTL', () =>
    ddbRaw.send(new UpdateTimeToLiveCommand({
      TableName: 'harbour-baskets',
      TimeToLiveSpecification: { Enabled: true, AttributeName: 'expiresAt' },
    })), 'dynamodb:UpdateTimeToLive');
  note('a basket nobody checks out disappears on its own — no sweeper to write');

  await step('tagged the orders table', () =>
    ddbRaw.send(new TagResourceCommand({
      ResourceArn: `arn:aws:dynamodb:eu-west-1:000000000000:table/harbour-orders`,
      Tags: [{ Key: 'team', Value: 'orders' }, { Key: 'pii', Value: 'yes' }],
    })), 'dynamodb:TagResource');

  await step('loaded the product catalogue', async () => {
    for (const p of PRODUCTS) {
      await ddb.send(new PutCommand({
        TableName: 'harbour-products',
        Item: {
          sku: p.sku, name: p.name, dept: p.dept, priceGbp: p.priceGbp, vatable: p.vatable,
          ...(p.multibuy ? { multibuy: p.multibuy } : {}),
          imageKey: `catalogue/${p.dept.toLowerCase().replace(/[^a-z]+/g, '-').replace(/^-|-$/g, '')}/${p.sku}.png`,
          active: true,
        },
      }));
    }
  }, 'dynamodb:PutItem ×15');

  await step('loaded the customer book', async () => {
    for (const c of CUSTOMERS) {
      await ddb.send(new PutCommand({
        TableName: 'harbour-customers',
        Item: {
          customerId: c.id, name: c.name, email: c.email, mobile: c.mobile,
          postcode: c.postcode, tier: c.tier, memberSince: c.since,
          marketingOptIn: c.tier === 'plus',
          deliveryNotes: c.id === 'CUS-10482' ? 'Buzzer is broken, please knock' : '',
        },
      }));
    }
  }, 'dynamodb:PutItem ×6');

  await step('added two abandoned baskets that expire in an hour', async () => {
    const expiresAt = Math.floor(Date.now() / 1000) + 3600;
    for (const [i, c] of CUSTOMERS.slice(0, 2).entries()) {
      await ddb.send(new PutCommand({
        TableName: 'harbour-baskets',
        Item: {
          basketId: `BSK-${c.id}-${i + 1}`, customerId: c.id, expiresAt,
          lines: [{ sku: 'BAK-CRS-4', quantity: 1 }, { sku: 'AMB-COF-227', quantity: 1 }],
          updatedAt: new Date().toISOString(),
        },
      }));
    }
  }, 'dynamodb:PutItem');

  await step('moved a product to a new price, in one transaction with its audit row', () =>
    ddb.send(new TransactWriteCommand({
      TransactItems: [
        { Update: {
          TableName: 'harbour-products', Key: { sku: 'AMB-COF-227' },
          UpdateExpression: 'SET priceGbp = :new, priceChangedAt = :now',
          ExpressionAttributeValues: { ':new': 5.25, ':now': new Date().toISOString() },
        } },
        { Put: {
          TableName: 'harbour-orders',
          Item: { customerId: 'SYSTEM#price-changes', placedAt: new Date().toISOString(),
            status: 'AUDIT', sku: 'AMB-COF-227', from: 4.95, to: 5.25, by: 'jo.mcleod' },
        } },
      ],
    })), 'dynamodb:TransactWriteItems');
  note('both rows land or neither does — the price never moves without its audit trail');

  await step('marked one product out of stock', () =>
    ddb.send(new UpdateCommand({
      TableName: 'harbour-products', Key: { sku: 'BAK-SRD-800' },
      UpdateExpression: 'SET active = :off, outOfStockSince = :now',
      ExpressionAttributeValues: { ':off': false, ':now': new Date().toISOString() },
    })), 'dynamodb:UpdateItem');

  look('/_console/ddb', 'items, the GSI, TTL, PartiQL and the prefilled add-item editor');
}
