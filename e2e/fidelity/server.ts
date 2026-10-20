// Build the real binary and boot it, the way e2e/playwright.config.ts's
// webServer does, but on a port the runner picks (14960-14969 only) and with a
// data dir that is wiped on every boot.

import { spawn, spawnSync, type ChildProcess } from 'node:child_process';
import { createServer } from 'node:net';
import { mkdirSync, openSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';
import type { Inst, Server } from './drivers/types';

export const E2E_DIR = resolve(import.meta.dir, '..');
export const REPO_DIR = resolve(E2E_DIR, '..');
export const TMP_DIR = join(E2E_DIR, '.tmp');
const BIN = join(TMP_DIR, 'bin', 'doze-aws-fidelity');

// FIDELITY_PORT_MIN moves the ten-port window, so two checkouts can run the
// harness at once without taking each other's ports.
export const PORT_MIN = Number(process.env.FIDELITY_PORT_MIN ?? 14960);
export const PORT_MAX = PORT_MIN + 9;

export function build(): void {
  mkdirSync(join(TMP_DIR, 'bin'), { recursive: true });
  const r = spawnSync('go', ['build', '-o', BIN, './cmd/doze-aws'], {
    cwd: REPO_DIR,
    env: { ...process.env, GOWORK: 'off' },
    stdio: ['ignore', 'inherit', 'inherit'],
  });
  if (r.status !== 0) throw new Error('go build ./cmd/doze-aws failed');
}

function free(port: number): Promise<boolean> {
  return new Promise((res) => {
    const s = createServer();
    s.once('error', () => res(false));
    s.listen(port, '127.0.0.1', () => s.close(() => res(true)));
  });
}

/** The first free port in 14960-14969 that this run has not already taken. */
export async function freePort(taken: Set<number>): Promise<number> {
  for (let p = PORT_MIN; p <= PORT_MAX; p++) {
    if (!taken.has(p) && (await free(p))) {
      taken.add(p);
      return p;
    }
  }
  throw new Error(`no free port in ${PORT_MIN}-${PORT_MAX}`);
}

export interface BootOpts {
  port: number;
  /** `--id2`: -account-id 111122223333 -region eu-west-1. */
  id2?: boolean;
  iamMode?: 'enforce';
  /** Distinguishes the data dir and the log. */
  tag: string;
}

export interface Booted {
  server: Server;
  stop(): Promise<void>;
}

export const ID2 = { account: '111122223333', region: 'eu-west-1' };
export const ID1 = { account: '000000000000', region: 'us-east-1' };

export async function boot(o: BootOpts): Promise<Booted> {
  const id = o.id2 ? ID2 : ID1;
  const data = join(TMP_DIR, `fidelity-data-${o.tag}-${o.port}`);
  // cd to a directory that holds no stack.yaml / doze-aws.toml, which
  // doze-aws would auto-load from the working directory.
  const cwd = join(TMP_DIR, 'fidelity-cwd');
  rmSync(data, { recursive: true, force: true });
  mkdirSync(data, { recursive: true });
  mkdirSync(cwd, { recursive: true });
  const log = openSync(join(TMP_DIR, `fidelity-${o.tag}-${o.port}.log`), 'w');
  const args = ['--listen', `127.0.0.1:${o.port}`, '--data-dir', data, '--console', '--name', 'harbour'];
  if (o.id2) args.push('--account-id', id.account, '--region', id.region);
  if (o.iamMode) args.push('--iam-mode', o.iamMode);
  const child: ChildProcess = spawn(BIN, args, { cwd, stdio: ['ignore', log, log] });
  let exited = false;
  child.once('exit', () => (exited = true));

  const origin = `http://127.0.0.1:${o.port}`;
  const deadline = Date.now() + 30_000;
  for (;;) {
    if (exited) throw new Error(`doze-aws exited while booting; see e2e/.tmp/fidelity-${o.tag}-${o.port}.log`);
    try {
      const r = await fetch(`${origin}/_console/`);
      if (r.ok) break;
    } catch {
      /* not up yet */
    }
    if (Date.now() > deadline) {
      child.kill();
      throw new Error(`doze-aws did not answer on ${origin} within 30s`);
    }
    await Bun.sleep(100);
  }
  return {
    server: { origin, console: `${origin}/_console/`, account: id.account, region: id.region, name: 'harbour' },
    stop: async () => {
      if (exited) return;
      child.kill('SIGTERM');
      await Promise.race([new Promise((r) => child.once('exit', r)), Bun.sleep(5000)]);
      if (!exited) child.kill('SIGKILL');
    },
  };
}

export function inst(server: Server): Inst {
  const sqs = async (op: string, body: Record<string, unknown>) => {
    const r = await fetch(`${server.origin}/`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-amz-json-1.0', 'X-Amz-Target': `AmazonSQS.${op}` },
      body: JSON.stringify(body),
    });
    const t = await r.text();
    let j: any;
    try {
      j = JSON.parse(t);
    } catch {
      j = t;
    }
    return { status: r.status, body: j };
  };
  return {
    server,
    url: (p) => server.console + p,
    sqs,
    queueUrl: (n) => `${server.origin}/${server.account}/${n}`,
    queueArn: (n) => `arn:aws:sqs:${server.region}:${server.account}:${n}`,
  };
}
