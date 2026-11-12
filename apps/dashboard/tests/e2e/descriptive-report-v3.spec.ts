import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, test, type Page } from "@playwright/test";

const customerChecklistGolden = JSON.parse(readFileSync(resolve(__dirname, "../../../../services/inspection/internal/features/reports/customer/core/testdata/customer_report_v3.golden.json"), "utf8")) as { coverage: unknown; rooms: Array<unknown> };
const internalChecklistGolden = JSON.parse(readFileSync(resolve(__dirname, "../../../../services/inspection/internal/platform/graphql/resolvers/testdata/internal_report_v3.golden.json"), "utf8")) as { coverage: unknown; rooms: Array<unknown> };
const comparisonGolden = JSON.parse(readFileSync(resolve(__dirname, "../../../../services/inspection/internal/features/reports/customer/core/testdata/task07_comparison_v3.golden.json"), "utf8")) as { counts: number; outcomes: string[]; pair: string; disclaimer: string };
const backendEvidenceReference = ((internalChecklistGolden.rooms[0] as { components: Array<{ attributeSources: string[] }> }).components[0].attributeSources[0] ?? "").match(/E\d{3}/)?.[0];

function customerChecklistWithPagedEvidence() {
  const checklist = JSON.parse(JSON.stringify(customerChecklistGolden)) as { coverage: unknown; rooms: Array<{ name: string; components: Array<{ evidenceIds: string[] }> }> };
  checklist.rooms[0].components[0].evidenceIds = ["E001", "E026", "E027"];
  return checklist;
}

type Scenario = "failed" | "catalog" | "history" | "baseline" | "customer";

function checklist(status = "OBSERVED", reviewStatus = "NOT_REVIEWED") {
  return {
    coverage: { expected: 1, fullyAssessed: status === "OBSERVED" ? 1 : 0, partiallyAssessed: 0, notObserved: status === "NOT_OBSERVED" ? 1 : 0, notApplicable: 0, impossible: 0, unknownScope: false },
    rooms: [{ key: "kitchen", typeCode: "KITCHEN", name: "Cozinha", components: [{ key: "cabinet", typeCode: "CABINET", label: "Armários", status, description: "Armários íntegros nas vistas disponíveis.", condition: "GOOD", reviewStatus, reviewLabel: reviewStatus === "NOT_REVIEWED" || reviewStatus === "PENDING" ? "Não revisado" : "", assessedScope: "Frentes e laterais", notAssessed: [], evidenceIds: ["evidence-cabinet"], limitations: ["A parte superior não foi observada."], conflict: false, outsideScope: false }] }],
  };
}

