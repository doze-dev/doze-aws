// CloudWatch and CloudWatch Logs: the metrics a shop actually watches, the
// alarms that page someone, and the log groups the pipeline writes into.
//
// Metrics are backdated over the last three hours so the console has a curve to
// draw rather than a single point. The numbers follow a working day — quiet at
// six, a lunch peak, a bigger evening one.

import { cw, logs, arn } from '../lib/aws';
import { section, step, note, look, watch } from '../lib/say';
import {
  PutMetricDataCommand, PutMetricAlarmCommand, SetAlarmStateCommand, TagResourceCommand,
  PutDashboardCommand,
} from '@aws-sdk/client-cloudwatch';
import {
  CreateLogGroupCommand, CreateLogStreamCommand, PutLogEventsCommand, PutRetentionPolicyCommand,
  PutMetricFilterCommand, TagLogGroupCommand,
} from '@aws-sdk/client-cloudwatch-logs';
import { TOPIC_OPS } from './messaging';

const NS = 'Harbour/Orders';

/** Orders per five minutes across a working day — a curve, not a flat line. */
function ordersAt(minutesAgo: number): number {
  const hour = (new Date(Date.now() - minutesAgo * 60_000).getUTCHours() + 24) % 24;
  const shape = [2, 1, 1, 1, 1, 2, 5, 9, 14, 17, 19, 24, 31, 28, 22, 20, 23, 29, 36, 33, 24, 14, 8, 4];
  const base = shape[hour];
  return Math.max(0, base + ((minutesAgo * 7) % 5) - 2);
}

