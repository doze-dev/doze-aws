import { spawn, type ChildProcess } from 'node:child_process';
import { mkdirSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';

// A doze-aws of a spec's own, for a test that needs a stack nobody else is
// touching (the first-run wire needs one with nothing in it) or one it may stop
// (the console's "cannot reach doze-aws" banner).
//
// It is the same binary the suite booted (playwright.config.ts builds
// e2e/.tmp/bin/doze-aws-<E2E_PORT>), on the port beside it: E2E_PORT + 1. A
// second run under another identity has its own E2E_PORT and so its own pair.
// The data directory is wiped on start unless the spec is restarting the stack
// it stopped.

const TMP = resolve(__dirname, '..', '.tmp');
const MAIN_PORT = Number(process.env.E2E_PORT ?? 14566);
export const PRIVATE_PORT = MAIN_PORT + 1;
export const PRIVATE_ORIGIN = `http://127.0.0.1:${PRIVATE_PORT}`;
export const PRIVATE_CONSOLE = `${PRIVATE_ORIGIN}/_console/`;

export interface PrivateServer {
  origin: string;
  console: string;
  /** Stops the process and waits until it has gone. */
  stop(): Promise<void>;
  /** Starts it again over the same data directory. */
  start(): Promise<void>;
}

export async function startPrivate(tag: string, name = 'harbour'): Promise<PrivateServer> {
  const data = join(TMP, `private-${PRIVATE_PORT}-${tag}`);
  rmSync(data, { recursive: true, force: true });
  mkdirSync(data, { recursive: true });
  let child: ChildProcess | undefined;
  let exited = true;

  const start = async () => {
    // cwd is e2e/.tmp, where no stack.yaml or doze-aws.toml can be found and
    // auto-loaded.
    child = spawn(
      join(TMP, 'bin', `doze-aws-${MAIN_PORT}`),
      ['--listen', `127.0.0.1:${PRIVATE_PORT}`, '--data-dir', data, '--console', '--name', name],
      { cwd: TMP, stdio: 'ignore' },
    );
    exited = false;
    child.once('exit', () => (exited = true));
    const deadline = Date.now() + 30_000;
    for (;;) {
      if (exited) throw new Error('the private doze-aws exited while starting');
      try {
        if ((await fetch(PRIVATE_CONSOLE)).ok) return;
      } catch {
        /* not up yet */
      }
      if (Date.now() > deadline) throw new Error('the private doze-aws did not answer within 30s');
      await new Promise((r) => setTimeout(r, 100));
    }
  };
  const stop = async () => {
    if (!child || exited) return;
    child.kill('SIGTERM');
    const deadline = Date.now() + 10_000;
    while (!exited && Date.now() < deadline) await new Promise((r) => setTimeout(r, 50));
    if (!exited) child.kill('SIGKILL');
    while (!exited) await new Promise((r) => setTimeout(r, 50));
  };
  await start();
  return { origin: PRIVATE_ORIGIN, console: PRIVATE_CONSOLE, stop, start };
}

/** An SQS JSON call against a private server. */
export async function privateSqs(origin: string, op: string, body: Record<string, unknown>) {
  const r = await fetch(`${origin}/`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': `AmazonSQS.${op}` },
    body: JSON.stringify(body),
  });
  const text = await r.text();
  let json: any;
  try {
    json = JSON.parse(text);
  } catch {
    json = undefined;
  }
  return { status: r.status, json, text };
}
