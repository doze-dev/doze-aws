// Record or check the mutation baseline for one package.
//
//   bun scripts/mutation-baseline.ts record internal/iampolicy
//   bun scripts/mutation-baseline.ts check  internal/iampolicy
//
//   # a package too slow to run whole (internal/console): a few files at a time,
//   # running only the tests that execute each mutated line (mutation-select.ts)
//   bun scripts/mutation-baseline.ts record internal/console --only map.go,glance.go --map /path/to/map
//   bun scripts/mutation-baseline.ts check  internal/console --only map.go --map /path/to/map
//
// `task mutate` runs gremlins and prints the lived mutants for a person to
// read. That is the right tool for finding a weak test; it is the wrong one for
// the protocol's question, which is "did this stage leave the suite weaker?".
// This keeps what survived, per package, in testdata/mutation.json:
//
//   record   run gremlins and write the package's counts and its lived mutants
//            (file:line:type), with the date and gremlins version.
//   check    run it again and fail if a mutant lives that the baseline did not
//            have. A mutant that now dies is not an error; it is a reason to
//            `record` again and say so.
//
// Lived mutants are keyed by file, line and type, not by count, so a refactor
// that moves code shows up as a changed set and has to be looked at.
//
// A baseline made with --only is PARTIAL: it carries a `files` object (the files
// it covers, each with its own counts), and the package-level counts are the sums
// over those files. `record --only` replaces the entries of the files it names and
// keeps the rest, so a package is built up a few files at a time and progress
// survives. `check` on a partial baseline without --only checks the recorded files.
import { spawnSync } from "node:child_process";
import { copyFileSync, existsSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const GREMLINS = "github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0";
const FILE = "testdata/mutation.json";

const argv = process.argv.slice(2);
const flag = (name: string) => {
  const i = argv.indexOf(name);
  if (i < 0) return undefined;
  const v = argv[i + 1];
  argv.splice(i, 2);
  return v;
};
const onlyArg = flag("--only");
const mapDir = flag("--map");
const workers = flag("--workers") ?? process.env.MUT_WORKERS ?? "2";
const [mode, pkg] = argv;
if ((mode !== "record" && mode !== "check") || !pkg) {
  console.error("usage: mutation-baseline.ts record|check <package dir, e.g. internal/iampolicy> [--only a.go,b.go] [--map <dir from mutation-select.ts>] [--workers n]");
  process.exit(2);
}

type Counts = { killed: number; lived: number; timedOut: number; notCovered: number; notViable: number };
type Entry = Partial<Counts> & { lived?: string[]; files?: Record<string, Partial<Counts> & { lived?: string[] }>; efficacy?: number };
const baseline: Record<string, any> = existsSync(FILE) ? JSON.parse(readFileSync(FILE, "utf8")) : {};
const was = baseline[pkg] as Entry | undefined;

// Which files this run covers: --only, else (check on a partial baseline) the
// files the baseline records, else the whole package.
const goFiles = readdirSync(pkg).filter((f) => f.endsWith(".go") && !f.endsWith("_test.go"));
let only: string[] | undefined = onlyArg?.split(",").filter(Boolean);
if (!only && mode === "check" && was?.files) only = Object.keys(was.files);
if (only) {
  const bad = only.filter((f) => !goFiles.includes(f));
  if (bad.length) {
    console.error(`not a non-test Go file of ${pkg}: ${bad.join(", ")}`);
    process.exit(2);
  }
}

const dir = mkdtempSync(join(tmpdir(), "gremlins-"));
const out = join(dir, "out.json");
// gremlins is installed and run as a binary, not with `go run`: the go command
// puts its own GOROOT/bin first on the PATH of what it runs, which would hide the
// `go` shim from gremlins' own `go test` calls.
const install = spawnSync("go", ["install", GREMLINS], { stdio: ["ignore", "ignore", "inherit"], env: { ...process.env, GOWORK: "off", GOBIN: join(dir, "bin") } });
if (install.status !== 0) {
  console.error("could not install gremlins");
  process.exit(1);
}
const args = ["unleash", "--timeout-coefficient", "10", "--workers", workers, "-o", out];
if (only) {
  // gremlins mutates every file of the package it is given; leave the others out.
  const skip = goFiles.filter((f) => !only!.includes(f)).map((f) => f.replace(/\./g, "\\."));
  // (gremlins matches the path relative to the package directory)
  if (skip.length) args.push("-E", `^(${skip.join("|")})$`);
  // and the packages below it, which have their own baselines
  for (const d of readdirSync(pkg, { withFileTypes: true })) if (d.isDirectory()) args.push("-E", `^${d.name}/`);
}
args.push(`./${pkg}`);

const env: Record<string, string | undefined> = { ...process.env, GOWORK: "off" };
if (mapDir) {
  const m = resolve(mapDir);
  const realGo = spawnSync("sh", ["-c", "command -v go"], { encoding: "utf8" }).stdout.trim();
  spawnSync("bun", [join(import.meta.dir, "mutation-select.ts"), "shim", m], { stdio: "ignore" });
  env.MUT_REAL_GO = realGo;
  env.MUT_MAP = m;
  env.MUT_PKG = pkg;
  env.MUT_SRC = process.cwd();
  env.PATH = `${join(m, "bin")}:${process.env.PATH}`;
}
const run = spawnSync(join(dir, "bin", "gremlins"), args, { stdio: ["ignore", process.env.MUT_VERBOSE ? "inherit" : "ignore", "inherit"], env: { ...env, PATH: env.PATH } });
if (!existsSync(out)) {
  console.error(`gremlins produced no result for ${pkg} (exit ${run.status})`);
  process.exit(1);
}

// MUT_RAW=<path> keeps gremlins' own result file (every mutant and its status).
if (process.env.MUT_RAW) copyFileSync(out, process.env.MUT_RAW);
type Mutation = { type: string; status: string; line: number };
const result = JSON.parse(readFileSync(out, "utf8")) as { files: { file_name: string; mutations: Mutation[] }[] };
const zero = (): Counts => ({ killed: 0, lived: 0, timedOut: 0, notCovered: 0, notViable: 0 });
const counts = zero();
const perFile: Record<string, Counts & { livedList: string[] }> = {};
const lived: string[] = [];
for (const f of result.files ?? []) {
  const c = (perFile[f.file_name] ??= { ...zero(), livedList: [] });
  for (const m of f.mutations) {
    switch (m.status) {
      case "KILLED": counts.killed++; c.killed++; break;
      case "LIVED": counts.lived++; c.lived++; lived.push(`${f.file_name}:${m.line}:${m.type}`); c.livedList.push(`${f.file_name}:${m.line}:${m.type}`); break;
      case "TIMED OUT": counts.timedOut++; c.timedOut++; break;
      case "NOT COVERED": counts.notCovered++; c.notCovered++; break;
      case "NOT VIABLE": counts.notViable++; c.notViable++; break;
    }
  }
}
lived.sort();
const effOf = (c: Counts) => {
  const tested = c.killed + c.lived;
  return tested === 0 ? 100 : Math.round((c.killed / tested) * 1000) / 10;
};
const efficacy = effOf(counts);

if (mode === "record") {
  const sorted = (o: Record<string, unknown>) => Object.fromEntries(Object.entries(o).sort(([a], [b]) => a.localeCompare(b)));
  if (only) {
    // Partial: keep the entries of the files not named, replace the ones named.
    const files: Record<string, any> = { ...(was?.files ?? {}) };
    for (const f of only) {
      const c = perFile[f] ?? { ...zero(), livedList: [] };
      files[f] = { killed: c.killed, lived: c.livedList.sort(), timedOut: c.timedOut, notCovered: c.notCovered, notViable: c.notViable, efficacy: effOf(c) };
    }
    const sum = zero();
    const all: string[] = [];
    for (const f of Object.values(files)) {
      sum.killed += f.killed; sum.lived += f.lived.length; sum.timedOut += f.timedOut; sum.notCovered += f.notCovered; sum.notViable += f.notViable;
      all.push(...f.lived);
    }
    baseline[pkg] = { ...sum, lived: all.sort(), efficacy: effOf(sum), partial: true, files: sorted(files), recorded: new Date().toISOString().slice(0, 10), gremlins: "v0.6.0" };
  } else {
    baseline[pkg] = { ...counts, efficacy, lived, recorded: new Date().toISOString().slice(0, 10), gremlins: "v0.6.0" };
  }
  writeFileSync(FILE, JSON.stringify(sorted(baseline), null, 2) + "\n");
  console.log(`${pkg}${only ? ` [${only.join(" ")}]` : ""}: killed ${counts.killed}, lived ${counts.lived}, timed out ${counts.timedOut}, not covered ${counts.notCovered}, efficacy ${efficacy}%`);
  process.exit(0);
}

if (!was) {
  console.error(`no baseline for ${pkg}; run: bun scripts/mutation-baseline.ts record ${pkg}`);
  process.exit(1);
}
// With --only the baseline is narrowed to the same files, so the comparison is
// like for like.
const wasLived = (was.lived ?? []).filter((k) => !only || only.some((f) => k.startsWith(`${f}:`)));
const wasNotCovered = only && was.files ? only.reduce((n, f) => n + (was.files![f]?.notCovered ?? 0), 0) : was.notCovered;
// Survivors are compared as a MULTISET. A key is file:line:type, and one line
// can hold two mutants of one type (a condition with two comparisons), so a set
// would let a second survivor on a line that already had one pass unnoticed.
const tally = (xs: string[]) => {
  const m = new Map<string, number>();
  for (const x of xs) m.set(x, (m.get(x) ?? 0) + 1);
  return m;
};
const had = tally(wasLived);
const now = tally(lived);
const fresh: string[] = [];
for (const [k, n] of now) {
  const extra = n - (had.get(k) ?? 0);
  if (extra > 0) fresh.push(extra > 1 ? `${k} (x${extra})` : k);
}
const gone: string[] = [];
for (const [k, n] of had) if ((now.get(k) ?? 0) < n) gone.push(k);
console.log(`${pkg}${only ? ` [${only.join(" ")}]` : ""}: killed ${counts.killed}, lived ${counts.lived} (baseline ${wasLived.length}), not covered ${counts.notCovered} (baseline ${wasNotCovered ?? "?"}), efficacy ${efficacy}%`);
if (gone.length) console.log(`  no longer live (record again): ${gone.join(", ")}`);
let failed = false;
if (fresh.length) {
  console.error(`  NEW survivors, each a behaviour nothing asserts:\n    ${fresh.join("\n    ")}`);
  failed = true;
}
// A mutant no test even reaches is not a survivor, so new code with no test at
// all would add none. The count of unreached mutants is held too.
if (typeof wasNotCovered === "number" && counts.notCovered > wasNotCovered) {
  console.error(`  ${counts.notCovered - wasNotCovered} more mutants are in code no test reaches (${counts.notCovered}, was ${wasNotCovered}): test what was added.`);
  failed = true;
}
if (failed) process.exit(1);