function report(version: number, scenario: Scenario) {
  const failed = scenario === "failed";
  const comparison = scenario === "customer" ? {
    baselineVersionNumber: 4, purpose: "MOVE_IN", purposeLabel: "Entrada no imóvel", activatedAt: "2026-10-01T12:00:00Z", status: "COMPLETED", statusLabel: "Comparação concluída",
    counts: { total: 6, unchanged: 1, changed: 1, improved: 1, missing: 1, notObserved: 1, noBasis: 1 }, displayed: 6,
    disclaimer: comparisonGolden.disclaimer,
    items: [
      { room: "Sala", name: "Sofá", outcome: "CHANGED", outcomeLabel: "Alterado", baselineCondition: "Bom", currentCondition: "Regular", limitations: [], pairs: [{ baselineEvidence: "E001", currentEvidence: "E002", baselineAvailability: "AVAILABLE", currentAvailability: "AVAILABLE" }] },
      { room: "Cozinha", name: "Armário", outcome: "NO_BASIS", outcomeLabel: "Sem base para comparar", baselineCondition: null, currentCondition: null, limitations: ["Não há vista avaliável na referência."], pairs: [] },
      { room: "Quarto", name: "Cama", outcome: "NOT_OBSERVED", outcomeLabel: "Não observado", baselineCondition: "Bom", currentCondition: null, limitations: ["Evidência da referência indisponível."], pairs: [{ baselineEvidence: "E003", currentEvidence: "E004", baselineAvailability: "UNAVAILABLE", currentAvailability: "UNAVAILABLE" }] },
      { room: "Sala", name: "Mesa", outcome: "UNCHANGED", outcomeLabel: "Sem alteração", baselineCondition: "Bom", currentCondition: "Bom", limitations: [], pairs: [] },
      { room: "Quarto", name: "Cômoda", outcome: "IMPROVED", outcomeLabel: "Melhorou", baselineCondition: "Regular", currentCondition: "Bom", limitations: [], pairs: [] },
      { room: "Cozinha", name: "Cadeira", outcome: "MISSING", outcomeLabel: "Ausência comprovada", baselineCondition: "Bom", currentCondition: null, limitations: [], pairs: [] },
    ], outsideBaseline: [],
  } : null;
  const comparisonInternal = scenario === "history" ? {
    baseline: { versionId: "baseline-approved", versionNumber: 4, purpose: "MOVE_IN", purposeLabel: "Entrada no imóvel", activatedAt: "2026-10-01T12:00:00Z" }, status: "COMPLETED", statusLabel: "Comparação concluída",
    counts: { total: 1, unchanged: 0, changed: 1, improved: 0, missing: 0, notObserved: 0, noBasis: 0 },
    items: [{ itemIdentityId: "item-sofa", room: "Sala", typeCode: "SOFA", name: "Sofá", outcome: "CHANGED", outcomeLabel: "Alterado", baselineCondition: "GOOD", currentCondition: "FAIR", description: "Mudança visível nas vistas pareadas.", limitations: [], pairs: [{ baselineEvidenceId: "origin-evidence", currentEvidenceId: "current-evidence" }], pairingRevised: false }], outsideBaseline: [],
  } : null;
  return {
    id: `snapshot-${version}`, inspectionId: scenario === "failed" ? "inspection-failed" : scenario === "catalog" ? "inspection-catalog" : "inspection-history", projectId: null, version,
    classification: failed ? "NORMAL" : "ATTENTION", classificationLabel: failed ? "Resultado com limitações" : "Requer atenção", limited: failed, advisory: failed ? "A análise não foi concluída." : "Laudo descritivo com limitações identificadas.", createdAt: `2026-10-0${version}T12:00:00Z`, pdfStatus: "READY", publication: { id: `publication-${version}`, status: "PUBLISHED", version, publishedAt: `2026-10-0${version}T12:00:00Z`, invalidatedAt: null },
    context: { asset: { id: "asset-a", name: "Apartamento QA", externalKey: "QA-CLD", address: "Rua de QA" }, participant: { id: "participant-a", name: "Participante QA" }, template: { id: "template-v3", name: "Modelo descritivo", version: 3 }, inspection: { stageLabel: "Saída" } },
    inventory: null, checklist: failed ? checklist("NOT_OBSERVED", "NOT_REVIEWED") : internalChecklistGolden, processing: { status: failed ? "FAILED" : "COMPLETED", message: failed ? "A análise não pôde ser concluída." : "A descrição aguarda revisão da equipe." },
    handover: { entries: [{ key: "keys", kind: "KEY", status: "PENDING", payload: { presentation: "Chaves · Pendente" }, evidenceIds: [], declarant: null }] }, comparison: scenario === "customer" ? comparison : comparisonInternal,
    requirements: [], evidence: { nodes: [{ id: "current-photo", requirementKey: "room:kitchen", role: "CURRENT", description: "Armários · vista geral", capturedAt: "2026-10-01T11:30:00Z", availability: "AVAILABLE", sourceAvailability: "AVAILABLE", evidenceKind: "PHOTO", timestampMs: null, sourceMediaId: null, component: "Armários", label: "Armários", reference: backendEvidenceReference, flags: [], url: "/fixtures/cabinet.jpg" }, { id: "origin-evidence", requirementKey: "inventory:item", role: "REFERENCE", description: "Armário na origem", capturedAt: null, availability: "AVAILABLE", sourceAvailability: "AVAILABLE", evidenceKind: "PHOTO", timestampMs: null, sourceMediaId: null, component: "Armário", label: "Armário", reference: "E001", flags: [], url: "/fixtures/origin.jpg" }, { id: "current-evidence", requirementKey: "inventory:item", role: "CURRENT", description: "Armário atual", capturedAt: null, availability: "AVAILABLE", sourceAvailability: "AVAILABLE", evidenceKind: "PHOTO", timestampMs: null, sourceMediaId: null, component: "Armário", label: "Armário", reference: "E002", flags: [], url: "/fixtures/current.jpg" }], pageInfo: { endCursor: null, hasNextPage: false } },
    findings: [],
  };
}

