import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, test, type Page } from "@playwright/test";

async function authenticate(page: Page, returnTo = "/reports") {
  await page.route("**/protocol/openid-connect/token", (route) => route.fulfill({ status: 200, contentType: "application/json", headers: { "access-control-allow-origin": "*" }, body: JSON.stringify({ access_token: "report-v2-test-token" }) }));
  await page.addInitScript((target) => sessionStorage.setItem("inspection.dashboard.pkce", JSON.stringify({ verifier: "report-v2-verifier", state: "report-v2-state", returnTo: target })), returnTo);
  await page.route("**/graphql", async (route) => {
    const request = route.request().postDataJSON() as { query: string; variables?: Record<string, unknown> };
    const operation = request.query.match(/(?:query|mutation)\s+(\w+)/)?.[1] ?? "unknown";
    let data: Record<string, unknown> = {};
    if (operation === "DashboardGate" || operation === "DashboardMemberships") data = { me: { identityId: "identity-a", tenantId: "tenant-a", productEntitlements: ["DASHBOARD"], roles: ["MANAGER"], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-a" }], memberships: [{ id: "membership-a", tenantId: "tenant-a", role: "MANAGER", status: "ACTIVE" }] }, tenant: { id: "tenant-a", name: "Operação QA", status: "ACTIVE" } };
    if (operation === "Reports") data = { reports: { nodes: [{ id: "snapshot-critical", inspectionId: "inspection-critical", assetName: "Imóvel QA", assetAddress: "Rua QA", assetExternalKey: "QA-1", participantName: "Pessoa QA", generatedAt: "2026-10-20T12:00:00Z", classification: "CRITICAL", version: 2 }, { id: "snapshot-normal", inspectionId: "inspection-normal", assetName: "Imóvel sem alertas", assetAddress: "Rua QA", assetExternalKey: "QA-2", participantName: "Pessoa QA", generatedAt: "2026-10-20T12:00:00Z", classification: "NORMAL", version: 2 }], pageInfo: { endCursor: null, hasNextPage: false } } };
    if (operation === "ReportWorkspace") {
      const normal = request.variables?.inspectionId === "inspection-normal";
      const evidence = normal ? [] : [
        { id: "origin-frame", requirementKey: "inventory:item-a", role: "REFERENCE", description: "Armário", capturedAt: null, availability: "AVAILABLE", sourceAvailability: "EXPIRED", evidenceKind: "VIDEO_FRAME", timestampMs: 5000, sourceMediaId: "origin-video", component: "Armário", label: "Armário", flags: [], url: "/origin-frame.jpg" },
        { id: "current-frame", requirementKey: "inventory:item-a", role: "CURRENT", description: "Armário", capturedAt: null, availability: "AVAILABLE", sourceAvailability: "AVAILABLE", evidenceKind: "VIDEO_FRAME", timestampMs: 17500, sourceMediaId: "current-video", component: "Armário", label: "Armário", flags: [], url: "/current-frame.jpg" },
      ];
      data = { report: { id: normal ? "snapshot-normal" : "snapshot-critical", inspectionId: normal ? "inspection-normal" : "inspection-critical", projectId: null, version: 2, classification: normal ? "NORMAL" : "CRITICAL", advisory: "Resumo da vistoria.", pdfStatus: "READY", context: { asset: { id: "asset-a", name: normal ? "Imóvel sem alertas" : "Imóvel QA", externalKey: "QA-1", address: "Rua QA" }, participant: { id: "person-a", name: "Pessoa QA" }, template: { id: "template-a", name: "Padrão", version: 2 }, inspection: { stageLabel: "Saída" } }, inventory: { referenceVersionNumber: 1, purpose: "MOVE_IN", coverage: { expectedItems: 1, assessedItems: normal ? 1 : 0, notObserved: 0, environmentsNotCaptured: [], environmentsAddedDuringCapture: [] }, problems: normal ? [] : [{ environment: "Sala", type: "CABINET", name: "Armário", status: "MISSING", conditionBefore: "GOOD", conditionAfter: null, reason: "Não localizado", pairs: [{ originEvidenceId: "origin-frame", currentEvidenceId: "current-frame", originSourceAvailability: "EXPIRED", currentSourceAvailability: "AVAILABLE", timestampMs: 17500 }] }], informational: [] }, requirements: [], evidence: { nodes: evidence, pageInfo: { endCursor: null, hasNextPage: false } }, findings: [] } };
    }
    if (operation === "DashboardSummary") data = { dashboardSummary: { total: 3, normal: 1, attention: 1, critical: 1, pending: 0, invalidated: 0 } };
    if (operation === "TriageWorkspace") data = { triageWorkspace: { counts: { new: 2, inReview: 0, awaitingEvidence: 0, criticalOpen: 1 }, nodes: [{ inspectionId: "normal", assetId: "asset-n", assetName: "Normal oculto", address: "Rua QA", classification: "NORMAL", status: "COMPLETED", reviewStatus: "NEW", assigneeId: null, reportVersion: 1, version: 1, findingCount: 0, reasonCodes: [], createdAt: "2026-10-01T00:00:00Z", updatedAt: "2026-10-01T00:00:00Z" }, { inspectionId: "attention", assetId: "asset-a", assetName: "Atenção QA", address: "Rua QA", classification: "ATTENTION", status: "COMPLETED", reviewStatus: "NEW", assigneeId: null, reportVersion: 1, version: 1, findingCount: 1, reasonCodes: ["DAMAGE"], createdAt: "2026-10-01T00:00:00Z", updatedAt: "2026-10-01T00:00:00Z" }, { inspectionId: "critical", assetId: "asset-c", assetName: "Crítica QA", address: "Rua QA", classification: "CRITICAL", status: "COMPLETED", reviewStatus: "NEW", assigneeId: null, reportVersion: 1, version: 1, findingCount: 1, reasonCodes: ["INVENTORY_ITEM_MISSING"], createdAt: "2026-10-01T00:00:00Z", updatedAt: "2026-10-01T00:00:00Z" }], pageInfo: { endCursor: null, hasNextPage: false } } };
    if (operation === "TriageAssignees") data = { triageAssignees: [] };
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data }) });
  });
  await page.route("**/api/media/videos/session", (route) => route.fulfill({ status: 204 }));
  await page.goto("/auth/callback?code=report-v2-code&state=report-v2-state");
  await expect(page.getByRole("navigation", { name: "Painel" })).toBeVisible();
}

