// SQS, SNS, EventBridge and Kinesis — how an order moves between the services
// that handle it.

import { sqs, sns, events, kinesis, arn, REGION } from '../lib/aws';
import { section, step, note, look, watch } from '../lib/say';
import {
  CreateQueueCommand, SetQueueAttributesCommand, SendMessageCommand, SendMessageBatchCommand,
  TagQueueCommand, ReceiveMessageCommand, ChangeMessageVisibilityCommand,
} from '@aws-sdk/client-sqs';
import {
  CreateTopicCommand, SubscribeCommand, PublishCommand, SetSubscriptionAttributesCommand,
  TagResourceCommand as SnsTag, SetTopicAttributesCommand,
} from '@aws-sdk/client-sns';
import {
  CreateEventBusCommand, PutRuleCommand, PutTargetsCommand, PutEventsCommand,
  CreateArchiveCommand, TagResourceCommand as EbTag,
} from '@aws-sdk/client-eventbridge';
import {
  CreateStreamCommand, PutRecordCommand, PutRecordsCommand, AddTagsToStreamCommand,
  RegisterStreamConsumerCommand, IncreaseStreamRetentionPeriodCommand,
} from '@aws-sdk/client-kinesis';
import { buildOrder, ORDER_BASKETS, CUSTOMERS, PRODUCTS } from '../lib/data';

export const QUEUE_ORDERS = 'harbour-checkout-orders';
export const QUEUE_DLQ = 'harbour-checkout-orders-dlq';
export const QUEUE_DISPATCH = 'harbour-depot-dispatch.fifo';
export const TOPIC_ORDER_EVENTS = 'harbour-order-events';
export const TOPIC_OPS = 'harbour-ops-alerts';
export const BUS = 'harbour-events';
export const STREAM = 'harbour-storefront-clicks';

