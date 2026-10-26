import { expect, test, type Page } from "@playwright/test";

const pageInfo = { endCursor: null, hasNextPage: false };
const activeVersion = {
  id: "version-active", originId: "origin-a", versionNumber: 2, status: "ACTIVE", mediaKind: "PHOTO", activatedAt: "2026-10-12T12:00:00Z", activatedBy: "membership-author", purpose: "MOVE_IN", source: "INSPECTION", sourceInspectionId: "inspection-a", hasInventory: true, draftState: "READY", sourceAvailability: "EXPIRED", counts: { included: 9, rejected: 1, notReviewed: 0 }, contestationSummary: { pending: 0, acknowledged: 1, contested: 1, open: 0, noResponse: 1 }, counterpartyInvitations: [{ id: "invite-failed", participantId: "participant-a", response: "PENDING", deadlineAt: "2026-11-01T00:00:00Z", respondedAt: null, statement: null, resolution: "OPEN", resolutionReason: null, adjustmentVersionId: null, deliveryStatus: "FAILED", deliveryId: "delivery-a" }, { id: "invite-contested", participantId: "participant-b", response: "CONTESTED", deadlineAt: "2026-11-01T00:00:00Z", respondedAt: "2026-10-20T12:00:00Z", statement: "O revestimento já tinha esta condição.", resolution: "OPEN", resolutionReason: null, adjustmentVersionId: null, deliveryStatus: "DELIVERED", deliveryId: "delivery-b" }] as Array<Record<string, unknown>>,
};
const earlierVersion = { ...activeVersion, id: "version-earlier", versionNumber: 1, status: "SUPERSEDED", activatedBy: "membership-old", purpose: "CONSTRUCTION_HANDOVER", sourceInspectionId: "inspection-earlier", sourceAvailability: "RETAINED", contestationSummary: { pending: 0, acknowledged: 0, contested: 0, open: 0, noResponse: 0 }, counterpartyInvitations: [] };
const draftVersion = { ...activeVersion, id: "draft-a", versionNumber: 3, status: "DRAFT", activatedAt: null, activatedBy: null, purpose: null, sourceAvailability: "AVAILABLE", draftState: "READY", contestationSummary: { pending: 0, acknowledged: 0, contested: 0, open: 0, noResponse: 0 }, counterpartyInvitations: [] };

