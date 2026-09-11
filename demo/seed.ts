// Seeds a running doze-aws with a realistic workload, for documentation
// screenshots.
//
//   bun seed.ts                  paced, so you can watch it fill up
//   bun seed.ts --fast           the same end state, no pauses
//   bun seed.ts --trade 5        then trade for five minutes, so things move
//   bun seed.ts --only compute   one section, while you iterate on a screenshot
//
// Re-running is safe: anything that already exists is reported and stepped over.

import { ENDPOINT } from './lib/aws';
import { summary, failures, warn } from './lib/say';
import { foundation } from './steps/foundation';
import { storage } from './steps/storage';
import { messaging } from './steps/messaging';
import { compute } from './steps/compute';
import { edge } from './steps/edge';
import { observability } from './steps/observability';
import { infra } from './steps/infra';
import { trading } from './steps/trading';

const SECTIONS: Record<string, () => Promise<void>> = {
  foundation, storage, messaging, compute, edge, observability, infra,
};

const argv = process.argv.slice(2);
const flag = (name: string) => {
  const i = argv.indexOf(`--${name}`);
  return i >= 0 ? (argv[i + 1] ?? '') : undefined;
};

const only = flag('only');
const tradeFor = Number(flag('trade') ?? 0);

console.log('\x1b[1mHarbour — seeding a doze-aws stack\x1b[0m');
console.log(`\x1b[2m   a fictional grocery delivery company, built entirely out of real SDK calls\x1b[0m`);
console.log(`\x1b[2m   against ${ENDPOINT}\x1b[0m`);

// Fail early and clearly if nothing is listening, rather than eighty timeouts.
try {
  const res = await fetch(`${ENDPOINT}/_console/`, { signal: AbortSignal.timeout(3000) });
  if (!res.ok) throw new Error(`console answered ${res.status}`);
} catch {
  warn(`nothing is answering on ${ENDPOINT}.`);
  console.log('   Start one with:  doze-aws --data-dir /tmp/harbour');
  process.exit(2);
}

const started = Date.now();

if (only) {
  const run = SECTIONS[only];
  if (!run) {
    warn(`no section called "${only}". Try: ${Object.keys(SECTIONS).join(', ')}`);
    process.exit(2);
  }
  await run();
} else {
  for (const run of Object.values(SECTIONS)) await run();
}

if (tradeFor > 0) await trading(tradeFor);

summary(started);
if (!tradeFor && !only) {
  console.log('\x1b[2m   Now open http://127.0.0.1:4566/_console/ — or run again with --trade 5\x1b[0m');
  console.log('\x1b[2m   to put live traffic through it while you take screenshots.\x1b[0m');
}
process.exit(failures.length ? 1 : 0);
