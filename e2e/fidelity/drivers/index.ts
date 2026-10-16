import type { DriverTable } from './types';
import { drivers as apigw } from './apigw';
import { drivers as cfn } from './cfn';
import { drivers as cw } from './cw';
import { drivers as ddb } from './ddb';
import { drivers as eb } from './eb';
import { drivers as iam } from './iam';
import { drivers as kinesis } from './kinesis';
import { drivers as kms } from './kms';
import { drivers as lambda } from './lambda';
import { drivers as logs } from './logs';
import { drivers as s3 } from './s3';
import { drivers as sfn } from './sfn';
import { drivers as shell } from './shell';
import { drivers as sm } from './sm';
import { drivers as sns } from './sns';
import { drivers as sqs } from './sqs';
import { drivers as ssm } from './ssm';

/** Keyed by the service key in boards.ts (and `shell`). */
export const DRIVERS: Record<string, DriverTable> = {
  apigw, cfn, cw, ddb, eb, iam, kinesis, kms, lambda, logs, s3, sfn, shell, sm, sns, sqs, ssm,
};
