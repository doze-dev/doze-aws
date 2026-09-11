// Lambda and Step Functions.
//
// The order-fulfilment workflow runs four functions in four languages, which is
// not a contrivance: each step is the kind of work its language actually gets
// picked for. Validation in Node next to the storefront, money in Go with
// integer pence, forecasting in Python, customer-facing prose in Ruby.
//
// They run as real supervised processes speaking the Lambda Runtime API — no
// Docker, no image pull — so what you see in the console is a process that
// really started, really logged, and really returned.

import path from 'node:path';
import { lambda, sfn, s3, arn, LAMBDA_ROLE, REGION } from '../lib/aws';
import { section, step, note, look, watch, warn } from '../lib/say';
import {
  CreateFunctionCommand, InvokeCommand, PublishVersionCommand, CreateAliasCommand,
  PublishLayerVersionCommand, TagResourceCommand as LambdaTag, CreateEventSourceMappingCommand,
  PutFunctionConcurrencyCommand, UpdateFunctionConfigurationCommand,
} from '@aws-sdk/client-lambda';
import {
  CreateStateMachineCommand, StartExecutionCommand, DescribeExecutionCommand,
  CreateActivityCommand, TagResourceCommand as SfnTag, PublishStateMachineVersionCommand,
  CreateStateMachineAliasCommand,
} from '@aws-sdk/client-sfn';
import { PutObjectCommand } from '@aws-sdk/client-s3';
import { buildOrder, ORDER_BASKETS, depotStock, receiptText } from '../lib/data';
import { QUEUE_ORDERS } from './messaging';

const fnDir = (name: string) => path.resolve(import.meta.dir, '..', 'functions', name);

/**
 * Whatever proto needs to resolve an interpreter, handed to the function.
 *
 * A Lambda child inherits doze-aws's environment, not this script's, so on a
 * machine where `node` or `ruby` is a version-manager shim the child cannot
 * resolve a version and exits before the runtime API is reached. Passing the
 * PROTO_* variables through is harmless where they are absent.
 */
const shimEnv = Object.fromEntries(
  Object.entries(process.env).filter(([k]) => k.startsWith('PROTO_')).map(([k, v]) => [k, String(v)])
);

export const PIPELINE = [
  { name: 'harbour-order-validator', runtime: 'nodejs22.x', handler: 'index.handler',
    dir: 'order-validator', lang: 'Node.js', why: 'lives next to the storefront, shares its validation rules' },
  { name: 'harbour-price-calculator', runtime: 'provided.al2023', handler: 'bootstrap',
    dir: 'price-calculator', lang: 'Go', why: 'money in integer pence, no float near a total' },
  { name: 'harbour-stock-forecaster', runtime: 'python3.12', handler: 'handler.handler',
    dir: 'stock-forecaster', lang: 'Python', why: 'the forecasting maths the data team owns' },
  { name: 'harbour-dispatch-notifier', runtime: 'ruby3.3', handler: 'handler.handler',
    dir: 'dispatch-notifier', lang: 'Ruby', why: 'the customer-facing prose, where it has always lived' },
];

