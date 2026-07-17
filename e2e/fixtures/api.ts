import type { APIRequestContext } from '@playwright/test';
import { BASE_URL } from '../playwright.config';

type Fields = Record<string, string | number | boolean | undefined>;

/**
 * Arrange-via-API helper: POSTs a plain (non-htmx) form to a console route,
 * exactly as the create-page `<form>`s do. The server 303-redirects plain
 * POSTs to the new resource's detail page (console.go's `redirect` helper);
 * Playwright's APIRequestContext follows redirects, so `res.ok()` reflects
 * the final page, not the redirect itself.
 *
 * Booleans mirror real HTML checkboxes: `true` sends "on" (what the server's
 * `r.FormValue("x") == "on"` checks for), `false`/`undefined` are omitted
 * entirely — an unchecked checkbox is never submitted by a real browser.
 */
export async function postForm(
  request: APIRequestContext,
  path: string,
  fields: Fields
) {
  const form: Record<string, string> = {};
  for (const [k, v] of Object.entries(fields)) {
    if (v === undefined || v === false) continue;
    form[k] = v === true ? 'on' : String(v);
  }
  const res = await request.post(BASE_URL + path.replace(/^\//, ''), { form });
  if (!res.ok()) {
    throw new Error(
      `POST ${path} failed: ${res.status()} ${(await res.text()).slice(0, 300)}`
    );
  }
  return res;
}

export async function createBucket(
  request: APIRequestContext,
  name: string,
  opts?: { versioning?: boolean; objectLock?: boolean }
) {
  await postForm(request, 's3/create', {
    name,
    versioning: opts?.versioning,
    object_lock: opts?.objectLock,
  });
  return name;
}

export async function createQueue(
  request: APIRequestContext,
  name: string,
  opts?: {
    fifo?: boolean;
    contentDedup?: boolean;
    visibility?: number;
    delay?: number;
    retention?: number;
    dlqMode?: 'new' | 'existing' | 'none';
    dlqExisting?: string;
    maxReceive?: number;
  }
) {
  await postForm(request, 'sqs/create', {
    name,
    fifo: opts?.fifo,
    content_dedup: opts?.contentDedup,
    visibility: opts?.visibility,
    delay: opts?.delay,
    retention: opts?.retention,
    dlq_mode: opts?.dlqMode ?? 'none',
    dlq: opts?.dlqExisting,
    max_receive: opts?.maxReceive,
  });
  // FIFO queues are stored (and addressed) with a .fifo suffix.
  return opts?.fifo ? `${name}.fifo` : name;
}

export async function createTable(
  request: APIRequestContext,
  name: string,
  opts?: {
    hashKey?: string;
    hashType?: 'S' | 'N' | 'B';
    rangeKey?: string;
    rangeType?: 'S' | 'N' | 'B';
  }
) {
  await postForm(request, 'ddb/create', {
    name,
    hash_key: opts?.hashKey ?? 'pk',
    hash_type: opts?.hashType ?? 'S',
    range_key: opts?.rangeKey,
    range_type: opts?.rangeKey ? opts?.rangeType ?? 'S' : undefined,
  });
  return name;
}

export async function createTopic(request: APIRequestContext, name: string) {
  await postForm(request, 'sns/create', { name });
  return name;
}

export async function createBus(request: APIRequestContext, name: string) {
  await postForm(request, 'eb/create-bus', { name });
  return name;
}

export async function createKey(
  request: APIRequestContext,
  opts?: {
    spec?: string;
    usage?: 'ENCRYPT_DECRYPT' | 'SIGN_VERIFY' | 'GENERATE_VERIFY_MAC' | '';
    alias?: string;
    description?: string;
  }
) {
  const res = await postForm(request, 'kms/create', {
    spec: opts?.spec ?? 'SYMMETRIC_DEFAULT',
    usage: opts?.usage ?? '',
    alias: opts?.alias,
    description: opts?.description,
  });
  // The detail page URL is /_console/kms/{keyId} after the redirect chain.
  const url = new URL(res.url());
  const keyId = url.pathname.split('/').filter(Boolean).pop();
  if (!keyId) throw new Error(`could not determine created key id from ${res.url()}`);
  return keyId;
}

/**
 * Minimal Lambda create — most specs need a real invokable function, which
 * requires a runtime-appropriate code path (see e2e/fixtures/lambda-handler/
 * and lambda.spec.ts for the `_local_` fixture handler). This wrapper is for
 * specs that only need *a* function to exist (e.g. as an SNS/EB/SM target)
 * and don't invoke it.
 */
export async function createFunction(
  request: APIRequestContext,
  name: string,
  opts: {
    runtime?: string;
    handler?: string;
    code: string;
    timeout?: number;
    memory?: number;
  }
) {
  await postForm(request, 'lambda/create', {
    name,
    runtime: opts.runtime ?? 'provided.al2',
    handler: opts.handler ?? 'bootstrap',
    code: opts.code,
    timeout: opts.timeout,
    memory: opts.memory,
  });
  return name;
}
