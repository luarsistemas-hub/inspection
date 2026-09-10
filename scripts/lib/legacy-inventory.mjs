#!/usr/bin/env node
import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";

const args = parseArgs(process.argv.slice(2));
const command = args._[0];
if (!command || !["generate", "validate"].includes(command)) fail("usage: legacy-inventory.mjs generate|validate [options]");

if (command === "generate") {
  const repository = resolve(args.repository ?? process.cwd());
  const root = resolve(repository, args["legacy-root"] ?? "apps/web");
  const output = resolve(repository, args.output ?? "docs/legacy-inventory.json");
  const files = existsSync(root) ? walk(root).map((path) => makeEntry(path, root)) : [];
  const inventory = { schemaVersion: 1, legacyRoot: relative(repository, root), generatedBy: "scripts/lib/legacy-inventory.mjs", files, activeReferences: findActiveReferences(repository, root, output) };
  writeFileSync(output, `${JSON.stringify(inventory, null, 2)}\n`);
  process.stdout.write(`legacy inventory: ${files.length} files, ${inventory.activeReferences.length} active references\n`);
} else {
  const input = resolve(process.cwd(), args.input ?? "docs/legacy-inventory.json");
  const inventory = readJSON(input);
  validateInventory(inventory, { failOnUnclassified: Boolean(args["fail-on-unclassified"]), failOnDrift: Boolean(args["fail-on-drift"]), baseline: args.baseline ? readJSON(resolve(process.cwd(), args.baseline)) : undefined });
  process.stdout.write(`legacy inventory valid: ${inventory.files.length} files\n`);
}

function parseArgs(argv) { const result = { _: [] }; for (let i = 0; i < argv.length; i += 1) { const token = argv[i]; if (!token.startsWith("--")) result._.push(token); else if (argv[i + 1] && !argv[i + 1].startsWith("--")) result[token.slice(2)] = argv[++i]; else result[token.slice(2)] = true; } return result; }
function walk(root) { const paths = []; for (const entry of readdirSync(root, { withFileTypes: true })) { if (entry.isDirectory() && [".git", ".next", "node_modules", "playwright-report", "test-results", "dist"].includes(entry.name)) continue; const path = join(root, entry.name); if (entry.isDirectory()) paths.push(...walk(path)); else if (entry.isFile()) paths.push(path); } return paths.sort(); }
function makeEntry(path, root) { return { path: relative(root, path).split("\\").join("/"), fingerprint: createHash("sha256").update(readFileSync(path)).digest("hex"), classification: "explicitly_retired", owner: ownerFor(path) }; }
function ownerFor(path) { if (/capture/.test(path)) return "apps/capture"; if (/dashboard/.test(path)) return "apps/dashboard"; return "apps/admin"; }
function findActiveReferences(repository, legacyRoot, output) {
  const ignored = new Set([".git", ".next", "node_modules", "playwright-report", "test-results"]); const references = [];
  for (const path of walk(repository)) {
    const rel = relative(repository, path).split("\\").join("/");
    if (path.startsWith(`${legacyRoot}/`) || resolve(path) === resolve(output) || rel === ".gitignore" || rel.endsWith(".test.mjs") || rel.startsWith(".compozy/") || ignored.has(rel.split("/")[0])) continue;
    let text; try { text = readFileSync(path, "utf8"); } catch { continue; }
    text.split(/\r?\n/).forEach((line, index) => {
      if (/legacy-root apps\/web|legacyRoot.*apps\/web|default.*apps\/web|legacy-root.*apps\/web/.test(line)) return;
      if (/apps\/web(?:[/'"`\s]|$)|\bweb\/(?:Dockerfile|package\.json)/.test(line)) references.push({ path: rel, line: index + 1, text: line.trim().slice(0, 240) });
    });
  }
  return references;
}
function validateInventory(inventory, options) {
  const errors = []; const allowed = new Set(["migrated", "explicitly_retired", "blocking"]);
  if (inventory?.schemaVersion !== 1) errors.push("unsupported schemaVersion");
  if (!Array.isArray(inventory?.files)) errors.push("files must be an array");
  for (const file of inventory.files ?? []) if (!file.path || !file.fingerprint || !allowed.has(file.classification) || !file.owner) errors.push(`invalid file entry: ${file.path ?? "<missing>"}`);
  if (options.failOnUnclassified && (inventory.files ?? []).some((file) => !allowed.has(file.classification))) errors.push("unclassified legacy file");
  if (options.failOnDrift && (inventory.activeReferences ?? []).length) errors.push(`active legacy references: ${inventory.activeReferences.length}`);
  if (options.baseline) errors.push(...compareBaseline(options.baseline, inventory));
  if (errors.length) fail(errors.join("; "));
}
function compareBaseline(baseline, current) { const old = new Map((baseline.files ?? []).map((file) => [file.path, file.fingerprint])); const now = new Map((current.files ?? []).map((file) => [file.path, file.fingerprint])); const errors = []; for (const [path, fingerprint] of old) if (now.get(path) !== fingerprint) errors.push(`legacy fingerprint drift: ${path}`); for (const path of now.keys()) if (!old.has(path)) errors.push(`legacy inventory gained file: ${path}`); return errors; }
function readJSON(path) { try { return JSON.parse(readFileSync(path, "utf8")); } catch (error) { fail(`cannot read JSON ${path}: ${error.message}`); } }
function fail(message) { process.stderr.write(`legacy inventory error: ${message}\n`); process.exit(1); }
