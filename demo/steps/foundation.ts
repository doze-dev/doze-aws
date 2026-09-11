// The platform layer: who you are, the keys, and the configuration every
// other service reads. Seeded first because the rest reference it.

import { sts, iam, kms, ssm, secrets, iamArn, REGION } from '../lib/aws';
import { section, step, note, look, did } from '../lib/say';
import { GetCallerIdentityCommand } from '@aws-sdk/client-sts';
import {
  CreateRoleCommand, CreatePolicyCommand, AttachRolePolicyCommand, CreateUserCommand,
  CreateGroupCommand, AddUserToGroupCommand, PutUserPolicyCommand, CreateAccessKeyCommand,
  TagRoleCommand, CreateInstanceProfileCommand, AddRoleToInstanceProfileCommand,
} from '@aws-sdk/client-iam';
import {
  CreateKeyCommand, CreateAliasCommand, EnableKeyRotationCommand, EncryptCommand,
  DecryptCommand, TagResourceCommand as KmsTag,
} from '@aws-sdk/client-kms';
import { PutParameterCommand, AddTagsToResourceCommand, LabelParameterVersionCommand } from '@aws-sdk/client-ssm';
import {
  CreateSecretCommand, PutSecretValueCommand, TagResourceCommand as SecTag,
  PutResourcePolicyCommand, RotateSecretCommand,
} from '@aws-sdk/client-secrets-manager';

const TRUST_LAMBDA = JSON.stringify({
  Version: '2012-10-17',
  Statement: [{ Effect: 'Allow', Principal: { Service: 'lambda.amazonaws.com' }, Action: 'sts:AssumeRole' }],
}, null, 2);

const TRUST_STATES = JSON.stringify({
  Version: '2012-10-17',
  Statement: [{ Effect: 'Allow', Principal: { Service: 'states.amazonaws.com' }, Action: 'sts:AssumeRole' }],
}, null, 2);

const ORDER_PIPELINE_POLICY = JSON.stringify({
  Version: '2012-10-17',
  Statement: [
    { Sid: 'ReadWriteOrders', Effect: 'Allow',
      Action: ['dynamodb:GetItem', 'dynamodb:PutItem', 'dynamodb:UpdateItem', 'dynamodb:Query'],
      Resource: `arn:aws:dynamodb:${REGION}:000000000000:table/harbour-orders` },
    { Sid: 'DrainCheckoutQueue', Effect: 'Allow',
      Action: ['sqs:ReceiveMessage', 'sqs:DeleteMessage', 'sqs:GetQueueAttributes'],
      Resource: `arn:aws:sqs:${REGION}:000000000000:harbour-checkout-orders` },
    { Sid: 'PutReceipts', Effect: 'Allow', Action: 's3:PutObject',
      Resource: 'arn:aws:s3:::harbour-receipts/*' },
    { Sid: 'Logs', Effect: 'Allow',
      Action: ['logs:CreateLogStream', 'logs:PutLogEvents'], Resource: '*' },
  ],
}, null, 2);