export async function observability() {
  section('CloudWatch Logs', 'the pipeline’s log groups, retention and a metric filter');

  const groups: [string, number, string][] = [
    ['/aws/lambda/harbour-order-validator', 14, 'Node — validation decisions'],
    ['/aws/lambda/harbour-price-calculator', 14, 'Go — pricing'],
    ['/aws/lambda/harbour-stock-forecaster', 14, 'Python — picking and forecasts'],
    ['/aws/lambda/harbour-dispatch-notifier', 14, 'Ruby — customer notifications'],
    ['/harbour/checkout', 30, 'The storefront’s own application log'],
    ['/harbour/depot/edinburgh', 7, 'Handheld scanner traffic'],
  ];

  for (const [name, days, why] of groups) {
    await step(`created ${name}`, async () => {
      await logs.send(new CreateLogGroupCommand({ logGroupName: name }));
      await logs.send(new PutRetentionPolicyCommand({ logGroupName: name, retentionInDays: days }));
    }, 'logs:CreateLogGroup + PutRetentionPolicy');
    note(`${why} · kept ${days} days`);
  }

  await step('tagged the checkout log group', () =>
    logs.send(new TagLogGroupCommand({
      logGroupName: '/harbour/checkout', tags: { team: 'orders', env: 'prod' },
    })), 'logs:TagLogGroup');

  await step('wrote a morning of checkout logs', async () => {
    const stream = `2026/09/11/[$LATEST]${'a1b2c3d4e5f6'.repeat(2)}`;
    await logs.send(new CreateLogStreamCommand({ logGroupName: '/harbour/checkout', logStreamName: stream }));
    const now = Date.now();
    const lines = [
      'INFO  checkout starting, build 2026.9.11-4a8f1c',
      'INFO  basket BSK-CUS-10482-1 checked out as HRB-2026-04801 (£23.40)',
      'INFO  payment authorised for HRB-2026-04801, tok_visa_4242',
      'WARN  stripe latency 1840ms on HRB-2026-04802, over the 1500ms budget',
      'INFO  basket BSK-CUS-10517-2 checked out as HRB-2026-04802 (£41.85)',
      'ERROR payment declined for HRB-2026-04803: insufficient_funds',
      'INFO  HRB-2026-04803 moved to awaiting_payment, customer notified',
      'INFO  basket BSK-CUS-10623-1 checked out as HRB-2026-04804 (£312.00)',
      'WARN  HRB-2026-04804 over the manual review threshold, flagged',
      'ERROR courier webhook rejected: signature mismatch on delivery.failed',
      'INFO  nightly reconciliation queued for depot DEP-EDI',
    ];
    await logs.send(new PutLogEventsCommand({
      logGroupName: '/harbour/checkout', logStreamName: stream,
      logEvents: lines.map((message, i) => ({ timestamp: now - (lines.length - i) * 60_000, message })),
    }));
  }, 'logs:PutLogEvents');

  await step('counted payment declines with a metric filter', () =>
    logs.send(new PutMetricFilterCommand({
      logGroupName: '/harbour/checkout',
      filterName: 'payment-declined',
      filterPattern: 'ERROR payment declined',
      metricTransformations: [{
        metricName: 'PaymentDeclines', metricNamespace: NS, metricValue: '1', defaultValue: 0,
      }],
    })), 'logs:PutMetricFilter');
  note('a log line becomes a metric, and the metric can raise an alarm — no extra code in the app');

  look('/_console/logs', 'the live tail, retention, and the metric filter');

  // --- CloudWatch ---------------------------------------------------------
  section('CloudWatch', 'three hours of business metrics, and alarms over them');

  await step('published three hours of order volume', async () => {
    const data = [];
    for (let m = 180; m >= 0; m -= 5) {
      data.push({
        MetricName: 'OrdersPlaced', Unit: 'Count' as const,
        Timestamp: new Date(Date.now() - m * 60_000),
        Value: ordersAt(m),
        Dimensions: [{ Name: 'Channel', Value: 'web' }],
      });
    }
    // PutMetricData takes 20 datums a call, the same as AWS.
    for (let i = 0; i < data.length; i += 20) {
      await cw.send(new PutMetricDataCommand({ Namespace: NS, MetricData: data.slice(i, i + 20) }));
    }
  }, 'cloudwatch:PutMetricData');

  await step('published the same for the apps, and basket value', async () => {
    const data = [];
    for (let m = 180; m >= 0; m -= 5) {
      const orders = ordersAt(m);
      data.push({
        MetricName: 'OrdersPlaced', Unit: 'Count' as const,
        Timestamp: new Date(Date.now() - m * 60_000),
        Value: Math.round(orders * 0.7),
        Dimensions: [{ Name: 'Channel', Value: 'ios' }],
      });
      data.push({
        MetricName: 'BasketValue', Unit: 'None' as const,
        Timestamp: new Date(Date.now() - m * 60_000),
        // A spread, not an average: this is what percentiles are for.
        StatisticValues: {
          SampleCount: Math.max(1, orders), Sum: orders * 3400,
          Minimum: 620, Maximum: 31200,
        },
      });
    }
    for (let i = 0; i < data.length; i += 20) {
      await cw.send(new PutMetricDataCommand({ Namespace: NS, MetricData: data.slice(i, i + 20) }));
    }
  }, 'cloudwatch:PutMetricData');
  note('BasketValue goes in as statistic sets, so p99 is a real percentile and not an average');

  await step('published delivery SLA and depot pick times', async () => {
    const data = [];
    for (let m = 180; m >= 0; m -= 15) {
      for (const depot of ['DEP-EDI', 'DEP-GLA']) {
        data.push({
          MetricName: 'PickSeconds', Unit: 'Seconds' as const,
          Timestamp: new Date(Date.now() - m * 60_000),
          Value: depot === 'DEP-EDI' ? 340 + (m % 60) : 410 + (m % 90),
          Dimensions: [{ Name: 'Depot', Value: depot }],
        });
      }
    }
    for (let i = 0; i < data.length; i += 20) {
      await cw.send(new PutMetricDataCommand({ Namespace: NS, MetricData: data.slice(i, i + 20) }));
    }
  }, 'cloudwatch:PutMetricData');

  const alarms: [string, string, string, number, string, string][] = [
    ['harbour-orders-stalled', 'OrdersPlaced', 'LessThanThreshold', 1,
      'No orders in fifteen minutes — the storefront is probably down', 'breaching'],
    ['harbour-payment-declines', 'PaymentDeclines', 'GreaterThanThreshold', 5,
      'More than five declines in five minutes', 'notBreaching'],
    ['harbour-pick-time-slow', 'PickSeconds', 'GreaterThanThreshold', 600,
      'Depot picking is over ten minutes an order', 'missing'],
  ];

  for (const [name, metric, op, threshold, description, missing] of alarms) {
    await step(`alarm ${name}`, () =>
      cw.send(new PutMetricAlarmCommand({
        AlarmName: name,
        AlarmDescription: description,
        Namespace: NS,
        MetricName: metric,
        Statistic: metric === 'PickSeconds' ? 'Average' : 'Sum',
        Period: 300,
        EvaluationPeriods: metric === 'OrdersPlaced' ? 3 : 1,
        Threshold: threshold,
        ComparisonOperator: op as any,
        TreatMissingData: missing,
        AlarmActions: [arn('sns', TOPIC_OPS)],
        OKActions: [arn('sns', TOPIC_OPS)],
      })), 'cloudwatch:PutMetricAlarm');
  }

  await step('tagged the stalled-orders alarm', () =>
    cw.send(new TagResourceCommand({
      ResourceARN: arn('cloudwatch', 'alarm:harbour-orders-stalled'),
      Tags: [{ Key: 'runbook', Value: 'https://wiki.example.com/harbour/storefront-down' }],
    })), 'cloudwatch:TagResource');

  await step('drove the declines alarm into ALARM', () =>
    cw.send(new SetAlarmStateCommand({
      AlarmName: 'harbour-payment-declines', StateValue: 'ALARM',
      StateReason: 'Seeded so the console has a red alarm and an SNS notification to show',
    })), 'cloudwatch:SetAlarmState');
  note('that fires the ops topic for real — the notification is on the SNS page');

  await step('published a dashboard', () =>
    cw.send(new PutDashboardCommand({
      DashboardName: 'harbour-trading',
      DashboardBody: JSON.stringify({
        widgets: [
          { type: 'metric', width: 12, height: 6, properties: {
            title: 'Orders placed', stat: 'Sum', period: 300,
            metrics: [[NS, 'OrdersPlaced', 'Channel', 'web'], [NS, 'OrdersPlaced', 'Channel', 'ios']] } },
          { type: 'metric', width: 12, height: 6, properties: {
            title: 'Basket value p99', stat: 'p99', period: 300,
            metrics: [[NS, 'BasketValue']] } },
        ],
      }),
    })), 'cloudwatch:PutDashboard');

  look('/_console/cw', 'the curve, three alarms, and one of them red');
}
