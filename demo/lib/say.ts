// Narration and pacing.
//
// The seed is deliberately slow. It exists so someone can watch a console fill
// up and photograph it, and a script that finishes in two seconds gives you a
// finished stack and no screenshots of anything happening — no live-tailing log
// pane, no traffic feed scrolling, no alarm crossing into ALARM. The pauses are
// the feature. `--fast` removes them for when you only want the end state.

const FAST = process.argv.includes('--fast');
const NO_COLOUR = process.argv.includes('--no-colour') || !!process.env.NO_COLOR;

const paint = (code: string, s: string) => (NO_COLOUR ? s : `\x1b[${code}m${s}\x1b[0m`);
const dim = (s: string) => paint('2', s);
const bold = (s: string) => paint('1', s);
const green = (s: string) => paint('32', s);
const yellow = (s: string) => paint('33', s);
const red = (s: string) => paint('31', s);
const cyan = (s: string) => paint('36', s);

let current = '';

/** A service section: everything under it belongs to one console page. */
export function section(name: string, blurb: string) {
  current = name;
  console.log('');
  console.log(bold(cyan(`── ${name} `.padEnd(66, '─'))));
  console.log(dim(`   ${blurb}`));
}

/** One thing done, with the AWS call that did it. */
export function did(what: string, api?: string) {
  console.log(`   ${green('✓')} ${what}${api ? dim(`  ${api}`) : ''}`);
}

/** Something the emulator answered differently than AWS would, said out loud. */
export function note(what: string) {
  console.log(`   ${yellow('·')} ${dim(what)}`);
}

export function warn(what: string) {
  console.log(`   ${yellow('!')} ${what}`);
}

export function fail(what: string, err: unknown) {
  const msg = err instanceof Error ? err.message : String(err);
  console.log(`   ${red('✗')} ${what}${dim(`  ${msg}`)}`);
}

/** Where to look in the console for what just happened. */
export function look(path: string, forWhat: string) {
  console.log(`   ${dim('→')} ${dim(`${path}  ${forWhat}`)}`);
}

export const sleep = (ms: number) =>
  new Promise((r) => setTimeout(r, FAST ? Math.min(ms, 5) : ms));

/** A beat between steps, so a watcher can follow along. */
export const beat = () => sleep(450);

/** A longer pause where something is happening server-side worth watching. */
export const watch = (ms = 3000) => sleep(ms);

/**
 * The names AWS uses to say "you already made this". Re-running the seed
 * before a fresh set of screenshots is the normal thing to do, so an existing
 * resource is a satisfied step and not a failure — printing it as a failure
 * would bury the one error that does matter under thirty that do not.
 */
const EXISTS = new Set([
  'EntityAlreadyExists', 'ResourceAlreadyExistsException', 'ResourceInUseException',
  'BucketAlreadyOwnedByYou', 'BucketAlreadyExists', 'AlreadyExistsException',
  'ResourceExistsException', 'QueueAlreadyExists', 'TopicAlreadyExists',
  'StateMachineAlreadyExists', 'ConflictException', 'ValidationException',
]);

function alreadyThere(err: unknown): boolean {
  const name = (err as { name?: string })?.name ?? '';
  const msg = (err as Error)?.message ?? '';
  if (name === 'ValidationException' || name === 'ConflictException') {
    // These two are reused for plenty of real mistakes, so only the message
    // is trustworthy here.
    return /already exist|already been created|in use/i.test(msg);
  }
  return EXISTS.has(name) || /already exist/i.test(msg);
}

/**
 * Run a step, reporting rather than throwing.
 *
 * A seed that dies two thirds of the way through leaves a stack nobody can
 * photograph and nobody can clean up. Every step is independent enough that a
 * failure is worth printing and stepping over — and a printed failure is also
 * how this doubles as a smoke test of the whole surface.
 */
export async function step<T>(what: string, fn: () => Promise<T>, api?: string): Promise<T | undefined> {
  try {
    const out = await fn();
    did(what, api);
    await beat();
    return out;
  } catch (err) {
    if (alreadyThere(err)) {
      console.log(`   ${dim('=')} ${dim(`${what} — already there`)}`);
      return undefined;
    }
    fail(what, err);
    failures.push({ section: current, what, err });
    return undefined;
  }
}

export const failures: { section: string; what: string; err: unknown }[] = [];

export function summary(started: number) {
  const secs = ((Date.now() - started) / 1000).toFixed(0);
  console.log('');
  if (failures.length === 0) {
    console.log(bold(green(`Seeded in ${secs}s. Everything the stack supports is now populated.`)));
    return;
  }
  console.log(bold(yellow(`Seeded in ${secs}s, with ${failures.length} step(s) not completed:`)));
  for (const f of failures) {
    const msg = f.err instanceof Error ? f.err.message : String(f.err);
    console.log(`   ${red('✗')} ${dim(`[${f.section}]`)} ${f.what} — ${msg}`);
  }
}
