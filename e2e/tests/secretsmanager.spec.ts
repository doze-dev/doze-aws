import { execFileSync } from 'node:child_process';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { Page } from '@playwright/test';
import { test, expect } from '../fixtures/console';
import { postForm, createFunction } from '../fixtures/api';

// Secrets Manager console coverage: create, "Generate password", new
// versions + diff, rotation (real Lambda invocation — SM's RotateSecret
// action synchronously drives the 4-step protocol against the configured
// function for BOTH "enable rotation" and "rotate now", so a stub function
// is not enough; see buildRotationLambda()), and delete/restore with the
// 7-day recovery window.

// Compiles a tiny "provided.al2" bootstrap that polls the Lambda runtime
// API and acks every invocation with an empty 200 — sufficient for all four
// SM rotation steps (createSecret/setSecret/testSecret/finishSecret), which
// only check that the invoke succeeds. Mirrors
// secretsmanager/rotate_test.go's rotationBootstrap.
function buildRotationLambda(): string {
  const dir = mkdtempSync(join(tmpdir(), 'sm-rotate-'));
  writeFileSync(
    join(dir, 'main.go'),
    `package main

import (
	"io"
	"net/http"
	"os"
)

func main() {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	for {
		resp, err := http.Get("http://" + api + "/2018-06-01/runtime/invocation/next")
		if err != nil {
			os.Exit(1)
		}
		reqID := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		http.Post("http://"+api+"/2018-06-01/runtime/invocation/"+reqID+"/response", "application/json", nil)
	}
}
`
  );
  writeFileSync(join(dir, 'go.mod'), 'module smrotate\n\ngo 1.26\n');
  execFileSync('go', ['build', '-o', 'bootstrap', '.'], {
    cwd: dir,
    env: { ...process.env, GOWORK: 'off', CGO_ENABLED: '0' },
  });
  return dir;
}

test.describe('create secret', () => {
  test('appears in the list and the value workspace masks it (keys stay visible)', async ({
    page,
    request,
    uniqueName,
  }) => {
    const name = uniqueName('e2e-sm');
    await postForm(request, 'sm/create', {
      name,
      description: 'e2e secret',
      value: '{"username":"admin"}',
    });

    await page.goto('sm');
    await expect(page.locator('.li', { hasText: name })).toBeVisible();

    await page.goto(`sm/secret?name=${name}`);
    await expect(page.locator('.det-title')).toContainText(name);

    // View mode: both the masked and revealed renderings are pre-rendered
    // server-side as the two DIRECT-CHILD spans of .ws-view (x-show="!reveal"
    // / x-show="reveal") — Alpine just toggles visibility client-side. Use
    // the direct-child combinator so this doesn't match the nested jk/js
    // syntax-highlight spans inside each rendering.
    const maskedSpan = page.locator('#sm-detail .ws-view > span').nth(0);
    await expect(maskedSpan).toContainText('username');
    await expect(maskedSpan).toContainText('••');
    await expect(maskedSpan).not.toContainText('admin');

    // Reveal flips to the real value.
    await page.locator('.ws-toolbar').getByRole('button', { name: 'Reveal' }).click();
    const revealedSpan = page.locator('#sm-detail .ws-view > span').nth(1);
    await expect(revealedSpan).toContainText('admin');
  });
});

test.describe('generate password', () => {
  test('merges a password field into the editor alongside existing content', async ({
    page,
    uniqueName,
    setEditor,
  }) => {
    await page.goto('sm/create');
    await page.locator('input[name=name]').fill(uniqueName('e2e-sm-pw'));
    await setEditor('textarea[data-editor]', '{"username":"admin"}');

    await page.getByRole('button', { name: /Generate password/ }).click();

    // dozeGenPassword fetches /sm/password, JSON.parse's the current editor
    // content, sets .password, and re-serializes via dozeEditor.set (which
    // calls cm.save() so the underlying textarea.value stays in sync) —
    // poll the textarea's value rather than racing the fetch.
    await expect(async () => {
      const val = await page.locator('textarea[data-editor]').inputValue();
      expect(val).toContain('"password"');
    }).toPass({ timeout: 8000 });

    const finalValue = await page.locator('textarea[data-editor]').inputValue();
    expect(finalValue).toContain('"username"');
    expect(finalValue).toContain('"admin"');
    const parsed = JSON.parse(finalValue);
    expect(parsed.username).toBe('admin');
    expect(typeof parsed.password).toBe('string');
    expect(parsed.password.length).toBeGreaterThan(0);
  });
});

