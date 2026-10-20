# The shell and the cross-cutting boards, side by side

## Status, 2026-10-16: the shell is built; this file's "Page:" columns are history

The owner decided (2026-10-16) to build the shell to the design: Delete beside
the name, Plex type and the design's tokens, the design's rule for the rail
(labelled on the wire, marks-only inside a service) at 56 px with the user's
choice winning, the boards in `plan/design/`. Built and merged on main, each red
first and rendered beside its boards:

| Piece | State | Commit |
|---|---|---|
| S1 tokens and type (Plex, the design's colours; binary 200 KB smaller) | built | cc406f9 |
| S2 top bar, S3 rail (instance name, IAM pill, rail by context, status dots) | built; the dot is for 5xx, a denial, an alarm or a dead-letter queue, not a refusal | 797199a, 548a169 |
| S4 list pane, S5 key bar (named keys all bound; `Esc` closes the inspector) | built | d6cacec |
| S6 resource header (sentence or strip, Delete beside), neighbours strip, S7 pinned wire dock | built | b7931c7 |
| S8 refusal/confirm states (novalidate, "Keep it", the card, "it threw") | built | af13645 |
| S9 palette, S10 create frame ("The call this makes") | built | 71c7177 |
| S11 name switcher at 1100 px and below, S12 axe sweep both themes, both widths | built | e24b6e7, a2f1d4b |
| Defects an independent re-audit found in the SQS pages and shared components | being fixed | (this section is updated) |

**The wire page, 2026-10-20 (D-a, D-b, D-e): built.** Main, Wire-Inspect,
Wire-Refused, Wire-Empty and Bad-Down are driven in their own state and match
(`go tool task console:fidelity -- shell`, light and dark, 1440), each with its
allow-list entries in `e2e/fidelity/allow/` and their reasons (measured values,
the board's own sample ids, and the decisions in section 4, items 13 to 16).
Built from data the recorder and the product have: the header's calls a minute,
the answer chips in a word, cascade rows with "via", time lanes on one scale, a
long poll as a wait, "Needs a look" (a dead-letter queue and its receive count,
an alarm and since when, refused, refused here, denied, failed, not built, a
function error and how often; each only when it is so) and "In this window", the
inspector with its tabs, the reason and the queue and the ledger for a call that
was not served, the first run, and the banner. The "N with no wiring
configured" line of the old deck is not on the wire any more (no board draws it);
the unwired list is the map's. Where the page differs from a board on purpose:
the filter row sits under the header in every state (Wire-Refused draws the
chips in the header), "paused · N new" shows whenever the feed is paused
(Wire-Refused draws none), and the inspector is 520 wide in every state.

**Not built, by decision or by schedule:** the ghost row in the list pane while
creating (the Create board draws one, Sqs-03 does not; off everywhere; the hook
exists); the compact header (boards 08-21 draw less than the Queue board: the
page keeps the Queue board's header, neighbours and dock on every tab, per
Anatomy's rule); the region chip and theme switch (no board draws a selector;
kept); the fidelity dialog (D-c), Connect (D-d), the map (D-f): foundation D,
scheduled after the services. (The wire page's header row, lanes, "Needs a look"
panel and inspector tabs (D-a), the first-run wire (D-b) and the "cannot reach"
banner (D-e) were built on 2026-10-20: see the paragraph above.)

## Status, 2026-10-20: the seven chrome boards, triaged line by line

Anatomy, Create, Dark, Narrow, Palette, States and Teaching were compared line
by line (`go tool task console:fidelity -- shell`; drivers in
`e2e/fidelity/drivers/shell-chrome.ts`, allow-lists in `e2e/fidelity/allow/`).
Every line the page lacked is one of: built, a harness fix (the page was not in
the board's state, or the fixture lacked the data), an annotation (the board's
own commentary), or a disagreement between two boards, which is listed with the
question for the owner.

| Board | Lacked before | Now | Built | Harness | Allowed (kind: n) | Still lacking (not this package's) |
|---|---|---|---|---|---|---|
| Anatomy | 30 of 71 | 0 | IAM pill "IAM soft · recording" | trigger enabled ("read by"); a refused call on the dock; the held table on its tab | annotation 17, icon 2 (was superseded 6) | |
| Create | 23 of 49 | 0 | the Create board's key bar; "The queue exists as long as the data directory does" | | icon 1 (was decision 11, superseded 9) | |
| Dark | 40 of 88 | 0 | IAM enforce pill (enforce instance) | theme kept across the reload (the runner overwrote it light); the wire boards' afternoon on an enforce instance (wireEnforce), with a bucket policy that denies one read | annotation 9, data 5 (was data 1, superseded 1) | |
| Narrow | 27 of 46 | 0 | the notice in the key bar; j k over messages; the short sentence; the dock's two forms | 1024 by 768, held messages, trigger enabled, a refused call | data 1 (was conflict 11, superseded 4, data 3) | |
| Palette | 6 of 32 | 0 | the query typed at 16 px | the wire's PutObject and Query rows (a bucket, a table) | | |
| States | 43 of 48 | 0 | | the toast on the board's queue; a refused and a denied call in the dock; the empty queue; the bucket confirm; a scan running past 300 ms; a function that threw, on the wire | annotation 29, icon 1 (was superseded 3) | |
| Teaching | 34 of 41 | 0 | line five under the field when the number fits ("Its timeout is 30 s ... so one run finishes before the message returns") | 60 typed, the trigger enabled | annotation 31 | |

Decisions taken (the owner's, 2026-10-20, and what followed from them):

- **The create bars are the Create board's**: ⌘⏎ create, tab next field, esc
  cancel (nothing made). No ⌘K on the bar; the key still opens the palette. The
  SQS create page used the Sqs-03 bar (tab, esc, ⌘K) and now uses this one.
- **The palette's query is 16 px**, as the Palette board draws it (14 before).
  16 is on the type ladder for that one input; `TestTypeScaleIsTheDesigns`
  checks the Palette board still draws it.
- **The dock's caption is "live"**, the pill every board draws, shut or open;
  "quiet" is the wire rail's annotation (Wire-Empty), not the dock's. At 1100
  and below the dock is shut to "N calls touched this queue · M refused" and
  *show*, which is what **Narrow** draws; the Queue and Bad-DLQ boards'
  literal, "calls that touched this queue", is what the dock says when it is
  open. (Narrow says otherwise for the shut form, so Narrow is followed.)
- **Narrow**: at 1100 and below the pane is a name switcher (D5), the pill is
  "IAM soft" (the " · recording" is its own span, shown above 1100), the
  sentence is "0 waiting · 2 in flight · hidden 60 s · dead letters after 3"
  (the long sentence with the words written to go, `{in braces}`, hidden), the
  dock is shut, j and k move over the messages and the bar says "j k message",
  and the notice "Built for 1024 and wider. Phones are not supported, and the
  console says so." is in the key bar at 1024 and below (it was a banner below
  1023, in other words). Rendered and read at 1100, 1024, 900 and 768.
- **The ghost row stays off.**
- **D9 changes**: the IAM pill says "IAM soft · recording" above 1100 px, as
  Anatomy draws it (D9 had dropped the suffix).

Disagreements between boards, decided 2026-10-20 (the service board and the
code win; the shell board was corrected and its allow-list entries deleted):

1. **Narrow's inspector** (a held message opens in a 340 px panel on the right)
   against Sqs-11 and Queue (Release and Delete on the row; a message expands
   in place). Narrow now draws the Consume tab as Sqs-11 does and the inspector
   is gone. Anatomy's "selecting anything opens it on the right" stays the
   shell's rule for objects, items and calls; its annotation now says a queue's
   messages are the exception.
2. **Narrow's "2 held by you"**, "1 of 3": now "2 in flight" and "1× of 3".
3. **Create's call panel** (the HTTP request, "Copy as curl", "Copy the body",
   and "attributes left at AWS's default are sent anyway on this form") against
   D4: the board now draws D4's panel (the aws CLI call, "Copy the command",
   "Made here, kept here", the sentence about the dead-letter queue made first).
   The sentence was false for this form, which sends only what is set; it is
   gone. The ghost row (decided off) is gone from the board too.
4. **Create, Narrow, Anatomy and States wording** against Sqs-03/04, Sqs-11,
   Sqs-21 and Queue: the boards now say "Create queue", "Visibility · s",
   "0 waiting", "dead letters to <queue> after 3", "1× of 3".
5. **Narrow and Anatomy's solid "Send"** beside "Delete": Queue draws Delete
   alone and the composer card at the foot, so the boards do, and Anatomy's
   rule 3 says so. Narrow draws the neighbours strip.
6. **Kms-19-Fidelity** drew the dialog a second way (1360 wide, "Fidelity", a
   right-hand column of tinted summaries). It now draws the Fidelity board's
   one dialog (940 wide, "KMS · what is real here", the ledger's bullets under
   the table), as every service's page does.
7. **Fidelity's** CreateQueue note ("merges attributes", which the ledger
   contradicts) and its paraphrase of "Delivery is in-process" are the ledger's
   words now; the board's "new here" tag and note (the dialog shows the section)
   are gone.
