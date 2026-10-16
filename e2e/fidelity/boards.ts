// Which boards exist, which service owns each, and whether every one has a
// driver. Pure: the caller lists the directory and passes the file names in,
// so the coverage rule is testable without a design checkout.
//
// The list of boards comes from the directory, never from a table here. A new
// board with no driver therefore appears as a failing row instead of being
// silently absent (plan/00-foundation B14).

import type { Driver, DriverTable } from './drivers/types';

export interface ServiceDef {
  /** The word after `console:fidelity --`. */
  key: string;
  /** Board files are `<Prefix>-*.dc.html`. */
  prefix: string;
  /** Boards that belong to the service but do not carry its prefix. */
  extra?: string[];
}

/** The sixteen services, in the order the rail lists them. */
export const SERVICES: ServiceDef[] = [
  { key: 's3', prefix: 'S3' },
  { key: 'ddb', prefix: 'Ddb' },
  // Queue is the SQS Messages tab and Bad-DLQ is its dead-letter queue: both
  // were drawn before the service-prefixed names and kept theirs.
  { key: 'sqs', prefix: 'Sqs', extra: ['Queue', 'Bad-DLQ'] },
  { key: 'sns', prefix: 'Sns' },
  { key: 'eb', prefix: 'Eb' },
  { key: 'kinesis', prefix: 'Kinesis' },
  { key: 'lambda', prefix: 'Lambda' },
  { key: 'apigw', prefix: 'Apigw' },
  { key: 'sfn', prefix: 'Sfn' },
  { key: 'logs', prefix: 'Logs' },
  { key: 'cw', prefix: 'Cw' },
  { key: 'kms', prefix: 'Kms' },
  { key: 'sm', prefix: 'Sm' },
  { key: 'ssm', prefix: 'Ssm' },
  { key: 'iam', prefix: 'Iam' },
  { key: 'cfn', prefix: 'Cfn' },
];

/** The key for the boards that belong to no service. */
export const SHELL = 'shell';

export const BOARD_SUFFIX = '.dc.html';

/** `Sqs-01-None.dc.html` -> `Sqs-01-None`; anything else -> null. */
export function boardName(file: string): string | null {
  return file.endsWith(BOARD_SUFFIX) ? file.slice(0, -BOARD_SUFFIX.length) : null;
}

export function allBoards(files: string[]): string[] {
  return files.map(boardName).filter((n): n is string => n !== null).sort();
}

function ownedBy(name: string): ServiceDef | undefined {
  return SERVICES.find((s) => name.startsWith(s.prefix + '-') || (s.extra ?? []).includes(name));
}

/** Which service a board belongs to (SHELL for the cross-cutting ones). */
export function serviceOf(name: string): string {
  return ownedBy(name)?.key ?? SHELL;
}

/** The boards of one service (or of the shell), sorted. */
export function boardsFor(key: string, files: string[]): string[] {
  return allBoards(files).filter((n) => serviceOf(n) === key);
}

export interface Target {
  /** Names the output directory. */
  label: string;
  /** The service whose driver table applies. */
  service: string;
  boards: string[];
}

/**
 * What the command line named: a service key (`sqs`), `shell`, or one board
 * (`Sqs-17-Config`, matched without regard to case). Returns an error string
 * for anything else, with what would have worked.
 */
export function resolveTarget(arg: string, files: string[]): Target | string {
  const a = arg.trim();
  const keys = [...SERVICES.map((s) => s.key), SHELL];
  const key = keys.find((k) => k === a.toLowerCase());
  if (key) return { label: key, service: key, boards: boardsFor(key, files) };
  const board = allBoards(files).find((n) => n.toLowerCase() === a.toLowerCase());
  if (board) return { label: board, service: serviceOf(board), boards: [board] };
  return `no service or board called "${arg}". Services: ${keys.join(', ')}. A board is a file name in plan/design/boards without ${BOARD_SUFFIX}.`;
}

export interface Coverage {
  /** Boards with no driver entry at all. Each is a failing row. */
  undriven: string[];
  /** Driver entries for boards that no longer exist. Also failing: a renamed
   *  board must not leave its old driver quietly passing. */
  orphaned: string[];
}

export function coverage(boards: string[], table: DriverTable): Coverage {
  const have = new Set(Object.keys(table));
  const want = new Set(boards);
  return {
    undriven: boards.filter((b) => !have.has(b)),
    orphaned: [...have].filter((b) => !want.has(b)).sort(),
  };
}

export function isSkip(d: Driver): d is { skip: string } {
  return 'skip' in d;
}
