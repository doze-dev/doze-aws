import { test, expect } from '../fixtures/console';
import { postForm } from '../fixtures/api';

// IAM console coverage for the burn-down surfaces: groups (a whole subsystem
// that had no UI), instance profiles, policy versioning, the draft-policy
// simulator, key lifecycle, and the account page.

test.describe('IAM console', () => {
  test('groups: create, membership from both sides, attach, rename', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const user = uniqueName('e2e-iam-user');
    const group = uniqueName('e2e-iam-group');
    await postForm(request, 'iam/create', { kind: 'user', name: user });

    await page.goto('iam/create?kind=group');
    await page.locator('.seg button', { hasText: 'Group' }).click();
    await page.locator('input[name="name"]').fill(group);
    await page.getByRole('button', { name: /Create/ }).click();
    await page.waitForURL(new RegExp(`/iam/group/${group}$`));

    // Membership from the group's side…
    await page.locator('select[name="user"]').selectOption(user);
    await page.locator('form:has(select[name="user"])').getByRole('button', { name: 'Add' }).click();
    await expect(page.locator('#flashbar')).toContainText(`${user} added to ${group}`);
    await expect(page.locator('.badge', { hasText: user })).toBeVisible();

    // …reflected on the user's side (ListGroupsForUser).
    await page.goto(`iam/user/${user}`);
    await expect(page.locator('.chips a.badge', { hasText: group })).toBeVisible();

    // Attach a policy to the group; rename it and land on the new page.
    await page.goto(`iam/group/${group}`);
    const policyArn = await page
      .locator('select[name="arn"] option')
      .first()
      .getAttribute('value');
    await page.locator('select[name="arn"]').selectOption({ index: 0 });
    await page.locator('form:has(select[name="arn"])').getByRole('button', { name: 'Attach' }).click();
    // This used to be toContainText(''), which is true of any content at all —
    // an empty flashbar, an error, a page that never attached anything. The
    // attached policy has to show up in the Attached policies table.
    await expect(page.locator('.tbl')).not.toContainText('No managed policies attached');
    await expect(page.locator('.tbl td.mono.dim', { hasText: policyArn! })).toBeVisible();
    await page.locator('input[name="new"]').fill(`${group}-renamed`);
    await page.getByRole('button', { name: 'Rename' }).click();
    await page.locator('#confirm-yes').click();
    await page.waitForURL(new RegExp(`/iam/group/${group}-renamed$`));
  });

  test('policy versions: publish, default, rollback; draft simulator', async ({
    page,
    request,
    uniqueName,
  }) => {
    const policy = uniqueName('e2e-iam-pol');
    const doc = (action: string) =>
      JSON.stringify({ Version: '2012-10-17', Statement: [{ Effect: 'Allow', Action: action, Resource: '*' }] });
    await postForm(request, 'iam/create', { kind: 'policy', name: policy, document: doc('s3:GetObject') });
    const arn = `arn:aws:iam::000000000000:policy/${policy}`;

    await page.goto(`iam/policy?arn=${arn}`);
    await page.getByRole('button', { name: 'Edit as new version' }).click();
    await page.evaluate((d) => window.dozeEditor.set('form[action=""] textarea[name="document"], form textarea[name="document"]', d), doc('s3:*'));
    await page.getByRole('button', { name: 'Publish version' }).click();
    await expect(page.locator('#flashbar')).toContainText('New version published');
    // v2 is default (the checkbox defaults on); v1 offers rollback.
    const v1row = page.locator('tr', { hasText: 'v1' });
    await v1row.getByRole('button', { name: 'Make default' }).click();
    await page.locator('#confirm-yes').click();
    await expect(page.locator('#flashbar')).toContainText('v1 is now the default');

    // The draft simulator: check a policy that exists nowhere.
    await page.goto('iam');
    await page.locator('.seg button', { hasText: 'Draft policy' }).click();
    await page.evaluate((d) => window.dozeEditor.set('form[hx-post$="/iam/simulate"] textarea[name="document"]', d), doc('sqs:SendMessage'));
    await page.locator('input[name="actions"]').fill('sqs:SendMessage s3:GetObject');
    await page.getByRole('button', { name: 'Evaluate' }).click();
    await expect(page.locator('#iam-sim .chip', { hasText: 'allowed' })).toBeVisible();
    await expect(page.locator('#iam-sim .chip.bad', { hasText: 'implicitDeny' })).toBeVisible();
  });

  test('access keys: mint, deactivate, re-activate; account alias', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const user = uniqueName('e2e-iam-key');
    await postForm(request, 'iam/create', { kind: 'user', name: user });
    await page.goto(`iam/user/${user}`);
    await page.getByRole('button', { name: 'New access key' }).click();
    // Consume the mint flash (it carries the one-time secret) so the next
    // toast assert sees a fresh entry.
    const mintFlash = await waitForToast();
    expect(mintFlash).toContain('not retrievable again');
    await expect(page.locator('tbody .chip', { hasText: 'Active' })).toBeVisible();
    await expect(page.locator('tbody td', { hasText: 'never' })).toBeVisible();

    await page.locator('button[title^="Deactivate"]').click();
    const toast = await waitForToast();
    expect(toast).toContain('deactivated');
    await expect(page.locator('tbody .chip.bad', { hasText: 'Inactive' })).toBeVisible();
    await page.locator('button[title="Re-activate"]').click();
    await expect(page.locator('tbody .chip:not(.bad)', { hasText: 'Active' })).toBeVisible();

    // Account page: summary strip renders, alias round-trips.
    const alias = uniqueName('e2e-alias');
    await page.goto('iam/account');
    await expect(page.locator('.factstrip .fact', { hasText: 'Users' })).toBeVisible();
    await page.locator('input[name="alias"]').fill(alias);
    await page.getByRole('button', { name: 'Set' }).click();
    await expect(page.locator('#flashbar')).toContainText(`Account alias set to ${alias}`);
    await expect(page.locator('.badge', { hasText: alias })).toBeVisible();
  });
});