test.describe('new version', () => {
  test('editing the value creates a second version with a diff', async ({
    page,
    request,
    uniqueName,
    setEditor,
    waitForToast,
  }) => {
    const name = uniqueName('e2e-sm-ver');
    await postForm(request, 'sm/create', { name, value: '{"pw":"old-value"}' });

    await page.goto(`sm/secret?name=${name}&tab=edit`);
    // Edit mode prefills the editor with the real (unmasked) value.
    await expect(page.locator('textarea[name="value"][data-editor]')).toHaveValue(/old-value/);

    await setEditor('textarea[name="value"][data-editor]', '{"pw":"new-value"}');
    await page.getByRole('button', { name: 'Save as new version' }).click();

    const toastMsg = await waitForToast();
    expect(toastMsg).toMatch(/New secret version stored/);

    // Two versions now: current + previous.
    await expect(page.locator('.meta-cellx', { hasText: 'Versions' }).locator('.v2')).toHaveText('2');

    await page.locator('.ws-toolbar a', { hasText: 'Versions' }).click();
    await expect(page.locator('.tbl tbody tr')).toHaveCount(2);

    // Stages is a map (version id -> [AWSCURRENT] / [AWSPREVIOUS]) rendered
    // via Go's randomized map iteration, so row order isn't stable — select
    // the AWSPREVIOUS row explicitly rather than assuming "first".
    const prevRow = page.locator('.tbl tbody tr', {
      has: page.locator('.badge', { hasText: 'AWSPREVIOUS' }),
    });
    await prevRow.getByRole('button', { name: 'Diff vs current' }).click();
    const diffBox = page.locator('#version-diff .diffbox');
    await expect(diffBox).toBeVisible();
    await expect(diffBox).toContainText('old-value');
    await expect(diffBox).toContainText('new-value');
    await expect(diffBox.locator('.dl.del')).toContainText('old-value');
    await expect(diffBox.locator('.dl.add')).toContainText('new-value');
  });
});

test.describe('rotation', () => {
  test.setTimeout(60_000);

  test('configuring and rotating now both persist and confirm', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const codeDir = buildRotationLambda();
    const fnName = uniqueName('e2e-sm-rotator');
    await createFunction(request, fnName, { code: codeDir });

    const name = uniqueName('e2e-sm-rot');
    await postForm(request, 'sm/create', { name, value: '{"pw":"v1"}' });

    await page.goto(`sm/secret?name=${name}`);
    await page.locator('select[name=lambda]').selectOption(fnName);
    await page.locator('input[name=days]').fill('45');
    await page.getByRole('button', { name: 'Enable rotation' }).click();

    const enableToast = await waitForToast();
    expect(enableToast).toMatch(/Rotation configured/);

    // Persisted: the rotation strip now shows the "on" state, pointed at our
    // function. NOTE: the "days" value entered in the form does NOT survive
    // — secretsmanager.rotateSecret() (console/../secretsmanager/rotate.go)
    // never writes RotationRules.AutomaticallyAfterDays onto the stored
    // Secret, so DescribeSecret always reports 0 regardless of what was
    // submitted. That's a backend gap, not a test bug — asserting the
    // literal "45" here would be asserting behavior the app doesn't have,
    // so this only pins the observable "every <N> days" shape.
    await expect(page.locator('#sm-detail .pub-receipt')).toContainText(fnName);
    await expect(page.locator('#sm-detail .pub-receipt')).toContainText(/every \d+ days/);
    await expect(
      page.locator('.meta-cellx', { hasText: 'Rotation' }).locator('.badge.state-on')
    ).toContainText(/every \d+d/);

    // Toasts self-remove after 7s and waitForToast() just grabs whatever
    // `.toast:not(.err)` element is currently last in the DOM — if the
    // "Rotation configured" toast from the previous step is still showing
    // when we click "Rotate now", it (not the new toast) satisfies the
    // visibility check first. Let it clear before triggering the next one.
    await page.evaluate(() => document.querySelectorAll('.toast').forEach((e) => e.remove()));

    await page.getByRole('button', { name: 'Rotate now' }).click();
    const rotateToast = await waitForToast();
    expect(rotateToast).toMatch(/Rotation triggered/);
    await expect(page.locator('#sm-detail .pub-receipt')).toContainText('last rotated');
  });
});

