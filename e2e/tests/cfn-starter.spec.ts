import { test, expect } from '../fixtures/console';
import { createQueue, createTable } from '../fixtures/api';

// "Start from what's running" turns the emptiest box in the console into one
// you edit. `doze-aws export` could always write the running stack as a
// template; this is the same code reached from a browser.
test.describe('CloudFormation starter template', () => {
  test('fills the editor with the running stack, and asks before replacing', async ({
    page,
    request,
    uniqueName,
  }) => {
    const queue = await createQueue(request, uniqueName('e2e-cfn-q'));
    const table = await createTable(request, uniqueName('e2e-cfn-t'));

    await page.goto('cfn/create');
    // Located by class, not by name: the label changes to "Replace what's there?"
    // when armed, and a name-based locator would stop matching its own button.
    const btn = page.locator('button.lbl-btn');
    await btn.click();

    // The editor now holds a template naming the resources that exist.
    await expect
      .poll(async () =>
        page.evaluate(() => {
          const ta = document.querySelector('textarea[name="template"]') as any;
          return ta.__cm ? ta.__cm.getValue() : ta.value;
        })
      )
      .toContain('AWSTemplateFormatVersion');

    const yaml: string = await page.evaluate(() => {
      const ta = document.querySelector('textarea[name="template"]') as any;
      return ta.__cm ? ta.__cm.getValue() : ta.value;
    });
    expect(yaml).toContain(queue);
    expect(yaml).toContain(table);
    expect(yaml).toContain('AWS::SQS::Queue');
    expect(yaml).toContain('AWS::DynamoDB::Table');

    // CodeMirror's buffer must have reached the textarea, or the form would
    // post an empty template however full the editor looks.
    const posted = await page.evaluate(
      () => (document.querySelector('textarea[name="template"]') as HTMLTextAreaElement).value
    );
    expect(posted).toContain('AWSTemplateFormatVersion');

    // A second press does not silently discard what is there: it arms first.
    await btn.click();
    await expect(btn).toHaveText(/Replace what/);
  });

  test('the exported template is one the console will actually deploy', async ({
    page,
    request,
    uniqueName,
  }) => {
    await createQueue(request, uniqueName('e2e-cfn-rt'));

    await page.goto('cfn/create');
    await page.locator('button.lbl-btn').click();
    await expect
      .poll(async () =>
        page.evaluate(() => {
          const ta = document.querySelector('textarea[name="template"]') as any;
          return ta.__cm ? ta.__cm.getValue() : ta.value;
        })
      )
      .toContain('AWSTemplateFormatVersion');

    // Validate is the console's own check, so this asserts the export is
    // round-trippable through the very page it seeds rather than just that it
    // looks like YAML.
    await page.getByRole('button', { name: /Validate/ }).click();
    const out = page.locator('#cfn-validate-out');
    await expect(out).toBeVisible();
    await expect(out).not.toContainText(/error|invalid|failed/i);
  });
});
