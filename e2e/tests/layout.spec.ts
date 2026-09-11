import { test, expect } from '../fixtures/console';
import { createBucket, createQueue, createTable, createTopic, createBus, postForm } from '../fixtures/api';

// Layout faults that no assertion about content would ever catch, measured
// rather than eyeballed. Both of these shipped: a stack page whose Parameters
// panel sat flush against the table above it so the two read as one block, and
// an IAM page whose access log grew to 4,939px and pushed the policy generator
// and the simulator five screens below the fold.
//
// Every surface is walked, not the two that were reported — a spacing rule and
// a height cap are the kind of thing that is right on the page someone looked
// at and wrong on the next one.

const SURFACES = [
  '', 'traffic', 'connect', 'deck',
  's3', 'ddb', 'sqs', 'sns', 'eb', 'eb/destinations', 'lambda', 'kinesis',
  'sfn', 'apigw', 'apigw-keys', 'logs', 'cw', 'kms', 'sm', 'ssm', 'iam', 'cfn',
  's3/create', 'sqs/create', 'ddb/create', 'sns/create', 'cfn/create', 'sfn/create',
];

test.describe('layout', () => {
  test('no two stacked blocks are flush against each other', async ({ page, request, uniqueName }) => {
    // A little content, so the pages have real blocks rather than empty states.
    await createBucket(request, uniqueName('e2e-layout'));
    await createQueue(request, uniqueName('e2e-layout'));
    await createTable(request, uniqueName('e2e-layout'));
    await createTopic(request, uniqueName('e2e-layout'));
    await createBus(request, uniqueName('e2e-layout'));

    // The reported fault was on a stack page, and it needed a stack with BOTH
    // parameters and outputs to show: that is what renders the split which was
    // landing flush against the resources table above it.
    const stack = uniqueName('e2e-layout-stack');
    await postForm(request, 'cfn/create', {
      name: stack,
      template: [
        'AWSTemplateFormatVersion: "2010-09-09"',
        'Parameters:',
        '  Environment:',
        '    Type: String',
        '    Default: prod',
        'Resources:',
        '  Q:',
        '    Type: AWS::SQS::Queue',
        '    Properties:',
        `      QueueName: ${stack}-q`,
        'Outputs:',
        '  QueueName:',
        '    Value: !Ref Q',
      ].join('\n'),
    });
    SURFACES.push(`cfn/${stack}`);

    await page.setViewportSize({ width: 1500, height: 1000 });
    const touching: string[] = [];

    for (const surface of SURFACES) {
      const res = await page.goto(surface).catch(() => null);
      if (!res || !res.ok()) continue;
      await page.waitForTimeout(150);

      const hits = await page.evaluate(() => {
        const CARD = '.panel, .split, .table-wrap, .form-sec, .eb-trace, .code-out';
        const out: string[] = [];
        for (const el of Array.from(document.querySelectorAll(CARD))) {
          // Walk back past empty slots: an htmx target with nothing in it yet
          // is still an element sibling, so a `+` rule in CSS stops matching
          // across it — which is exactly how the stack page's gap went missing.
          let prev = el.previousElementSibling;
          while (prev && prev.getBoundingClientRect().height < 4) prev = prev.previousElementSibling;
          if (!prev) continue;
          const a = prev.getBoundingClientRect();
          const b = el.getBoundingClientRect();
          if (b.height < 4) continue;
          if (Math.abs(a.left - b.left) > 40) continue; // side by side, not stacked
          const gap = Math.round(b.top - a.bottom);
          if (gap < 0 || gap > 3) continue;
          const name = (e: Element) =>
            `${e.tagName.toLowerCase()}${e.id ? '#' + e.id : ''}.${String(e.className).split(' ').filter(Boolean).slice(0, 2).join('.')}`;
          out.push(`${name(prev)} → ${name(el)} (${gap}px)`);
        }
        return [...new Set(out)];
      });

      for (const h of hits) touching.push(`${surface || '(home)'}: ${h}`);
    }

    expect(touching, `blocks with no gap between them:\n${touching.join('\n')}`).toEqual([]);
  });

  // The IAM access log records every API call the stack serves, so it is the
  // one table with no ceiling at all: after a busy session it was 142 rows and
  // 4,939px, and the policy generator and simulator beneath it started 5,190px
  // down. Asserted structurally rather than by height, because reproducing a
  // few hundred rows here would only prove the fixture can make noise.
  test('the IAM access log is capped rather than unbounded', async ({ page }) => {
    await page.goto('iam');
    const log = page.locator('.table-wrap').first();
    await expect(log).toBeVisible();
    await expect(
      log,
      'the access log grows with every API call the stack serves; uncapped it pushes ' +
        'the policy generator and the simulator below the fold'
    ).toHaveClass(/capped/);

    const scrolls = await log.evaluate((el) => getComputedStyle(el).overflowY);
    expect(scrolls, 'a capped table has to scroll inside itself').toMatch(/auto|scroll/);
  });

  test('a growing table does not push a page’s controls past the fold', async ({ page }) => {
    await page.setViewportSize({ width: 1500, height: 950 });
    const buried: string[] = [];

    for (const surface of SURFACES) {
      const res = await page.goto(surface).catch(() => null);
      if (!res || !res.ok()) continue;
      await page.waitForTimeout(150);

      const m = await page.evaluate(() => {
        const vh = window.innerHeight;
        let tallest = 0;
        for (const t of Array.from(document.querySelectorAll('.table-wrap'))) {
          tallest = Math.max(tallest, Math.round(t.getBoundingClientRect().height));
        }
        return { tallest, screens: +(tallest / vh).toFixed(1) };
      });

      // No single table should be taller than about two screens. Past that it
      // is not a table any more, it is a wall, and whatever sits under it is
      // effectively gone. Cap it with .table-wrap.capped so it scrolls inside
      // itself instead.
      if (m.tallest > 1900) buried.push(`${surface || '(home)'}: a table is ${m.tallest}px (${m.screens} screens)`);
    }

    expect(buried, `tables tall enough to hide what follows them:\n${buried.join('\n')}`).toEqual([]);
  });
});
