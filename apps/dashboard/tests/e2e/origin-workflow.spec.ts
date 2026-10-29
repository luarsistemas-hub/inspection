import { expect, test, type Page, type Route } from "@playwright/test";
import type { DashboardOriginVersionsQuery } from "@/graphql/generated";
import { loginAsMockAdmin } from "./support/auth";

const pageInfo = { hasNextPage: false, endCursor: null };
const activeOrigin: DashboardOriginVersionsQuery["originVersions"]["nodes"][number] = {
  id: "origin-version-a", originId: "origin-a", versionNumber: 2, status: "ACTIVE", mediaKind: "VIDEO",
  preparationStatus: "READY", captureSubmitted: true, selectable: true, source: "INSPECTION_PROMOTION", sourceInspectionId: "inspection-old", draftState: null,
  sourceAvailability: "RETAINED", limitedCoverage: true, scopeManifestDigest: "digest-a", supersedesId: null, activatedAt: "2030-05-10T12:00:00Z", purpose: "INVENTORY_UPDATE", hasInventory: true,
};
const promotionDraft: DashboardOriginVersionsQuery["originVersions"]["nodes"][number] = {
  ...activeOrigin, id: "origin-version-draft", originId: "origin-draft", versionNumber: 3, status: "DRAFT", selectable: false,
  preparationStatus: null, source: "INSPECTION_PROMOTION", sourceInspectionId: "inspection-a", draftState: "READY", activatedAt: null, purpose: null, hasInventory: true,
};

