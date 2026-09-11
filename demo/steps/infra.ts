// CloudFormation: a stack deployed the way the rest of Harbour's infrastructure
// is, so the console has resources it owns, outputs, and an export another
// stack imports.

import { cfn } from '../lib/aws';
import { section, step, note, look, watch } from '../lib/say';
import {
  CreateStackCommand, DescribeStacksCommand, UpdateStackCommand, ListStackResourcesCommand,
} from '@aws-sdk/client-cloudformation';

const LOYALTY = `AWSTemplateFormatVersion: "2010-09-09"
Description: Harbour loyalty — points ledger and the queue that feeds it

Parameters:
  Environment:
    Type: String
    Default: prod
    AllowedValues: [prod, staging]
    Description: Which environment this stack belongs to
  PointsPerPound:
    Type: Number
    Default: 4
    Description: Loyalty points awarded per whole pound spent

Resources:
  LoyaltyLedger:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: !Sub "harbour-loyalty-\${Environment}"
      BillingMode: PAY_PER_REQUEST
      AttributeDefinitions:
        - AttributeName: customerId
          AttributeType: S
        - AttributeName: earnedAt
          AttributeType: S
      KeySchema:
        - AttributeName: customerId
          KeyType: HASH
        - AttributeName: earnedAt
          KeyType: RANGE

  PointsQueue:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub "harbour-loyalty-points-\${Environment}"
      MessageRetentionPeriod: 345600

  PointsTopic:
    Type: AWS::SNS::Topic
    Properties:
      TopicName: !Sub "harbour-loyalty-milestones-\${Environment}"

  StatementsBucket:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Sub "harbour-loyalty-statements-\${Environment}"

Outputs:
  LedgerTable:
    Description: The points ledger other services read
    Value: !Ref LoyaltyLedger
    Export:
      Name: !Sub "harbour-loyalty-table-\${Environment}"
  PointsQueueUrl:
    Description: Where the checkout posts earned points
    Value: !Ref PointsQueue
  EarnRate:
    Description: Points per pound, as deployed
    Value: !Ref PointsPerPound
`;

export async function infra() {
  section('CloudFormation', 'the loyalty stack, deployed from a template');

  await step('created harbour-loyalty', () =>
    cfn.send(new CreateStackCommand({
      StackName: 'harbour-loyalty',
      TemplateBody: LOYALTY,
      Parameters: [
        { ParameterKey: 'Environment', ParameterValue: 'prod' },
        { ParameterKey: 'PointsPerPound', ParameterValue: '4' },
      ],
      Tags: [{ Key: 'team', Value: 'growth' }, { Key: 'env', Value: 'prod' }],
    })), 'cloudformation:CreateStack');
  note('four resources across DynamoDB, SQS, SNS and S3 — really created, not recorded');

  await watch(1200);

  const described = await step('read the stack back', async () => {
    const d = await cfn.send(new DescribeStacksCommand({ StackName: 'harbour-loyalty' }));
    return d.Stacks?.[0];
  }, 'cloudformation:DescribeStacks');
  if (described) {
    note(`${described.StackStatus} · ${described.Outputs?.length ?? 0} outputs, one of them exported`);
  }

  await step('listed what the stack owns', async () => {
    const r = await cfn.send(new ListStackResourcesCommand({ StackName: 'harbour-loyalty' }));
    return r.StackResourceSummaries?.length;
  }, 'cloudformation:ListStackResources');

  await step('updated the earn rate to 5 points per pound', () =>
    cfn.send(new UpdateStackCommand({
      StackName: 'harbour-loyalty',
      TemplateBody: LOYALTY,
      Parameters: [
        { ParameterKey: 'Environment', ParameterValue: 'prod' },
        { ParameterKey: 'PointsPerPound', ParameterValue: '5' },
      ],
    })), 'cloudformation:UpdateStack');
  note('the console shows what changed and what was left alone');

  look('/_console/cfn', 'resources, parameters, outputs and the exported value');
}