test("E2E-010 exception-first report pairs evidence and summary-only NORMAL", async ({ page }, testInfo) => {
  // Playwright's Chromium has no H.264 decoder, so the seek fixture is VP9/WebM.
  const videoFixture = readFileSync(resolve("tests/e2e/fixtures/inventory-playback.webm"));
  await authenticate(page, "/reports?inspectionId=inspection-critical");
  const report = page.getByRole("dialog", { name: "Laudo de vistoria" });
  await expect(report.getByRole("heading", { name: "Problemas encontrados" })).toBeVisible();
  await expect(report.getByRole("heading", { name: "Armário" })).toBeVisible();
  await expect(report.getByText("Na origem")).toBeVisible();
  await expect(report.getByText("Original não disponível; quadro preservado", { exact: true })).toBeVisible();
  await expect(report.getByText("Na vistoria atual")).toBeVisible();
  await expect(report.getByText("Momento 00:17", { exact: true })).toBeVisible();
  await expect(report.getByText("Momento 00:05", { exact: true })).toBeVisible();
  await page.route("**/api/media/videos/current-video/content?*", async (route) => {
    expect(new URL(route.request().url()).searchParams.get("snapshotId")).toBe("snapshot-critical");
    const range = route.request().headers().range?.match(/^bytes=(\d+)-(\d*)$/);
    const start = Number(range?.[1] ?? 0);
    const end = Math.min(Number(range?.[2] || videoFixture.length - 1), videoFixture.length - 1);
    await route.fulfill({ status: range ? 206 : 200, contentType: "video/webm", headers: { "accept-ranges": "bytes", ...(range ? { "content-range": `bytes ${start}-${end}/${videoFixture.length}` } : {}) }, body: videoFixture.subarray(start, end + 1) });
  });
  const currentEvidence = report.locator(".report-inventory-pair > div").nth(1);
  await currentEvidence.getByRole("button", { name: "Reproduzir vídeo no ponto 00:17", exact: true }).click();
  const video = report.locator("video[aria-label='Vídeo da evidência Armário']");
  await expect(video).toBeVisible();
  await expect.poll(() => video.evaluate((element: HTMLVideoElement) => element.readyState >= 1)).toBe(true);
  await video.evaluate((element: HTMLVideoElement) => element.pause());
  await expect.poll(() => video.evaluate((element: HTMLVideoElement) => element.currentTime)).toBeGreaterThanOrEqual(17.5);
  await report.getByRole("button", { name: "Fechar", exact: true }).click();
  await page.getByRole("button", { name: "Abrir detalhes do laudo de Imóvel sem alertas", exact: true }).click();
  const normalReport = page.getByRole("dialog", { name: "Laudo de vistoria" });
  await expect(normalReport.getByRole("heading", { name: "Sem alertas no inventário" })).toBeVisible();
  await expect(normalReport.locator(".report-room-list")).toHaveCount(0);
  await expect(normalReport.getByText("1 de 1 itens avaliados")).toBeVisible();
});

test("E2E-011 triage only renders Requer atenção and Crítica", async ({ page }, testInfo) => {
  await authenticate(page);
  const navigation = page.getByRole("navigation", { name: "Painel" });
  const triageLink = navigation.getByRole("link", { name: "Triagem", exact: true });
  if (!(await triageLink.isVisible())) await navigation.getByRole("button", { name: /^Mais destinos/ }).click();
  await navigation.getByRole("link", { name: "Triagem", exact: true }).click();
  await expect(page.getByText("Atenção QA")).toBeVisible();
  await expect(page.getByText("Crítica QA")).toBeVisible();
  await expect(page.getByText("Normal oculto")).toHaveCount(0);
  const queue = page.getByRole("region", { name: "Casos para revisão" });
  await expect(queue.getByRole("listitem").filter({ hasText: "Atenção QA" }).getByText("Requer atenção", { exact: true })).toBeVisible();
  await expect(queue.getByRole("listitem").filter({ hasText: "Crítica QA" }).getByText("Crítica", { exact: true })).toBeVisible();
});