async function openBaselines(page: Page, role = "MANAGER", returnTo = "/baselines") {
  const calls: Array<{ operation: string; variables: Record<string, unknown> }> = [];
  let discardStatus = "DRAFT";
  let adjustmentDraftCreated = false;
  await page.route("**/protocol/openid-connect/token", (route) => route.fulfill({ status: 200, contentType: "application/json", headers: { "access-control-allow-origin": "*" }, body: JSON.stringify({ access_token: "baselines-journey-token" }) }));
  await page.addInitScript((target) => sessionStorage.setItem("inspection.dashboard.pkce", JSON.stringify({ verifier: "baselines-journey-verifier", state: "baselines-journey-state", returnTo: target })), returnTo);
  await page.route("**/graphql", async (route) => {
    const request = route.request().postDataJSON() as { query: string; variables?: Record<string, unknown> };
    const operation = request.query.match(/(?:query|mutation)\s+(\w+)/)?.[1] ?? "unknown";
    calls.push({ operation, variables: request.variables ?? {} });
    const memberships = [{ id: "membership-a", tenantId: "tenant-a", name: "Operador QA", email: "operator@example.test", role, status: "ACTIVE" }];
    let data: Record<string, unknown> = {};
    if (operation === "DashboardGate" || operation === "DashboardMemberships") data = { me: { identityId: "identity-a", tenantId: "tenant-a", productEntitlements: ["DASHBOARD"], roles: [role], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-a" }], memberships }, tenant: { id: "tenant-a", name: "Operação QA", status: "ACTIVE" } };
    if (operation === "DashboardBaselineAsset") data = { asset: { id: "asset-a", name: "Apartamento QA", externalKey: "APT-1", businessUnitId: "unit-a" } };
    if (operation === "DashboardBaselineAssets") data = { assets: { nodes: [{ id: "asset-a", name: "Apartamento QA", externalKey: "APT-1", businessUnitId: "unit-a", segmentVersionId: "segment-a", templateId: "template-a", status: "ACTIVE", version: 1 }], pageInfo } };
    if (operation === "DashboardCustomerBaselineAssets" || operation === "CustomerPortfolio") data = { customerPortfolio: { nodes: [{ assetId: "asset-a", assetName: "Edifício QA", assetExternalKey: "QA-001", latestPublishedInspectionId: "inspection-published", projectId: null, publishedClassification: "ATTENTION", status: "ACTIVE", progress: 100, updatedAt: "2026-10-12T12:00:00Z" }], pageInfo } };
    if (operation === "CustomerReport") data = { customerReport: { inspectionId: "inspection-published", snapshotId: "snapshot-published", version: 2, classification: "ATTENTION", advisory: "Uma alteração exige acompanhamento.", status: "PUBLISHED", historical: false, context: { asset: { id: "asset-a", name: "Edifício QA", externalKey: "QA-001", address: "Rua QA" }, participant: { id: "participant-a", name: "Pessoa QA" }, template: { id: "template-a", name: "Modelo QA", version: 2 }, inspection: { projectId: null, stageId: null, stageLabel: "Saída", dueAt: null, submittedAt: null, generatedAt: "2026-10-20T12:00:00Z" } }, inventory: { referenceVersionNumber: 1, purpose: "MOVE_IN", coverage: { expectedItems: 8, assessedItems: 7, notObserved: 1, environmentsNotCaptured: [], environmentsAddedDuringCapture: [] }, problems: [{ environment: "Sala", type: "SOFA", name: "Sofá", status: "MISSING", conditionBefore: "GOOD", conditionAfter: null, reason: "Não localizado", pairs: [] }], informational: [] }, requirements: [] } };
    if (operation === "CustomerEvidence") data = { customerEvidence: { nodes: [], pageInfo } };
    if (operation === "DashboardCounterpartyOptions") data = { participants: { nodes: [{ id: "participant-a", businessUnitId: "unit-a", name: "Proprietária QA", status: "ACTIVE" }, { id: "participant-b", businessUnitId: "unit-a", name: "Inquilino QA", status: "ACTIVE" }], pageInfo } };
    if (operation === "DashboardReviewers") data = { memberships: { nodes: [{ id: "membership-author", tenantId: "tenant-a", name: "Gestora QA", email: "gestora@example.test", role: "MANAGER", status: "ACTIVE", scopes: [] }], pageInfo } };
    if (operation === "DashboardBaselineVersions") data = { baselineVersions: { nodes: [activeVersion, earlierVersion, { ...draftVersion, status: discardStatus }, ...(adjustmentDraftCreated ? [{ ...draftVersion, id: "adjustment-draft", versionNumber: 4, status: "DRAFT", purpose: "CONTESTATION_ADJUSTMENT" }] : [])], pageInfo } };
    if (operation === "DashboardBaselineInventory") data = { baselineInventory: { hasInventory: true, version: { id: "version-active", versionNumber: 2, status: "ACTIVE", purpose: "MOVE_IN", activatedAt: "2026-10-12T12:00:00Z", sourceAvailability: "EXPIRED" }, environments: [{ id: "environment-a", typeCode: "LIVING_ROOM", name: "Sala", expected: true, addedDuringCapture: false, items: [{ id: "item-a", typeCode: "SOFA", name: "Sofá", material: "tecido", color: "azul", quantity: 1, condition: "GOOD", observation: "OBSERVED", limitation: null, declared: false, decision: "ACCEPTED", evidence: [] }] }] } };
    if (operation === "DashboardInventoryDraft") {
      const originVersionId = (request.variables as { originVersionId?: string } | undefined)?.originVersionId;
      const preparing = originVersionId === "inspection-draft";
      data = {
      inventoryDraft: {
        assetId: "asset-a",
        readOnly: discardStatus === "DISCARDED", noticeCode: null, warnings: [],
        version: { id: originVersionId ?? "draft-a", originRevision: 1, versionNumber: 3, status: discardStatus, draftState: preparing ? "PREPARING" : "READY", hasInventory: true, purpose: null, counts: { included: 0, rejected: 0, notReviewed: 1 } },
        progress: { decided: 0, pending: 1 },
        environments: [{
          id: "environment-a", identityId: "env-a", typeCode: "LIVING_ROOM", name: "Sala", expected: true, addedDuringCapture: false, decision: "PENDING", position: 0,
          items: [{ id: "item-a", identityId: "identity-item-a", environmentId: "environment-a", typeCode: "SOFA", name: "Sofá", material: null, color: null, quantity: null, condition: "GOOD", observation: "OBSERVED", limitation: null, confidenceBps: 9500, origin: "AI", declared: false, decision: "PENDING", rejectionReason: null, version: 1, evidence: [] }],
        }],
      },
      };
    }
    if (operation === "RequestOriginCataloging") data = { requestOriginCataloging: { version: { id: "catalog-draft", status: "DRAFT", versionNumber: 3, draftState: "PREPARING" }, userErrors: [], clientMutationId: "catalog" } };
    if (operation === "StartBaselineDraftFromInspection") data = { startBaselineDraftFromInspection: { version: { id: "inspection-draft", status: "DRAFT", draftState: "READY" }, userErrors: [], clientMutationId: "from-inspection" } };
    if (operation === "DiscardBaselineDraft") { discardStatus = "DISCARDED"; data = { discardBaselineDraft: { version: { id: "draft-a", status: "DISCARDED" }, userErrors: [], clientMutationId: "discard" } }; }
    if (operation === "ResendCounterpartyInvitation") data = { resendCounterpartyInvitation: { invitationId: "invite-failed", response: "PENDING", deadlineAt: "2026-11-01T00:00:00Z", userErrors: [], clientMutationId: "resend" } };
    if (operation === "ResolveBaselineContestation") { const outcome = (request.variables?.input as { outcome?: string })?.outcome; adjustmentDraftCreated = outcome === "ACCEPT"; data = { resolveBaselineContestation: { invitationId: "invite-contested", response: "CONTESTED", resolution: outcome === "ACCEPT" ? "ACCEPTED" : "DECLINED", adjustmentVersionId: adjustmentDraftCreated ? "adjustment-draft" : null, userErrors: [], clientMutationId: "resolve" } }; }
    if (operation === "InspectionDetail") data = { inspection: { id: "inspection-a", assetId: "asset-a" } };
    if (operation === "Inspections") data = { inspections: { nodes: [{ id: "inspection-a", assetId: "asset-a", participantName: "Participante QA", status: "COMPLETED", dueAt: "2026-10-20T12:00:00Z" }], pageInfo } };
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data }) });
  });
  await page.goto("/auth/callback?code=baselines-code&state=baselines-journey-state");
  await expect(page.getByRole("navigation", { name: "Painel" })).toBeVisible();
  if (returnTo === "/baselines") await expect(page.getByRole("heading", { name: "Inventários dos imóveis" })).toBeVisible();
  return calls;
}

