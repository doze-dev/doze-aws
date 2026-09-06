// doze-aws's Node.js runtime client: the Lambda Runtime API loop, in one file.
//
// It stands in for aws-lambda-ric — which ships a native addon compiled
// against the Node ABI at install time — so a nodejs*.x function runs with
// nothing installed but node. Handler resolution follows the RIC: CommonJS
// and ES modules, .mjs/.cjs/.js, nested paths, async or callback handlers.
// Console output is prefixed the way Lambda prefixes it, and becomes JSON
// records under AWS_LAMBDA_LOG_FORMAT=JSON.

import { createRequire } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";
import fs from "node:fs";

const API = `http://${process.env.AWS_LAMBDA_RUNTIME_API}/2018-06-01/runtime/`;
const TASK_ROOT = process.env.LAMBDA_TASK_ROOT || process.cwd();
const JSON_LOGS = (process.env.AWS_LAMBDA_LOG_FORMAT || "").toUpperCase() === "JSON";

let currentRequestId = "";

// ---- logging, the way Lambda wires it ----

const LEVELS = { trace: "TRACE", debug: "DEBUG", info: "INFO", log: "INFO", warn: "WARN", error: "ERROR", fatal: "FATAL" };
const ORDER = ["TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"];
const MIN = ORDER.indexOf((process.env.AWS_LAMBDA_LOG_LEVEL || "TRACE").toUpperCase());

function fmt(args) {
  return args.map((a) => (typeof a === "string" ? a : a instanceof Error ? a.stack || String(a) : (() => { try { return JSON.stringify(a); } catch { return String(a); } })())).join(" ");
}

for (const [method, level] of Object.entries(LEVELS)) {
  const stream = level === "ERROR" || level === "FATAL" || level === "WARN" ? process.stderr : process.stdout;
  console[method] = (...args) => {
    if (ORDER.indexOf(level) < MIN) return;
    const line = JSON_LOGS
      ? JSON.stringify({ timestamp: new Date().toISOString(), level, requestId: currentRequestId, message: fmt(args) })
      : `${new Date().toISOString()}\t${currentRequestId}\t${level}\t${fmt(args).replace(/\n/g, "\r")}`;
    stream.write(line + "\n");
  };
}

// ---- the handler ----

async function loadHandler(spec) {
  const dot = spec.lastIndexOf(".");
  if (dot <= 0) throw errorNamed("Runtime.MalformedHandlerName", `Bad handler '${spec}': not of the form file.export`);
  const modulePath = spec.slice(0, dot);
  const exportName = spec.slice(dot + 1);
  const base = path.resolve(TASK_ROOT, modulePath);
  const candidates = [base + ".mjs", base + ".js", base + ".cjs", path.join(base, "index.mjs"), path.join(base, "index.js"), path.join(base, "index.cjs")];
  const file = candidates.find((f) => fs.existsSync(f));
  if (!file) throw errorNamed("Runtime.ImportModuleError", `Error: Cannot find module '${modulePath}' under ${TASK_ROOT}`);
  let mod;
  try {
    // import() loads both module systems; a CJS module's exports are the
    // default export, so both shapes are tried.
    mod = await import(pathToFileURL(file).href);
  } catch (e) {
    if (file.endsWith(".cjs") || file.endsWith(".js")) {
      try {
        mod = createRequire(file)(file);
      } catch (e2) {
        throw errorNamed("Runtime.ImportModuleError", `${e2.name}: ${e2.message}`, e2.stack);
      }
    } else {
      throw errorNamed("Runtime.ImportModuleError", `${e.name}: ${e.message}`, e.stack);
    }
  }
  const fn = mod[exportName] ?? mod.default?.[exportName] ?? (exportName === "default" ? mod.default : undefined);
  if (typeof fn !== "function") throw errorNamed("Runtime.HandlerNotFound", `${spec} is undefined or not exported`);
  return fn;
}

