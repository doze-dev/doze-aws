import { test, expect } from '../fixtures/console';
import { postForm } from '../fixtures/api';

// CloudWatch in a real browser: publish a metric through the JavaScript v3
// SDK — which resolves to AWS JSON 1.0, the AWS CLI's wire — then find it in
// the console's browser, chart it, alarm on it, and flip the alarm by hand.
//
// The SDK half matters as much as the console half: this is the only place in
// the suite where CloudWatch is exercised by a client that picked its own
// protocol rather than one doze-aws's own tests chose.

test.describe('cloudwatch metrics and alarms', () => {
  test('publish a metric, chart it, alarm on it, and set the state', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    const namespace = uniqueName('E2E');
    const alarmName = uniqueName('e2e-alarm');

    // Published through the gateway on JSON 1.0. Two dimension sets of the
    // same metric name, because keeping those distinct is the thing an alarm
    // cannot tolerate getting wrong.
    for (const [stage, value] of [
      ['prod', 9],
      ['dev', 2],
    ] as const) {
      const res = await request.post('/', {
        headers: {
          'content-type': 'application/x-amz-json-1.0',
          'x-amz-target': 'GraniteServiceVersion20100801.PutMetricData',
        },
        data: {
          Namespace: namespace,
          MetricData: [
            {
              MetricName: 'Checkouts',
              Value: value,
              Unit: 'Count',
              Dimensions: [{ Name: 'Stage', Value: stage }],
            },
          ],
        },
      });
      expect(res.status(), await res.text()).toBe(200);
    }

    // The browser lists both series under the namespace, as two rows.
    await page.goto('cw');
    const nsTable = page.locator('.cw-ns', { hasText: namespace });
    await expect(nsTable.locator('tbody tr')).toHaveCount(2);
    await expect(nsTable).toContainText('Stage=prod');
    await expect(nsTable).toContainText('Stage=dev');

    // The chart. The sparkline is server-rendered, so a visible line means
    // real datapoints reached the template.
    await nsTable.locator('tbody tr', { hasText: 'Stage=prod' }).getByRole('link').click();
    await page.waitForURL(/\/cw\/metric/);
    // One observation per series here, so the chart is dots rather than a
    // line — a polyline through a single point has zero area and draws
    // nothing, which is why the dots exist.
    await expect(page.locator('circle.cw-dot')).toHaveCount(1);
    await expect(page.getByText('Nothing watches this metric')).toBeVisible();

    // Changing the statistic re-renders the chart rather than reloading.
    await page.locator('select[name="stat"]').selectOption('Maximum');
    await expect(page.locator('.panel-h .badge.type').first()).toHaveText('Maximum');
    await expect(page.locator('circle.cw-dot')).toHaveCount(1);

    // An alarm on it, through the create form the metric page links to.
    await page.getByRole('link', { name: 'Create an alarm on it' }).click();
    await page.waitForURL(/\/cw\/create/);
    await page.locator('input[name="name"]').fill(alarmName);
    await page.locator('select[name="operator"]').selectOption('LessThanThreshold');
    await page.locator('input[name="threshold"]').fill('100');
    await page.getByRole('button', { name: 'Create alarm' }).click();
    await page.waitForURL(new RegExp(`/cw/alarm/${alarmName}`));
    await expect(page.locator('#flashbar')).toContainText(alarmName);

    // A fresh alarm reads INSUFFICIENT_DATA: the evaluator judges completed
    // periods only, so nothing has been decided yet.
    await expect(page.locator('.det-title .badge').first()).toHaveText('INSUFFICIENT_DATA');

    // Setting the state by hand is what makes the alarm's notification
    // testable before its metric has ever breached.
    await page.locator('select[name="state"]').selectOption('ALARM');
    await page.locator('input[name="reason"]').fill('e2e');
    await page.getByRole('button', { name: 'Set state' }).click();
    await page.waitForURL(new RegExp(`/cw/alarm/${alarmName}`));
    await expect(page.locator('.det-title .badge').first()).toHaveText('ALARM');

    // And the flip is in the history, which is the pane a person reads to
    // find out why an alarm is where it is.
    await page.goto(`cw/alarm/${alarmName}?tab=history`);
    await expect(page.locator('table.tbl')).toContainText('StateUpdate');
    await expect(page.locator('table.tbl')).toContainText('e2e');

    // The metric's own page now lists the alarm watching it.
    await page.goto('cw');
    await page.locator('.cw-ns', { hasText: namespace })
      .locator('tbody tr', { hasText: 'Stage=prod' }).getByRole('link').click();
    await page.waitForURL(/\/cw\/metric/);
    await expect(page.locator('table.tbl')).toContainText(alarmName);

    // Delete it, and the list pane loses it.
    await page.goto(`cw/alarm/${alarmName}`);
    await page.getByRole('button', { name: 'Delete' }).click();
    await confirmDialog('accept');
    await page.waitForURL(/\/cw$/);
    await expect(page.locator('.listpane')).not.toContainText(alarmName);
  });

  // A CloudFormation template with an alarm deploys and the alarm exists —
  // the shape a CDK app emits, and the one that used to transpile into
  // nothing because AWS::CloudWatch::Alarm was an ignored type.
  test('a template with an alarm deploys it', async ({ page, request, uniqueName }) => {
    const stackName = uniqueName('cwstack');
    const alarmName = uniqueName('tmpl-alarm');
    const template = JSON.stringify({
      AWSTemplateFormatVersion: '2010-09-09',
      Resources: {
        Warn: {
          Type: 'AWS::CloudWatch::Alarm',
          Properties: {
            AlarmName: alarmName,
            Namespace: 'Tmpl',
            MetricName: 'Errors',
            Statistic: 'Sum',
            Period: 60,
            EvaluationPeriods: 1,
            Threshold: 1,
            ComparisonOperator: 'GreaterThanOrEqualToThreshold',
          },
        },
      },
    });
    const res = await postForm(request, 'cfn/create', { name: stackName, template });
    expect(res.status(), await res.text()).toBeLessThan(400);

    await page.goto('cw');
    await expect(page.locator('.listpane')).toContainText(alarmName);
    await page.goto(`cw/alarm/${alarmName}`);
    await expect(page.locator('.factstrip')).toContainText('Tmpl/Errors');
  });
});