async function installJourney(page: Page, scenario: Scenario, customer = false, holdHistoryPoll = false) {
  let currentVersion = scenario === "history" ? 1 : 2;
  let reissueRequested = false;
  let historyReadsAfterRequest = 0;
  let customerEvidenceLoads = 0;
  let reportWorkspaceReads = 0;
  let markHistoryPollStarted!: () => void;
  let releaseHistoryPoll!: () => void;
  const historyPollStarted = new Promise<void>((resolve) => { markHistoryPollStarted = resolve; });
  const historyPollGate = new Promise<void>((resolve) => { releaseHistoryPoll = resolve; });
  const reportsForInspection = () => [1, 2].filter((version) => version <= currentVersion).map((version) => ({ id: `snapshot-${version}`, inspectionId: scenario === "failed" ? "inspection-failed" : scenario === "catalog" ? "inspection-catalog" : "inspection-history", assetName: "Apartamento QA", assetAddress: "Rua de QA", assetExternalKey: "QA-CLD", participantName: "Participante QA", generatedAt: `2026-10-0${version}T12:00:00Z`, classification: version === 1 ? "NORMAL" : "ATTENTION", version, scopeLabel: "1 componente no escopo", reviewLabel: "Não revisado", publication: { id: `publication-${version}`, status: version === currentVersion ? "PUBLISHED" : "SUPERSEDED", version, publishedAt: `2026-10-0${version}T12:00:00Z`, invalidatedAt: null } }));
  await page.route("**/protocol/openid-connect/token", (route) => route.fulfill({ status: 200, contentType: "application/json", headers: { "access-control-allow-origin": "*" }, body: JSON.stringify({ access_token: "descriptive-report-v3-token" }) }));
  await page.route("**/fixtures/*.jpg", (route) => route.fulfill({ status: 200, contentType: "image/svg+xml", body: '<svg xmlns="http://www.w3.org/2000/svg" width="2" height="2"><rect width="2" height="2" fill="#345"/></svg>' }));
  await page.addInitScript((returnTo) => sessionStorage.setItem("inspection.dashboard.pkce", JSON.stringify({ verifier: "descriptive-report-v3-verifier", state: "descriptive-report-v3-state", returnTo })), customer ? "/reports?inspectionId=inspection-history" : "/reports");
  await page.route("**/graphql", async (route) => {
    const request = route.request().postDataJSON() as { query: string; variables?: Record<string, unknown> };
    const operation = request.query.match(/(?:query|mutation)\s+(\w+)/)?.[1] ?? "unknown";
    const membership = { id: "membership-a", tenantId: "tenant-a", name: "Gestora QA", email: "qa@example.test", role: customer ? "CUSTOMER_VIEWER" : "MANAGER", status: "ACTIVE", scopes: [] };
    let data: Record<string, unknown> = {};
    if (["DashboardGate", "DashboardMemberships"].includes(operation)) data = { me: { identityId: "identity-a", tenantId: "tenant-a", audience: customer ? "customer" : "internal", product: "DASHBOARD", productEntitlements: ["DASHBOARD"], roles: [membership.role], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-a" }], memberships: [membership] }, tenant: { id: "tenant-a", name: "Operação QA", status: "ACTIVE" } };
    if (operation === "MyNotifications") data = { myNotifications: { nodes: [], unreadCount: 0, pageInfo: { endCursor: null, hasNextPage: false } } };
    if (operation === "Reports") {
      const historyRequest = request.variables?.inspectionId === "inspection-history";
      if (historyRequest && reissueRequested && holdHistoryPoll) {
        markHistoryPollStarted();
        await historyPollGate;
      }
      if (historyRequest && reissueRequested && ++historyReadsAfterRequest >= 2) currentVersion = 2;
      const rows = reportsForInspection();
      data = { reports: { nodes: historyRequest ? rows : rows.filter((item) => item.version === currentVersion), pageInfo: { endCursor: null, hasNextPage: false } } };
    }
    if (operation === "ReportWorkspace") {
      reportWorkspaceReads++;
      const selectedVersion = Number(request.variables?.version) || currentVersion;
      data = { report: report(selectedVersion, scenario) };
    }
    if (operation === "ReissueReport") {
      data = { reissueReport: { report: { id: "snapshot-1", inspectionId: "inspection-history", version: currentVersion, classification: "ATTENTION", limited: false, advisory: "Laudo atualizado.", pdfStatus: "READY" }, userErrors: [], clientMutationId: "reissue" } };
      reissueRequested = true;
    }
    if (operation === "ReviseComparisonPairing") data = { reviseComparisonPairing: { report: { id: "snapshot-1", inspectionId: "inspection-history", version: 1, classification: "ATTENTION", limited: false, advisory: "Pareamento revisado.", pdfStatus: "READY" }, userErrors: [], clientMutationId: "pairing" } };
    if (operation === "CustomerPortfolio") data = { customerPortfolio: { nodes: [{ assetId: "asset-a", assetName: "Apartamento QA", assetExternalKey: "QA-CLD", latestPublishedInspectionId: "inspection-history", projectId: null, publishedClassification: "ATTENTION", status: "ACTIVE", progress: 100, updatedAt: "2026-10-02T12:00:00Z" }], pageInfo: { endCursor: null, hasNextPage: false } } };
    if (operation === "CustomerReport") {
      const internal = report(2, "customer");
      data = { customerReport: { inspectionId: "inspection-history", snapshotId: "snapshot-2", version: 2, classification: "ATTENTION", classificationLabel: "Requer atenção", limited: false, advisory: "Laudo descritivo.", status: "PUBLISHED", historical: false, context: internal.context, requirements: [], inventory: null, evidenceNotice: "", scopeStatements: [], reasonLabels: [], checklist: customerChecklistWithPagedEvidence(), processing: { status: "COMPLETED", message: "" }, handover: { entries: [{ label: "Chaves", kind: "KEY", status: "Pendente", type: null, value: null, unit: null, date: null, declarant: null, evidenceIds: [] }] }, comparison: internal.comparison } };
    }
    if (operation === "CustomerEvidence") {
      const after = request.variables?.after;
      if (after === null && ++customerEvidenceLoads > 1) {
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ errors: [{ message: "FORBIDDEN", extensions: { code: "FORBIDDEN" } }] }) });
        return;
      }
      const item = (id: string, reference: string, availability: string, url: string | null) => ({ id, requirementKey: "room:kitchen", section: "Cozinha", label: "Armários", reference, role: "CURRENT", description: "Vista dos armários", captureSource: "PHOTO", capturedAt: "2026-10-01T11:30:00Z", state: availability, lineageId: id, replacedBy: null, mediaAvailability: availability, sourceAvailability: availability, evidenceKind: "PHOTO", frameId: null, timestampMs: null, ptsMicros: null, preparationId: null, planId: null, sourceMediaId: null, component: "Armários", flags: [], url });
      if (after === "cursor-1") {
        data = { customerEvidence: { nodes: [item("evidence-26", "E026", "AVAILABLE", "/fixtures/evidence-26.jpg"), item("withheld-evidence", "E027", "WITHHELD", "/fixtures/withheld-evidence.jpg")], pageInfo: { endCursor: null, hasNextPage: false } } };
      } else {
        const firstPage = Array.from({ length: 25 }, (_, index) => item(`evidence-${index + 1}`, index === 0 ? "E001" : `P${String(index + 1).padStart(3, "0")}`, "AVAILABLE", index === 0 ? "/fixtures/cabinet.jpg" : null));
        data = { customerEvidence: { nodes: firstPage, pageInfo: { endCursor: "cursor-1", hasNextPage: true } } };
      }
    }
    if (operation === "ReportDownload") data = { reportDownload: { snapshotId: "snapshot-2", kind: "PDF", objectKey: "qa-report.pdf", url: "/fixtures/qa-report.pdf", status: "READY", sha256: "digest" } };
    if (operation === "VerifyReport") data = { verifyReport: Number(request.variables?.version) === 1 ? { reportNumber: "CLD-QA-001", version: 1, digest: "public-digest", status: "SUPERSEDED", issuedAt: "2026-10-01T12:00:00Z" } : { reportNumber: "CLD-QA-001", version: 2, digest: "public-digest-2", status: "PUBLISHED", issuedAt: "2026-10-02T12:00:00Z" } };
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data }) });
  });
  await page.goto("/auth/callback?code=report-code&state=descriptive-report-v3-state");
  await expect(page.getByRole("navigation", { name: "Painel" })).toBeVisible();
  return { historyPollStarted, releaseHistoryPoll: () => releaseHistoryPoll(), reportWorkspaceReads: () => reportWorkspaceReads };
}

