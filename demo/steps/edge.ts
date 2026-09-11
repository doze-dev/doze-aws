// API Gateway: the public REST API in front of the pipeline, and an HTTP API
// for the depot scanners.

import { apigw, apigwv2, lambda, arn, REGION } from '../lib/aws';
import { section, step, note, look } from '../lib/say';
import {
  CreateRestApiCommand, GetResourcesCommand, CreateResourceCommand, PutMethodCommand,
  PutIntegrationCommand, PutMethodResponseCommand, PutIntegrationResponseCommand,
  CreateDeploymentCommand, CreateApiKeyCommand, CreateUsagePlanCommand,
  CreateUsagePlanKeyCommand, TagResourceCommand,
} from '@aws-sdk/client-api-gateway';
import { CreateApiCommand, CreateStageCommand, CreateRouteCommand, CreateIntegrationCommand } from '@aws-sdk/client-apigatewayv2';
import { AddPermissionCommand } from '@aws-sdk/client-lambda';

export async function edge() {
  section('API Gateway', 'the storefront API, and an HTTP API for the depot scanners');

  const api = await step('created the harbour-public REST API', () =>
    apigw.send(new CreateRestApiCommand({
      name: 'harbour-public',
      description: 'What the storefront and the mobile apps call',
      tags: { team: 'orders', env: 'prod' },
    })), 'apigateway:CreateRestApi');
  if (!api?.id) return;

  const roots = await apigw.send(new GetResourcesCommand({ restApiId: api.id }));
  const rootId = roots.items?.find((r) => r.path === '/')?.id;
  if (!rootId) return;

  const orders = await step('added /orders', () =>
    apigw.send(new CreateResourceCommand({ restApiId: api.id!, parentId: rootId, pathPart: 'orders' })),
    'apigateway:CreateResource');

  const oneOrder = orders?.id
    ? await step('added /orders/{orderId}', () =>
        apigw.send(new CreateResourceCommand({
          restApiId: api.id!, parentId: orders.id!, pathPart: '{orderId}',
        })), 'apigateway:CreateResource')
    : undefined;

  const validatorArn = arn('lambda', 'function:harbour-order-validator');
  const proxyUri = `arn:aws:apigateway:${REGION}:lambda:path/2015-03-31/functions/${validatorArn}/invocations`;

  if (orders?.id) {
    await step('POST /orders → the validator, as a Lambda proxy', async () => {
      await apigw.send(new PutMethodCommand({
        restApiId: api.id!, resourceId: orders.id!, httpMethod: 'POST', authorizationType: 'NONE',
        apiKeyRequired: true,
      }));
      await apigw.send(new PutIntegrationCommand({
        restApiId: api.id!, resourceId: orders.id!, httpMethod: 'POST',
        type: 'AWS_PROXY', integrationHttpMethod: 'POST', uri: proxyUri,
      }));
    }, 'apigateway:PutMethod + PutIntegration');

    await step('let API Gateway invoke the function', () =>
      lambda.send(new AddPermissionCommand({
        FunctionName: 'harbour-order-validator',
        StatementId: 'apigw-post-orders',
        Action: 'lambda:InvokeFunction',
        Principal: 'apigateway.amazonaws.com',
        SourceArn: `arn:aws:execute-api:${REGION}:000000000000:${api.id}/*/POST/orders`,
      })), 'lambda:AddPermission');
  }

  if (oneOrder?.id) {
    await step('GET /orders/{orderId} → a MOCK, for the status endpoint', async () => {
      await apigw.send(new PutMethodCommand({
        restApiId: api.id!, resourceId: oneOrder.id!, httpMethod: 'GET', authorizationType: 'NONE',
      }));
      await apigw.send(new PutIntegrationCommand({
        restApiId: api.id!, resourceId: oneOrder.id!, httpMethod: 'GET',
        type: 'MOCK', requestTemplates: { 'application/json': '{"statusCode": 200}' },
      }));
      await apigw.send(new PutMethodResponseCommand({
        restApiId: api.id!, resourceId: oneOrder.id!, httpMethod: 'GET', statusCode: '200',
      }));
      await apigw.send(new PutIntegrationResponseCommand({
        restApiId: api.id!, resourceId: oneOrder.id!, httpMethod: 'GET', statusCode: '200',
        responseTemplates: {
          'application/json': JSON.stringify({ orderId: '$input.params("orderId")', status: 'out_for_delivery', slot: 'today, 6pm–9pm' }),
        },
      }));
    }, 'apigateway:PutIntegration (MOCK)');
    note('a MOCK integration answers without a backend — the honest way to stub a status endpoint');
  }

  await step('deployed it to the prod stage', () =>
    apigw.send(new CreateDeploymentCommand({
      restApiId: api.id!, stageName: 'prod', description: 'Storefront cutover',
      variables: { depot: 'edinburgh' },
    })), 'apigateway:CreateDeployment');

  const key = await step('issued an API key for the mobile apps', () =>
    apigw.send(new CreateApiKeyCommand({
      name: 'harbour-mobile', description: 'iOS and Android storefront', enabled: true,
    })), 'apigateway:CreateApiKey');

  const plan = await step('created a 10 req/s usage plan', () =>
    apigw.send(new CreateUsagePlanCommand({
      name: 'harbour-mobile-plan',
      description: 'What a released app build is allowed',
      throttle: { rateLimit: 10, burstLimit: 20 },
      quota: { limit: 50000, period: 'MONTH' },
      apiStages: [{ apiId: api.id!, stage: 'prod' }],
    })), 'apigateway:CreateUsagePlan');

  if (key?.id && plan?.id) {
    await step('put the key on the plan', () =>
      apigw.send(new CreateUsagePlanKeyCommand({
        usagePlanId: plan.id!, keyId: key.id!, keyType: 'API_KEY',
      })), 'apigateway:CreateUsagePlanKey');
  }

  look(`/_console/apigw`, 'resources, methods, the deployment and the usage plan');

  // --- HTTP API (v2) ------------------------------------------------------
  const http = await step('created the depot-scanner HTTP API', () =>
    apigwv2.send(new CreateApiCommand({
      Name: 'harbour-depot-scanners',
      ProtocolType: 'HTTP',
      Description: 'What the handheld scanners in the depots call',
      Tags: { team: 'logistics' },
    })), 'apigatewayv2:CreateApi');

  if (http?.ApiId) {
    const integ = await step('integrated it with the forecaster', () =>
      apigwv2.send(new CreateIntegrationCommand({
        ApiId: http.ApiId!, IntegrationType: 'AWS_PROXY',
        IntegrationUri: arn('lambda', 'function:harbour-stock-forecaster'),
        PayloadFormatVersion: '2.0',
      })), 'apigatewayv2:CreateIntegration');

    if (integ?.IntegrationId) {
      await step('routed POST /scan to it', () =>
        apigwv2.send(new CreateRouteCommand({
          ApiId: http.ApiId!, RouteKey: 'POST /scan', Target: `integrations/${integ.IntegrationId}`,
        })), 'apigatewayv2:CreateRoute');
    }

    await step('auto-deployed the $default stage', () =>
      apigwv2.send(new CreateStageCommand({
        ApiId: http.ApiId!, StageName: '$default', AutoDeploy: true,
      })), 'apigatewayv2:CreateStage');
  }

  look('/_console/apigw-v2', 'the HTTP API, its route and its stage');
}
