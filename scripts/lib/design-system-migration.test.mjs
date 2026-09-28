import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import {
  CRITERION_IDS,
  auditPresentationSources,
  validateBoundaryErrorFixture,
  validateAccessibilityEvidence,
  validateMembershipTransition,
  validateMigrationInventory,
  validatePackageShells,
  validateUsageGuidance,
} from "./design-system-migration.mjs";

const route = (overrides = {}) => ({
  id: "ADM-001",
  application: "admin",
  route: "/prompts",
  pageSource: "apps/admin/app/prompts/page.tsx",
  domainStories: ["US-014"],
  stateProfiles: ["READ", "EDIT", "ACCESS"],
  owner: { task: "task_02", accountable: "Admin" },
  evidence: { automated: ["IT-005"], manual: ["E2E-043"] },
  ...overrides,
});

const accessibilityInventory = {
  routes: [
    { id: "ADM-001", route: "/prompts", stateProfiles: ["READ", "EDIT"] },
  ],
  embeddedSurfaces: [],
};

test("UT-079 accepts a route with an existing source, known story and explicit pending evidence", () => {
  const root = mkdtempSync(join(tmpdir(), "inspection-design-system-"));
  writeFileSync(join(root, "page.tsx"), "export default function Page() {}\n");
  const inventory = { schemaVersion: 1, routes: [route({ pageSource: join(root, "page.tsx") })], embeddedSurfaces: [], patterns: ["Dialog"], exceptions: [] };
  const errors = validateMigrationInventory(inventory, [{ application: "admin", route: "/prompts" }], ["US-014"]);
  if (errors.length) throw new Error(errors.join("; "));
});

test("UT-080 rejects an omitted /prompts route and an unknown US-999 reference", () => {
  const inventory = { schemaVersion: 1, routes: [route({ route: "/overview", domainStories: ["US-999"] })], embeddedSurfaces: [], patterns: ["Dialog"], exceptions: [] };
  const errors = validateMigrationInventory(inventory, [{ application: "admin", route: "/prompts" }], ["US-014"]);
  if (!errors.some((error) => error.includes("discovered route missing"))) throw new Error("omitted route was accepted");
  if (!errors.some((error) => error.includes("unknown story US-999"))) throw new Error("unknown story was accepted");
});

test("UT-081 rejects pass evidence when the required result was skipped", () => {
  const errors = validateAccessibilityEvidence({ records: [{ surfaceId: "ADM-001", route: "/prompts", state: "READ", criterion: "1.1.1", result: "pass", requiredResult: "skipped" }] }, CRITERION_IDS);
  if (!errors.some((error) => error.includes("unresolved"))) throw new Error("skipped evidence was accepted as pass");
});

test("UT-084 rejects unresolved manual accessibility evidence", () => {
  const errors = validateAccessibilityEvidence({ records: [{ surfaceId: "ADM-001", route: "/prompts", state: "READ", criterion: "1.1.1", result: "not-tested" }] }, CRITERION_IDS);
  if (!errors.some((error) => error.includes("accessibility evidence is unresolved"))) throw new Error("not-tested evidence was accepted");
});

test("UT-085 rejects evidence that omits a configured criterion", () => {
  const records = CRITERION_IDS.slice(0, -1).map((criterion) => ({ surfaceId: "ADM-001", route: "/prompts", state: "READ", criterion, result: "not-tested" }));
  const errors = validateAccessibilityEvidence({ records }, CRITERION_IDS);
  if (!errors.some((error) => error.includes(`missing accessibility criterion ${CRITERION_IDS.at(-1)}`))) throw new Error("missing criterion was accepted");
});

test("UT-087 rejects aggregate-only accessibility evidence without inventory targets", () => {
  const records = CRITERION_IDS.map((criterion) => ({
    surfaceId: "ALL-SURFACES",
    route: "inventory routes",
    state: "manual execution gate",
    criterion,
    result: "pass",
    persona: "User of assistive technology",
    executionContext: "manual browser execution",
  }));
  const errors = validateAccessibilityEvidence({ records }, CRITERION_IDS, accessibilityInventory);
  if (!errors.some((error) => error.includes("target is not in inventory"))) throw new Error("aggregate target was accepted");
  if (!errors.some((error) => error.includes("missing accessibility evidence row: ADM-001:/prompts:READ:1.1.1"))) throw new Error("route/state evidence was not required");
});

test("UT-088 rejects accessibility evidence without persona and execution context", () => {
  const errors = validateAccessibilityEvidence({ records: [{ surfaceId: "ADM-001", route: "/prompts", state: "READ", criterion: "1.1.1", result: "not-tested" }] }, CRITERION_IDS);
  if (!errors.some((error) => error.includes("missing persona"))) throw new Error("missing persona was accepted");
  if (!errors.some((error) => error.includes("missing executionContext"))) throw new Error("missing execution context was accepted");
});

test("UT-086 resolves inventory page sources against the supplied repository", () => {
  const root = mkdtempSync(join(tmpdir(), "inspection-design-system-repository-"));
  writeFileSync(join(root, "page.tsx"), "export default function Page() {}\n");
  const inventory = { schemaVersion: 1, routes: [route({ pageSource: "page.tsx" })], embeddedSurfaces: [], patterns: ["Dialog"], exceptions: [] };
  const errors = validateMigrationInventory(inventory, [{ application: "admin", route: "/prompts" }], ["US-014"], root);
  if (errors.length) throw new Error(errors.join("; "));
});

test("UT-082 requires a route and state rationale for not-applicable criteria", () => {
  const errors = validateAccessibilityEvidence({ records: [{ surfaceId: "ADM-001", route: "/prompts", state: "READ", criterion: "1.2.1", result: "not-applicable" }] }, CRITERION_IDS);
  if (!errors.some((error) => error.includes("not-applicable requires"))) throw new Error("unreasoned not-applicable evidence was accepted");
});

