import { describe, expect, test } from 'bun:test';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { counts, esc, failing, renderReport, type Row } from './report';
import { checkText, parseAllow } from './text';

const row = (over: Partial<Row>): Row => ({ board: 'B', status: 'pass', detail: '', pagePngs: [], boardText: '', pageText: '', notes: [], ...over });

describe('report', () => {
  test('a row with no driver, an orphan, a render or drive failure, or missing text is failing; a skip and a pass are not', () => {
    const rows = [
      row({ board: 'a', status: 'pass' }),
      row({ board: 'b', status: 'skip' }),
      row({ board: 'c', status: 'nodriver' }),
      row({ board: 'd', status: 'orphan' }),
      row({ board: 'e', status: 'render' }),
      row({ board: 'f', status: 'drive' }),
      row({ board: 'g', status: 'text' }),
    ];
    expect(failing(rows).map((r) => r.board)).toEqual(['c', 'd', 'e', 'f', 'g']);
    expect(counts(rows)).toMatchObject({ pass: 1, skip: 1, nodriver: 1, orphan: 1, render: 1, drive: 1, text: 1 });
  });

  test('escapes what it prints', () => {
    expect(esc('<b>"x" & y</b>')).toBe('&lt;b&gt;&quot;x&quot; &amp; y&lt;/b&gt;');
  });

  test('the sheet has a row per board, both pictures, both texts, the missing line and the skip reason', () => {
    const board = 'Keep it\nIt is gone for good.';
    const page = 'Keep it';
    const html = renderReport(
      [
        row({ board: 'Sqs-16-Confirms', status: 'text', boardPng: 'board-Sqs-16-Confirms.png', pagePngs: ['page-Sqs-16-Confirms.png'], boardText: board, pageText: page, check: checkText(board, page) }),
        row({ board: 'Map', status: 'skip', detail: 'skip: no map page' }),
        row({ board: 'Sqs-99', status: 'nodriver', detail: 'no driver for Sqs-99' }),
      ],
      { service: 'sqs', variant: 'light 1440', generated: 'now', origin: 'x' },
    );
    expect(html).toContain('board-Sqs-16-Confirms.png');
    expect(html).toContain('page-Sqs-16-Confirms.png');
    expect(html).toContain('<span class="miss">It is gone for good.</span>');
    expect(html).toContain('skip: no map page');
    expect(html).toContain('no driver for Sqs-99');
    expect(html).toContain('2 failing');
  });
});

// The allowlists are data, and the rule for them is in text.ts: a line with a
// reason or it is an error. Checked here for every file in the repo.
describe('allow/*.txt', () => {
  const dir = join(import.meta.dir, 'allow');
  const files = existsSync(dir) ? readdirSync(dir).filter((f) => f.endsWith('.txt')) : [];

  test('every entry in every file has text and a reason', () => {
    for (const f of files) {
      const { errors } = parseAllow(readFileSync(join(dir, f), 'utf8'));
      expect({ file: f, errors }).toEqual({ file: f, errors: [] });
    }
  });

  test('each file is named for a board', () => {
    const boards = join(import.meta.dir, '..', '..', 'plan', 'design', 'boards');
    if (!existsSync(boards)) return;
    const have = new Set(readdirSync(boards).map((f) => f.replace(/\.dc\.html$/, '')));
    for (const f of files) expect(have.has(f.replace(/\.txt$/, ''))).toBe(true);
  });
});