export async function compute() {
  section('Lambda', 'four functions, four languages, all running as real processes');

  for (const fn of PIPELINE) {
    await step(`${fn.name} — ${fn.lang}`, () =>
      lambda.send(new CreateFunctionCommand({
        FunctionName: fn.name,
        Runtime: fn.runtime as any,
        Handler: fn.handler,
        Role: LAMBDA_ROLE,
        Description: `${fn.lang}: ${fn.why}`,
        Timeout: 30,
        MemorySize: 512,
        Code: { S3Bucket: '_local_', S3Key: fnDir(fn.dir) },
        Environment: { Variables: { HARBOUR_ENV: 'prod', ...shimEnv } },
        Tags: { team: 'orders', env: 'prod', language: fn.lang },
      })), 'lambda:CreateFunction');
    note(fn.why);
  }

  await step('published a shared layer of Harbour helpers', () =>
    lambda.send(new PublishLayerVersionCommand({
      LayerName: 'harbour-common',
      Description: 'Postcode tables and VAT rates shared by the pipeline',
      CompatibleRuntimes: ['nodejs22.x', 'python3.12'],
      Content: { S3Bucket: '_local_', S3Key: fnDir('order-validator') },
    })), 'lambda:PublishLayerVersion');

  await step('pinned the validator to 5 concurrent executions', () =>
    lambda.send(new PutFunctionConcurrencyCommand({
      FunctionName: 'harbour-order-validator', ReservedConcurrentExecutions: 5,
    })), 'lambda:PutFunctionConcurrency');

  const version = await step('published version 1 of the validator', () =>
    lambda.send(new PublishVersionCommand({
      FunctionName: 'harbour-order-validator', Description: 'First cut of the postcode rules',
    })), 'lambda:PublishVersion');

  if (version?.Version) {
    await step(`aliased version ${version.Version} as "live"`, () =>
      lambda.send(new CreateAliasCommand({
        FunctionName: 'harbour-order-validator', Name: 'live',
        FunctionVersion: version.Version!, Description: 'What the storefront calls',
      })), 'lambda:CreateAlias');
  }

  await step('pointed the checkout queue at the validator', () =>
    lambda.send(new CreateEventSourceMappingCommand({
      FunctionName: 'harbour-order-validator',
      EventSourceArn: arn('sqs', QUEUE_ORDERS),
      BatchSize: 5,
      Enabled: true,
    })), 'lambda:CreateEventSourceMapping');
  note('queued orders now drain into the function on their own');

  // A real synchronous invoke of each, so every function has logs and a
  // measured duration before anyone opens the console.
  const order = buildOrder(0, ORDER_BASKETS[0]);
  const withStock = { ...order, depotStock: depotStock() };

  let piped: any = withStock;
  for (const fn of PIPELINE) {
    const out = await step(`invoked ${fn.name}`, async () => {
      const res = await lambda.send(new InvokeCommand({
        FunctionName: fn.name, Payload: JSON.stringify(piped), LogType: 'Tail',
      }));
      const body = JSON.parse(new TextDecoder().decode(res.Payload));
      if (res.FunctionError) throw new Error(`${res.FunctionError}: ${JSON.stringify(body).slice(0, 160)}`);
      return body;
    }, 'lambda:Invoke');
    if (out) piped = out;
  }

  if (piped?.pricing) {
    note(`the chain priced ${piped.orderId} at £${piped.pricing.totalGbp}`);
    await step('wrote the rendered receipt to S3', () =>
      s3.send(new PutObjectCommand({
        Bucket: 'harbour-receipts',
        Key: `${piped.orderId}.txt`,
        Body: receiptText(piped),
        ContentType: 'text/plain',
        Metadata: { customer: piped.customer?.id ?? '', total: String(piped.pricing.totalPence) },
      })), 's3:PutObject');
  }

  await step('invoked the validator with an order it should refuse', async () => {
    const bad = buildOrder(5, [['BAK-SRD-800', 1]]); // CUS-10999, a Cardiff postcode
    const res = await lambda.send(new InvokeCommand({
      FunctionName: 'harbour-order-validator', Payload: JSON.stringify(bad), LogType: 'Tail',
    }));
    const body = JSON.parse(new TextDecoder().decode(res.Payload));
    if (body?.validation?.accepted !== false) throw new Error('expected a rejection');
    return body;
  }, 'lambda:Invoke');
  note('rejected, with a reason — so the logs have a warning in them and not only happy paths');

  look('/_console/lambda', 'four runtimes, versions, the alias and the queue trigger');

  // --- Step Functions -----------------------------------------------------
  section('Step Functions', 'the workflow that runs all four, in order');

  const definition = {
    Comment: 'Harbour order fulfilment: validate, price, pick, notify.',
    StartAt: 'Validate',
    States: {
      Validate: {
        Type: 'Task',
        Resource: arn('lambda', 'function:harbour-order-validator'),
        ResultPath: '$',
        Retry: [{ ErrorEquals: ['Lambda.ServiceException', 'States.TaskFailed'], IntervalSeconds: 2, MaxAttempts: 3, BackoffRate: 2 }],
        Next: 'Accepted?',
      },
      'Accepted?': {
        Type: 'Choice',
        Choices: [{ Variable: '$.validation.accepted', BooleanEquals: true, Next: 'Price' }],
        Default: 'Rejected',
      },
      Rejected: {
        Type: 'Pass',
        Parameters: { 'orderId.$': '$.orderId', outcome: 'rejected', 'reasons.$': '$.validation.reasons' },
        End: true,
      },
      Price: {
        Type: 'Task',
        Resource: arn('lambda', 'function:harbour-price-calculator'),
        ResultPath: '$',
        Next: 'Reserve stock',
      },
      'Reserve stock': {
        Type: 'Task',
        Resource: arn('lambda', 'function:harbour-stock-forecaster'),
        ResultPath: '$',
        Next: 'Complete?',
      },
      'Complete?': {
        Type: 'Choice',
        Choices: [{ Variable: '$.fulfilment.complete', BooleanEquals: false, Next: 'Wait for restock' }],
        Default: 'Notify',
      },
      'Wait for restock': {
        Type: 'Wait',
        Seconds: 3,
        Comment: 'A short hold so the depot can pick a substitute',
        Next: 'Notify',
      },
      Notify: {
        Type: 'Task',
        Resource: arn('lambda', 'function:harbour-dispatch-notifier'),
        ResultPath: '$',
        End: true,
      },
    },
  };

  const machine = await step('created the order-fulfilment workflow', () =>
    sfn.send(new CreateStateMachineCommand({
      name: 'harbour-order-fulfilment',
      definition: JSON.stringify(definition, null, 2),
      roleArn: arn('iam', 'role/harbour-states-exec').replace(`:${REGION}:`, '::'),
      type: 'STANDARD',
      tags: [{ key: 'team', value: 'orders' }, { key: 'env', value: 'prod' }],
    })), 'states:CreateStateMachine');

  const machineArn = machine?.stateMachineArn ?? arn('states', 'stateMachine:harbour-order-fulfilment');

  await step('created the manual-review activity', () =>
    sfn.send(new CreateActivityCommand({ name: 'harbour-manual-review' })), 'states:CreateActivity');
  note('a human task: something a worker polls for and answers');

  const ver = await step('published version 1 of the workflow', () =>
    sfn.send(new PublishStateMachineVersionCommand({
      stateMachineArn: machineArn, description: 'Adds the restock wait',
    })), 'states:PublishStateMachineVersion');

  if (ver?.stateMachineVersionArn) {
    await step('aliased it as "live"', () =>
      sfn.send(new CreateStateMachineAliasCommand({
        name: 'live',
        routingConfiguration: [{ stateMachineVersionArn: ver.stateMachineVersionArn!, weight: 100 }],
      })), 'states:CreateStateMachineAlias');
  }

  // Three executions: one clean, one that takes the restock branch, one rejected.
  const runs: [string, any][] = [
    ['a straightforward order', { ...buildOrder(1, ORDER_BASKETS[1]), depotStock: depotStock() }],
    ['an order that runs short and waits', { ...buildOrder(2, ORDER_BASKETS[4]), depotStock: depotStock() }],
    ['an order from outside the delivery area', { ...buildOrder(5, ORDER_BASKETS[0]), depotStock: depotStock() }],
  ];

  const started: string[] = [];
  for (const [why, input] of runs) {
    const exec = await step(`started an execution — ${why}`, () =>
      sfn.send(new StartExecutionCommand({
        stateMachineArn: machineArn,
        name: `${input.orderId}-${Date.now().toString(36)}`,
        input: JSON.stringify(input),
      })), 'states:StartExecution');
    if (exec?.executionArn) started.push(exec.executionArn);
  }

  if (started.length) {
    note('one of these sits in a Wait state for three seconds — open it now to watch it');
    await watch(4500);
    for (const execArn of started) {
      await step('read back the execution', async () => {
        const d = await sfn.send(new DescribeExecutionCommand({ executionArn: execArn }));
        return d.status;
      }, 'states:DescribeExecution');
    }
  }

  look('/_console/sfn', 'the graph, the history, and an execution that waited');
}