test("E2E-003 opens the automatic report after terminal analysis failure without claiming no alerts", async ({ page }) => {
  await installJourney(page, "failed");
  await page.getByRole("row").filter({ hasText: "v2" }).getByRole("button", { name: "Abrir detalhes do laudo de Apartamento QA" }).click();
  const reportDialog = page.getByRole("dialog", { name: "Laudo de vistoria" });
  await expect(reportDialog.getByText("Falha na análise", { exact: true })).toBeVisible();
  await expect(reportDialog.getByText("Resultado com limitações", { exact: true })).toBeVisible();
  await expect(reportDialog.getByText("Sem alertas no inventário", { exact: true })).toHaveCount(0);
  await expect(reportDialog.getByText("Sem alertas identificados", { exact: true })).toHaveCount(0);
});

test("E2E-004 keeps the sound descriptive catalog, evidence number, limitation and AI review state visible", async ({ page }) => {
  await installJourney(page, "catalog");
  await page.getByRole("row").filter({ hasText: "v2" }).getByRole("button", { name: "Abrir detalhes do laudo de Apartamento QA" }).click();
  const reportDialog = page.getByRole("dialog", { name: "Laudo de vistoria" });
  await expect(reportDialog.getByText("Armários íntegros nas vistas disponíveis.")).toBeVisible();
  await expect(reportDialog.getByText("E001", { exact: true })).toBeVisible();
  await expect(reportDialog.locator(".report-checklist-evidence figcaption")).toHaveText("E001");
  await expect(reportDialog.locator(".report-descriptive-checklist").getByText("Não revisado", { exact: true })).toBeVisible();
  await expect(reportDialog.getByText("Limitação: A parte superior não foi observada.")).toBeVisible();
  await expect(reportDialog.getByRole("region", { name: "Identificação e síntese do laudo" }).getByText(/avaliação completa: 1 de 1/)).toBeVisible();
  await expect(reportDialog.getByText(/Perfil de bens: Não informado/)).toBeVisible();
  await expect(reportDialog.getByText("Chaves · Pendente", { exact: true })).toBeVisible();
});

