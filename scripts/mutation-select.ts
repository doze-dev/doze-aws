// Run only the tests that execute a mutated line, so a package whose suite takes
// minutes can still be mutation-tested.
//
//   bun scripts/mutation-select.ts map internal/console <dir>   build the coverage map
//   bun scripts/mutation-select.ts shim <dir>                   write the `go` shim gremlins is run through
//
// gremlins (v0.6.0) runs the package's WHOLE suite for every mutant. For
// internal/console that is about 150 s a mutant, which is days. A mutant on a
// line that only three tests execute can only be seen by those three tests, so
// the others are wasted time, not rigour. This keeps rigour and drops the waste:
//
//   map    builds the package's test binary with coverage and runs every
//          top-level test ALONE, keeping its coverage profile, then the whole
//          suite once for each test's cost among others. The result is
//          <dir>/lines.json (file -> line -> the tests that execute it),
//          <dir>/merged.cov (every line any test executes, in `go test
//          -coverprofile` form) and <dir>/tests.json (each test, its seconds, its
//          suite seconds, whether it passed alone). A test that fails alone on
//          unmutated code is "broken" and is left out of every selection: it would
//          "kill" every mutant it touches without saying anything about the mutant.
//   shim   writes <dir>/bin/go, a shell shim that sends `go test` to this file
//          (the `wrap` mode) and everything else to the real go. gremlins finds
//          `go` on PATH, so no gremlins code changes. (Run gremlins as a binary:
//          `go run` would put GOROOT/bin ahead of the shim.)
//   wrap   (called by the shim) answers gremlins' two `go test` calls:
//            the coverage run  -> writes merged.cov as the profile, after a
//                                 fixed pause (gremlins sets each mutant's deadline
//                                 to 10x this run's time; MUT_COVER_SECS, default
//                                 40, makes it far longer than any stage below);
//            a mutant's run    -> finds the file and line the mutation changed
//                                 (the worker's copy against the pristine tree),
//                                 builds the test binary once, and runs the tests
//                                 that execute that line, plus the ALWAYS tests,
//                                 in two stages: the cheap tests (about a second
//                                 or less each in a whole run), then the rest, so
//                                 a mutant any cheap test catches never pays for
//                                 the slow ones.
//
// What this does not weaken: a test that does not execute the mutated line
// cannot observe the mutant, except by reading the package's source text. The
// ALWAYS tests are the ones that do (they scan *.go for string literals, which an
// operator mutation cannot change; they are kept in every selection regardless).
// If a mutated line has no covering test in the map the whole passing suite runs.
//
// A stage gets 60 s plus five times what its tests take in a normal run. A mutant
// that makes a test hang (a loop that no longer ends) trips that and counts as
// killed, the way `go test` counts its own timeout. gremlins' own deadline is not
// used for that: it is one number for the whole package, so a mutant that merely
// survives the cheap stage and then runs the slow tests on a busy machine was
// reported TIMED OUT, and a survivor hidden as a timeout is the one thing this
// must not do.
import { spawn, spawnSync } from "node:child_process";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";

// Tests that read the package's own source files. They stay in every selection.
export const ALWAYS = [
  "TestSDKCoverage",
  "TestTrafficTableIsNotSilentlyLoadBearing",
  "TestEveryPartialIsClassified",
  "TestDisplayedTimesGoThroughOneFormatter",
];

// secs: the test alone in its own process (mostly the package's fixed start-up);
// suite: its own time inside one run of the whole suite, which is what it costs
// when it is run with others.
type TestInfo = { secs: number; ok: boolean; suite?: number };

function realGo(): string {
  const p = process.env.MUT_REAL_GO;
  if (p) return p;
  const r = spawnSync("sh", ["-c", "command -v go"], { encoding: "utf8" });
  return r.stdout.trim();
}

