// The contact sheet: one row per board, the board on the left and the page on
// the right, then the two texts. Pure: results in, a string out.

import type { TextCheck } from './text';
import { normaliseLine } from './text';

export type Status =
  | 'pass' // driven, and every line of the board's text is on the page (or allowed)
  | 'text' // driven, and the page lacks sentences the board has
  | 'nodriver' // no driver for this board: a failing row
  | 'orphan' // a driver for a board that is gone: a failing row
  | 'render' // the board did not render
  | 'drive' // the driver threw
  | 'skip'; // an explicit, reasoned skip

export const FAILING: Status[] = ['text', 'nodriver', 'orphan', 'render', 'drive'];

export interface Row {
  board: string;
  status: Status;
  /** One line: the skip reason, the error, "no driver for ...". */
  detail: string;
  boardPng?: string; // file name, relative to report.html
  pagePngs: string[];
  boardText: string;
  pageText: string;
  check?: TextCheck;
  notes: string[];
}

export interface Meta {
  service: string;
  variant: string; // "light 1440", "dark 1440", "light 900 id2"...
  generated: string;
  origin: string;
}

export function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

export function counts(rows: Row[]): Record<Status, number> {
  const c: Record<Status, number> = { pass: 0, text: 0, nodriver: 0, orphan: 0, render: 0, drive: 0, skip: 0 };
  for (const r of rows) c[r.status]++;
  return c;
}

export function failing(rows: Row[]): Row[] {
  return rows.filter((r) => FAILING.includes(r.status));
}

const LABEL: Record<Status, string> = {
  pass: 'matches',
  text: 'text differs',
  nodriver: 'NO DRIVER',
  orphan: 'ORPHAN DRIVER',
  render: 'RENDER FAILED',
  drive: 'DRIVE FAILED',
  skip: 'skipped',
};

/** The board's text with the lines the page lacks marked. */
function boardColumn(r: Row): string {
  const miss = new Set(r.check?.missing ?? []);
  const allow = new Map((r.check?.allowed ?? []).map((a) => [a.line, a.reason]));
  return r.boardText
    .split('\n')
    .filter((l) => l.trim() !== '')
    .map((l) => {
      const n = normaliseLine(l);
      if (miss.has(n)) return `<span class="miss">${esc(l)}</span>`;
      if (allow.has(n)) return `<span class="allow" title="${esc(allow.get(n)!)}">${esc(l)}</span>`;
      return esc(l);
    })
    .join('\n');
}