test("E2E-005 customer report paginates checklist media and never requests withheld evidence", async ({ page }) => {
  const privateMediaRequests: string[] = [];
  page.on("request", request => {
    const url = request.url();
    if (request.resourceType() === "image" || (url.includes("/media/") && !url.includes("/_next/static/media/"))) privateMediaRequests.push(url);
  });
  await installJourney(page, "customer", true);
  await page.getByRole("button", { name: "Abrir resultado publicado" }).click();
  const report = page.getByRole("article").filter({ has: page.getByRole("heading", { name: "Apartamento QA" }) });
  await expect(report.getByText("Referência · versão 4 · Entrada no imóvel")).toBeVisible();
  await expect(report.getByText("Perfil de bens: Não informado", { exact: true })).toBeVisible();
  await expect(report.getByText("Sem base para comparar", { exact: true })).toBeVisible();
  const unavailablePair = report.getByRole("heading", { name: "Cama" }).locator("xpath=ancestor::article");
  await expect(unavailablePair.getByText("Evidência da referência indisponível.", { exact: true })).toHaveCount(1);
  await expect(unavailablePair.getByText("Evidência atual indisponível.", { exact: true })).toHaveCount(2);
  await expect(report.getByText("E001 ↔ E002", { exact: true })).toBeVisible();
  await expect(report.locator(".report-descriptive-checklist")).toContainText("Armários");
  await expect(report.locator(".report-descriptive-checklist")).not.toContainText("Ambientes previstos");
  await expect(report.locator(".report-checklist-evidence figcaption")).toHaveText(["E001", "E026", "E027"]);
  await report.locator(".report-checklist-evidence").scrollIntoViewIfNeeded();
  await expect(report.locator(".report-checklist-evidence img")).toHaveCount(2);
  await expect.poll(() => privateMediaRequests.filter((url) => url.includes("/fixtures/cabinet.jpg")).length).toBe(1);
  await expect.poll(() => privateMediaRequests.filter((url) => url.includes("/fixtures/evidence-26.jpg")).length).toBe(1);
  expect(privateMediaRequests.some((url) => url.includes("withheld-evidence"))).toBe(false);
  expect(privateMediaRequests).toHaveLength(2);
  expect(privateMediaRequests.filter((url) => !["/fixtures/cabinet.jpg", "/fixtures/evidence-26.jpg"].some((path) => url.includes(path)))).toEqual([]);
  await expect(report.getByText(comparisonGolden.disclaimer, { exact: true })).toBeVisible();
  await expect(report.getByText(/confidence|tenant|UUID/i)).toHaveCount(0);
  const imageRequestsBeforeRevocation = privateMediaRequests.length;
  await page.getByRole("button", { name: "Abrir resultado publicado" }).click();
  await expect(page.getByText("Este resultado não está disponível no momento.")).toBeVisible();
  expect(privateMediaRequests).toHaveLength(imageRequestsBeforeRevocation);
});

