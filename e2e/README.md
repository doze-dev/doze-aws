# doze-aws console E2E suite

Playwright browser tests for the `/_console` web UI. This is the only
JS/Node tooling in doze-aws — everything else is pure Go — so it's kept
self-contained in this directory.

## Running

```sh
bun install
bunx --bun playwright install --with-deps chromium   # once, or after a Playwright bump
bunx --bun playwright test                            # headless
bunx --bun playwright test --ui                        # interactive UI mode
bunx --bun playwright test --headed                     # watch it drive a real browser
bunx --bun playwright test tests/sqs.spec.ts             # one file
```

Or via the repo's Taskfile from `doze-aws/`: `go tool task test:e2e`.

`bunx --bun playwright test` builds the real `doze-aws`
binary and boots it with `--listen 127.0.0.1:14566` against an isolated data
dir (`e2e/.tmp/data`) — see `playwright.config.ts`'s `webServer`.

`--listen` matters here: it claims no `.doze` name, needs no `dns-setup`, and
touches no shared registry, so the suite is identical in CI and on a laptop
with instances already running. The port is off 4566 so it never collides with
a dev instance; 4566 is not "the default" any more, since doze-aws answers on
its name and binds no address unless asked.

The server is never reused — the data dir is wiped in the boot command, so a
reused one would keep its state and the wipe would never run.

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
- **A server of the spec's own**: a test that needs a stack nobody else is calling (the first-run wire), or one it may stop and start (the "cannot reach doze-aws" banner), uses `fixtures/private.ts`. It starts the binary the suite built, on `E2E_PORT + 1`, over a data directory of its own; `wire.spec.ts` is the example. Keep two runs' `E2E_PORT`s at least two apart.
- **No `data-testid` convention** exists in the console's templates. Prefer semantic locators (`getByRole`, `getByLabel`, resource names) — they're almost always sufficient since the app's forms/buttons have real labels and text.

## Fidelity: the board beside the page

`e2e/fidelity/` is the console's design-fidelity gate (plan/00-foundation B14,
plan/01-protocol step 5). It is not part of the Playwright suite and not part
of `task check`; it is a tool a service's console stage runs, and reads.

```sh
go tool task console:fidelity -- sqs               # every board of SQS, light, 1440 by 900
go tool task console:fidelity -- sqs --dark        # both sides dark
go tool task console:fidelity -- sqs --w900        # the page at 900 wide (the board stays 1440)
go tool task console:fidelity -- sqs --w1024       # the page at 1024 wide
go tool task console:fidelity -- sqs --id2         # the instance boots as 111122223333 in eu-west-1
go tool task console:fidelity -- Sqs-17-Config     # one board
go tool task console:fidelity -- shell             # the cross-cutting boards (Anatomy, Main, ...)
go tool task console:fidelity:test                 # unit tests: `bun test e2e/fidelity`
# or, from e2e/: bun fidelity/run.ts sqs
```

It builds the real binary, boots it on a free port in 14960-14969 (move the range
with `FIDELITY_PORT_MIN` and `FIDELITY_PORT_MAX`, so two runs can share a machine)
with a fresh data dir (`--name harbour`), seeds the **harbour** fixture
(`fidelity/harbour.ts`: the shop the boards are drawn from; `fidelity/wire.ts`
adds the afternoon of calls the wire boards draw, on an instance of its own, see
`ctx.instance('wire')`), and for each board in
`plan/design/boards/` renders the board (`file://` at 1440 by 900, `.app.dark`
for `--dark`, Google Fonts blocked so the fallbacks make every machine's picture
the same), drives the real page into the board's state, and writes
`e2e/.tmp/fidelity/<service>[-dark][-w900][-id2]/`:

- `board-<name>.png`, `page-<name>.png` (more states of one board:
  `page-<name>-<label>.png`), and the same texts as `.txt`;
- `report.html`: the contact sheet, board left and page right, one row per
  board, then the board's text and the page's text extracted from the DOM,
  with the board lines the page lacks marked;
- `results.json`.

**The list of boards is the directory listing**, never a table. Each service has
`fidelity/drivers/<service>.ts`: one entry per board, `{ drive(ctx) }` to put
the page into the board's state or `{ skip: reason }` where there is nothing to
drive (a skip is shown in the report). A board with no entry is a failing row
(`no driver for <board>`); so is an entry for a board that no longer exists.
The other services' tables are empty on purpose: each is that service's stage
to-do list. The cross-cutting boards (`Main`, `Anatomy`, `Wire-*`, ...) are in
`drivers/shell.ts`.

**A board whose state the main instance cannot be in** boots another
(`c.instance(kind)` in `drivers/types.ts`): `enforce`, `empty`, `fresh`, and
`reader`: the fixture with an *enabled* event source mapping to a real function.
A queue that is being read has no messages waiting, so the reader is held inside
one invocation (the first message it reads is `{"sleep": ms}`, which the fixture
bootstrap in `fixtures/lambda-handler` honours) and the waiting messages are
sent after. The hold lasts 25 s from the seed (`HOLD_MS`, under the function's 30 s
timeout), so each board that needs a reader boots its own and drives inside it.

**The structural check** is not a pixel diff. For every pair it asks whether
each line of the board's visible text is somewhere in the page's visible text,
and prints the lines the page lacks. For a service's boards the shell
(top bar, rail, list pane, key bar) is not compared: those are the shell's, and
`shell`'s boards are compared whole. Times, ports, ids and (under `--id2`) the
account and Region are masked, case is ignored, and `fidelity/allow/<board>.txt`
lists the board text the page legitimately omits or words differently:
`board text # kind: why`, where the kind is `shell`, `icon`, `doc`, `decision`,
`annotation` or `data`. An entry with no reason is an error, an entry for a
sentence the page now has is reported, and **a missing sentence is never listed
to make a board pass**: where the page truly lacks board text the row fails and
the line is the finding.

Exit status is non-zero for a board with no driver, one that fails to render or
to drive, an orphaned driver, or a page that lacks board text. The pictures are
still read by a person: the tool makes sure they exist and that a missing
sentence cannot hide.

To add a service: write `drivers/<service>.ts` from `drivers/sqs.ts` (selectors
and flows from the service's own spec in `tests/`), seed what its boards show in
`harbour.ts`, run it, read `report.html`, and write `allow/` entries only for
what the plan's 6a table decided.

## Updating browsers

`bunx playwright install --with-deps chromium` re-downloads if the pinned
`@playwright/test` version in `package.json` changes.
