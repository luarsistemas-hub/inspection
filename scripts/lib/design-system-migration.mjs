#!/usr/bin/env node
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const CRITERION_IDS = [
  "1.1.1", "1.2.1", "1.2.2", "1.2.3", "1.2.4", "1.2.5", "1.3.1", "1.3.2", "1.3.3", "1.3.4", "1.3.5",
  "1.4.1", "1.4.2", "1.4.3", "1.4.4", "1.4.5", "1.4.10", "1.4.11", "1.4.12", "1.4.13",
  "2.1.1", "2.1.2", "2.1.4", "2.2.1", "2.2.2", "2.3.1", "2.4.1", "2.4.2", "2.4.3", "2.4.4", "2.4.5", "2.4.6", "2.4.7", "2.4.11",
  "2.5.1", "2.5.2", "2.5.3", "2.5.4", "2.5.7", "2.5.8", "3.1.1", "3.1.2", "3.2.1", "3.2.2", "3.2.3", "3.2.4", "3.2.6",
  "3.3.1", "3.3.2", "3.3.3", "3.3.4", "3.3.7", "3.3.8", "4.1.2", "4.1.3",
];

const VALID_RESULTS = new Set(["pass", "fail", "not-tested", "blocked", "not-applicable"]);
const PAGE_DIRS = ["admin", "dashboard", "capture", "onboarding"];
const IGNORED_DIRS = new Set([".next", "node_modules", "dist", "playwright-report", "test-results"]);

export function validateMigrationInventory(inventory, discoveredRoutes = [], knownCases = [], repository = process.cwd()) {
  const errors = [];
  const rows = inventory?.routes ?? inventory?.routeCoverage;
  if (inventory?.schemaVersion !== 1) errors.push("inventory schemaVersion must be 1");
  if (!Array.isArray(rows)) return ["inventory routes must be an array"];

  const knownStoryIds = new Set((knownCases ?? []).map((item) => typeof item === "string" ? item : item?.id).filter(Boolean));
  const seenIds = new Set();
  const seenRoutes = new Set();
  for (const row of rows) {
    const key = `${row?.application ?? ""}:${row?.route ?? ""}`;
    if (!row?.id || seenIds.has(row.id)) errors.push(`duplicate or missing inventory id: ${row?.id ?? "<missing>"}`);
    seenIds.add(row?.id);
    if (!row?.application || !row?.route || !row?.pageSource) errors.push(`incomplete inventory route: ${row?.id ?? "<missing>"}`);
    if (seenRoutes.has(key)) errors.push(`duplicate inventory route: ${key}`);
    seenRoutes.add(key);
    if (!Array.isArray(row?.domainStories) || row.domainStories.length === 0) errors.push(`route has no domain stories: ${key}`);
    if (knownStoryIds.size > 0) for (const story of row.domainStories ?? []) if (!knownStoryIds.has(story)) errors.push(`unknown story ${story} in ${key}`);
    if (!Array.isArray(row?.stateProfiles) || row.stateProfiles.length === 0) errors.push(`route has no state profiles: ${key}`);
    if (!row?.owner?.task || !row.owner.accountable) errors.push(`route has no accountable owner: ${key}`);
    if (!row?.evidence || !Array.isArray(row.evidence.automated) || !Array.isArray(row.evidence.manual)) errors.push(`route has no explicit evidence record: ${key}`);
    if (row?.pageSource && !existsSync(resolve(repository, row.pageSource))) errors.push(`inventory page source does not exist: ${row.pageSource}`);
  }

  const discovered = discoveredRoutes.map(normalizeRoute);
  const inventoryKeys = new Set(rows.map((row) => `${row.application}:${row.route}`));
  for (const route of discovered) if (!inventoryKeys.has(`${route.application}:${route.route}`)) errors.push(`discovered route missing from inventory: ${route.application}:${route.route}`);
  for (const row of rows) if (discovered.length > 0 && !discovered.some((route) => route.application === row.application && route.route === row.route)) errors.push(`inventory route is not present on disk: ${row.application}:${row.route}`);

  if (!Array.isArray(inventory.embeddedSurfaces)) errors.push("inventory embeddedSurfaces must be an array");
  if (!Array.isArray(inventory.patterns) || inventory.patterns.length === 0) errors.push("inventory patterns must document shared patterns");
  if (!Array.isArray(inventory.exceptions)) errors.push("inventory exceptions must be an array");
  return errors;
}

