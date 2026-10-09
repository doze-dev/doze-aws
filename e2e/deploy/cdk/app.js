// A small CDK app: a bucket, a queue, a table and a function that reads them.
const cdk = require('aws-cdk-lib');
const s3 = require('aws-cdk-lib/aws-s3');
const sqs = require('aws-cdk-lib/aws-sqs');
const ddb = require('aws-cdk-lib/aws-dynamodb');
const lambda = require('aws-cdk-lib/aws-lambda');

const app = new cdk.App();
const stack = new cdk.Stack(app, 'ShopStack', {
  env: { account: '000000000000', region: 'us-east-1' },
});

const bucket = new s3.Bucket(stack, 'Receipts', { removalPolicy: cdk.RemovalPolicy.DESTROY });
const queue = new sqs.Queue(stack, 'Orders');
const table = new ddb.Table(stack, 'Sessions', {
  partitionKey: { name: 'id', type: ddb.AttributeType.STRING },
  billingMode: ddb.BillingMode.PAY_PER_REQUEST,
  removalPolicy: cdk.RemovalPolicy.DESTROY,
});

const fn = new lambda.Function(stack, 'Handler', {
  runtime: lambda.Runtime.PYTHON_3_12,
  handler: 'index.handler',
  code: lambda.Code.fromInline('def handler(event, context):\n    return {"ok": True}\n'),
  environment: { BUCKET: bucket.bucketName, QUEUE: queue.queueUrl, TABLE: table.tableName },
});
bucket.grantRead(fn);
queue.grantSendMessages(fn);
table.grantReadWriteData(fn);

new cdk.CfnOutput(stack, 'FunctionName', { value: fn.functionName });
