import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

const definition = {
  schemaVersion: 1,
  version: 4,
  segment: "REAL_ESTATE",
  segmentVersion: "real-estate-v1",
  purposes: ["SALE", "RENTAL"],
  templates: ["real-estate-checklist"],
  analysisType: "REAL_ESTATE",
  steps: [
    { key: "agency", label: "Imobiliária", position: 1, required: true, fields: [{ key: "name", label: "Nome da imobiliária", type: "text", required: true, placeholder: null, options: [], choices: [] }] },
    { key: "property", label: "Imóvel", position: 2, required: true, fields: [
      { key: "address", label: "Endereço do imóvel", type: "textarea", required: true, placeholder: null, options: [], choices: [] },
      { key: "propertyType", label: "Tipo de imóvel", type: "select", required: true, placeholder: null, options: ["APARTMENT", "HOUSE"], choices: [{ value: "APARTMENT", label: "Apartamento" }, { value: "HOUSE", label: "Casa" }] },
      { key: "purpose", label: "Finalidade da vistoria", type: "select", required: true, placeholder: null, options: ["SALE", "RENTAL"], choices: [{ value: "SALE", label: "Venda" }, { value: "RENTAL", label: "Locação" }] },
      { key: "deadline", label: "Prazo para concluir a vistoria", type: "date", required: true, placeholder: null, options: [], choices: [] },
    ] },
    { key: "participant", label: "Responsável pela vistoria", position: 3, required: true, fields: [
      { key: "mode", label: "Quem realizará a vistoria?", type: "select", required: true, placeholder: null, options: ["SELF", "DELEGATE"], choices: [{ value: "SELF", label: "Eu farei a vistoria" }, { value: "DELEGATE", label: "Outra pessoa fará a vistoria" }] },
      { key: "name", label: "Nome do responsável", type: "text", required: false, placeholder: null, options: [], choices: [] },
      { key: "email", label: "E-mail do responsável", type: "email", required: false, placeholder: null, options: [], choices: [] },
      { key: "emailConfirmation", label: "Confirme o e-mail do responsável", type: "email", required: false, placeholder: null, options: [], choices: [] },
    ] },
  ],
  originModes: [],
};

type Scenario = {
  existingAgency?: boolean;
  completed?: boolean;
  definition?: typeof definition;
  initialState?: string;
  initialStep?: string;
  requestErrors?: Array<{ code: string; field: string | null; message: string }>;
  verifyErrors?: Array<{ code: string; field: string | null; message: string }>;
  saveErrors?: Array<{ code: string; field: string | null; message: string }>;
  status?: Record<string, unknown>;
  completeMode?: "normal" | "lose-response";
  requestDelay?: number;
};

function session(state: string, currentStep: string, version: number, completedSteps: Record<string, unknown>, scenario: Scenario) {
  return { id: "session-migration", state, currentStep, version, expiresAt: "2099-01-01T00:00:00Z", definition: scenario.definition ?? definition, completedSteps, owner: { name: "Ana", email: "ana@example.test" }, existingAgency: scenario.existingAgency ? { tenantId: "tenant-1", businessUnitId: "unit-1", name: "Imobiliária existente", businessUnitCode: "IMOB", status: "ACTIVE" } : null };
}

