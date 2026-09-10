#!/usr/bin/env node
import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, writeFileSync, mkdirSync } from "node:fs";
import { join, resolve } from "node:path";

const args = parseArgs(process.argv.slice(2)); const command = args._[0];
if (!command || !["generate", "validate"].includes(command)) fail("usage: parity-evidence.mjs generate|validate");
if (command === "generate") {
  const input = resolve(args.input ?? "."); const output = resolve(args.output ?? "artifacts/parity-evidence"); mkdirSync(output, { recursive: true });
  const files = existsSync(input) ? walk(input).map((path) => ({ path: path.slice(input.length + 1), sha256: sha256(readFileSync(path)) })) : [];
  const schemaPath = resolve(process.cwd(), "services/inspection/schema.graphqls");
  const evidence = { schemaVersion: 1, journey: String(args.journey ?? "unknown"), testIds: String(args["test-id"] ?? args.journey ?? "unknown").split(",").filter(Boolean), route: String(args.route ?? "dashboard -> capture"), runIdentity: process.env.GITHUB_RUN_ID ? `github:${process.env.GITHUB_RUN_ID}` : `local:${process.pid}`, timestamp: new Date().toISOString(), schemaHash: process.env.INSPECTION_SCHEMA_HASH ?? (existsSync(schemaPath) ? sha256(readFileSync(schemaPath)) : "unknown"), artifacts: files, redaction: { credentials: false, tokens: false, contacts: false, media: false } };
  writeFileSync(join(output, "evidence.json"), `${JSON.stringify(evidence, null, 2)}\n`); process.stdout.write(`parity evidence: ${files.length} artifacts for ${evidence.journey}\n`);
} else {
  const input = resolve(process.cwd(), args.input ?? "artifacts/parity-evidence"); const evidence = readJSON(join(input, "evidence.json")); const errors = [];
  for (const field of ["schemaVersion", "journey", "testIds", "route", "runIdentity", "timestamp", "schemaHash", "artifacts", "redaction"]) if (evidence[field] === undefined) errors.push(`missing ${field}`);
  if (!evidence.testIds?.length) errors.push("testIds must not be empty"); if (!evidence.redaction || Object.values(evidence.redaction).some((value) => value !== false)) errors.push("redaction contract failed");
  if (JSON.stringify(evidence).match(/password|access_token|refresh_token|authorization:|Bearer\s+[A-Za-z0-9._-]+/i)) errors.push("sensitive value detected"); if (errors.length) fail(errors.join(";")); process.stdout.write(`parity evidence valid: ${evidence.testIds.join(", ")}\n`);
}
function parseArgs(argv) { const result = { _: [] }; for (let i = 0; i < argv.length; i += 1) { const token = argv[i]; if (!token.startsWith("--")) result._.push(token); else if (argv[i + 1] && !argv[i + 1].startsWith("--")) result[token.slice(2)] = argv[++i]; else result[token.slice(2)] = true; } return result; }
function walk(root) { const result = []; let entries; try { entries = readdirSync(root, { withFileTypes: true }); } catch { return result; } for (const entry of entries) { const path = join(root, entry.name); if (entry.isDirectory()) result.push(...walk(path)); else if (entry.isFile()) result.push(path); } return result.sort(); }
function sha256(value) { return createHash("sha256").update(value).digest("hex"); }
function readJSON(path) { try { return JSON.parse(readFileSync(path, "utf8")); } catch (error) { fail(`cannot read ${path}: ${error.message}`); } }
function fail(message) { process.stderr.write(`parity evidence error: ${message}\n`); process.exit(1); }