8. **Dark** said "All 212 / Errors 1 / Refused 1", "Where the time went" and
   "Logs", "Secrets", "Parameters": the wire boards (Main, Wire-Refused) and the
   page say "All / Errors / Refused", "Time", "CloudWatch Logs", "Secrets
   Manager", "Parameter Store", and Dark does now.
9. **Bad-Down** drew the queue behind the banner as a table with column heads;
   Queue draws rows. **Connect** abbreviated the endpoints block with an
   ellipsis; the page writes the whole block, and the board does now.

**How to read the rest of this file.** Sections 2.1 to 2.14 each describe "Page:"
(what the console did on 2026-10-11) beside "Board:" (the design). The Page
columns are the pre-rebuild console and read as current if you are not careful:
for the pieces in the table above they are history, and the Board columns and
the verdicts' reasons are what the build followed. The build list in section 5
is the record of what was built and in what order.


Written 2026-10-11 at `7cae441`. Fifteen service reviews each compared their
own boards with the real pages and each found the one thing it could not own:
the shell around the page differs from every board. Nobody had compared the
boards that are not a service's. This is that comparison, and it is the
fidelity table (protocol step 5, [SQS 6a](services/sqs.md)) for
the shell and for every cross-cutting board.

**How it was done.** Each of the 19 boards was rendered from
`console-design/project/*.dc.html` under `file://` at 1440 by 900. A real
`doze-aws` built from this tree (`go build ./cmd/doze-aws`, `-listen
127.0.0.1:14850`) was seeded by AWS calls (four queues, a topic with a
subscription, a bucket with an object, a table, a key, a secret, a
parameter, a log group, a rule with a target; three messages received past
`maxReceiveCount` so the DLQ held six) and put into the board's state. Pages
were screenshotted in light and dark at 1440, and at 1100, 1024, 900 and 768
wide; the palette, the keys, the confirm, the toast, an inline refusal, the
fidelity dialog, the first-run wire and a server that goes away were driven
with Playwright from the scratchpad. Every number below that is a width was
measured in the browser; every sentence quoted was read from a template, a
script or a board. Facts a board states were checked against the code, and
where a limit is quoted against `.audit-models/sqs.json`.

The per-service AWS icons stay (lessons L12). Where a board draws a
two-letter mark the page keeps the icon, and that is not counted as a
difference anywhere below.

## 1. Summary

**19 boards.** Matches: 1. Differs: 14. Design is wrong, corrected, nothing to
build: 1. Cannot match yet: 3. Differs deliberately: 0, because nothing was
ever decided in writing; section 6 proposes the ones that should be.

| Verdict | Boards |
|---|---|
| matches | Teaching |
| differs | Anatomy, Main, Queue, Create, Dark, Narrow, Palette, States, Connect, Fidelity, Bad-DLQ, Wire-Empty, Wire-Inspect, Wire-Refused |
| design is wrong (corrected in the working copy) | Grounding |
| cannot match yet | Map, Map-Large (no page; foundation D), Bad-Down (not built; browser-side) |

Nine of the "differs" boards are also partly wrong about a fact (Anatomy,
Main, Dark, Narrow, States, Connect, Fidelity, Bad-DLQ, Wire-Refused); those
facts were corrected (section 4) and the page is compared with the corrected
board.

**The five that matter most**

1. **The rail.** The design has a context rule, written on Anatomy as rule 1:
   "Full names on the wire, marks only inside a service. The room goes to the
   work." Three boards draw a 196 px labelled rail (Main, Dark, Wire-Empty);
   335 draw 48 px marks; the wire with a call open (Wire-Inspect,
   Wire-Refused) is 48 px too. The page is 208 px labelled everywhere above
   1180 px, with a user toggle. It was built on 2026-07-11 (`3038aa7`),
   three months before the design, for a stated reason that still holds: the
   AWS icons are pink and red squares and, without names, eight of sixteen
   read as the same dot.
2. **The detail header contradicts the design in two ways, and the design
   contradicts itself.** Anatomy rule 3 says "Delete lives under `···`, never
   beside it" and rule 4 says "No strip of labelled boxes". The page has a red
   Delete beside the name and a strip of labelled boxes (the kit's
   `res_header`, which foundation C describes as "facts as a sentence").
   But 119 boards draw a danger Delete beside the name, two boards draw
   `···`, and 28 boards draw a `.facts` strip. The rule is wrong as often as
   the page is.
3. **The footer key bar.** 334 of 367 boards draw a 28 px bar at the foot with
   the keys that page answers to. The page has none; it has a one-line key
   hint inside the list pane, which the wire, Connect and every create page
   do not have. Of the keys the boards name, seven do nothing on the page
   (table in 2.4), and Escape does not close the wire's inspector.
4. **The wire is a different page.** Main/Dark/Wire-* draw a table with a
   header, a time lane per call, a segmented All/Errors/Refused filter, a
   right-hand "Needs a look" panel with four conditions and an "In this
   window" box, and an inspector with Request, Response and "What it caused"
   tabs. The page has a deck of service tiles over a header-less list, one
   attention line (the one condition it computes is a non-empty DLQ), an
   "only errors" checkbox, and a single scrolling inspector. "It threw" cannot
   appear, because the recorder does not read `X-Amz-Function-Error`.
5. **The states board promises what native form validation prevents.** The
   inline refusal beside the control (States, card 2) is not what an SQS
   number field does: the kit gives the input `min`/`max`, so the browser's
   own bubble ("Value must be less than or equal to 43200.") stops the submit
   and the service's refusal is never reached. The Teaching board says a hint
   is never a gate.

**Build estimate.** The shell pieces (foundation C) are about 21 days; the
cross-cutting pages (foundation D: the wire, first run, fidelity dialog,
Connect, banner, map) about 15 more. About 36 days, range 28 to 45. SQS's
plan was low by about 3x; these figures already carry what that taught
(rendered pairs in two themes and three widths, the accessibility sweep, the
route gate and the e2e edits are inside each line, and four days are set aside
at the end for the whole-console re-check, because a shell edit touches every
page). Section 5.

**Decisions needed from a human**: eleven, section 6. The ones that gate
work are D1 (rail default), D2 (header: where Delete goes, sentence or
strip), D3 (typeface and tokens), D4 (what the Create panel shows).

## 2. The shell, piece by piece

Counts of boards are over the 367 files in `console-design/project`, taken by
grepping the markup; they are evidence of what the design draws, not of what
it intends.

### 2.1 The rail

| | Design | Page |
|---|---|---|
| Width | 196 px labelled on the wire's bare list (Main, Dark, Wire-Empty). 48 px marks everywhere else: 335 boards use `rail c`, Anatomy and Narrow set 48 inline; Wire-Inspect, Wire-Refused and Palette's wire backdrop are 48 too | 208 px labelled (`app.css` `.rail`). 56 px when `<html data-rail="slim">`, and automatically at 1180 px and below unless `data-rail="wide"` |
| Content | Labelled: "The wire" (with `live`, `quiet` on first run), "Connect", five group captions, 16 services with the resource count; two boards draw a warn or bad dot (Main: SQS warn, CloudWatch bad; Dark: SQS warn, Lambda bad). Dark adds "The map". Marks: one mark per service | "The wire", "Connect", five groups with the same names (`&` for "and"), 16 services, count in a pill (a zero draws nothing), AWS icon 24 px |
| Names | Main: CloudWatch Logs, Secrets Manager, Parameter Store. Dark: Logs, Secrets, Parameters | the first set |
| Active | tinted row, bold | tinted row and a 2.5 px accent bar |
| Collapse | no board draws a toggle, a state, or a flyout. Grounding lists `[` among the keys that exist | a footer "Collapse" button, `[`, a hover flyout in slim, persisted in `localStorage` `rail`, set before first paint |
| Status dots, "live" | Main and Dark only | none |