async function buildMap(pkg: string, dirArg: string) {
  const dir = resolve(dirArg);
  mkdirSync(join(dir, "prof"), { recursive: true });
  const go = realGo();
  const bin = join(dir, "pkg.test");
  const env = { ...process.env, GOWORK: "off" };
  const b = spawnSync(go, ["test", "-c", "-cover", "-covermode=set", "-o", bin, `./${pkg}`], { env, stdio: "inherit" });
  if (b.status !== 0) throw new Error("build of the cover test binary failed");
  const list = spawnSync(bin, ["-test.list", ".*"], { cwd: pkg, encoding: "utf8" });
  const names = list.stdout.split("\n").filter((l) => /^Test/.test(l));

  const tests: Record<string, TestInfo> = {};
  const par = Number(process.env.MUT_MAP_PAR ?? 6);
  let next = 0;
  const one = (name: string) =>
    new Promise<void>((done) => {
      const t0 = Date.now();
      const prof = resolve(dir, "prof", `${name}.out`);
      const p = spawn(bin, [`-test.run=^${name}$`, "-test.count=1", "-test.timeout=10m", `-test.coverprofile=${prof}`], { cwd: pkg, stdio: "ignore", env });
      p.on("exit", (code) => {
        tests[name] = { secs: Math.round(Date.now() - t0) / 1000, ok: code === 0 };
        done();
      });
    });
  const lane = async () => {
    while (next < names.length) await one(names[next++]);
  };
  await Promise.all(Array.from({ length: par }, lane));

  // Any test that failed alone is run once more with nothing else running, in
  // case it only failed because several of them shared the machine.
  for (const name of names) {
    if (!tests[name].ok) await one(name);
  }

  // One run of the whole suite, for each test's cost among others.
  const whole = spawnSync(bin, ["-test.count=1", "-test.v", "-test.timeout=30m"], { cwd: pkg, encoding: "utf8", env, maxBuffer: 1 << 30 });
  for (const m of whole.stdout.matchAll(/^--- (?:PASS|FAIL): (Test\w+) \(([\d.]+)s\)$/gm)) if (tests[m[1]]) tests[m[1]].suite = Number(m[2]);

  const lines: Record<string, Record<number, number[]>> = {};
  const merged = new Map<string, number>();
  names.forEach((name, idx) => {
    if (!tests[name].ok) return;
    const prof = join(dir, "prof", `${name}.out`);
    if (!existsSync(prof)) return;
    for (const l of readFileSync(prof, "utf8").split("\n").slice(1)) {
      const m = /^(.+):(\d+)\.\d+,(\d+)\.\d+ (\d+) (\d+)$/.exec(l);
      if (!m || m[5] === "0") continue;
      const file = m[1].slice(m[1].lastIndexOf("/") + 1);
      merged.set(l.slice(0, l.lastIndexOf(" ")), 1);
      const f = (lines[file] ??= {});
      for (let ln = Number(m[2]); ln <= Number(m[3]); ln++) {
        const a = (f[ln] ??= []);
        if (a[a.length - 1] !== idx) a.push(idx);
      }
    }
  });
  writeFileSync(join(dir, "lines.json"), JSON.stringify({ tests: names, lines }));
  writeFileSync(join(dir, "tests.json"), JSON.stringify(tests, null, 1));
  writeFileSync(join(dir, "merged.cov"), "mode: set\n" + [...merged.keys()].map((k) => k + " 1").join("\n") + "\n");
  const broken = names.filter((n) => !tests[n].ok);
  console.log(`${names.length} tests mapped, ${broken.length} fail alone: ${broken.join(", ") || "none"}`);
}

function writeShim(dir: string) {
  mkdirSync(join(dir, "bin"), { recursive: true });
  const self = resolve(import.meta.path);
  const shim = join(dir, "bin", "go");
  writeFileSync(
    shim,
    `#!/bin/sh\n# Written by scripts/mutation-select.ts; see there.\nif [ "$1" = test ]; then exec bun ${JSON.stringify(self)} wrap "$@"; fi\nexec "$MUT_REAL_GO" "$@"\n`,
  );
  chmodSync(shim, 0o755);
  console.log(shim);
}

function runReal(args: string[], cwd: string): never {
  const r = spawnSync(realGo(), args, { cwd, stdio: "inherit", env: process.env });
  process.exit(r.status ?? 1);
}

// The first line on which two versions of a file differ.
function firstDiff(a: string, b: string): number {
  const x = a.split("\n");
  const y = b.split("\n");
  for (let i = 0; i < Math.max(x.length, y.length); i++) if (x[i] !== y[i]) return i + 1;
  return 0;
}

