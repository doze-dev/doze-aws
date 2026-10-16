// The pure half of the structural check: no browser, no files, no clock.
//
// The check is NOT a pixel diff. It reads the text a board shows and the text
// the real page shows and asks one question: is every line of the board's text
// somewhere in the page's text? A sentence, a callout or a confirm body that
// the board has and the page lacks is found by this, without a person diffing
// two images. The person still reads the pair.
//
// What it deliberately does not do:
//   - It never fails a page for having MORE text than the board (the page has
//     real data, the AWS icons' labels, toasts).
//   - It does not compare order or layout. Shape is a human reading of the
//     two pictures.
//   - It is case-insensitive, because innerText applies text-transform and a
//     board that uppercases a caption in CSS would otherwise differ from a page
//     that does not. A case-only difference is for the human.

export interface Identity {
  account: string;
  region: string;
}

/** What a board is drawn under. A page booted under another identity is
 *  rewritten to this before comparing, so --id2 is not "every ARN differs". */
export const BOARD_IDENTITY: Identity = { account: '000000000000', region: 'us-east-1' };

const MONTHS = 'jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec';

/** Time-like text is masked on both sides: the page prints times the board
 *  drew by hand, and no hook exists to freeze the server's clock. */
export function maskTimes(s: string): string {
  return (
    s
      // 2026-10-16T12:03:04.123Z, 2026-10-16 12:03
      .replace(/\b\d{4}-\d{2}-\d{2}[T ]\d{1,2}:\d{2}(?::\d{2})?(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?/gi, '<ts>')
      // 16 Oct 2026, Oct 16, 2026
      .replace(new RegExp(`\\b\\d{1,2} (?:${MONTHS})[a-z]* \\d{4}\\b`, 'gi'), '<date>')
      .replace(new RegExp(`\\b(?:${MONTHS})[a-z]* \\d{1,2},? \\d{4}\\b`, 'gi'), '<date>')
      .replace(/\b\d{4}-\d{2}-\d{2}\b/g, '<date>')
      // 12:03:04, 9:41 pm
      .replace(/\b\d{1,2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?: ?[ap]m)?\b/gi, '<clock>')
      // 5s ago, 2 min ago, 3 hours ago, just now, a moment ago
      .replace(/\b(?:\d+\s?(?:s|sec|secs|seconds?|m|min|mins|minutes?|h|hr|hrs|hours?|d|days?)|just now|a moment|a few seconds|an? (?:second|minute|hour|day)) ago\b/gi, '<ago>')
      .replace(/\bjust now\b/gi, '<ago>')
  );
}

export interface NormaliseOpts {
  /** The identity the PAGE ran under; rewritten to BOARD_IDENTITY. */
  page?: Identity;
}

/** One line of text, comparable: nbsp and runs of whitespace collapsed,
 *  typographic quotes straightened, ports and times masked, lower-cased. */
export function normaliseLine(s: string, opts: NormaliseOpts = {}): string {
  let t = s
    .replace(/[    ]/g, ' ')
    .replace(/[‘’]/g, "'")
    .replace(/[“”]/g, '"')
    .replace(/\s+/g, ' ')
    .trim();
  if (opts.page) {
    const { account, region } = opts.page;
    if (account) t = t.split(account).join(BOARD_IDENTITY.account);
    if (region) t = t.split(region).join(BOARD_IDENTITY.region);
  }
  t = t.replace(/(?:127\.0\.0\.1|localhost):\d+/g, '127.0.0.1:<port>');
  // Message ids and request ids are random on every run.
  t = t.replace(/\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi, '<id>');
  // A key cap sits against the word it names with no space on a board and with
  // one on a page (`<kbd>⌘⏎</kbd> sends`): the glyph is what is compared.
  t = t.replace(/\s*([⌘⏎⌥⇧⌃↵⎋])\s*/g, '$1');
  t = maskTimes(t);
  return t.toLowerCase();
}

/** Split extracted text into normalised, non-empty lines. */
export function toLines(text: string, opts: NormaliseOpts = {}): string[] {
  return text
    .split(/\n+/)
    .map((l) => normaliseLine(l, opts))
    .filter((l) => l.length > 0);
}

// A hyphen joins the parts of a name (harbour-order-validator), so "order"
// is not found inside it.
const WORD = /[\p{L}\p{N}_-]/u;

/** Is `needle` somewhere in `hay`, not as part of a longer word? "3" in "13"
 *  does not count; "send" in "send a message" does. */
export function containsText(hay: string, needle: string): boolean {
  if (needle === '') return true;
  let from = 0;
  for (;;) {
    const i = hay.indexOf(needle, from);
    if (i < 0) return false;
    const before = i === 0 ? '' : hay[i - 1]!;
    const afterIdx = i + needle.length;
    const after = afterIdx >= hay.length ? '' : hay[afterIdx]!;
    const startWord = WORD.test(needle[0]!);
    const endWord = WORD.test(needle[needle.length - 1]!);
    const okBefore = !startWord || before === '' || !WORD.test(before);
    const okAfter = !endWord || after === '' || !WORD.test(after);
    if (okBefore && okAfter) return true;
    from = i + 1;
  }
}

export interface AllowEntry {
  /** Normalised board text. */
  line: string;
  reason: string;
  /** 1-based line in the file, for messages. */
  at: number;
}

/**
 * Parse an allowlist file. One entry per line: `board text # reason`. The
 * reason is required; an entry without one is an error, because an allowlist
 * line nobody explained is a missing sentence nobody decided on. Blank lines
 * and lines starting with `#` are comments. The split is on the LAST ` # `, so
 * board text may itself contain a `#`.
 */
export function parseAllow(content: string): { entries: AllowEntry[]; errors: string[] } {
  const entries: AllowEntry[] = [];
  const errors: string[] = [];
  content.split('\n').forEach((raw, idx) => {
    const l = raw.trim();
    if (l === '' || l.startsWith('#')) return;
    const cut = l.lastIndexOf(' # ');
    if (cut < 0) {
      errors.push(`line ${idx + 1}: no reason after "#": ${l}`);
      return;
    }
    const text = l.slice(0, cut).trim();
    const reason = l.slice(cut + 3).trim();
    if (text === '' || reason === '') {
      errors.push(`line ${idx + 1}: empty text or reason: ${l}`);
      return;
    }
    entries.push({ line: normaliseLine(text), reason, at: idx + 1 });
  });
  return { entries, errors };
}

export interface TextCheck {
  /** Board lines checked (after allowlisting). */
  checked: number;
  /** Board lines the page lacks and nothing allows. These fail the row. */
  missing: string[];
  /** Board lines the page lacks that an allow entry covers. */
  allowed: { line: string; reason: string }[];
  /** Allow entries that matched nothing on the board. */
  unusedAllow: AllowEntry[];
  /** Allow entries whose text the page DOES have: the entry is no longer needed. */
  needlessAllow: AllowEntry[];
  allowErrors: string[];
}

/**
 * Is every line of the board's text in the page's text, modulo the allowlist?
 * `boardText` and `pageText` are raw innerText; both are normalised here.
 */
export function checkText(
  boardText: string,
  pageText: string,
  allowContent = '',
  opts: { pageIdentity?: Identity } = {},
): TextCheck {
  const boardLines = toLines(boardText);
  const pageLines = toLines(pageText, { page: opts.pageIdentity });
  // Lines are joined with a space, so a sentence the page splits across flex
  // children ("Visibility" / "30 s") still matches the board's single line.
  const hay = pageLines.join(' ');
  const { entries, errors } = parseAllow(allowContent);

  const missing: string[] = [];
  const allowed: { line: string; reason: string }[] = [];
  const used = new Set<AllowEntry>();
  const seen = new Set<string>();
  let checked = 0;
  for (const line of boardLines) {
    if (seen.has(line)) continue; // a repeated line is asked about once
    seen.add(line);
    checked++;
    if (containsText(hay, line)) continue;
    const a = entries.find((e) => e.line === line);
    if (a) {
      used.add(a);
      allowed.push({ line, reason: a.reason });
    } else {
      missing.push(line);
    }
  }
  const boardSet = new Set(boardLines);
  return {
    checked,
    missing,
    allowed,
    unusedAllow: entries.filter((e) => !used.has(e) && !boardSet.has(e.line)),
    needlessAllow: entries.filter((e) => !used.has(e) && boardSet.has(e.line)),
    allowErrors: errors,
  };
}