function summary(r: Row): string {
  const out: string[] = [];
  const c = r.check;
  // The count is in the heading below when there is a list to go with it.
  if (r.detail && !(c && c.missing.length)) out.push(`<p class="detail">${esc(r.detail)}</p>`);
  if (c) {
    if (c.missing.length) {
      out.push(`<p class="h">The page lacks ${c.missing.length} of ${c.checked} board lines</p><ul class="missing">${c.missing.map((m) => `<li>${esc(m)}</li>`).join('')}</ul>`);
    }
    if (c.allowed.length) {
      out.push(`<p class="h">Allowed (${c.allowed.length})</p><ul class="allowed">${c.allowed.map((a) => `<li>${esc(a.line)} <i># ${esc(a.reason)}</i></li>`).join('')}</ul>`);
    }
    for (const e of c.allowErrors) out.push(`<p class="warn">allowlist: ${esc(e)}</p>`);
    for (const e of c.unusedAllow) out.push(`<p class="warn">allowlist line ${e.at} matches nothing on the board: ${esc(e.line)}</p>`);
    for (const e of c.needlessAllow) out.push(`<p class="warn">allowlist line ${e.at} is no longer needed, the page has it: ${esc(e.line)}</p>`);
  }
  for (const n of r.notes) out.push(`<p class="note">${esc(n)}</p>`);
  return out.join('');
}

export function renderReport(rows: Row[], meta: Meta): string {
  const c = counts(rows);
  const bad = failing(rows).length;
  const body = rows
    .map((r) => {
      const imgs = (files: string[], alt: string) =>
        files.map((f) => `<a href="${esc(f)}" target="_blank"><img loading="lazy" src="${esc(f)}" alt="${esc(alt)}"></a>`).join('');
      return `<tr id="${esc(r.board)}" class="${r.status}">
<th scope="row"><a href="#${esc(r.board)}">${esc(r.board)}</a><span class="badge ${r.status}">${LABEL[r.status]}</span>${summary(r)}</th>
<td class="img">${r.boardPng ? imgs([r.boardPng], 'board ' + r.board) : '<em>no board image</em>'}</td>
<td class="img">${r.pagePngs.length ? imgs(r.pagePngs, 'page ' + r.board) : '<em>no page image</em>'}</td>
<td><pre>${boardColumn(r)}</pre></td>
<td><pre>${esc(r.pageText)}</pre></td>
</tr>`;
    })
    .join('\n');
  return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Fidelity ${esc(meta.service)}</title>
<style>
:root{--bg:#f6f6f3;--card:#fff;--ink:#17191c;--mut:#565b63;--line:#d9d8d2;--ok:#166b3f;--bad:#b3251b;--warn:#855000;--badbg:#f8e3e0;--okbg:#e3f1e8;--warnbg:#f6ecd6}
@media (prefers-color-scheme:dark){:root{--bg:#111315;--card:#191c1f;--ink:#e9eaeb;--mut:#a3a9b0;--line:#2d3136;--ok:#5fca8f;--bad:#ff8a7e;--warn:#e0b060;--badbg:#3a1a17;--okbg:#17301f;--warnbg:#33290f}}
body{margin:0;background:var(--bg);color:var(--ink);font:13px/1.45 system-ui,sans-serif}
header{padding:16px 20px;border-bottom:1px solid var(--line)}
h1{margin:0 0 4px;font-size:18px}
.sum{color:var(--mut)}
table{border-collapse:collapse;width:100%;table-layout:fixed}
th,td{vertical-align:top;border-bottom:1px solid var(--line);padding:10px;text-align:left}
th{width:230px;font-weight:600}
th a{color:inherit}
td.img{width:30%}
td.img img{width:100%;height:auto;border:1px solid var(--line);margin-bottom:6px;display:block}
pre{margin:0;white-space:pre-wrap;word-break:break-word;font:11px/1.45 ui-monospace,Menlo,monospace;max-height:560px;overflow:auto}
.badge{display:block;width:max-content;margin:4px 0 6px;padding:1px 8px;border-radius:10px;font-size:11px;font-weight:600}
.badge.pass{background:var(--okbg);color:var(--ok)}
.badge.skip{background:var(--warnbg);color:var(--warn)}
.badge.text,.badge.nodriver,.badge.orphan,.badge.render,.badge.drive{background:var(--badbg);color:var(--bad)}
.miss{background:var(--badbg);color:var(--bad)}
.allow{background:var(--warnbg);color:var(--warn)}
ul{margin:2px 0 6px;padding-left:16px;font-weight:400}
.missing li{color:var(--bad)}
.allowed li{color:var(--warn)}
.h{margin:8px 0 0;font-weight:600}
.detail,.note{font-weight:400;color:var(--mut);margin:4px 0}
.warn{font-weight:400;color:var(--warn);margin:4px 0}
@media (max-width:1100px){th{width:160px}}
</style></head><body>
<header>
<h1>Fidelity: ${esc(meta.service)} <span class="sum">(${esc(meta.variant)})</span></h1>
<div class="sum">${rows.length} boards. ${c.pass} match, ${c.text} text differs, ${c.nodriver + c.orphan} undriven, ${c.render + c.drive} failed to render or drive, ${c.skip} skipped. ${bad ? `<b style="color:var(--bad)">${bad} failing.</b>` : '<b style="color:var(--ok)">none failing.</b>'}<br>
Board left, page right; the text columns are what each draws, extracted from the DOM, the board's marked where the page lacks the line (red) or an allowlist covers it (amber). Generated ${esc(meta.generated)} against ${esc(meta.origin)}.</div>
</header>
<table>
<thead><tr><th>Board</th><th>Board render</th><th>Real page</th><th>Board text</th><th>Page text</th></tr></thead>
<tbody>
${body}
</tbody></table>
</body></html>
`;
}

/** `annotation 12, data 2`: how many allowed lines of each reason kind (the word before the first colon). */
export function kindTally(reasons: string[]): string {
  const n = new Map<string, number>();
  for (const r of reasons) {
    const k = r.includes(':') ? r.slice(0, r.indexOf(':')).trim() : 'unkinded';
    n.set(k, (n.get(k) ?? 0) + 1);
  }
  return [...n].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([k, c]) => `${k} ${c}`).join(', ');
}