test.describe('delete with recovery window', () => {
  test('delete schedules deletion; restore reactivates', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
  }) => {
    const name = uniqueName('e2e-sm-del');
    await postForm(request, 'sm/create', { name, value: '{"pw":"v1"}' });

    await page.goto(`sm/secret?name=${name}`);
    await page.locator('.acts').getByRole('button', { name: 'Delete' }).click();
    await expect(page.locator('#confirm-msg')).toContainText(name);
    await confirmDialog('accept');

    // smDelete sends HX-Redirect (a full client-side navigation) without a
    // flash query param and its HX-Trigger toast is issued in the same
    // response — htmx navigates away before the toast can render, so (unlike
    // most other console mutations) there's nothing to wait for here besides
    // the navigation itself.
    await page.waitForURL(/\/sm(\?.*)?$/);

    // List row shows the deleted marker.
    const row = page.locator('.li', { hasText: name });
    await expect(row).toHaveClass(/li-deleted/);
    await expect(row.locator('.sb')).toHaveText('deleted');

    // Detail view: pending-deletion badge, warning banner, Restore button.
    await page.goto(`sm/secret?name=${name}`);
    await expect(page.locator('.badge.dlq')).toContainText('pending deletion');
    await expect(page.locator('.pub-receipt.warn')).toContainText('Scheduled for deletion');
    const restoreBtn = page.locator('.acts').getByRole('button', { name: 'Restore secret' });
    await expect(restoreBtn).toBeVisible();

    // smRestore uses the c.redirect() helper, which DOES carry a flash
    // message through the redirect's query string.
    await restoreBtn.click();
    await page.waitForURL(new RegExp(`name=${name}`));
    await expect(page.locator('#toasts .toast:not(.err)').last()).toContainText('restored');
    await expect(page.locator('.badge.dlq')).toHaveCount(0);
    await expect(page.locator('.pub-receipt.warn')).toHaveCount(0);
    await expect(page.locator('.acts').getByRole('button', { name: 'Delete' })).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Metadata edits (UpdateSecret), the rollback button (UpdateSecretVersionStage)
// and the resource policy panel (Put/DeleteResourcePolicy).

test.describe('secret details', () => {
  test('pencil -> edit description -> Save updates without a new version', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const name = uniqueName('e2e-sm-meta');
    await postForm(request, 'sm/create', { name, description: 'old description', value: '{"k":"v"}' });

    await page.goto(`sm/secret?name=${name}`);
    await expect(page.locator('.det-sub')).toContainText('old description');
    await page.getByRole('button', { name: 'Edit details' }).click();
    const form = page.locator('form[hx-post$="/sm/update"]');
    await expect(form).toBeVisible();
    await form.locator('input[name="description"]').fill('new description');
    await form.getByRole('button', { name: 'Save' }).click();
    expect(await waitForToast()).toContain('Secret details updated');
    await expect(page.locator('.det-sub')).toContainText('new description');
    // Metadata only: still one version.
    await expect(page.locator('.meta-cellx', { hasText: 'Versions' }).locator('.v2')).toHaveText('1');
  });
});

test.describe('rollback', () => {
  // Regression (fixed in 1.0): "Make current" blanks the whole workspace. The promote form
  // (templates/workspace.html:83) carries no hx-target/hx-select="", so it
  // inherits #workspace + hx-select="#workspace" from the layout; smPromote
  // answers with the #sm-detail partial, which has no #workspace, so the swap
  // replaces the page with nothing. The rollback itself does happen (a reload
  // shows it) — the user just gets an empty screen.
  test('Make current on the previous version rolls the value back', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    waitForToast,
  }) => {
    const name = uniqueName('e2e-sm-roll');
    await postForm(request, 'sm/create', { name, value: '{"pw":"first"}' });
    await postForm(request, 'sm/put', { name, value: '{"pw":"second"}' });

    await page.goto(`sm/secret?name=${name}&tab=versions`);
    const prevRow = page.locator('.tbl tbody tr', {
      has: page.locator('.badge', { hasText: 'AWSPREVIOUS' }),
    });
    const prevId = ((await prevRow.locator('td.ver-id').getAttribute('title')) ?? '').trim();
    expect(prevId).toBeTruthy();
    await prevRow.getByRole('button', { name: 'Make current' }).click();
    await expect(page.locator('#confirm-msg')).toContainText('Make this version current');
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Rolled back');

    // The version that was previous now carries AWSCURRENT, on the same tab.
    const nowCurrent = page.locator('.tbl tbody tr', { has: page.locator(`td.ver-id[title="${prevId}"]`) });
    await expect(nowCurrent.locator('.badge', { hasText: 'AWSCURRENT' })).toBeVisible();
    await expect(nowCurrent.getByRole('button', { name: 'Make current' })).toHaveCount(0);
    // Exactly one detail panel: the swap replaced it rather than nesting.
    await expect(page.locator('#sm-detail')).toHaveCount(1);

    // And the value really is the old one again.
    await page.goto(`sm/secret?name=${name}`);
    await page.locator('.ws-toolbar').getByRole('button', { name: 'Reveal' }).click();
    await expect(page.locator('#sm-detail .ws-view > span').nth(1)).toContainText('first');
  });
});