async function navigate(page: Page, label: string) {
  const navigation = page.getByRole("navigation", { name: "Painel" });
  const destination = navigation.getByRole("link", { name: label, exact: true });
  if (!(await destination.isVisible())) await navigation.getByRole("button", { name: /^Mais destinos/ }).click();
  await navigation.getByRole("link", { name: label, exact: true }).click();
}

test("E2E-008 history shows version purpose activation author and contestation summary", async ({ page }, testInfo) => {
  await openBaselines(page);
  await expect(page.getByRole("heading", { name: /Versão 2 · Entrada de locação/ })).toBeVisible();
  await expect(page.getByText(/Autor Gestora QA/)).toBeVisible();
  await expect(page.getByText(/1 aceitas · 1 contestadas · 1 sem resposta no prazo/)).toBeVisible();
});

test("E2E-008 creates an inspection pinned to an earlier baseline version", async ({ page }) => {
  await openBaselines(page);
  const earlier = page.getByRole("listitem").filter({ has: page.getByRole("heading", { name: "Versão 1 · Entrega de obra", exact: true }) });
  await earlier.getByRole("button", { name: "Criar vistoria com esta versão", exact: true }).click();
  await expect(page).toHaveURL(/\/inspections\?create=1&assetId=asset-a&originVersionId=version-earlier/);
});

test("E2E-009 requests legacy-origin cataloging and starts a draft from a completed inspection", async ({ page }, testInfo) => {
  const calls = await openBaselines(page);
  await page.getByRole("button", { name: "Catalogar origem existente" }).click();
  await expect(page.getByRole("heading", { name: "Revisar inventário" })).toBeVisible();
  await navigate(page, "Inventários");
  await page.getByLabel("Vistoria concluída").selectOption("inspection-a");
  await page.getByRole("button", { name: "Criar rascunho desta vistoria" }).click();
  await expect(page).toHaveURL(/\/baselines\/review\/inspection-draft$/);
  await expect(page.getByText("O inventário está sendo preparado. Você pode voltar a esta página mais tarde.", { exact: true })).toBeVisible();
  expect(calls.map(({ operation }) => operation)).toEqual(expect.arrayContaining(["RequestOriginCataloging", "StartBaselineDraftFromInspection"]));
});

