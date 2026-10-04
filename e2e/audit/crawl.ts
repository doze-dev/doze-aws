// Mechanical UX crawl of the console against a seeded stack — run it before a
// release, not in CI (it takes minutes and its findings want a human's eye).
//
//   doze-aws --data-dir /tmp/harbour &  (cd ../../demo && bun seed.ts --fast)
//   ORIGIN=http://127.0.0.1:4566 bun audit/crawl.ts
//
// Follows the console's own links from the home page and, on every page, at two
// viewports and two themes, records JS errors, console errors, failed or 4xx/5xx
// same-origin requests, horizontal overflow, and (desktop/light) axe violations.
// Writes audit/out/report.json and screenshots of every page. Expect two benign
// findings on S3 object previews: the preview iframe is sandboxed with a CSP on
// purpose, so it logs blocked scripts and inline styles.
import { chromium, type Page } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';

const ORIGIN = process.env.ORIGIN ?? 'http://127.0.0.1:4566';
const ROOT = `${ORIGIN}/_console/`;
const OUT = import.meta.dir + '/out';
const AXE = import.meta.dir + '/../node_modules/axe-core/axe.min.js';
const MAX_PAGES = Number(process.env.MAX_PAGES ?? 500);
const PER_SHAPE = 3;
mkdirSync(OUT + '/shots', { recursive: true });

const VIEWPORTS = { desktop: { width: 1440, height: 900 }, mobile: { width: 768, height: 900 } };
const THEMES = ['light', 'dark'] as const;

// A page's shape: its path with the parts that name a resource collapsed, so a
// hundred objects in one bucket count as one page to look at.
function shape(u: URL): string {
  const segs = u.pathname.replace(/^\/_console\/?/, '').split('/').filter(Boolean);
  const keyed = segs.map((s, i) => (i === 0 ? s : /^(details|tags|records|logs|executions|versions|aliases|config|policy|access|metrics|monitor|events|resources|outputs|template|changeset|execution|version|alias|alarm|rule|archive|replay|destinations|connection|destination|plans|activities|layers|role|user|group|policy|profile|key|stream|record|route|stage|authorizer|graph)$/.test(s) ? s : '*'));
  const q = [...u.searchParams.keys()].filter((k) => ['tab', 'view', 'mode'].includes(k)).map((k) => `${k}=${u.searchParams.get(k)}`);
  return keyed.join('/') + (q.length ? '?' + q.join('&') : '');
}

type Finding = { url: string; shape: string; vp: string; theme: string; kind: string; detail: string };
const findings: Finding[] = [];
const pages: { url: string; shape: string; status: number; title: string; ms: number }[] = [];

const browser = await chromium.launch();

async function visit(url: string, vp: keyof typeof VIEWPORTS, theme: string, withAxe: boolean, shot: string | null) {
  const ctx = await browser.newContext({ viewport: VIEWPORTS[vp] });
  await ctx.addInitScript((t) => { try { localStorage.setItem('theme', t); } catch {} }, theme);
  const page = await ctx.newPage();
  const sh = shape(new URL(url));
  const add = (kind: string, detail: string) => findings.push({ url, shape: sh, vp, theme, kind, detail: detail.slice(0, 400) });
  page.on('pageerror', (e) => add('js-error', String(e)));
  page.on('console', (m) => { if (m.type() === 'error') add('console-error', m.text()); });
  page.on('requestfailed', (r) => { if (r.url().startsWith(ORIGIN) && !/aborted/i.test(r.failure()?.errorText ?? '')) add('request-failed', `${r.method()} ${r.url()} ${r.failure()?.errorText}`); });
  page.on('response', (r) => { if (r.url().startsWith(ORIGIN) && r.status() >= 400) add('http-' + r.status(), `${r.request().method()} ${r.url()}`); });
  const t0 = Date.now();
  let status = 0, links: string[] = [], title = '';
  try {
    const resp = await page.goto(url, { waitUntil: 'networkidle', timeout: 20000 }).catch(async () => page.waitForLoadState('load').then(() => null));
    status = resp?.status() ?? 0;
    await page.waitForTimeout(400);
    title = await page.title();
    const overflow = await page.evaluate(() => {
      const d = document.scrollingElement!;
      if (d.scrollWidth <= window.innerWidth + 1) return null;
      // Name the widest offenders so the finding is actionable.
      const wide = [...document.querySelectorAll('body *')].filter((e) => {
        const r = e.getBoundingClientRect();
        return r.right > window.innerWidth + 1 && getComputedStyle(e).position !== 'fixed';
      }).slice(0, 4).map((e) => e.tagName.toLowerCase() + (e.id ? '#' + e.id : '') + (e.className && typeof e.className === 'string' ? '.' + e.className.trim().split(/\s+/).slice(0, 2).join('.') : ''));
      return `${d.scrollWidth}px > ${window.innerWidth}px: ${wide.join(', ')}`;
    });
    if (overflow) add('h-overflow', overflow);
    if (withAxe) {
      await page.addScriptTag({ path: AXE });
      const res: any = await page.evaluate(async () => (window as any).axe.run(document, { resultTypes: ['violations'] }));
      for (const v of res.violations) {
        if (v.impact === 'minor') continue;
        add(`axe-${v.impact}`, `${v.id}: ${v.help} (${v.nodes.length}) e.g. ${v.nodes.slice(0, 6).map((n: any) => n.target.join(' ')).join(' | ')}`);
      }
    }
    if (shot) await page.screenshot({ path: `${OUT}/shots/${shot}.png`, fullPage: false });
    links = await page.$$eval('a[href]', (as) => as.map((a) => (a as HTMLAnchorElement).href));
  } catch (e) {
    add('crawl-error', String(e));
  }
  const ms = Date.now() - t0;
  await ctx.close();
  return { status, links, title, ms };
}

const seen = new Set<string>();
const perShape = new Map<string, number>();
const queue: string[] = [ROOT];
let n = 0;
while (queue.length && n < MAX_PAGES) {
  const url = queue.shift()!;
  const u = new URL(url);
  u.hash = '';
  const key = u.toString();
  if (seen.has(key)) continue;
  seen.add(key);
  const sh = shape(u);
  if ((perShape.get(sh) ?? 0) >= PER_SHAPE) continue;
  perShape.set(sh, (perShape.get(sh) ?? 0) + 1);
  n++;
  const first = perShape.get(sh) === 1;
  const id = String(n).padStart(3, '0') + '_' + sh.replace(/[^a-z0-9]+/gi, '_').slice(0, 60);
  const main = await visit(key, 'desktop', 'light', first, first ? id + '_desktop_light' : null);
  pages.push({ url: key, shape: sh, status: main.status, title: main.title, ms: main.ms });
  if (first) {
    await visit(key, 'desktop', 'dark', false, id + '_desktop_dark');
    await visit(key, 'mobile', 'light', false, null);
    await visit(key, 'mobile', 'dark', false, id + '_mobile_dark');
  }
  for (const l of main.links) {
    if (!l.startsWith(ROOT) || /\/static\/|download|\.zip|export/.test(l)) continue;
    queue.push(l);
  }
  if (n % 25 === 0) console.log(`${n} pages, ${findings.length} findings, queue ${queue.length}`);
}
await browser.close();
writeFileSync(`${OUT}/report.json`, JSON.stringify({ pages, findings }, null, 1));
console.log(`done: ${n} pages, ${new Set(pages.map((p) => p.shape)).size} shapes, ${findings.length} findings`);