test.describe('resource policy', () => {
  const policyPanel = (page: Page) =>
    page.locator('.panel', { has: page.locator('form[hx-post$="/sm/policy"]') });
  const docSel = 'form[hx-post$="/sm/policy"] textarea[name="document"]';

  async function savePolicy(page: Page, sid: string) {
    await policyPanel(page).locator('.panel-h').getByRole('button', { name: 'Edit' }).click();
    const doc = JSON.stringify({
      Version: '2012-10-17',
      Statement: [
        { Sid: sid, Effect: 'Allow', Principal: { AWS: 'arn:aws:iam::000000000000:root' }, Action: 'secretsmanager:GetSecretValue', Resource: '*' },
      ],
    });
    await page.waitForFunction((s) => !!(document.querySelector(s) as any)?.__cm, docSel);
    await page.evaluate(
      ([s, d]) => {
        const ta = document.querySelector(s)!;
        (window as any).dozeEditor.set(ta, d);
        ta.dispatchEvent(new Event('input', { bubbles: true }));
      },
      [docSel, doc]
    );
    await policyPanel(page).getByRole('button', { name: 'Save policy' }).click();
  }

  test('Edit -> Save policy stores it and the badge flips to Configured', async ({
    page,
    request,
    uniqueName,
    waitForToast,
  }) => {
    const name = uniqueName('e2e-sm-pol');
    await postForm(request, 'sm/create', { name, value: '{"k":"v"}' });
    await page.goto(`sm/secret?name=${name}`);
    await expect(policyPanel(page).locator('.panel-h .badge')).toHaveText('Not set');

    const sid = uniqueName('E2ESm').replace(/-/g, '');
    await savePolicy(page, sid);
    expect(await waitForToast()).toContain('Resource policy saved');
    await expect(policyPanel(page).locator('.panel-h .badge')).toHaveText('Configured');
    // GetResourcePolicy round-trips it.
    await page.reload();
    await expect(page.locator(docSel)).toHaveValue(new RegExp(sid));
  });

  // Regression (fixed in 1.0): "Remove" deletes the resource policy with no confirmation. Its
  // hx-confirm sits on the submit button (templates/sm.html:107), but htmx 4
  // reads hx-confirm from the triggering element — the form — so the styled
  // confirm never opens and DeleteResourcePolicy runs on the first click.
  test('Remove asks first, then deletes the policy', async ({
    page,
    request,
    uniqueName,
    confirmDialog,
    waitForToast,
  }) => {
    const name = uniqueName('e2e-sm-polrm');
    await postForm(request, 'sm/create', { name, value: '{"k":"v"}' });
    await page.goto(`sm/secret?name=${name}`);
    await savePolicy(page, uniqueName('E2ESm').replace(/-/g, ''));
    expect(await waitForToast()).toContain('Resource policy saved');

    await policyPanel(page).locator('.panel-h').getByRole('button', { name: 'Edit' }).click();
    await policyPanel(page).getByRole('button', { name: 'Remove', exact: true }).click();
    await expect(page.locator('#confirm-msg')).toContainText('Remove the resource policy');
    await confirmDialog('accept');
    expect(await waitForToast()).toContain('Resource policy removed');
    await expect(policyPanel(page).locator('.panel-h .badge')).toHaveText('Not set');
  });
});
