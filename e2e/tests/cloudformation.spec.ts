import { test, expect } from '../fixtures/console';
import { postForm, createBucket } from '../fixtures/api';
import { ORIGIN } from '../playwright.config';

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
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('Stack update deployed');
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
    const put = await page.request.put(`${ORIGIN}/${bucket}/child.json`, { data: child });
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

// ---- Route-coverage pass: deleting a change set, and deleting a stack. ----

test.describe('CloudFormation deletes', () => {
  const queueStack = (q: string) =>
    JSON.stringify({ Resources: { Q: { Type: 'AWS::SQS::Queue', Properties: { QueueName: q } } } });

  test('a change set is discarded from the Change sets tab; the stack is untouched', async ({
    page,
    uniqueName,
    confirmDialog,
  }) => {
    const stack = uniqueName('e2e-cfn-csdel');
    const cs = 'review-' + stack.split('-').pop();
    // Arrange: a stack under review, via the create form's own POST.
    await postForm(page.request, 'cfn/create', {
      name: stack,
      template: queueStack(`${stack}-q`),
      review: true,
      changeset: cs,
    });

    await page.goto(`cfn/${stack}?tab=changesets`);
    const row = page.locator('.det-b tr', { hasText: cs });
    await expect(row).toBeVisible();
    await row.getByRole('button', { name: `Delete change set ${cs}` }).click();
    await confirmDialog('accept');

    await page.waitForURL(/tab=changesets/);
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`Change set ${cs} deleted`);
    await expect(page.locator('.det-b tr', { hasText: cs })).toHaveCount(0);
    await expect(page.locator('.det-b .empty')).toContainText('No change sets');
    await expect(page.locator('.det-title')).toContainText(stack);
  });

  test('a stack is deleted from its page, and what it created goes with it', async ({
    page,
    uniqueName,
    confirmDialog,
  }) => {
    const stack = uniqueName('e2e-cfn-del');
    const queue = `${stack}-q`;
    await postForm(page.request, 'cfn/create', { name: stack, template: queueStack(queue) });

    await page.goto(`cfn/${stack}`);
    await expect(page.locator('.det-h .chip').first()).toContainText('CREATE_COMPLETE');
    await page.locator('.acts').getByRole('button', { name: 'Delete' }).click();
    await confirmDialog('accept');

    await page.waitForURL(/\/cfn(\?|$)/);
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`Deleted ${stack}`);
    // The home page keeps the record, as CloudFormation does, under its own heading.
    const deleted = page.locator('.det-b .table-wrap', { hasText: 'DELETE_COMPLETE' }).locator('tr', { hasText: stack });
    await expect(deleted).toContainText('DELETE_COMPLETE');

    // AWS-side: the stack's queue was deleted with it.
    await page.goto('sqs');
    await expect(page.locator('.li', { hasText: queue })).toHaveCount(0);
  });

  // Regression (fixed in 1.0): a deleted stack stays in the CloudFormation list pane (with an
  // output count of 0), right beside the home page's "Deleted stacks — the
  // record the stack list no longer shows". The list comes from DescribeStacks
  // with no StackName (console/client_cfn.go:140), and the emulator's
  // hDescribeStacks (cloudformation/stacks.go:655) returns DELETE_COMPLETE
  // records there; AWS omits deleted stacks from an unnamed DescribeStacks.
  test('a deleted stack leaves the list pane', async ({ page, uniqueName, confirmDialog }) => {
    const stack = uniqueName('e2e-cfn-dellp');
    await postForm(page.request, 'cfn/create', { name: stack, template: queueStack(`${stack}-q`) });
    await page.goto(`cfn/${stack}`);
    await page.locator('.acts').getByRole('button', { name: 'Delete' }).click();
    await confirmDialog('accept');
    await page.waitForURL(/\/cfn(\?|$)/);
    await expect(page.locator('.li', { hasText: stack })).toHaveCount(0);
  });
});
