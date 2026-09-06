import { test, expect } from '@playwright/test';
import * as sfn from '@aws-sdk/client-sfn';
import { SQSClient, CreateQueueCommand, ReceiveMessageCommand } from '@aws-sdk/client-sqs';
import { BASE_URL } from '../playwright.config';

// Step Functions through the real JavaScript SDK v3, against the same server
// the console specs drive. The Go tests already speak aws-sdk-go v1 and v2;
// this is the third SDK, and the one CDK users hold. What it pins is what the
// SDK's TYPES promise: dates are Dates, payloads are JSON strings, errors are
// the typed exception classes a program branches on, pagination yields
// nextToken. StartSyncExecution and TestState are absent on purpose: the SDK's
// endpoint ruleset prefixes `sync-` onto the host, which an IP endpoint cannot
// resolve — the ledger says so.

const endpoint = BASE_URL.replace(/\/_console\/$/, '');
const cfg = {
  endpoint,
  region: 'us-east-1',
  credentials: { accessKeyId: 'test', secretAccessKey: 'test' },
  maxAttempts: 1,
};
const c = new sfn.SFNClient(cfg);
const sqs = new SQSClient(cfg);
const ROLE = 'arn:aws:iam::000000000000:role/StepFunctions';
const uniq = (p: string) => `${p}-${Math.random().toString(16).slice(2, 8)}`;

async function settle(arn: string, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const d = await c.send(new sfn.DescribeExecutionCommand({ executionArn: arn }));
    if (d.status !== 'RUNNING' || Date.now() > deadline) return d;
    await new Promise((r) => setTimeout(r, 100));
  }
}

async function history(arn: string) {
  const out: sfn.HistoryEvent[] = [];
  let nextToken: string | undefined;
  do {
    const page = await c.send(new sfn.GetExecutionHistoryCommand({ executionArn: arn, maxResults: 5, nextToken, includeExecutionData: true }));
    out.push(...(page.events ?? []));
    nextToken = page.nextToken;
  } while (nextToken);
  return out;
}

const FLOW = JSON.stringify({
  StartAt: 'Prep',
  States: {
    Prep: { Type: 'Pass', Result: { ready: true }, ResultPath: '$.prep', Next: 'Route' },
    Route: {
      Type: 'Choice',
      Choices: [{ Variable: '$.mode', StringEquals: 'fail', Next: 'Boom' }],
      Default: 'Fan',
    },
    Fan: {
      Type: 'Parallel', ResultPath: '$.fan', Next: 'Each',
      Branches: [
        { StartAt: 'A', States: { A: { Type: 'Pass', Result: 'a', End: true } } },
        { StartAt: 'B', States: { B: { Type: 'Pass', Result: 'b', End: true } } },
      ],
    },
    Each: {
      Type: 'Map', ItemsPath: '$.items', ResultPath: '$.each', MaxConcurrency: 1, Next: 'Done',
      ItemProcessor: { StartAt: 'I', States: { I: { Type: 'Pass', End: true } } },
    },
    Done: { Type: 'Succeed' },
    Boom: { Type: 'Fail', Error: 'Custom.Failure', Cause: 'asked to' },
  },
});

