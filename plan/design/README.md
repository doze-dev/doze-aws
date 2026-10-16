# The console design, as boards

369 files: one `.dc.html` per board (each opens under `file://` at 1440 by 900;
`con.css` is the design's stylesheet and sits beside them) and `canvas.json`,
the canvas index. The canvas at <https://claude.ai/artifact/1WSqsF1GUQJdrhscUzzcTw>
holds the same files as published on 2026-10-11; the copy here is the one the
plan works from, and the one that is corrected.

## Where this came from

On 2026-10-16 the working copy that held the corrections (about 126 boards,
edited by the fifteen pre-stage reviews and the shell review on 2026-10-11 and
12) was found wiped from the session's scratch directory. These files were
restored from the canvas, which has the boards as they were **before** those
corrections, except nine SQS boards that survived. Each service document's
"6a" section lists the board facts that were corrected; they are being
re-applied from those lists (see the commit log of this directory). A board
that still carries one of the listed stale facts is a defect in this copy, not
in the plan.

`BRIEF.md`, `FINDINGS.md` and `RAIL.html` (the grounding notes and the rail
snippet the per-service boards were made from) were not on the canvas and are
gone; `plan/00-foundation.md` and `plan/03-shell-fidelity.md` carry what they
decided.