Widths, measured on `/sqs/harbour-checkout-orders`:

| Viewport | Design | Page: rail / list pane / detail |
|---|---|---|
| 1440 wire, bare list | rail 196, no list pane, right panel 300 | 208 / none / full width |
| 1440 wire, call open | rail 48, inspector 520 (Wire-Inspect) or 560 (Wire-Refused) | 208 / none / drawer about 520 |
| 1440 service | rail 48, list 220 (Anatomy), 232 (Fidelity), 248 (Create), 262 (Queue, Bad-DLQ, Bad-Down) | 208 / 232 / 1000 |
| 1440, user chose slim | not drawn | 56 / 232 / 1152 |
| 1180 and below | not drawn | 56 / 232 / 812 at 1100 |
| 1024 | Narrow: rail 48, no list pane (a "1 of 4 queues" switcher on the name), inspector 340, the notice in the key bar | 56 / 232 / 736; no notice at 1024 |
| 900 | not drawn | 56 / 232 / 612, the notice above the panes. `app.css` line 984 says the list pane becomes 168 px at 900; line 1039 sets `.listpane { width: 232px }` later at the same specificity and wins, so it never does (measured 232 at 900 and at 768) |

**Why the labelled rail was built.** `git log -S'data-rail'` finds it in
`3038aa7` (2026-07-11): "The rail now defaults to 208px — official AWS icon
(24px, glyph legible), service name, and a live resource count — grouped by AWS
category so the six pink/red services stop reading as identical dots.
Collapses to 56px icons with hover-flyout names ... `[` or the footer button
toggles; the choice persists via localStorage." The 1180 px auto-slim is in the
tree by `f5c208d` (2026-09-20), the first commit that carries the string
(`app.css`: "The rail's names are the cheapest 152px in the shell").
The design's marks are distinguishable (a colour and two letters each); the
AWS icons are not, and L12 keeps them. That is the real tension: a marks-only
rail with these icons is the situation the labelled rail fixed.

**Does any board draw the labelled rail.** Yes, three, and they say when:
the wire, with nothing selected. No board draws a rail that is labelled inside
a service, and none draws the collapse control.

**Verdict: differs.** The page is always the wire's rail. **Recommendation:**
follow the design's rule as the default and keep the user's override. The wire
at `/` and its first-run state default to wide; every other page defaults to
slim; an explicit toggle (kept: the button, `[`, the flyout, `localStorage`)
wins everywhere. Set `data-rail` from the page on the server so nothing
flashes. Keep 56 px, not 48: the icon is 24, and the design's 18 px mark is
what fitted 48 (a deliberate deviation, D1). Add the status dots from
`/api/counts` (a non-empty DLQ is warn; an alarm in ALARM is bad). Wide stays
the default when the viewport is wide and the user has chosen it.

### 2.2 The top bar

| | Design | Page |
|---|---|---|
| Height | 44 px | 48 px |
| Brand | "doze-aws" then the instance name in a muted weight, "harbour" (338 of 340 top bars) | `doze-aws` then "console" in a muted weight. The console has no instance name: `console.Options` carries `Peers`, `Suffix`, `Identity`, `Recorder`, `Prefix` |
| Jump box | 420 px, "Jump to a resource, or do something…" with `⌘K` (339 boards; Narrow: "Jump or act…" at 300) | 260 px, "Jump or act…" with `⌘K`; the palette's own input says "Search resources, services, actions…" |
| Endpoint | a pill with a green dot, the host and port | a chip with a live dot, the host and port |
| Region | a plain `us-east-1` pill on six boards (Main, Dark, Palette, Wire-Empty, Wire-Inspect, Wire-Refused) and none of the 334 service boards | a plain chip on every page. **It is not a selector**: `<span class="chip">us-east-1</span>`, and no board draws a selector either |
| IAM mode | a purple pill on eight boards: "IAM soft · recording" (Anatomy), "IAM soft" (Narrow), "IAM enforce" (Dark and the five Denied boards) | none. Mode is read by the IAM page's `AccessLog` call only |
| Theme | one board's side card (see 2.5) | a three-button group, light / match system / dark, on every page |

**Verdict: differs.** **Recommendation:** build the union on every page: the
instance name (a new `Options.Name`, passed by `cmd/doze-aws` from `-name`),
the jump text from the board, the 420 px box, endpoint, region chip, and the
IAM mode pill. The boards disagree about when the pill appears; D9. The region
chip stays a chip: nothing draws, and nothing in the code supports, a selector
(a region is an argument to `-region`/`-regions`, not a console state).

### 2.3 The list pane: header, filter, key line

