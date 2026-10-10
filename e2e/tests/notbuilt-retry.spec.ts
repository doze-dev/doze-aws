import { test, expect } from '@playwright/test';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { SQSClient, ListQueuesCommand } from '@aws-sdk/client-sqs';
import { CloudWatchClient, ListDashboardsCommand } from '@aws-sdk/client-cloudwatch';

// The JavaScript SDK does not retry the answer for an operation doze-aws does
// not build.
//
// A not-built refusal is HTTP 501 with the code NotImplemented (plan decision
// D5), and the design rests on no SDK retrying a 501: if one did, a caller
// would wait through its backoff for an answer that cannot change. The Go SDKs
// are proved in internal/awshttp/notbuilt_sdk_test.go and boto3 in
// conformance/tests/test_notbuilt_retry.py; this is the third.
//
// It serves the refusal itself and counts what reaches it, so it needs no
// doze-aws binary. The same call answered with a 500 is the control: it has to
// be retried, or "one request" would only mean the client never retries.

const MESSAGE = 'ListThings is not built in doze-aws: there is nothing for it to act on';

async function serve(status: number, code: string, body: (code: string) => string, type: string) {
  let hits = 0;
  const srv: Server = createServer((req, res) => {
    hits++;
    req.resume();
    req.on('end', () => {
      res.writeHead(status, { 'Content-Type': type, 'x-amzn-ErrorType': code, 'X-Doze-Not-Built': 'declined' });
      res.end(body(code));
    });
  });
  await new Promise<void>((r) => srv.listen(0, '127.0.0.1', r));
  const url = `http://127.0.0.1:${(srv.address() as AddressInfo).port}`;
  return { url, hits: () => hits, close: () => new Promise((r) => srv.close(() => r(null))) };
}

const jsonBody = (code: string) => JSON.stringify({ __type: code, message: MESSAGE });
const queryBody = (code: string) =>
  `<?xml version="1.0"?><ErrorResponse><Error><Type>Sender</Type><Code>${code}</Code>` +
  `<Message>${MESSAGE}</Message></Error><RequestId>r1</RequestId></ErrorResponse>`;

const creds = { accessKeyId: 'test', secretAccessKey: 'test' };

const cases: { name: string; type: string; body: (c: string) => string; call: (url: string) => Promise<unknown> }[] = [
  {
    name: 'JSON (sqs)',
    type: 'application/x-amz-json-1.0',
    body: jsonBody,
    call: (url) => new SQSClient({ endpoint: url, region: 'us-east-1', credentials: creds, maxAttempts: 4 })
      .send(new ListQueuesCommand({})),
  },
  {
    name: 'Query (cloudwatch)',
    type: 'text/xml',
    body: queryBody,
    call: (url) => new CloudWatchClient({ endpoint: url, region: 'us-east-1', credentials: creds, maxAttempts: 4 })
      .send(new ListDashboardsCommand({})),
  },
];

for (const c of cases) {
  test(`a 501 NotImplemented is not retried: ${c.name}`, async () => {
    test.setTimeout(60_000);
    const refusal = await serve(501, 'NotImplemented', c.body, c.type);
    await expect(c.call(refusal.url)).rejects.toBeTruthy();
    expect(
      refusal.hits(),
      'the SDK retried a 501; the status has to be reconsidered before the rule spreads',
    ).toBe(1);
    await refusal.close();

    const control = await serve(500, 'InternalFailure', c.body, c.type);
    await expect(c.call(control.url)).rejects.toBeTruthy();
    expect(
      control.hits(),
      'control: this client did not retry a 500, so the 501 result above proves nothing',
    ).toBeGreaterThan(1);
    await control.close();
  });
}