function errorNamed(type, message, stack) {
  const e = new Error(message);
  e.name = type;
  if (stack) e.stack = stack;
  return e;
}

function errorBody(e) {
  const err = e instanceof Error ? e : new Error(String(e));
  return JSON.stringify({
    errorType: err.name || "Error",
    errorMessage: err.message,
    trace: (err.stack || "").split("\n"),
  });
}

async function post(pathname, body, type) {
  const headers = { "Content-Type": "application/json" };
  if (type) headers["Lambda-Runtime-Function-Error-Type"] = type;
  await fetch(API + pathname, { method: "POST", body, headers });
}

function context(requestId, headers) {
  const deadline = Number(headers.get("lambda-runtime-deadline-ms") || 0);
  const cc = headers.get("lambda-runtime-client-context");
  const ci = headers.get("lambda-runtime-cognito-identity");
  return {
    callbackWaitsForEmptyEventLoop: true,
    functionName: process.env.AWS_LAMBDA_FUNCTION_NAME || "",
    functionVersion: process.env.AWS_LAMBDA_FUNCTION_VERSION || "$LATEST",
    invokedFunctionArn: headers.get("lambda-runtime-invoked-function-arn") || "",
    memoryLimitInMB: process.env.AWS_LAMBDA_FUNCTION_MEMORY_SIZE || "128",
    awsRequestId: requestId,
    logGroupName: process.env.AWS_LAMBDA_LOG_GROUP_NAME || "",
    logStreamName: process.env.AWS_LAMBDA_LOG_STREAM_NAME || "",
    identity: ci ? JSON.parse(ci) : undefined,
    clientContext: cc ? JSON.parse(cc) : undefined,
    getRemainingTimeInMillis: () => (deadline ? Math.max(0, deadline - Date.now()) : 0),
  };
}

// A callback-style handler (event, context, callback) is promisified; a
// handler that returns a promise is awaited. Both are what the RIC accepts.
function invoke(handler, event, ctx) {
  return new Promise((resolve, reject) => {
    let settled = false;
    const done = (err, result) => {
      if (settled) return;
      settled = true;
      err ? reject(err) : resolve(result);
    };
    let out;
    try {
      out = handler(event, ctx, done);
    } catch (e) {
      return done(e);
    }
    if (out && typeof out.then === "function") out.then((r) => done(null, r), done);
    else if (handler.length < 3) done(null, out);
  });
}

async function main() {
  const spec = process.argv[2] || process.env._HANDLER || "";
  let handler;
  try {
    handler = await loadHandler(spec);
  } catch (e) {
    console.error(`[ERROR] ${e.name}: ${e.message}`);
    await post("init/error", errorBody(e), e.name);
    process.exit(1);
  }
  process.on("uncaughtException", async (e) => {
    console.error(`[ERROR] Uncaught ${e?.name}: ${e?.message}`);
    if (currentRequestId) await post(`invocation/${currentRequestId}/error`, errorBody(e), e?.name);
    process.exit(1);
  });
  for (;;) {
    const resp = await fetch(API + "invocation/next");
    const requestId = resp.headers.get("lambda-runtime-aws-request-id") || "";
    currentRequestId = requestId;
    const trace = resp.headers.get("lambda-runtime-trace-id");
    if (trace) process.env._X_AMZN_TRACE_ID = trace;
    const raw = await resp.text();
    let event;
    try {
      event = raw ? JSON.parse(raw) : null;
    } catch {
      event = raw;
    }
    try {
      const result = await invoke(handler, event, context(requestId, resp.headers));
      await post(`invocation/${requestId}/response`, result === undefined ? "null" : JSON.stringify(result));
    } catch (e) {
      const err = e instanceof Error ? e : new Error(String(e));
      console.error(`[ERROR] ${err.name}: ${err.message}\n${err.stack || ""}`);
      await post(`invocation/${requestId}/error`, errorBody(err), err.name);
    }
  }
}

main();