test.describe('Step Functions via @aws-sdk/client-sfn', () => {
  test('machines: create is idempotent, describe carries AWS defaults, lists paginate', async () => {
    const name = uniq('sdk-machine');
    const created = await c.send(new sfn.CreateStateMachineCommand({ name, definition: FLOW, roleArn: ROLE }));
    expect(created.creationDate).toBeInstanceOf(Date);
    const arn = created.stateMachineArn!;

    const again = await c.send(new sfn.CreateStateMachineCommand({ name, definition: FLOW, roleArn: ROLE }));
    expect(again.stateMachineArn).toBe(arn);
    await expect(c.send(new sfn.CreateStateMachineCommand({ name, definition: '{"StartAt":"X","States":{"X":{"Type":"Succeed"}}}', roleArn: ROLE })))
      .rejects.toBeInstanceOf(sfn.StateMachineAlreadyExists);

    const d = await c.send(new sfn.DescribeStateMachineCommand({ stateMachineArn: arn }));
    expect(d.definition).toBe(FLOW);
    expect(d.type).toBe('STANDARD');
    expect(d.loggingConfiguration?.level).toBe('OFF');
    expect(d.tracingConfiguration?.enabled).toBe(false);
    expect(d.encryptionConfiguration?.type).toBe('AWS_OWNED_KEY');

    const upd = await c.send(new sfn.UpdateStateMachineCommand({ stateMachineArn: arn, roleArn: ROLE + '2' }));
    expect(upd.updateDate).toBeInstanceOf(Date);
    expect(upd.revisionId).not.toBe(d.revisionId);

    // Pagination: one at a time until the token runs out; every machine shows up once.
    const seen: string[] = [];
    let nextToken: string | undefined;
    do {
      const page = await c.send(new sfn.ListStateMachinesCommand({ maxResults: 1, nextToken }));
      expect(page.stateMachines?.length).toBeLessThanOrEqual(1);
      seen.push(...(page.stateMachines ?? []).map((m) => m.stateMachineArn!));
      nextToken = page.nextToken;
    } while (nextToken);
    expect(seen.filter((a) => a === arn)).toHaveLength(1);
    await expect(c.send(new sfn.ListStateMachinesCommand({ nextToken: 'garbage' }))).rejects.toMatchObject({ name: 'InvalidToken' });

    const bad = await c.send(new sfn.ValidateStateMachineDefinitionCommand({ definition: '{"StartAt":"Nope","States":{}}' }));
    expect(bad.result).toBe('FAIL');
    expect(bad.diagnostics?.[0]?.severity).toBe('ERROR');
    expect(bad.truncated).toBe(false);

    await c.send(new sfn.DeleteStateMachineCommand({ stateMachineArn: arn }));
    await c.send(new sfn.DeleteStateMachineCommand({ stateMachineArn: arn })); // idempotent
    await expect(c.send(new sfn.DescribeStateMachineCommand({ stateMachineArn: arn })))
      .rejects.toBeInstanceOf(sfn.StateMachineDoesNotExist);
  });

  test('executions: shapes, history chains, sequential fan-outs, failure, stop', async () => {
    const name = uniq('sdk-exec');
    const { stateMachineArn: arn } = await c.send(new sfn.CreateStateMachineCommand({ name, definition: FLOW, roleArn: ROLE }));

    const input = JSON.stringify({ mode: 'go', items: [1, 2, 3] });
    const started = await c.send(new sfn.StartExecutionCommand({ stateMachineArn: arn, name: 'run-1', input }));
    expect(started.startDate).toBeInstanceOf(Date);
    const same = await c.send(new sfn.StartExecutionCommand({ stateMachineArn: arn, name: 'run-1', input }));
    expect(same.executionArn).toBe(started.executionArn);
    await expect(c.send(new sfn.StartExecutionCommand({ stateMachineArn: arn, name: 'run-1', input: '{"mode":"other"}' })))
      .rejects.toBeInstanceOf(sfn.ExecutionAlreadyExists);

    const done = await settle(started.executionArn!);
    expect(done.status).toBe('SUCCEEDED');
    expect(done.stopDate).toBeInstanceOf(Date);
    expect(typeof done.input).toBe('string');
    expect(typeof done.output).toBe('string');
    expect(done.traceHeader).toBeUndefined();
    // A Map after a Parallel on the same frame: exactly the items, no leak
    // from the Parallel's settled branches.
    const out = JSON.parse(done.output!);
    expect(out.fan).toEqual(['a', 'b']);
    expect(out.each).toEqual([1, 2, 3]);

    const events = await history(started.executionArn!);
    const types = events.map((e) => e.type);
    expect(types[0]).toBe('ExecutionStarted');
    expect(types[types.length - 1]).toBe('ExecutionSucceeded');
    for (let i = 1; i < events.length; i++) expect(events[i].id!).toBeGreaterThan(events[i - 1].id!);
    for (const e of events) {
      expect(e.timestamp).toBeInstanceOf(Date);
      if (e.stateEnteredEventDetails) expect(typeof e.stateEnteredEventDetails.input).toBe('string');
    }
    // Under MaxConcurrency 1 an iteration starts only after the previous
    // one has succeeded, and its Succeeded chains on its own last event.
    const byID = new Map(events.map((e) => [e.id, e]));
    const its = events.filter((e) => e.type === 'MapIterationStarted');
    const oks = events.filter((e) => e.type === 'MapIterationSucceeded');
    expect(its).toHaveLength(3);
    expect(its[1].id!).toBeGreaterThan(oks[0].id!);
    for (const ok of oks) expect(byID.get(ok.previousEventId)?.type).toBe('PassStateExited');

    const listed = await c.send(new sfn.ListExecutionsCommand({ stateMachineArn: arn, statusFilter: 'SUCCEEDED', maxResults: 1 }));
    expect(listed.executions).toHaveLength(1);
    expect(listed.executions![0].startDate).toBeInstanceOf(Date);

    const frozen = await c.send(new sfn.DescribeStateMachineForExecutionCommand({ executionArn: started.executionArn }));
    expect(frozen.definition).toBe(FLOW);

    // The Fail branch.
    const failed = await c.send(new sfn.StartExecutionCommand({ stateMachineArn: arn, input: '{"mode":"fail"}' }));
    const f = await settle(failed.executionArn!);
    expect(f.status).toBe('FAILED');
    expect(f.error).toBe('Custom.Failure');
    expect(f.cause).toBe('asked to');
    const ftypes = (await history(failed.executionArn!)).map((e) => e.type);
    expect(ftypes).toContain('FailStateEntered');
    expect(ftypes[ftypes.length - 1]).toBe('ExecutionFailed');

    // Stop a Wait mid-flight.
    const { stateMachineArn: slow } = await c.send(new sfn.CreateStateMachineCommand({
      name: uniq('sdk-slow'), roleArn: ROLE,
      definition: '{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":300,"End":true}}}',
    }));
    const waiting = await c.send(new sfn.StartExecutionCommand({ stateMachineArn: slow }));
    const stopped = await c.send(new sfn.StopExecutionCommand({ executionArn: waiting.executionArn, error: 'Operator', cause: 'enough' }));
    expect(stopped.stopDate).toBeInstanceOf(Date);
    const s = await settle(waiting.executionArn!);
    expect(s.status).toBe('ABORTED');
    expect(s.error).toBe('Operator');
  });

  test('task tokens: SQS callback, heartbeat, failure by name, spent token', async () => {
    const q = await sqs.send(new CreateQueueCommand({ QueueName: uniq('sdk-tokens') }));
    const def = JSON.stringify({
      StartAt: 'Ask',
      States: {
        Ask: {
          Type: 'Task', Resource: 'arn:aws:states:::sqs:sendMessage.waitForTaskToken',
          Parameters: { QueueUrl: q.QueueUrl, MessageBody: { 'token.$': '$$.Task.Token' } },
          ResultPath: '$.answer', End: true,
        },
      },
    });
    const { stateMachineArn: arn } = await c.send(new sfn.CreateStateMachineCommand({ name: uniq('sdk-token'), definition: def, roleArn: ROLE }));

    const token = async () => {
      for (let i = 0; i < 50; i++) {
        const r = await sqs.send(new ReceiveMessageCommand({ QueueUrl: q.QueueUrl, WaitTimeSeconds: 1 }));
        const body = r.Messages?.[0]?.Body;
        if (body) return JSON.parse(body).token as string;
      }
      throw new Error('no token message arrived');
    };

    const ok = await c.send(new sfn.StartExecutionCommand({ stateMachineArn: arn, input: '{}' }));
    const t1 = await token();
    await c.send(new sfn.SendTaskHeartbeatCommand({ taskToken: t1 }));
    await c.send(new sfn.SendTaskSuccessCommand({ taskToken: t1, output: '{"approved":true}' }));
    const d1 = await settle(ok.executionArn!);
    expect(d1.status).toBe('SUCCEEDED');
    expect(JSON.parse(d1.output!).answer).toEqual({ approved: true });
    await expect(c.send(new sfn.SendTaskSuccessCommand({ taskToken: t1, output: '{}' })))
      .rejects.toBeInstanceOf(sfn.TaskDoesNotExist);

    const no = await c.send(new sfn.StartExecutionCommand({ stateMachineArn: arn, input: '{}' }));
    const t2 = await token();
    await c.send(new sfn.SendTaskFailureCommand({ taskToken: t2, error: 'Human.Rejected', cause: 'nope' }));
    const d2 = await settle(no.executionArn!);
    expect(d2.status).toBe('FAILED');
    expect(d2.error).toBe('Human.Rejected');
  });

  test('errors and staged operations are typed the way the SDK expects', async () => {
    const ghost = 'arn:aws:states:us-east-1:000000000000:stateMachine:ghost';
    await expect(c.send(new sfn.DescribeStateMachineCommand({ stateMachineArn: ghost }))).rejects.toBeInstanceOf(sfn.StateMachineDoesNotExist);
    await expect(c.send(new sfn.DescribeStateMachineCommand({ stateMachineArn: 'nope' }))).rejects.toBeInstanceOf(sfn.InvalidArn);
    await expect(c.send(new sfn.ListTagsForResourceCommand({ resourceArn: ghost }))).rejects.toBeInstanceOf(sfn.ResourceNotFound);
    await expect(c.send(new sfn.CreateStateMachineCommand({ name: 'has space', definition: FLOW, roleArn: ROLE }))).rejects.toBeInstanceOf(sfn.InvalidName);
    await expect(c.send(new sfn.CreateStateMachineCommand({ name: uniq('bad'), definition: '{"StartAt":"X"}', roleArn: ROLE }))).rejects.toBeInstanceOf(sfn.InvalidDefinition);
    await expect(c.send(new sfn.DescribeExecutionCommand({ executionArn: 'arn:aws:states:us-east-1:000000000000:execution:ghost:run' }))).rejects.toBeInstanceOf(sfn.ExecutionDoesNotExist);
    await expect(c.send(new sfn.SendTaskSuccessCommand({ taskToken: 'never', output: '{}' }))).rejects.toBeInstanceOf(sfn.TaskDoesNotExist);

    // Staged and refused operations answer a 400 the SDK surfaces as a
    // service exception, with a message that names why.
    for (const cmd of [
      new sfn.GetActivityTaskCommand({ activityArn: 'arn:aws:states:us-east-1:000000000000:activity:x' }),
      new sfn.RedriveExecutionCommand({ executionArn: 'arn:aws:states:us-east-1:000000000000:execution:m:e' }),
      new sfn.ListStateMachineAliasesCommand({ stateMachineArn: ghost }),
      new sfn.ListMapRunsCommand({ executionArn: 'arn:aws:states:us-east-1:000000000000:execution:m:e' }),
    ]) {
      const err = await c.send(cmd).catch((e) => e);
      expect(err.name).toBe('UnsupportedOperationException');
      expect(err.$metadata?.httpStatusCode).toBe(400);
      expect(err.message).toMatch(/not supported by doze-aws/);
    }
  });
});
