// The route gate: every console route is driven by a browser test, or says
// why not.
//
// The console registers ~370 routes and the Go tests reach most handlers
// directly, so a route can work in Go and still be unreachable from the page
// that is meant to call it — a form posting to the wrong path, a button whose
// hx-post nobody clicks. Only the browser proves the wiring. The binary the
// suite boots is built with -tags e2e: it writes every route its router serves
// to <routes file>.registered, and appends every route a request lands on to
// the routes file itself. After the run this compares the two.
//
// route-gaps.txt lists the routes not driven yet, each with its reason. It
// ratchets both ways, like conformance/deviations.py: a route missing from
// the run and from the list fails, and so does a listed route the run reached
// — take it off the list, the gap is closed.
//
// Enforced when E2E_ROUTE_GATE=1 (the full suite, as `task test:e2e` and CI
// run it). A run of one spec file reaches a handful of routes by design, so
// it only reports.

import { readFileSync, existsSync } from 'node:fs';
import { ROUTES_FILE } from './playwright.config';

const GAPS = 'route-gaps.txt';

function norm(method: string, path: string): string {
  return `${method} ${path || '/'}`;
}

// The routes the router serves, as the binary wrote them on its first request.
export function registered(): Set<string> {
  return read(ROUTES_FILE + '.registered');
}

function read(file: string): Set<string> {
  const out = new Set<string>();
  if (!existsSync(file)) return out;
  for (const line of readFileSync(file, 'utf8').split('\n')) {
    const [method, path = ''] = line.trim().split(/\s+/);
    if (method) out.add(norm(method, path));
  }
  return out;
}

function reached(): Set<string> {
  return read(ROUTES_FILE);
}

function gaps(): Map<string, string> {
  const out = new Map<string, string>();
  if (!existsSync(GAPS)) return out;
  for (const raw of readFileSync(GAPS, 'utf8').split('\n')) {
    const line = raw.trim();
    if (!line || line.startsWith('#')) continue;
    const [route, reason = ''] = line.split(/\s+#\s*/, 2);
    const [method, path] = route.trim().split(/\s+/);
    out.set(norm(method, path), reason);
  }
  return out;
}

export default function setup() {
  return function teardown() {
    const all = registered();
    const hit = reached();
    const known = gaps();
    const missed = [...all].filter((r) => !hit.has(r));
    const unexplained = missed.filter((r) => !known.has(r));
    const closed = [...known.keys()].filter((r) => hit.has(r));
    const gone = [...known.keys()].filter((r) => !all.has(r));
    const unreasoned = [...known].filter(([, why]) => !why).map(([r]) => r);

    console.log(`\nroute gate: ${all.size - missed.length} of ${all.size} console routes driven by a browser; ${known.size} listed in ${GAPS}`);
    if (process.env.E2E_ROUTE_GATE !== '1') return;

    const problems: string[] = [];
    const say = (title: string, routes: string[]) => {
      if (routes.length) problems.push(`${title}:\n${routes.sort().map((r) => `  ${r}`).join('\n')}`);
    };
    say(`no test drives these, and ${GAPS} does not say why`, unexplained);
    say(`a test drives these now — take them off ${GAPS}`, closed);
    say(`${GAPS} lists routes the router no longer serves`, gone);
    say(`${GAPS} lists these without a reason`, unreasoned);
    if (problems.length) throw new Error(`route gate failed\n\n${problems.join('\n\n')}`);
  };
}