async function mockOnboarding(page: Page, scenario: Scenario = {}) {
  let currentState = scenario.completed ? "SUBMITTED" : scenario.initialState ?? (scenario.initialStep ? "IDENTITY_VERIFIED" : "");
  let currentStep = scenario.completed ? "ready" : scenario.initialStep ?? "agency";
  let version = 1;
  const completedSteps: Record<string, unknown> = {};
  if (scenario.initialState === "PROPERTY_SAVED") {
    completedSteps.agency = { name: "Imobiliária Ana" };
    completedSteps.property = { address: "Rua do Fluxo, 123", propertyType: "APARTMENT", purpose: "RENTAL" };
  }
  let saveCalls = 0;
  let requestCalls = 0;
  const activeDefinition = scenario.definition ?? definition;
  const status = () => ({ state: currentState || "IDENTITY_VERIFIED", requestId: currentState === "SUBMITTED" ? "request-1" : null, inspectionId: currentState === "SUBMITTED" ? "inspection-1" : null, nextAction: "WAIT_FOR_DELIVERY", originStatus: "NOT_REQUIRED", deliveryStatus: "QUEUED", responsibleEmail: "ana@example.test", responsibilityStatus: "PENDING", responsibilityVersion: 1, deliveryFailureCode: null, canCorrectResponsibleEmail: false, updatedAt: "2099-01-01T00:00:00Z", ...scenario.status });
  await page.route("https://challenges.cloudflare.com/turnstile/v0/api.js**", (route) => route.fulfill({
    contentType: "application/javascript",
    body: 'window.turnstile={render:function(container,options){setTimeout(function(){options.callback("XXXX.DUMMY.TOKEN.XXXX")},0);return "playwright-widget"},reset:function(){},remove:function(){}};',
  }));
  await page.route("**/*graphql*", async (route) => {
    const body = JSON.parse(route.request().postData() ?? "{}") as { query?: string; variables?: { input?: { step?: string; payload?: Record<string, unknown> } } };
    const query = body.query ?? "";
    if (query.includes("OnboardingDefinition")) return route.fulfill({ json: { data: { onboardingDefinition: activeDefinition } } });
    if (query.includes("OnboardingSession")) return route.fulfill({ json: { data: { onboardingSession: currentState ? session(currentState, currentStep, version, completedSteps, scenario) : null } } });
    if (query.includes("RequestOnboardingOtp")) {
      requestCalls += 1;
      if (scenario.requestDelay) await new Promise((resolve) => setTimeout(resolve, scenario.requestDelay));
      return route.fulfill({ json: { data: { requestOnboardingOtp: { sessionLocator: scenario.requestErrors?.length ? null : "locator-1", userErrors: scenario.requestErrors ?? [], clientMutationId: "m1" } } } });
    }
    if (query.includes("VerifyOnboardingOtp")) {
      if (scenario.verifyErrors?.length) return route.fulfill({ json: { data: { verifyOnboardingOtp: { session: null, userErrors: scenario.verifyErrors, clientMutationId: "m2" } } } });
      currentState = "IDENTITY_VERIFIED";
      return route.fulfill({ json: { data: { verifyOnboardingOtp: { session: session(currentState, currentStep, version, completedSteps, scenario), userErrors: [], clientMutationId: "m2" } } } });
    }
    if (query.includes("SaveOnboardingStep")) {
      if (scenario.saveErrors?.length) return route.fulfill({ json: { data: { saveOnboardingStep: { session: null, status: null, userErrors: scenario.saveErrors, clientMutationId: "m3" } } } });
      const input = body.variables?.input;
      const step = input?.step ?? ["agency", "property", "participant"][saveCalls] ?? "";
      completedSteps[step] = input?.payload ?? {};
      currentStep = step;
      currentState = step === "agency" ? "AGENCY_SAVED" : step === "property" ? "PROPERTY_SAVED" : step === "participant" ? "PARTICIPANT_SAVED" : "PROPERTY_SAVED";
      version += 1;
      saveCalls += 1;
      return route.fulfill({ json: { data: { saveOnboardingStep: { session: session(currentState, currentStep, version, completedSteps, scenario), status: null, userErrors: [], clientMutationId: "m3" } } } });
    }
    if (query.includes("CompleteOnboarding")) {
      currentState = "SUBMITTED";
      currentStep = "ready";
      if (scenario.completeMode === "lose-response") return route.abort();
      return route.fulfill({ json: { data: { completeOnboarding: { session: session(currentState, currentStep, version + 1, completedSteps, scenario), request: { id: "request-1", status: "CREATED", assetId: "asset-1", participantId: "participant-1", originVersionId: "origin-1", templateId: "template-1" }, status: { state: "SUBMITTED", requestId: "request-1", inspectionId: "inspection-1", nextAction: "WAIT_FOR_DELIVERY", originStatus: "NOT_REQUIRED", deliveryStatus: "QUEUED", responsibleEmail: "ana@example.test", responsibilityStatus: "PENDING", responsibilityVersion: 1, deliveryFailureCode: null, canCorrectResponsibleEmail: false, updatedAt: "2099-01-01T00:00:00Z" }, userErrors: [], clientMutationId: "m4" } } } });
    }
    if (query.includes("OnboardingStatus")) return route.fulfill({ json: { data: { onboardingStatus: status() } } });
    return route.fulfill({ json: { data: {} } });
  });
  return { get saveCalls() { return saveCalls; }, get requestCalls() { return requestCalls; } };
}

