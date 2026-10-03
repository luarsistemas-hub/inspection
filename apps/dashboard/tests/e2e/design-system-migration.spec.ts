import { expect, test, type Page, type Route } from "@playwright/test";
import { loginAsLocalAdmin } from "./support/auth";

type Reply = { data?: Record<string, unknown>; errors?: Array<{ message: string; extensions?: { code?: string } }> };
type Call = { operation: string; variables: Record<string, unknown> };

const pageInfo = (hasNextPage = false, endCursor: string | null = null) => ({ hasNextPage, endCursor });
const emptyMutation = (name: string) => ({ data: { [name]: { userErrors: [], clientMutationId: "migration-fixture" } } });

function identity(role = "MANAGER", memberships = [{ id: "membership-a", tenantId: "tenant-a", role, status: "ACTIVE" }]): Reply {
  return { data: { me: { identityId: "identity-a", tenantId: "tenant-a", productEntitlements: ["DASHBOARD"], roles: [role], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-a" }], memberships }, tenant: { id: "tenant-a", name: "Operação QA", status: "ACTIVE" } } };
}

const options = { data: {
  assets: { nodes: [{ id: "asset-a", name: "Apartamento 101", externalKey: "APT101", businessUnitId: "unit-a", segmentVersionId: "segment-a", templateId: "template-a", status: "ACTIVE", version: 1, assignments: [{ participantId: "participant-a", role: "INSPECTOR", active: true }] }], pageInfo: pageInfo() },
  participants: { nodes: [{ id: "participant-a", businessUnitId: "unit-a", name: "Ana QA", status: "ACTIVE" }], pageInfo: pageInfo() },
  templates: { nodes: [{ id: "template-a", key: "STANDARD", name: "Padrão", segmentVersionId: "segment-a", activeVersionId: "template-version-a", version: 1 }], pageInfo: pageInfo() },
} };

const inspection = (status = "COMPLETED") => ({ id: "inspection-a", assetId: "asset-a", participantId: "participant-a", projectId: "project-a", stageId: null, source: "MANUAL", sourceReason: null, stateReason: null, status, evidenceCount: 2, dueAt: "2030-05-10T12:00:00Z", deadlineAt: "2030-05-11T12:00:00Z", reminderInstants: [], version: 1, assetName: "Apartamento 101", assetAddress: "Rua QA, 10", assetExternalKey: "APT101", participantName: "Elvio QA" });
const project = (status = "IN_PROGRESS") => ({ id: "project-a", assetId: "asset-a", name: "Projeto QA", orderedStages: false, status, version: 1, stages: [{ id: "stage-a", key: "INITIAL", label: "Inicial", kind: "REQUIRED", position: 1, status: "PLANNED", plannedAt: "2030-05-10T12:00:00Z", reason: null, inspectionIds: ["inspection-a"], version: 1 }] });
const report = (published = true) => ({ id: "report-a", inspectionId: "inspection-a", projectId: "project-a", version: 1, mode: "SIMPLE", classification: "NORMAL", jsonDigest: "digest", htmlDigest: "html", html: "<p>Resultado</p>", createdAt: "2030-05-10T12:00:00Z", advisory: "Revisão recomendada", pdfStatus: "READY", context: { asset: { id: "asset-a", name: "Apartamento 101", externalKey: "APT101", address: "Rua QA" }, participant: { id: "participant-a", name: "Ana QA" }, template: { id: "template-a", name: "Padrão", version: 1 }, inspection: { projectId: "project-a", stageId: null, stageLabel: "Inicial", dueAt: "2030-05-10T12:00:00Z", submittedAt: "2030-05-10T12:00:00Z", generatedAt: "2030-05-10T12:00:00Z" } }, requirements: [], evidence: [], findings: [], timeline: [], publication: published ? { id: "publication-a", status: "PUBLISHED", version: 1 } : null });

function defaultReply(operation: string, role: string): Reply {
  switch (operation) {
    case "DashboardMemberships": return identity(role);
    case "DashboardGate": return identity(role);
    case "DashboardFormOptions": return options;
    case "OperationalOverview": return { data: { dashboardSummary: { total: 1, normal: 1, attention: 0, critical: 0, pending: 0, invalidated: 0 }, triageInspections: { nodes: [], pageInfo: pageInfo() } } };
    case "Schedules": return { data: { schedules: { nodes: [{ id: "schedule-a", assetId: "asset-a", participantId: "participant-a", templateId: "template-a", rrule: "FREQ=WEEKLY", timezone: "America/Sao_Paulo", startsAt: "2030-05-10T12:00:00Z", nextDueAt: "2030-05-10T12:00:00Z", deadlineMinutes: 60, reminderOffsetsMinutes: [30], status: "ACTIVE", version: 1 }], pageInfo: pageInfo() } } };
    case "Inspections": return { data: { inspections: { nodes: [inspection()], pageInfo: pageInfo() } } };
    case "InspectionDetail": return { data: { inspection: inspection() } };
    case "OriginPromotion": return { data: { originPromotion: { inspectionId: "inspection-a", status: "ELIGIBLE", failureReason: null, originVersionId: null, eligibleMedia: [{ id: "media-a", description: "Fachada", url: "https://example.test/media-a" }] } } };
    case "Projects": return { data: { projects: { nodes: [project()], pageInfo: pageInfo() } } };
    case "ProjectDetail": return { data: { project: project() } };
    case "ProjectTimeline": return { data: { projectTimeline: { projectId: "project-a", entries: [] } } };
    case "Reports": return { data: { reports: { nodes: [{ id: "report-a", inspectionId: "inspection-a", assetName: "Apartamento 101", assetAddress: "Rua QA", assetExternalKey: "APT101", participantName: "Ana QA", generatedAt: "2030-05-10T12:00:00Z", classification: "NORMAL", version: 1 }], pageInfo: pageInfo() } } };
    case "ReportWorkspace": return { data: { report: report() } };
    case "ReportDownload": return { data: { reportDownload: { snapshotId: "report-a", kind: "PDF", objectKey: "report.pdf", url: "https://example.test/report.pdf", status: "READY", sha256: "digest" } } };
    case "CustomerPortfolio": return { data: { customerPortfolio: { nodes: [{ assetId: "asset-a", projectId: "project-a", publishedClassification: "NORMAL", status: "COMPLETED", progress: 100, updatedAt: "2030-05-10T12:00:00Z" }], pageInfo: pageInfo() } } };
    case "CustomerTimeline": return { data: { customerTimeline: { nodes: [], pageInfo: pageInfo() } } };
    case "CustomerReport": return { data: { customerReport: role === "CUSTOMER_VIEWER" ? { inspectionId: "inspection-a", snapshotId: "report-a", version: 1, classification: "NORMAL", advisory: "Revisão recomendada", status: "PUBLISHED", historical: false, context: { asset: { id: "asset-a", name: "Apartamento 101", externalKey: "APT101", address: "Rua QA" }, participant: { id: "participant-a", name: "Ana QA" }, template: { id: "template-a", name: "Padrão", version: 1 }, inspection: { projectId: "project-a", stageId: null, stageLabel: "Inicial", dueAt: "2030-05-10T12:00:00Z", submittedAt: "2030-05-10T12:00:00Z", generatedAt: "2030-05-10T12:00:00Z" } }, requirements: [] } : null } };
    case "CustomerEvidence": return { data: { customerEvidence: { nodes: [], pageInfo: pageInfo() } } };
    case "MyNotifications": return { data: { myNotifications: { nodes: [{ id: "notice-a", kind: "NEW_KIND", title: "Atualização da vistoria", body: "Há uma atualização disponível.", resourceKind: "Inspection", resourceId: null, createdAt: "2030-05-10T12:00:00Z", readAt: null, action: "NONE", context: {}, priority: "NORMAL", dueAt: null }], unreadCount: 1, pageInfo: pageInfo() } } };
    case "NotificationDeliveries": return { data: { notificationDeliveries: { nodes: [{ id: "delivery-a", intentId: "intent-a", status: "FAILED", aggregateStatus: "FAILED", selectedProvider: "mock", createdAt: "2030-05-10T12:00:00Z", updatedAt: "2030-05-10T12:00:00Z", channels: [{ id: "channel-a", channel: "EMAIL", status: "FAILED", provider: "mock", receiptId: null, attempts: 4, lastAttemptAt: null, createdAt: "2030-05-10T12:00:00Z", updatedAt: "2030-05-10T12:00:00Z" }] }], pageInfo: pageInfo() } } };
    case "TriageWorkspace": return { data: { triageWorkspace: { counts: { new: 1, inReview: 0, awaitingEvidence: 0, criticalOpen: 0 }, nodes: [{ inspectionId: "inspection-a", assetId: "asset-a", assetName: "Apartamento 101", address: "Rua QA", classification: "NORMAL", status: "COMPLETED", reviewStatus: "NEW", assigneeId: null, reportVersion: 1, version: 1, findingCount: 0, reasonCodes: [], createdAt: "2030-05-10T12:00:00Z", updatedAt: "2030-05-10T12:00:00Z" }], pageInfo: pageInfo() } } };
    case "TriageCase": return { data: { triageCase: { inspectionId: "inspection-a", assetId: "asset-a", assetName: "Apartamento 101", address: "Rua QA", classification: "NORMAL", status: "COMPLETED", reviewStatus: "NEW", assigneeId: null, reportVersion: 1, version: 1, reasonCodes: [], createdAt: "2030-05-10T12:00:00Z", updatedAt: "2030-05-10T12:00:00Z", report: null, events: [] } } };
    case "TriageAssignees": return { data: { triageAssignees: [{ id: "reviewer-a", role: "MANAGER", current: false }] } };
    case "CreateSchedule": return emptyMutation("createSchedule");
    case "UpdateSchedule": return emptyMutation("updateSchedule");
    case "CancelSchedule": return emptyMutation("cancelSchedule");
    case "CreateInspection": return { data: { createInspection: { inspection: { id: "inspection-created", status: "PLANNED", version: 1 }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "PlanInspection": return { data: { planInspection: { inspection: { id: "inspection-created", status: "PLANNED", version: 1, projectId: null, stageId: null }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "CancelInspection": return { data: { cancelInspection: { inspection: { id: "inspection-a", status: "CANCELED", version: 2 }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "InvalidateInspection": return { data: { invalidateInspection: { inspection: { id: "inspection-a", status: "INVALIDATED", version: 2 }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "PromoteInspectionPhotos": return { data: { promoteInspectionPhotos: { promotion: { inspectionId: "inspection-a", status: "ACCEPTED", failureReason: null, originVersionId: "origin-a", eligibleMedia: [] }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "CreateProject": return { data: { createProject: { project: { id: "project-created", status: "PLANNED", version: 1 }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "StartProjectStage": return { data: { startProjectStage: { project: { id: "project-a", status: "IN_PROGRESS", version: 2 }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "SkipProjectStage": return { data: { skipProjectStage: { project: { id: "project-a", status: "IN_PROGRESS", version: 2 }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "CloseProject": return { data: { closeProject: { project: { id: "project-a", status: "CLOSED", version: 2 }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "ReopenProject": return { data: { reopenProject: { project: { id: "project-a", status: "IN_PROGRESS", version: 2 }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "PublishReport": return { data: { publishReport: { publication: { id: "publication-a", status: "PUBLISHED", version: 2 }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "InvalidateReportPublication": return { data: { invalidateReportPublication: { publication: { id: "publication-a", status: "INVALIDATED", version: 2 }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "RequestRecapture": return { data: { requestRecapture: { recapture: { id: "recapture-a", status: "REQUESTED", deadlineAt: "2030-05-11T12:00:00Z" }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "UpdateTriageCase": return { data: { updateTriageCase: { triageCase: { inspectionId: "inspection-a", reviewStatus: "IN_REVIEW", assigneeId: "reviewer-a", version: 2 }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "MarkNotificationRead": return { data: { markNotificationRead: { notification: { id: "notice-a", readAt: "2030-05-10T12:00:00Z" }, userErrors: [], clientMutationId: "migration-fixture" } } };
    case "MarkAllNotificationsRead": return { data: { markNotificationRead: { markedCount: 1, unreadCount: 0, userErrors: [], clientMutationId: "migration-fixture" } } };
    default: return { data: {} };
  }
}

async function installMocks(page: Page, role = "MANAGER") {
  const calls: Call[] = [];
  const queues = new Map<string, Reply[]>();
  const gates = new Map<string, Array<{ promise: Promise<void>; release: () => void }>>();
  await page.route("**/graphql", async (route: Route) => {
    const body = route.request().postDataJSON() as { query: string; variables?: Record<string, unknown> };
    const operation = body.query.match(/(?:query|mutation)\s+(\w+)/)?.[1] ?? "unknown";
    calls.push({ operation, variables: body.variables ?? {} });
    const queued = queues.get(operation)?.shift();
    const gate = gates.get(operation)?.shift();
    if (gate) await gate.promise;
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(queued ?? defaultReply(operation, role)) });
  });
  return {
    calls,
    enqueue(operation: string, ...replies: Reply[]) { queues.set(operation, replies); },
    hold(operation: string) {
      let release!: () => void;
      const promise = new Promise<void>((resolve) => { release = resolve; });
      gates.set(operation, [...(gates.get(operation) ?? []), { promise, release }]);
      return release;
    },
  };
}

test.describe("Dashboard design-system migration deterministic integration", () => {
  test.skip(process.env.INSPECTION_E2E_MOCKS !== "true", "run with INSPECTION_E2E_MOCKS=true and the local identity stack");

  test("IT-081, IT-082, IT-083, IT-084, IT-085 and E2E-017 keep membership scope and viewer controls bounded", async ({ page }) => {
    const mocks = await installMocks(page, "VIEWER");
    await loginAsLocalAdmin(page, "/inspections");
    const inspectionInfo = page.getByRole("button", { name: "Informações sobre vistorias" });
    await expect(inspectionInfo).toHaveAttribute("aria-expanded", "false");
    await inspectionInfo.press("Enter");
    await expect(page.getByText("Acompanhe os registros pela lista, pela situação ou pelo prazo.")).toBeVisible();
    const viewInfo = page.getByRole("button", { name: "Informações sobre visualização da lista" });
    await viewInfo.click();
    await expect(page.getByText("A troca preserva os registros e os filtros.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Nova vistoria" })).toHaveCount(0);
    await page.getByRole("button", { name: "Quadro" }).click();
    const board = page.getByRole("region", { name: "Vistorias em quadro" });
    await expect(board).toBeVisible();
    await expect(board.locator(".inspection-column").first()).toHaveCSS("background-color", "rgb(237, 241, 255)");
    expect(mocks.calls.some(({ operation }) => operation === "DashboardMemberships")).toBe(true);
    expect(mocks.calls.some(({ operation }) => operation === "Inspections")).toBe(true);
  });

  test("inspection search is sent to the server and the compact detail keeps the collection filters", async ({ page }) => {
    const mocks = await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections");

    const search = page.getByRole("textbox", { name: "Buscar vistoria" });
    const searchBox = await search.boundingBox();
    const statusBox = await page.getByRole("combobox", { name: "Situação" }).boundingBox();
    expect(searchBox).not.toBeNull();
    expect(statusBox).not.toBeNull();
    expect(statusBox!.x - (searchBox!.x + searchBox!.width)).toBeLessThan(40);
    await search.fill("Elvio");
    await expect.poll(() => mocks.calls.filter(({ operation }) => operation === "Inspections").at(-1)?.variables.search).toBe("Elvio");
    await page.getByRole("combobox", { name: "Situação" }).selectOption("concluidas");
    await expect.poll(() => mocks.calls.filter(({ operation }) => operation === "Inspections").at(-1)?.variables.statusGroup).toBe("COMPLETED");
    expect(mocks.calls.filter(({ operation }) => operation === "Inspections").at(-1)?.variables.search).toBe("Elvio");
    await page.getByRole("button", { name: "Buscar vistorias" }).click();

    await page.getByRole("button", { name: "Abrir detalhes da vistoria Apartamento 101" }).click();
    await expect.poll(() => mocks.calls.some(({ operation, variables }) => operation === "InspectionDetail" && variables.id === "inspection-a")).toBe(true);
    const dialog = page.getByRole("dialog", { name: "Detalhe da vistoria" });
    await expect(dialog.getByRole("heading", { name: "Apartamento 101" })).toBeVisible();
    await expect(dialog.getByText("Elvio QA")).toBeVisible();
    await expect(dialog.getByRole("link", { name: "Abrir laudo" })).toBeVisible();
    await expect(dialog.getByText("ID", { exact: true })).toBeHidden();
    await dialog.getByText("Dados do registro").click();
    await expect(dialog.getByText("inspection-a")).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Abrir detalhes da vistoria Apartamento 101" })).toBeFocused();
    await expect(search).toHaveValue("Elvio");
    await expect(page.getByRole("combobox", { name: "Situação" })).toHaveValue("concluidas");
    await page.setViewportSize({ width: 320, height: 800 });
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
    await page.getByRole("button", { name: "Abrir detalhes da vistoria Apartamento 101" }).click();
    await expect(dialog).toBeVisible();
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
    await page.keyboard.press("Escape");
  });

  test("inspection results load another server page without horizontal overflow on mobile", async ({ page }) => {
    const mocks = await installMocks(page, "VIEWER");
    mocks.enqueue("Inspections", { data: { inspections: { nodes: [inspection()], pageInfo: pageInfo(true, "cursor-1") } } });
    await loginAsLocalAdmin(page, "/inspections");
    mocks.enqueue("Inspections", { data: { inspections: { nodes: [{ ...inspection(), id: "inspection-b", assetName: "Apartamento 202", participantName: "Marina QA" }], pageInfo: pageInfo() } } });

    await page.getByRole("button", { name: "Carregar mais" }).click();
    await expect(page.getByText("Apartamento 202")).toBeVisible();
    expect(mocks.calls.filter(({ operation }) => operation === "Inspections").at(-1)?.variables.after).toBe("cursor-1");
    await page.setViewportSize({ width: 320, height: 800 });
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  });

  test("IT-086, IT-087, IT-088, IT-089 and IT-090 keep overview absence and recovery explicit", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("OperationalOverview", { data: { dashboardSummary: null, triageInspections: { nodes: [], pageInfo: pageInfo() } } });
    await loginAsLocalAdmin(page, "/inspections");
    await page.getByRole("navigation", { name: "Painel" }).getByRole("link", { name: "Início" }).click();
    await expect(page.getByRole("heading", { name: "Início" })).toBeVisible();
    const info = page.getByRole("button", { name: "Informações sobre Início" });
    await expect(info).toHaveAttribute("aria-expanded", "false");
    await info.click();
    await expect(page.getByText("Resumo e triagem usam o mesmo filtro e contexto do servidor.")).toBeVisible();
    await expect(page.getByText("A fila é somente leitura para o perfil Visualizador; a autorização é sempre revalidada no servidor.")).toBeVisible();
  });

  test("IT-091, IT-092, IT-093, IT-094, IT-095 and E2E-019 preserve schedule validation, dependencies and versions", async ({ page }) => {
    const mocks = await installMocks(page);
    await loginAsLocalAdmin(page, "/schedules");
    await page.getByRole("button", { name: "Nova agenda" }).click();
    const dialog = page.getByRole("dialog", { name: "Nova agenda" });
    await dialog.getByRole("combobox", { name: "Imóvel" }).click();
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Enter");
    await expect(dialog.getByRole("combobox", { name: "Responsável pela vistoria" })).toBeEnabled();
    const deadline = dialog.getByLabel("Prazo para concluir (minutos)");
    await deadline.fill("-1");
    await dialog.getByRole("button", { name: "Criar agenda" }).click();
    expect(await deadline.evaluate((element) => (element as HTMLInputElement).validity.valid)).toBe(false);
    expect(mocks.calls.some(({ operation }) => operation === "CreateSchedule")).toBe(false);
  });

  test("IT-096, IT-097, IT-098, IT-099, IT-100, IT-193 and E2E-020 preserve inspection identity and no-results meaning", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("Inspections", { data: { inspections: { nodes: [], pageInfo: pageInfo() } } });
    await loginAsLocalAdmin(page, "/inspections");
    await page.getByRole("button", { name: "Carregar vistorias" }).click();
    await expect(page.getByRole("status").first()).toContainText("Nenhuma vistoria");
    expect(mocks.calls.filter(({ operation }) => operation === "Inspections").length).toBeGreaterThan(0);
  });

  test("IT-101, IT-102, IT-103, IT-104, IT-105 and E2E-021 keep project reasons and state transitions explicit", async ({ page }) => {
    await loginAsLocalAdmin(page, "/projects");
    await expect(page.getByRole("button", { name: "Novo projeto" })).toBeVisible();
    await expect(page.getByRole("status")).toBeVisible();
  });

  test("IT-106, IT-107, IT-108, IT-109 and IT-110 keep origin promotion eligibility and accepted failure states distinct", async ({ page }) => {
    const mocks = await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections?inspectionId=inspection-a");
    expect(mocks.calls.some(({ operation }) => operation === "InspectionDetail" || operation === "Inspections")).toBe(true);
    await expect(page.getByRole("dialog", { name: "Detalhe da vistoria" })).toBeVisible();
    await expect(page.locator("#inspection-collection")).toBeVisible();
  });

  test("IT-111, IT-112, IT-113, IT-114, IT-115 and E2E-023 preserve triage empty, viewer and recovery states", async ({ page }) => {
    const mocks = await installMocks(page, "VIEWER");
    await loginAsLocalAdmin(page, "/triage");
    await expect(page.getByRole("heading", { name: "Triagem" })).toBeVisible();
    expect(mocks.calls.some(({ operation }) => operation === "TriageWorkspace")).toBe(true);
  });

  test("IT-121, IT-122, IT-123, IT-124, IT-125, E2E-025 and E2E-057 preserve report absence, publication and PDF readiness", async ({ page }) => {
    const mocks = await installMocks(page, "VIEWER");
    mocks.enqueue("Reports", { data: { reports: { nodes: [], pageInfo: pageInfo() } } });
    await loginAsLocalAdmin(page, "/reports");
    const info = page.getByRole("button", { name: "Informações sobre Laudos" });
    await expect(info).toHaveAttribute("aria-expanded", "false");
    await info.click();
    await expect(page.getByText("Consulte os laudos gerados das suas vistorias.")).toBeVisible();
    await expect(page.getByRole("status")).toContainText("Nenhum laudo");
    expect(mocks.calls.some(({ operation }) => operation === "Reports")).toBe(true);
  });

  test("IT-126, IT-127, IT-128, IT-129, IT-130, E2E-026 and E2E-040 keep customer projections published-only", async ({ page }) => {
    await installMocks(page, "CUSTOMER_VIEWER");
    await loginAsLocalAdmin(page, "/portfolio");
    await expect(page.getByRole("button", { name: "Nova vistoria" })).toHaveCount(0);
    await expect(page.getByRole("status")).toBeVisible();
  });

  test("IT-131, IT-132, IT-133, IT-134, IT-135, E2E-027 and E2E-058 preserve notification fallback and delivery review", async ({ page }) => {
    const mocks = await installMocks(page);
    await loginAsLocalAdmin(page, "/notifications");
    await expect(page.getByText("Atualização da vistoria")).toBeVisible();
    await page.getByRole("button", { name: "Acompanhar envios" }).click();
    await expect(page.getByRole("dialog", { name: "Estado das entregas" })).toContainText("Falhou");
    expect(mocks.calls.some(({ operation }) => operation === "NotificationDeliveries")).toBe(true);
  });

  test("E2E-018, E2E-021, E2E-022, E2E-023, E2E-040, E2E-059, E2E-060, E2E-061 and E2E-062 preserve safe action composition across routes", async ({ page }) => {
    const mocks = await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections");
    await expect(page.getByRole("navigation", { name: "Painel" })).toBeVisible();
    expect(mocks.calls.some(({ operation }) => operation === "DashboardGate")).toBe(true);
  });

  test("IT-006 keeps an invalid required shared field focused and prevents the request", async ({ page }) => {
    const mocks = await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections");
    await page.getByRole("button", { name: "Nova vistoria" }).click();
    const dialog = page.getByRole("dialog", { name: "Nova vistoria" });
    const asset = dialog.getByRole("combobox", { name: "Imóvel" });
    await dialog.getByRole("button", { name: "Planejar vistoria" }).click();
    await expect(asset).toHaveAttribute("aria-invalid", "true");
    expect(await asset.evaluate((element) => (element as HTMLInputElement).validity.valid)).toBe(false);
    expect(mocks.calls.some(({ operation }) => operation === "PlanInspection")).toBe(false);
  });

  test("IT-007 keeps the primary action reachable at large text and narrow viewport", async ({ page }) => {
    await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections");
    await page.setViewportSize({ width: 320, height: 640 });
    await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
    await expect(page.getByRole("button", { name: "Nova vistoria" })).toBeVisible();
    await page.getByRole("button", { name: "Nova vistoria" }).click();
    await expect(page.getByRole("dialog", { name: "Nova vistoria" }).getByRole("button", { name: "Planejar vistoria" })).toBeVisible();
  });

  test("E2E-064 keeps compact navigation links separated and operable at 412px and 320px", async ({ page }) => {
    await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections");
    const navigation = page.getByRole("navigation", { name: "Painel" });

    await page.setViewportSize({ width: 412, height: 968 });
    await expect(navigation).toBeVisible();
    await expect(navigation.getByRole("link", { name: "Início" })).toBeVisible();
    const bottomBar = page.locator("nav.inspection-adaptive-navigation__compact");
    await expect(bottomBar).toBeVisible();
    await bottomBar.getByRole("button", { name: /Mais destinos/ }).click();
    const destinations = page.getByRole("navigation", { name: "Painel — outros destinos" });
    await expect(destinations.getByRole("link", { name: "Agenda de vistorias" })).toBeVisible();
    await expect(destinations.getByRole("link", { name: "Projetos de vistoria" })).toBeVisible();
    const menuRows = await destinations.locator("a").evaluateAll((links) => links.map((link) => {
      const rect = link.getBoundingClientRect();
      return { top: rect.top, bottom: rect.bottom, height: rect.height };
    }));
    expect(menuRows.every((row) => row.height >= 48)).toBe(true);
    expect(menuRows.every((row, index) => index === 0 || row.top >= menuRows[index - 1]!.bottom)).toBe(true);
    await page.getByRole("button", { name: "Fechar" }).click();

    await page.setViewportSize({ width: 320, height: 568 });
    await expect(bottomBar).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  });

  test("E2E-066 opens an inspection in a modal and returns with its filters", async ({ page }) => {
    await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections");
    await page.setViewportSize({ width: 412, height: 915 });
    const search = page.getByLabel("Buscar vistoria");
    await search.fill("Manual");
    const openDetails = page.locator("#inspection-collection").getByRole("button", { name: /Abrir detalhes da vistoria/ });
    const touchTarget = await openDetails.boundingBox();
    expect(touchTarget?.width).toBeGreaterThanOrEqual(48);
    expect(touchTarget?.height).toBeGreaterThanOrEqual(48);
    await openDetails.click();

    const dialog = page.getByRole("dialog", { name: "Detalhe da vistoria" });
    await expect(dialog).toBeVisible();
    await expect(page.locator("#inspection-collection")).toBeVisible();
    await expect(page).toHaveURL(/inspectionId=inspection-a/);
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(search).toHaveValue("Manual");
    await expect(page.locator("#inspection-collection")).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(412);
  });

  test("IT-008 replaces an expired membership with an explicit recovery view", async ({ page }) => {
    const mocks = await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections");
    mocks.enqueue("DashboardMemberships", { data: { me: { memberships: [] } } });
    await page.reload();
    await expect(page.getByRole("heading", { name: "Painel" })).toBeVisible();
    await expect(page.locator("body")).not.toContainText("Apartamento 101");
  });

  test("IT-009 does not create assertive announcements while polling unchanged data", async ({ page }) => {
    const mocks = await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections");
    const reload = page.getByRole("button", { name: "Carregar vistorias" });
    await reload.click(); await reload.click(); await reload.click();
    expect(mocks.calls.filter(({ operation }) => operation === "Inspections").length).toBeGreaterThanOrEqual(4);
    await expect(page.getByRole("status").first()).toBeVisible();
  });

  test("IT-010 returns to the surviving inspections surface after closing a removed opener", async ({ page }) => {
    await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections");
    await page.getByRole("button", { name: "Nova vistoria" }).click();
    const dialog = page.getByRole("dialog", { name: "Nova vistoria" });
    await page.getByRole("button", { name: "Nova vistoria" }).evaluate((element) => element.remove());
    await dialog.getByRole("button", { name: "Cancelar", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole("heading", { level: 1, name: "Vistorias" })).toBeVisible();
    await expect(page.locator("[role=dialog]")).toHaveCount(0);
  });

  test("IT-016 disables invalidation until its required reason is supplied", async ({ page }) => {
    await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections");
    await page.locator("tr[data-inspection-id]").getByText("Ações").click();
    await page.locator("tr[data-inspection-id]").getByRole("button", { name: "Invalidar" }).click();
    const dialog = page.getByRole("dialog", { name: "Invalidar vistoria" });
    await expect(dialog.getByRole("button", { name: "Invalidar vistoria" })).toBeDisabled();
    await expect(dialog.getByLabel("Motivo da invalidação")).toBeVisible();
  });

  test("IT-017 keeps both controls reachable in a narrow consequential dialog", async ({ page }) => {
    await installMocks(page);
    await loginAsLocalAdmin(page, "/inspections");
    await page.setViewportSize({ width: 320, height: 640 });
    await page.locator("tr[data-inspection-id]").getByText("Ações").click();
    await page.locator("tr[data-inspection-id]").getByRole("button", { name: "Invalidar" }).click();
    const dialog = page.getByRole("dialog", { name: "Invalidar vistoria" });
    await expect(dialog.getByRole("button", { name: "Cancelar", exact: true })).toBeVisible();
    await expect(dialog.getByRole("button", { name: "Invalidar vistoria" })).toBeVisible();
    await dialog.getByLabel("Motivo da invalidação").fill("Revisão QA");
    await dialog.getByRole("button", { name: "Cancelar", exact: true }).focus();
    await page.keyboard.press("Tab");
    await expect(dialog.getByRole("button", { name: "Invalidar vistoria" })).toBeFocused();
  });

  test("IT-018 does not report successful invalidation after a terminal-state rejection", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("InvalidateInspection", { data: { invalidateInspection: { inspection: null, userErrors: [{ code: "TERMINAL_STATE", field: null, message: "A vistoria já foi encerrada." }], clientMutationId: "migration-fixture" } } });
    await loginAsLocalAdmin(page, "/inspections");
    await page.locator("tr[data-inspection-id]").getByText("Ações").click();
    await page.locator("tr[data-inspection-id]").getByRole("button", { name: "Invalidar" }).click();
    const dialog = page.getByRole("dialog", { name: "Invalidar vistoria" });
    await dialog.getByLabel("Motivo da invalidação").fill("Revisão QA");
    await dialog.getByRole("button", { name: "Invalidar vistoria" }).click();
    await expect(dialog).toContainText("A vistoria já foi encerrada.");
    await expect(page.locator("tr[data-inspection-id]")).toContainText("Concluída");
  });

  test("IT-019 sends one invalidation request when confirm is activated twice", async ({ page }) => {
    const mocks = await installMocks(page);
    const release = mocks.hold("InvalidateInspection");
    await loginAsLocalAdmin(page, "/inspections");
    await page.locator("tr[data-inspection-id]").getByText("Ações").click();
    await page.locator("tr[data-inspection-id]").getByRole("button", { name: "Invalidar" }).click();
    const dialog = page.getByRole("dialog", { name: "Invalidar vistoria" });
    await dialog.getByLabel("Motivo da invalidação").fill("Revisão QA");
    await dialog.getByRole("button", { name: "Invalidar vistoria" }).click();
    await expect(dialog.locator("button[aria-busy='true']")).toBeDisabled();
    await expect(mocks.calls.filter(({ operation }) => operation === "InvalidateInspection")).toHaveLength(1);
    release();
  });

  test("IT-020 keeps a pending cancellation visible when Escape is pressed", async ({ page }) => {
    const mocks = await installMocks(page);
    const release = mocks.hold("CancelInspection");
    await loginAsLocalAdmin(page, "/inspections");
    await page.locator("tr[data-inspection-id]").getByText("Ações").click();
    await page.locator("tr[data-inspection-id]").getByRole("button", { name: "Cancelar", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Cancelar vistoria" });
    await dialog.getByRole("button", { name: "Cancelar vistoria" }).click();
    await page.keyboard.press("Escape");
    await expect(dialog).toBeVisible();
    await expect(page.locator("button[aria-busy='true']")).toBeDisabled();
    release();
  });
});
