import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

const definition = {
  schemaVersion: 1,
  version: 4,
  segment: "REAL_ESTATE",
  segmentVersion: "real-estate-v1",
  purposes: ["SALE", "RENTAL"],
  templates: ["real-estate-checklist", "real-estate-fixed-origin"],
  analysisType: "REAL_ESTATE",
  steps: [
    { key: "agency", label: "Imobiliária", position: 1, required: true, fields: [{ key: "name", label: "Nome da imobiliária", type: "text", required: true, placeholder: null, options: [], choices: [] }] },
    { key: "property", label: "Imóvel", position: 2, required: true, fields: [
      { key: "address", label: "Endereço do imóvel", type: "textarea", required: true, placeholder: null, options: [], choices: [] },
      { key: "propertyType", label: "Tipo de imóvel", type: "select", required: true, placeholder: null, options: ["APARTMENT", "HOUSE"], choices: [{ value: "APARTMENT", label: "Apartamento" }, { value: "HOUSE", label: "Casa" }] },
      { key: "purpose", label: "Finalidade da vistoria", type: "select", required: true, placeholder: null, options: ["SALE", "RENTAL"], choices: [{ value: "SALE", label: "Venda" }, { value: "RENTAL", label: "Locação" }] },
      { key: "deadline", label: "Prazo para concluir a vistoria", type: "date", required: true, placeholder: null, options: [], choices: [] },
    ] },
    { key: "origin", label: "Fotos de referência", position: 3, required: true, fields: [{ key: "mode", label: "Base de comparação", type: "select", required: true, placeholder: null, options: ["CHECKLIST_ONLY", "FIXED_ORIGIN"], choices: [{ value: "CHECKLIST_ONLY", label: "Registrar estado inicial" }, { value: "FIXED_ORIGIN", label: "Comparar com fotos de referência" }] }] },
    { key: "participant", label: "Responsável pela vistoria", position: 4, required: true, fields: [
      { key: "mode", label: "Quem realizará a vistoria?", type: "select", required: true, placeholder: null, options: ["SELF", "DELEGATE"], choices: [{ value: "SELF", label: "Eu farei a vistoria" }, { value: "DELEGATE", label: "Outra pessoa fará a vistoria" }] },
      { key: "name", label: "Nome do responsável", type: "text", required: false, placeholder: null, options: [], choices: [] },
      { key: "email", label: "E-mail do responsável", type: "email", required: false, placeholder: null, options: [], choices: [] },
      { key: "emailConfirmation", label: "Confirme o e-mail do responsável", type: "email", required: false, placeholder: null, options: [], choices: [] },
    ] },
  ],
  originModes: [{ key: "CHECKLIST_ONLY", label: "Registrar estado inicial", templateKey: "real-estate-checklist", required: false }, { key: "FIXED_ORIGIN", label: "Comparar com fotos de referência", templateKey: "real-estate-fixed-origin", required: true }],
};

type Scenario = { existingAgency?: boolean; completed?: boolean };

function session(state: string, currentStep: string, version: number, completedSteps: Record<string, unknown>, scenario: Scenario) {
  return { id: "session-migration", state, currentStep, version, expiresAt: "2099-01-01T00:00:00Z", definition, completedSteps, owner: { name: "Ana", email: "ana@example.test" }, existingAgency: scenario.existingAgency ? { tenantId: "tenant-1", businessUnitId: "unit-1", name: "Imobiliária existente", businessUnitCode: "IMOB", status: "ACTIVE" } : null };
}

