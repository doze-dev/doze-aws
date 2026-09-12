// Every AWS client the seed uses, pointed at a local doze-aws.
//
// One endpoint, one set of credentials, no per-service configuration: that is
// the whole point of the emulator, and a seed script that needed a special case
// per service would be evidence against it.

import { S3Client } from '@aws-sdk/client-s3';
import { SQSClient } from '@aws-sdk/client-sqs';
import { SNSClient } from '@aws-sdk/client-sns';
import { DynamoDBClient } from '@aws-sdk/client-dynamodb';
import { DynamoDBDocumentClient } from '@aws-sdk/lib-dynamodb';
import { LambdaClient } from '@aws-sdk/client-lambda';
import { EventBridgeClient } from '@aws-sdk/client-eventbridge';
import { KinesisClient } from '@aws-sdk/client-kinesis';
import { IAMClient } from '@aws-sdk/client-iam';
import { KMSClient } from '@aws-sdk/client-kms';
import { SecretsManagerClient } from '@aws-sdk/client-secrets-manager';
import { SSMClient } from '@aws-sdk/client-ssm';
import { CloudWatchClient } from '@aws-sdk/client-cloudwatch';
import { CloudWatchLogsClient } from '@aws-sdk/client-cloudwatch-logs';
import { SFNClient } from '@aws-sdk/client-sfn';
import { APIGatewayClient } from '@aws-sdk/client-api-gateway';
import { ApiGatewayV2Client } from '@aws-sdk/client-apigatewayv2';
import { CloudFormationClient } from '@aws-sdk/client-cloudformation';
import { STSClient } from '@aws-sdk/client-sts';
import { NodeHttpHandler } from '@smithy/node-http-handler';

/**
 * Where the instance answers. `eval "$(doze-aws env)"` sets this, and the
 * fallback is the address a `--listen` run uses — the seed should work whether
 * you started doze-aws on its .doze name or on an address.
 */
export const ENDPOINT = process.env.AWS_ENDPOINT_URL ?? 'http://127.0.0.1:4566';

/**
 * The region and account this demo builds ARNs with.
 *
 * These must MATCH the instance, not merely be plausible. An ARN built here
 * with a different region names a resource that does not exist, and the
 * services that take an ARN rather than a name — an SQS redrive policy, an
 * EventBridge target, a Lambda event source — accept it quietly and then never
 * fire.
 *
 * This comment used to say the region in your config was IGNORED, because
 * doze-aws stamped one region onto everything whatever the client asked for.
 * That is no longer true: a region is a folder under the data directory, a
 * signed request is served from the region its credential scope names, and
 * regions are created on first use. So the region below is a real choice —
 * it has to agree with the instance's default (`--region`, or the `region` key
 * in doze-aws.toml), or these ARNs point into a region the resources are not in.
 *
 * Both are read from the environment for that reason, so `doze-aws env` can
 * supply them.
 */
export const REGION = process.env.AWS_REGION ?? 'us-east-1';
export const ACCOUNT = process.env.AWS_ACCOUNT_ID ?? '000000000000';

const shared = {
  endpoint: ENDPOINT,
  region: REGION,
  credentials: { accessKeyId: 'test', secretAccessKey: 'test' },
};

export const s3 = new S3Client({ ...shared, forcePathStyle: true });
export const sqs = new SQSClient(shared);
export const sns = new SNSClient(shared);
export const ddbRaw = new DynamoDBClient(shared);
export const ddb = DynamoDBDocumentClient.from(ddbRaw, {
  marshallOptions: { removeUndefinedValues: true },
});
export const lambda = new LambdaClient(shared);
export const events = new EventBridgeClient(shared);
// Kinesis is the one client that needs telling. Its JS SDK defaults to HTTP/2
// (AWS's Kinesis endpoints speak it), and doze-aws serves HTTP/1.1 — so the
// default handler fails with a bare "Protocol error" from node:http2 before a
// request ever reaches the emulator. Handing it the ordinary HTTP handler is
// the whole fix, and it is the same one you would apply against any HTTP/1.1
// proxy in front of real Kinesis.
export const kinesis = new KinesisClient({ ...shared, requestHandler: new NodeHttpHandler() });
export const iam = new IAMClient(shared);
export const kms = new KMSClient(shared);
export const secrets = new SecretsManagerClient(shared);
export const ssm = new SSMClient(shared);
export const cw = new CloudWatchClient(shared);
export const logs = new CloudWatchLogsClient(shared);
export const sfn = new SFNClient(shared);
export const apigw = new APIGatewayClient(shared);
export const apigwv2 = new ApiGatewayV2Client(shared);
export const cfn = new CloudFormationClient(shared);
export const sts = new STSClient(shared);

export const arn = (service: string, resource: string) =>
  `arn:aws:${service}:${REGION}:${ACCOUNT}:${resource}`;

/**
 * IAM is a global service, so its ARNs carry no region — `arn:aws:iam::acct:...`
 * with the empty field. Getting this wrong is a good first thing to see the
 * emulator refuse: doze-aws validates it against the same pattern the real
 * service publishes and quotes the pattern back.
 */
export const iamArn = (resource: string) => `arn:aws:iam::${ACCOUNT}:${resource}`;

/** The execution role every function in the demo runs as. */
export const LAMBDA_ROLE = iamArn('role/harbour-lambda-exec');
