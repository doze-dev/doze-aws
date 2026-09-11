// The trading hour: a stack that is being used rather than one that was built.
//
// Everything before this creates resources. This puts traffic through them, on
// a loop, so the Traffic feed scrolls, the SQS peek panel refills, the live log
// tail moves and CloudWatch gets fresh points while you are looking at it.
//
// Run it for as long as you need the screenshots to take:
//   bun seed.ts --trade 5      five minutes of it

import { sqs, sns, events, kinesis, cw, logs, lambda, sfn, ddb, arn } from '../lib/aws';
import { section, step, note, look, sleep, did } from '../lib/say';
import { SendMessageCommand, GetQueueUrlCommand, ReceiveMessageCommand, DeleteMessageCommand } from '@aws-sdk/client-sqs';
import { PublishCommand } from '@aws-sdk/client-sns';
import { PutEventsCommand } from '@aws-sdk/client-eventbridge';
import { PutRecordCommand } from '@aws-sdk/client-kinesis';
import { PutMetricDataCommand } from '@aws-sdk/client-cloudwatch';
import { PutLogEventsCommand, CreateLogStreamCommand } from '@aws-sdk/client-cloudwatch-logs';
import { InvokeCommand } from '@aws-sdk/client-lambda';
import { StartExecutionCommand } from '@aws-sdk/client-sfn';
import { PutCommand } from '@aws-sdk/lib-dynamodb';
import { buildOrder, ORDER_BASKETS, CUSTOMERS, PRODUCTS, depotStock } from '../lib/data';
import { QUEUE_ORDERS, TOPIC_ORDER_EVENTS, BUS, STREAM } from './messaging';

const NS = 'Harbour/Orders';

/** One shopper's journey, start to finish, across six services. */
async function oneOrder(queueUrl: string, topicArn: string, i: number) {
  const order = buildOrder(i, ORDER_BASKETS[i % ORDER_BASKETS.length], i % 4 === 0 ? 'express' : 'standard');
  const totalPence = Math.round(
    order.lines.reduce((s, l) => s + l.unitPriceGbp * l.quantity, 0) * 100
  );

  // Browsing, before the order exists.
  await kinesis.send(new PutRecordCommand({
    StreamName: STREAM, PartitionKey: `sess-${order.orderId}`,
    Data: new TextEncoder().encode(JSON.stringify({
      event: 'checkout_start', session: `sess-${order.orderId}`,
      customerId: order.customer.id, basketPence: totalPence, at: new Date().toISOString(),
    })),
  }));

  await sqs.send(new SendMessageCommand({
    QueueUrl: queueUrl, MessageBody: JSON.stringify(order),
    MessageAttributes: {
      channel: { DataType: 'String', StringValue: order.channel },
      tier: { DataType: 'String', StringValue: order.customer.tier },
    },
  }));

  await events.send(new PutEventsCommand({
    Entries: [{
      EventBusName: BUS, Source: 'harbour.checkout', DetailType: 'order.placed',
      Detail: JSON.stringify({ orderId: order.orderId, totalPence, customerId: order.customer.id }),
    }],
  }));

  await sns.send(new PublishCommand({
    TopicArn: topicArn, Subject: `Order ${order.orderId} placed`,
    Message: JSON.stringify({ orderId: order.orderId, totalPence }),
    MessageAttributes: {
      eventType: { DataType: 'String', StringValue: 'order.placed' },
      totalPence: { DataType: 'Number', StringValue: String(totalPence) },
    },
  }));

  await ddb.send(new PutCommand({
    TableName: 'harbour-orders',
    Item: {
      customerId: order.customer.id, placedAt: order.placedAt, orderId: order.orderId,
      status: 'PLACED', totalPence, channel: order.channel,
      lineCount: order.lines.length, depot: i % 2 === 0 ? 'DEP-EDI' : 'DEP-GLA',
    },
  }));

  await cw.send(new PutMetricDataCommand({
    Namespace: NS,
    MetricData: [
      { MetricName: 'OrdersPlaced', Value: 1, Unit: 'Count',
        Dimensions: [{ Name: 'Channel', Value: order.channel === 'web' ? 'web' : 'ios' }] },
      { MetricName: 'BasketValue', Value: totalPence, Unit: 'None' },
    ],
  }));

  return { order, totalPence };
}

export async function trading(minutes: number) {
  section('Trading', `${minutes} minute(s) of a shop that is open — watch the console while this runs`);
  note('the Traffic feed, the SQS peek panel, the log tail and the alarms all move from here');

  const q = await sqs.send(new GetQueueUrlCommand({ QueueName: QUEUE_ORDERS }));
  const topicArn = arn('sns', TOPIC_ORDER_EVENTS);
  const stream = `trading/${new Date().toISOString().slice(0, 10)}/${Date.now().toString(36)}`;
  await logs.send(new CreateLogStreamCommand({ logGroupName: '/harbour/checkout', logStreamName: stream })).catch(() => {});

  const until = Date.now() + minutes * 60_000;
  let placed = 0;

  look('/_console/', 'the wire — every call below appears there as it happens');

  while (Date.now() < until) {
    const i = placed;
    try {
      const { order, totalPence } = await oneOrder(q.QueueUrl!, topicArn, i);
      placed += 1;
      did(`order ${order.orderId} — £${(totalPence / 100).toFixed(2)}, ${order.channel}`);

      await logs.send(new PutLogEventsCommand({
        logGroupName: '/harbour/checkout', logStreamName: stream,
        logEvents: [{
          timestamp: Date.now(),
          message: `INFO  ${order.orderId} checked out by ${order.customer.id} (£${(totalPence / 100).toFixed(2)}, ${order.channel})`,
        }],
      })).catch(() => {});

      // Every third order goes all the way through the workflow.
      if (i % 3 === 0) {
        await sfn.send(new StartExecutionCommand({
          stateMachineArn: arn('states', 'stateMachine:harbour-order-fulfilment'),
          name: `${order.orderId}-${Date.now().toString(36)}`,
          input: JSON.stringify({ ...order, depotStock: depotStock() }),
        })).catch(() => {});
        did(`  → fulfilment workflow started for ${order.orderId}`);
      }

      // Every fifth, a driver reports a problem — so the feed is not all green.
      if (i % 5 === 4) {
        await events.send(new PutEventsCommand({
          Entries: [{
            EventBusName: BUS, Source: 'harbour.delivery', DetailType: 'delivery.failed',
            Detail: JSON.stringify({ orderId: order.orderId, van: 'EDI-VAN-03', reason: 'nobody home, no safe place' }),
          }],
        }));
        await logs.send(new PutLogEventsCommand({
          logGroupName: '/harbour/checkout', logStreamName: stream,
          logEvents: [{ timestamp: Date.now(), message: `ERROR delivery failed for ${order.orderId}: nobody home` }],
        })).catch(() => {});
        did(`  → delivery failed for ${order.orderId}`);
      }

      // A consumer drains one, so the queue depth moves both ways.
      if (i % 2 === 1) {
        const got = await sqs.send(new ReceiveMessageCommand({ QueueUrl: q.QueueUrl!, MaxNumberOfMessages: 1 }));
        const m = got.Messages?.[0];
        if (m) await sqs.send(new DeleteMessageCommand({ QueueUrl: q.QueueUrl!, ReceiptHandle: m.ReceiptHandle! }));
      }
    } catch (err) {
      note(`an order did not complete: ${(err as Error).message}`);
    }

    await sleep(4000);
  }

  note(`${placed} orders placed over ${minutes} minute(s)`);
  look('/_console/cw', 'the alarms have live data under them now');
}