async function installMocks(page: Page, initialHistoricalPromotion = false) {
  const calls: Array<{ operation: string; variables: Record<string, unknown> }> = [];
  let draftStatus = "DRAFT";
  let promotionStatus: string | null = initialHistoricalPromotion ? "COMPLETED" : null;
  let promotionPollCount = 0;
  let promotionRequestCount = 0;
  await page.route("**/graphql", async (route: Route) => {
    const request = route.request().postDataJSON() as { query: string; variables?: Record<string, unknown> };
    const operation = request.query.match(/(?:query|mutation)\s+(\w+)/)?.[1] ?? "unknown";
    calls.push({ operation, variables: request.variables ?? {} });
    let data: Record<string, unknown> = {};
    switch (operation) {
      case "DashboardMemberships":
      case "DashboardGate":
        data = { me: operation === "DashboardMemberships"
          ? { memberships: [{ id: "membership-a", tenantId: "tenant-a", role: "MANAGER", status: "ACTIVE" }] }
          : { tenantId: "tenant-a", productEntitlements: ["DASHBOARD"], roles: ["MANAGER"], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-a" }] },
        tenant: { id: "tenant-a", name: "Operação QA", status: "ACTIVE" } };
        break;
      case "DashboardFormOptions":
        data = { assets: { nodes: [{ id: "asset-a", name: "Apartamento QA", externalKey: "APT-1", businessUnitId: "unit-a", segmentVersionId: "segment-a", templateId: "template-a", status: "ACTIVE", version: 1 }], pageInfo },
          participants: { nodes: [{ id: "participant-a", businessUnitId: "unit-a", name: "Ana QA", status: "ACTIVE" }], pageInfo },
          templates: { nodes: [{ id: "template-a", key: "VIDEO", name: "Modelo vídeo", segmentVersionId: "segment-a", activeVersionId: "template-version-a", version: 1 }], pageInfo } };
        break;
      case "DashboardTemplateVersion": data = { templateVersion: { id: "template-version-a", mediaKind: "VIDEO", isVideo: true } }; break;
      case "DashboardOriginVersions": data = { originVersions: { nodes: [activeOrigin, { ...promotionDraft, status: draftStatus }, { ...activeOrigin, id: "origin-version-old", versionNumber: 1, status: "SUPERSEDED", purpose: "MOVE_IN" }], pageInfo } }; break;
      case "DiscardBaselineDraft":
        draftStatus = "DISCARDED";
        data = { discardBaselineDraft: { version: { id: "origin-version-draft", status: "DISCARDED" }, userErrors: [], clientMutationId: "discard-a" } };
        break;
      case "Inspections": data = { inspections: { nodes: [], pageInfo } }; break;
      case "Schedules": data = { schedules: { nodes: [], pageInfo } }; break;
      case "MyNotifications": data = { myNotifications: { nodes: [], unreadCount: 0, pageInfo } }; break;
      case "CreateSchedule": data = { createSchedule: { schedule: { id: "schedule-a", status: "ACTIVE", nextDueAt: "2030-05-11T12:00:00Z", version: 1 }, userErrors: [], clientMutationId: "schedule-a" } }; break;
      case "InspectionDetail": data = { inspection: { id: "inspection-a", assetId: "asset-a", participantId: "participant-a", projectId: null, stageId: null, source: "MANUAL", sourceReason: null, stateReason: null, status: "COMPLETED", mediaKind: "VIDEO", comparisonMode: "CHECKLIST_ONLY", referenceVersionId: null, referenceVersionNumber: null, referenceMediaKind: null, referenceVersionStatus: null, referenceIsActive: null, originPromotionStatus: null, originPromotionFailure: null, promotedOriginVersionId: null, promotedOriginVersionNumber: null, promotedOriginMediaKind: null, promotedOriginIsActive: null, evidenceCount: 1, dueAt: "2030-05-10T12:00:00Z", deadlineAt: "2030-05-11T12:00:00Z", reminderInstants: [], version: 1, assetName: "Apartamento QA", assetAddress: "Rua QA", assetExternalKey: "APT-1", participantName: "Ana QA" } }; break;
      case "InspectionOriginPromotion":
        if (promotionStatus && promotionPollCount > 0) promotionStatus = promotionPollCount >= 2 ? "COMPLETED" : "ACTIVATING";
        if (promotionStatus) promotionPollCount += 1;
        data = { inspectionOriginPromotion: { inspectionId: "inspection-a", eligible: true, impediments: [], activeOrigin: { id: "origin-active", versionNumber: 1, mediaKind: "PHOTO", hasInventory: true }, conflictingDraft: { id: "draft-conflict", source: "CAPTURE", sourceInspectionId: "inspection-other", draftState: "READY", ownInspection: false }, automaticCatalogSkipped: true, confirmationDigest: "digest-current", currentPromotion: promotionStatus ? { id: "promotion-a", status: promotionStatus, purpose: "INVENTORY_UPDATE", failureCode: null, failureReason: null, retryable: false, originVersion: promotionStatus === "COMPLETED" ? { id: initialHistoricalPromotion && promotionRequestCount === 0 ? "origin-version-historical" : "origin-version-new", versionNumber: initialHistoricalPromotion && promotionRequestCount === 0 ? 2 : initialHistoricalPromotion ? 3 : 2, mediaKind: "VIDEO", hasInventory: true } : null, supersededVersionId: "origin-active", draftVersionId: "draft-promotion", replacedDraftId: "draft-conflict", requestedAt: "2030-05-10T12:00:00Z", completedAt: promotionStatus === "COMPLETED" ? "2030-05-10T12:01:00Z" : null } : null, generatedVersionActive: promotionStatus === "COMPLETED" && !(initialHistoricalPromotion && promotionRequestCount === 0) } }; break;
      case "PromoteInspectionToOrigin":
        promotionRequestCount += 1;
        promotionStatus = "PREPARING";
        promotionPollCount = 0;
        data = { promoteInspectionToOrigin: { clientMutationId: "mutation-a", promotion: { id: "promotion-a", status: "PREPARING", purpose: "INVENTORY_UPDATE", failureCode: null, failureReason: null, retryable: false, originVersion: null, supersededVersionId: "origin-active", draftVersionId: "draft-promotion", replacedDraftId: "draft-conflict", requestedAt: "2030-05-10T12:00:00Z", completedAt: null }, originVersion: null, userErrors: [] } }; break;
      case "Projects": data = { projects: { nodes: [], pageInfo } }; break;
      case "PlanInspection": data = { planInspection: { inspection: { id: "inspection-new", status: "PLANNED", version: 1, projectId: null, stageId: null }, schedule: null, project: null, userErrors: [], clientMutationId: "plan-a" } }; break;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data }) });
  });
  return calls;
}