// STS: the credentials page — the last service on the burn-down.
test.describe('STS credentials', () => {
  test('assume a role, get the export line; key lookup answers', async ({ page, request, uniqueName }) => {
    const role = uniqueName('e2e-sts-role');
    await postForm(request, 'iam/create', { kind: 'role', name: role });

    await page.goto('iam/sts');
    await page.locator('select[name="role"]').selectOption(role);
    await page.getByRole('button', { name: 'Mint credentials' }).click();
    const out = page.locator('#sts-out');
    await expect(out.locator('.panel', { hasText: 'Minted' })).toBeVisible();
    await expect(out).toContainText(`assumed-role/${role}`);
    await expect(out).toContainText('export AWS_ACCESS_KEY_ID');

    await page.locator('input[name="id"]').fill('AKIAEXAMPLE1234567890');
    await page.getByRole('button', { name: 'Look up' }).click();
    await expect(page.locator('#keyinfo-out')).toContainText('000000000000');
  });
});

// The policy builder: rows over the JSON textarea, with the fidelity rules
// the plan demanded as tests — single-element lists stay bare strings, a bare
// Statement object stays bare, and toggling Builder↔JSON must be a byte-level
// no-op on a document the rows can represent.
test.describe('policy builder', () => {
  const tricky = {
    Version: '2012-10-17',
    Statement: {
      Effect: 'Allow',
      Action: 's3:GetObject',
      Resource: ['arn:aws:s3:::a/*', 'arn:aws:s3:::b/*'],
      Condition: { 'ForAnyValue:StringLikeIfExists': { 'aws:SourceIp': '10.*' } },
    },
  };

  test('round-trips the trap shapes byte-identically', async ({ page }) => {
    await page.goto('iam/create?kind=policy');
    await page.locator('.seg button', { hasText: 'Policy' }).click();
    const pb = page.locator('.field:has(textarea[name="document"]) .pb');
    await pb.locator('.ws-seg a', { hasText: 'JSON' }).click();
    await page.evaluate((d) => {
      const ta = document.querySelector('.field textarea[name="document"]');
      window.dozeEditor.set(ta, JSON.stringify(d, null, 2));
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    }, tricky);
    const before = await page.evaluate(() =>
      document.querySelector('.field textarea[name="document"]').__cm.getValue());
    await pb.locator('.ws-seg a', { hasText: 'Builder' }).click();
    await expect(pb.locator('.pb-stmt')).toHaveCount(1);
    await pb.locator('.ws-seg a', { hasText: 'JSON' }).click();
    const after = await page.evaluate(() =>
      document.querySelector('.field textarea[name="document"]').__cm.getValue());
    // Semantically identical is not enough: a builder that re-shapes a
    // document the user never edited writes noise into their diff.
    expect(JSON.parse(after)).toEqual(tricky);
    expect(JSON.parse(after).Statement.Action).toBe('s3:GetObject'); // still bare
    expect(Array.isArray(JSON.parse(after).Statement)).toBe(false); // still a bare object
    expect(after).toBe(before);
  });

  test('a policy built purely from rows creates and simulates', async ({ page, uniqueName }) => {
    const name = uniqueName('e2e-pb');
    await page.goto('iam/create?kind=policy');
    await page.locator('.seg button', { hasText: 'Policy' }).click();
    const pb = page.locator('.field:has(textarea[name="document"]) .pb');
    await expect(pb.locator('.pb-stmt')).toBeVisible();

    // Add an action through the chip input; check the draft inline.
    const add = pb.locator('.pb-add[list="iam-actions"]').first();
    await add.fill('kinesis:PutRecord');
    await add.press('Enter');
    await pb.locator('.pb-check input').fill('kinesis:PutRecord iam:DeleteUser');
    await pb.locator('.pb-check button').click();
    const out = page.locator('#pb-out-create-pol');
    await expect(out.locator('.chip', { hasText: 'kinesis:PutRecord' })).toContainText('allowed');
    await expect(out.locator('.chip.bad', { hasText: 'iam:DeleteUser' })).toContainText('implicitDeny');

    // An unknown condition operator hides the builder instead of flattening.
    await pb.locator('.ws-seg a', { hasText: 'JSON' }).click();
    await page.evaluate(() => {
      const ta = document.querySelector('.field textarea[name="document"]');
      window.dozeEditor.set(ta, JSON.stringify({ Statement: [{ Effect: 'Allow', Action: 's3:*', Resource: '*', Condition: { NoSuchOp: { 'aws:username': 'x' } } }] }));
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await expect(pb.locator('.kv-note', { hasText: 'Builder unavailable' })).toBeVisible();

    // Back to a representable doc and submit — the textarea is what posts.
    await page.evaluate(() => {
      const ta = document.querySelector('.field textarea[name="document"]');
      window.dozeEditor.set(ta, JSON.stringify({ Version: '2012-10-17', Statement: [{ Effect: 'Allow', Action: 'kinesis:PutRecord', Resource: '*' }] }));
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await page.locator('input[name="name"]').fill(name);
    await page.getByRole('button', { name: 'Create policy' }).click();
    await expect(page.locator('#flashbar')).toContainText(`Created ${name}`);
  });
});

// data-err-slot: a refused document's error lands INSIDE the builder, next to
// what caused it — not below the fold at the form's end — and clears on the
// next attempt instead of stacking.
test('a refused policy document errors next to the builder and clears on retry', async ({
  page,
  uniqueName,
}) => {
  await page.goto('iam/create?kind=policy');
  await page.locator('.seg button', { hasText: 'Policy' }).click();
  const pb = page.locator('.field:has(textarea[name="document"]) .pb');
  await expect(pb.locator('.pb-stmt')).toBeVisible();
  await pb.locator('.ws-seg a', { hasText: 'JSON' }).click();
  await page.evaluate(() => {
    const ta = document.querySelector('.field textarea[name="document"]');
    window.dozeEditor.set(ta, '{"Version":"2012-10-17","Statement":[]}');
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  });
  const name = uniqueName('e2e-errslot');
  await page.locator('input[name="name"]').fill(name);
  await page.getByRole('button', { name: 'Create policy' }).click();
  await expect(pb.locator('[data-doze-err]')).toContainText('policy document has no Statement');

  await page.evaluate(() => {
    const ta = document.querySelector('.field textarea[name="document"]');
    window.dozeEditor.set(ta, JSON.stringify({ Version: '2012-10-17', Statement: [{ Effect: 'Allow', Action: 's3:*', Resource: '*' }] }));
  });
  await page.getByRole('button', { name: 'Create policy' }).click();
  await expect(page.locator('#flashbar')).toContainText(`Created ${name}`);
  await expect(page.locator('[data-doze-err]')).toHaveCount(0);
});