test("E2E-006 correction issues version 2 and leaves version 1 available in report history", async ({ page }) => {
  const journey = await installJourney(page, "history", false, true);
  await page.getByRole("row").filter({ hasText: "v1" }).getByRole("button", { name: "Abrir detalhes do laudo de Apartamento QA" }).click();
  const reportDialog = page.getByRole("dialog", { name: "Laudo de vistoria" });
  await expect(reportDialog.getByRole("region", { name: "Histórico de versões" })).toBeVisible();
  await expect(reportDialog.getByRole("button", { name: /Versão 1/ })).toBeVisible();
  await expect(reportDialog.getByRole("button", { name: /Versão 1/ }).getByText("01/10/2026", { exact: false })).toBeVisible();
  await expect(reportDialog.getByText("1 componente no escopo")).toBeVisible();
  await reportDialog.getByRole("button", { name: "Emitir nova versão" }).click();
  await expect(reportDialog.getByText("Nova versão solicitada. Ela ficará disponível após o processamento do laudo.")).toBeVisible();
  await journey.historyPollStarted;
  await expect(reportDialog.getByRole("button", { name: "Emitir nova versão" })).toBeDisabled();
  await reportDialog.getByRole("button", { name: /Versão 1/ }).click();
  journey.releaseHistoryPoll();
  await expect(reportDialog.getByRole("button", { name: /Versão 2/ })).toBeVisible();
  await expect(reportDialog.getByRole("button", { name: /Versão 1/ })).toBeVisible();
  await expect(reportDialog.getByRole("button", { name: /Versão 1/ })).toHaveAttribute("aria-current", "page");
  await expect(page).toHaveURL(/version=1/);
  await expect(reportDialog.getByRole("button", { name: "Emitir nova versão" })).toHaveCount(0);
  await expect(reportDialog.getByText("Pareamento revisado pela equipe.")).toHaveCount(0);
  await page.goto("/verify-report?code=opaque-code&version=1");
  await expect(page.getByRole("heading", { name: "Versão substituída" })).toBeVisible();
  await expect(page.getByText("opaque-code")).toHaveCount(0);
  await expect(page.getByText("public-digest", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Copiar resumo" })).toBeVisible();
});

test("report history polling stops updating details after the journey unmounts", async ({ page }) => {
  const journey = await installJourney(page, "history", false, true);
  await page.getByRole("row").filter({ hasText: "v1" }).getByRole("button", { name: "Abrir detalhes do laudo de Apartamento QA" }).click();
  const reportDialog = page.getByRole("dialog", { name: "Laudo de vistoria" });
  await expect(reportDialog.getByRole("button", { name: "Emitir nova versão" })).toBeVisible();
  await reportDialog.getByRole("button", { name: "Emitir nova versão" }).click();
  await journey.historyPollStarted;
  await page.evaluate(() => window.history.pushState({}, "", "/inspections"));
  await expect(page).toHaveURL(/\/inspections/);
  journey.releaseHistoryPoll();
  await page.waitForTimeout(100);
  expect(journey.reportWorkspaceReads()).toBe(1);
});

async function installBaselineCreation(page: Page, approved: boolean) {
  await page.route("**/protocol/openid-connect/token", (route) => route.fulfill({ status: 200, contentType: "application/json", headers: { "access-control-allow-origin": "*" }, body: JSON.stringify({ access_token: "baseline-gate-token" }) }));
  await page.addInitScript(() => sessionStorage.setItem("inspection.dashboard.pkce", JSON.stringify({ verifier: "baseline-gate-verifier", state: "baseline-gate-state", returnTo: "/inspections?create=1" })));
  await page.route("**/graphql", async (route) => {
    const request = route.request().postDataJSON() as { query: string };
    const operation = request.query.match(/(?:query|mutation)\s+(\w+)/)?.[1] ?? "unknown";
    let data: Record<string, unknown> = {};
    const pageInfo = { endCursor: null, hasNextPage: false };
    if (["DashboardGate", "DashboardMemberships"].includes(operation)) data = { me: { identityId: "identity-a", tenantId: "tenant-a", productEntitlements: ["DASHBOARD"], roles: ["MANAGER"], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-a" }], memberships: [{ id: "membership-a", tenantId: "tenant-a", name: "Gestora QA", email: "qa@example.test", role: "MANAGER", status: "ACTIVE" }] }, tenant: { id: "tenant-a", name: "Operação QA", status: "ACTIVE" } };
    if (operation === "MyNotifications") data = { myNotifications: { nodes: [], unreadCount: 0, pageInfo } };
    if (operation === "Inspections") data = { inspections: { nodes: [], pageInfo } };
    if (operation === "DashboardReviewers") data = { memberships: { nodes: [], pageInfo } };
    if (operation === "DashboardFormOptions") data = { assets: { nodes: [{ id: "asset-a", name: "Apartamento QA", externalKey: "QA", businessUnitId: "unit-a", segmentVersionId: "segment-a", templateId: "template-a", status: "ACTIVE", version: 1 }], pageInfo }, participants: { nodes: [{ id: "participant-a", businessUnitId: "unit-a", name: "Pessoa QA", status: "ACTIVE" }], pageInfo }, templates: { nodes: [{ id: "template-a", key: "qa-v3", name: "Modelo de saída", segmentVersionId: "segment-a", activeVersionId: "template-version-a", version: 3 }], pageInfo } };
    if (operation === "DashboardTemplateVersion") data = { templateVersion: { id: "template-version-a", mediaKind: "PHOTO", isVideo: false } };
    if (operation === "DashboardOriginVersions") {
      const eligible = { id: "approved-baseline", originId: "origin-a", versionNumber: 4, status: "SUPERSEDED", mediaKind: "PHOTO", preparationStatus: null, captureSubmitted: true, selectable: false, source: "CAPTURE", sourceInspectionId: null, draftState: null, sourceAvailability: "RETAINED", scopeManifestDigest: "digest", supersedesId: null, activatedAt: "2026-10-01T00:00:00Z", purpose: "MOVE_IN", hasInventory: true, limitedCoverage: false };
      const mediaOnly = { id: "legacy-media-origin", originId: "origin-a", versionNumber: 1, status: "ACTIVE", mediaKind: "PHOTO", preparationStatus: "READY", captureSubmitted: true, selectable: true, source: "LEGACY_ORIGIN", sourceInspectionId: null, draftState: null, sourceAvailability: "AVAILABLE", scopeManifestDigest: "digest", supersedesId: null, activatedAt: "2026-10-01T00:00:00Z", purpose: "MOVE_IN", hasInventory: false, limitedCoverage: false };
      data = { originVersions: { nodes: approved ? [eligible] : [mediaOnly], pageInfo } };
    }
    if (operation === "DashboardSummary") data = { dashboardSummary: { total: 0, normal: 0, attention: 0, critical: 0, pending: 0, invalidated: 0 } };
    if (operation === "DashboardBaselineVersions") data = { baselineVersions: { nodes: [], pageInfo } };
    if (operation === "PlanInspection") data = { planInspection: { inspection: { id: "inspection-moveout", status: "PLANNED", version: 1, projectId: null, stageId: null }, schedule: null, project: null, userErrors: [], clientMutationId: "plan" } };
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data }) });
  });
  await page.goto("/auth/callback?code=baseline-code&state=baseline-gate-state");
  await expect(page.getByRole("navigation", { name: "Painel" })).toBeVisible();
  const dialog = page.getByRole("dialog", { name: "Nova vistoria" });
  await expect(dialog).toBeVisible();
  const choose = async (label: string, optionName: RegExp) => { const combo = dialog.getByRole("combobox", { name: label }); await expect(combo).toBeEnabled(); await combo.click(); await page.getByRole("option", { name: optionName }).click(); };
  await choose("Imóvel", /Apartamento QA/);
  await choose("Responsável pela vistoria", /Pessoa QA/);
  await choose("Modelo de vistoria", /Modelo de saída/);
  await dialog.getByRole("combobox", { name: "Comparação" }).selectOption("FIXED_ORIGIN");
  return dialog;
}

