import { test, expect } from '@playwright/test';
import * as sfn from '@aws-sdk/client-sfn';
import { SQSClient, CreateQueueCommand, ReceiveMessageCommand } from '@aws-sdk/client-sqs';
import { BASE_URL } from '../playwright.config';

// Step Functions through the real JavaScript SDK v3, against the same server
// the console specs drive. The Go tests already speak aws-sdk-go v1 and v2;
// this is the third SDK, and the one CDK users hold. What it pins is what the
// SDK's TYPES promise: dates are Dates, payloads are JSON strings, errors are
// the typed exception classes a program branches on, pagination yields
// nextToken. StartSyncExecution and TestState go through a second client with
// disableHostPrefix: the SDK's endpoint ruleset prefixes `sync-` onto the
// host, which the sync-aws.doze name serves but an IP endpoint cannot.

const endpoint = BASE_URL.replace(/\/_console\/$/, '');
const cfg = {
  endpoint,
  region: 'us-east-1',
  credentials: { accessKeyId: 'test', secretAccessKey: 'test' },
  maxAttempts: 1,
};
const c = new sfn.SFNClient(cfg);
const sync = new sfn.SFNClient({ ...cfg, disableHostPrefix: true });
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

  test('versions and aliases: publish is idempotent, an alias routes by weight, deletes are guarded', async () => {
    const name = uniq('sdk-ver');
    const { stateMachineArn: arn } = await c.send(new sfn.CreateStateMachineCommand({ name, definition: FLOW, roleArn: ROLE }));
    const v1 = await c.send(new sfn.PublishStateMachineVersionCommand({ stateMachineArn: arn, description: 'first' }));
    expect(v1.stateMachineVersionArn).toBe(`${arn}:1`);
    expect(v1.creationDate).toBeInstanceOf(Date);
    // The same revision publishes to the same version.
    const again = await c.send(new sfn.PublishStateMachineVersionCommand({ stateMachineArn: arn }));
    expect(again.stateMachineVersionArn).toBe(v1.stateMachineVersionArn);
    // A changed definition, published in the same call, is version 2.
    const changed = FLOW.replace('"ready":true', '"ready":false');
    const upd = await c.send(new sfn.UpdateStateMachineCommand({ stateMachineArn: arn, definition: changed, publish: true }));
    expect(upd.stateMachineVersionArn).toBe(`${arn}:2`);
    const versions = await c.send(new sfn.ListStateMachineVersionsCommand({ stateMachineArn: arn }));
    expect(versions.stateMachineVersions!.map((v) => v.stateMachineVersionArn)).toEqual([`${arn}:2`, `${arn}:1`]);

    const alias = await c.send(new sfn.CreateStateMachineAliasCommand({
      name: 'live', routingConfiguration: [{ stateMachineVersionArn: `${arn}:1`, weight: 100 }],
    }));
    expect(alias.stateMachineAliasArn).toBe(`${arn}:live`);
    // The version an alias routes to cannot be deleted.
    await expect(c.send(new sfn.DeleteStateMachineVersionCommand({ stateMachineVersionArn: `${arn}:1` }))).rejects.toBeInstanceOf(sfn.ConflictException);
    // Weights must sum to 100.
    await expect(c.send(new sfn.UpdateStateMachineAliasCommand({
      stateMachineAliasArn: alias.stateMachineAliasArn,
      routingConfiguration: [{ stateMachineVersionArn: `${arn}:1`, weight: 60 }, { stateMachineVersionArn: `${arn}:2`, weight: 30 }],
    }))).rejects.toBeInstanceOf(sfn.ValidationException);

    // An execution on the alias records the version it ran and the alias.
    const run = await c.send(new sfn.StartExecutionCommand({ stateMachineArn: alias.stateMachineAliasArn, input: '{"mode":"go","items":[1]}' }));
    const d = await settle(run.executionArn!);
    expect(d.status).toBe('SUCCEEDED');
    expect(d.stateMachineAliasArn).toBe(alias.stateMachineAliasArn);
    expect(d.stateMachineVersionArn).toBe(`${arn}:1`);
    // Version 1 has the original definition, whatever the machine says now.
    const frozen = await c.send(new sfn.DescribeStateMachineForExecutionCommand({ executionArn: run.executionArn }));
    expect(frozen.definition).toContain('"ready":true');

    const aliases = await c.send(new sfn.ListStateMachineAliasesCommand({ stateMachineArn: arn }));
    expect(aliases.stateMachineAliases!.map((a) => a.stateMachineAliasArn)).toEqual([alias.stateMachineAliasArn]);
    await c.send(new sfn.DeleteStateMachineAliasCommand({ stateMachineAliasArn: alias.stateMachineAliasArn }));
    await c.send(new sfn.DeleteStateMachineVersionCommand({ stateMachineVersionArn: `${arn}:1` }));
    await expect(c.send(new sfn.DescribeStateMachineAliasCommand({ stateMachineAliasArn: alias.stateMachineAliasArn }))).rejects.toBeInstanceOf(sfn.ResourceNotFound);
  });

  test('express: StartSyncExecution and TestState answer inside the call', async () => {
    const name = uniq('sdk-express');
    const { stateMachineArn: arn } = await c.send(new sfn.CreateStateMachineCommand({ name, definition: FLOW, roleArn: ROLE, type: 'EXPRESS' }));
    // StartExecution on an EXPRESS machine is fire-and-forget: it answers an
    // :express: ARN that nothing can describe afterwards, as on AWS.
    const async = await c.send(new sfn.StartExecutionCommand({ stateMachineArn: arn, input: '{"mode":"go","items":[]}' }));
    expect(async.executionArn).toContain(':express:');
    await expect(c.send(new sfn.DescribeExecutionCommand({ executionArn: async.executionArn }))).rejects.toBeInstanceOf(sfn.ExecutionDoesNotExist);

    const ok = await sync.send(new sfn.StartSyncExecutionCommand({ stateMachineArn: arn, input: '{"mode":"go","items":[1,2]}' }));
    expect(ok.status).toBe('SUCCEEDED');
    expect(ok.startDate).toBeInstanceOf(Date);
    expect(ok.stopDate).toBeInstanceOf(Date);
    expect(ok.executionArn).toContain(':express:');
    expect(JSON.parse(ok.output!).each).toEqual([1, 2]);
    expect(ok.billingDetails?.billedDurationInMilliseconds).toBeGreaterThanOrEqual(100);
    expect(ok.billingDetails?.billedMemoryUsedInMB).toBe(64);

    const failed = await sync.send(new sfn.StartSyncExecutionCommand({ stateMachineArn: arn, input: '{"mode":"fail"}' }));
    expect(failed.status).toBe('FAILED');
    expect(failed.error).toBe('Custom.Failure');
    expect(failed.cause).toBe('asked to');
    // Express executions are not listable after the fact, as on AWS.
    await expect(c.send(new sfn.ListExecutionsCommand({ stateMachineArn: arn }))).rejects.toBeInstanceOf(sfn.StateMachineTypeNotSupported);

    // TestState runs one state in isolation, with inspection data.
    const tested = await sync.send(new sfn.TestStateCommand({
      definition: JSON.stringify({ Type: 'Pass', Parameters: { 'who.$': '$.name', at: 'test' }, End: true }),
      input: '{"name":"ada"}',
      inspectionLevel: 'DEBUG',
    }));
    expect(tested.status).toBe('SUCCEEDED');
    expect(JSON.parse(tested.output!)).toEqual({ who: 'ada', at: 'test' });
    expect(tested.inspectionData?.afterInputPath).toBe('{"name":"ada"}');
    expect(JSON.parse(tested.inspectionData!.afterParameters!)).toEqual({ who: 'ada', at: 'test' });
    // A mocked Task answers the mock without calling anything.
    const mocked = await sync.send(new sfn.TestStateCommand({
      definition: JSON.stringify({ Type: 'Task', Resource: 'arn:aws:states:::lambda:invoke', Parameters: { FunctionName: 'absent' }, End: true }),
      input: '{}',
      mock: { result: '{"Payload":{"answer":42}}' },
    }));
    expect(mocked.status).toBe('SUCCEEDED');
    expect(JSON.parse(mocked.output!).Payload.answer).toBe(42);
    const caught = await sync.send(new sfn.TestStateCommand({
      definition: JSON.stringify({ Type: 'Task', Resource: 'arn:aws:states:::lambda:invoke', Parameters: { FunctionName: 'absent' }, Catch: [{ ErrorEquals: ['States.ALL'], Next: 'Recover' }], End: true }),
      input: '{}',
      mock: { errorOutput: { error: 'Lambda.Unknown', cause: 'mocked' } },
    }));
    expect(caught.status).toBe('CAUGHT_ERROR');
    expect(caught.nextState).toBe('Recover');
    expect(caught.error).toBe('Lambda.Unknown');
  });

  test('activities: a worker polls, works and answers', async () => {
    const act = await c.send(new sfn.CreateActivityCommand({ name: uniq('sdk-act') }));
    const definition = JSON.stringify({
      StartAt: 'Work',
      States: { Work: { Type: 'Task', Resource: act.activityArn, HeartbeatSeconds: 60, End: true } },
    });
    const name = uniq('sdk-actm');
    const { stateMachineArn: arn } = await c.send(new sfn.CreateStateMachineCommand({ name, definition, roleArn: ROLE }));
    const run = await c.send(new sfn.StartExecutionCommand({ stateMachineArn: arn, input: '{"job":7}' }));

    const task = await c.send(new sfn.GetActivityTaskCommand({ activityArn: act.activityArn, workerName: 'w1' }));
    expect(task.taskToken).toBeTruthy();
    expect(JSON.parse(task.input!)).toEqual({ job: 7 });
    await c.send(new sfn.SendTaskHeartbeatCommand({ taskToken: task.taskToken }));
    await c.send(new sfn.SendTaskSuccessCommand({ taskToken: task.taskToken, output: '{"job":7,"done":true}' }));
    const d = await settle(run.executionArn!);
    expect(d.status).toBe('SUCCEEDED');
    expect(JSON.parse(d.output!)).toEqual({ job: 7, done: true });
    const kinds = (await history(run.executionArn!)).map((e) => e.type);
    expect(kinds).toEqual(expect.arrayContaining(['ActivityScheduled', 'ActivityStarted', 'ActivitySucceeded']));
    const started = (await history(run.executionArn!)).find((e) => e.type === 'ActivityStarted');
    expect(started?.activityStartedEventDetails?.workerName).toBe('w1');

    // An idle poll holds for 60 s on AWS and here; the Go tests cover it with
    // a shortened timeout. Deleting the activity is enough to end this one.
    await c.send(new sfn.DeleteActivityCommand({ activityArn: act.activityArn }));
    await expect(c.send(new sfn.GetActivityTaskCommand({ activityArn: act.activityArn }))).rejects.toBeInstanceOf(sfn.ActivityDoesNotExist);
  });

  test('redrive: a failed execution resumes from the state that failed', async () => {
    const name = uniq('sdk-redrive');
    const { stateMachineArn: arn } = await c.send(new sfn.CreateStateMachineCommand({ name, definition: FLOW, roleArn: ROLE }));
    const run = await c.send(new sfn.StartExecutionCommand({ stateMachineArn: arn, input: '{"mode":"fail"}' }));
    const failed = await settle(run.executionArn!);
    expect(failed.status).toBe('FAILED');
    expect(failed.redriveStatus).toBe('REDRIVABLE');

    const redriven = await c.send(new sfn.RedriveExecutionCommand({ executionArn: run.executionArn, clientToken: 'once' }));
    expect(redriven.redriveDate).toBeInstanceOf(Date);
    // The same token repeats the same answer; a running execution is refused.
    await c.send(new sfn.RedriveExecutionCommand({ executionArn: run.executionArn, clientToken: 'once' }));
    // The Fail state fails again — redrive reruns from the failure, so the
    // outcome is the same FAILED, now with a redrive count.
    const after = await settle(run.executionArn!);
    expect(after.status).toBe('FAILED');
    expect(after.redriveCount).toBe(1);
    expect(after.redriveDate).toBeInstanceOf(Date);
    const kinds = (await history(run.executionArn!)).map((e) => e.type);
    expect(kinds).toContain('ExecutionRedriven');

    const filtered = await c.send(new sfn.ListExecutionsCommand({ stateMachineArn: arn, redriveFilter: 'REDRIVEN' }));
    expect(filtered.executions!.map((e) => e.executionArn)).toEqual([run.executionArn]);
    // A succeeded execution is not redrivable.
    const good = await c.send(new sfn.StartExecutionCommand({ stateMachineArn: arn, input: '{"mode":"go","items":[]}' }));
    await settle(good.executionArn!);
    await expect(c.send(new sfn.RedriveExecutionCommand({ executionArn: good.executionArn }))).rejects.toBeInstanceOf(sfn.ExecutionNotRedrivable);
  });

  test('distributed map: a Map Run with child executions, JSONata in the processor', async () => {
    const definition = JSON.stringify({
      StartAt: 'Fan',
      States: {
        Fan: {
          Type: 'Map', End: true, Label: 'items', MaxConcurrency: 2, ToleratedFailureCount: 1,
          ItemProcessor: {
            ProcessorConfig: { Mode: 'DISTRIBUTED', ExecutionType: 'STANDARD' },
            StartAt: 'Double',
            States: {
              Double: { Type: 'Pass', QueryLanguage: 'JSONata', Output: '{% $states.input * 2 %}', Next: 'Check' },
              Check: {
                Type: 'Choice', QueryLanguage: 'JSONata',
                Choices: [{ Condition: '{% $states.input > 6 %}', Next: 'TooBig' }],
                Default: 'Fine',
              },
              Fine: { Type: 'Succeed' },
              TooBig: { Type: 'Fail', Error: 'TooBig' },
            },
          },
        },
      },
    });
    const name = uniq('sdk-dmap');
    const { stateMachineArn: arn } = await c.send(new sfn.CreateStateMachineCommand({ name, definition, roleArn: ROLE }));
    const run = await c.send(new sfn.StartExecutionCommand({ stateMachineArn: arn, input: '[1,2,3,4]' }));
    const d = await settle(run.executionArn!);
    // One child fails (8 > 6), within the tolerated count.
    expect(d.status).toBe('SUCCEEDED');

    const runs = await c.send(new sfn.ListMapRunsCommand({ executionArn: run.executionArn }));
    expect(runs.mapRuns).toHaveLength(1);
    const mapRunArn = runs.mapRuns![0].mapRunArn!;
    expect(mapRunArn).toContain(':mapRun:');
    const mr = await c.send(new sfn.DescribeMapRunCommand({ mapRunArn }));
    expect(mr.status).toBe('SUCCEEDED');
    expect(mr.maxConcurrency).toBe(2);
    expect(mr.toleratedFailureCount).toBe(1);
    expect(mr.itemCounts?.total).toBe(4);
    expect(mr.itemCounts?.succeeded).toBe(3);
    expect(mr.itemCounts?.failed).toBe(1);
    expect(mr.executionCounts?.total).toBe(4);
    expect(mr.startDate).toBeInstanceOf(Date);

    const children = await c.send(new sfn.ListExecutionsCommand({ mapRunArn }));
    expect(children.executions).toHaveLength(4);
    const failedChild = children.executions!.find((e) => e.status === 'FAILED');
    expect(failedChild).toBeTruthy();
    const child = await c.send(new sfn.DescribeExecutionCommand({ executionArn: failedChild!.executionArn }));
    expect(child.mapRunArn).toBe(mapRunArn);
    expect(child.error).toBe('TooBig');
    await expect(c.send(new sfn.UpdateMapRunCommand({ mapRunArn, maxConcurrency: 5 }))).resolves.toBeTruthy();
    expect((await c.send(new sfn.DescribeMapRunCommand({ mapRunArn }))).maxConcurrency).toBe(5);
  });

  test('errors are typed the way the SDK expects', async () => {
    const ghost = 'arn:aws:states:us-east-1:000000000000:stateMachine:ghost';
    await expect(c.send(new sfn.DescribeStateMachineCommand({ stateMachineArn: ghost }))).rejects.toBeInstanceOf(sfn.StateMachineDoesNotExist);
    await expect(c.send(new sfn.DescribeStateMachineCommand({ stateMachineArn: 'nope' }))).rejects.toBeInstanceOf(sfn.InvalidArn);
    await expect(c.send(new sfn.ListTagsForResourceCommand({ resourceArn: ghost }))).rejects.toBeInstanceOf(sfn.ResourceNotFound);
    await expect(c.send(new sfn.CreateStateMachineCommand({ name: 'has space', definition: FLOW, roleArn: ROLE }))).rejects.toBeInstanceOf(sfn.InvalidName);
    await expect(c.send(new sfn.CreateStateMachineCommand({ name: uniq('bad'), definition: '{"StartAt":"X"}', roleArn: ROLE }))).rejects.toBeInstanceOf(sfn.InvalidDefinition);
    await expect(c.send(new sfn.DescribeExecutionCommand({ executionArn: 'arn:aws:states:us-east-1:000000000000:execution:ghost:run' }))).rejects.toBeInstanceOf(sfn.ExecutionDoesNotExist);
    await expect(c.send(new sfn.SendTaskSuccessCommand({ taskToken: 'never', output: '{}' }))).rejects.toBeInstanceOf(sfn.TaskDoesNotExist);

    // Nothing is staged: every operation on a missing resource answers its
    // typed not-found, never a generic 400.
    await expect(c.send(new sfn.GetActivityTaskCommand({ activityArn: 'arn:aws:states:us-east-1:000000000000:activity:x' }))).rejects.toBeInstanceOf(sfn.ActivityDoesNotExist);
    await expect(c.send(new sfn.RedriveExecutionCommand({ executionArn: 'arn:aws:states:us-east-1:000000000000:execution:m:e' }))).rejects.toBeInstanceOf(sfn.ExecutionDoesNotExist);
    await expect(c.send(new sfn.ListStateMachineAliasesCommand({ stateMachineArn: ghost }))).rejects.toBeInstanceOf(sfn.StateMachineDoesNotExist);
    await expect(c.send(new sfn.ListMapRunsCommand({ executionArn: 'arn:aws:states:us-east-1:000000000000:execution:m:e' }))).rejects.toBeInstanceOf(sfn.ExecutionDoesNotExist);
    await expect(c.send(new sfn.DescribeMapRunCommand({ mapRunArn: 'arn:aws:states:us-east-1:000000000000:mapRun:m/e:x' }))).rejects.toBeInstanceOf(sfn.ResourceNotFound);
  });
});
