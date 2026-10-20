import { describe, expect, test } from 'bun:test';
import { checkText, containsText, maskTimes, normaliseLine, parseAllow, toLines } from './text';

describe('normaliseLine', () => {
  test('collapses whitespace, including no-break spaces, and lower-cases', () => {
    expect(normaliseLine('  1 024   –  Queue  ')).toBe('1 024 – queue');
  });

  test('straightens typographic quotes', () => {
    expect(normaliseLine('Delete queue “harbour”? It’s gone')).toBe('delete queue "harbour"? it\'s gone');
  });

  test('masks ports, so a board drawn on :4566 matches a page on any port', () => {
    expect(normaliseLine('127.0.0.1:4566')).toBe(normaliseLine('127.0.0.1:14962'));
    expect(normaliseLine('url http://127.0.0.1:14960/x')).toBe('url http://127.0.0.1:<port>/x');
  });

  test('masks a bare port too: the narrow top bar draws ":4566" with no host', () => {
    expect(normaliseLine(':4566')).toBe(normaliseLine(':15180'));
    expect(normaliseLine(':15180')).toBe(':<port>');
    // ...but not a time, a ratio or a label with a colon in it.
    expect(normaliseLine('Hidden for another 0:52')).toBe('hidden for another <clock>');
    expect(normaliseLine('ratio 1:2')).toBe('ratio 1:2');
    expect(normaliseLine('Sent to: harbour')).toBe('sent to: harbour');
  });

  test('masks uuids, which are random on every run', () => {
    expect(normaliseLine('4f2c1e77-9a0b-4c1d-8d12-3b6a0c5ebd8f')).toBe('<id>');
  });

  test('a key cap matches whether or not a space follows it', () => {
    expect(normaliseLine('⌘⏎sends · lands in the list above')).toBe(normaliseLine('⌘⏎ sends · lands in the list above'));
  });

  test('rewrites the page identity to the board identity, only when told the page ran under another', () => {
    const arn = 'arn:aws:sqs:eu-west-1:111122223333:q';
    expect(normaliseLine(arn)).toBe(arn);
    expect(normaliseLine(arn, { page: { account: '111122223333', region: 'eu-west-1' } })).toBe('arn:aws:sqs:us-east-1:000000000000:q');
  });
});

describe('maskTimes', () => {
  test('masks the time-like text a page prints and a board draws by hand', () => {
    expect(maskTimes('14:36:21.402 SendMessage')).toBe('<clock> SendMessage');
    expect(maskTimes('sent 9:41 pm')).toBe('sent <clock>');
    expect(maskTimes('2026-10-16T12:03:04.123Z')).toBe('<ts>');
    expect(maskTimes('16 Oct 2026')).toBe('<date>');
    expect(maskTimes('12 s ago')).toBe('<ago>');
    expect(maskTimes('2 min ago')).toBe('<ago>');
    expect(maskTimes('just now')).toBe('<ago>');
  });

  test('leaves durations that are settings alone', () => {
    expect(maskTimes('hidden for 60 s on receive')).toBe('hidden for 60 s on receive');
    expect(maskTimes('kept 4 days')).toBe('kept 4 days');
  });

  test('a board time and a page time compare equal', () => {
    expect(normaliseLine('12 s ago')).toBe(normaliseLine('just now'));
    expect(normaliseLine('14:36:21.402')).toBe(normaliseLine('16:48:55.879'));
  });
});

describe('toLines', () => {
  test('drops blank lines and normalises each line', () => {
    expect(toLines('A  b\n\n\n  C\n')).toEqual(['a b', 'c']);
  });
});

describe('containsText', () => {
  test('finds a phrase inside a longer line', () => {
    expect(containsText('a message lands here', 'lands here')).toBe(true);
  });

  test('does not find a number inside a longer number', () => {
    expect(containsText('13 waiting', '3 waiting')).toBe(false);
    expect(containsText('3 waiting', '3 waiting')).toBe(true);
  });

  test('does not find a word inside a name', () => {
    expect(containsText('harbour-order-validator', 'order')).toBe(false);
    expect(containsText('an order arrived', 'order')).toBe(true);
  });

  test('a needle that begins or ends in punctuation is not held to a word edge', () => {
    expect(containsText('x → y', '→')).toBe(true);
    expect(containsText('see harbour-orders-dlq', '…-orders-dlq')).toBe(false);
  });
});

