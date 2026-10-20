// The fidelity gate, as machinery (plan/00-foundation B14).
//
//   bun e2e/fidelity/run.ts <service|board-name> [--dark] [--w900] [--w1024] [--id2]
//   go tool task console:fidelity -- sqs
//
// For every board of the service: render the board (file:// of the .dc.html
// at 1440 by 900), drive the real page into the board's state, screenshot both
// at the same size, and write
//
//   e2e/.tmp/fidelity/<service>[-dark][-w900][-w1024][-id2]/
//       board-<name>.png  page-<name>.png  report.html  results.json
//
// Then the structural check: every line of the board's text must be somewhere
// in the page's text (modulo allow/<board>.txt, whose every line says why).
// It is not a pixel diff. The pair is still read by a person.
//
// It exits non-zero when any board has no driver, an orphaned driver, fails to
// render or to drive, or when the page lacks sentences the board has.

import { chromium, type Browser, type BrowserContext, type Page } from 'playwright';
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { allBoards, BOARDS_PATH, boardsDirProblem, boardsFor, coverage, isSkip, resolveTarget, SHELL } from './boards';
import type { Ctx, Driver, Inst } from './drivers/types';
import { DRIVERS } from './drivers';
import { seedHarbour } from './harbour';
import { seedMap, seedMapLarge } from './mapfixture';
import { seedWire } from './wire';
import { counts, failing, kindTally, renderReport, type Row } from './report';
import { boot, build, E2E_DIR, freePort, ID2, inst, REPO_DIR, TMP_DIR, type Booted } from './server';
import { checkText } from './text';

const BOARDS_DIR = join(REPO_DIR, ...BOARDS_PATH.split('/'));
const ALLOW_DIR = join(import.meta.dir, 'allow');

/** What the board draws that the page draws differently by design (the AWS
 *  icons for the two-letter marks) or not at all (the key bar and list pane
 *  belong to the shell boards). Hidden before the board's text is read. */
const MARKS = '.sv, .logo, .dot';
const SERVICE_CHROME = '.top, .rail, .keys, .list';

export interface Flags {
  dark: boolean;
  w900: boolean;
  w1024: boolean;
  id2: boolean;
}

export function parseArgs(argv: string[]): { target: string; flags: Flags } | string {
  const flags: Flags = { dark: false, w900: false, w1024: false, id2: false };
  const pos: string[] = [];
  for (const a of argv) {
    if (a === '--dark') flags.dark = true;
    else if (a === "--w900") flags.w900 = true;
    else if (a === "--w1024") flags.w1024 = true;
    else if (a === '--id2') flags.id2 = true;
    else if (a === '--') continue;
    else if (a.startsWith('--')) return `unknown flag ${a}`;
    else pos.push(a);
  }
  if (pos.length !== 1) return 'usage: bun e2e/fidelity/run.ts <service|board-name> [--dark] [--w900] [--w1024] [--id2]';
  return { target: pos[0]!, flags };
}

export function variantOf(f: Flags): string {
  return [f.dark ? 'dark' : '', f.w900 ? "w900" : "", f.w1024 ? "w1024" : "", f.id2 ? 'id2' : ''].filter(Boolean).join('-');
}

