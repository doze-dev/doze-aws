import { test, expect } from '../fixtures/console';
import { postForm } from '../fixtures/api';
import { ORIGIN } from '../playwright.config';

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
    await page.locator('form:has(select[name="user"])').getByRole('button', { name: 'Add', exact: true }).click();
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`${user} added to ${group}`);
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
    await page.locator('form:has(select[name="arn"])').getByRole('button', { name: 'Attach', exact: true }).click();
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
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('New version published');
    // v2 is default (the checkbox defaults on); v1 offers rollback.
    const v1row = page.locator('tr', { hasText: 'v1' });
    await v1row.getByRole('button', { name: 'Make default' }).click();
    await page.locator('#confirm-yes').click();
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('v1 is now the default');

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
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`Account alias set to ${alias}`);
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
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`Created ${name}`);
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
  await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`Created ${name}`);
  await expect(page.locator('[data-doze-err]')).toHaveCount(0);
});

// ---------------------------------------------------------------------------
// Remaining IAM mutations, each driven from the control a user would click:
// role trust/settings/inline policies/detach/delete, user group-join/key
// delete/delete, policy version + policy delete, instance-profile role and
// delete, the account export, and the access-log policy generator.

const allow = (action: string, sid?: string) =>
  JSON.stringify({
    Version: '2012-10-17',
    Statement: [{ ...(sid ? { Sid: sid } : {}), Effect: 'Allow', Action: action, Resource: '*' }],
  });

/** Writes a policy document into the builder textarea under `scope`, the way
 *  the builder's own JSON view does (CodeMirror + input event so the rows
 *  stay in step with what posts). */
async function setPolicyDoc(page: import('@playwright/test').Page, scope: string, doc: string) {
  const sel = `${scope} textarea[name="document"]`;
  await page.waitForFunction((s) => !!(document.querySelector(s) as any)?.__cm, sel);
  await page.evaluate(
    ([s, d]) => {
      const ta = document.querySelector(s)!;
      (window as any).dozeEditor.set(ta, d);
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    },
    [sel, doc]
  );
}

test.describe('IAM role page', () => {
  test('trust policy, role settings, inline policy lifecycle, detach, delete', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    const role = uniqueName('e2e-iam-role');
    await postForm(request, 'iam/create', { kind: 'role', name: role });
    await postForm(request, `iam/role/${role}/attach`, {
      arn: 'arn:aws:iam::aws:policy/ReadOnlyAccess',
    });
    await page.goto(`iam/role/${role}`);

    // --- Trust policy: Edit -> new document -> Save ---
    const trustPanel = page.locator('.panel', { has: page.locator('form[hx-post$="/trust"]') });
    await trustPanel.locator('.panel-h').getByRole('button', { name: 'Edit' }).click();
    const trust = JSON.stringify({
      Version: '2012-10-17',
      Statement: [
        { Effect: 'Allow', Principal: { Service: 'ecs-tasks.amazonaws.com' }, Action: 'sts:AssumeRole' },
      ],
    });
    await setPolicyDoc(page, 'form[hx-post$="/trust"]', trust);
    await trustPanel.getByRole('button', { name: 'Save trust policy' }).click();
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('Trust policy updated');
    await expect(
      page.locator('.panel', { has: page.locator('form[hx-post$="/trust"]') }).locator('.code-out pre')
    ).toContainText('ecs-tasks.amazonaws.com');

    // --- Role settings: description + max session ---
    const meta = page.locator('form[hx-post$="/meta"]');
    await meta.locator('input[name="description"]').fill('e2e role description');
    await meta.locator('input[name="session"]').fill('7200');
    await meta.getByRole('button', { name: 'Save' }).click();
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('Role settings saved');
    await page.reload();
    await expect(page.locator('form[hx-post$="/meta"] input[name="description"]')).toHaveValue(
      'e2e role description'
    );
    await expect(page.locator('form[hx-post$="/meta"] input[name="session"]')).toHaveValue('7200');

    // --- Inline policy: add ---
    const inlineName = uniqueName('inline');
    const addPanel = page.locator('.panel', { has: page.locator('h2', { hasText: 'Add an inline policy' }) });
    await addPanel.locator('.panel-h').getByRole('button', { name: 'Add', exact: true }).click();
    await addPanel.locator('input[name="policy"]').fill(inlineName);
    await setPolicyDoc(page, `form[hx-post$="/iam/role/${role}/inline"]:has(input[name="policy"]:not([type=hidden]))`, allow('sqs:SendMessage'));
    await addPanel.locator('form').getByRole('button', { name: 'Add', exact: true }).click();
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`Saved ${inlineName}`);
    const inlinePanel = page.locator('.panel', { has: page.locator('h2', { hasText: inlineName }) });
    await expect(inlinePanel.locator('.code-out pre')).toContainText('sqs:SendMessage');

    // --- Inline policy: edit in place ---
    await inlinePanel.locator('.panel-h').getByRole('button', { name: 'Edit' }).click();
    await setPolicyDoc(
      page,
      `form[hx-post$="/inline"]:has(input[type=hidden][name="policy"][value="${inlineName}"])`,
      allow('sqs:ReceiveMessage')
    );
    await inlinePanel.getByRole('button', { name: 'Save' }).click();
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`Saved ${inlineName}`);
    await expect(inlinePanel.locator('.code-out pre')).toContainText('sqs:ReceiveMessage');
    await expect(inlinePanel.locator('.code-out pre')).not.toContainText('sqs:SendMessage');

    // --- Inline policy: remove ---
    await inlinePanel.getByRole('button', { name: 'Remove' }).click();
    await confirmDialog('accept');
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('Removed inline policy');
    await expect(inlinePanel).toHaveCount(0);

    // --- Detach the managed policy ---
    const attachedRow = page.locator('.tbl tr', { hasText: 'arn:aws:iam::aws:policy/ReadOnlyAccess' });
    await expect(attachedRow).toBeVisible();
    await attachedRow.getByRole('button', { name: 'Detach' }).click();
    await confirmDialog('accept');
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('Detached');
    await expect(page.locator('.tbl').first()).toContainText('No managed policies attached');

    // --- Delete the role ---
    await page.locator('.det-title .acts').getByRole('button', { name: 'Delete' }).click();
    await expect(page.locator('#confirm-msg')).toContainText(role);
    await confirmDialog('accept');
    await page.waitForURL(/\/iam(\?.*)?$/);
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`Deleted ${role}`);
    await expect(page.locator('.listpane .li', { hasText: role })).toHaveCount(0);
  });
});