async function completeChecklist(page: Page) {
  await page.goto("/");
  await page.getByLabel("Seu nome").fill("Ana");
  await page.getByLabel("Seu e-mail").fill("ana@example.test");
  await page.getByRole("button", { name: "Enviar código" }).click();
  await page.getByLabel("Código de confirmação").fill("123456");
  await page.getByRole("button", { name: "Confirmar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Imobiliária" })).toBeVisible();
  await page.getByLabel("Nome da imobiliária").fill("Imobiliária Ana");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Imóvel" })).toBeVisible();
  await page.getByLabel("Endereço do imóvel").fill("Rua do Fluxo, 123");
  await page.getByLabel("Tipo de imóvel").selectOption("APARTMENT");
  await page.getByLabel("Finalidade da vistoria").selectOption("RENTAL");
  await page.getByLabel("Prazo para concluir a vistoria").fill("2099-01-10");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Responsável pela vistoria" })).toBeVisible();
  await page.getByLabel("Quem realizará a vistoria?").selectOption("SELF");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Revise os dados" })).toBeVisible();
  await page.getByRole("button", { name: "Criar primeira vistoria" }).click();
}

test("mocked definition-driven onboarding reaches truthful status", async ({ page }) => {
  const calls = await mockOnboarding(page);
  await completeChecklist(page);
  await expect(page.getByText("Vistoria criada:")).toBeVisible();
  await expect(page.getByText("inspection-1", { exact: true })).toBeVisible();
  expect(calls.saveCalls).toBe(3);
});

test("IT-171 E2E-054 retained field drafts survive reload while server progression remains authoritative", async ({ page }) => {
  await mockOnboarding(page);
  await page.goto("/");
  await page.getByLabel("Seu nome").fill("Ana");
  await page.getByLabel("Seu e-mail").fill("ana@example.test");
  await page.getByRole("button", { name: "Enviar código" }).click();
  await page.getByLabel("Código de confirmação").fill("123456");
  await page.getByRole("button", { name: "Confirmar e continuar" }).click();
  await page.getByLabel("Nome da imobiliária").fill("Rascunho local");
  await page.reload();
  await expect(page.getByRole("heading", { name: "Imobiliária" })).toBeVisible();
  await expect(page.getByLabel("Nome da imobiliária")).toHaveValue("Rascunho local");
});

test("E2E-042 onboarding remains operable at 320, 360, 768 and 1440 CSS pixels", async ({ page }) => {
  await mockOnboarding(page);
  await page.goto("/");
  const info = page.getByRole("button", { name: "Informações sobre cadastro e salvamento" });
  await expect(info).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByText(/Os campos ficam neste navegador/)).toBeHidden();
  await info.focus();
  await info.press("Enter");
  await expect(page.getByText(/Os campos ficam neste navegador/)).toBeVisible();
  await info.press("Enter");
  await expect(page.getByText(/Os campos ficam neste navegador/)).toBeHidden();
  for (const width of [320, 360, 768, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    await expect(page.getByRole("heading", { name: "Comece sua primeira vistoria" })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth), `horizontal overflow at ${width}px`).toBe(false);
    const axePage = page as unknown as ConstructorParameters<typeof AxeBuilder>[0]["page"];
    const scan = await new AxeBuilder({ page: axePage }).withTags(["wcag2a", "wcag2aa"]).analyze();
    expect(scan.violations, `axe violations at ${width}px`).toEqual([]);
  }
  await page.setViewportSize({ width: 320, height: 900 });
  await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  const overflow = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    innerWidth,
    elements: [...document.querySelectorAll("body *")].filter((element) => {
      const rect = element.getBoundingClientRect();
      return rect.right > innerWidth + 1 || rect.left < -1;
    }).map((element) => ({ tag: element.tagName, className: typeof element.className === "string" ? element.className : "", right: Math.round(element.getBoundingClientRect().right) })).slice(0, 12),
  }));
  expect(overflow.scrollWidth <= overflow.innerWidth, `horizontal overflow with enlarged text: ${JSON.stringify(overflow)}`).toBe(true);
});