async function wrap(args: string[]) {
  const dir = process.env.MUT_MAP;
  const pkg = process.env.MUT_PKG;
  const src = process.env.MUT_SRC;
  if (!dir || !pkg || !src) runReal(args, process.cwd());
  const testArgs = args.slice(1);
  const all = JSON.parse(readFileSync(join(dir!, "lines.json"), "utf8")) as { tests: string[]; lines: Record<string, Record<number, number[]>> };
  const status = JSON.parse(readFileSync(join(dir!, "tests.json"), "utf8")) as Record<string, TestInfo>;
  const passing = all.tests.filter((n) => status[n]?.ok);

  if (testArgs.includes("-coverprofile")) {
    const out = testArgs[testArgs.indexOf("-coverprofile") + 1];
    const t0 = Date.now();
    writeFileSync(out, readFileSync(join(dir!, "merged.cov")));
    const floor = Number(process.env.MUT_COVER_SECS ?? 40) * 1000;
    const left = floor - (Date.now() - t0);
    if (left > 0) await new Promise((r) => setTimeout(r, left));
    process.exit(0);
  }

  // A mutant's run: the worker's copy of the package is the cwd.
  const cwd = process.cwd();
  let picked: string[] | null = null;
  for (const f of readdirSync(cwd)) {
    if (!f.endsWith(".go") || f.endsWith("_test.go")) continue;
    const orig = join(src!, pkg!, f);
    if (!existsSync(orig)) continue;
    const a = readFileSync(join(cwd, f), "utf8");
    const b = readFileSync(orig, "utf8");
    if (a === b) continue;
    const ln = firstDiff(a, b);
    const idx = all.lines[f]?.[ln];
    if (idx) picked = idx.map((i) => all.tests[i]).filter((n) => status[n]?.ok);
    break;
  }
  const names = new Set(picked && picked.length ? picked : passing);
  for (const n of ALWAYS) if (status[n]?.ok) names.add(n);
  // Two stages, cheap tests first. The mutant is killed if either stage fails,
  // so this changes the cost, not the verdict.
  const cheap = [...names].filter((n) => (status[n]?.suite ?? 0) <= 1);
  const slow = [...names].filter((n) => (status[n]?.suite ?? 0) > 1);
  if (process.env.MUT_LOG) writeFileSync(process.env.MUT_LOG, `${new Date().toISOString()} ${cwd} ${cheap.length}+${slow.length}\n`, { flag: "a" });
  // One build for both stages. `go test -c` fails exactly when `go test` fails to
  // build (both exit 1, which gremlins reads as killed).
  const tagsAt = testArgs.indexOf("-tags");
  const tags = tagsAt >= 0 ? testArgs[tagsAt + 1] : undefined;
  const pkgArg = testArgs[testArgs.length - 1];
  const bin = join(mkdtempSync(join(tmpdir(), "mutsel-")), "pkg.test");
  const build = spawnSync(realGo(), ["test", "-c", "-o", bin, ...(tags ? ["-tags", tags] : []), pkgArg], { cwd, stdio: "inherit", env: process.env });
  if (build.status !== 0) process.exit(1);
  let code = 0;
  for (const stage of [cheap, slow]) {
    if (!stage.length) continue;
    const budget = Math.round(60 + 5 * stage.reduce((t, n) => t + (status[n]?.suite ?? 1), 0));
    const pattern = "^(" + stage.join("|") + ")$";
    const r = spawnSync(bin, ["-test.run=" + pattern, "-test.failfast", "-test.timeout=" + budget + "s"], { cwd, stdio: "ignore", env: process.env });
    if (r.status !== 0) {
      code = 1;
      break;
    }
  }
  rmSync(dirname(bin), { recursive: true, force: true });
  process.exit(code);
}

const [mode, ...rest] = process.argv.slice(2);
if (mode === "map") await buildMap(rest[0], rest[1]);
else if (mode === "shim") writeShim(rest[0]);
else if (mode === "wrap") await wrap(rest);
else if (import.meta.main) {
  console.error("usage: mutation-select.ts map <pkg> <dir> | shim <dir>");
  process.exit(2);
}
