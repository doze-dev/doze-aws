import { test, expect } from '../fixtures/console';
import { postForm, createBucket } from '../fixtures/api';

// CloudFormation console coverage: the deploy path (template in → validate →
// parameters generated from the template → create), the change-set workflow
// (create-as-review → diff → execute / discard), UsePreviousTemplate updates,
// the resource drill-down, and the exports registry on the service home.

const template = (bucketRef: string) =>
  JSON.stringify({
    Parameters: { Env: { Type: 'String', Default: 'dev', Description: 'stage name' } },
    Resources: {
      [bucketRef]: {
        Type: 'AWS::S3::Bucket',
        Properties: { BucketName: { 'Fn::Sub': `${bucketRef.toLowerCase()}-\${Env}` } },
      },
    },
  });

test.describe('CloudFormation console', () => {
  test('validate, load parameters, review-first create, execute, update', async ({
    page,
    uniqueName,
    setEditor,
    confirmDialog,
  }) => {
    const stack = uniqueName('e2e-cfn-stack');
    const bucketRef = 'B' + stack.replace(/[^a-zA-Z0-9]/g, '');

    await page.goto('cfn/create');
    await setEditor('textarea[name="template"]', template(bucketRef));

    // Validate runs the emulator's real parser and reports in place.
    await page.getByRole('button', { name: 'Validate' }).click();
    await expect(page.locator('#cfn-validate-out .cfn-valid')).toContainText('Template is valid');

    // Load parameters builds the form from what the template declares.
    await page.getByRole('button', { name: 'Load parameters' }).click();
    const envInput = page.locator('#cfn-params input[name="param:Env"]');
    await expect(envInput).toHaveAttribute('placeholder', /default: dev/);
    await envInput.fill('prod');

    // Review-first: the change-set switch reveals the name field, and the
    // create lands on the diff instead of deploying.
    await page.locator('input[name="name"]').fill(stack);
    await page.locator('.opt-row:has-text("Create a change set") .switch').click();
    await expect(page.locator('input[name="changeset"]')).toBeVisible();
    await page.getByRole('button', { name: 'Create stack' }).click();
    await page.waitForURL(/tab=changesets&cs=console-review/);

    // The diff: one Add, in place, and the parameter as it would apply.
    const body = page.locator('.det-b');
    await expect(body.locator('.chip.ok', { hasText: 'Add' })).toBeVisible();
    await expect(body).toContainText(bucketRef);
    await expect(body).toContainText('prod');

    // Nothing has deployed yet: the stack is materialised for review only.
    await expect(page.locator('.det-h')).toContainText('REVIEW_IN_PROGRESS');

    // Execute deploys and lands on the events tab.
    await body.getByRole('button', { name: 'Execute' }).click();
    await confirmDialog('accept');
    await page.waitForURL(/tab=events/);
    await expect(page.locator('.det-b')).toContainText('CREATE_COMPLETE');

    // Update tab: current parameter values prefilled; a UsePreviousTemplate
    // update changes only what was typed.
    await page.goto(`cfn/${stack}?tab=update`);
    const current = page.locator('#cfn-params input[name="param:Env"]');
    await expect(current).toHaveValue('prod');
    await page.locator('.opt-row:has-text("Use previous template") .switch').click();
    await current.fill('prod2');
    await page.getByRole('button', { name: 'Update stack' }).click();
    await page.waitForURL(/tab=events/);
    await expect(page.locator('#flashbar')).toContainText('Stack update deployed');
    await page.goto(`cfn/${stack}`);
    await expect(page.locator('.det-b')).toContainText('prod2');

    // Resource drill-down: the logical id opens DescribeStackResource detail.
    await page.locator('.det-b td a[title="Resource detail"]').click();
    await expect(page.locator('#cfn-res-out .cfn-res-detail')).toContainText('Status reason');
  });

  test('exports registry shows the value and its importers', async ({ page, uniqueName }) => {
    const exporter = uniqueName('e2e-cfn-exp');
    const importer = uniqueName('e2e-cfn-imp');
    const exportName = `${exporter}-arn`;
    await postForm(page.request, 'cfn/create', {
      name: exporter,
      template: JSON.stringify({
        Resources: { Q: { Type: 'AWS::SQS::Queue', Properties: { QueueName: `${exporter}-q` } } },
        Outputs: { Arn: { Value: { 'Fn::GetAtt': ['Q', 'Arn'] }, Export: { Name: exportName } } },
      }),
    });
    await postForm(page.request, 'cfn/create', {
      name: importer,
      template: JSON.stringify({
        Resources: {
          P: {
            Type: 'AWS::SSM::Parameter',
            Properties: { Name: `/${importer}`, Type: 'String', Value: { 'Fn::ImportValue': exportName } },
          },
        },
      }),
    });

    await page.goto('cfn');
    const row = page.locator('tr', { hasText: exportName });
    await expect(row).toContainText(exporter);
    // The importer chip is the blast radius, resolved via ListImports.
    await expect(row.locator('a.badge', { hasText: importer })).toBeVisible();
  });

  test('a nested stack deploys from a staged child and lists under its parent', async ({ page, uniqueName }) => {
    const parent = uniqueName('e2e-cfn-nest');
    const bucket = uniqueName('e2e-cfn-staging');
    await createBucket(page.request, bucket);
    // Stage the child the way CDK does: an object in the local S3.
    const child = JSON.stringify({
      Parameters: { Prefix: { Type: 'String' } },
      Resources: { Work: { Type: 'AWS::SQS::Queue', Properties: { QueueName: { 'Fn::Sub': '${Prefix}-work' } } } },
      Outputs: { WorkArn: { Value: { 'Fn::GetAtt': ['Work', 'Arn'] } } },
    });
    const put = await page.request.put(`http://127.0.0.1:14566/${bucket}/child.json`, { data: child });
    expect(put.ok()).toBeTruthy();
    await postForm(page.request, 'cfn/create', {
      name: parent,
      template: JSON.stringify({
        Resources: {
          Queues: {
            Type: 'AWS::CloudFormation::Stack',
            Properties: {
              TemplateURL: `https://s3.us-east-1.amazonaws.com/${bucket}/child.json`,
              Parameters: { Prefix: parent },
            },
          },
        },
        Outputs: { Work: { Value: { 'Fn::GetAtt': ['Queues', 'Outputs.WorkArn'] } } },
      }),
    });
    await page.goto('cfn');
    const childRow = page.locator('.li', { hasText: `${parent}-Queues` });
    await expect(childRow).toContainText(`nested under ${parent}`);
    await page.goto(`cfn/${parent}`);
    await expect(page.locator('.detail')).toContainText(`${parent}-work`);
  });
});
