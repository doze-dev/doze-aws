# doze-aws console E2E suite

Playwright browser tests for the `/_console` web UI. This is the only
JS/Node tooling in doze-aws — everything else is pure Go — so it's kept
self-contained in this directory.

## Running

```sh
bun install
bunx playwright install --with-deps chromium   # once, or after a Playwright bump
bunx playwright test                            # headless
bunx playwright test --ui                        # interactive UI mode
bunx playwright test --headed                     # watch it drive a real browser
bunx playwright test tests/sqs.spec.ts             # one file
```

Or via the repo's Taskfile from `doze-aws/`: `go tool task test:e2e`.

`bunx playwright test` builds the real `doze-aws` binary and boots it on a
fixed port (`127.0.0.1:14566`, deliberately off the default 4566 so it never
collides with a dev instance) against an isolated data dir
(`e2e/.tmp/data`) — see `playwright.config.ts`'s `webServer`. Locally it
reuses an already-running instance on that port (fast iteration while
authoring a spec); CI always boots fresh.

## How the suite is isolated

There is **one shared, long-lived server for the whole run**, not one per
spec file — bbolt is single-writer, and a single instance is how doze-aws
is actually used. Isolation instead comes from every spec giving every
resource it creates a unique name via the `uniqueName()` fixture (e.g.
`uniqueName('e2e-sqs')` → `e2e-sqs-a1b2c3`), and specs assert on "my named
resource behaves correctly" — never on exact list counts or total resource
state, since sibling specs (and repeated local runs against a reused
server) are constantly adding to that state.

## Structure

- `playwright.config.ts` — `webServer`, `baseURL` (`http://127.0.0.1:14566/_console/`), chromium-only project, trace/screenshot/retry config.
- `fixtures/console.ts` — shared test fixtures: `uniqueName`, `openPalette`, `waitForToast`, `confirmDialog`, `setEditor`, `waitForLive`.
- `fixtures/api.ts` — "arrange via API" helpers (`createBucket`, `createQueue`, `createTopic`, `createTable`, `createBus`, `createKey`, `createFunction`, plus a generic `postForm`) that POST straight to the console's own create routes, so specs don't have to click through every service's create wizard just to set up a precondition.
- `fixtures/lambda-handler/` — a minimal real Lambda Runtime Interface Client (Go, `provided.al2`) used by `lambda.spec.ts` to actually invoke functions end-to-end. Build output goes to `e2e/.tmp/lambda-fixture/`, never committed.
- `tests/` — one spec file per console surface (`s3`, `sqs`, `dynamodb`, `sns`, `eventbridge`, `lambda`, `kms`, `ssm`, `secretsmanager`), plus cross-cutting ones (`shell` for chrome/theme/palette/confirm-dialogs, `flows`, `traffic`, `tags`, `connections`) and `smoke.spec.ts` (harness sanity check).

## Conventions and gotchas

- **baseURL trap**: `baseURL` is `http://host:port/_console/` (a path, not just an origin). Playwright resolves a *leading-slash* `goto('/...')` against the **origin**, dropping `/_console/` entirely. Always `page.goto('')` for the console home or `page.goto('s3')` (no leading slash) for a sub-path.
- **CodeMirror editors**: any `textarea[data-editor]` is upgraded to a CodeMirror 5 instance client-side. Use the `setEditor(selector, value)` fixture (which calls the app's own `window.dozeEditor.set`, keeping the CM buffer and the underlying textarea's `.value` in sync) instead of `.fill()`/keyboard typing.
- **`[data-live]` regions** (SQS peek, Traffic feed, Flows canvas, Lambda runtime badge) poll and the server replies `204` when unchanged — never assert on a fixed poll tick or sleep; use `waitForLive` or an auto-retrying `expect(locator).toContainText(...)`.
- **Styled confirm dialogs**: every destructive action goes through `hx-confirm`, intercepted client-side into a styled `#confirm` overlay (never a native `confirm()`). Use the `confirmDialog('accept' | 'cancel')` fixture.
- **Toasts** self-remove after 3.2s (success) / 6s (error) — read `waitForToast()`'s return value immediately, don't re-query later. When two toast-producing actions fire close together, the previous toast can still be visible and satisfy `.last()` before the new one lands — several specs insert a short "wait for the toast to clear" step between such actions; look at `kms.spec.ts` or `lambda.spec.ts` for the pattern if you hit this.
- **Icon-only buttons** (no visible text, just an SVG) get their accessible name from their `title` attribute, so `getByRole('button', { name: '...' })` works — but scope it (e.g. to the containing row) since the same title ("Delete") often appears more than once on a page.
- **A real Playwright/Chromium quirk**: a synthetic `.click()` on a link inside the ⌘K command palette (which has a CSS open-animation) doesn't trigger navigation, even though the click provably lands on the right element (`elementFromPoint`-verified). A native DOM `.click()` works fine, and so does keyboard `Enter` selection — `shell.spec.ts`'s palette test uses the keyboard path, which is also more representative of real command-palette usage. If a click on something inside an `.anim`-classed overlay silently does nothing, try that before spending a long time debugging it.
- **Never assert on global/exact counts.** The shared server accumulates resources across the whole run (and across repeated local runs, since the data dir persists). Scope every assertion to your own `uniqueName()`d resources.
- **No `data-testid` convention** exists in the console's templates. Prefer semantic locators (`getByRole`, `getByLabel`, resource names) — they're almost always sufficient since the app's forms/buttons have real labels and text.

## Updating browsers

`bunx playwright install --with-deps chromium` re-downloads if the pinned
`@playwright/test` version in `package.json` changes.