test.describe("Dashboard origin management mock journey", () => {
  test.skip(process.env.INSPECTION_E2E_MOCKS !== "true", "run with INSPECTION_E2E_MOCKS=true and the local identity stack");

  test("discards a promotion draft, then selects an active origin for inspection planning", async ({ page }) => {
    const calls = await installMocks(page);
    await loginAsMockAdmin(page, "/inspections?inspectionId=inspection-a");

    await expect(page.getByText("Esta vistoria foi configurada sem referência de comparação.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Usar esta vistoria como origem" })).toBeVisible();
    await expect(page.getByText("A catalogação automática desta vistoria foi ignorada porque havia outro rascunho aberto.")).toBeVisible();
    await page.getByRole("button", { name: "Usar esta vistoria como origem" }).click();
    const promotionDialog = page.getByRole("dialog", { name: "Usar esta vistoria como origem" });
    await expect(promotionDialog.getByText("Versão 1", { exact: false })).toBeVisible();
    await expect(promotionDialog.getByText("O rascunho de inventário aberto para este imóvel (origem: captura de vistoria, estado: Pronto para revisão) será descartado.")).toBeVisible();
    await promotionDialog.getByRole("button", { name: "Confirmar substituição" }).click();
    await expect(page.getByText("Preparando o inventário…")).toBeVisible();
    await expect(page.getByText("Ativando a nova origem…")).toBeVisible({ timeout: 5_000 });
    await expect(page.getByText("Versão 2 criada e ativa.")).toBeVisible();
    expect(calls.find((call) => call.operation === "PromoteInspectionToOrigin")?.variables.input).toMatchObject({ inspectionId: "inspection-a", purpose: "INVENTORY_UPDATE", replaceDraftId: "draft-conflict", confirmationDigest: "digest-current" });
    await expect(page.getByText("Convide o responsável para enviar fotos")).toHaveCount(0);
    await page.getByRole("button", { name: "Origens do imóvel" }).click();
    const originsDialog = page.getByRole("dialog", { name: "Origens do imóvel" });
    await expect(originsDialog.getByText("Rascunho de promoção pronto para revisão")).toBeVisible();
    await expect(originsDialog.getByRole("button", { name: "Ativar", exact: true })).toHaveCount(0);
    await expect(originsDialog.getByRole("button", { name: "Fechar convite", exact: true })).toHaveCount(0);
    await expect(originsDialog.getByRole("button", { name: "Convidar origem", exact: false })).toHaveCount(0);
    await originsDialog.getByRole("button", { name: "Descartar rascunho" }).click();
    const discardDialog = page.getByRole("dialog", { name: "Descartar rascunho" });
    await expect(discardDialog.getByText(/a promoção também será cancelada/i)).toBeVisible();
    await discardDialog.getByRole("button", { name: "Descartar rascunho" }).click();
    await expect.poll(() => calls.find((call) => call.operation === "DiscardBaselineDraft")?.variables.input).toMatchObject({ originVersionId: "origin-version-draft" });
    await expect(originsDialog.getByText("Descartada")).toBeVisible();
    expect(calls.filter((call) => ["InviteOriginCapture", "ActivateOriginVersion", "CloseOriginInvitation"].includes(call.operation))).toHaveLength(0);
    await originsDialog.getByRole("button", { name: "Fechar", exact: true }).click();
    await expect(originsDialog).toBeHidden();
    const detailDialog = page.getByRole("dialog", { name: "Detalhe da vistoria" });
    await detailDialog.getByRole("button", { name: "Fechar", exact: true }).click();
    await expect(detailDialog).toBeHidden();

    await page.getByRole("button", { name: "Nova vistoria" }).click();
    const form = page.getByRole("dialog", { name: "Nova vistoria" }).locator("form");
    await form.getByRole("combobox", { name: "Imóvel" }).click();
    await page.getByRole("option", { name: /Apartamento QA/ }).click();
    await form.getByRole("combobox", { name: "Responsável pela vistoria" }).click();
    await page.getByRole("option", { name: /Ana QA/ }).click();
    await form.getByRole("combobox", { name: "Modelo de vistoria" }).click();
    await page.getByRole("option", { name: /Modelo vídeo/ }).click();
    await expect(form.getByLabel("Comparação")).toHaveValue("FIXED_ORIGIN");
    await expect.poll(() => calls.some((call) => call.operation === "DashboardOriginVersions" && call.variables.assetId === "asset-a")).toBe(true);
    await expect(form.getByLabel("Origem", { exact: true })).toHaveValue("origin-version-a");
    await form.getByLabel("Comparação").selectOption("FIXED_ORIGIN");
    await expect(form.getByRole("option", { name: /Versão 1.*anterior.*Inventário/ })).toBeAttached();
    await form.getByLabel("Origem", { exact: true }).selectOption("origin-version-old");
    await form.getByLabel("Vencimento").fill("2030-05-10T12:00");
    await form.getByLabel("Prazo final").fill("2030-05-11T12:00");
    await form.getByRole("button", { name: "Planejar vistoria" }).click();
    await expect.poll(() => calls.find((call) => call.operation === "PlanInspection")?.variables.input).toMatchObject({
      assetId: "asset-a", comparisonMode: "FIXED_ORIGIN", referenceVersionId: "origin-version-old",
    });
  });

  test("allows re-promoting an inspection whose earlier generated version was superseded", async ({ page }) => {
    const calls = await installMocks(page, true);
    await loginAsMockAdmin(page, "/inspections?inspectionId=inspection-a");

    await expect(page.getByRole("status").filter({ hasText: "Esta vistoria gerou a versão 2, hoje substituída." })).toBeVisible();
    await page.getByRole("button", { name: "Usar esta vistoria como origem" }).click();
    const dialog = page.getByRole("dialog", { name: "Usar esta vistoria como origem" });
    await expect(dialog.getByRole("button", { name: "Confirmar substituição" })).toBeVisible();
    await dialog.getByRole("button", { name: "Confirmar substituição" }).click();
    await expect(page.getByText("Preparando o inventário…")).toBeVisible();
    await expect(page.getByText("Versão 3 criada e ativa.")).toBeVisible({ timeout: 10_000 });
    expect(calls.filter((call) => call.operation === "PromoteInspectionToOrigin")).toHaveLength(1);
  });

  test("creates a recurring schedule that follows the active origin", async ({ page }) => {
    const calls = await installMocks(page);
    await loginAsMockAdmin(page, "/schedules");
    await page.getByRole("button", { name: "Nova agenda" }).click();
    const dialog = page.getByRole("dialog", { name: "Nova agenda" });
    for (const label of ["Imóvel", "Responsável pela vistoria", "Modelo de vistoria"]) {
      await dialog.getByRole("combobox", { name: label }).click();
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("Enter");
    }
    await dialog.getByLabel("Comparação").selectOption("FIXED_ORIGIN");
    await expect(dialog.getByLabel("Acompanhar origem ativa")).toBeChecked();
    await dialog.getByLabel("Primeira vistoria").fill("2030-05-11T12:00");
    await dialog.getByLabel("Repetir").selectOption("MONTHLY");
    await dialog.getByRole("button", { name: "Criar agenda" }).click();
    await expect.poll(() => calls.find((call) => call.operation === "CreateSchedule")?.variables.input).toMatchObject({ comparisonMode: "FIXED_ORIGIN", referenceVersionId: null });
  });
});