describe('parseAllow', () => {
  test('reads text and reason, splitting on the last " # "', () => {
    const { entries, errors } = parseAllow('# a comment\n\nSelect # to move # the page words it differently\n');
    expect(errors).toEqual([]);
    expect(entries.map((e) => [e.line, e.reason])).toEqual([['select # to move', 'the page words it differently']]);
  });

  test('a line with no reason is an error, not an entry', () => {
    const { entries, errors } = parseAllow('Nobody said why\n');
    expect(entries).toEqual([]);
    expect(errors).toHaveLength(1);
    expect(errors[0]).toContain('no reason');
  });

  test('an empty reason is an error', () => {
    expect(parseAllow('text #  \n').errors.length + parseAllow('text # \n').errors.length).toBeGreaterThan(0);
  });
});

describe('checkText', () => {
  test('passes when every board line is on the page, in any order, with more on the page', () => {
    const r = checkText('Send a message\nlands in the list above', 'Chrome\nlands in the list above\nSend a message\nextra');
    expect(r.missing).toEqual([]);
    expect(r.checked).toBe(2);
  });

  test('prints the board sentences the page lacks', () => {
    const r = checkText('Purge all messages?\nIt is gone for good.', 'Purge all messages?');
    expect(r.missing).toEqual(['it is gone for good.']);
  });

  test('a sentence the page splits across lines still counts as present', () => {
    // flex children: "Visibility" and "30 s" are separate lines on the page, one on the board
    const r = checkText('Visibility 30 s', 'Visibility\n30 s');
    expect(r.missing).toEqual([]);
  });

  test('is case-insensitive: innerText applies text-transform', () => {
    expect(checkText('OTHER REFUSALS', 'Other refusals').missing).toEqual([]);
  });

  test('a repeated board line is asked about once', () => {
    const r = checkText('Release\nRelease\nRelease', 'nothing');
    expect(r.missing).toEqual(['release']);
    expect(r.checked).toBe(1);
  });

  test('times and ports are masked on both sides', () => {
    const r = checkText('12 s ago\n127.0.0.1:4566', 'just now\n127.0.0.1:14961');
    expect(r.missing).toEqual([]);
  });

  test('the allowlist covers a line, and says why', () => {
    const r = checkText('🗑\nSend', 'Send', '🗑 # icon: the page draws an SVG\n');
    expect(r.missing).toEqual([]);
    expect(r.allowed).toEqual([{ line: '🗑', reason: 'icon: the page draws an SVG' }]);
  });

  test('an allowlist does not hide a line it does not name', () => {
    const r = checkText('🗑\nA sentence', 'nothing', '🗑 # icon\n');
    expect(r.missing).toEqual(['a sentence']);
  });

  test('an allow entry that matches nothing on the board is reported', () => {
    const r = checkText('Send', 'Send', 'A line the board never had # why\n');
    expect(r.unusedAllow.map((e) => e.line)).toEqual(['a line the board never had']);
  });

  test('an allow entry for a line the page now has is reported as no longer needed', () => {
    const r = checkText('Send', 'Send', 'Send # once missing\n');
    expect(r.needlessAllow.map((e) => e.line)).toEqual(['send']);
    expect(r.missing).toEqual([]);
  });

  test('allowlist syntax errors come back with the result', () => {
    const r = checkText('Send', 'Send', 'no reason here\n');
    expect(r.allowErrors).toHaveLength(1);
  });

  test('under another identity the page is compared as the board is drawn', () => {
    const board = 'arn:aws:sqs:us-east-1:000000000000:harbour-checkout-orders';
    const page = 'arn:aws:sqs:eu-west-1:111122223333:harbour-checkout-orders';
    expect(checkText(board, page).missing).toHaveLength(1);
    expect(checkText(board, page, '', { pageIdentity: { account: '111122223333', region: 'eu-west-1' } }).missing).toEqual([]);
  });
});