test.describe('IAM user page', () => {
  test('join a group, delete an access key, delete the user', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    const user = uniqueName('e2e-iam-u');
    const group = uniqueName('e2e-iam-g');
    await postForm(request, 'iam/create', { kind: 'user', name: user });
    await postForm(request, 'iam/create', { kind: 'group', name: group });
    await postForm(request, `iam/user/${user}/keys`, {});

    await page.goto(`iam/user/${user}`);
    await expect(page.locator('.mini-note', { hasText: 'In no group.' })).toBeVisible();

    // --- Join a group from the user's side ---
    const join = page.locator(`form[hx-post$="/iam/user/${user}/join-group"]`);
    await join.locator('select[name="group"]').selectOption(group);
    await join.getByRole('button', { name: 'Add', exact: true }).click();
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`${user} added to ${group}`);
    await expect(page.locator('.chips a.badge', { hasText: group })).toBeVisible();
    // …and the group sees the member.
    await page.goto(`iam/group/${group}`);
    await expect(page.locator('.badge', { hasText: user })).toBeVisible();

    // --- Delete the access key ---
    await page.goto(`iam/user/${user}`);
    const keyRow = page.locator('.tbl tbody tr', { has: page.locator('.chip', { hasText: 'Active' }) });
    await expect(keyRow).toHaveCount(1);
    const keyId = ((await keyRow.locator('td').first().textContent()) ?? '').trim();
    await keyRow.getByRole('button', { name: 'Delete key' }).click();
    await expect(page.locator('#confirm-msg')).toContainText(keyId);
    await confirmDialog('accept');
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('Key deleted');
    await expect(page.locator('.tbl tbody', { hasText: 'No access keys' })).toBeVisible();
    await expect(page.locator('.tbl', { hasText: keyId })).toHaveCount(0);

    // --- Delete the user (leave the group first: IAM refuses a member) ---
    await page.goto(`iam/group/${group}`);
    // The × carries its meaning only in title=, so its accessible name is "×".
    await page.locator(`button[title="Remove ${user} from the group"]`).click();
    await confirmDialog('accept');
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`${user} removed from ${group}`);

    await page.goto(`iam/user/${user}`);
    await page.locator('.det-title .acts').getByRole('button', { name: 'Delete' }).click();
    await expect(page.locator('#confirm-msg')).toContainText(user);
    await confirmDialog('accept');
    await page.waitForURL(/\/iam(\?.*)?$/);
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`Deleted ${user}`);
    await expect(page.locator('.listpane .li', { hasText: user })).toHaveCount(0);
  });

  // Regression (fixed in 1.0): POST /iam/user/{name}/rename (UpdateUser) has a handler but the user
  // page renders no rename control — groups have one, users do not.
  test('rename a user from its page', async ({ page, request, uniqueName, confirmDialog }) => {
    const user = uniqueName('e2e-iam-ren');
    await postForm(request, 'iam/create', { kind: 'user', name: user });
    await page.goto(`iam/user/${user}`);
    const form = page.locator(`form[hx-post$="/iam/user/${user}/rename"]`);
    await form.locator('input[name="new"]').fill(`${user}-renamed`);
    await form.getByRole('button', { name: 'Rename' }).click();
    if (await page.locator('#confirm').isVisible()) await confirmDialog('accept');
    await page.waitForURL(new RegExp(`/iam/user/${user}-renamed$`));
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`User renamed to ${user}-renamed`);
  });
});

