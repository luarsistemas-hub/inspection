import { expect, test, type Page, type Route } from "@playwright/test";
import type { DashboardOriginVersionsQuery } from "@/graphql/generated";
import { loginAsLocalAdmin } from "./support/auth";

const pageInfo = { hasNextPage: false, endCursor: null };
const readyOrigin: DashboardOriginVersionsQuery["originVersions"]["nodes"][number] = {
  id: "origin-version-a", originId: "origin-a", versionNumber: 1, status: "DRAFT", mediaKind: "VIDEO",
  preparationStatus: "READY", captureSubmitted: true, selectable: false,
  sourceAvailability: "RETAINED", limitedCoverage: true, scopeManifestDigest: "digest-a", supersedesId: null, activatedAt: null, purpose: null, hasInventory: false,
};

async function installMocks(page: Page) {
  const calls: Array<{ operation: string; variables: Record<string, unknown> }> = [];
  let originVersion = { ...readyOrigin };
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
      case "DashboardReviewers": data = { memberships: { nodes: [{ id: "membership-a", tenantId: "tenant-a", name: "Gestora QA", email: "reviewer@example.test", role: "MANAGER", status: "ACTIVE", scopes: [{ kind: "BUSINESS_UNIT", resourceId: "unit-a" }] }], pageInfo } }; break;
      case "DashboardTemplateVersion": data = { templateVersion: { id: "template-version-a", mediaKind: "VIDEO", isVideo: true } }; break;
      case "DashboardOriginVersions": data = { originVersions: { nodes: originVersion.status === "DRAFT" && calls.filter((call) => call.operation === "InviteOriginCapture").length === 0 ? [] : [originVersion, ...(originVersion.status === "ACTIVE" ? [{ ...originVersion, id: "origin-version-old", versionNumber: 1, status: "SUPERSEDED", purpose: "MOVE_IN", hasInventory: true }] : [])], pageInfo } }; break;
      case "InviteOriginCapture": data = { inviteOriginCapture: { invitationId: "invitation-a", originVersionId: originVersion.id, status: "ACTIVE", userErrors: [], clientMutationId: "invite-a" } }; break;
      case "ActivateOriginVersion":
        originVersion = { ...originVersion, status: "ACTIVE", versionNumber: 2, selectable: true, activatedAt: "2030-05-10T12:00:00Z", purpose: "MOVE_IN" };
        data = { activateOriginVersion: { version: originVersion, userErrors: [], clientMutationId: "activate-a" } };
        break;
      case "Inspections": data = { inspections: { nodes: [], pageInfo } }; break;
      case "InspectionDetail": data = { inspection: { id: "inspection-a", assetId: "asset-a", participantId: "participant-a", projectId: null, stageId: null, source: "MANUAL", sourceReason: null, stateReason: null, status: "COMPLETED", mediaKind: "VIDEO", comparisonMode: "CHECKLIST_ONLY", referenceVersionId: null, referenceVersionNumber: null, referenceMediaKind: null, referenceVersionStatus: null, referenceIsActive: null, originPromotionStatus: null, originPromotionFailure: null, promotedOriginVersionId: null, promotedOriginVersionNumber: null, promotedOriginMediaKind: null, promotedOriginIsActive: null, evidenceCount: 1, dueAt: "2030-05-10T12:00:00Z", deadlineAt: "2030-05-11T12:00:00Z", reminderInstants: [], version: 1, assetName: "Apartamento QA", assetAddress: "Rua QA", assetExternalKey: "APT-1", participantName: "Ana QA" } }; break;
      case "Projects": data = { projects: { nodes: [], pageInfo } }; break;
      case "PlanInspection": data = { planInspection: { inspection: { id: "inspection-new", status: "PLANNED", version: 1, projectId: null, stageId: null }, schedule: null, project: null, userErrors: [], clientMutationId: "plan-a" } }; break;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data }) });
  });
  return calls;
}

test.describe("Dashboard origin management mock journey", () => {
  test.skip(process.env.INSPECTION_E2E_MOCKS !== "true", "run with INSPECTION_E2E_MOCKS=true and the local identity stack");

  test("invites and activates a video origin, then selects it for inspection planning", async ({ page }) => {
    const calls = await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections?inspectionId=inspection-a");

    await expect(page.getByText("Esta vistoria foi configurada sem referência de comparação.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Usar esta vistoria como origem" })).toBeVisible();
    await expect(page.getByText("Convide o responsável para enviar fotos")).toHaveCount(0);
    await page.getByRole("button", { name: "Origens do imóvel" }).click();
    const originsDialog = page.getByRole("dialog", { name: "Origens do imóvel" });
    await originsDialog.getByLabel("Responsável pela origem").selectOption("participant-a");
    await originsDialog.getByLabel("Revisor responsável").selectOption("membership-a");
    await originsDialog.getByRole("button", { name: "Convidar origem (vídeo)" }).click();
    await expect.poll(() => calls.find((call) => call.operation === "InviteOriginCapture")?.variables.input).toMatchObject({ reviewerMembershipId: "membership-a" });
    await expect(page.getByRole("status").filter({ hasText: "Convite de vídeo enviado" })).toBeVisible();
    await originsDialog.getByRole("button", { name: "Ativar", exact: true }).click();
    await expect(page.getByRole("status").filter({ hasText: "Origem ativada" })).toBeVisible();
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
    await form.getByLabel("Comparação").selectOption("FIXED_ORIGIN");
    await expect(form.getByRole("option", { name: /Versão 1.*anterior.*Vídeo/ })).toBeAttached();
    await form.getByLabel("Origem").selectOption("origin-version-old");
    await form.getByLabel("Vencimento").fill("2030-05-10T12:00");
    await form.getByLabel("Prazo final").fill("2030-05-11T12:00");
    await form.getByRole("button", { name: "Planejar vistoria" }).click();
    await expect.poll(() => calls.find((call) => call.operation === "PlanInspection")?.variables.input).toMatchObject({
      assetId: "asset-a", comparisonMode: "FIXED_ORIGIN", referenceVersionId: "origin-version-old",
    });
  });
});
