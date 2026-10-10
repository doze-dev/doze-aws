// Record or check the mutation baseline for one package.
//
//   bun scripts/mutation-baseline.ts record internal/iampolicy
//   bun scripts/mutation-baseline.ts check  internal/iampolicy
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
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const GREMLINS = "github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0";
const FILE = "testdata/mutation.json";

const [mode, pkg] = process.argv.slice(2);
if ((mode !== "record" && mode !== "check") || !pkg) {
  console.error("usage: mutation-baseline.ts record|check <package dir, e.g. internal/iampolicy>");
  process.exit(2);
}

const dir = mkdtempSync(join(tmpdir(), "gremlins-"));
const out = join(dir, "out.json");
const run = spawnSync(
  "go",
  ["run", GREMLINS, "unleash", "--timeout-coefficient", "10", "--workers", "2", "-o", out, `./${pkg}`],
  { stdio: ["ignore", "ignore", "inherit"], env: { ...process.env, GOWORK: "off" } },
);
if (!existsSync(out)) {
  console.error(`gremlins produced no result for ${pkg} (exit ${run.status})`);
  process.exit(1);
}

type Mutation = { type: string; status: string; line: number };
const result = JSON.parse(readFileSync(out, "utf8")) as { files: { file_name: string; mutations: Mutation[] }[] };
const counts = { killed: 0, lived: 0, timedOut: 0, notCovered: 0, notViable: 0 };
const lived: string[] = [];
for (const f of result.files ?? []) {
  for (const m of f.mutations) {
    switch (m.status) {
      case "KILLED": counts.killed++; break;
      case "LIVED": counts.lived++; lived.push(`${f.file_name}:${m.line}:${m.type}`); break;
      case "TIMED OUT": counts.timedOut++; break;
      case "NOT COVERED": counts.notCovered++; break;
      case "NOT VIABLE": counts.notViable++; break;
    }
  }
}
lived.sort();
const tested = counts.killed + counts.lived;
const efficacy = tested === 0 ? 100 : Math.round((counts.killed / tested) * 1000) / 10;

const baseline: Record<string, unknown> = existsSync(FILE) ? JSON.parse(readFileSync(FILE, "utf8")) : {};
const was = baseline[pkg] as { lived?: string[] } | undefined;

if (mode === "record") {
  baseline[pkg] = { ...counts, efficacy, lived, recorded: new Date().toISOString().slice(0, 10), gremlins: "v0.6.0" };
  const sorted = Object.fromEntries(Object.entries(baseline).sort(([a], [b]) => a.localeCompare(b)));
  writeFileSync(FILE, JSON.stringify(sorted, null, 2) + "\n");
  console.log(`${pkg}: killed ${counts.killed}, lived ${counts.lived}, timed out ${counts.timedOut}, not covered ${counts.notCovered}, efficacy ${efficacy}%`);
  process.exit(0);
}

if (!was) {
  console.error(`no baseline for ${pkg}; run: bun scripts/mutation-baseline.ts record ${pkg}`);
  process.exit(1);
}
const had = new Set(was.lived ?? []);
const fresh = lived.filter((m) => !had.has(m));
const gone = [...had].filter((m) => !lived.includes(m));
console.log(`${pkg}: killed ${counts.killed}, lived ${counts.lived} (baseline ${had.size}), efficacy ${efficacy}%`);
if (gone.length) console.log(`  no longer live (record again): ${gone.join(", ")}`);
if (fresh.length) {
  console.error(`  NEW survivors, each a behaviour nothing asserts:\n    ${fresh.join("\n    ")}`);
  process.exit(1);
}
