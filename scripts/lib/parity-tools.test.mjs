import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const inventoryTool = join(process.cwd(), "scripts/lib/legacy-inventory.mjs");
const evidenceTool = join(process.cwd(), "scripts/lib/parity-evidence.mjs");

test("UT-129 legacy inventory classifies every file and preserves fingerprints", () => {
  const root = mkdtempSync(join(tmpdir(), "inspection-inventory-"));
  const legacy = join(root, "apps/web"); const output = join(root, "inventory.json");
  execFileSync("mkdir", ["-p", join(legacy, "app")]); writeFileSync(join(legacy, "app/page.tsx"), "export default function Page() {}\n");
  execFileSync("node", [inventoryTool, "generate", "--repository", root, "--legacy-root", "apps/web", "--output", "inventory.json"]);
  execFileSync("node", [inventoryTool, "validate", "--input", output, "--fail-on-unclassified", "--fail-on-drift"]);
  const inventory = JSON.parse(readFileSync(output, "utf8"));
  if (inventory.files.length !== 1 || inventory.files[0].classification !== "explicitly_retired") throw new Error("legacy file was not classified");
});

test("UT-165.05 inventory drift and active references fail closed", () => {
  const root = mkdtempSync(join(tmpdir(), "inspection-drift-"));
  const legacy = join(root, "apps/web"); const output = join(root, "inventory.json"); const baseline = join(root, "baseline.json");
  execFileSync("mkdir", ["-p", legacy]); writeFileSync(join(legacy, "README.md"), "legacy\n"); writeFileSync(join(root, "consumer.txt"), "apps/web/README.md\n");
  execFileSync("node", [inventoryTool, "generate", "--repository", root, "--legacy-root", "apps/web", "--output", "inventory.json"]);
  writeFileSync(baseline, readFileSync(output));
  writeFileSync(join(legacy, "README.md"), "changed\n");
  let failed = false; try { execFileSync("node", [inventoryTool, "validate", "--input", output, "--baseline", baseline, "--fail-on-drift"]); } catch { failed = true; }
  if (!failed) throw new Error("inventory drift was accepted");
});

test("UT-165.09 parity evidence keeps execution identity and redaction contract", () => {
  const root = mkdtempSync(join(tmpdir(), "inspection-evidence-")); const output = join(root, "evidence");
  writeFileSync(join(root, "result.txt"), "safe result\n");
  execFileSync("node", [evidenceTool, "generate", "--journey", "E2E-033", "--test-id", "E2E-033", "--input", root, "--output", output]);
  execFileSync("node", [evidenceTool, "validate", "--input", output]);
});