async function enterIdentity(page: Page) {
  await page.goto("/");
  await page.getByLabel("Seu nome").fill("Ana");
  await page.getByLabel("Seu e-mail").fill("ana@example.test");
  await page.getByRole("button", { name: "Enviar código" }).click();
}

async function verifyIdentity(page: Page) {
  await enterIdentity(page);
  await page.getByLabel("Código de confirmação").fill("123456");
  await page.getByRole("button", { name: "Confirmar e continuar" }).click();
}

test("IT-162 Onboarding mostra userErrors de e-mail e OTP nos campos correspondentes", async ({ page }) => {
  await mockOnboarding(page, { requestErrors: [{ code: "INVALID_INPUT", field: "email", message: "Informe um e-mail válido." }] });
  await page.goto("/");
  await page.getByLabel("Seu nome").fill("Ana");
  await page.getByLabel("Seu e-mail").fill("ana@");
  await page.getByRole("button", { name: "Enviar código" }).click();
  await expect(page.getByText("Informe um e-mail válido.")).toBeVisible();

  await page.unroute("**/*graphql*");
  await mockOnboarding(page, { verifyErrors: [{ code: "INVALID_INPUT", field: "code", message: "O código informado é inválido." }] });
  await enterIdentity(page);
  await page.getByLabel("Código de confirmação").fill("000000");
  await page.getByRole("button", { name: "Confirmar e continuar" }).click();
  await expect(page.getByText("O código informado é inválido.")).toBeVisible();
});

test("IT-163 restrição de reenvio do código é informada pela interface", async ({ page }) => {
  await mockOnboarding(page, { requestErrors: [{ code: "RATE_LIMITED", field: null, message: "Aguarde antes de tentar novamente." }] });
  await enterIdentity(page);
  await expect(page.getByText("Aguarde antes de tentar novamente.")).toBeVisible();
});

test("IT-164 sessão expirada antes de salvar exige nova verificação", async ({ page }) => {
  await mockOnboarding(page, { saveErrors: [{ code: "SESSION_EXPIRED", field: null, message: "Sua sessão expirou. Confirme seu e-mail novamente para continuar." }] });
  await verifyIdentity(page);
  await expect(page.getByRole("heading", { name: "Imobiliária" })).toBeVisible();
  await page.getByLabel("Nome da imobiliária").fill("Imobiliária Ana");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await expect(page.getByText("Sua sessão expirou. Confirme seu e-mail novamente para continuar.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Comece sua primeira vistoria" })).toBeVisible();
  await expect(page.getByLabel("Seu e-mail")).toHaveValue("");
});

test("IT-169 erro de validação da etapa permanece associado ao formulário", async ({ page }) => {
  await mockOnboarding(page, { saveErrors: [{ code: "INVALID_INPUT", field: "name", message: "Informe um nome válido para a imobiliária." }] });
  await verifyIdentity(page);
  await expect(page.getByRole("heading", { name: "Imobiliária" })).toBeVisible();
  await page.getByLabel("Nome da imobiliária").fill("x".repeat(256));
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await expect(page.getByText("Informe um nome válido para a imobiliária.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Imobiliária" })).toBeVisible();
});

test("IT-165 clique duplo durante o pedido de OTP envia uma única solicitação", async ({ page }) => {
  const calls = await mockOnboarding(page, { requestDelay: 500 });
  await page.goto("/");
  await page.getByLabel("Seu nome").fill("Ana");
  await page.getByLabel("Seu e-mail").fill("ana@example.test");
  await page.getByRole("button", { name: "Enviar código" }).dblclick();
  await expect(page.getByLabel("Código de confirmação")).toBeVisible();
  expect(calls.requestCalls).toBe(1);
});

test("IT-166 acesso direto a /complete sem sessão recupera pela entrada de cadastro", async ({ page }) => {
  await mockOnboarding(page);
  await page.goto("/complete");
  await expect(page.getByRole("heading", { name: "Comece sua primeira vistoria" })).toBeVisible();
  await expect(page.getByText("Nenhuma solicitação ativa foi encontrada.")).toBeVisible();
});