export function validateAccessibilityEvidence(input, criteria = CRITERION_IDS, inventory = input?.inventory) {
  const errors = [];
  const records = Array.isArray(input) ? input : input?.records;
  if (!Array.isArray(records)) return ["accessibility evidence records must be an array"];
  const criterionIds = new Set((criteria ?? []).map((criterion) => typeof criterion === "string" ? criterion : criterion?.id).filter(Boolean));
  if (criterionIds.size !== 55) errors.push(`accessibility criteria must contain 55 entries; found ${criterionIds.size}`);
  const inventorySurfaces = [...(inventory?.routes ?? inventory?.routeCoverage ?? []), ...(inventory?.embeddedSurfaces ?? [])];
  const expectedTargets = new Map();
  for (const surface of inventorySurfaces) {
    for (const state of surface?.stateProfiles ?? []) {
      expectedTargets.set(`${surface?.id ?? ""}:${surface?.route ?? ""}:${state}`, surface);
    }
  }
  const seen = new Set();
  const observedCriteria = new Set();
  const observedTargets = new Set();
  for (const record of records) {
    const key = `${record?.surfaceId ?? ""}:${record?.route ?? ""}:${record?.state ?? ""}:${record?.criterion ?? ""}`;
    if (!record?.surfaceId || !record?.route || !record?.state || !record?.criterion) errors.push(`incomplete accessibility evidence row: ${key}`);
    if (seen.has(key)) errors.push(`duplicate accessibility evidence row: ${key}`);
    seen.add(key);
    const targetKey = `${record?.surfaceId ?? ""}:${record?.route ?? ""}:${record?.state ?? ""}`;
    if (expectedTargets.size > 0 && !expectedTargets.has(targetKey)) errors.push(`accessibility evidence target is not in inventory: ${targetKey}`);
    if (expectedTargets.size > 0) observedTargets.add(targetKey);
    if (!String(record?.persona ?? "").trim()) errors.push(`missing persona in accessibility evidence row: ${key}`);
    if (!String(record?.executionContext ?? "").trim()) errors.push(`missing executionContext in accessibility evidence row: ${key}`);
    if (record?.criterion) observedCriteria.add(record.criterion);
    if (!criterionIds.has(record?.criterion)) errors.push(`unknown accessibility criterion ${record?.criterion ?? "<missing>"}`);
    const result = record?.result ?? record?.verdict;
    const requiredResult = String(record?.requiredResult ?? record?.execution?.result ?? "").toLowerCase();
    if (!VALID_RESULTS.has(result)) errors.push(`invalid accessibility result for ${key}: ${result ?? "<missing>"}`);
    if (["not-tested", "blocked"].includes(result)) errors.push(`accessibility evidence is unresolved for ${key}: ${result}`);
    if (result === "pass" && ["skip", "skipped", "untested", "not-tested", "blocked"].includes(requiredResult)) errors.push(`pass evidence is unresolved for ${key}: ${requiredResult}`);
    if (result === "not-applicable" && !String(record?.applicabilityRationale ?? record?.rationale ?? "").trim()) errors.push(`not-applicable requires a route/state rationale for ${key}`);
    if (["pass", "fail", "blocked"].includes(result)) {
      for (const field of ["testId", "browser", "viewport", "fixture", "steps", "observed", "artifact", "reviewer", "executedAt"]) if (!record?.[field]) errors.push(`missing ${field} in accessibility evidence row: ${key}`);
    }
  }
  for (const criterion of criterionIds) if (!observedCriteria.has(criterion)) errors.push(`missing accessibility criterion ${criterion}`);
  for (const targetKey of expectedTargets.keys()) {
    if (!observedTargets.has(targetKey)) errors.push(`missing accessibility evidence target: ${targetKey}`);
    for (const criterion of criterionIds) {
      const evidenceKey = `${targetKey}:${criterion}`;
      if (!seen.has(evidenceKey)) errors.push(`missing accessibility evidence row: ${evidenceKey}`);
    }
  }
  return errors;
}

