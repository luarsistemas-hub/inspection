import { expect, test, type Page, type Route } from "@playwright/test";
import { loginAsMockAdmin, openDashboardRoute } from "./support/auth";

type Call = { operation: string; variables: Record<string, unknown> };
const pageInfo = { hasNextPage: false, endCursor: null };

const attentionInspection = (status: string, kind: string, extra: Record<string, unknown> = {}) => ({
  id: "inspection-a", assetId: "asset-a", participantId: "participant-a", projectId: null, stageId: null, source: "MANUAL", sourceReason: null, stateReason: null, status,
  mediaKind: "VIDEO", comparisonMode: null, referenceVersionId: null, referenceVersionNumber: null, referenceMediaKind: null, referenceVersionStatus: null, referenceIsActive: null,
  originPromotionStatus: null, originPromotionFailure: null, promotedOriginVersionId: null, promotedOriginVersionNumber: null, promotedOriginMediaKind: null, promotedOriginIsActive: null,
  evidenceCount: 1, dueAt: "2030-05-10T12:00:00Z", deadlineAt: "2030-05-11T12:00:00Z", reminderInstants: [], version: 2,
  assetName: "Apartamento 101", assetAddress: "Rua QA, 10", assetExternalKey: "APT101", participantName: "Elvio QA",
  attention: { kind, mediaId: "media-a", replacementDeadlineAt: null, since: "2030-05-10T12:00:00Z", ...extra },
});

const review = { mediaId: "media-a", inspectionId: "inspection-a", assetName: "Apartamento 101", participantName: "Elvio QA", mediaKind: "VIDEO", requirementKey: "video:main", blockedAt: "2030-05-10T12:00:00Z", submitted: true, sourceAvailable: true };

function reply(operation: string, status: string, kind: string): { data: Record<string, unknown> } {
  const identity = { me: { identityId: "identity-a", tenantId: "tenant-a", productEntitlements: ["DASHBOARD"], roles: ["MANAGER"], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-a" }], memberships: [{ id: "membership-a", tenantId: "tenant-a", role: "MANAGER", status: "ACTIVE" }] }, tenant: { id: "tenant-a", name: "Operação QA", status: "ACTIVE" } };
  switch (operation) {
    case "DashboardMemberships":
    case "DashboardGate": return { data: identity };
    case "DashboardFormOptions": return { data: { assets: { nodes: [], pageInfo }, participants: { nodes: [], pageInfo }, templates: { nodes: [], pageInfo } } };
    case "DashboardReviewers": return { data: { memberships: { nodes: [], pageInfo } } };
    case "MyNotifications": return { data: { myNotifications: { nodes: [], unreadCount: 0, pageInfo } } };
    case "DashboardOriginVersions": return { data: { originVersions: { nodes: [], pageInfo } } };
    case "DashboardTemplateVersion": return { data: { templateVersion: { id: "template-version-a", mediaKind: "PHOTO", isVideo: false } } };
    case "Inspections": return { data: { inspections: { nodes: [attentionInspection(status, kind)], pageInfo } } };
    case "InspectionDetail": return { data: { inspection: attentionInspection(status, kind) } };
    case "SensitiveMediaReviews": return { data: { sensitiveMediaReviews: { nodes: [review], pageInfo } } };
    case "SensitiveMediaOriginal": return { data: { sensitiveMediaOriginal: { mediaId: "media-a", mediaKind: "VIDEO", url: "https://signed.invalid/original.mp4", expiresAt: "2030-05-10T12:05:00Z" } } };
    case "DeclareSensitiveDetectionFalsePositive": return { data: { declareSensitiveDetectionFalsePositive: { media: { id: "media-a", status: "SCREENED" }, userErrors: [], clientMutationId: "m" } } };
    case "RequestVideoReplacement": return { data: { requestVideoReplacement: { replacement: { id: "replacement-a", responsibilityId: "responsibility-b", status: "REQUESTED", deadlineAt: "2030-05-13T12:00:00Z" }, userErrors: [], clientMutationId: "m" } } };
    default: return { data: {} };
  }
}

async function installMocks(page: Page, status: string, kind: string) {
  const calls: Call[] = [];
  await page.route("**/graphql", async (route: Route) => {
    const body = route.request().postDataJSON() as { query: string; variables?: Record<string, unknown> };
    const operation = body.query.match(/(?:query|mutation)\s+(\w+)/)?.[1] ?? "unknown";
    calls.push({ operation, variables: body.variables ?? {} });
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(reply(operation, status, kind)) });
  });
  return calls;
}

