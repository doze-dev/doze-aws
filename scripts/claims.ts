// Prints, per service, how many claims are recorded and what the rest rest on.
//
//   bun scripts/claims.ts
//
// A claim is RECORDED when its scenario has an entry in
// conformance/snapshots/<module>.json and that file was recorded against AWS
// (`_recorded.target` is "aws"); otherwise it is what its Source column says.
// Run after the conformance suite, which prints its own matched/unverified
// counts: those are about responses, these are about beliefs.
import { readdirSync, readFileSync, existsSync } from "node:fs";

const claimsDir = "conformance/claims";
const snapDir = "conformance/snapshots";

type Row = { claim: string; source: string; scenario: string };

function recordedScenarios(): Set<string> {
  const out = new Set<string>();
  if (!existsSync(snapDir)) return out;
  for (const f of readdirSync(snapDir).filter((n) => n.endsWith(".json"))) {
    const data = JSON.parse(readFileSync(`${snapDir}/${f}`, "utf8"));
    if (data._recorded?.target !== "aws") continue;
    const mod = f.replace(/\.json$/, "");
    for (const k of Object.keys(data)) if (k !== "_recorded") out.add(`${mod}::${k}`);
  }
  return out;
}

const recorded = recordedScenarios();
const rows = new Map<string, Row[]>();
for (const f of readdirSync(claimsDir).filter((n) => n.endsWith(".md") && n !== "README.md").sort()) {
  const list: Row[] = [];
  for (const line of readFileSync(`${claimsDir}/${f}`, "utf8").split("\n")) {
    const m = /^\|\s*(.+?)\s*\|\s*(model|doc|memory)\s*\|\s*(.+?)\s*\|$/.exec(line);
    if (m && !line.startsWith("| Claim")) list.push({ claim: m[1], source: m[2], scenario: m[3] });
  }
  rows.set(f.replace(/\.md$/, ""), list);
}

console.log("\nclaims (conformance/claims):");
console.log("  service           claims  recorded  model  doc  memory  to write");
let total = 0, totalRecorded = 0;
for (const [svc, list] of rows) {
  const isRecorded = (r: Row) => recorded.has(r.scenario);
  const rec = list.filter(isRecorded).length;
  const count = (s: string) => list.filter((r) => !isRecorded(r) && r.source === s).length;
  const toWrite = list.filter((r) => r.scenario === "(to write)").length;
  total += list.length;
  totalRecorded += rec;
  console.log(
    `  ${svc.padEnd(16)} ${String(list.length).padStart(7)} ${String(rec).padStart(9)} ${String(count("model")).padStart(6)}` +
      ` ${String(count("doc")).padStart(4)} ${String(count("memory")).padStart(7)} ${String(toWrite).padStart(9)}`,
  );
}
console.log(`  ${"all".padEnd(16)} ${String(total).padStart(7)} ${String(totalRecorded).padStart(9)}`);
if (totalRecorded === 0) console.log("  (nothing is recorded: conformance/snapshots has no recording made against AWS)");
