import { expect, test } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin as loginAsLocalAdminDashboard, loginAsLocalAdminPortal, loginAsMockAdmin } from "./support/auth";

test.describe("CLD E2E-001 · confirmação de escopo", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "execute com a stack local e o seed de QA");
  test("Admin publica, Dashboard confirma e Capture mostra exatamente o escopo fixado", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    const unique = Date.now(); const templateName = `Escopo QA ${unique}`; const templateKey = `qa-scope-${unique}`;
    const admin = await page.context().newPage();
    await loginAsLocalAdminPortal(admin, "http://localhost:3000/templates");
    await admin.getByRole("button", { name: "Novo modelo" }).click();
    const editor = admin.locator(".admin-template-editor form");
    await editor.getByLabel("Chave do modelo").fill(templateKey);
    await editor.getByLabel("Nome do modelo").fill(templateName);
    await editor.getByRole("button", { name: "Adicionar ambiente esperado" }).click();
    const room = editor.locator("fieldset").filter({ has: editor.getByLabel("Chave do ambiente") }).last();
    await room.getByLabel("Chave do ambiente").fill("kitchen");
    await room.getByLabel("Nome do ambiente").fill("Cozinha");
    await room.getByRole("button", { name: /Adicionar componente a Cozinha/ }).click();
    await room.getByLabel("Chave do componente").fill("piso");
    await room.getByLabel("Componente esperado").fill("Piso");
    await room.getByRole("button", { name: /Adicionar componente a Cozinha/ }).click();
    const cabinet = room.getByLabel("Chave do componente").nth(1);
    await cabinet.fill("armarios");
    await room.getByLabel("Componente esperado").nth(1).fill("Armários");
    const publish = admin.waitForRequest((request) => request.url().endsWith("/graphql") && request.postData()?.includes("mutation AdminPublishTemplateVersion") === true);
    await editor.getByRole("button", { name: "Publicar nova versão do modelo" }).click();
    expect((await publish).postDataJSON().variables.input.definition.schemaVersion).toBe(3);
    await expect(admin.getByText(/Modelo publicado e ativado/)).toBeVisible();
    await admin.close();

    await loginAsLocalAdminDashboard(page, "/inspections");
    const oldMessagesResponse = await page.request.get("http://localhost:8026/api/v1/messages?limit=100");
    const oldMessages = await oldMessagesResponse.json() as { messages: Array<{ ID: string }> };
    const oldIds = new Set(oldMessages.messages.map((item) => item.ID));
    await page.getByRole("button", { name: "Nova vistoria" }).click();
    const dialog = page.getByRole("dialog", { name: "Nova vistoria" });
    const form = dialog.locator("form");
    const chooseFirst = async (label: string) => {
      const field = form.getByRole("combobox", { name: label });
      await expect(field).toBeEnabled();
      await expect(async () => { await field.scrollIntoViewIfNeeded(); await field.click(); await page.keyboard.press("ArrowDown"); await page.keyboard.press("Enter"); await expect(field).not.toHaveValue(""); }).toPass({ timeout: 10000 });
    };
    await chooseFirst("Imóvel"); await chooseFirst("Responsável pela vistoria");
    const model = form.getByRole("combobox", { name: "Modelo de vistoria" });
    await expect(async () => {
      await model.scrollIntoViewIfNeeded(); await model.click();
      const option = page.getByRole("option", { name: new RegExp(templateName) });
      await expect(option).toBeVisible({ timeout: 1000 }); await option.click();
      await expect(model).not.toHaveValue("");
    }).toPass({ timeout: 10000 });
    const scope = dialog.getByRole("group", { name: /Escopo desta vistoria/ });
    await scope.getByLabel("Perfil de bens").selectOption("FURNISHED_PROPERTY");
    await scope.getByLabel("Armários").uncheck();
    await dialog.getByLabel("Vencimento").fill("2027-01-15T10:00");
    await dialog.getByLabel("Prazo final").fill("2027-01-16T10:00");
    const create = page.waitForRequest((request) => request.url().endsWith("/graphql") && request.postData()?.includes("mutation CreateInspection") === true);
    await dialog.getByRole("button", { name: "Planejar vistoria" }).click();
    const input = (await create).postDataJSON().variables.input;
    expect(input.scopeOverride.profile).toBe("FURNISHED_PROPERTY");
    expect(input.scopeOverride.selections).toEqual([expect.objectContaining({ roomKey: "kitchen", componentKey: "piso" })]);

    let captureURL = ""; let invitationID = "";
    await expect.poll(async () => {
      const response = await page.request.get("http://localhost:8026/api/v1/messages?limit=100");
      if (!response.ok()) return "";
      const list = await response.json() as { messages: Array<{ ID: string; Created: string }> };
      const message = list.messages.filter((item) => !oldIds.has(item.ID)).sort((a, b) => Date.parse(b.Created) - Date.parse(a.Created))[0];
      if (!message) return "";
      invitationID = message.ID;
      const detailResponse = await page.request.get(`http://localhost:8026/api/v1/message/${message.ID}`);
      const detail = await detailResponse.json() as { Text?: string; HTML?: string };
      captureURL = `${detail.Text ?? ""} ${detail.HTML ?? ""}`.match(/https?:\/\/[^\s<>"']*localhost:[0-9]+\/[^\s<>"']+/)?.[0]?.replace(/&amp;/g, "&") ?? "";
      return captureURL;
    }, { timeout: 90000 }).toMatch(/^https?:\/\//);
    const capture = await page.context().newPage();
    const url = new URL(captureURL); url.host = "localhost:3003";
    await capture.goto(url.toString());
    await expect(capture.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
    const priorOtp = new Set([invitationID]);
    let otp = "";
    await expect.poll(async () => {
      const response = await capture.request.get("http://localhost:8026/api/v1/messages?limit=100");
      if (!response.ok()) return "";
      const list = await response.json() as { messages: Array<{ ID: string; Subject: string }> };
      const message = list.messages.find((item) => !priorOtp.has(item.ID) && item.Subject === "Seu código de acesso");
      if (!message) return "";
      const detailResponse = await capture.request.get(`http://localhost:8026/api/v1/message/${message.ID}`);
      const detail = await detailResponse.json() as { Text?: string };
      otp = detail.Text?.match(/Código:\s*(\d{6})/)?.[1] ?? "";
      return otp;
    }, { timeout: 60000 }).toMatch(/^\d{6}$/);
    await capture.getByLabel("Código de seis dígitos").fill(otp);
    await capture.getByRole("button", { name: "Confirmar código" }).click();
    await capture.getByLabel("Processamento das fotos").check();
    await capture.getByLabel("Análise por inteligência artificial").check();
    await capture.getByLabel("Localização quando necessária").check();
    await capture.getByRole("button", { name: "Aceitar e continuar" }).click();
    await expect(capture.getByRole("heading", { name: "Componentes esperados" })).toBeVisible();
    await expect(capture.getByText("Piso", { exact: true })).toBeVisible();
    await expect(capture.getByText("Armários", { exact: true })).toHaveCount(0);
    await capture.close();
    await assertRuntimeClean();
  });
});

test.fixme("E2E-002 Dashboard mostra a lacuna e a declaração de chaves da captura", async () => {
  // Valores de handover no relatório pertencem à projeção da tarefa 7. Não consultar campos GraphQL que ainda não existem.
});

test("E2E-002 Dashboard mostra a contagem da lacuna e as declarações pendentes da cobertura", async ({ page }) => {
  const scope = { profile: "FURNISHED_PROPERTY", version: 2, digest: "digest-2", rooms: [{ key: "kitchen", typeCode: "KITCHEN", name: "Cozinha", components: [{ key: "floor", typeCode: "FLOOR", label: "Piso", views: ["geral"] }] }], handoverRecords: [{ key: "keys", kind: "KEY", required: true }] };
  await page.route("**/graphql", async (route) => {
    const request = route.request().postDataJSON() as { query: string };
    const operationName = request.query.match(/(?:query|mutation)\s+(\w+)/)?.[1];
    const data: Record<string, unknown> = {};
    switch (operationName) {
      case "DashboardGate": data.me = { identityId: "identity-a", tenantId: "tenant-a", audience: "internal", product: "DASHBOARD", productEntitlements: ["DASHBOARD"], roles: ["MANAGER"], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-a" }], memberships: [{ id: "membership-a", tenantId: "tenant-a", name: "Gestora QA", email: "qa@example.test", role: "MANAGER", status: "ACTIVE", scopes: [] }] }; data.tenant = { id: "tenant-a", name: "Operação QA", status: "ACTIVE" }; break;
      case "DashboardMemberships": data.me = { identityId: "identity-a", tenantId: "tenant-a", audience: "internal", product: "DASHBOARD", productEntitlements: ["DASHBOARD"], roles: ["MANAGER"], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-a" }], memberships: [{ id: "membership-a", tenantId: "tenant-a", name: "Gestora QA", email: "qa@example.test", role: "MANAGER", status: "ACTIVE", scopes: [] }] }; break;
      case "Inspections": data.inspections = { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } }; break;
      case "InspectionDetail": data.inspection = { id: "inspection-a", assetId: "asset-a", participantId: "participant-a", projectId: null, stageId: null, source: "MANUAL", sourceReason: null, stateReason: null, status: "PLANNED", mediaKind: "PHOTO", comparisonMode: "CHECKLIST_ONLY", referenceVersionId: null, referenceVersionNumber: null, referenceMediaKind: null, referenceVersionStatus: null, referenceIsActive: null, scope, evidenceCount: 1, dueAt: "2026-11-04T12:00:00Z", deadlineAt: "2026-11-05T12:00:00Z", reminderInstants: [], version: 1, assetName: "Apartamento QA", assetAddress: "Rua QA", assetExternalKey: "QA-1", participantName: "Ana QA", attention: null }; break;
      case "InspectionCoverage": data.inspectionCoverage = { expected: 1, fullyAssessed: 0, partiallyAssessed: 0, notObserved: 1, notApplicable: 0, impossible: 0, unknownScope: false, pendingRecords: ["keys"], suggestions: [] }; break;
      case "DashboardAudioNotes": data.audioNotes = { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } }; break;
      case "InspectionOriginPromotion": data.inspectionOriginPromotion = { inspectionId: "inspection-a", eligible: false, impediments: [], activeOrigin: null, conflictingDraft: null, automaticCatalogSkipped: false, confirmationDigest: null, currentPromotion: null, generatedVersionActive: false }; break;
      case "DashboardOriginVersions": data.originVersions = { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } }; break;
      case "DashboardFormOptions": data.assets = { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } }; data.participants = { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } }; data.templates = { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } }; break;
      case "DashboardReviewers": data.memberships = { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } }; break;
      case "MyNotifications": data.myNotifications = { nodes: [], unreadCount: 0, pageInfo: { endCursor: null, hasNextPage: false } }; break;
      default: break;
    }
    await route.fulfill({ json: { data } });
  });
  await loginAsMockAdmin(page, "/inspections?inspectionId=inspection-a");
  await expect(page.getByRole("heading", { name: "Escopo aplicado" })).toBeVisible();
  await expect(page.getByText(/Perfil: Imóvel mobiliado · versão 2/)).toBeVisible();
  await expect(page.getByText(/1 pendência\(s\) de cobertura/)).toBeVisible();
  await expect(page.getByText("Registros de entrega pendentes: Chaves.")).toBeVisible();
});