export async function messaging() {
  section('SQS', 'the checkout queue, its dead letter queue, and a FIFO dispatch lane');

  const dlq = await step(`created ${QUEUE_DLQ}`, () =>
    sqs.send(new CreateQueueCommand({
      QueueName: QUEUE_DLQ,
      Attributes: { MessageRetentionPeriod: '1209600' }, // 14 days, the AWS maximum
    })), 'sqs:CreateQueue');
  note('14-day retention: a poison message has to survive a long weekend');

  const main = await step(`created ${QUEUE_ORDERS}`, () =>
    sqs.send(new CreateQueueCommand({
      QueueName: QUEUE_ORDERS,
      Attributes: { VisibilityTimeout: '60', ReceiveMessageWaitTimeSeconds: '10' },
    })), 'sqs:CreateQueue');

  await step('wired the DLQ in after 3 failed receives', () =>
    sqs.send(new SetQueueAttributesCommand({
      QueueUrl: main!.QueueUrl!,
      Attributes: {
        RedrivePolicy: JSON.stringify({
          deadLetterTargetArn: arn('sqs', QUEUE_DLQ), maxReceiveCount: 3,
        }),
      },
    })), 'sqs:SetQueueAttributes');

  await step('tagged the checkout queue', () =>
    sqs.send(new TagQueueCommand({
      QueueUrl: main!.QueueUrl!, Tags: { team: 'orders', env: 'prod', 'cost-centre': 'CC-4181' },
    })), 'sqs:TagQueue');

  await step(`created ${QUEUE_DISPATCH}`, () =>
    sqs.send(new CreateQueueCommand({
      QueueName: QUEUE_DISPATCH,
      Attributes: {
        FifoQueue: 'true', ContentBasedDeduplication: 'true',
        DeduplicationScope: 'messageGroup', FifoThroughputLimit: 'perMessageGroupId',
      },
    })), 'sqs:CreateQueue');
  note('one message group per van, so a van’s drops stay in order without blocking the depot');

  await step('queued five checkout orders', async () => {
    const entries = ORDER_BASKETS.map((basket, i) => {
      const order = buildOrder(i, basket, i === 3 ? 'express' : 'standard');
      return {
        Id: order.orderId,
        MessageBody: JSON.stringify(order),
        MessageAttributes: {
          channel: { DataType: 'String', StringValue: order.channel },
          tier: { DataType: 'String', StringValue: order.customer.tier },
        },
      };
    });
    await sqs.send(new SendMessageBatchCommand({ QueueUrl: main!.QueueUrl!, Entries: entries }));
  }, 'sqs:SendMessageBatch');

  await step('dispatched two drops to the same van, in order', async () => {
    for (const [i, drop] of [['DEP-EDI', 'HRB-2026-04801'], ['DEP-EDI', 'HRB-2026-04805']].entries()) {
      await sqs.send(new SendMessageCommand({
        QueueUrl: `${main!.QueueUrl!.replace(QUEUE_ORDERS, QUEUE_DISPATCH)}`,
        MessageBody: JSON.stringify({ depot: drop[0], orderId: drop[1], van: 'EDI-VAN-07', stop: i + 1 }),
        MessageGroupId: 'EDI-VAN-07',
      }));
    }
  }, 'sqs:SendMessage (FIFO)');

  await step('took one order off the queue and put it back', async () => {
    const got = await sqs.send(new ReceiveMessageCommand({ QueueUrl: main!.QueueUrl!, MaxNumberOfMessages: 1 }));
    const m = got.Messages?.[0];
    if (m) {
      await sqs.send(new ChangeMessageVisibilityCommand({
        QueueUrl: main!.QueueUrl!, ReceiptHandle: m.ReceiptHandle!, VisibilityTimeout: 0,
      }));
    }
  }, 'sqs:ReceiveMessage + ChangeMessageVisibility');

  await step('sent one deliberately malformed order to the DLQ', () =>
    sqs.send(new SendMessageCommand({
      QueueUrl: dlq!.QueueUrl!,
      MessageBody: JSON.stringify({ orderId: 'HRB-2026-04799', lines: null, note: 'checkout v2 rollout, bad payload' }),
    })), 'sqs:SendMessage');
  note('so the DLQ page has something to redrive');

  look('/_console/sqs', 'the peek panel, redrive policy and the FIFO lane');

  // --- SNS ----------------------------------------------------------------
  section('SNS', 'order events fanned out, and an ops alert topic');

  const topic = await step(`created ${TOPIC_ORDER_EVENTS}`, () =>
    sns.send(new CreateTopicCommand({ Name: TOPIC_ORDER_EVENTS })), 'sns:CreateTopic');
  const ops = await step(`created ${TOPIC_OPS}`, () =>
    sns.send(new CreateTopicCommand({ Name: TOPIC_OPS })), 'sns:CreateTopic');

  await step('described the topic for the humans', () =>
    sns.send(new SetTopicAttributesCommand({
      TopicArn: topic!.TopicArn!, AttributeName: 'DisplayName', AttributeValue: 'Harbour orders',
    })), 'sns:SetTopicAttributes');

  await step('tagged both topics', async () => {
    for (const t of [topic!.TopicArn!, ops!.TopicArn!]) {
      await sns.send(new SnsTag({ ResourceArn: t, Tags: [{ Key: 'team', Value: 'orders' }] }));
    }
  }, 'sns:TagResource');

  const sub = await step('subscribed the checkout queue to order events', () =>
    sns.send(new SubscribeCommand({
      TopicArn: topic!.TopicArn!, Protocol: 'sqs', Endpoint: arn('sqs', QUEUE_ORDERS), ReturnSubscriptionArn: true,
    })), 'sns:Subscribe');

  await step('filtered it down to placed orders over £30', () =>
    sns.send(new SetSubscriptionAttributesCommand({
      SubscriptionArn: sub!.SubscriptionArn!,
      AttributeName: 'FilterPolicy',
      AttributeValue: JSON.stringify({
        eventType: ['order.placed'],
        totalPence: [{ numeric: ['>=', 3000] }],
      }),
    })), 'sns:SetSubscriptionAttributes');
  note('the filter runs on the message attributes, so a cheap order never wakes the consumer');

  await step('subscribed the on-call inbox to ops alerts', () =>
    sns.send(new SubscribeCommand({
      TopicArn: ops!.TopicArn!, Protocol: 'email', Endpoint: 'ops-oncall@example.com',
    })), 'sns:Subscribe');

  await step('published two order events, one under the filter', async () => {
    for (const [total, id] of [[4185, 'HRB-2026-04802'], [1875, 'HRB-2026-04803']] as [number, string][]) {
      await sns.send(new PublishCommand({
        TopicArn: topic!.TopicArn!,
        Subject: `Order ${id} placed`,
        Message: JSON.stringify({ orderId: id, totalPence: total, at: new Date().toISOString() }),
        MessageAttributes: {
          eventType: { DataType: 'String', StringValue: 'order.placed' },
          totalPence: { DataType: 'Number', StringValue: String(total) },
        },
      }));
    }
  }, 'sns:Publish ×2');
  note('only the £41.85 one reaches the queue; £18.75 is filtered out at the topic');

  look('/_console/sns', 'subscriptions, the filter-policy builder, and a live publish');

  // --- EventBridge --------------------------------------------------------
  section('EventBridge', 'the domain bus, its rules and an archive to replay from');

  await step(`created the ${BUS} bus`, () =>
    events.send(new CreateEventBusCommand({ Name: BUS })), 'events:CreateEventBus');

  const rules: [string, string, object, string][] = [
    ['harbour-order-placed', 'Every accepted order, to the fulfilment workflow',
      { source: ['harbour.checkout'], 'detail-type': ['order.placed'] }, QUEUE_ORDERS],
    ['harbour-order-rejected', 'Rejections, to the ops queue for a human to read',
      { source: ['harbour.checkout'], 'detail-type': ['order.rejected'] }, QUEUE_DLQ],
    ['harbour-high-value', 'Anything over £250 gets a second pair of eyes',
      { source: ['harbour.checkout'], detail: { totalPence: [{ numeric: ['>', 25000] }] } }, QUEUE_ORDERS],
    ['harbour-delivery-failed', 'A failed drop, so the depot can re-plan',
      { source: ['harbour.delivery'], 'detail-type': ['delivery.failed'] }, QUEUE_DLQ],
  ];
  for (const [name, description, pattern, target] of rules) {
    await step(`rule ${name}`, async () => {
      await events.send(new PutRuleCommand({
        Name: name, EventBusName: BUS, Description: description,
        EventPattern: JSON.stringify(pattern), State: 'ENABLED',
      }));
      await events.send(new PutTargetsCommand({
        Rule: name, EventBusName: BUS,
        Targets: [{ Id: `${name}-to-queue`, Arn: arn('sqs', target) }],
      }));
    }, 'events:PutRule + PutTargets');
  }

  // TagResource on this service takes rule ARNs, not bus ARNs — the same as AWS.
  await step('tagged the order-placed rule', () =>
    events.send(new EbTag({
      ResourceARN: arn('events', `rule/${BUS}/harbour-order-placed`),
      Tags: [{ Key: 'team', Value: 'orders' }],
    })), 'events:TagResource');

  await step('a nightly stock reconciliation, on a schedule', async () => {
    await events.send(new PutRuleCommand({
      Name: 'harbour-nightly-reconcile', EventBusName: BUS,
      Description: 'Kicks off the depot stock reconciliation',
      ScheduleExpression: 'cron(0 2 * * ? *)', State: 'ENABLED',
    }));
    await events.send(new PutTargetsCommand({
      Rule: 'harbour-nightly-reconcile', EventBusName: BUS,
      Targets: [{ Id: 'reconcile-to-queue', Arn: arn('sqs', QUEUE_ORDERS) }],
    }));
  }, 'events:PutRule (schedule)');
  note('a schedule rule is the one thing that makes the bus tick on its own');

  await step('archived everything the bus sees', () =>
    events.send(new CreateArchiveCommand({
      ArchiveName: 'harbour-events-30d',
      EventSourceArn: arn('events', `event-bus/${BUS}`),
      Description: 'Thirty days of the domain bus, for replay during an incident',
      RetentionDays: 30,
    })), 'events:CreateArchive');

  await step('published four domain events', () =>
    events.send(new PutEventsCommand({
      Entries: [
        { EventBusName: BUS, Source: 'harbour.checkout', DetailType: 'order.placed',
          Detail: JSON.stringify({ orderId: 'HRB-2026-04801', totalPence: 2340, customerId: 'CUS-10482' }) },
        { EventBusName: BUS, Source: 'harbour.checkout', DetailType: 'order.placed',
          Detail: JSON.stringify({ orderId: 'HRB-2026-04804', totalPence: 31200, customerId: 'CUS-10801' }) },
        { EventBusName: BUS, Source: 'harbour.checkout', DetailType: 'order.rejected',
          Detail: JSON.stringify({ orderId: 'HRB-2026-04806', reason: 'outside the delivery area', postcode: 'CF10 1EP' }) },
        { EventBusName: BUS, Source: 'harbour.delivery', DetailType: 'delivery.failed',
          Detail: JSON.stringify({ orderId: 'HRB-2026-04799', van: 'EDI-VAN-03', reason: 'nobody home, no safe place' }) },
      ],
    })), 'events:PutEvents');
  note('the £312 order matches two rules at once — placed, and high value');

  look('/_console/eb', 'rules with live match verdicts, and the archive tab');

  // --- Kinesis ------------------------------------------------------------
  section('Kinesis', 'the storefront clickstream');

  await step(`created ${STREAM}`, () =>
    kinesis.send(new CreateStreamCommand({ StreamName: STREAM, ShardCount: 2 })), 'kinesis:CreateStream');

  await step('kept a week of it', () =>
    kinesis.send(new IncreaseStreamRetentionPeriodCommand({
      StreamName: STREAM, RetentionPeriodHours: 168,
    })), 'kinesis:IncreaseStreamRetentionPeriod');

  await step('tagged the stream', () =>
    kinesis.send(new AddTagsToStreamCommand({
      StreamName: STREAM, Tags: { team: 'growth', env: 'prod' },
    })), 'kinesis:AddTagsToStream');

  await step('registered the analytics fan-out consumer', () =>
    kinesis.send(new RegisterStreamConsumerCommand({
      StreamARN: arn('kinesis', `stream/${STREAM}`), ConsumerName: 'harbour-clickstream-warehouse',
    })), 'kinesis:RegisterStreamConsumer');

  await step('wrote a browsing session', async () => {
    const session = 'sess-8f31c0a4';
    const records = [
      { event: 'page_view', path: '/', customerId: 'CUS-10517' },
      { event: 'search', term: 'sourdough', results: 4, customerId: 'CUS-10517' },
      { event: 'product_view', sku: 'BAK-SRD-800', customerId: 'CUS-10517' },
      { event: 'add_to_basket', sku: 'BAK-SRD-800', quantity: 1, customerId: 'CUS-10517' },
      { event: 'product_view', sku: 'AMB-COF-227', customerId: 'CUS-10517' },
      { event: 'add_to_basket', sku: 'AMB-COF-227', quantity: 2, customerId: 'CUS-10517' },
      { event: 'checkout_start', basketPence: 4185, customerId: 'CUS-10517' },
    ];
    await kinesis.send(new PutRecordsCommand({
      StreamName: STREAM,
      Records: records.map((r) => ({
        Data: new TextEncoder().encode(JSON.stringify({ ...r, session, at: new Date().toISOString() })),
        PartitionKey: session,
      })),
    }));
  }, 'kinesis:PutRecords');

  await step('wrote one abandoned session on the other shard', () =>
    kinesis.send(new PutRecordCommand({
      StreamName: STREAM,
      PartitionKey: 'sess-2b90de77',
      Data: new TextEncoder().encode(JSON.stringify({
        event: 'basket_abandoned', session: 'sess-2b90de77', customerId: 'CUS-10744',
        basketPence: 1290, secondsOnPage: 812, at: new Date().toISOString(),
      })),
    })), 'kinesis:PutRecord');

  look('/_console/kinesis', 'shards, records and the registered consumer');
}
