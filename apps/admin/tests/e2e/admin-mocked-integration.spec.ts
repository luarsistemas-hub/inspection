import { expect, test, type Page, type Route } from "@playwright/test";
import { collectionRows, loginAsLocalAdmin, mockAdminOidc, navigateAdmin } from "./support/auth";

type GraphQLReply = { data?: Record<string, unknown>; errors?: Array<{ message: string; extensions?: { code?: string; field?: string } }> };
type GraphQLCall = { operation: string; variables: Record<string, unknown> };
type MockController = {
  calls: GraphQLCall[];
  enqueue(operation: string, ...replies: GraphQLReply[]): void;
  hold(operation: string): () => void;
  setIdentity(reply: GraphQLReply): void;
};

const activeIdentity = (role = "TENANT_ADMIN", canViewLLMCosts = true): GraphQLReply => ({ data: {
  me: { identityId: "identity-a", tenantId: "tenant-a", roles: [role], productEntitlements: ["ADMIN", "DASHBOARD"], memberships: [{ id: "membership-a", tenantId: "tenant-a", role, status: "ACTIVE", scopes: [{ kind: "Tenant", resourceId: "tenant-a" }] }], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-a" }], canViewLLMCosts },
  tenant: { id: "tenant-a", name: "Minha operação", language: "pt-BR", defaultTimezone: "America/Sao_Paulo", status: "ACTIVE", version: 1 },
} });

const pageInfo = (hasNextPage = false, endCursor: string | null = null) => ({ hasNextPage, endCursor });
async function fillAssetAddress(page: Page, street: string, number = "101") {
  await page.getByLabel("CEP").fill("01001-000");
  await page.getByLabel("Logradouro").fill(street);
  await page.getByRole("textbox", { name: "Número *" }).fill(number);
  await page.getByLabel("Cidade").fill("São Paulo");
  await page.getByLabel("UF").selectOption("SP");
}
const defaultReply = (operation: string): GraphQLReply => {
  switch (operation) {
    case "AdminIdentity": return activeIdentity();
    case "AdminOrganization": return { data: { businessUnits: { nodes: [{ id: "unit-a", code: "UN01", name: "Unidade Norte", status: "ACTIVE", version: 1 }], pageInfo: pageInfo() } } };
    case "AdminAccess": return { data: { memberships: { nodes: [{ id: "member-a", role: "EMPLOYEE", status: "ACTIVE", version: 1, scopes: [] }], pageInfo: pageInfo() } } };
    case "AdminCatalogs": return { data: { participants: { nodes: [{ id: "participant-a", name: "Ana QA", status: "ACTIVE", version: 1, contacts: [] }], pageInfo: pageInfo() }, segmentDefinitions: { nodes: [{ id: "segment-a", key: "RESIDENTIAL", name: "Residencial", activeVersionId: "segment-version-a", version: 1 }], pageInfo: pageInfo() }, templates: { nodes: [{ id: "template-a", key: "STANDARD", name: "Padrão", segmentVersionId: "segment-version-a", activeVersionId: "template-version-a", version: 1 }], pageInfo: pageInfo() } } };
    case "AdminAssets": return { data: { assets: { nodes: [{ id: "asset-a", name: "Apartamento 101", externalKey: "APT101", address: "Rua de Teste, 101", addressDetails: null, addressStatus: "LEGACY", status: "ACTIVE", businessUnitId: "unit-a", segmentVersionId: "segment-version-a", templateId: "template-a", latitudeE6: 12345, longitudeE6: 67890, geofenceMeters: 150, attributes: { propertyType: "residential" }, policyOverrides: { allowGallery: true }, assignments: [{ participantId: "participant-a", role: "OWNER", active: true }], version: 1 }], pageInfo: pageInfo() } } };
    case "AdminLookupPostalCode": return { data: { lookupPostalCode: { found: true, postalCode: "01001000", street: "Praça da Sé", district: "Sé", city: "São Paulo", state: "SP", municipalityCode: "3550308" } } };
    case "AdminPublicationPolicy": return { data: { publicationPolicy: { mode: "MANUAL", version: 1 } } };
    case "AdminRetentionPolicies": return { data: { retentionPolicies: { nodes: [{ id: "retention-a", evidenceDays: 30, operationalDays: 60, securityDays: 90, version: 1 }], pageInfo: pageInfo() } } };
    case "AdminNotificationDeliveries": return { data: { notificationDeliveries: { nodes: [{ id: "delivery-a", status: "FAILED", createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-02T00:00:00Z", inspectionId: "inspection-a", invitationId: null, logicalTemplate: "invite", failureCode: "BOUNCE", recipientMasked: "a***@example.test", responsibilityVersion: 1, canCorrectResponsibleEmail: true }], pageInfo: pageInfo() } } };
    case "AdminAudit": return { data: { auditEvents: { nodes: [{ id: "event-a", action: "UPDATED", targetType: "Asset", targetId: "asset-a", outcome: "SUCCEEDED", reason: "fixture event", correlationId: "correlation-a", occurredAt: "2026-01-01T00:00:00Z" }], pageInfo: pageInfo() }, usageSummary: { from: "2026-01-01", to: "2026-01-02", requests: 1, inputTokens: 2, outputTokens: 3, cost: null } } };
    case "AdminHistory": return { data: { auditEvents: { nodes: [{ id: "history-a", action: "UPDATED", targetType: "Asset", targetId: "asset-a", outcome: "SUCCEEDED", reason: "fixture history", correlationId: "correlation-a", occurredAt: "2026-01-01T00:00:00Z" }], pageInfo: pageInfo() } } };
    case "AdminAnalysisPrompt": return { data: { analysisPrompt: { analysisType: "REAL_ESTATE", systemPrompt: "Descreva somente evidências observáveis.", modelAlias: "inspection-vision", minimumConfidenceBps: 7000, canonicalDigest: "digest-a", revision: 1, updatedAt: "2026-01-01T00:00:00Z" } } };
    case "AdminLLMUsageTenants": return { data: { llmUsageTenants: { nodes: [{ id: "tenant-a", name: "Minha operação", language: "pt-BR", defaultTimezone: "America/Sao_Paulo", status: "ACTIVE", version: 1 }], pageInfo: pageInfo() } } };
    case "AdminLLMUsage": return { data: { llmUsage: { from: "2026-01-01T00:00:00Z", to: "2026-01-02T00:00:00Z", attemptedCalls: 0, deliveredCalls: 0, incompleteCalls: 0, inputTokens: 0, outputTokens: 0, cachedInputTokens: 0, cacheHitCalls: 0, knownCacheCalls: 0, unknownCacheCalls: 0, knownReportedCost: 0, unknownCostCalls: 0, costComplete: true, coverageStartedAt: "2026-01-01T00:00:00Z", coverageComplete: true, calls: [], pageInfo: pageInfo() } } };
    case "AdminCreateBusinessUnit": return { data: { createBusinessUnit: { businessUnit: { id: "unit-created", code: "UN01", name: "Unidade Norte", status: "ACTIVE", version: 1 }, userErrors: [], clientMutationId: "unit-mutation" } } };
    case "AdminInviteInternalUser": return { data: { inviteInternalUser: { membership: { id: "member-created", role: "EMPLOYEE", status: "ACTIVE", version: 1, scopes: [] }, userErrors: [], clientMutationId: "invite-mutation" } } };
    case "AdminUpsertParticipant": return { data: { upsertParticipant: { participant: { id: "participant-created", name: "Ana QA", status: "ACTIVE", version: 1, contacts: [] }, userErrors: [], clientMutationId: "participant-mutation" } } };
    case "AdminRegisterAsset": return { data: { registerAsset: { asset: { id: "asset-created", name: "Apartamento 101", externalKey: "APT101", status: "ACTIVE", version: 1 }, userErrors: [], clientMutationId: "asset-mutation" } } };
    case "AdminUpdateAsset": return { data: { updateAsset: { asset: { id: "asset-a", name: "Apartamento 101", externalKey: "APT101", address: "Rua de Teste, 101 — São Paulo/SP — CEP 01001-000", addressStatus: "COMPLETE", status: "ACTIVE", version: 2 }, userErrors: [], clientMutationId: "address-mutation" } } };
    case "AdminConfigurePublicationPolicy": return { data: { configurePublicationPolicy: { policy: { mode: "AUTOMATIC", version: 2 }, userErrors: [], clientMutationId: "policy-mutation" } } };
    case "AdminUpdateAnalysisPrompt": return { data: { updateAnalysisPrompt: { prompt: { analysisType: "REAL_ESTATE", systemPrompt: "Revise as imagens", modelAlias: "inspection-vision", minimumConfidenceBps: 7000, canonicalDigest: "digest-b", revision: 2, updatedAt: "2026-01-02T00:00:00Z" }, userErrors: [], clientMutationId: "prompt-mutation" } } };
    case "BootstrapTenant": return { data: { createTenant: { tenant: { id: "tenant-a", name: "Minha operação", status: "ACTIVE" }, userErrors: [], clientMutationId: "bootstrap-mutation" } } };
    default: return { data: {} };
  }
};

async function installMocks(page: Page): Promise<MockController> {
  const calls: GraphQLCall[] = [];
  const queues = new Map<string, GraphQLReply[]>();
  const gates = new Map<string, Array<{ promise: Promise<void>; release: () => void }>>();
  let identityReply = activeIdentity();
  await page.route("**/graphql", async (route: Route) => {
    const request = route.request().postDataJSON() as { query: string; variables?: Record<string, unknown> };
    const operation = request.query.match(/(?:query|mutation)\s+(\w+)/)?.[1] ?? "unknown";
    calls.push({ operation, variables: request.variables ?? {} });
    const queue = queues.get(operation);
    const reply = queue?.shift() ?? (operation === "AdminIdentity" ? identityReply : defaultReply(operation));
    const gate = gates.get(operation)?.shift();
    if (gate) await gate.promise;
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(reply) });
  });
  return {
    calls,
    enqueue(operation, ...replies) { queues.set(operation, [...(queues.get(operation) ?? []), ...replies]); },
    hold(operation) {
      let release!: () => void;
      const promise = new Promise<void>((resolve) => { release = resolve; });
      gates.set(operation, [...(gates.get(operation) ?? []), { promise, release }]);
      return release;
    },
    setIdentity(reply) { identityReply = reply; },
  };
}

async function loginIntoFirstAccess(page: Page): Promise<void> {
  await mockAdminOidc(page);
  await page.goto("/organization");
  await page.getByRole("button", { name: "Entrar com conta administrativa" }).click();
  await page.waitForURL((url) => url.hostname === "localhost" && (url.port === "8081" || url.pathname === "/auth/callback"), { timeout: 15_000 });
  const currentURL = new URL(page.url());
  if (currentURL.hostname === "localhost" && currentURL.pathname.includes("/protocol/openid-connect/auth")) {
    await page.locator("#username").fill(process.env.INSPECTION_E2E_USERNAME ?? "admin");
    await page.locator("#password").fill(process.env.INSPECTION_E2E_PASSWORD ?? "admin");
    await page.locator("button[type=submit]").click();
  }
  await page.waitForURL("**/organization", { timeout: 15_000 });
  await expect(page.getByRole("button", { name: "Criar operação local" })).toBeVisible();
}

function usageReply(tenantName: string, inspectionId: string): GraphQLReply {
  const call = { callId: `call-${tenantName}`, tenantId: tenantName === "Tenant A" ? "tenant-a" : "tenant-b", tenantName, inspectionId, jobId: "job-a", eventId: "event-a", executionId: "execution-a", correlationId: "correlation-a", attempt: 1, replayGeneration: 0, mode: "LIVE", comparisonMode: "LIVE", modelAlias: "inspection-vision", provider: "mock", model: "mock-vision", gatewayRequestId: "request-a", state: "FINISHED", technicalOutcome: "SUCCESS", transportDelivered: true, httpStatus: 200, inputTokens: 10, outputTokens: 5, cachedInputTokens: 0, imageCount: 1, requestBodyBytes: 100, reportedCost: 0, durationMs: 10, startedAt: "2026-01-01T00:00:00Z", finishedAt: "2026-01-01T00:00:01Z" };
  return { data: { llmUsage: { from: "2026-01-01T00:00:00Z", to: "2026-01-02T00:00:00Z", attemptedCalls: 1, deliveredCalls: 1, incompleteCalls: 0, inputTokens: 10, outputTokens: 5, cachedInputTokens: 0, cacheHitCalls: 0, knownCacheCalls: 0, unknownCacheCalls: 0, knownReportedCost: 0, unknownCostCalls: 0, costComplete: true, coverageStartedAt: "2026-01-01T00:00:00Z", coverageComplete: true, calls: [call], pageInfo: pageInfo() } } };
}

test.describe("Admin deterministic feature integration (mocked GraphQL; no LLM provider)", () => {
  test.skip(process.env.INSPECTION_E2E_MOCKS !== "true", "run explicitly with INSPECTION_E2E_MOCKS=true and the local mock-configured identity stack");

  test("IT-031 prevents invalid codes from reaching later activation stages", async ({ page }) => {
    let mutationCount = 0;
    await page.route("**/graphql", async (route) => {
      const body = route.request().postDataJSON() as { query: string };
      if (body.query.includes("AdminActivationSession")) return route.fulfill({ status: 200, headers: { "x-csrf-token": "csrf-fixture" }, json: { data: { onboardingSession: { id: "session-a" } } } });
      if (body.query.includes("RequestAdminActivationOtp")) { mutationCount++; return route.fulfill({ json: { data: { requestAdminActivationOtp: { userErrors: [] } } } }); }
      return route.fulfill({ json: { data: { verifyAdminActivationOtp: { userErrors: [] } } } });
    });
    await page.goto("/activate?token=activation-fixture");
    await page.getByRole("button", { name: "Enviar código de ativação" }).click();
    await expect(page.getByLabel("Código de ativação")).toBeVisible();
    await page.getByLabel("Código de ativação").fill("12345");
    await expect(page.getByRole("button", { name: "Confirmar código" })).toBeDisabled();
    await expect(page.getByRole("textbox", { name: "Nova senha", exact: true })).toHaveCount(0);
    expect(mutationCount).toBe(1);
  });

  test("invitation activation requires the emailed code and sets a password only for a new account", async ({ page }) => {
    const calls: string[] = [];
    await page.route("**/graphql", async (route) => {
      const query = (route.request().postDataJSON() as { query: string }).query;
      const field = query.match(/\{\s*(\w+)\(input/)?.[1] ?? "";
      calls.push(field);
      if (field === "requestInternalUserActivationOtp") return route.fulfill({ headers: { "x-csrf-token": "csrf-fixture" }, json: { data: { [field]: { activation: { membershipId: "member-a", email: "new@example.test", name: "New User", newIdentity: true, status: "OTP_SENT" }, userErrors: [] } } } });
      if (field === "verifyInternalUserActivationOtp") return route.fulfill({ json: { data: { [field]: { activation: { membershipId: "member-a", email: "new@example.test", name: "New User", newIdentity: true, status: "VERIFIED" }, userErrors: [] } } } });
      return route.fulfill({ json: { data: { [field]: { activation: { membershipId: "member-a", email: "new@example.test", name: "New User", newIdentity: true, status: "ACCEPTED" }, userErrors: [] } } } });
    });
    await page.goto("/activate/invitation?token=invitation-fixture");
    await page.getByRole("button", { name: "Enviar código" }).click();
    await expect(page.getByLabel("Código de confirmação")).toBeVisible();
    await expect(page.getByRole("textbox", { name: "Nova senha", exact: true })).toHaveCount(0);
    await page.getByLabel("Código de confirmação").fill("123456");
    await page.getByRole("button", { name: "Confirmar código" }).click();
    await expect(page.getByRole("textbox", { name: "Nova senha", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Criar senha e ativar" })).toBeDisabled();
    await page.getByRole("textbox", { name: "Nova senha", exact: true }).fill("NovaSenha1!");
    await page.getByLabel("Confirme a nova senha").fill("NovaSenha1!");
    await page.getByRole("button", { name: "Criar senha e ativar" }).click();
    await expect(page.getByRole("heading", { name: "Acesso ativado" })).toBeVisible();
    expect(calls).toEqual(["requestInternalUserActivationOtp", "verifyInternalUserActivationOtp", "completeInternalUserActivation"]);
  });

  test("existing account accepts an invitation without a password step", async ({ page }) => {
    await page.route("**/graphql", async (route) => {
      const query = (route.request().postDataJSON() as { query: string }).query;
      const field = query.match(/\{\s*(\w+)\(input/)?.[1] ?? "";
      const payload = field === "requestInternalUserActivationOtp"
        ? { membershipId: "member-a", email: "existing@example.test", name: "Existing User", newIdentity: false, status: "OTP_SENT" }
        : { membershipId: "member-a", email: "existing@example.test", name: "Existing User", newIdentity: false, status: "ACCEPTED" };
      return route.fulfill({ headers: { "x-csrf-token": "csrf-fixture" }, json: { data: { [field]: { activation: payload, userErrors: [] } } } });
    });
    await page.goto("/activate/invitation?token=existing-fixture");
    await page.getByRole("button", { name: "Enviar código" }).click();
    await page.getByLabel("Código de confirmação").fill("123456");
    await page.getByRole("button", { name: "Confirmar código" }).click();
    await expect(page.getByRole("heading", { name: "Acesso ativado" })).toBeVisible();
    await expect(page.getByRole("textbox", { name: "Nova senha", exact: true })).toHaveCount(0);
  });

  test("IT-032 rejects passwords that do not meet the new password policy", async ({ page }) => {
    await page.route("**/graphql", async (route) => {
      const query = (route.request().postDataJSON() as { query: string }).query;
      if (query.includes("AdminActivationSession")) return route.fulfill({ status: 200, headers: { "x-csrf-token": "csrf-fixture" }, json: { data: { onboardingSession: { id: "session-a" } } } });
      const field = query.match(/\{\s*(\w+)\(input/ )?.[1] ?? "requestAdminActivationOtp";
      return route.fulfill({ json: { data: { [field]: { userErrors: [] } } } });
    });
    await page.goto("/activate?token=activation-fixture");
    await page.getByRole("button", { name: "Enviar código de ativação" }).click();
    await page.getByLabel("Código de ativação").fill("123456");
    await page.getByRole("button", { name: "Confirmar código" }).click();
    await page.getByRole("textbox", { name: "Nova senha", exact: true }).fill("abcdefghijk");
    await page.getByLabel("Confirme a nova senha").fill("abcdefghijk");
    await expect(page.getByText("Use pelo menos 6 caracteres, uma letra maiúscula e um caractere especial.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Criar senha" })).toBeDisabled();
  });

  test("IT-033 displays safe expired-token feedback", async ({ page }) => {
    await page.route("**/graphql", async (route) => route.fulfill({ json: { data: { requestAdminActivationOtp: { userErrors: [{ code: "SESSION_EXPIRED", field: null, message: "Token expirado." }] } } } }));
    await page.goto("/activate?token=expired-fixture");
    await page.getByRole("button", { name: "Enviar código de ativação" }).click();
    await expect(page.getByText("Este link de ativação é inválido, já foi utilizado ou expirou.")).toBeVisible();
    await expect(page.getByLabel("Código de ativação")).toHaveCount(0);
  });

  test("IT-035 does not expose password setup before code verification", async ({ page }) => {
    await page.goto("/activate?token=activation-fixture");
    await expect(page.getByLabel("Código de ativação")).toHaveCount(0);
    await expect(page.getByRole("textbox", { name: "Nova senha", exact: true })).toHaveCount(0);
    await expect(page.getByLabel("Confirme a nova senha")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Enviar código de ativação" })).toBeVisible();
  });

  test("IT-034 suppresses a second activation request while the first is pending", async ({ page }) => {
    let requestCount = 0;
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    await page.route("**/graphql", async (route) => {
      const query = (route.request().postDataJSON() as { query: string }).query;
      if (query.includes("RequestAdminActivationOtp")) { requestCount++; await gate; }
      return route.fulfill({ json: { data: { requestAdminActivationOtp: { userErrors: [] } } } });
    });
    await page.goto("/activate?token=activation-fixture");
    const submit = page.getByRole("button", { name: "Enviar código de ativação" });
    await submit.click();
    await expect(page.getByRole("button", { name: "Processando…" })).toBeDisabled();
    const bounds = await page.getByRole("button", { name: "Processando…" }).boundingBox();
    if (bounds) await page.mouse.click(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2);
    expect(requestCount).toBe(1);
    release();
    await expect(page.getByLabel("Código de ativação")).toBeVisible();
  });

  test("IT-036, IT-039, IT-040 and IT-192 preserve unit validation, version, failure and success", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminOrganization",
      { data: { businessUnits: { nodes: [{ id: "unit-a", code: "UN01", name: "Unidade Norte", status: "ACTIVE", version: 1 }], pageInfo: pageInfo() } } },
      { data: { businessUnits: { nodes: [{ id: "unit-south", code: "UN02", name: "Unidade Sul", status: "ACTIVE", version: 1 }], pageInfo: pageInfo() } } },
    );
    mocks.enqueue("AdminCreateBusinessUnit",
      { data: { createBusinessUnit: { businessUnit: null, userErrors: [{ code: "VERSION_CONFLICT", field: null, message: "A versão mudou. Revise o estado atual." }], clientMutationId: "conflict" } } },
      { errors: [{ message: "Falha de transporte", extensions: { code: "INTERNAL" } }] },
      { data: { createBusinessUnit: { businessUnit: { id: "unit-new", code: "UN02", name: "Unidade Sul", status: "ACTIVE", version: 1 }, userErrors: [], clientMutationId: "success" } } },
    );
    await loginAsLocalAdmin(page, "/organization");
    await page.getByRole("button", { name: "Criar unidade" }).click();
    await page.getByLabel("Código da unidade").fill("UN01");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(page.getByLabel("Nome da unidade").evaluate((el: HTMLInputElement) => el.validationMessage)).not.toBe("");
    await page.getByLabel("Nome da unidade").fill("Unidade Norte");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(page.getByText("A versão mudou. Revise o estado atual.")).toBeVisible();
    expect(mocks.calls.find((call) => call.operation === "AdminCreateBusinessUnit")?.variables.input).toMatchObject({ expectedTenantVersion: 1, code: "UN01", name: "Unidade Norte" });
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(page.getByText("Falha de transporte")).toBeVisible();
    await expect(page.getByLabel("Nome da unidade")).toHaveValue("Unidade Norte");
    await page.getByLabel("Código da unidade").fill("UN02");
    await page.getByLabel("Nome da unidade").fill("Unidade Sul");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(collectionRows(page).filter({ hasText: "Unidade Sul" })).toContainText("UN02");
  });

  test("IT-037 distinguishes an empty unit collection from a paginated collection", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminOrganization",
      { data: { businessUnits: { nodes: [], pageInfo: pageInfo() } } },
      { data: { businessUnits: { nodes: Array.from({ length: 25 }, (_, index) => ({ id: `unit-${index}`, code: `UN${String(index).padStart(2, "0")}`, name: `Unidade ${index}`, status: "ACTIVE", version: 1 })), pageInfo: pageInfo(true, "cursor-next") } } },
    );
    await loginAsLocalAdmin(page, "/organization");
    await expect(page.getByRole("heading", { name: "Nenhum registro configurado em Organização." })).toBeVisible();
    await navigateAdmin(page, "Visão geral");
    await navigateAdmin(page, "Organização");
    await expect(collectionRows(page).filter({ hasText: "Unidade 0" })).toContainText("UN00");
    await expect(page.getByRole("button", { name: "Próxima" })).toBeVisible();
    await page.getByRole("button", { name: "Próxima" }).click();
    await expect.poll(() => mocks.calls.filter((call) => call.operation === "AdminOrganization").at(-1)?.variables.after).toBe("cursor-next");
  });

  test("IT-038 does not request protected organization data for an auditor", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.setIdentity(activeIdentity("AUDITOR"));
    await loginAsLocalAdmin(page, "/organization");
    await expect(page.getByRole("heading", { name: "Acesso restrito" })).toBeVisible();
    await expect(page.getByRole("row", { name: /Unidade Norte/ })).toHaveCount(0);
    await expect(page.getByRole("navigation", { name: "Navegação administrativa" }).getByRole("link", { name: "Organização" })).toHaveCount(0);
    expect(mocks.calls.some((call) => call.operation === "AdminOrganization")).toBe(false);
  });

  test("E2E-051 allows local bootstrap and reports a denied bootstrap without fabricating a tenant", async ({ page }) => {
    const mocks = await installMocks(page);
    const emptyTenant = { data: { me: { identityId: "identity-a", tenantId: "00000000-0000-0000-0000-000000000000", roles: ["SUPER_ADMIN"], productEntitlements: ["ADMIN"], memberships: [], effectiveScopes: [], canViewLLMCosts: false }, tenant: null } };
    mocks.enqueue("AdminIdentity", emptyTenant, activeIdentity(), activeIdentity());
    await loginIntoFirstAccess(page);
    await page.getByRole("button", { name: "Criar operação local" }).click();
    await expect(page.getByRole("heading", { name: "Organização" })).toBeVisible();
    await expect(page.getByText("Minha operação").first()).toBeVisible();
    expect(mocks.calls.find((call) => call.operation === "BootstrapTenant")?.variables.input).toMatchObject({ name: "Minha operação", businessUnitCode: "MATRIZ" });

  });

  test("E2E-051 keeps an unauthorized local bootstrap from creating a tenant", async ({ page }) => {
    const mocks = await installMocks(page);
    const emptyTenant = { data: { me: { identityId: "identity-a", tenantId: "00000000-0000-0000-0000-000000000000", roles: ["EMPLOYEE"], productEntitlements: ["ADMIN"], memberships: [], effectiveScopes: [], canViewLLMCosts: false }, tenant: null } };
    mocks.enqueue("AdminIdentity", emptyTenant);
    mocks.enqueue("BootstrapTenant", { errors: [{ message: "Você não tem permissão para esta operação.", extensions: { code: "FORBIDDEN" } }] });
    await loginIntoFirstAccess(page);
    await page.getByRole("button", { name: "Criar operação local" }).click();
    await expect(page.getByText("Você não tem permissão para esta operação.")).toBeVisible();
    expect(mocks.calls.filter((call) => call.operation === "BootstrapTenant")).toHaveLength(1);
  });

  test("IT-041, IT-042, IT-044 and IT-045 keep membership invitations correctable and single-flight", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminAccess", { data: { memberships: { nodes: [], pageInfo: pageInfo() } } });
    mocks.enqueue("AdminInviteInternalUser",
      { data: { inviteInternalUser: { membership: null, userErrors: [{ code: "INVALID_INPUT", field: "email", message: "E-mail inválido." }], clientMutationId: "invite-error" } } },
      { data: { inviteInternalUser: { membership: { id: "member-new", role: "EMPLOYEE", status: "ACTIVE", version: 1, scopes: [] }, userErrors: [], clientMutationId: "invite-success" } } },
    );
    const release = mocks.hold("AdminInviteInternalUser");
    await loginAsLocalAdmin(page, "/access");
    await expect(page.getByRole("heading", { name: "Nenhum registro configurado em Identidade e acesso." })).toBeVisible();
    await page.getByRole("button", { name: "Convidar usuário" }).click();
    await page.getByLabel("Nome do usuário").fill("Usuário de teste");
    await page.getByRole("textbox", { name: "E-mail", exact: true }).fill("user@example.test");
    await page.getByLabel("Perfil de acesso").selectOption("EMPLOYEE");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    expect(await page.getByLabel("Confirme o e-mail").evaluate((el: HTMLInputElement) => el.validationMessage)).not.toBe("");
    expect(mocks.calls.filter((call) => call.operation === "AdminInviteInternalUser")).toHaveLength(0);
    await page.getByLabel("Confirme o e-mail").fill("user@example.test");
    const save = page.getByRole("button", { name: "Salvar operação" });
    await save.click();
    await expect(page.getByRole("button", { name: "Salvando…" })).toBeDisabled();
    const bounds = await page.getByRole("button", { name: "Salvando…" }).boundingBox();
    if (bounds) await page.mouse.click(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2);
    expect(mocks.calls.filter((call) => call.operation === "AdminInviteInternalUser")).toHaveLength(1);
    release();
    await expect(page.getByText("E-mail inválido.")).toBeVisible();
    await expect(page.getByLabel("Perfil de acesso")).toHaveValue("EMPLOYEE");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(collectionRows(page).filter({ hasText: "Operador" })).toBeVisible();
  });

  test("IT-043 withholds invitation controls from a role without invite permission", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.setIdentity(activeIdentity("AUDITOR"));
    mocks.enqueue("AdminAccess", { data: { memberships: { nodes: [], pageInfo: pageInfo() } } });
    await loginAsLocalAdmin(page, "/access");
    await expect(page.getByRole("heading", { name: "Identidade e acesso", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Convidar usuário" })).toHaveCount(0);
    expect(mocks.calls.some((call) => call.operation === "AdminInviteInternalUser")).toBe(false);
  });

  test("IT-046 through IT-050 validate participant prerequisites, scope, pending save and choice failure", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminOrganization",
      { data: { businessUnits: { nodes: [{ id: "unit-a", code: "UN01", name: "Unidade Norte", status: "ACTIVE", version: 1 }], pageInfo: pageInfo() } } },
      { data: { businessUnits: { nodes: [], pageInfo: pageInfo() } } },
      { data: { businessUnits: { nodes: [{ id: "unit-a", code: "UN01", name: "Unidade Norte", status: "ACTIVE", version: 1 }], pageInfo: pageInfo() } } },
      { errors: [{ message: "Unidades indisponíveis.", extensions: { code: "INTERNAL" } }] },
    );
    const release = mocks.hold("AdminUpsertParticipant");
    await loginAsLocalAdmin(page, "/catalogs");

    await page.getByRole("button", { name: "Criar responsável" }).click();
    const requiredUnit = page.getByLabel("Unidade");
    await expect(requiredUnit.locator("option")).toHaveCount(2);
    await requiredUnit.selectOption("unit-a");
    await page.getByLabel("Função no segmento").fill("OWNER");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    expect(await page.getByLabel("Nome do responsável").evaluate((el: HTMLInputElement) => el.validationMessage)).not.toBe("");
    await page.getByRole("button", { name: "Cancelar" }).click();

    await page.getByRole("button", { name: "Criar responsável" }).click();
    const noUnit = page.getByLabel("Unidade");
    await expect(noUnit.locator("option")).toHaveCount(1);
    await page.getByLabel("Nome do responsável").fill("Ana");
    await page.getByLabel("Função no segmento").fill("OWNER");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    expect(await noUnit.evaluate((el: HTMLSelectElement) => el.validationMessage)).not.toBe("");
    await page.getByRole("button", { name: "Cancelar" }).click();

    await page.getByRole("button", { name: "Criar responsável" }).click();
    const unit = page.getByLabel("Unidade");
    await expect(unit.locator("option")).toHaveCount(2);
    await expect(unit.locator("option[value='unit-b']")).toHaveCount(0);
    await unit.selectOption("unit-a");
    await page.getByLabel("Nome do responsável").fill("Ana QA");
    await page.getByRole("textbox", { name: "E-mail", exact: true }).fill("ana@example.test");
    await page.getByLabel("Confirme o e-mail").fill("ana@example.test");
    await page.getByLabel("Função no segmento").fill("OWNER");
    const save = page.getByRole("button", { name: "Salvar operação" });
    await save.click();
    await expect(page.getByRole("button", { name: "Salvando…" })).toBeDisabled();
    const pending = await page.getByRole("button", { name: "Salvando…" }).boundingBox();
    if (pending) await page.mouse.click(pending.x + pending.width / 2, pending.y + pending.height / 2);
    expect(mocks.calls.filter((call) => call.operation === "AdminUpsertParticipant")).toHaveLength(1);
    release();
    await expect(collectionRows(page).filter({ hasText: "Ana QA" })).toBeVisible();

    await page.getByRole("button", { name: "Criar responsável" }).click();
    await expect(page.getByText("Unidades indisponíveis.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Salvar operação" })).toBeDisabled();
    expect(mocks.calls.some((call) => call.operation === "AdminUpsertParticipant")).toBe(true);
  });

  test("IT-051, IT-052, IT-054 and IT-055 preserve asset relationships and optional templates", async ({ page }) => {
    const mocks = await installMocks(page);
    const units = { data: { businessUnits: { nodes: [{ id: "unit-a", code: "UN01", name: "Unidade Norte", status: "ACTIVE", version: 1 }, { id: "unit-b", code: "UN02", name: "Unidade Sul", status: "ACTIVE", version: 1 }], pageInfo: pageInfo() } } };
    const catalogs = { data: { segmentDefinitions: { nodes: [{ id: "segment-version-a", key: "RESIDENTIAL", name: "Residencial", activeVersionId: "segment-version-a", version: 1 }], pageInfo: pageInfo() }, templates: { nodes: [{ id: "template-a", key: "STANDARD", name: "Padrão", segmentVersionId: "segment-version-a", activeVersionId: "template-version-a", version: 1 }], pageInfo: pageInfo() } } };
    mocks.enqueue("AdminOrganization", units, units, units, units);
    mocks.enqueue("AdminCatalogs", catalogs,
      { data: { segmentDefinitions: catalogs.data.segmentDefinitions, templates: { nodes: [], pageInfo: pageInfo() } } },
      catalogs, catalogs);
    await loginAsLocalAdmin(page, "/assets");

    await page.getByRole("button", { name: "Registrar imóvel" }).click();
    await expect(page.getByLabel("Unidade").locator("option")).toHaveCount(3);
    await page.getByLabel("Versão do segmento").selectOption("segment-version-a");
    await page.getByLabel("Nome do imóvel").fill("Apartamento sem unidade");
    await page.getByLabel("Código do imóvel").fill("APT-INVALID");
    await fillAssetAddress(page, "Rua de Teste", "101");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    expect(await page.getByLabel("Unidade").evaluate((el: HTMLSelectElement) => el.validationMessage)).not.toBe("");
    await page.getByRole("button", { name: "Cancelar" }).click();

    await page.getByRole("button", { name: "Registrar imóvel" }).click();
    await page.getByLabel("Unidade").selectOption("unit-a");
    await page.getByLabel("Versão do segmento").selectOption("segment-version-a");
    await expect(page.getByLabel("Modelo de vistoria").locator("option")).toHaveCount(1);
    await page.getByLabel("Nome do imóvel").fill("Apartamento 102");
    await page.getByLabel("Código do imóvel").fill("APT102");
    await fillAssetAddress(page, "Rua de Teste", "102");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(collectionRows(page).filter({ hasText: "APT101" })).toBeVisible();

    await page.getByRole("button", { name: "Registrar imóvel" }).click();
    const unit = page.getByLabel("Unidade");
    await expect(unit.locator("option")).toHaveCount(3);
    await unit.selectOption("unit-a");
    await page.getByLabel("Versão do segmento").selectOption("segment-version-a");
    await page.getByLabel("Modelo de vistoria").selectOption("template-a");
    await unit.selectOption("unit-b");
    await expect(page.getByLabel("Versão do segmento")).toHaveValue("");
    await expect(page.getByLabel("Modelo de vistoria")).toHaveValue("");
    await page.getByRole("button", { name: "Cancelar" }).click();

    const release = mocks.hold("AdminRegisterAsset");
    await page.getByRole("button", { name: "Registrar imóvel" }).click();
    await page.getByLabel("Unidade").selectOption("unit-a");
    await page.getByLabel("Versão do segmento").selectOption("segment-version-a");
    await page.getByLabel("Modelo de vistoria").selectOption("template-a");
    await page.getByLabel("Nome do imóvel").fill("Apartamento pendente");
    await page.getByLabel("Código do imóvel").fill("APT-PENDING");
    await fillAssetAddress(page, "Rua de Teste", "103");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(page.getByRole("button", { name: "Salvando…" })).toBeDisabled();
    const pending = await page.getByRole("button", { name: "Salvando…" }).boundingBox();
    if (pending) await page.mouse.click(pending.x + pending.width / 2, pending.y + pending.height / 2);
    expect(mocks.calls.filter((call) => call.operation === "AdminRegisterAsset")).toHaveLength(2);
    release();
    await expect(collectionRows(page).filter({ hasText: "APT101" })).toBeVisible();
  });

  test("CEP lookup preserves edits made while awaiting the provider", async ({ page }) => {
    const mocks = await installMocks(page);
    const release = mocks.hold("AdminLookupPostalCode");
    await loginAsLocalAdmin(page, "/assets");
    await page.getByRole("button", { name: "Registrar imóvel" }).click();
    await page.getByLabel("CEP", { exact: false }).fill("01001-000");
    await page.getByRole("button", { name: "Buscar CEP" }).click();
    await expect(page.getByRole("button", { name: "Consultando…" })).toBeDisabled();
    await page.getByRole("textbox", { name: "Número *" }).fill("42");
    await page.getByLabel("Complemento").fill("Casa dos fundos");
    await page.getByLabel("Logradouro").fill("Via informada pelo morador");
    release();
    await expect(page.getByLabel("Cidade")).toHaveValue("São Paulo");
    await expect(page.getByRole("textbox", { name: "Número *" })).toHaveValue("42");
    await expect(page.getByLabel("Complemento")).toHaveValue("Casa dos fundos");
    await expect(page.getByLabel("Logradouro")).toHaveValue("Via informada pelo morador");
    await page.getByLabel("Sem número", { exact: true }).check();
    await expect(page.getByRole("textbox", { name: "Número", exact: true })).toHaveValue("");
    await expect(page.getByRole("textbox", { name: "Número", exact: true })).toBeDisabled();
  });

  test("changing CEP ignores the old result and allows a new lookup", async ({ page }) => {
    const mocks = await installMocks(page);
    const release = mocks.hold("AdminLookupPostalCode");
    await loginAsLocalAdmin(page, "/assets");
    await page.getByRole("button", { name: "Registrar imóvel" }).click();
    await page.getByLabel("CEP").fill("01001000");
    await page.getByRole("button", { name: "Buscar CEP" }).click();
    await expect.poll(() => mocks.calls.filter((call) => call.operation === "AdminLookupPostalCode").length).toBe(1);
    await page.getByLabel("CEP").fill("20040002");
    release();
    await expect(page.getByRole("button", { name: "Buscar CEP" })).toBeEnabled();
    mocks.enqueue("AdminLookupPostalCode", { data: { lookupPostalCode: { found: true, postalCode: "20040002", street: null, district: null, city: "Rio de Janeiro", state: "RJ", municipalityCode: "3304557" } } });
    await page.getByRole("button", { name: "Buscar CEP" }).click();
    await expect(page.getByLabel("Cidade")).toHaveValue("Rio de Janeiro");
    await expect(page.getByLabel("CEP")).toHaveValue("20040002");
    await expect(page.getByLabel("Logradouro")).toHaveValue("");
  });

  test("address form fits small screens with enlarged text and both themes", async ({ page }) => {
    await installMocks(page);
    await loginAsLocalAdmin(page, "/assets");
    await page.setViewportSize({ width: 320, height: 900 });
    await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
    for (const theme of ["light", "dark"]) {
      await page.getByLabel("Aparência").selectOption(theme);
      await page.getByRole("button", { name: "Registrar imóvel" }).click();
      const form = page.getByRole("form", { name: "Operação administrativa" });
      await expect(form).toBeVisible();
      expect(await form.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
      await page.getByRole("button", { name: "Cancelar", exact: true }).click();
    }
  });

  test("completes a legacy address and preserves asset coordinates and configuration", async ({ page }) => {
    const mocks = await installMocks(page);
    await loginAsLocalAdmin(page, "/assets");
    await page.getByRole("button", { name: "Abrir detalhes de Apartamento 101" }).click();
    await page.getByRole("button", { name: "Completar endereço" }).click();
    await page.getByLabel("CEP").fill("01001-000");
    await page.getByRole("button", { name: "Buscar CEP" }).click();
    await expect(page.getByLabel("Logradouro")).toHaveValue("Praça da Sé");
    await page.getByRole("textbox", { name: "Número *" }).fill("101");
    await page.getByRole("button", { name: "Salvar endereço" }).click();
    const update = mocks.calls.find((call) => call.operation === "AdminUpdateAsset");
    expect(update?.variables.input).toMatchObject({
      assetId: "asset-a",
      expectedVersion: 1,
      asset: {
        addressDetails: { countryCode: "BR", postalCode: "01001-000", street: "Praça da Sé", number: "101", district: "Sé", city: "São Paulo", state: "SP" },
        latitudeE6: 12345,
        longitudeE6: 67890,
        geofenceMeters: 150,
        attributes: { propertyType: "residential" },
        policyOverrides: { allowGallery: true },
        assignments: [{ participantId: "participant-a", role: "OWNER" }],
      },
    });
  });

  test("IT-013 shows a forbidden asset relationship error and preserves the entered values", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminRegisterAsset", { errors: [{ message: "Você não tem permissão para esta operação.", extensions: { code: "FORBIDDEN" } }] });
    await loginAsLocalAdmin(page, "/assets");
    await page.getByRole("button", { name: "Registrar imóvel" }).click();
    await page.getByLabel("Unidade").selectOption("unit-a");
    await page.getByLabel("Versão do segmento").selectOption("segment-version-a");
    await page.getByLabel("Nome do imóvel").fill("Apartamento 101");
    await page.getByLabel("Código do imóvel").fill("APT101");
    await fillAssetAddress(page, "Rua de Teste", "101");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(page.getByText("Você não tem permissão para esta operação.")).toBeVisible();
    await expect(page.getByLabel("Nome do imóvel")).toHaveValue("Apartamento 101");
    await expect(page.getByLabel("Código do imóvel")).toHaveValue("APT101");
    expect(mocks.calls.filter((call) => call.operation === "AdminRegisterAsset")).toHaveLength(1);
  });

  test("IT-011 identifies an empty asset name without losing the other form values", async ({ page }) => {
    const mocks = await installMocks(page);
    await loginAsLocalAdmin(page, "/assets");
    await page.getByRole("button", { name: "Registrar imóvel" }).click();
    await page.getByLabel("Unidade").selectOption("unit-a");
    await page.getByLabel("Versão do segmento").selectOption("segment-version-a");
    await page.getByLabel("Código do imóvel").fill("APT-EMPTY-NAME");
    await fillAssetAddress(page, "Rua de Teste", "101");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    const name = page.getByLabel("Nome do imóvel");
    await expect(name).toBeFocused();
    expect(await name.evaluate((element) => (element as HTMLInputElement).validity.valid)).toBe(false);
    expect(mocks.calls.some((call) => call.operation === "AdminRegisterAsset")).toBe(false);
    await expect(page.getByLabel("Código do imóvel")).toHaveValue("APT-EMPTY-NAME");
  });

  test("IT-012 keeps an entered asset name editable after a server length validation", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminRegisterAsset", { data: { registerAsset: { asset: null, userErrors: [{ code: "VALIDATION", field: "name", message: "O nome do imóvel excede o limite permitido." }], clientMutationId: "migration-fixture" } } });
    await loginAsLocalAdmin(page, "/assets");
    await page.getByRole("button", { name: "Registrar imóvel" }).click();
    await page.getByLabel("Unidade").selectOption("unit-a");
    await page.getByLabel("Versão do segmento").selectOption("segment-version-a");
    await page.getByLabel("Nome do imóvel").fill("Apartamento 101");
    await page.getByLabel("Código do imóvel").fill("APT101");
    await fillAssetAddress(page, "Rua de Teste", "101");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(page.getByText("O nome do imóvel excede o limite permitido.")).toBeVisible();
    await expect(page.getByLabel("Nome do imóvel")).toHaveValue("Apartamento 101");
    await expect(page.getByLabel("Nome do imóvel")).toBeEnabled();
  });

  test("IT-014 requires current-value review after an asset version conflict", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminRegisterAsset", { data: { registerAsset: { asset: null, userErrors: [{ code: "VERSION_CONFLICT", field: null, message: "A configuração mudou. Revise os valores atuais." }], clientMutationId: "migration-fixture" } } });
    await loginAsLocalAdmin(page, "/assets");
    await page.getByRole("button", { name: "Registrar imóvel" }).click();
    await page.getByLabel("Unidade").selectOption("unit-a");
    await page.getByLabel("Versão do segmento").selectOption("segment-version-a");
    await page.getByLabel("Nome do imóvel").fill("Apartamento 101");
    await page.getByLabel("Código do imóvel").fill("APT101");
    await fillAssetAddress(page, "Rua de Teste", "101");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(page.getByText("A configuração mudou. Revise os valores atuais.")).toBeVisible();
    await expect(page.getByLabel("Nome do imóvel")).toHaveValue("Apartamento 101");
  });

  test("IT-015 preserves the unsaved asset name after a failed save request", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminRegisterAsset", { errors: [{ message: "Falha temporária ao registrar o imóvel.", extensions: { code: "INTERNAL" } }] });
    await loginAsLocalAdmin(page, "/assets");
    await page.getByRole("button", { name: "Registrar imóvel" }).click();
    await page.getByLabel("Unidade").selectOption("unit-a");
    await page.getByLabel("Versão do segmento").selectOption("segment-version-a");
    await page.getByLabel("Nome do imóvel").fill("Apartamento 101");
    await page.getByLabel("Código do imóvel").fill("APT101");
    await fillAssetAddress(page, "Rua de Teste", "101");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(page.getByText("Falha temporária ao registrar o imóvel.")).toBeVisible();
    await expect(page.getByLabel("Nome do imóvel")).toHaveValue("Apartamento 101");
  });

  test("IT-056 through IT-060 keep governance subcollections independent and versioned", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminPublicationPolicy",
      defaultReply("AdminPublicationPolicy"),
      defaultReply("AdminPublicationPolicy"),
      { data: { publicationPolicy: { mode: "MANUAL", version: null } } },
      defaultReply("AdminPublicationPolicy"),
    );
    mocks.enqueue("AdminRetentionPolicies",
      { data: { retentionPolicies: { nodes: [], pageInfo: pageInfo() } } },
      defaultReply("AdminRetentionPolicies"),
    );
    mocks.enqueue("AdminNotificationDeliveries",
      defaultReply("AdminNotificationDeliveries"),
      { errors: [{ message: "Falha de entrega.", extensions: { code: "INTERNAL" } }] },
    );
    mocks.enqueue("AdminConfigurePublicationPolicy", { data: { configurePublicationPolicy: { policy: null, userErrors: [{ code: "VERSION_CONFLICT", field: null, message: "A política mudou. Revise antes de salvar." }], clientMutationId: "stale-policy" } } });
    await loginAsLocalAdmin(page, "/governance");

    const retention = collectionRows(page).filter({ hasText: "Retenção" });
    const delivery = collectionRows(page).filter({ hasText: "Entrega" });
    await expect(retention).toContainText("Nenhuma regra configurada");
    await expect(delivery).toContainText("Falhou");
    await expect(page.getByRole("button", { name: /Editar retenção/ })).toHaveCount(0);

    await navigateAdmin(page, "Visão geral");
    await navigateAdmin(page, "Governança");
    await expect(collectionRows(page).filter({ hasText: "Retenção" })).toContainText("Configurada");
    await expect(collectionRows(page).filter({ hasText: "Política de publicação" })).toContainText("Manual");
    await expect(collectionRows(page).filter({ hasText: "Entrega" })).toContainText("Indisponível");

    await navigateAdmin(page, "Visão geral");
    await navigateAdmin(page, "Governança");
    await page.getByRole("button", { name: "Configurar política" }).click();
    await page.getByLabel("Modo de publicação").selectOption("AUTOMATIC");
    await expect(page.getByRole("button", { name: "Salvar operação" })).toBeDisabled();
    await expect(page.getByLabel("Modo de publicação")).toHaveValue("AUTOMATIC");
    await page.getByRole("button", { name: "Cancelar" }).click();

    await navigateAdmin(page, "Visão geral");
    await navigateAdmin(page, "Governança");
    await page.getByRole("button", { name: "Configurar política" }).click();
    await page.getByLabel("Modo de publicação").selectOption("AUTOMATIC");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(page.getByText("A política mudou. Revise antes de salvar.")).toBeVisible();
    await expect(page.getByLabel("Modo de publicação")).toHaveValue("AUTOMATIC");
    expect(mocks.calls.find((call) => call.operation === "AdminConfigurePublicationPolicy")?.variables.input).toMatchObject({ expectedVersion: 1, mode: "AUTOMATIC" });
    await expect(collectionRows(page).filter({ hasText: "Política de publicação" })).toContainText("Manual");
  });

  test("IT-061, IT-062 and IT-065 show empty, long and recoverable audit history states", async ({ page }) => {
    const mocks = await installMocks(page);
    const events = (ids: string[]) => ({ data: { auditEvents: { nodes: ids.map((id) => ({ id, action: `UPDATED_${id}`, targetType: "Asset", targetId: "asset-a", outcome: "SUCCEEDED", reason: `Reason ${id} `.repeat(20), correlationId: `correlation-${id}`, occurredAt: "2026-01-01T00:00:00Z" })), pageInfo: pageInfo() }, usageSummary: { from: "2026-01-01", to: "2026-01-02", requests: 1, inputTokens: 2, outputTokens: 3, cost: null } } });
    mocks.enqueue("AdminAudit", events(["event-a"]), events(Array.from({ length: 25 }, (_, index) => `event-${index}`)), events(["event-a"]));
    mocks.enqueue("AdminHistory",
      { data: { auditEvents: { nodes: [], pageInfo: pageInfo() } } },
      { data: { auditEvents: { nodes: Array.from({ length: 25 }, (_, index) => ({ id: `history-${index}`, action: `UPDATED_${index}`, targetType: "Asset", targetId: "asset-a", outcome: "SUCCEEDED", reason: `Long synthetic reason ${index} `.repeat(20), correlationId: `history-correlation-${index}`, occurredAt: "2026-01-01T00:00:00Z" })), pageInfo: pageInfo(true, "history-cursor") } } },
      { data: { auditEvents: { nodes: [{ id: "history-25", action: "UPDATED_25", targetType: "Asset", targetId: "asset-a", outcome: "SUCCEEDED", reason: "Last page", correlationId: "history-correlation-25", occurredAt: "2026-01-02T00:00:00Z" }], pageInfo: pageInfo() } } },
      { errors: [{ message: "Histórico temporariamente indisponível.", extensions: { code: "INTERNAL" } }] },
    );
    await loginAsLocalAdmin(page, "/audit");
    await collectionRows(page).first().getByRole("button", { name: "Abrir detalhes" }).click();
    await expect(page.getByRole("dialog")).toContainText("Nenhum evento encontrado para este recurso.");
    await page.getByRole("button", { name: "Fechar detalhe" }).click();

    await navigateAdmin(page, "Visão geral");
    await navigateAdmin(page, "Auditoria");
    await collectionRows(page).first().getByRole("button", { name: "Abrir detalhes" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText(/Long synthetic reason 0/)).toBeVisible();
    await expect(dialog.getByRole("button", { name: "Carregar mais histórico" })).toBeVisible();
    await dialog.getByRole("button", { name: "Carregar mais histórico" }).click();
    await expect(dialog.getByText("Last page")).toBeVisible();
    await dialog.getByRole("button", { name: "Fechar detalhe" }).click();

    await navigateAdmin(page, "Visão geral");
    await navigateAdmin(page, "Auditoria");
    await collectionRows(page).first().getByRole("button", { name: "Abrir detalhes" }).click();
    await expect(page.getByText("Histórico temporariamente indisponível.")).toBeVisible();
    await expect(page.getByRole("dialog")).toContainText("UPDATED_event-a");
  });

  test("IT-063 hides history contents when the selected resource history is forbidden", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminHistory", { errors: [{ message: "O histórico deste recurso é restrito.", extensions: { code: "FORBIDDEN" } }] });
    await loginAsLocalAdmin(page, "/audit");
    await collectionRows(page).first().getByRole("button", { name: "Abrir detalhes" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("O histórico deste recurso é restrito.");
    await expect(dialog.getByText("fixture history")).toHaveCount(0);
  });

  test("IT-064 discards history that resolves after another resource is selected", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminAudit", { data: { auditEvents: { nodes: [
      { id: "event-a", action: "UPDATED_A", targetType: "Asset", targetId: "asset-a", outcome: "SUCCEEDED", reason: "resource A", correlationId: "correlation-a", occurredAt: "2026-01-01T00:00:00Z" },
      { id: "event-b", action: "UPDATED_B", targetType: "Asset", targetId: "asset-b", outcome: "SUCCEEDED", reason: "resource B", correlationId: "correlation-b", occurredAt: "2026-01-01T00:00:00Z" },
    ], pageInfo: pageInfo() }, usageSummary: { from: "2026-01-01", to: "2026-01-02", requests: 1, inputTokens: 2, outputTokens: 3, cost: null } } });
    mocks.enqueue("AdminHistory",
      { data: { auditEvents: { nodes: [{ id: "history-a", action: "UPDATED_A", targetType: "Asset", targetId: "asset-a", outcome: "SUCCEEDED", reason: "history A", correlationId: "history-a", occurredAt: "2026-01-01T00:00:00Z" }], pageInfo: pageInfo() } } },
      { data: { auditEvents: { nodes: [{ id: "history-b", action: "UPDATED_B", targetType: "Asset", targetId: "asset-b", outcome: "SUCCEEDED", reason: "history B", correlationId: "history-b", occurredAt: "2026-01-01T00:00:00Z" }], pageInfo: pageInfo() } } },
    );
    const releaseOldHistory = mocks.hold("AdminHistory");
    await loginAsLocalAdmin(page, "/audit");
    await collectionRows(page).first().getByRole("button", { name: "Abrir detalhes" }).click();
    await expect(page.getByText("Carregando histórico…")).toBeVisible();
    await page.getByRole("button", { name: "Fechar detalhe" }).click();
    await collectionRows(page).nth(1).getByRole("button", { name: "Abrir detalhes" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText("history B")).toBeVisible();
    releaseOldHistory();
    await expect(dialog.getByText("history B")).toBeVisible();
    await expect(dialog.getByText("history A")).toHaveCount(0);
  });

  test("IT-066, IT-067, IT-069 and IT-070 preserve prompt validation, length, revision and draft", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminAnalysisPrompt", ...Array.from({ length: 5 }, () => defaultReply("AdminAnalysisPrompt")));
    mocks.enqueue("AdminUpdateAnalysisPrompt",
      { data: { updateAnalysisPrompt: { prompt: null, userErrors: [{ code: "INVALID_PROMPT", field: "systemPrompt", message: "A instrução não pode ficar em branco." }], clientMutationId: "empty-prompt" } } },
      defaultReply("AdminUpdateAnalysisPrompt"),
      { data: { updateAnalysisPrompt: { prompt: null, userErrors: [{ code: "VERSION_CONFLICT", field: null, message: "A revisão mudou. Revise a configuração atual." }], clientMutationId: "stale-prompt" } } },
      { errors: [{ message: "Falha ao salvar o prompt.", extensions: { code: "INTERNAL" } }] },
    );
    await loginAsLocalAdmin(page, "/prompts");

    const editor = page.getByLabel("Instrução do sistema");
    await editor.fill(" ");
    await page.getByLabel("Confirmo a alteração do prompt global.").check();
    await page.getByRole("button", { name: "Salvar nova revisão" }).click();
    await expect(page.getByText("A instrução não pode ficar em branco.")).toBeVisible();
    await expect(page.getByText("Revisão: 1")).toBeVisible();

    await navigateAdmin(page, "Visão geral");
    await navigateAdmin(page, "Prompts de análise");
    const longPrompt = "x".repeat(20_000);
    await page.getByLabel("Instrução do sistema").fill(longPrompt);
    await page.getByLabel("Confirmo a alteração do prompt global.").check();
    await expect(page.getByText("20000/20000 caracteres.")).toBeVisible();
    const promptBounds = await page.getByLabel("Instrução do sistema").boundingBox();
    expect(promptBounds?.height).toBeGreaterThan(0);
    await page.getByRole("button", { name: "Salvar nova revisão" }).click();
    await expect(page.getByText("Revisão: 1")).toBeVisible();

    await navigateAdmin(page, "Visão geral");
    await navigateAdmin(page, "Prompts de análise");
    await page.getByLabel("Instrução do sistema").fill("Prompt revisto após conflito.");
    await page.getByLabel("Confirmo a alteração do prompt global.").check();
    await page.getByRole("button", { name: "Salvar nova revisão" }).click();
    await expect(page.getByText("A revisão mudou. Revise a configuração atual.")).toBeVisible();
    await expect(page.getByLabel("Instrução do sistema")).toHaveValue("Prompt revisto após conflito.");
    await expect(page.getByText("Revisão: 1")).toBeVisible();

    await navigateAdmin(page, "Visão geral");
    await navigateAdmin(page, "Prompts de análise");
    await page.getByLabel("Instrução do sistema").fill("Revise as imagens");
    await page.getByLabel("Confirmo a alteração do prompt global.").check();
    await page.getByRole("button", { name: "Salvar nova revisão" }).click();
    await expect(page.getByText("Falha ao salvar o prompt.")).toBeVisible();
    await expect(page.getByLabel("Instrução do sistema")).toHaveValue("Revise as imagens");
  });

  test("IT-068 denies prompt configuration to an auditor", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.setIdentity(activeIdentity("AUDITOR"));
    await loginAsLocalAdmin(page, "/prompts");
    await expect(page.getByText("Você não tem permissão para acessar este recurso neste escopo.")).toBeVisible();
    await expect(page.getByLabel("Instrução do sistema")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Salvar nova revisão" })).toHaveCount(0);
    expect(mocks.calls.some((call) => call.operation === "AdminAnalysisPrompt")).toBe(false);
  });

  test("IT-071 blocks reversed dates before requesting usage", async ({ page }) => {
    const mocks = await installMocks(page);
    await loginAsLocalAdmin(page, "/llm-usage");
    const start = page.getByLabel("Início");
    const end = page.getByLabel("Fim");
    await start.fill("2026-02-02T10:00");
    await end.fill("2026-02-01T10:00");
    await expect(page.getByText("O início deve ocorrer antes do fim.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Aplicar" })).toBeDisabled();
    expect(mocks.calls.filter((call) => call.operation === "AdminLLMUsage")).toHaveLength(1);
  });

  test("IT-072 distinguishes incomplete coverage from confirmed zero usage", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminLLMUsage", { data: { llmUsage: { from: "2026-01-01T00:00:00Z", to: "2026-01-02T00:00:00Z", attemptedCalls: 0, deliveredCalls: 0, incompleteCalls: 0, inputTokens: 0, outputTokens: 0, cachedInputTokens: 0, cacheHitCalls: 0, knownCacheCalls: 0, unknownCacheCalls: 1, knownReportedCost: null, unknownCostCalls: 1, costComplete: false, coverageStartedAt: "2026-01-01T00:00:00Z", coverageComplete: false, calls: [], pageInfo: pageInfo() } } });
    await loginAsLocalAdmin(page, "/llm-usage");
    await expect(page.getByText("Cobertura histórica parcial")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Nenhuma chamada encontrada" })).toBeVisible();
    expect(mocks.calls.filter((call) => call.operation === "AdminLLMUsage")).toHaveLength(1);
  });

  test("IT-073 hides global usage and cost details from a membership without cost access", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.setIdentity(activeIdentity("TENANT_ADMIN", false));
    await loginAsLocalAdmin(page, "/llm-usage");
    await expect(page.getByText("O consumo de LLM está disponível somente para o super admin.")).toBeVisible();
    await expect(page.getByText("Custo informado")).toHaveCount(0);
    expect(mocks.calls.some((call) => call.operation === "AdminLLMUsage")).toBe(false);
  });

  test("IT-074 discards a delayed result for a previous tenant filter", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminLLMUsage", usageReply("Tenant A", "inspection-old"), usageReply("Tenant A", "inspection-a"), usageReply("Tenant B", "inspection-b"));
    const tenantChoices = (nodes: Array<{ id: string; name: string }>) => ({ data: { llmUsageTenants: { nodes: nodes.map((tenant) => ({ ...tenant, language: "pt-BR", defaultTimezone: "UTC", status: "ACTIVE", version: 1 })), pageInfo: pageInfo() } } });
    const tenantA = { id: "tenant-a", name: "Tenant A" }; const tenantB = { id: "tenant-b", name: "Tenant B" };
    mocks.enqueue("AdminLLMUsageTenants", tenantChoices([tenantA, tenantB]), tenantChoices([tenantA, tenantB]), tenantChoices([tenantB]), tenantChoices([tenantB]));
    const releaseOldRequest = mocks.hold("AdminLLMUsage");
    await loginAsLocalAdmin(page, "/llm-usage");
    await page.locator(".llm-usage-advanced-filters > summary").click();
    const tenant = page.getByRole("textbox", { name: "Tenant", exact: true });
    await tenant.fill("Tenant A");
    await page.getByRole("button", { name: "Tenant A · tenant-a" }).click();
    await page.getByRole("button", { name: "Aplicar" }).click();
    await expect(page.getByRole("row").filter({ hasText: "Tenant A" })).toBeVisible();
    await tenant.fill("Tenant B");
    await page.getByRole("button", { name: "Tenant B · tenant-b" }).click();
    await page.getByRole("button", { name: "Aplicar" }).click();
    await expect(page.getByRole("row").filter({ hasText: "Tenant B" })).toBeVisible();
    releaseOldRequest();
    await expect(page.getByRole("row").filter({ hasText: "Tenant B" })).toBeVisible();
    await expect(page.getByRole("row").filter({ hasText: "Tenant A" })).toHaveCount(0);
  });

  test("IT-075 replaces prior usage with a clear refresh failure", async ({ page }) => {
    const mocks = await installMocks(page);
    mocks.enqueue("AdminLLMUsage", usageReply("Tenant A", "inspection-a"), { errors: [{ message: "Falha ao atualizar o consumo.", extensions: { code: "INTERNAL" } }] });
    await loginAsLocalAdmin(page, "/llm-usage");
    await expect(page.getByRole("row").filter({ hasText: "Tenant A" })).toBeVisible();
    await page.getByRole("button", { name: "Atualizar" }).click();
    await expect(page.getByRole("alert").getByText("Falha ao atualizar o consumo.")).toBeVisible();
    await expect(page.getByRole("table", { name: "Chamadas de LLM" })).toHaveCount(0);
  });
});