export function auditPresentationSources(files, documentedExceptions = []) {
  const errors = [];
  const exceptions = new Set((documentedExceptions ?? []).map((item) => typeof item === "string" ? item : item?.path).filter(Boolean));
  const entries = (files ?? []).map((file) => typeof file === "string" ? { path: file, content: readText(file) } : file);
  for (const entry of entries) {
    const path = entry?.path ?? "<unknown>";
    const content = String(entry?.content ?? "");
    if (/(?:^|["'])react-aria-components(?:["']|$)/m.test(content) && !path.startsWith("packages/inspection-design-system/")) {
      if (!exceptions.has(path)) errors.push(`application-owned react-aria-components import: ${path}`);
    }
  }
  const reusableDialogs = entries.filter((entry) => /(?:^|\/)(?:[^/]*(?:modal|dialog)[^/]*)\.(?:tsx?|jsx?)$/i.test(entry?.path ?? "") && /(?:<Dialog\b|aria-modal|role=["']dialog|ModalOverlay|className=["'][^"']*modal|(?:function|const)\s+\w*Modal\b)/.test(String(entry?.content ?? ""))).map((entry) => entry.path);
  const undocumented = reusableDialogs.filter((path) => !exceptions.has(path));
  if (undocumented.length > 1) errors.push(`undocumented reusable modal implementations: ${undocumented.join(", ")}`);
  return errors;
}

export function validateUsageGuidance(guidance, inventory = {}) {
  const text = String(guidance ?? "");
  const errors = [];
  for (const pattern of ["Combobox", "Field", "Dialog", "Confirmation", "Recovery"]) {
    if (!new RegExp(`\\b${pattern}\\b`, "i").test(text)) errors.push(`usage guidance omits ${pattern}`);
  }
  for (const requirement of ["320", "200%", "400%", "scrolls inside the dialog", "keyboard"]) {
    if (!text.includes(requirement)) errors.push(`usage guidance omits responsive requirement: ${requirement}`);
  }
  if (!/Synthetic example[\s\S]*no credentials[\s\S]*invitation token[\s\S]*personal\s+evidence/i.test(text)) errors.push("synthetic example safety boundary is missing");
  if (/eyJ[A-Za-z0-9_-]{20,}|invitationToken\s*[:=]\s*["'][^"']+["']/i.test(text)) errors.push("usage guidance contains a token-shaped synthetic fixture");
  if (!/primary action|primary button|Button.*action/i.test(text)) errors.push("primary action meaning is undocumented");
  if (/contradictory|different meaning/i.test(text)) errors.push("primary action meaning is contradictory");
  for (const exception of inventory.exceptions ?? []) {
    if (typeof exception === "object" && !String(exception.removalCondition ?? "").trim()) errors.push(`exception has no removal condition: ${exception.path ?? "<unknown>"}`);
  }
  return errors;
}

export function validateBoundaryErrorFixture(fixture) {
  const errors = [];
  const message = String(fixture?.message ?? "");
  if (!String(fixture?.correlationId ?? "").trim()) errors.push("boundary error fixture must include correlationId");
  if (/stack|trace|at\s+\w+\s*\(/i.test(message)) errors.push("boundary error fixture exposes an internal trace");
  if (!String(fixture?.safeMessage ?? "").trim()) errors.push("boundary error fixture must define safeMessage");
  return errors;
}

export function validateMembershipTransition(previous, next, visiblePayloads = []) {
  const errors = [];
  if (!previous?.membershipId || !next?.membershipId || previous.membershipId === next.membershipId) errors.push("membership transition must change membershipId");
  for (const payload of visiblePayloads) if (payload?.membershipId === previous.membershipId) errors.push(`stale protected payload rendered: ${payload.kind ?? "unknown"}`);
  return errors;
}

export function validatePackageShells(files, exceptions = []) {
  return auditPresentationSources(files, exceptions).filter((error) => error.includes("react-aria-components") || error.includes("reusable modal"));
}

export function discoverPageRoutes(repository = process.cwd()) {
  const routes = [];
  for (const application of PAGE_DIRS) {
    const root = join(repository, "apps", application, "app");
    if (!existsSync(root)) continue;
    for (const path of walk(root)) if (path.endsWith("/page.tsx") || path.endsWith("/page.ts")) {
      const relativePage = relative(root, path).split("\\").join("/").replace(/(?:^|\/)page\.tsx?$/, "");
      const route = relativePage ? `/${relativePage.split("/").map((part) => part.startsWith("[") ? part : part).join("/")}` : "/";
      routes.push({ application, route, pageSource: relative(repository, path).split("\\").join("/") });
    }
  }
  return routes.sort((a, b) => `${a.application}:${a.route}`.localeCompare(`${b.application}:${b.route}`));
}

export function validateRealE2EPrerequisites(environment = process.env, repository = process.cwd()) {
  const errors = [];
  for (const variable of ["INSPECTION_E2E_USERNAME", "INSPECTION_E2E_PASSWORD", "KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME", "KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD"]) if (!environment[variable]) errors.push(`missing real-E2E prerequisite: ${variable}`);
  if (environment.INSPECTION_E2E_AUTH !== "true") errors.push("INSPECTION_E2E_AUTH must be true for the real-E2E gate");
  if (!existsSync(join(repository, ".env.example"))) errors.push("missing real-E2E prerequisite: .env.example");
  for (const path of ["apps/admin/tests/e2e/activation.spec.ts", "apps/dashboard/tests/e2e/recapture-cross-product.spec.ts", "apps/dashboard/tests/e2e/design-system-cross-product.spec.ts"]) if (!existsSync(join(repository, path))) errors.push(`missing real-E2E acceptance surface: ${path}`);
  return errors;
}

function normalizeRoute(route) {
  if (typeof route === "string") {
    const match = route.match(/^([^:]+):(\/.*)$/);
    return match ? { application: match[1], route: match[2] } : { application: "", route };
  }
  return route ?? { application: "", route: "" };
}

function readText(path) { try { return readFileSync(resolve(path), "utf8"); } catch { return ""; } }
function walk(root) {
  const paths = [];
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    if (IGNORED_DIRS.has(entry.name)) continue;
    const path = join(root, entry.name);
    if (entry.isDirectory()) paths.push(...walk(path));
    else if (entry.isFile()) paths.push(path);
  }
  return paths.sort();
}
function parseArgs(argv) { const result = { _: [] }; for (let i = 0; i < argv.length; i += 1) { const token = argv[i]; if (!token.startsWith("--")) result._.push(token); else if (argv[i + 1] && !argv[i + 1].startsWith("--")) result[token.slice(2)] = argv[++i]; else result[token.slice(2)] = true; } return result; }
function readJSON(path) { try { return JSON.parse(readFileSync(path, "utf8")); } catch (error) { fail(`cannot read JSON ${path}: ${error.message}`); } }
function fail(message) { process.stderr.write(`design-system migration error: ${message}\n`); process.exitCode = 1; }

if (fileURLToPath(import.meta.url) === resolve(process.argv[1] ?? "")) {
  const args = parseArgs(process.argv.slice(2));
  const command = args._[0];
  const repository = resolve(args.repository ?? process.cwd());
  if (command === "validate") {
    const inventoryPath = resolve(repository, args.inventory ?? "docs/design-system/migration-inventory.json");
    const inventory = readJSON(inventoryPath);
    const knownCases = [...Array.from({ length: 37 }, (_, index) => `US-${String(index + 1).padStart(3, "0")}`)];
    const errors = [
      ...validateMigrationInventory(inventory, discoverPageRoutes(repository), knownCases, repository),
      ...validateUsageGuidance(readFileSync(join(repository, "docs/design-system/usage.md"), "utf8"), inventory),
      ...auditPresentationSources(discoverPresentationFiles(repository), inventory.exceptions),
    ];
    if (args.evidence) errors.push(...validateAccessibilityEvidence(readJSON(resolve(repository, args.evidence)), CRITERION_IDS, inventory));
    if (errors.length) fail(errors.join("; ")); else process.stdout.write(`design-system migration inventory valid: ${inventory.routes?.length ?? inventory.routeCoverage?.length} routes\n`);
  } else if (command === "prerequisites") {
    const errors = validateRealE2EPrerequisites(process.env, repository);
    if (errors.length) fail(errors.join("; ")); else process.stdout.write("real-E2E prerequisites available\n");
  } else {
    fail("usage: design-system-migration.mjs validate|prerequisites [--repository path] [--inventory path] [--evidence path]");
  }
}

function discoverPresentationFiles(repository) {
  const files = [];
  for (const application of PAGE_DIRS) {
    const roots = [join(repository, "apps", application, "app"), join(repository, "apps", application, "src")];
    for (const root of roots) if (existsSync(root)) for (const path of walk(root)) if (/\.(?:tsx?|jsx?|css)$/.test(path)) files.push({ path: relative(repository, path).split("\\").join("/"), content: readFileSync(path, "utf8") });
  }
  return files;
}

export { CRITERION_IDS };