| | Design | Page |
|---|---|---|
| Heading | the kind, plural: "Queues" (264 boards draw this with a `New` button) | the service name and a count: "SQS (4)", then an ⓘ button, then a square `+` (title and aria-label "New") |
| New | a text button, `New` (solid on Create) | `+` only. `panes.html` says the text was cut short in nine of sixteen service names |
| Filter | none. 0 of 302 list panes draw an input; the key bars of 74 boards say "`/` filter" | an always-visible "Filter…" box, 38 px, and `/` focuses it (or opens the palette when the page has none) |
| Row | name and the one number; a number that needs a look is coloured and bold (the DLQ's 3 in warn) | name and number, neutral. No board-level rule drives a colour |
| Foot | none inside the pane; the key bar is global | "j k move · c new · / filter" in the pane (`lp_foot`) |
| Ghost row | Create draws the new queue as an italic row marked `new` | none |
| Width | 220 to 262 | 232 |

**Verdict: differs.** The page's filter is a thing the key bar promises and
no board draws, so the board is silent and not contradicting. **Recommendation:**
keep the box (add it to Anatomy and to the pilot's boards), change the heading to
the kind's plural noun from the catalogue (a `Plural` field beside `Noun`) with
the count kept after it, restore the text `New` (the pane's inner width is
208 px; "State machines", the longest plural in the catalogue, has to be
measured with the ⓘ and the button beside it, and `panes.html` records that
"+ New" was cut in nine of sixteen names when the heading was the service
name), draw the coloured count where a service has something to say (the
service's list partial already knows `Available` and the DLQ set), and move
the key line into the global bar (2.4). Note L12: it names "the list pane
header" as a place the icon stays; no board draws a mark there, and
`app.css` hides it (`.lp-h .aws-ic { display: none }`, "the highlighted rail
item beside it already shows one"). L12's wording should follow the boards.

### 2.4 The footer key bar, and what each key does

334 of 367 boards end in a 28 px `.keys` bar: the keys that page answers to,
each a keycap and a verb. The page has the list-pane key line only, so the
wire, Connect, Narrow, Create, the Map and every dialog have nothing.

Every key a board names, against `static/shell.js` (lines 663 to 692):

| Key | On the boards | On the page |
|---|---|---|
| `j` `k` | "move", "queue", "message", "next call" | move the cursor over the list pane's rows only (`listRows()` is `.listpane .li[href]`). Does nothing on the wire, which has no list pane (tested) |
| `⏎` | "open the call", "open a message", "open the resource" | opens the cursor's list row, which is a queue; there is no message cursor and no call cursor |
| `/` | "filter" | focuses the list filter; with none on the page it **opens the palette**, which is what it does on the wire |
| `⌘K` | "jump or act" | works |
| `⌘⏎` | "send", "create" | submits the focused form when the form has a textarea or a CodeMirror editor (the composers, and the SQS create form because it holds a policy editor). Tested: it creates a queue |
| `c` | "copy as curl" (Wire-Inspect, Wire-Refused) | clicks the list pane's New link. Nothing on the wire |
| `→` | "go to the queue" | nothing |
| `esc` | "close" (inspector), "cancel, nothing made" (Create) | closes the palette and the confirm. The wire's inspector stays open (tested). `data-esc-back` is read by `shell.js:691` and set by no template, so Escape on a create page does nothing (tested) |
| `?` | "every key" (Wire-Empty) | nothing, and no board draws what it would show |
| `tab` | "next field" | the browser's |
| `[` | not on any key bar | toggles the rail; Grounding names it |

**Verdict: differs, and seven of the named keys are not built.**
**Recommendation:** build the bar once in `shell_bottom`, each page declaring
its keys as data (`data-keys` on `.panes`), and name only what is bound. The
cheap ones to bind, because the design wants them: on the wire, `j`/`k` and
`⏎` over `.tr-row`, `Escape` closing the drawer, `c` copying the open call's
curl, and `→` following the resource link; on create pages, set `data-esc-back`.
Leave `?` off the board (D10): no board draws its sheet, and a key with no
page behind it is the kind of promise the brief rules out.

### 2.5 The region and theme controls

Region is covered in 2.2. Theme: the Dark board draws a right-hand card,
"Appearance", with a segmented "light / dark / follow the system" and the
sentence "Following the system is the default and costs no script: it is the
absence of a setting." The page's control is a three-icon group in the top
bar of every page; with no stored choice no attribute is set and CSS follows
`prefers-color-scheme` (`layout.html`: `if(t&&t!=="system")`), so the
board's claim is true of the page. The page does carry a small inline script
to read the stored choice, so "costs no script" is true only for the
unchosen case.

**Verdict: differs; board silent on every other page.** The board's card is a
one-off panel that exists on that board only. **Recommendation:** keep the
top-bar group (it is on all 367 pages' worth of shell and the board's card is
on one), and add it to the top bar of Anatomy so the design draws it once.

### 2.6 The detail header

| | Design | Page |
|---|---|---|
| Name | icon mark, name, a type pill | AWS icon, name, badges (the same) |
| Action | Anatomy and Narrow: one solid primary ("Send a message"), `···` menu; "Delete lives under `···`, never beside it". Queue, Bad-DLQ and 119 boards: a red-outline "Delete queue" beside the name and no primary | a red-outline "Delete" with a trash icon beside the name (`res_header`), no primary; "Send a message" is the composer at the foot |
| Facts | Anatomy: "One line, read left to right. No strip of labelled boxes." Queue, Bad-DLQ, Narrow draw the sentence. 28 boards (Lambda 13, Step Functions 7, SNS 3, CloudWatch 2, IAM, Secrets Manager, Parameter Store 1 each) draw the `.facts` strip, which `con.css` defines | a strip: TYPE, VISIBLE, IN FLIGHT, VISIBILITY TIMEOUT, RETENTION, DEAD-LETTER (`factstrip`) |
| Ids | none | two copy chips: `arn` and `url` |
| Fidelity ⓘ | in the header, beside the name ("What of SQS is real here") | in the list pane's header |

**Verdict: differs, and the design is internally inconsistent**, so "follow
the design" has no single answer here. The SQS stage called this "a shell
decision, not an SQS one" and left it; this is the shell decision.
**Recommendation (D2):** the rule is the majority of the boards. Keep the
red Delete beside the name, confirmed (119 boards, the kit, and the page
agree), and correct Anatomy rule 3 and Narrow. Facts: the sentence is the
pilot's and Anatomy's; the strip is where a service has many numbers with
labels. Let `res_header` take either (`Facts` or `Sentence`) and let each
service's boards decide, and say so in rule 4. The ARN and URL chips are
useful and silent on every board: keep, and add one to Anatomy. ⓘ: follow the
board (header beside the name) and keep one in the list header only for the
no-selection state.

### 2.7 The neighbours strip

Boards: a quiet `well` row, "fed by [mark] name → this queue → read by [mark]
name", every name a link, nothing when there are none ("nothing reads this
queue — wire a Lambda trigger to close the loop"). Page: a bordered strip,
"FED BY" then each source as a chip with a coloured left edge and its edge kind
in small mono ("sub", "target"), the resource in bold, "DRAINS TO" the
redrive target. The graph has seven node kinds and seven edge kinds
(`client_flow.go`: notify, sub, target, esm, redrive, dlq, dest).

**Verdict: differs (shape), content matches.** The page shows more (a
second source, the EventBridge target, the edge kind), and says
"no Lambda trigger reads this queue — wire one to close the loop", which is the
corrected sentence (A18: the graph cannot see an SDK reader, so it never says
"nothing"). Bad-DLQ had the older sentence and was corrected. **Recommendation:**
keep; the edge-kind label is the same word the Map's wire labels use, which is
right. Add the typed chips to Anatomy rule 5.

### 2.8 Tabs

Boards: text tabs, no icons, the first is what is inside, "five at most".
Page: the same tabs with an icon each and a live count on the first. Tab names
agree with Queue (Messages, Consume, Configuration, Tags). Anatomy and Narrow
drew "Settings" and "Access", which the SQS page does not have; corrected.

**Verdict: matches** after the correction; the icons are board-silent.
**Recommendation:** keep the icons; they are cheap and the AWS-console habit.

### 2.9 The wire dock under a resource

| | Design | Page |
|---|---|---|
| Place | a strip pinned at the foot of the page under the composer, `margin: 0 -18px` | a block after the composer inside the scrolling detail, 14 px inset |
| Header | "The wire" bold, "calls that touched this queue", a green `live` pill | "ON THE WIRE", "calls your app made to this queue, newest first", a link "The wire →" |
| Rows | time, action bold, resource, status | time, action, status, ms; up to five, then "N earlier calls on the wire." |
| Narrow | collapsed to "3 calls touched this queue · 1 refused" and a `show` button | n/a |

**Verdict: differs.** **Recommendation:** pin it (it is the design's eighth
part, and "You never leave the page to see what your code did to it" is a
pinned claim, not a scrolled one), add the live pill (the dock already
polls: `data-live` on `#res-wire`), and add the collapsed "N calls · M refused" form for the 1024 case.

### 2.10 The wire page and its inspector

See the Main, Dark, Wire-* rows in section 3 for the board-by-board table. The
shell-level facts: the page's drawer is about 520 px (`.t-drawer`), sticky,
beside the list and not over it, which agrees with Anatomy's "It replaces this
panel, in place. Nothing opens at the foot of the page or in a drawer over the
content." The row is 35 px against the boards' 28. The cascade nesting is
real and matches: `└ sqs SendMessage via sns:harbour-orders-topic`.

### 2.11 The palette (keyboard flow, run)

`⌘K` or `Ctrl-K` or the top-bar button opens it, the input is focused, a
list of up to ten shows, `↓`/`↑` move, `⏎` follows, `Esc` closes. Typing
"checkout" gave two queues; two `↓` and `⏎` landed on
`/_console/sqs/harbour-checkout-orders-dlq`. Pasting
`arn:aws:sqs:us-east-1:000000000000:harbour-checkout-orders-dlq` resolved to
that queue (180 ms debounce, `/api/resolve`). With an empty box it lists
recents, then resources, then services and creates, cut at ten (ten
resources filled it). Typing "create q" offers
"Create queue" (kind `create`). It is a single flat list.

What the Palette board draws and the page does not: section captions "Go to"
and "Create" with a `new` tag, a "4 matches" count, the matched substring
underlined, the `⏎` keycap on the selected row, a footer with "Paste an ARN or
a queue URL and it goes straight to the resource." and the key legend, and a
create entry that carries the typed name ("New queue called checkout"). The
page's create entries are static ("Create queue") and the create pages read no
`?name=`. Width 560 against 700. **Verdict: differs; the keyboard flow
matches.** The ARN feature the footer advertises exists and has no
advertisement on the page.

### 2.12 The six moments (States)

| Moment | Design | Page |
|---|---|---|
| It worked | a toast, bottom right, replaced by the next | matches. 7 s, 10 s for an error, held while hovered or focused (`shell.js:140`); "Message sent" with a check. The board said "Today it stays 3.2 seconds" and was corrected |
| It did not work | inline, beside the control, the service's code and sentence, the form keeps what was typed | partly. A refusal that comes back from the server is placed beside the control. But fields with `pattern`, `min` or `max` never reach the server: the browser's bubble stops the submit ("Please match the requested format.", "Value must be less than or equal to 43200."). The States card's own example (90000 into a field with `max="43200"`) cannot be produced from the page |
| Four answers, four colours | `200`, `400 refused` (purple), `403 denied` (amber), `it threw` (red); "A refusal is never red" | five states, one copy table (`stateCopyFor`): Refused, Refused here, Denied, Not built, Failed. Purple and amber are as drawn. A wire row shows the status and the code ("400 InvalidAttributeValue") in the state's colour and not the state's word; the word is in the inspector ("REFUSED · NOSUCHKEY"). "It threw" does not exist: a Lambda function error is a 200 with `X-Amz-Function-Error` (`lambda/invoke.go:88`) and the recorder does not read it |
| It cannot be undone | a confirm that names the thing, says what is lost, "Keep it" / "Delete bucket" | shape matches (title names the thing, a sentence, the verb). The cancel is "Keep it" only where the trigger carries `data-confirm-cancel`: 4 of about 95 `hx-confirm` sites (SQS and the kit). The rest say "Cancel". The verb is the first word of the question ("Delete"), not "Delete bucket" |
| There is nothing here | what would put something here, one button | matches in shape. SQS: "No visible messages. Send one below — it lands here." The board said "Nothing feeds this queue yet", which A18 forbids; corrected |
| It is taking a while | a thin bar after 300 ms, the page stays | matches. `reqbar`, 300 ms, on any non-GET request (the board names a DynamoDB scan and an SQS redrive; both are POSTs) |

**Verdict: differs** on rows 2, 3 and 4. **Recommendation:** put `novalidate`
on forms built from the kit (keep `min`/`max`/`pattern` for the spinners and
the teaching line, which already prints them) so the server's refusal is what
the user sees (D8); make the cancel word "Keep it" whenever the confirm is
destructive; add the two missing answers to the States board; put the state's
word on the wire row; capture the function error.

### 2.13 Tokens and type

Both are a close family and not equal. Design (`con.css`): light `--bg
#f3f3f0`, `--ink #17191c`, `--mut #565b63`, `--line #d9d8d2`, `--ok #166b3f`,
`--warn #855000`, `--bad #b3251b`, `--deny #673c9c`; dark `--bg #111315`,
`--card #191c1f`, `--ink #e9eaeb`. Page (`app.css`): light `--background
#f5f5f5`, `--foreground #202020`, `--muted-foreground #5c5c5c`, `--border
#ddd`, `--success #176e41`, `--warning #8a5200`, `--destructive #c0281c`,
`--refused #6b3fa0`; dark `--background #131313`, `--card #1e1e1e`. Type:
the design is IBM Plex Sans 13/20 and IBM Plex Mono 12, loaded from Google
Fonts; the page embeds Inter (`InterVariable.woff2`, 352 KB per `app.css`'s
own comment) and a system mono. Row height 28 against 35; radius 4 against 2;
list rows 30 against 34. **Verdict: differs, small, visible.** D3.

### 2.14 What the page has that no board shows

| Thing | Board stance |
|---|---|
| Region chip | silent on service pages; the wire family draws a plain pill. Not contradicted |
| Theme switch in the top bar | contradicted in placement (Dark's card) |
| Rail collapse button, `[`, flyouts, persisted choice, the auto-slim at 1180 | silent (Grounding names `[`) |
| List filter box | silent (the key bar names `/ filter`) |
| ⓘ in the list header | contradicted in placement (Fidelity draws it in the header) |
| ARN and URL copy chips | silent |
| Count in the list heading "SQS (4)" | silent |
| The deck: service tiles with counts and state ("4 queues · 3 msgs queued · dlq 3"), "5/min", "updated 18:19:40", a refresh button | replaced by Main's right panel |
| Copy-as-curl on every wire row | silent (the boards put it in the inspector) |
| Palette recents | silent |
| The `cli` toggle on 30 forms in 9 templates (the aws command a form maps to) | silent on Create; Grounding names it. SQS's create form does not have one |
| Tab icons, the live count on the first tab | silent |
| `Clear` asks "Clear the feed? Calls already recorded are dropped from this view." | silent |

## 3. Board by board

| Board | Verdict | What differs |
|---|---|---|
| **Anatomy** | differs | Rail 48 px marks (page: 208 labelled; 2.1). Top bar 44 px, 420 px jump, "doze-aws harbour", `IAM soft · recording` (page: 48, 260, "doze-aws console", no pill). List: "Queues" and a `New` button, no filter, 220 wide (page: "SQS (4)", ⓘ, `+`, filter, 232). Header: one solid primary and `···`, "Delete lives under ···" (page: red Delete beside, no primary; and 119 other boards agree with the page, so rule 3 is wrong). Facts "No strip of labelled boxes" (page: strip; 28 boards agree). No key bar on the page. Inspector 440 "Selecting anything opens it on the right" (page: SQS rows expand inline; Queue.dc.html agrees with the page and Anatomy contradicts the pilot). Dock pinned with a live pill (page: scrolls, no pill). The wire sample row said "MessageGroupId is only valid on a FIFO queue" and the tabs "Settings, Access": **corrected** (accepted on a standard queue now; tabs are Consume, Configuration). Dark: the same board with `.dark` renders correctly; the page's dark matches in intent and differs in tokens (2.13) |
| **Main** | differs | Rail 196 labelled with groups, counts, `live`, two status dots: the page's rail is the same idea at 208, pill counts, no `live`, no dots. Header "The wire" with a `live · 234 a minute` pill, Pause, Clear (page: "live" chip, rate in the deck as "5/min", pause and clear as chips). Filters: `All | Errors | Refused` segmented and "every service ▾" (page: "only errors" checkbox, no Refused filter, a select of raw keys: `s3`, `ddb`, `apigw`, `cw`). A table header (page: none) and a time lane per row with child offsets (page: none; ms only). Answer as a pill with the word ("400 refused", "it threw") (page: colour and code, no word; no "it threw"). Right panel: four "Needs a look" callouts and "In this window" (page: one attention line computed only for a non-empty DLQ; the alarm, the refusal and the function error are not computed). Key bar j k ⏎ / ⌘K (page: none; j k ⏎ do nothing here). "The last 500 calls are kept" matches (`trafficCap = 500`) but is only said in the expired-drawer text. Row note corrected (see Anatomy) |
| **Queue** | differs | Chrome as Anatomy: rail 48/208, list 262 vs 232, header Delete beside the name (this board agrees with the page), facts as a sentence (page: strip; the SQS stage called this deliberate; it is a shell decision, D2). Neighbours as a well (page: chips). Messages as one card of rows with a trash icon each (page: one bordered card per message, no trash icon at rest). Composer is a card "Send a message ⌘⏎ — lands in the list above" (page: a bar that opens a form). Dock pinned. Key bar. List count for the DLQ coloured |
| **Create** | differs | Frame only (the form is Sqs-03's, already compared). A ghost list row, italic, tagged `new` (page: none). A right panel "The call this makes": a live HTTP request with `POST /` and `X-Amz-Target`, "Copy as curl", "Copy the body", and a callout "Made here, kept here" (page: nothing on SQS; 30 call sites in 9 templates have a collapsible `cli` button that shows an `aws` command, 14 of them create forms). A key bar: ⌘⏎ create, tab next field, esc cancel (page: ⌘⏎ works; esc does nothing). "Free." under the name (a live availability check: not built; buildable by `GetQueueUrl`). "Made here, kept here" is true: `./template.yaml` is applied at boot (`docs/cli.md:21`). Light and dark both rendered |
| **Dark** | differs | Wire in dark, labelled rail, "The map" entry (page: no map), `IAM enforce` pill, `us-east-1`, an Appearance card (2.5), "What a theme never changes" card: the colours and the four words. Grounds neutral: the page's dark grounds are neutral too (`#131313`, `#1e1e1e` against `#111315`, `#191c1f`). "Sixteen marks" is true of the rail (16 services; STS has no entry). Sample row note corrected |
| **Narrow** | differs | Drawn at 1024 wide: rail 48, no list pane and a name switcher with "1 of 4 queues", an inspector of 340, the notice in the key bar "Built for 1024 and wider. Phones are not supported, and the console says so." The page at 1024: rail 56, list pane 232, no notice (it shows at 1023 and below, above the panes, "The console is built for a window 1024 px wide or more, and phones are not supported. Everything still works here, but it will not fit well."). The board draws a held-by-you list and an inspector with "Delete · it is done" and "Let it go"; those two phrases are on no other board, and the page's Consume tab says "Delete selected" and "Release selected" (Sqs-11 is "Held by you. Nobody else sees these until they come back."). Tabs corrected |
| **Palette** | differs | See 2.11. Keyboard flow matches; sections, count, underline, footer legend, typed create entry, width 700 do not. The "Create `new`" entry is partly built: the entries exist, the typed name is not carried. Backdrop is the wire with a 48 px rail (Main draws 196) |
| **States** | differs | See 2.12. Toast, confirm shape, empty, slow bar match; inline refusal is pre-empted by native validation; four answers against five states and no "it threw"; the cancel word. Four facts corrected (3.2 s, the VisibilityTimeout sentence now the code's, "AWS's code and AWS's sentence", the empty copy) |
| **Teaching** | matches | All five lines are on the SQS forms: label with unit ("Visibility seconds"), the echo ("1 minute", "none"), the sentence, "0 – 43 200 · default 30 · enforced" (checked: 0 to 43,200 and default 30 are in the model), and the live neighbour sentence ("Its timeout is 30 s and a received message stays hidden 60 s, so one run finishes before the message returns." is `sqs.html:343` verbatim). One nuance: the board labels line 1 "Visibility · s"; the page writes "Visibility seconds" |
| **Connect** | differs | Three columns (CLI profile | Environment and Terraform | Deploy and Check) against two columns and a full-width Deploy; the Check button below the fold on the page, level with Deploy on the board; the page has a "then" block (`aws --profile doze s3 ls`) the board omits; the Terraform block is complete on the page and abbreviated on the board (`endpoints { s3 = … }`); the page's block clips with an inner scroll. Text: "Every block is the same fact" against "Every block below is the same fact"; "It does not appear on the wire" against "It will not appear on the wire". The result ARN was `…:root` on the board and is `arn:aws:iam::000000000000:user/test` from the page: corrected. The "endpoint_url … needs AWS CLI v2.13 or newer" sentence is on both and sourced on neither |
| **Fidelity** | differs | Title "SQS · what is real here" with an `esc` (page: "Fidelity", no esc); 940 x 760 (page: `dialog wide`, max 520 px, so the operations column wraps mid-word, "GetQueueAttribut es"); ⓘ beside the name (page: list header); a "Differences from AWS" section, honestly tagged `new here` (not built: `info.go` renders only the ledger table; the text exists in `docs/SUPPORT.md`). The three SQS rows were stale (ListQueues "no pagination", StartMessageMoveTask "completes synchronously ... DestinationArn required", CancelMessageMoveTask "always 'task is not active'"): **corrected** to the ledger. "18 functional" is right |
| **Grounding** | design is wrong (corrected) | Four stale statements: toasts "Today they last 3.2 s" (7 s, 10 s), "Create from ⌘K ... the palette does not offer them yet" (it does, without the name), "Refused is purple, Denied is amber. Today the console has them the other way round" (built, A3), and the map's "five kinds of wire" (seven; the nodes include tables and streams). All corrected. Nothing to build |
| **Map** | matches (built 2026-10-20, D-f) | `/_console/map`, `internal/console/map.go`, `templates/map.html`. Boxes are links with the AWS icon, wires labelled with the seven kinds, flows named "from <the resource that feeds the rest>" (the board said "loyalty points", which no data names), hot from the ring (D6: a call to either end in the last minute, tagged `new`). **Corrected on the board:** Flow 2 (a Kinesis stream and a DynamoDB table feeding one function) so all seven node kinds are drawn; only `notify` is dark, because the fixture reads the statements bucket and `Hot` is a call to an end. Harness: 1 of 1, one allow-list line (the board's own "new page" tag) |
| **Map-Large** | matches (built 2026-10-20, D-f) | The same page past 12 wired resources: one card a flow, hot flows first, the fold, Hot only, find. **Corrected on the board:** "nightly-forecast" shared a function with the statements flow, which connected components forbid, so it is the stream-and-table flow; "a handful" is 12; "harbour-archive" (a table) is among the unwired; cards are in the page's order, so the fold holds the smallest: 33 shown + four flows of 2 = 41. Harness: 1 of 1, the same one allow-list line |
| **Bad-DLQ** | differs | The shell as Queue. "nothing reads this queue — wire a Lambda trigger to close the loop" is the old sentence (page: "no Lambda trigger reads this queue — wire one to close the loop"); **corrected**. The explanatory callout ("These failed to be deleted after 3 receives on …. Open one to read it. Redrive moves all of them back to that queue.") is not on the page. Rows show "received 3 of 3" and the page shows no pill on a dead-lettered row: a move resets the receive count here (sqs.md "reset to 0 here; not established"; the model's description says "across all queues", so this may be a divergence; the SQS owner's open item `test_redrive_receive_count`). "Redrive → harbour-checkout-orders" matches |
| **Bad-Down** | cannot match yet | Not built. Stopped the server with the page open and waited 9 s: no banner, no toast, no change. There is no "cannot reach"/"not answering"/"last heard" anywhere in `shell.js`. Browser-side, buildable: the live regions already poll (`setupLive`), so a failed poll is detectable. The board's key bar "last heard 14:52:10" is the same thing |
| **Wire-Empty** | differs | Board: H1 "Nothing has called yet." at 28 px, one sentence, two cards ("1 · Point your code at it" with the env block and Copy; "2 · Or make something from here" with Create a bucket, Create a queue, Create a table, Deploy a CloudFormation template), a "waiting for a call…" strip, rail 196 with `quiet`, key bar with `? every key`. Page: the usual header, then "Nothing has happened here yet. Point an SDK at 127.0.0.1:14851 and every call it makes appears below, with the work each call caused nested underneath it." and three links ("How to connect", "bucket", "queue", "function"), then the filter chips and an empty table that says "No calls yet. Point an SDK or the CLI at … and watch them stream in." (two empty messages stacked). No env block on the page |
| **Wire-Inspect** | differs | Inspector 520, Request / Response / "What it caused 1" tabs, "Copy as curl" and "Copy the body", a "paused · 31 new" pill with Resume (page: one scrolling pane of Request and Response sections, a small "curl" copy button, no tabs; pause toggles "resume" with no count; the nested call is a row in the list, not a tab). Request id in the header line (page: in the Response section's note). Rail 48 (page 208). Keys j k c → esc (mostly unbound, 2.4). The board's sentence "Only work doze-aws did itself is nested here. What your function calls from inside, with an SDK, arrives as its own call." is true of the page (cascade rows only for `Parent != 0`) |
| **Wire-Refused** | differs | Inspector 560 with the error callout, "What it was sent", "The queue it was sent to", "The same operation, in the ledger" (page: the inspector shows the state label, the message, and a one-line "why" with a link for Denied; no queue card, no ledger card). The scenario was **wrong**: a `MessageGroupId` on a standard queue is accepted here (verified, a `SequenceNumber` comes back), so this call would not be a 400. Corrected to `MessageDeduplicationId`, which is refused with `InvalidParameterValue`: "The request include parameter that is not valid for this queue type: MessageDeduplicationId belongs to FIFO queues." (`sqs/store.go:394`; wording `memory`, as sqs.md section 8 records) |

## 4. Where the design is wrong

Each of these was verified, and the facts in 1 to 6 were corrected in the
working copy (the list is at the end of this section).

1. **Anatomy contradicts the boards it summarises.** Rule 3 (Delete under
   `···`): 2 boards draw `···`, 119 draw Delete beside. Rule 4 (no strip): 28
   boards draw `.facts`, which `con.css` defines for them. Rule 1 (marks only
   inside a service) is the one the boards follow; rule 7 and the callout
   ("Selecting anything opens it on the right ... Nothing opens ... at the foot
   of the page") disagree with Queue.dc.html, which expands a message in place.
   The brief says "Panels open inline". These are not facts about the
   console; they are the design disagreeing with itself and need a decision
   (D2), not an edit.
2. **A 400 for `MessageGroupId` on a standard queue** on Anatomy, Main, Dark
   and Wire-Refused. Accepted here since SQS-L4 (fair queues); the plan already
   corrected Sqs-09 and missed these four.
3. **"Today it stays 3.2 seconds"** (States, Grounding): 7 s and 10 s since A12.
4. **"Refused is purple, Denied is amber. Today ... the other way round"**
   (Grounding): built (A3).
5. **"The palette does not offer them yet"** (Grounding): it offers each
   catalogue entry's create page.
6. **"Five kinds of wire", five node kinds** (Grounding, Map): seven and seven.
7. **"AWS's code and AWS's sentence"** (States): the SQS service model has no
   range traits, so the sentence is the emulator's own, derived from the API
   reference (`SUPPORT.md`: "The rules are hand-derived, because the model has
   none"). The range, 0 to 43,200, is in the model; the wording is not
   established as AWS's.
8. **Four answers** (States): five states exist (Refused, Refused here,
   Denied, Not built, Failed). "Not built" is a state a user meets (the
   `501`s); "Refused here" is the one that says "AWS would accept this". Not
   corrected (it is a design addition, not a fact edit); D7.
9. **"A dark wire carried traffic recently"** (Map): not what `Hot` is.
10. **The region is drawn on six boards and the IAM mode on eight**, with no
    rule for when. The five Denied boards draw `IAM enforce` and Anatomy and
    Narrow draw `IAM soft`; 330 boards in soft mode draw nothing.
11. **Boards that name keys with no binding** (2.4) and a "paused · 31 new"
    counter and a "live · 234 a minute" pill with no data source: the rate is
    available (the deck shows "5/min"); the paused counter needs a sequence
    diff the poll does not keep. Neither is tagged `new`, which the brief
    requires of anything not in the console today. The right panel's
    "alarm firing", "call refused" and "function error" callouts, the time
    lanes, and the `live`/`quiet` rail annotation are untagged too.
12. **L12 names "the list pane header"** as a place the icon stays; no board
    draws one there and the page hides it by CSS.
13. **S3 notifies a FIFO queue** (Main, Wire-Inspect: `PutObject` then
    `└ SendMessage harbour-depot-dispatch.fifo via s3:ObjectCreated:Put`). AWS
    does not deliver a bucket's notifications to a FIFO queue and refuses to
    configure one; doze-aws accepts the configuration and the delivery is then
    refused (`The request must contain the parameter MessageGroupId`). The
    fidelity fixture notifies a standard queue, `harbour-depot-dispatch`, and the
    one board line is allow-listed (`decision`). *Product gap found:* the
    configuration should be refused (S3 stage).
14. **The validator's `Invoke` nested under the app's `SendMessage`** (Main,
    Wire-Inspect: `└ Invoke harbour-order-validator via the queue's event source
    mapping`, and "What it caused 1"). The nesting is carried on the message
    (`AWSTraceHeader`), which an SDK's `SendMessage` does not set, so doze-aws
    cannot know the mapping's invoke came of that send; guessing would draw a
    confident line it cannot know. The invoke was not on the wire at all (the
    trace package says it "appears as a root"; it did not, and now does), and it
    is a root row beside the send. The card's sentence is allow-listed
    (`decision`). *Open question for the owner:* SQS could keep the cause on the
    message for a send made through the recorder without putting it in the
    message's system attributes a consumer reads back; that is a storage change
    in SQS, not a console one.
15. **"Refused" is one chip on the board and two states in the page**: "refused"
    and "refused here" (D7). The Needs-a-look callout says each in its own words
    ("The same request would fail on AWS." / "AWS would accept it. This is a
    limit of doze-aws."); the board draws only the first.
16. **The three inspector widths** (Wire-Inspect 520, Wire-Refused 560, Narrow
    340): one width, 520, and 400 at 1100 px and below, so that the table's
    On column stays readable at the width the console is declared for.

**Edits made to the working copy** (originals kept as
`scratchpad/shell/*.orig.html`; nothing else in the repo was touched):

| File | Change |
|---|---|
| Anatomy | tabs "Settings, Access" to "Consume, Configuration"; wire row note to "MessageDeduplicationId belongs to FIFO queues" |
| Narrow | tabs "Settings, Access" to "Consume, Configuration" |
| Main, Dark | refused row note to "MessageDeduplicationId belongs to FIFO queues" |
| Wire-Refused | callout to "The request include parameter that is not valid for this queue type: MessageDeduplicationId belongs to FIFO queues."; body field `MessageDeduplicationId` |
| States | toast sentence (7 s, 10 s, held on pointer or focus); the refusal sentence to the code's "Value for parameter VisibilityTimeout is invalid. Reason: must be between 0 and 43200."; the card note "The code and the sentence the service answered with ..."; the empty copy to "No visible messages. / Send one below — it lands here." |
| Bad-DLQ | neighbours sentence to "no Lambda trigger reads this queue — wire one to close the loop" |
| Connect | result ARN to `arn:aws:iam::000000000000:user/test` |
| Fidelity | ListQueues, StartMessageMoveTask and CancelMessageMoveTask notes to the current ledger text |
| Grounding | the map's node and wire kinds; the toast, create-from-⌘K and refused/denied lines |
| Map | legend: added `dlq` and `dest` |

## 5. Build list, in order

Days are one person, including the rendered pair in light and dark and at
1024 and 900, the accessibility sweep (`e2e/audit/crawl.ts`) on every page the
piece touches, the route gate, and the e2e edits. The SQS plan was low by
about 3x; these are not. A line says which decision it waits on.

**Shell (foundation C)**

| # | Piece | Files | Days | Waits on |
|---|---|---|---|---|
| S1 | Tokens and type: the design's colour values, IBM Plex Sans and Mono subsets in place of Inter, re-record the binary-size and idle budgets | `static/app.css` (`:root`, dark blocks, `@font-face`), `static/*.woff2`, `binarysize_test.go` baseline | 1.5 | D3 |
| S2 | Top bar: 44 px, instance name, the jump text and 420 px, endpoint, region chip, IAM mode pill | `templates/layout.html`, `console.go` (`Options.Name`; an IAM mode accessor, cached), `cmd/doze-aws/stack.go`, `static/app.css` | 1 | D9 |
| S3 | Rail: default by page (wire wide, services slim), the user's choice wins, auto-slim kept, call-open collapses on the wire, status dots | `layout.html`, `static/shell.js` (`setRail`), `static/app.css`, `glance.go` and `/api/counts` (a `warn` field) | 1.5 | D1 |
| S4 | List pane: plural heading, `New` text, filter kept, coloured count, ghost row hook, the key line removed | `templates/panes.html` (`lp_head`, `lp_foot`), `catalog.go` (`Plural`), each service's list partial for the colour (the SQS one now; the rest in their stages), `copy.go` | 1.5 | |
| S5 | The global key bar, per-page key sets, only bound keys named; bind `Escape` to close the inspector and `data-esc-back` on create pages | `layout.html` (`shell_bottom`), `static/shell.js`, `static/app.css`, every `templates/*.html` page's `.panes` | 2 | D10 |
| S6 | `res_header` takes a primary, a `···` menu if D2 says so, and `Facts` or `Sentence`; SQS adopts | `templates/kit.html`, `templates/sqs.html`, `console/res_wire_test.go`-style test, `internal/settings` untouched | 1.5 | D2 |
| S7 | The dock: pinned, live pill, collapsed form at 1024 | `kit.html` (`res_wire`, `res_wire_rows`), `traffic.go` (`/traffic/for`), `static/app.css` | 1.5 | |
| S8 | States: `novalidate` on kit forms, "Keep it" for destructive confirms, the five answers with the state's word on the wire row, function-error capture | `kit.html` (`setting_field`), `create.html` (forms), `static/shell.js` (about line 478), `handlers_surfaces.go` (`stateCopyFor`, `factsOfEntry`), `traffic.go` (`X-Amz-Function-Error`), `templates/traffic.html` | 1.5 | D7, D8, D11 |
| S9 | Palette: sections, count, underline, footer legend, typed create entry, width | `static/shell.js`, `layout.html`, `static/app.css`, a generic `?name=` prefill in `create.html` | 1.5 | |
| S10 | Create frame: ghost row, the right panel, keys | `create.html` (`form_actions`), `panes.html`, `shell.js`, the `cli` component in `panes.html` | 2 | D4 |
| S11 | 1024: the list pane as a name switcher; the dead 900 px rule fixed | `static/app.css` (line 984 versus 1039), `panes.html` | 1.5 | D5 |
| S12 | Whole-console re-check: 17 services' pages in two themes at 1440, 1024, 900; the axe sweep; the e2e specs `shell.spec.ts`, `layout.spec.ts`, `traffic.spec.ts` | `e2e/tests/*`, `e2e/audit/crawl.ts` | 4 | |
| | **Shell total** | | **21** | |

**Cross-cutting pages (foundation D)**

| # | Piece | Files | Days | Waits on |
|---|---|---|---|---|
| D-a | The wire to Main and Wire-*: header row, lanes, segmented filter, right panel, inspector tabs, paused count, wire keys | `templates/traffic.html`, `panes.html` (`deck`), `traffic.go`, `handlers_surfaces.go`, `glance.go` (alarm, refused, function error), `static/shell.js`, `static/app.css` | 5 | D7 |
| D-b | Wire first run | `panes.html` (`deck` first-run branch), `templates/traffic.html` | 1 | |
| D-c | Fidelity dialog: 940, title, ⓘ placement, "Differences from AWS" | `panes.html` (`info_button`, `info_panel`), `info.go`, `docs/` (a `Differences` reader beside `Rows`), `app.css` | 2 | D12 |
| D-c, built | (as above) | `docs.Differences`, `docs.Extensions`, `info.go`, `panes.html` (`info_button`, `info_open`, `info_dialog`, `info_panel`), `kit.html` (`res_header` holds the ⓘ), `sqs.html` (one key, `Selected`), `app.css` (`.fid-*`), `e2e/fidelity/allow/{Fidelity,Kms-19-Fidelity}.txt` | done 2026-10-20 | |
| D-d | Connect: three columns, Check beside Deploy, the `then` and Terraform blocks as the board has them | `templates/connect.html`, `handlers_connect.go`, `app.css` | 0.5 | |
| D-d, built | the `then` block is dropped (the board omits it), the Terraform block is whole (the board's ellipsis is allow-listed as a decision), the account line under the result is dropped | `connect.html`, `handlers_connect.go`, `app.css` (`.cn-*`), `e2e/fidelity/allow/Connect.txt` | done 2026-10-20 | |
| D-e | "Cannot reach" banner, last-heard, Try now | `static/shell.js` (`setupLive`), `layout.html`, `app.css` | 1 | |
| D-f | The map and the map at scale (**built 2026-10-20**: `map.go`, `templates/map.html`, `static/app.css`, `e2e/tests/map.spec.ts`, `e2e/fidelity/mapfixture.ts`; a CSS-and-SVG layout, no `graph.js`) | `routes.go`, `routes_stackwide.go`, `testdata/routes.txt`, `handlers_surfaces.go`, a new `templates/map.html`, `client_flow.go` (hot from the ring, `Unwired` with names), `static/graph.js` or a CSS layout, the rail entry, `e2e/tests` | 6 | D6 |
| | **Pages total** | | **15.5** | |

About 36 days; 28 if the decisions fall on the cheap side (keep Inter, no
switcher, strip allowed), 45 if the map needs a real layout engine and D5 is
taken as written.

## 6. Decisions for a human

**D1. The default rail.** The design's rule (labelled on the wire, marks inside
a service) against the page's (208 labelled everywhere, collapse on request).
The page's rule was set for a reason that is still true: with the AWS icons
the marks are indistinguishable without names. *Recommendation:* the
design's rule as the default, the user's toggle and the flyout kept and
persisted, 56 px not 48, and the wire with a call open collapsing to marks as
Wire-Inspect draws it. This is the user's stated rule ("followed absolutely")
with the one deviation the icons force, written down here.

**D2. The detail header.** Delete beside the name (119 boards, the kit, the
page) or under `···` (Anatomy, Narrow); facts as a sentence (Anatomy, SQS) or
a strip (28 boards, the kit). *Recommendation:* Delete beside, confirmed, and
fix rule 3; the kit takes `Sentence` or `Facts`, SQS uses the sentence its
boards draw, and a service whose boards draw the strip keeps it. Say both in
rule 4. This ends the "shell decision, not an SQS one" that the SQS table left
open.

**D3. Typeface and tokens.** IBM Plex and the design's colours against Inter
and the page's. *Recommendation:* follow the design: embed Plex Sans 400/500/600
and Mono 400/500, Latin subset, and drop Inter. The size has not been
measured and it must be, because the idle heap and binary size are
CI-enforced; if the Plex subsets are not smaller than Inter's 352 KB, say so
and decide again. The colour tokens are a few steps apart and cost an
afternoon.

**D4. What the Create panel shows.** The board draws the HTTP request and
"Copy as curl"; the page's idiom is the `aws` command behind a `cli` button,
tested by `TestEveryCLIPreviewMatchesItsForm`, and Grounding lists the CLI
preview as the thing that exists. *Recommendation:* the panel shows the `cli`
preview, open by default on create pages, titled "The call this makes", with
"Copy the command"; edit the board's two buttons. The HTTP body is a second
generator per form for no reader the CLI line does not serve. The SQS create
form must get its preview (it has none).

**D5. 1024.** The board replaces the list pane with a switcher below some
width; the page keeps the pane (a navigation model with `j`/`k`).
*Recommendation:* follow the board at 1100 px and below; it is the width the
console is declared for, and the board draws it. Costs the `j`/`k` list cursor
there, which the switcher's own keys replace.

**D6. The map's "hot".** The graph's `Hot` is "an edge into a queue with
depth". The boards say traffic. *Recommendation:* hot from the ring (a call to
either end in the last minute), tagged `new`, and keep `Hot` as "has a
backlog", shown differently. The Flows page's deletion rationale (`panes.html`:
"a synoptic view you have to navigate to is the thing that got the flows page
deleted") is overruled by the design; say so in the commit.

**D7. Five answers.** The board has four and an "it threw". The page has five
and no "threw". *Recommendation:* add "Refused here" and "Not built" to the
board, build "it threw" (the header is already set by the service), and put
the state's word in the row so the colour is never the only signal, which the
board itself requires.

**D8. Native validation.** *Recommendation:* `novalidate` on kit forms, so the
refusal the service gives is the one shown and the form keeps what was typed
(the States board's card 2 and the Teaching board's "a hint is never a
gate"). `min`, `max` and `pattern` stay for the spinners and for line four.

**D9. The top bar's IAM pill and instance name.** *Recommendation:* the
instance name on every page (a new `Options.Name`), and the mode on every
page, always, in one wording ("IAM soft", "IAM enforce", "IAM off"); the
"· recording" suffix on Anatomy is dropped. *2026-10-20: reversed for the suffix.
The design is followed: "IAM soft · recording" above 1100 px (Anatomy), "IAM soft"
at 1100 and below (Narrow).*

**D10. `?`.** *Recommendation:* remove it from Wire-Empty's key bar. No
board draws what it shows.

**D11. The cancel word.** *Recommendation:* "Keep it" on every destructive
confirm by default, "Cancel" otherwise, set in `shell.js` from
`parts.destructive` rather than at 95 call sites.

**D12. Where ⓘ lives.** *Recommendation:* in the resource header beside the
name, as Fidelity draws it, and in the list header when nothing is selected.
Rewrite L12's list-pane wording to match the boards.
*Built for SQS through the kit's `res_header` (2026-10-20); a service whose header
has not adopted the kit keeps the button in the list header, where the sixteen
Kms boards draw it, until its stage adopts `res_header`.*

## Appendix: bugs found on the way (not fixed; this file is the only edit)

| Where | What |
|---|---|
| `internal/console/static/app.css` 984 versus 1039 | the 900 px list-pane rule never applies; the pane is 232 px at 900 and at 768 |
| `internal/console/static/shell.js:691` | `Escape` reads `[data-esc-back]`, which no template sets |
| `internal/console/templates/kit.html` `setting_field` | `min`/`max` turn a server refusal into a browser bubble |
| `internal/console/templates/panes.html:286` | the ⓘ title reads "What of sqs is real here" (the service key, not its name) |
| `internal/console/traffic.go` | `X-Amz-Function-Error` is not read; a failing function is a green 200 on the wire |
| `internal/console/templates/layout.html` `.narrow-note` | fixed 2026-10-20: it is the key bar's `.keys-note`, in the board's words, at 1024 and below |
| First-run wire | two empty messages are drawn, "Nothing has happened here yet." and "No calls yet." |
| The palette | typing a name offers no create entry, only a phrase that matches a catalogue label ("create q") |
