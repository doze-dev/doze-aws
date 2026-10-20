import { describe, expect, test } from 'bun:test';
import { readdirSync } from 'node:fs';
import { join } from 'node:path';
import { allBoards, boardName, boardsFor, coverage, resolveTarget, serviceOf, SERVICES, SHELL } from './boards';
import { DRIVERS } from './drivers';
import type { DriverTable } from './drivers/types';
import { parseArgs, variantOf } from './run';

const files = [
  'Sqs-01-None.dc.html',
  'Sqs-02-Pick.dc.html',
  'Queue.dc.html',
  'Bad-DLQ.dc.html',
  'Bad-Down.dc.html',
  'Main.dc.html',
  'S3-01-None.dc.html',
  'Sm-01-None.dc.html',
  'Ssm-01-None.dc.html',
  'con.css',
  'canvas.json',
];

describe('which boards exist', () => {
  test('only .dc.html files are boards', () => {
    expect(boardName('Sqs-01-None.dc.html')).toBe('Sqs-01-None');
    expect(boardName('con.css')).toBeNull();
    expect(allBoards(files)).not.toContain('canvas');
  });

  test('a service owns its prefix and its named extras', () => {
    expect(boardsFor('sqs', files)).toEqual(['Bad-DLQ', 'Queue', 'Sqs-01-None', 'Sqs-02-Pick']);
  });

  test('a prefix is not a prefix of another service (Sm, Ssm, S3, Sqs)', () => {
    expect(boardsFor('sm', files)).toEqual(['Sm-01-None']);
    expect(boardsFor('ssm', files)).toEqual(['Ssm-01-None']);
    expect(boardsFor('s3', files)).toEqual(['S3-01-None']);
  });

  test('a board with no service is the shell\'s, so a new cross-cutting board has to be driven too', () => {
    expect(serviceOf('Main')).toBe(SHELL);
    expect(serviceOf('Bad-Down')).toBe(SHELL);
    expect(boardsFor(SHELL, files)).toEqual(['Bad-Down', 'Main']);
    expect(serviceOf('Brand-New-Board')).toBe(SHELL);
  });
});

describe('resolveTarget', () => {
  test('a service key', () => {
    const t = resolveTarget('sqs', files);
    expect(t).toEqual({ label: 'sqs', service: 'sqs', boards: boardsFor('sqs', files) });
  });

  test('a board name, in any case, belongs to its service', () => {
    const t = resolveTarget('sqs-02-pick', files);
    expect(t).toEqual({ label: 'Sqs-02-Pick', service: 'sqs', boards: ['Sqs-02-Pick'] });
    expect(resolveTarget('queue', files)).toEqual({ label: 'Queue', service: 'sqs', boards: ['Queue'] });
  });

  test('something else is an error that says what would work', () => {
    const t = resolveTarget('nope', files);
    expect(typeof t).toBe('string');
    expect(t as string).toContain('sqs');
    expect(t as string).toContain('shell');
  });
});

describe('coverage: a board with no driver is a failing row, never a silent one', () => {
  const table: DriverTable = { 'Sqs-01-None': { drive: async () => {} }, Gone: { skip: 'was renamed' } };

  test('lists every board that has no driver', () => {
    expect(coverage(['Sqs-01-None', 'Sqs-02-Pick', 'Queue'], table).undriven).toEqual(['Sqs-02-Pick', 'Queue']);
  });

  test('lists every driver whose board is gone', () => {
    expect(coverage(['Sqs-01-None'], table).orphaned).toEqual(['Gone']);
  });

  test('an empty table fails every board', () => {
    expect(coverage(['A', 'B'], {}).undriven).toEqual(['A', 'B']);
  });

  test('a skip counts as having an entry: it is visible in the report, not silent', () => {
    expect(coverage(['Gone'], table)).toEqual({ undriven: [], orphaned: ['Sqs-01-None'] });
  });
});

describe('the real tables against the real boards', () => {
  const dir = join(import.meta.dir, '..', '..', 'plan', 'design', 'boards');
  let real: string[] = [];
  try {
    real = readdirSync(dir);
  } catch {
    /* no design checkout */
  }
  const have = real.length > 0;

  test.skipIf(!have)('SQS has an entry for every one of its 19 boards, and none for a board that is gone', () => {
    const boards = boardsFor('sqs', real);
    expect(boards.length).toBe(19);
    expect(coverage(boards, DRIVERS.sqs!)).toEqual({ undriven: [], orphaned: [] });
  });

  test.skipIf(!have)('the shell table covers the cross-cutting boards, with the two undrivable ones as explicit skips', () => {
    const boards = boardsFor(SHELL, real);
    expect(coverage(boards, DRIVERS.shell!)).toEqual({ undriven: [], orphaned: [] });
    for (const name of ['Bad-Down', 'Grounding']) {
      const d = DRIVERS.shell![name];
      expect(d && 'skip' in d && d.skip.length > 10).toBe(true);
    }
  });

  test.skipIf(!have)('every other service has an empty table, so every one of its boards fails with no driver', () => {
    for (const s of SERVICES) {
      if (s.key === 'sqs') continue;
      // KMS's stage has not started, but its Fidelity board is the shell's
      // dialog (foundation D-c), and is driven with it.
      const driven = s.key === 'kms' ? ['Kms-19-Fidelity'] : [];
      expect(Object.keys(DRIVERS[s.key]!)).toEqual(driven);
      const boards = boardsFor(s.key, real);
      expect(boards.length).toBeGreaterThan(0);
      expect(coverage(boards, DRIVERS[s.key]!).undriven).toEqual(boards.filter((b) => !driven.includes(b)));
    }
  });

  test('every service in the table has a driver file registered', () => {
    for (const s of SERVICES) expect(DRIVERS[s.key]).toBeDefined();
    expect(DRIVERS[SHELL]).toBeDefined();
  });
});

describe('the command line', () => {
  test('a target and its flags', () => {
    expect(parseArgs(['sqs', '--dark', '--id2'])).toEqual({ target: 'sqs', flags: { dark: true, w900: false, id2: true } });
    expect(parseArgs(['--', 'sqs'])).toEqual({ target: 'sqs', flags: { dark: false, w900: false, id2: false } });
  });

  test('no target, two targets and unknown flags are refused', () => {
    expect(typeof parseArgs([])).toBe('string');
    expect(typeof parseArgs(['sqs', 's3'])).toBe('string');
    expect(parseArgs(['sqs', '--wide'])).toBe('unknown flag --wide');
  });

  test('the output directory is named by the variant', () => {
    expect(variantOf({ dark: false, w900: false, id2: false })).toBe('');
    expect(variantOf({ dark: true, w900: true, id2: false })).toBe('dark-w900');
    expect(variantOf({ dark: false, w900: false, id2: true })).toBe('id2');
  });
});