async function mockOnboarding(page: Page, scenario: Scenario = {}) {
  let currentState = scenario.completed ? "SUBMITTED" : "";
  let currentStep = scenario.completed ? "ready" : "agency";
  let version = 1;
  const completedSteps: Record<string, unknown> = {};
  let saveCalls = 0;
  await page.route("**/*graphql*", async (route) => {
    const body = JSON.parse(route.request().postData() ?? "{}") as { query?: string; variables?: { input?: { step?: string; payload?: Record<string, unknown> } } };
    const query = body.query ?? "";
    if (query.includes("OnboardingDefinition")) return route.fulfill({ json: { data: { onboardingDefinition: definition } } });
    if (query.includes("OnboardingSession")) return route.fulfill({ json: { data: { onboardingSession: currentState ? session(currentState, currentStep, version, completedSteps, scenario) : null } } });
    if (query.includes("RequestOnboardingOtp")) return route.fulfill({ json: { data: { requestOnboardingOtp: { sessionLocator: "locator-1", userErrors: [], clientMutationId: "m1" } } } });
    if (query.includes("VerifyOnboardingOtp")) {
      currentState = "IDENTITY_VERIFIED";
      return route.fulfill({ json: { data: { verifyOnboardingOtp: { session: session(currentState, "agency", version, completedSteps, scenario), userErrors: [], clientMutationId: "m2" } } } });
    }
    if (query.includes("SaveOnboardingStep")) {
      const input = body.variables?.input;
      const step = input?.step ?? ["agency", "property", "origin", "participant"][saveCalls] ?? "";
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
      return route.fulfill({ json: { data: { completeOnboarding: { session: session(currentState, currentStep, version + 1, completedSteps, scenario), request: { id: "request-1", status: "CREATED", assetId: "asset-1", participantId: "participant-1", originVersionId: "origin-1", templateId: "template-1" }, status: { state: "SUBMITTED", requestId: "request-1", inspectionId: "inspection-1", nextAction: "WAIT_FOR_DELIVERY", originStatus: "NOT_REQUIRED", deliveryStatus: "QUEUED", responsibleEmail: "ana@example.test", responsibilityStatus: "PENDING", responsibilityVersion: 1, deliveryFailureCode: null, canCorrectResponsibleEmail: false, updatedAt: "2099-01-01T00:00:00Z" }, userErrors: [], clientMutationId: "m4" } } } });
    }
    if (query.includes("OnboardingStatus")) return route.fulfill({ json: { data: { onboardingStatus: { state: currentState || "IDENTITY_VERIFIED", requestId: currentState === "SUBMITTED" ? "request-1" : null, inspectionId: currentState === "SUBMITTED" ? "inspection-1" : null, nextAction: "WAIT_FOR_DELIVERY", originStatus: "NOT_REQUIRED", deliveryStatus: "QUEUED", responsibleEmail: "ana@example.test", responsibilityStatus: "PENDING", responsibilityVersion: 1, deliveryFailureCode: null, canCorrectResponsibleEmail: false, updatedAt: "2099-01-01T00:00:00Z" } } } });
    return route.fulfill({ json: { data: {} } });
  });
  return { get saveCalls() { return saveCalls; } };
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
  await expect(page.getByRole("heading", { name: "Fotos de referência" })).toBeVisible();
  await page.getByLabel("Base de comparação").selectOption("CHECKLIST_ONLY");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Responsável pela vistoria" })).toBeVisible();
  await page.getByLabel("Quem realizará a vistoria?").selectOption("SELF");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Revise os dados" })).toBeVisible();
  await page.getByRole("button", { name: "Criar primeira vistoria" }).click();
}

test("E2E-033 E2E-034 E2E-036 definition-driven onboarding reaches truthful status", async ({ page }) => {
  test.skip(process.env.INSPECTION_E2E_MOCKS !== "true", "set INSPECTION_E2E_MOCKS=true to run the deterministic onboarding contract flow");
  const calls = await mockOnboarding(page);
  await completeChecklist(page);
  await expect(page.getByText("Vistoria criada:")).toBeVisible();
  await expect(page.getByText("inspection-1", { exact: true })).toBeVisible();
  expect(calls.saveCalls).toBe(4);
});

test("IT-171 E2E-054 retained field drafts survive reload while server progression remains authoritative", async ({ page }) => {
  test.skip(process.env.INSPECTION_E2E_MOCKS !== "true", "set INSPECTION_E2E_MOCKS=true to run the deterministic onboarding contract flow");
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
  test.skip(process.env.INSPECTION_E2E_MOCKS !== "true", "set INSPECTION_E2E_MOCKS=true to run the deterministic onboarding contract flow");
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

test("E2E keeps the unsent reference photo reload warning next to its file picker", async ({ page }) => {
  test.skip(process.env.INSPECTION_E2E_MOCKS !== "true", "set INSPECTION_E2E_MOCKS=true to run the deterministic onboarding contract flow");
  await mockOnboarding(page);
  await page.goto("/");
  await page.getByLabel("Seu nome").fill("Ana");
  await page.getByLabel("Seu e-mail").fill("ana@example.test");
  await page.getByRole("button", { name: "Enviar código" }).click();
  await page.getByLabel("Código de confirmação").fill("123456");
  await page.getByRole("button", { name: "Confirmar e continuar" }).click();
  await page.getByLabel("Nome da imobiliária").fill("Imobiliária Ana");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await page.getByLabel("Endereço do imóvel").fill("Rua do Fluxo, 123");
  await page.getByLabel("Tipo de imóvel").selectOption("APARTMENT");
  await page.getByLabel("Finalidade da vistoria").selectOption("RENTAL");
  await page.getByLabel("Prazo para concluir a vistoria").fill("2099-01-10");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await page.getByLabel("Base de comparação").selectOption("FIXED_ORIGIN");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await page.getByLabel("Fotos de referência").setInputFiles({ name: "referencia.png", mimeType: "image/png", buffer: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/nwAAAABJRU5ErkJggg==", "base64") });
  await expect(page.getByText("Cada foto precisa de uma descrição antes de ser enviada.")).toBeVisible();
  await expect(page.getByRole("img", { name: "Fotos ainda não enviadas precisarão ser selecionadas novamente se a página for recarregada." })).toBeVisible();
});