test("E2E-009 opens the inspection-keyed waiting route from the authentication returnTo", async ({ page }) => {
  await openBaselines(page, "MANAGER", "/baselines/review/inspection/inspection-a");
  await expect(page).toHaveURL(/\/baselines\/review\/inspection\/inspection-a$/);
  await expect(page.getByRole("heading", { name: "Inventário pronto", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Revisar inventário", exact: true })).toBeVisible();
});

test("E2E-014 resolves contestation with a reason and exposes the adjustment draft", async ({ page }, testInfo) => {
  const calls = await openBaselines(page);
  const counterparties = page.getByRole("region", { name: "Contrapartes da versão" });
  await expect(counterparties.getByText(/Proprietária QA · Aguardando resposta · .* · entrega: Falhou/)).toBeVisible();
  await expect(counterparties.getByText(/Inquilino QA · Contestou/)).toBeVisible();
  await counterparties.getByRole("button", { name: "Reenviar convite para Proprietária QA", exact: true }).click();
  await expect.poll(() => calls.find(({ operation }) => operation === "ResendCounterpartyInvitation")?.variables.input).toMatchObject({ counterpartyInvitationId: "invite-failed" });
  await page.getByLabel("Justificativa para recusar").fill("A evidência apresentada foi conferida.");
  await page.getByRole("button", { name: "Recusar contestação" }).click();
  await expect.poll(() => calls.find(({ operation }) => operation === "ResolveBaselineContestation")?.variables.input).toMatchObject({ outcome: "DECLINE", reason: "A evidência apresentada foi conferida." });
});

test("E2E-014 accepting a contestation exposes its new draft for review", async ({ page }) => {
  const calls = await openBaselines(page);
  await expect(page.getByRole("button", { name: "Revisar rascunho", exact: true })).toHaveCount(1);
  await page.getByRole("button", { name: "Aceitar e criar rascunho", exact: true }).click();
  await expect(page.getByText("Contestação aceita; um novo rascunho foi criado.", { exact: true })).toBeVisible();
  await expect.poll(() => calls.filter(({ operation }) => operation === "ResolveBaselineContestation").length).toBe(1);
  expect(calls.find(({ operation }) => operation === "ResolveBaselineContestation")?.variables.input).toMatchObject({ outcome: "ACCEPT", reason: null });
  await expect(page.getByRole("button", { name: "Revisar rascunho", exact: true })).toHaveCount(2);
});

test("E2E-015 CUSTOMER_VIEWER sees the published baseline without review controls", async ({ page }, testInfo) => {
  await openBaselines(page, "CUSTOMER_VIEWER");
  await expect(page.getByText("Sofá · Bom — uso leve, sem danos visíveis")).toBeVisible();
  await expect(page.getByRole("button", { name: "Catalogar origem existente" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Ativar inventário" })).toHaveCount(0);
});

test("E2E-015 customer selects a published exception report; unpublished reports are absent", async ({ page }) => {
  await openBaselines(page, "CUSTOMER_VIEWER");
  await navigate(page, "Laudos publicados");
  const reportPicker = page.getByLabel("Laudo publicado", { exact: true });
  await expect(reportPicker.getByRole("option")).toHaveCount(2);
  await expect(reportPicker.getByRole("option", { name: /inspection-unpublished/ })).toHaveCount(0);
  await reportPicker.selectOption("inspection-published");
  await page.getByRole("button", { name: "Abrir resultado publicado", exact: true }).click();
  const report = page.getByRole("article").filter({ has: page.getByRole("heading", { name: "Edifício QA", exact: true }) });
  await expect(report.getByRole("heading", { name: "Sofá", exact: true })).toBeVisible();
  await expect(report.getByText("Uma alteração exige acompanhamento.", { exact: true })).toBeVisible();
});

test("E2E-017 labels expired baseline source and E2E-018 discards a draft", async ({ page }, testInfo) => {
  const calls = await openBaselines(page);
  await expect(page.getByText("original não disponível; quadro preservado")).toBeVisible();
  await navigate(page, "Inventários");
  await page.getByRole("button", { name: "Revisar rascunho", exact: true }).click();
  await expect(page.getByRole("button", { name: "Descartar rascunho" })).toBeVisible();
  await page.getByRole("button", { name: "Descartar rascunho" }).click();
  const discardDialog = page.getByRole("dialog", { name: "Descartar rascunho" });
  await discardDialog.getByRole("button", { name: "Descartar rascunho", exact: true }).click();
  await expect(page).toHaveURL(/\/baselines$/);
  expect(calls.map(({ operation }) => operation)).toContain("DiscardBaselineDraft");
  await expect(page.getByRole("button", { name: "Revisar rascunho", exact: true })).toHaveCount(0);
  const discarded = page.getByRole("listitem").filter({ has: page.getByRole("heading", { name: /^Versão 3 · / }) });
  await expect(discarded.getByText("Descartada", { exact: true })).toBeVisible();
  await discarded.getByRole("button", { name: "Ver inventário", exact: true }).click();
  await expect(page).toHaveURL(/\/baselines\/review\/draft-a$/);
  await expect(page.getByText("Este inventário está em modo somente leitura.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Ativar inventário", exact: true })).toHaveCount(0);
});