test("UT-083 rejects an undocumented second reusable modal and app-owned React Aria import", () => {
  const files = [
    { path: "apps/admin/src/modal-a.tsx", content: 'import { Dialog } from "react-aria-components"; export function ModalA() { return <Dialog />; }' },
    { path: "apps/dashboard/src/modal-b.tsx", content: 'export function ModalB() { return <div role="dialog" />; }' },
  ];
  const errors = auditPresentationSources(files, []);
  if (!errors.some((error) => error.includes("react-aria-components"))) throw new Error("app-owned React Aria import was accepted");
  if (!errors.some((error) => error.includes("undocumented reusable modal"))) throw new Error("second modal implementation was accepted");
});

const guidance = readFileSync(new URL("../../docs/design-system/usage.md", import.meta.url), "utf8");
const inventory = JSON.parse(readFileSync(new URL("../../docs/design-system/migration-inventory.json", import.meta.url), "utf8"));

test("IT-182 rejects migration guidance when the Combobox pattern is absent", () => {
  const errors = validateUsageGuidance(guidance.replace(/Combobox/gi, "SearchField"), inventory);
  if (!errors.includes("usage guidance omits Combobox")) throw new Error("missing Combobox guidance was accepted");
});

test("IT-183 rejects a desktop-only dialog example without long-content constraints", () => {
  const errors = validateUsageGuidance(guidance.replace(/320|200%|400%|scrolls inside the dialog|keyboard/g, "desktop"), inventory);
  if (!errors.some((error) => error.includes("responsive requirement"))) throw new Error("desktop-only guidance was accepted");
});

test("IT-184 rejects token-shaped synthetic fixtures", () => {
  const errors = validateUsageGuidance(`${guidance}\ninvitationToken: "eyJ${"a".repeat(24)}"`, inventory);
  if (!errors.includes("usage guidance contains a token-shaped synthetic fixture")) throw new Error("token-shaped fixture was accepted");
});

test("IT-185 rejects contradictory primary-action guidance", () => {
  const errors = validateUsageGuidance(`${guidance}\nAdmin and Capture use a different meaning for the primary button.`, inventory);
  if (!errors.includes("primary action meaning is contradictory")) throw new Error("contradictory action guidance was accepted");
});

test("IT-186 requires every documented exception to have a retirement condition", () => {
  const errors = validateUsageGuidance(guidance, { ...inventory, exceptions: [{ path: "legacy-modal.tsx", reason: "temporary" }] });
  if (!errors.some((error) => error.includes("no removal condition"))) throw new Error("unbounded exception was accepted");
});

test("IT-196 keeps boundary failures safe while retaining correlation identity", () => {
  const errors = validateBoundaryErrorFixture({ message: "Não foi possível concluir a solicitação.", safeMessage: "Não foi possível concluir a solicitação.", correlationId: "corr-fixture" });
  if (errors.length) throw new Error(errors.join("; "));
  if (!validateBoundaryErrorFixture({ message: "Error: boom at handler (internal.ts:1)", correlationId: "corr-fixture" }).some((error) => error.includes("internal trace"))) throw new Error("internal trace was accepted");
});

test("IT-197 drops protected payloads from the previous membership", () => {
  const errors = validateMembershipTransition({ membershipId: "internal-a" }, { membershipId: "customer-a" }, [{ kind: "reports", membershipId: "customer-a" }]);
  if (errors.length) throw new Error(errors.join("; "));
  if (!validateMembershipTransition({ membershipId: "internal-a" }, { membershipId: "customer-a" }, [{ kind: "notifications", membershipId: "internal-a" }]).some((error) => error.includes("stale protected payload"))) throw new Error("stale protected payload was accepted");
});

test("IT-198 audits all four app shells for retired control imports and duplicate modals", () => {
  const files = ["admin", "dashboard", "capture", "onboarding"].map((application) => ({ path: `apps/${application}/app/layout.tsx`, content: readFileSync(new URL(`../../apps/${application}/app/layout.tsx`, import.meta.url), "utf8") }));
  const errors = validatePackageShells(files);
  if (errors.length) throw new Error(errors.join("; "));
  const bad = validatePackageShells([{ path: "apps/admin/app/legacy-modal.tsx", content: 'import { Dialog } from "react-aria-components"; export function LegacyModal() { return <Dialog />; }' }, ...files]);
  if (!bad.some((error) => error.includes("react-aria-components"))) throw new Error("retired control import was accepted");
});

test("E2E-037 traces the actual inventory, guidance and retirement audit together", () => {
  const inventoryErrors = validateMigrationInventory(inventory, [], [], process.cwd());
  if (inventoryErrors.length) throw new Error(inventoryErrors.join("; "));
  const guidanceErrors = validateUsageGuidance(guidance, inventory);
  if (guidanceErrors.length) throw new Error(guidanceErrors.join("; "));
});

test("E2E-048 fails the CI entry with actionable missing real-E2E prerequisites", () => {
  const tool = fileURLToPath(new URL("./design-system-migration.mjs", import.meta.url));
  let output = "";
  try {
    execFileSync(process.execPath, [tool, "prerequisites", "--repository", process.cwd()], { env: { PATH: process.env.PATH ?? "" }, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  } catch (error) {
    output = `${error.stdout?.toString?.() ?? error.stdout ?? ""}${error.stderr?.toString?.() ?? error.stderr ?? ""}`;
  }
  if (!output.includes("missing real-E2E prerequisite")) throw new Error("missing prerequisites did not fail with an actionable reason");
});