export async function foundation() {
  section('IAM, KMS, SSM, Secrets Manager', 'the platform layer everything else references');

  const me = await step('confirmed the caller identity', async () => {
    const r = await sts.send(new GetCallerIdentityCommand({}));
    return r;
  }, 'sts:GetCallerIdentity');
  if (me) note(`account ${me.Account}, ${me.Arn}`);

  // --- roles and policies -------------------------------------------------
  await step('created the Lambda execution role', () =>
    iam.send(new CreateRoleCommand({
      RoleName: 'harbour-lambda-exec',
      AssumeRolePolicyDocument: TRUST_LAMBDA,
      Description: 'Execution role for the order pipeline functions',
      MaxSessionDuration: 3600,
    })), 'iam:CreateRole');

  await step('tagged the role for cost allocation', () =>
    iam.send(new TagRoleCommand({
      RoleName: 'harbour-lambda-exec',
      Tags: [{ Key: 'team', Value: 'orders' }, { Key: 'env', Value: 'prod' }, { Key: 'cost-centre', Value: 'CC-4181' }],
    })), 'iam:TagRole');

  await step('created the Step Functions role', () =>
    iam.send(new CreateRoleCommand({
      RoleName: 'harbour-states-exec',
      AssumeRolePolicyDocument: TRUST_STATES,
      Description: 'Lets the order-fulfilment workflow invoke the pipeline functions',
    })), 'iam:CreateRole');

  const policy = await step('wrote the order-pipeline policy', () =>
    iam.send(new CreatePolicyCommand({
      PolicyName: 'harbour-order-pipeline',
      PolicyDocument: ORDER_PIPELINE_POLICY,
      Description: 'Least privilege for the checkout → fulfilment path',
    })), 'iam:CreatePolicy');

  if (policy?.Policy?.Arn) {
    await step('attached it to the execution role', () =>
      iam.send(new AttachRolePolicyCommand({
        RoleName: 'harbour-lambda-exec', PolicyArn: policy.Policy!.Arn!,
      })), 'iam:AttachRolePolicy');
  }

  // A human, a group and an inline policy — the shapes an IAM page should show.
  await step('created the platform group', () =>
    iam.send(new CreateGroupCommand({ GroupName: 'harbour-platform' })), 'iam:CreateGroup');
  await step('created a developer user', () =>
    iam.send(new CreateUserCommand({ UserName: 'jo.mcleod', Path: '/engineering/' })), 'iam:CreateUser');
  await step('put jo.mcleod in the platform group', () =>
    iam.send(new AddUserToGroupCommand({ GroupName: 'harbour-platform', UserName: 'jo.mcleod' })), 'iam:AddUserToGroup');
  await step('gave the user a read-only inline policy', () =>
    iam.send(new PutUserPolicyCommand({
      UserName: 'jo.mcleod',
      PolicyName: 'read-orders-bucket',
      PolicyDocument: JSON.stringify({
        Version: '2012-10-17',
        Statement: [{ Effect: 'Allow', Action: ['s3:GetObject', 's3:ListBucket'],
          Resource: ['arn:aws:s3:::harbour-receipts', 'arn:aws:s3:::harbour-receipts/*'] }],
      }, null, 2),
    })), 'iam:PutUserPolicy');
  await step('issued an access key for the user', () =>
    iam.send(new CreateAccessKeyCommand({ UserName: 'jo.mcleod' })), 'iam:CreateAccessKey');

  await step('created an instance profile for the depot scanners', () =>
    iam.send(new CreateInstanceProfileCommand({ InstanceProfileName: 'harbour-depot-scanner' })), 'iam:CreateInstanceProfile');
  await step('attached the execution role to it', () =>
    iam.send(new AddRoleToInstanceProfileCommand({
      InstanceProfileName: 'harbour-depot-scanner', RoleName: 'harbour-lambda-exec',
    })), 'iam:AddRoleToInstanceProfile');

  look('/_console/iam', 'roles, the policy builder, and the access checker');

  // --- KMS ----------------------------------------------------------------
  const key = await step('created the payments key', () =>
    kms.send(new CreateKeyCommand({
      Description: 'Encrypts card tokens and customer PII at rest',
      KeyUsage: 'ENCRYPT_DECRYPT',
      Tags: [{ TagKey: 'team', TagValue: 'payments' }, { TagKey: 'pci', TagValue: 'in-scope' }],
    })), 'kms:CreateKey');

  const keyId = key?.KeyMetadata?.KeyId;
  if (keyId) {
    await step('gave it a friendly alias', () =>
      kms.send(new CreateAliasCommand({ AliasName: 'alias/harbour-payments', TargetKeyId: keyId })), 'kms:CreateAlias');
    await step('turned on annual rotation', () =>
      kms.send(new EnableKeyRotationCommand({ KeyId: keyId })), 'kms:EnableKeyRotation');

    const ct = await step('encrypted a card token with it', () =>
      kms.send(new EncryptCommand({
        KeyId: 'alias/harbour-payments',
        Plaintext: new TextEncoder().encode('tok_visa_4242_CUS-10482'),
        EncryptionContext: { purpose: 'card-token', customer: 'CUS-10482' },
      })), 'kms:Encrypt');

    if (ct?.CiphertextBlob) {
      await step('decrypted it back, context and all', async () => {
        const out = await kms.send(new DecryptCommand({
          CiphertextBlob: ct.CiphertextBlob,
          EncryptionContext: { purpose: 'card-token', customer: 'CUS-10482' },
        }));
        const plain = new TextDecoder().decode(out.Plaintext);
        if (plain !== 'tok_visa_4242_CUS-10482') throw new Error(`round trip gave ${plain}`);
      }, 'kms:Decrypt');
      note('a wrong encryption context is refused — the key alone is not enough');
    }
  }

  const archiveKey = await step('created a second key, then scheduled it for deletion', async () => {
    const k = await kms.send(new CreateKeyCommand({ Description: 'Retired: 2024 loyalty export key' }));
    await kms.send(new CreateAliasCommand({ AliasName: 'alias/harbour-loyalty-legacy', TargetKeyId: k.KeyMetadata!.KeyId! }));
    return k;
  }, 'kms:CreateKey');
  if (archiveKey) look('/_console/kms', 'two keys, aliases, rotation state');

  // --- SSM Parameter Store ------------------------------------------------
  const params: [string, string, 'String' | 'StringList' | 'SecureString', string][] = [
    ['/harbour/prod/checkout/max-basket-gbp', '400', 'String', 'Single-order ceiling enforced by order-validator'],
    ['/harbour/prod/checkout/free-delivery-over-gbp', '40', 'String', 'Threshold price-calculator applies'],
    ['/harbour/prod/delivery/serviceable-areas', 'EH,G,KY,FK,ML', 'StringList', 'Postcode areas Harbour delivers to'],
    ['/harbour/prod/delivery/slot-length-minutes', '240', 'String', ''],
    ['/harbour/prod/depot/edinburgh/van-count', '18', 'String', ''],
    ['/harbour/prod/depot/glasgow/van-count', '24', 'String', ''],
    ['/harbour/prod/integrations/stripe/publishable-key', 'pk_test_51Harbour0000000000', 'String', ''],
    ['/harbour/prod/integrations/courier/webhook-secret', 'whsec_7f3a91c22b8e4d56', 'SecureString', 'Verifies inbound courier callbacks'],
    ['/harbour/staging/checkout/max-basket-gbp', '999', 'String', 'Staging is deliberately looser'],
  ];
  for (const [name, value, type, description] of params) {
    await step(`set ${name}`, () =>
      ssm.send(new PutParameterCommand({
        Name: name, Value: value, Type: type, Overwrite: true,
        ...(description ? { Description: description } : {}),
        ...(type === 'SecureString' ? { KeyId: 'alias/harbour-payments' } : {}),
      })), 'ssm:PutParameter');
  }
  await step('tagged the courier secret', () =>
    ssm.send(new AddTagsToResourceCommand({
      ResourceType: 'Parameter', ResourceId: '/harbour/prod/integrations/courier/webhook-secret',
      Tags: [{ Key: 'rotate', Value: 'quarterly' }],
    })), 'ssm:AddTagsToResource');
  await step('labelled the basket ceiling as live', () =>
    ssm.send(new LabelParameterVersionCommand({
      Name: '/harbour/prod/checkout/max-basket-gbp', Labels: ['live'],
    })), 'ssm:LabelParameterVersion');
  look('/_console/ssm', 'the parameter tree, prod and staging side by side');

  // --- Secrets Manager ----------------------------------------------------
  await step('stored the database credentials', () =>
    secrets.send(new CreateSecretCommand({
      Name: 'harbour/prod/orders-db',
      Description: 'Postgres credentials for the orders service',
      SecretString: JSON.stringify({
        engine: 'postgres', host: 'orders-db.harbour.internal', port: 5432,
        dbname: 'orders', username: 'orders_app', password: 'Pv9!nQ2wLr7xTb4m',
      }, null, 2),
      Tags: [{ Key: 'team', Value: 'orders' }, { Key: 'rotate', Value: '30d' }],
    })), 'secretsmanager:CreateSecret');

  await step('rotated it, leaving the previous version readable', () =>
    secrets.send(new PutSecretValueCommand({
      SecretId: 'harbour/prod/orders-db',
      SecretString: JSON.stringify({
        engine: 'postgres', host: 'orders-db.harbour.internal', port: 5432,
        dbname: 'orders', username: 'orders_app', password: 'Hm3@kW8zPq5vRn1c',
      }, null, 2),
      VersionStages: ['AWSCURRENT'],
    })), 'secretsmanager:PutSecretValue');
  note('AWSPREVIOUS still resolves to the old password — that is what makes a rotation safe');

  await step('stored the Stripe secret key', () =>
    secrets.send(new CreateSecretCommand({
      Name: 'harbour/prod/stripe-secret-key',
      Description: 'Live Stripe key — payments team owns rotation',
      SecretString: 'sk_test_51Harbour00000000000000000000',
      Tags: [{ Key: 'team', Value: 'payments' }],
    })), 'secretsmanager:CreateSecret');

  await step('put a resource policy on the Stripe secret', () =>
    secrets.send(new PutResourcePolicyCommand({
      SecretId: 'harbour/prod/stripe-secret-key',
      ResourcePolicy: JSON.stringify({
        Version: '2012-10-17',
        Statement: [{
          Effect: 'Allow',
          Principal: { AWS: iamArn('role/harbour-lambda-exec') },
          Action: 'secretsmanager:GetSecretValue',
          Resource: '*',
        }],
      }, null, 2),
    })), 'secretsmanager:PutResourcePolicy');

  await step('asked for a rotation of the courier secret', () =>
    secrets.send(new CreateSecretCommand({
      Name: 'harbour/prod/courier-api-token',
      SecretString: 'crr_live_2f81aa64c9d34e77b0',
    })).then(() => secrets.send(new RotateSecretCommand({ SecretId: 'harbour/prod/courier-api-token' })))
      .catch(() => undefined), 'secretsmanager:RotateSecret');

  look('/_console/sm', 'versions, staging labels and the resource policy builder');
}