test.describe("Dashboard pending review", () => {
  test.skip(process.env.INSPECTION_E2E_MOCKS !== "true", "run with INSPECTION_E2E_MOCKS=true and the local identity stack");

  test("the Pendências filter asks for the ATTENTION group and the detail explains the next action", async ({ page }) => {
    const calls = await installMocks(page, "REVIEW_REQUIRED", "PRIVACY_REVIEW");
    await loginAsMockAdmin(page, "/inspections");
    await page.getByRole("combobox", { name: "Situação" }).selectOption("pendencias");
    await expect.poll(() => calls.filter(({ operation }) => operation === "Inspections").at(-1)?.variables.statusGroup).toBe("ATTENTION");
    await expect(page.getByText("Revisão de privacidade necessária").first()).toBeVisible();
    await page.getByRole("button", { name: "Abrir detalhes da vistoria Apartamento 101" }).click();
    await expect(page.getByRole("heading", { name: "Revisão de privacidade necessária" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Abrir revisão" })).toHaveAttribute("href", /\/privacy-review\?mediaId=media-a/);
    await expect(page.getByText(/SENSITIVE_CONTENT|PREPARATION_FAILED/)).toHaveCount(0);
  });

  test("a manager requests a new recording from the inspection detail", async ({ page }) => {
    const calls = await installMocks(page, "REVIEW_REQUIRED", "PRIVACY_REVIEW");
    await loginAsMockAdmin(page, "/inspections");
    await page.getByRole("button", { name: "Abrir detalhes da vistoria Apartamento 101" }).click();
    await page.getByRole("button", { name: "Pedir nova gravação" }).click();
    await page.getByLabel("Motivo da nova gravação").fill("Gravar novamente sem pessoas");
    await page.getByRole("button", { name: "Enviar pedido" }).click();
    await expect(page.getByRole("dialog", { name: "Detalhe da vistoria" }).getByText("Nova gravação solicitada. O participante receberá um novo convite.", { exact: true }).first()).toBeVisible();
    const input = calls.filter(({ operation }) => operation === "RequestVideoReplacement").at(-1)?.variables.input as Record<string, unknown>;
    expect(input).toMatchObject({ inspectionId: "inspection-a", mediaId: "media-a", reason: "Gravar novamente sem pessoas" });
  });

  test("the privacy queue opens the original and releases a false positive with a reason", async ({ page }) => {
    const calls = await installMocks(page, "REVIEW_REQUIRED", "PRIVACY_REVIEW");
    await loginAsMockAdmin(page, "/inspections");
    await openDashboardRoute(page, "Revisão de privacidade");
    await expect(page.getByRole("heading", { name: "Revisão de privacidade", level: 2 })).toBeVisible();
    await expect(page.getByText("Apartamento 101")).toBeVisible();
    await page.getByRole("button", { name: "Ver original" }).click();
    await expect(page.locator("video")).toHaveAttribute("src", "https://signed.invalid/original.mp4");
    await page.keyboard.press("Escape");
    const release = page.getByRole("button", { name: "Liberar mídia (falso positivo)" });
    await expect(release).toBeDisabled();
    await page.getByLabel("Motivo da liberação").fill("Objeto confundido com documento");
    await release.click();
    await expect(page.getByText("Mídia liberada e análise retomada.")).toBeVisible();
    const input = calls.filter(({ operation }) => operation === "DeclareSensitiveDetectionFalsePositive").at(-1)?.variables.input as Record<string, unknown>;
    expect(input).toMatchObject({ mediaId: "media-a", reason: "Objeto confundido com documento" });
  });
});