async function main(): Promise<number> {
  const parsed = parseArgs(process.argv.slice(2));
  if (typeof parsed === 'string') {
    console.error(parsed);
    return 2;
  }
  const { target: arg, flags } = parsed;
  // The boards are read from plan/design/boards in this repository and from
  // nowhere else. A missing or empty directory is a failure with a message that
  // says so, never a run that compares nothing and passes.
  const files = existsSync(BOARDS_DIR) ? readdirSync(BOARDS_DIR) : undefined;
  const problem = boardsDirProblem(files);
  if (problem || !files) {
    console.error(`FAIL: ${problem}`);
    return 2;
  }
  const t = resolveTarget(arg, files);
  if (typeof t === 'string') {
    console.error(t);
    return 2;
  }
  const table = DRIVERS[t.service] ?? {};
  const serviceBoards = boardsFor(t.service, files);
  const cov = coverage(serviceBoards, table);
  const variant = variantOf(flags);
  const outName = variant ? `${t.label}-${variant}` : t.label;
  const out = join(TMP_DIR, 'fidelity', outName);
  rmSync(out, { recursive: true, force: true });
  mkdirSync(out, { recursive: true });

  console.log(`fidelity ${t.label}: ${t.boards.length} board(s)${variant ? ` (${variant})` : ''} -> ${out}`);
  build();

  const taken = new Set<number>();
  const booted: Booted[] = [];
  const stopAll = async () => {
    await Promise.all(booted.map((b) => b.stop().catch(() => {})));
  };
  for (const sig of ['SIGINT', 'SIGTERM'] as const)
    process.on(sig, () => {
      void stopAll().finally(() => process.exit(130));
    });

  let browser: Browser | undefined;
  const rows: Row[] = [];
  try {
    const bootSeeded = async (tag: string, enforceMode: boolean, seed: boolean | 'wire' | ((i: Inst) => Promise<void>), name?: string, reader = false): Promise<Inst> => {
      const b = await boot({ port: await freePort(taken), id2: flags.id2, iamMode: enforceMode ? 'enforce' : undefined, tag, name });
      booted.push(b);
      const i = inst(b.server);
      if (typeof seed === 'function') await seed(i);
      else if (seed === 'wire') await seedWire(i, (s) => console.warn('  warning:', s));
      else if (seed) await seedHarbour(i, (s) => console.warn('  warning:', s), { reader });
      return i;
    };
    let readers = 0;
    const main = await bootSeeded('main', false, true);
    console.log(`booted ${main.server.origin} (account ${main.server.account}, ${main.server.region}), seeded harbour`);
    let enforce: Promise<Inst> | undefined;
    let empty: Promise<Inst> | undefined;
    let fresh: Promise<Inst> | undefined;
    let map: Promise<Inst> | undefined;
    let mapLarge: Promise<Inst> | undefined;
    let wire: Promise<Inst> | undefined;

    browser = await chromium.launch();
    const pageWidth = flags.w900 ? 900 : flags.w1024 ? 1024 : 1440;

    for (const board of t.boards) {
      rows.push(await runBoard(board));
    }

    async function runBoard(board: string): Promise<Row> {
      const row: Row = { board, status: 'pass', detail: '', pagePngs: [], boardText: '', pageText: '', notes: [] };
      const driver: Driver | undefined = table[board];

      // The board, always: even a row with no driver shows what is missing.
      const bctx = await browser!.newContext({ viewport: { width: 1440, height: 900 } });
      try {
        // Google Fonts: the board works offline on its fallbacks, and the
        // fallbacks make the picture the same on every machine.
        await bctx.route(/fonts\.(googleapis|gstatic)\.com/, (r) => r.abort());
        const bp = await bctx.newPage();
        await bp.goto(`file://${join(BOARDS_DIR, board + '.dc.html')}`, { waitUntil: 'load' });
        await bp.waitForSelector('.app', { timeout: 5000 });
        if (flags.dark) await bp.evaluate(() => document.querySelector('.app')!.classList.add('dark'));
        await bp.waitForTimeout(400);
        row.boardPng = `board-${board}.png`;
        await bp.screenshot({ path: join(out, row.boardPng) });
        const hide = t.service === SHELL ? MARKS : `${MARKS}, ${SERVICE_CHROME}`;
        row.boardText = await bp.evaluate((sel) => {
          document.querySelectorAll(sel).forEach((e) => ((e as HTMLElement).style.display = 'none'));
          return (document.querySelector('.app') as HTMLElement).innerText;
        }, hide);
      } catch (e) {
        row.status = 'render';
        row.detail = `the board did not render: ${msg(e)}`;
        await bctx.close();
        return row;
      }
      await bctx.close();

      if (!driver) {
        row.status = 'nodriver';
        row.detail = `no driver for ${board}`;
        return row;
      }
      if (isSkip(driver)) {
        row.status = 'skip';
        row.detail = `skip: ${driver.skip}`;
        return row;
      }

      const pctx = await browser!.newContext({ viewport: { width: pageWidth, height: 900 }, reducedMotion: 'reduce' });
      // The theme the run asked for, set before the page's own script reads it.
      // Only when nothing is stored: a driver that chooses a theme itself (the
      // Dark board) writes one, and an init script that wrote over it on the next
      // load was why the Dark board's page was always drawn light.
      await pctx.addInitScript((theme) => {
        try {
          if (!localStorage.getItem('theme')) localStorage.setItem('theme', theme);
        } catch {
          /* private window */
        }
      }, flags.dark ? 'dark' : 'light');
      const page = await pctx.newPage();
      const errs = new Set<string>();
      page.on('pageerror', (e) => errs.add(e.message));
      const shots: { file: string; text: string }[] = [];
      const ctx = makeCtx(page, main, shots, board, row);
      try {
        await driver.drive(ctx);
        if (!shots.some((s) => s.file === `page-${board}.png`)) await ctx.capture();
      } catch (e) {
        row.status = 'drive';
        row.detail = `the driver threw: ${msg(e)}`;
        // Whatever was captured before the throw is still worth seeing.
        try {
          if (shots.length === 0) await ctx.capture('failed');
        } catch {
          /* the page may be gone */
        }
      }
      await pctx.close();
      // The main picture first, the others after it in the order taken.
      row.pagePngs = [...shots].sort((a, b) => Number(b.file === `page-${board}.png`) - Number(a.file === `page-${board}.png`)).map((s) => s.file);
      row.pageText = shots.map((s) => s.text).join('\n\n');
      for (const e of errs) row.notes.push(`page error: ${e}`);
      if (driver.note) row.notes.push(driver.note);

      if (row.status === 'drive') return row;
      const allowFile = join(ALLOW_DIR, `${board}.txt`);
      const allow = existsSync(allowFile) ? readFileSync(allowFile, 'utf8') : '';
      row.check = checkText(row.boardText, row.pageText, allow, { pageIdentity: flags.id2 ? ID2 : undefined });
      if (row.check.missing.length > 0) {
        row.status = 'text';
        row.detail = `the page lacks ${row.check.missing.length} of ${row.check.checked} board lines`;
      }
      return row;
    }

    function makeCtx(page: Page, i: Inst, shots: { file: string; text: string }[], board: string, row: Row): Ctx {
      return {
        ...i,
        page,
        setEditor: (sel, v) => page.evaluate(([s, val]) => (window as any).dozeEditor.set(s, val), [sel, v] as const),
        settle: (ms = 500) => page.waitForTimeout(ms).then(() => {}),
        note: (s) => row.notes.push(s),
        capture: async (label) => {
          await page.waitForTimeout(500);
          // The unlabelled picture is the page's answer to the board; labelled
          // ones are further states of the same board.
          const file = label ? `page-${board}-${label}.png` : `page-${board}.png`;
          await page.screenshot({ path: join(out, file) });
          const text = await page.evaluate(() => document.body.innerText);
          shots.push({ file, text });
        },
        instance: (kind) => {
          if (kind === 'enforce') return (enforce ??= bootSeeded('enforce', true, true));
          // The first-run board is drawn on another shop ("shop"), which has not
          // been called yet; the instance is named so the page says the same.
          if (kind === 'empty') return (empty ??= bootSeeded('empty', false, false, 'shop'));
          if (kind === 'wire') return (wire ??= bootSeeded('wire', false, 'wire'));
          if (kind === 'fresh') return (fresh ??= bootSeeded('fresh', false, true));
          if (kind === 'map') return (map ??= bootSeeded('map', false, seedMap));
          if (kind === 'mapLarge') return (mapLarge ??= bootSeeded('map-large', false, seedMapLarge));
          // Never cached: the hold on the reader lasts HOLD_MS from the seed, so
          // each board that needs it boots its own, and drives inside the window.
          if (kind === 'reader') return bootSeeded(`reader${++readers}`, false, true, undefined, true);
          if (kind === 'own') return bootSeeded('own', false, true);
          throw new Error(`unknown instance ${kind}`);
        },
      };
    }
  } finally {
    await browser?.close().catch(() => {});
    await stopAll();
  }

  // Orphaned drivers are rows too (for a whole-service run).
  if (t.boards.length === serviceBoards.length) {
    for (const o of cov.orphaned)
      rows.push({
        board: o,
        status: 'orphan',
        detail: `driver for ${o}, but there is no ${o}.dc.html: a renamed or removed board`,
        pagePngs: [],
        boardText: '',
        pageText: '',
        notes: [],
      });
  }

  writeFileSync(join(out, 'report.html'), renderReport(rows, { service: t.label, variant: variant || 'light 1440', generated: new Date().toISOString(), origin: 'a fresh doze-aws' }));
  // The raw texts beside the pictures, for `diff` and for grep.
  for (const r of rows) {
    if (r.boardText) writeFileSync(join(out, `board-${r.board}.txt`), r.boardText);
    if (r.pageText) writeFileSync(join(out, `page-${r.board}.txt`), r.pageText);
  }
  writeFileSync(join(out, 'results.json'), JSON.stringify(rows.map(({ boardText, pageText, ...r }) => r), null, 1));
  return print(rows, out);
}