test("E2E-007 blocks a photo move-out without an approved baseline and creates one with an explicitly selected eligible version", async ({ page }) => {
  const dialog = await installBaselineCreation(page, false);
  await expect(dialog.getByText("Não há inventário de referência aprovado. Catalogue e aprove uma origem antes de criar a vistoria de saída.")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Planejar vistoria" })).toBeDisabled();
  await page.close();

  const approvedPage = await page.context().newPage();
  const approvedDialog = await installBaselineCreation(approvedPage, true);
  await expect(approvedDialog.getByText("1 origem(ns) pronta(s) para este modelo.")).toBeVisible();
  await approvedDialog.getByRole("combobox", { name: "Origem" }).selectOption("approved-baseline");
  await approvedDialog.getByLabel("Vencimento").fill("2026-12-01T10:00");
  await approvedDialog.getByLabel("Prazo final").fill("2026-12-02T10:00");
  const request = approvedPage.waitForRequest((item) => item.url().endsWith("/graphql") && item.postData()?.includes("mutation PlanInspection") === true);
  await approvedDialog.getByRole("button", { name: "Planejar vistoria" }).click();
  expect((await request).postDataJSON().variables.input.referenceVersionId).toBe("approved-baseline");
  await approvedPage.close();
});

test("E2E-008 customer comparison filters retain counts and the total denominator", async ({ page }) => {
  await installJourney(page, "customer", true);
  await page.getByRole("button", { name: "Abrir resultado publicado" }).click();
  const report = page.getByRole("article").filter({ has: page.getByRole("heading", { name: "Apartamento QA" }) });
  await expect(report.getByText(`${comparisonGolden.counts} de ${comparisonGolden.counts} itens`)).toBeVisible();
  for (const outcome of comparisonGolden.outcomes) await expect(report.getByText(outcome, { exact: true })).toBeVisible();
  await expect(report.getByText("Armários íntegros nas vistas disponíveis.")).toBeVisible();
  await expect(report.getByText("Limitação: A parte superior não foi observada.")).toBeVisible();
  await expect(report.getByText(comparisonGolden.disclaimer, { exact: true })).toBeVisible();
  await expect(report.getByText("E001 ↔ E002", { exact: true })).toBeVisible();
  await report.getByRole("button", { name: "Alterado (1)", exact: true }).click();
  await expect(report.locator(".report-v3-comparison-item")).toHaveCount(1);
  await expect(report.getByText(`1 de ${comparisonGolden.counts} itens`)).toBeVisible();
  await report.getByRole("button", { name: "Sem base para comparar (1)" }).click();
  await expect(report.getByText("Não há uma vista avaliável do item na referência aprovada")).toBeVisible();
});