test.describe('IAM managed policy page', () => {
  test('delete a non-default version, then delete the policy', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    const policy = uniqueName('e2e-iam-delpol');
    const arn = `arn:aws:iam::000000000000:policy/${policy}`;
    await postForm(request, 'iam/create', { kind: 'policy', name: policy, document: allow('s3:GetObject') });
    await postForm(request, 'iam/policy/new-version', { arn, document: allow('s3:*'), default: '1' });

    await page.goto(`iam/policy?arn=${arn}`);
    const v1 = page.locator('.tbl tbody tr', { has: page.locator('td.mono', { hasText: /^v1$/ }) });
    await expect(v1).toHaveCount(1);
    await v1.getByRole('button', { name: 'Delete version v1' }).click();
    await expect(page.locator('#confirm-msg')).toContainText('v1');
    await confirmDialog('accept');
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('Version deleted');
    await expect(v1).toHaveCount(0);
    await expect(page.locator('.tbl tbody tr', { hasText: 'v2' }).locator('.badge')).toHaveText('default');

    await page.locator('.det-h .acts').getByRole('button', { name: 'Delete' }).click();
    await expect(page.locator('#confirm-msg')).toContainText(policy);
    await confirmDialog('accept');
    await page.waitForURL(/\/iam(\?.*)?$/);
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('Policy deleted');
    await expect(page.locator('.listpane .li', { hasText: policy })).toHaveCount(0);
  });
});

test.describe('IAM instance profile page', () => {
  test('add a role, remove it, delete the profile', async ({ page, request, uniqueName, confirmDialog }) => {
    const profile = uniqueName('e2e-iam-ip');
    const role = uniqueName('e2e-iam-iprole');
    await postForm(request, 'iam/create', { kind: 'role', name: role });
    await postForm(request, 'iam/create', { kind: 'profile', name: profile });

    await page.goto(`iam/profile/${profile}`);
    await expect(page.getByText('Empty — an instance with this profile has no permissions.')).toBeVisible();
    const add = page.locator(`form[hx-post$="/iam/profile/${profile}/role"]`);
    await add.locator('select[name="role"]').selectOption(role);
    await add.getByRole('button', { name: 'Add', exact: true }).click();
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('Profile updated');
    await expect(page.locator('.chips .badge a', { hasText: role })).toBeVisible();
    // A profile holds one role: the add form is gone.
    await expect(add).toHaveCount(0);
    // The role page lists the profile back.
    await page.goto(`iam/role/${role}`);
    await expect(page.locator('a.badge', { hasText: profile })).toBeVisible();

    await page.goto(`iam/profile/${profile}`);
    await page.locator(`button[title="Remove ${role}"]`).click();
    await confirmDialog('accept');
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('Profile updated');
    await expect(page.getByText('Empty — an instance with this profile has no permissions.')).toBeVisible();

    await page.locator('.det-title .acts').getByRole('button', { name: 'Delete' }).click();
    await expect(page.locator('#confirm-msg')).toContainText(profile);
    await confirmDialog('accept');
    await page.waitForURL(/\/iam(\?.*)?$/);
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText(`Instance profile ${profile} deleted`);
    await expect(page.locator('.listpane .li', { hasText: profile })).toHaveCount(0);
  });
});

test.describe('IAM account and access log', () => {
  // Regression (fixed in 1.0): the "Load the export" button does nothing. Its wrapper uses
  // hx-trigger="click from:find button", which htmx 4's trigger parser splits
  // at the space (from="find", then a stray "button"), so no listener is bound
  // and POST /iam/account/details is never sent (templates/iam.html:687).
  test('authorization export loads on demand', async ({ page, request, uniqueName }) => {
    const user = uniqueName('e2e-iam-audit');
    await postForm(request, 'iam/create', { kind: 'user', name: user });
    await page.goto('iam/account');
    await expect(page.locator('#iam-auth-out')).toBeEmpty();
    await page.getByRole('button', { name: 'Load the export' }).click();
    await expect(page.locator('#iam-auth-out pre')).toContainText(user);
  });

  test('generate a least-privilege policy from what was recorded', async ({ page, request }) => {
    // Something has to be on the record. Console mutations do not pass
    // through the IAM middleware, so arrange one real SDK-shaped call (SQS
    // ListQueues, root credentials): soft mode records it.
    const res = await request.post(ORIGIN + '/', {
      form: { Action: 'ListQueues', Version: '2012-11-05' },
      headers: {
        Authorization:
          'AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20260101/us-east-1/sqs/aws4_request, SignedHeaders=host, Signature=00',
        'X-Amz-Date': '20260101T000000Z',
      },
    });
    expect(res.ok()).toBeTruthy();

    await page.goto('iam');
    await expect(page.locator('.chip', { hasText: 'mode: soft' })).toBeVisible();
    await expect(page.locator('.tbl td', { hasText: 'sqs:ListQueues' }).first()).toBeVisible();
    const gen = page.locator('form[hx-post$="/iam/generate"]');
    await gen.locator('select[name="principal"]').selectOption('');
    await gen.getByRole('button', { name: 'Generate' }).click();
    const out = page.locator('#iam-generated');
    await expect(out.locator('pre')).toContainText('sqs:ListQueues');
    await expect(out.locator('.err')).toHaveCount(0);
    // The document can be saved as a policy in one step.
    await expect(out.getByRole('button', { name: 'Create policy' })).toBeVisible();
  });
});