function msg(e: unknown): string {
  return (e instanceof Error ? e.message : String(e)).split('\n').slice(0, 3).join(' | ');
}

function print(rows: Row[], out: string): number {
  const c = counts(rows);
  for (const r of rows) {
    const tag = { pass: 'ok  ', text: 'TEXT', nodriver: 'NONE', orphan: 'ORPH', render: 'REND', drive: 'DRIV', skip: 'skip' }[r.status];
    console.log(`${tag}  ${r.board}${r.detail ? `  - ${r.detail}` : ''}`);
    if (r.check) {
      for (const m of r.check.missing) console.log(`        lacks: ${m}`);
      for (const a of r.check.allowed) console.log(`        allowed: ${a.line}  # ${a.reason}`);
      if (r.check.allowed.length > 0) console.log(`        allowed by kind: ${kindTally(r.check.allowed.map((a) => a.reason))}`);
      for (const e of r.check.allowErrors) console.log(`        allowlist: ${e}`);
      for (const e of r.check.unusedAllow) console.log(`        allowlist line ${e.at} matches nothing on the board: ${e.line}`);
      for (const e of r.check.needlessAllow) console.log(`        allowlist line ${e.at} no longer needed, the page has it: ${e.line}`);
    }
    for (const n of r.notes) console.log(`        note: ${n}`);
  }
  const bad = failing(rows).length;
  console.log(
    `\n${rows.length} boards: ${c.pass} match, ${c.text} text differs, ${c.nodriver + c.orphan} undriven, ${c.render + c.drive} failed to render or drive, ${c.skip} skipped.`,
  );
  console.log(`report: ${join(out, 'report.html')}`);
  if (bad > 0) console.log(`FAIL: ${bad} board(s) failing`);
  return bad > 0 ? 1 : 0;
}

if (import.meta.main) {
  main().then(
    (code) => process.exit(code),
    (e) => {
      console.error(e);
      process.exit(2);
    },
  );
}
