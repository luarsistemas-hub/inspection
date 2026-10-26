import { expect, test, type Page } from "@playwright/test";

const pageInfo = { endCursor: null, hasNextPage: false };
const reviewItem = { id: "item-a", identityId: "identity-a", environmentId: "environment-a", typeCode: "SOFA", name: "Sofá", material: "tecido", color: null, quantity: 1, condition: "GOOD", observation: "OBSERVED", limitation: null, confidenceBps: 9200, origin: "AI", declared: false, decision: "PENDING", rejectionReason: null, version: 1, evidence: [] };

async function openReview(page: Page, sharedState: { itemDecisions: Record<string, string>; itemVersion: number } = { itemDecisions: {}, itemVersion: 1 }) {
  const calls: Array<{ operation: string; variables: Record<string, unknown> }> = [];
  await page.route("**/protocol/openid-connect/token", (route) => route.fulfill({ status: 200, contentType: "application/json", headers: { "access-control-allow-origin": "*" }, body: JSON.stringify({ access_token: "baseline-review-test-token" }) }));
  await page.addInitScript(() => sessionStorage.setItem("inspection.dashboard.pkce", JSON.stringify({ verifier: "baseline-review-verifier", state: "baseline-review-state", returnTo: "/baselines/review/draft-a" })));
  await page.route("**/graphql", async (route) => {
    const request = route.request().postDataJSON() as { query: string; variables?: Record<string, unknown> };
    const operation = request.query.match(/(?:query|mutation)\s+(\w+)/)?.[1] ?? "unknown";
    calls.push({ operation, variables: request.variables ?? {} });
    const memberships = [{ id: "membership-a", tenantId: "tenant-a", name: "Revisora QA", email: "reviewer@example.test", role: "MANAGER", status: "ACTIVE" }];
    let data: Record<string, unknown> = {};
    if (operation === "DashboardGate" || operation === "DashboardMemberships") data = { me: { identityId: "identity-a", tenantId: "tenant-a", productEntitlements: ["DASHBOARD"], roles: ["MANAGER"], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-a" }], memberships }, tenant: { id: "tenant-a", name: "Operação QA", status: "ACTIVE" } };
    if (operation === "DashboardBaselineAsset") data = { asset: { id: "asset-a", name: "Apartamento QA", externalKey: "APT-1", businessUnitId: "unit-a" } };
    if (operation === "DashboardBaselineAssets") data = { assets: { nodes: [{ id: "asset-a", name: "Apartamento QA", externalKey: "APT-1", businessUnitId: "unit-a", segmentVersionId: "segment-a", templateId: "template-a", status: "ACTIVE", version: 1 }, { id: "asset-b", name: "Casa QA", externalKey: "CASA-2", businessUnitId: "unit-b", segmentVersionId: "segment-b", templateId: "template-b", status: "ACTIVE", version: 1 }], pageInfo } };
    if (operation === "DashboardCounterpartyOptions") data = { participants: { nodes: [{ id: "participant-a", businessUnitId: "unit-a", name: "Proprietária QA", status: "ACTIVE" }, { id: "participant-b", businessUnitId: "unit-b", name: "Proprietário de outra unidade", status: "ACTIVE" }], pageInfo } };
    if (operation === "DashboardBaselineVersions") data = { baselineVersions: { nodes: [{ id: "draft-a", originId: "origin-a", versionNumber: 2, status: "DRAFT", mediaKind: "PHOTO", activatedAt: null, activatedBy: null, purpose: null, source: "INSPECTION", sourceInspectionId: "inspection-a", hasInventory: true, draftState: "READY", sourceAvailability: "AVAILABLE", counts: { included: Object.values(sharedState.itemDecisions).filter((value) => ["ACCEPTED", "EDITED", "ADDED"].includes(value)).length, rejected: Object.values(sharedState.itemDecisions).filter((value) => value === "REJECTED").length, notReviewed: 3 - Object.keys(sharedState.itemDecisions).length }, contestationSummary: { pending: 0, acknowledged: 0, contested: 0, open: 0, noResponse: 0 }, counterpartyInvitations: [] }], pageInfo } };
    if (operation === "DashboardInventoryDraft") data = { inventoryDraft: { assetId: "asset-a", version: { id: "draft-a", originRevision: 1, versionNumber: 2, status: "DRAFT", draftState: "READY", hasInventory: true, purpose: null, counts: { included: Object.values(sharedState.itemDecisions).filter((value) => ["ACCEPTED", "EDITED", "ADDED"].includes(value)).length, rejected: Object.values(sharedState.itemDecisions).filter((value) => value === "REJECTED").length, notReviewed: 3 - Object.keys(sharedState.itemDecisions).length } }, progress: { decided: Object.keys(sharedState.itemDecisions).length, pending: 3 - Object.keys(sharedState.itemDecisions).length }, readOnly: false, noticeCode: null, warnings: [], environments: [{ id: "environment-a", identityId: "environment-identity-a", typeCode: "LIVING_ROOM", name: "Sala", expected: true, addedDuringCapture: false, decision: "PENDING", position: 0, items: [{ ...reviewItem, id: "item-a", name: "Sofá", version: sharedState.itemVersion, evidence: [{ id: "evidence-a", kind: "PHOTO", mediaId: "media-a", frameId: null, timestampMs: null, url: "https://example.test/evidence-a.jpg", position: 0, availability: "AVAILABLE", retentionAvailability: null }], decision: sharedState.itemDecisions["item-a"] ?? "PENDING" }, { ...reviewItem, id: "item-b", name: "Mesa", version: sharedState.itemVersion, decision: sharedState.itemDecisions["item-b"] ?? "PENDING" }, { ...reviewItem, id: "item-c", name: "Cadeira", version: sharedState.itemVersion, decision: sharedState.itemDecisions["item-c"] ?? "PENDING" }] }] } };
    if (operation === "DecideInventoryItems") { const input = request.variables?.input as { decisions?: Array<{ itemId: string; action: string; expectedVersion: number }> }; const decisions = input?.decisions ?? []; const stale = decisions.some((decision) => decision.expectedVersion !== sharedState.itemVersion); if (!stale) { for (const decision of decisions) sharedState.itemDecisions[decision.itemId] = ({ ACCEPT: "ACCEPTED", EDIT: "EDITED", REJECT: "REJECTED" } as Record<string, string>)[decision.action] ?? "PENDING"; sharedState.itemVersion++; } data = { decideInventoryItems: { items: [], emptyEnvironments: [], userErrors: stale ? [{ code: "CONFLICT", field: "expectedVersion", message: "O item foi atualizado por outra pessoa." }] : [], clientMutationId: "decision-a" } }; }
    if (operation === "BulkAcceptInventoryEnvironment") { const acceptedCount = 3 - Object.keys(sharedState.itemDecisions).length; for (const itemId of ["item-a", "item-b", "item-c"]) sharedState.itemDecisions[itemId] ??= "ACCEPTED"; data = { bulkAcceptInventoryEnvironment: { acceptedCount, userErrors: [], clientMutationId: "bulk-a" } }; }
    if (operation === "AddInventoryItem") data = { addInventoryItem: { item: { id: "item-new", decision: "ADDED", name: "Luminária declarada" }, userErrors: [], clientMutationId: "add-a" } };
    if (operation === "MergeInventoryItems") data = { mergeInventoryItems: { item: { id: "item-merged", decision: "PENDING", name: "Sofá" }, userErrors: [], clientMutationId: "merge-a" } };
    if (operation === "SplitInventoryItem") data = { splitInventoryItem: { items: [{ id: "item-part-a", decision: "PENDING", name: "Parte A" }, { id: "item-part-b", decision: "PENDING", name: "Parte B" }], userErrors: [], clientMutationId: "split-a" } };
    if (operation === "UpsertInventoryEnvironment") data = { upsertInventoryEnvironment: { environment: { id: "environment-a", name: "Sala renovada", typeCode: "LIVING_ROOM", expected: true }, userErrors: [], clientMutationId: "environment-a" } };
    if (operation === "ActivateBaselineVersion") data = { activateBaselineVersion: { version: { id: "draft-a", versionNumber: 2, counts: { included: 1, rejected: 0, notReviewed: 0 } }, userErrors: [], clientMutationId: "activate-a" } };
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data }) });
  });
  await page.goto("/auth/callback?code=baseline-review-code&state=baseline-review-state");
  await expect(page.getByRole("navigation", { name: "Painel" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Revisar inventário" })).toBeVisible();
  return calls;
}

test("E2E-005 review decisions and activation with a counterparty", async ({ page }, testInfo) => {
  const calls = await openReview(page);
  await expect(page.getByRole("heading", { name: "Sofá" })).toBeVisible();
  await expect(page.getByText("Definições das condições").first()).toBeVisible();
  await page.locator(".baseline-item").filter({ has: page.getByRole("heading", { name: "Sofá" }) }).getByRole("button", { name: "Aceitar Sofá", exact: true }).click();
  await expect(page.getByText("1 item revisado · 2 pendentes.", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Aceitar itens confiáveis" }).click();
  const bulkDialog = page.getByRole("dialog", { name: "Aceitar itens confiáveis" });
  await bulkDialog.getByRole("button", { name: "Aceitar itens", exact: true }).click();
  await expect(bulkDialog).toBeHidden();
  const sofa = page.locator(".baseline-item").filter({ has: page.getByRole("heading", { name: "Sofá", exact: true }) });
  await sofa.getByRole("button", { name: "Editar Sofá", exact: true }).click();
  const editDialog = page.getByRole("dialog", { name: "Editar item" });
  await editDialog.getByLabel("Nome do item", { exact: true }).fill("Sofá editado");
  await editDialog.getByLabel("Condição", { exact: true }).selectOption("FAIR");
  await editDialog.getByRole("button", { name: "Salvar alterações", exact: true }).click();
  await expect(editDialog).toBeHidden();
  const chair = page.locator(".baseline-item").filter({ has: page.getByRole("heading", { name: "Cadeira", exact: true }) });
  await chair.getByRole("button", { name: "Rejeitar Cadeira", exact: true }).click();
  const rejectDialog = page.getByRole("dialog", { name: "Rejeitar item" });
  await rejectDialog.getByLabel("Motivo da rejeição", { exact: true }).selectOption("DUPLICATE");
  await rejectDialog.getByRole("button", { name: "Confirmar rejeição", exact: true }).click();
  await expect(rejectDialog).toBeHidden();
  const addItem = page.locator(".baseline-add-item");
  await addItem.getByLabel("Nome do item", { exact: true }).fill("Luminária declarada");
  await addItem.getByLabel(/Sofá · Foto/).check();
  await page.getByRole("button", { name: "Adicionar item" }).click();
  await sofa.getByRole("button", { name: "Mesclar Sofá", exact: true }).click();
  const mergeDialog = page.getByRole("dialog", { name: "Mesclar itens" });
  await mergeDialog.getByLabel("Mesa", { exact: true }).check();
  await mergeDialog.getByRole("button", { name: "Mesclar itens selecionados", exact: true }).click();
  await expect(mergeDialog).toBeHidden();
  await sofa.getByRole("button", { name: "Dividir Sofá", exact: true }).click();
  const splitDialog = page.getByRole("dialog", { name: "Dividir item" });
  await splitDialog.getByLabel("Nome da primeira parte", { exact: true }).fill("Mesa e tampo");
  await splitDialog.getByLabel("Nome da segunda parte", { exact: true }).fill("Pés da mesa");
  await splitDialog.getByLabel("Foto", { exact: true }).check();
  await splitDialog.getByRole("button", { name: "Dividir item", exact: true }).click();
  await expect(splitDialog).toBeHidden();
  await page.getByRole("button", { name: "Editar ambiente", exact: true }).click();
  const environmentDialog = page.getByRole("dialog", { name: "Editar ambiente" });
  await environmentDialog.getByLabel("Nome do ambiente", { exact: true }).fill("Sala renovada");
  await environmentDialog.getByRole("button", { name: "Salvar ambiente", exact: true }).click();
  await expect(environmentDialog).toBeHidden();
  await page.getByLabel("Propósito da versão").selectOption("MOVE_IN");
  await expect(page.getByText(/Incluídos: 2 · rejeitados: 1 · não revisados: 0/)).toBeVisible();
  const counterparties = page.getByLabel("Convidar pessoas para revisar esta versão");
  await expect(counterparties.getByRole("option")).toHaveCount(1);
  await expect(counterparties.getByRole("option", { name: "Proprietária QA", exact: true })).toBeAttached();
  await counterparties.selectOption(["participant-a"]);
  await page.getByRole("button", { name: "Ativar inventário" }).click();
  const activationDialog = page.getByRole("dialog", { name: "Ativar inventário" });
  await activationDialog.getByRole("button", { name: "Ativar inventário", exact: true }).click();
  await expect(page.getByText(/Versão 2 ativada/)).toBeVisible();
  expect(calls.find(({ operation }) => operation === "ActivateBaselineVersion")?.variables.input).toMatchObject({ purpose: "MOVE_IN", counterpartyParticipantIds: ["participant-a"] });
  expect(calls.map(({ operation }) => operation)).toEqual(expect.arrayContaining(["BulkAcceptInventoryEnvironment", "AddInventoryItem", "MergeInventoryItems", "SplitInventoryItem", "UpsertInventoryEnvironment"]));
  expect(calls.find(({ operation }) => operation === "AddInventoryItem")?.variables.input).toMatchObject({ name: "Luminária declarada", evidence: [{ kind: "PHOTO", mediaId: "media-a", frameId: null }] });
  expect(calls.filter(({ operation }) => operation === "DecideInventoryItems").some(({ variables }) => JSON.stringify(variables.input).includes('"condition":"FAIR"'))).toBe(true);
  expect(calls.find(({ operation }) => operation === "SplitInventoryItem")?.variables.input).toMatchObject({ parts: [{ name: "Mesa e tampo", evidenceIds: ["evidence-a"] }, { name: "Pés da mesa", evidenceIds: [] }] });
  expect(calls.find(({ operation }) => operation === "UpsertInventoryEnvironment")?.variables.input).toMatchObject({ typeCode: "LIVING_ROOM", name: "Sala renovada" });
  expect(calls.find(({ operation, variables }) => operation === "DecideInventoryItems" && JSON.stringify(variables.input).includes('"action":"REJECT"'))?.variables.input).toMatchObject({ decisions: [{ action: "REJECT", reason: "DUPLICATE", reasonText: null }] });
});

test("E2E-006 mobile review keeps the item actions reachable", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "mobile");
  await openReview(page);
  await expect(page.getByRole("heading", { name: "Sala" })).toBeVisible();
  await page.locator(".baseline-item").filter({ has: page.getByRole("heading", { name: "Sofá" }) }).getByRole("button", { name: "Aceitar Sofá", exact: true }).tap();
  await page.locator(".baseline-item").filter({ has: page.getByRole("heading", { name: "Sofá" }) }).getByRole("button", { name: "Editar Sofá", exact: true }).tap();
  const editDialog = page.getByRole("dialog", { name: "Editar item" });
  await editDialog.getByLabel("Nome do item", { exact: true }).fill("Sofá revisado");
  await editDialog.getByRole("button", { name: "Salvar alterações", exact: true }).tap();
  await expect(page.getByRole("button", { name: "Ativar inventário" })).toBeVisible();
  await page.getByLabel("Propósito da versão").selectOption("MOVE_IN");
  await page.getByRole("button", { name: "Ativar inventário" }).tap();
  await page.getByRole("dialog", { name: "Ativar inventário" }).getByRole("button", { name: "Ativar inventário", exact: true }).tap();
  await expect(page.getByText(/Versão 2 ativada/)).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(await page.evaluate(() => window.innerWidth));
});

test("E2E-007 a stale review displays Atualizar and reloads the changed draft", async ({ page, browser }) => {
  const secondContext = await browser.newContext();
  const secondPage = await secondContext.newPage();
  try {
    const sharedState = { itemDecisions: {} as Record<string, string>, itemVersion: 1 };
    await openReview(page, sharedState);
    await openReview(secondPage, sharedState);
    const firstSofa = page.locator(".baseline-item").filter({ has: page.getByRole("heading", { name: "Sofá", exact: true }) });
    const secondSofa = secondPage.locator(".baseline-item").filter({ has: secondPage.getByRole("heading", { name: "Sofá", exact: true }) });
    await firstSofa.getByRole("button", { name: "Aceitar Sofá", exact: true }).click();
    await expect(page.getByText("1 item revisado · 2 pendentes.", { exact: true })).toBeVisible();
    await secondSofa.getByRole("button", { name: "Aceitar Sofá", exact: true }).click();
    await expect(secondPage.getByRole("alert").filter({ hasText: "atualizado por outra pessoa" })).toBeVisible();
    await expect(secondPage.getByRole("button", { name: "Atualizar", exact: true })).toHaveCount(1);
    await secondPage.getByRole("button", { name: "Atualizar", exact: true }).click();
    await expect(secondSofa.getByRole("button", { name: "Aceitar Sofá", exact: true })).toBeDisabled();
    await expect(secondPage.getByText("1 item revisado · 2 pendentes.", { exact: true })).toBeVisible();
  } finally {
    await secondContext.close();
  }
});