test("IT-167 confirmação diferente bloqueia o salvamento da responsabilidade DELEGATE", async ({ page }) => {
  const calls = await mockOnboarding(page);
  await verifyIdentity(page);
  await page.getByLabel("Nome da imobiliária").fill("Imobiliária Ana");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await page.getByLabel("Endereço do imóvel").fill("Rua do Fluxo, 123");
  await page.getByLabel("Tipo de imóvel").selectOption("APARTMENT");
  await page.getByLabel("Finalidade da vistoria").selectOption("RENTAL");
  await page.getByLabel("Prazo para concluir a vistoria").fill("2099-01-10");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await page.getByLabel("Quem realizará a vistoria?").selectOption("DELEGATE");
  await page.getByLabel("Nome do responsável").fill("Bia");
  await page.getByLabel("E-mail do responsável", { exact: true }).fill("ana@example.test");
  await page.getByLabel("Confirme o e-mail do responsável").fill("bia@example.test");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await expect(page.getByText("Os e-mails precisam ser iguais.")).toBeVisible();
  expect(calls.saveCalls).toBe(2);
});

test("IT-168 schemaVersion 3 não suportada apresenta estado indisponível com segurança", async ({ page }) => {
  await mockOnboarding(page, { definition: { ...definition, schemaVersion: 3 } });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Não foi possível abrir o cadastro" })).toBeVisible();
});

test("IT-170 PROPERTY_SAVED com etapa removida continua após o último checkpoint confirmado", async ({ page }) => {
  await page.addInitScript(() => sessionStorage.setItem("inspection.onboarding.draft.session-migration.participant", JSON.stringify({ mode: "DELEGATE", name: "Rascunho local", email: "bia@example.test" })));
  await mockOnboarding(page, { initialStep: "origin", initialState: "PROPERTY_SAVED" });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Responsável pela vistoria" })).toBeVisible();
  await expect(page.getByLabel("Nome do responsável")).toHaveValue("Rascunho local");
  await expect(page.getByText(/origem|fotos de referência/i)).toHaveCount(0);
});

test("IT-178 processamento e entrega pendente não aparecem como conclusão", async ({ page }) => {
  await mockOnboarding(page, { completed: true, status: { state: "SUBMITTED", deliveryStatus: "PENDING", requestId: "request-1", inspectionId: null } });
  await page.goto("/complete");
  await expect(page.getByRole("heading", { name: "Acompanhe a vistoria" })).toBeVisible();
  await expect(page.getByRole("status")).toContainText("A configuração foi aceita pelo servidor.");
  await expect(page.getByText("Vistoria criada", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Pendente", { exact: true })).toBeVisible();
});

test("IT-179 correção de e-mail não é oferecida quando o servidor a desabilita", async ({ page }) => {
  await mockOnboarding(page, { completed: true, status: { canCorrectResponsibleEmail: false } });
  await page.goto("/complete");
  await expect(page.getByRole("heading", { name: "Acompanhe a vistoria" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Corrigir e reenviar" })).toHaveCount(0);
});

test("IT-179 correção de e-mail é oferecida quando o servidor permite", async ({ page }) => {
  await mockOnboarding(page, { completed: true, status: { canCorrectResponsibleEmail: true } });
  await page.goto("/complete");
  await expect(page.getByRole("heading", { name: "Acompanhe a vistoria" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Corrigir e reenviar" })).toBeVisible();
});

test("IT-180 resposta de conclusão perdida é reconciliada antes de nova tentativa", async ({ page }) => {
  await mockOnboarding(page, { completeMode: "lose-response" });
  await completeChecklist(page);
  await expect(page.getByRole("heading", { name: "Acompanhe a vistoria" })).toBeVisible();
  await expect(page.getByText("A vistoria inspection-1 foi criada.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Criar primeira vistoria" })).toHaveCount(0);
});

test("IT-181 /complete com sessão aceita usa o estado retornado pelo servidor", async ({ page }) => {
  await mockOnboarding(page, { completed: true, status: { state: "SUBMITTED", deliveryStatus: "QUEUED", inspectionId: null } });
  await page.goto("/complete");
  await expect(page.getByRole("heading", { name: "Acompanhe a vistoria" })).toBeVisible();
  await expect(page.getByText("A vistoria inspection-1 foi criada.")).toHaveCount(0);
  await expect(page.getByText("A configuração foi aceita pelo servidor.")).toBeVisible();
});
