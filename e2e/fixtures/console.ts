import { test as base, expect } from '@playwright/test';
import { randomBytes } from 'node:crypto';

function randomSuffix(len = 6): string {
  return randomBytes(len).toString('hex').slice(0, len);
}

type ConfirmAction = 'accept' | 'cancel';

type ConsoleFixtures = {
  /** A short unique resource name: `${prefix}-${randomHex}`. Use this for
   *  every resource a spec creates so parallel tests never collide. */
  uniqueName: (prefix: string) => string;

  /** Opens the ⌘K command palette via its button (not the keyboard shortcut,
   *  which is flaky across OS/CI keyboard-layout differences) and waits for
   *  it to be focused and populated with its synchronous (non-fetched) items. */
  openPalette: () => Promise<void>;

  /** Waits for a toast (success by default) and returns its text. Toasts
   *  self-remove after 3.2s/6s, so callers should assert on the returned
   *  text immediately rather than re-querying the DOM later. */
  waitForToast: (opts?: { kind?: 'ok' | 'err' }) => Promise<string>;

  /** Answers the styled #confirm dialog that intercepts every hx-confirm
   *  action (delete, purge, etc — never a native confirm()). */
  confirmDialog: (action: ConfirmAction) => Promise<void>;

  /** Writes into a CodeMirror-backed `textarea[data-editor]` through the
   *  app's own programmatic API (window.dozeEditor.set), which is what
   *  "Edit item" / "Generate password" already use — it keeps the CM
   *  instance AND the underlying textarea.value in sync for native submits.
   *  Prefer this over locator.fill()/keyboard.type() for editor fields. */
  setEditor: (selector: string, value: string) => Promise<void>;

  /** Polls a `[data-live]` region until `predicate(text)` is true. Never
   *  waits on a fixed poll tick — the server 204s on no change, so a
   *  fixed-tick wait is either too eager (race) or wastefully slow. */
  waitForLive: (
    selector: string,
    predicate: (text: string) => boolean,
    opts?: { timeout?: number }
  ) => Promise<void>;
};

export const test = base.extend<ConsoleFixtures>({
  uniqueName: async ({}, use) => {
    await use((prefix: string) => `${prefix}-${randomSuffix()}`);
  },

  openPalette: async ({ page }, use) => {
    await use(async () => {
      // NAV/ACTS render synchronously before the /api/resources fetch
      // resolves; that fetch's .then() re-renders #pal-list (replacing
      // DOM nodes) once it lands. On a long-lived server with hundreds of
      // accumulated resources that fetch can take a while, so a caller
      // that types/clicks between the sync render and the async one races
      // a node getting swapped out from under it. Wait for the response
      // here so every caller gets the settled list.
      const resources = page.waitForResponse((res) =>
        res.url().includes('/api/resources')
      );
      await page.locator('#palette-open').click();
      await expect(page.locator('#palette')).toBeVisible();
      await expect(page.locator('#pal-q')).toBeFocused();
      await expect(page.locator('.pal-item').first()).toBeVisible();
      await resources;
    });
  },

  waitForToast: async ({ page }, use) => {
    await use(async (opts) => {
      const kind = opts?.kind ?? 'ok';
      const locator = page
        .locator(kind === 'err' ? '.toast.err' : '.toast:not(.err)')
        .last();
      await expect(locator).toBeVisible({ timeout: 8000 });
      return (await locator.locator('span').nth(1).textContent()) ?? '';
    });
  },

  confirmDialog: async ({ page }, use) => {
    await use(async (action: ConfirmAction) => {
      const dialog = page.locator('#confirm');
      await expect(dialog).toBeVisible();
      await page
        .locator(action === 'accept' ? '#confirm-yes' : '#confirm-no')
        .click();
      await expect(dialog).toBeHidden();
    });
  },

  setEditor: async ({ page }, use) => {
    await use(async (selector: string, value: string) => {
      await page.locator(selector).waitFor({ state: 'attached' });
      // Wait for CodeMirror to finish upgrading the textarea so .set()'s
      // ta.__cm branch is the one that fires (keeps CM's own buffer synced,
      // not just the underlying textarea).
      await page.waitForFunction((sel) => {
        const ta = document.querySelector(sel) as HTMLTextAreaElement & {
          __cm?: unknown;
        };
        return !!ta?.__cm;
      }, selector);
      await page.evaluate(
        ([sel, val]) => {
          (window as any).dozeEditor.set(sel, val);
        },
        [selector, value]
      );
    });
  },

  waitForLive: async ({ page }, use) => {
    await use(async (selector, predicate, opts) => {
      await expect(async () => {
        const text = (await page.locator(selector).textContent()) ?? '';
        expect(predicate(text)).toBe(true);
      }).toPass({ timeout: opts?.timeout ?? 15000 });
    });
  },
});

export { expect };
