import { expect, test, type APIRequestContext } from "@playwright/test";
import { OnboardingPage } from "./onboarding-page";

type MailSummary = { ID: string; Subject: string; To: Array<{ Address: string }> };
type MailList = { messages: MailSummary[] };
type MailDetail = { Text: string };
const mailpitURL = process.env.PLAYWRIGHT_MAILPIT_URL ?? "http://localhost:8026";

async function latestMail(request: APIRequestContext, recipient: string, subject: string) {
  const response = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!response.ok()) return "";
  const list = await response.json() as MailList;
  const message = list.messages.find((item) => item.Subject === subject && item.To.some((to) => to.Address.toLowerCase() === recipient.toLowerCase()));
  if (!message) return "";
  const detail = await request.get(`${mailpitURL}/api/v1/message/${message.ID}`);
  return detail.ok() ? (((await detail.json()) as MailDetail).Text ?? "") : "";
}

async function mailIDs(request: APIRequestContext, recipient: string, subject: string) {
  const response = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!response.ok()) return new Set<string>();
  const list = await response.json() as MailList;
  return new Set(list.messages.filter((item) => item.Subject === subject && item.To.some((to) => to.Address.toLowerCase() === recipient.toLowerCase())).map((item) => item.ID));
}

// Returns the body of the first message for the recipient that was not in `existing`, so a shared QA mailbox cannot yield stale mail from earlier runs.
async function newMail(request: APIRequestContext, recipient: string, subject: string, existing: Set<string>) {
  const response = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!response.ok()) return "";
  const list = await response.json() as MailList;
  const message = list.messages.find((item) => !existing.has(item.ID) && item.Subject === subject && item.To.some((to) => to.Address.toLowerCase() === recipient.toLowerCase()));
  if (!message) return "";
  const detail = await request.get(`${mailpitURL}/api/v1/message/${message.ID}`);
  return detail.ok() ? (((await detail.json()) as MailDetail).Text ?? "") : "";
}

async function mailCount(request: APIRequestContext, recipient: string, subject: string) {
  const response = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  expect(response.ok()).toBeTruthy();
  const list = await response.json() as MailList;
  return list.messages.filter((item) => item.Subject === subject && item.To.some((to) => to.Address.toLowerCase() === recipient.toLowerCase())).length;
}

test("requires a fresh Turnstile token before requesting an onboarding code", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 844 });
  const submittedTokens: string[] = [];
  await page.route("**/graphql", async (route) => {
    const request = route.request().postDataJSON() as { query?: string; variables?: { input?: { turnstileToken?: string } } };
    if (request.query?.includes("OnboardingDefinition")) {
      await route.fulfill({ json: { data: { onboardingDefinition: { schemaVersion: 1, version: 1, segment: "REAL_ESTATE", segmentVersion: "1", steps: [{ key: "agency", label: "Imobiliária", position: 1, required: true, fields: [] }], purposes: [], originModes: [], templates: [], analysisType: "PHOTOS" } } } });
      return;
    }
    if (request.query?.includes("OnboardingSession")) {
      await route.fulfill({ json: { data: { onboardingSession: null } } });
      return;
    }
    if (request.query?.includes("RequestOnboardingOtp")) {
      const token = request.variables?.input?.turnstileToken;
      submittedTokens.push(token ?? "");
      if (submittedTokens.length === 1) {
        await route.fulfill({ json: { data: { requestOnboardingOtp: { sessionLocator: null, userErrors: [{ code: "INVALID_INPUT", field: "turnstileToken", message: "Conclua a verificação de segurança e tente novamente." }], clientMutationId: "test" } } } });
      } else {
        await route.fulfill({ json: { data: { requestOnboardingOtp: { sessionLocator: "opaque-locator", userErrors: [], clientMutationId: "test" } } } });
      }
      return;
    }
    await route.fulfill({ json: { data: {} } });
  });

  const onboarding = new OnboardingPage(page);
  await onboarding.open({ autoSolve: false });
  const requestButton = page.getByRole("button", { name: "Enviar código" });
  await expect(requestButton).toBeDisabled();
  await onboarding.solveTurnstile("expired-token");
  await expect(requestButton).toBeEnabled();
  await onboarding.expireTurnstile();
  await expect(requestButton).toBeDisabled();
  await expect(page.getByText("A verificação expirou. Faça a verificação novamente.")).toBeVisible();
  await page.getByRole("button", { name: "Tentar novamente" }).click();
  await onboarding.solveTurnstile("token-1");
  await expect(requestButton).toBeEnabled();
  await page.getByLabel("Seu nome").fill("Ana Teste");
  await page.getByLabel("Seu e-mail").fill("ana@example.test");
  await requestButton.click();
  await expect(page.getByText("Conclua a verificação de segurança e tente novamente.")).toBeVisible();
  await expect(requestButton).toBeDisabled();
  await onboarding.solveTurnstile("token-2");
  await expect(requestButton).toBeEnabled();
  await requestButton.click();
  await expect(page.getByLabel("Código de confirmação")).toBeVisible();
  expect(submittedTokens).toEqual(["token-1", "token-2"]);
});

test("reloads the Turnstile script after its first network failure", async ({ page }) => {
  let loads = 0;
  await page.route("**/graphql", async (route) => {
    const query = (route.request().postDataJSON() as { query?: string }).query ?? "";
    if (query.includes("OnboardingDefinition")) {
      await route.fulfill({ json: { data: { onboardingDefinition: { schemaVersion: 1, version: 1, segment: "REAL_ESTATE", segmentVersion: "1", steps: [{ key: "agency", label: "Imobiliária", position: 1, required: true, fields: [] }], purposes: [], originModes: [], templates: [], analysisType: "PHOTOS" } } } });
      return;
    }
    await route.fulfill({ json: { data: { onboardingSession: null } } });
  });
  page.on("request", (request) => {
    if (request.url().startsWith("https://challenges.cloudflare.com/turnstile/v0/api.js")) loads++;
  });
  await new OnboardingPage(page).open({ autoSolve: false, failFirstLoad: true });
  await expect(page.locator(".onboarding-turnstile-error")).toContainText("Não foi possível carregar a verificação de segurança");
  const requestButton = page.getByRole("button", { name: "Enviar código" });
  await expect(requestButton).toBeDisabled();
  await page.getByRole("button", { name: "Tentar novamente" }).click();
  await expect.poll(() => loads).toBe(2);
  await new OnboardingPage(page).solveTurnstile("retry-token");
  await expect(requestButton).toBeEnabled();
});

test("E2E-004 E2E-005 completes checklist onboarding and sends Capture by email", async ({ page, request }, testInfo) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requires the local Inspection API, Keycloak and Mailpit stack");
  const onboarding = new OnboardingPage(page);
  const suffix = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const deadline = new Date(Date.now() + 10 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);

  await page.route("**/graphql", async (route) => {
    const request = route.request().postDataJSON() as { query?: string };
    if (request.query?.includes("OnboardingLookupPostalCode")) {
      await route.fulfill({ json: { data: { lookupPostalCode: { found: true, postalCode: "01001000", street: "Praça da Sé", district: "Sé", city: "São Paulo", state: "SP", municipalityCode: "3550308" } } } });
      return;
    }
    await route.continue();
  });

  await onboarding.open();
  await page.getByLabel("Seu nome").fill("Ana E2E");
  await page.getByLabel("Seu e-mail").fill(`ana.${suffix}@example.test`);
  await page.getByRole("button", { name: "Ativar tema escuro" }).click();
  await expect(page.getByLabel("Seu nome")).toHaveValue("Ana E2E");
  await expect(page.getByRole("button", { name: "Enviar código" })).toBeEnabled();
  await page.getByRole("button", { name: "Enviar código" }).click();
  await onboarding.verify("654321");
  await onboarding.expectStep("Imobiliária");
  await onboarding.saveAgency(`Imobiliária E2E ${suffix}`);
  await onboarding.expectStep("Imóvel");

  // A full reload proves that the HttpOnly session can restore a fresh CSRF
  // proof and continue from the next server-confirmed step.
  await page.reload();
  await onboarding.expectStep("Imóvel");
  const defaultDeadline = new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
  const deadlineField = page.getByLabel("Prazo para concluir a vistoria");
  await expect(deadlineField).toHaveValue(defaultDeadline);
  await onboarding.saveProperty({ postalCode: "01001-000", street: "Praça da Sé", number: "123", district: "Sé", city: "São Paulo", state: "SP", propertyType: "APARTMENT", purpose: "RENTAL", deadline });
  await onboarding.expectStep("Responsável pela vistoria");
  await onboarding.saveSelfParticipant("Ana E2E", `ana.${suffix}@example.test`);
  await expect(page.getByRole("heading", { name: "Revise os dados" })).toBeVisible();
  await expect(page.getByText("Imóvel: Endereço do imóvel")).toBeVisible();
  await expect(page.getByText(/Praça da Sé/)).toBeVisible();
  await expect(page.getByText(deadline, { exact: true })).toBeVisible();
  await onboarding.submit();
  await onboarding.expectCreated();
  await expect(page.getByRole("heading", { name: "Acompanhe a vistoria" })).toBeVisible();
  await expect(page.getByLabel("Senha")).toHaveCount(0);
  let invitation = "";
  await expect.poll(async () => { invitation = await latestMail(request, `ana.${suffix}@example.test`, "Sua vistoria está disponível"); return invitation; }, { message: "owner Capture invitation to arrive in Mailpit", timeout: 60_000, intervals: [1_000, 2_000, 3_000] }).toContain("http");
});

test("E2E-007 delegate verifies the emailed invitation and reaches scoped Capture", async ({ page, request }, testInfo) => {
  test.setTimeout(150_000);
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requires the local Inspection API, Keycloak and Mailpit stack");
  const onboarding = new OnboardingPage(page);
  const suffix = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const deadline = new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
  await page.route("**/graphql", async (route) => {
    if ((route.request().postDataJSON() as { query?: string }).query?.includes("OnboardingLookupPostalCode")) {
      await route.fulfill({ json: { data: { lookupPostalCode: { found: true, postalCode: "01001000", street: "Praça da Sé", district: "Sé", city: "São Paulo", state: "SP", municipalityCode: "3550308" } } } });
      return;
    }
    await route.continue();
  });
  // Fora de produção o OTP por e-mail só é entregue ao destinatário de QA local (ChannelNotifier.SendOTP);
  // por isso o delegado usa esse endereço e o teste ignora mensagens anteriores da mesma caixa.
  const delegateEmail = "qa.inspection@example.test";
  const knownInvitations = await mailIDs(request, delegateEmail, "Sua vistoria está disponível");
  const knownOTPs = await mailIDs(request, delegateEmail, "Seu código de acesso");
  await onboarding.open();
  await onboarding.start("Ana Delegante", `ana.delegante.${suffix}@example.test`);
  await onboarding.verify("654321");
  await onboarding.saveAgency(`Imobiliária Delegada ${suffix}`);
  await onboarding.saveProperty({ postalCode: "01001-000", street: "Praça da Sé", number: "123", district: "Sé", city: "São Paulo", state: "SP", propertyType: "APARTMENT", purpose: "RENTAL", deadline });
  await page.getByLabel("Quem realizará a vistoria?").selectOption("DELEGATE");
  await page.getByLabel("Nome do responsável").fill("Bia Delegada");
  await page.getByLabel("E-mail do responsável", { exact: true }).fill(delegateEmail);
  await page.getByLabel("Confirme o e-mail do responsável").fill(delegateEmail);
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await onboarding.submit();
  await expect(page.getByRole("heading", { name: "Acompanhe a vistoria" })).toBeVisible();
  let invitation = "";
  await expect.poll(async () => { invitation = await newMail(request, delegateEmail, "Sua vistoria está disponível", knownInvitations); return invitation; }, { message: "delegated Capture invitation to arrive in Mailpit", timeout: 60_000, intervals: [1_000, 2_000, 3_000] }).toContain("http");
  const captureURL = invitation.match(/https?:\/\/[^\s<>"']+/)?.[0]?.replace(/[.,;]+$/, "");
  expect(captureURL).toBeTruthy();
  await page.goto(captureURL!);
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  let otpEmail = "";
  await expect.poll(async () => { otpEmail = await newMail(request, delegateEmail, "Seu código de acesso", knownOTPs); return otpEmail.match(/Código:\s*(\d{6})/)?.[1] ?? ""; }, { message: "delegate Capture OTP to arrive in Mailpit", timeout: 60_000, intervals: [1_000, 2_000, 3_000] }).toMatch(/^\d{6}$/);
  const otp = otpEmail.match(/Código:\s*(\d{6})/)?.[1] ?? "";
  await page.getByLabel("Código de seis dígitos").fill(otp);
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByLabel("Processamento das fotos")).toBeVisible();
  await page.getByLabel("Processamento das fotos").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Adicione as evidências" })).toBeVisible();
  await expect(page.getByText(/admin|configuração da imobiliária/i)).toHaveCount(0);
});

test("E2E-008 double submit and reload keep one onboarding request", async ({ page, request }, testInfo) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requires the local Inspection API, Keycloak and Mailpit stack");
  const onboarding = new OnboardingPage(page);
  const recipient = `ana.reenvio.${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}@example.test`;
  const suffix = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const deadline = new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
  let completeCalls = 0;
  await page.route("**/graphql", async (route) => {
    const request = route.request().postDataJSON() as { query?: string };
    if (request.query?.includes("OnboardingLookupPostalCode")) {
      await route.fulfill({ json: { data: { lookupPostalCode: { found: true, postalCode: "01001000", street: "Praça da Sé", district: "Sé", city: "São Paulo", state: "SP", municipalityCode: "3550308" } } } });
      return;
    }
    if (request.query?.includes("CompleteOnboarding")) {
      completeCalls++;
      await new Promise((resolve) => setTimeout(resolve, 700));
    }
    await route.continue();
  });
  await onboarding.open();
  await onboarding.start("Ana Reenvio", recipient);
  await onboarding.verify("654321");
  await onboarding.saveAgency(`Imobiliária Reenvio ${suffix}`);
  await onboarding.saveProperty({ postalCode: "01001-000", street: "Praça da Sé", number: "123", district: "Sé", city: "São Paulo", state: "SP", propertyType: "APARTMENT", purpose: "RENTAL", deadline });
  await onboarding.saveSelfParticipant("Ana Reenvio", recipient);
  const submit = page.getByRole("button", { name: "Criar primeira vistoria" });
  await submit.dblclick();
  await expect(page.getByRole("heading", { name: "Acompanhe a vistoria" })).toBeVisible();
  const inspectionText = await page.getByText(/vistoria [0-9a-f-]{36}/i).textContent();
  const inspectionID = inspectionText?.match(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i)?.[0];
  expect(inspectionID).toBeTruthy();
  await expect.poll(() => mailCount(request, recipient, "Sua vistoria está disponível")).toBe(1);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Acompanhe a vistoria" })).toBeVisible();
  const reloadedInspectionText = await page.getByText(/vistoria [0-9a-f-]{36}/i).textContent();
  const reloadedInspectionID = reloadedInspectionText?.match(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i)?.[0];
  expect(reloadedInspectionID).toBe(inspectionID);
  expect(await mailCount(request, recipient, "Sua vistoria está disponível")).toBe(1);
  expect(completeCalls).toBe(1);
});

type RealJourneyOptions = { delegate?: boolean; agencyName?: string; email?: string; path?: string };

async function completeRealJourney(page: import("@playwright/test").Page, testInfo: import("@playwright/test").TestInfo, options: RealJourneyOptions = {}) {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local de Inspection, Keycloak e Mailpit");
  const onboarding = new OnboardingPage(page);
  const suffix = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const email = options.email ?? `ana.dsm.${suffix}@example.test`;
  const agency = options.agencyName ?? `Imobiliária DSM ${suffix}`;
  const deadline = new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
  await page.route("**/graphql", async (route) => {
    if ((route.request().postDataJSON() as { query?: string }).query?.includes("OnboardingLookupPostalCode")) {
      await route.fulfill({ json: { data: { lookupPostalCode: { found: true, postalCode: "01001000", street: "Praça da Sé", district: "Sé", city: "São Paulo", state: "SP", municipalityCode: "3550308" } } } });
      return;
    }
    await route.continue();
  });
  await onboarding.open({ path: options.path });
  await onboarding.start("Ana DSM", email);
  await onboarding.verify("654321");
  await onboarding.saveAgency(agency);
  await onboarding.saveProperty({ postalCode: "01001-000", street: "Praça da Sé", number: "123", district: "Sé", city: "São Paulo", state: "SP", propertyType: "APARTMENT", purpose: "RENTAL", deadline });
  if (options.delegate) {
    await page.getByLabel("Quem realizará a vistoria?").selectOption("DELEGATE");
    await page.getByLabel("Nome do responsável").fill("Bia DSM");
    await page.getByLabel("E-mail do responsável", { exact: true }).fill(`bia.${suffix}@example.test`);
    await page.getByLabel("Confirme o e-mail do responsável").fill(`bia.${suffix}@example.test`);
    await page.getByRole("button", { name: "Salvar e continuar" }).click();
  } else {
    await onboarding.saveSelfParticipant("Ana DSM", email);
  }
  await onboarding.submit();
  return { email, agency, suffix, deadline };
}

test("DSM E2E-033 identidade real abre uma sessão e avança com confirmação do servidor", async ({ page }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local de Inspection, Keycloak e Mailpit");
  const onboarding = new OnboardingPage(page);
  await onboarding.open();
  await onboarding.start("Ana DSM", `ana.identidade.${Date.now()}@example.test`);
  await onboarding.verify("654321");
  await onboarding.expectStep("Imobiliária");
});

test("DSM E2E-034 responsabilidade delegada real aceita e-mail confirmado", async ({ page }, testInfo) => {
  test.setTimeout(150_000);
  await completeRealJourney(page, testInfo, { delegate: true, path: "/imobiliaria" });
  await onboardingExpectCreated(page);
  await expect(page.getByRole("definition")).toHaveCount(4);
  const invitationStatus = page.getByRole("term").filter({ hasText: /^Convite$/ }).locator("xpath=following-sibling::dd[1]");
  const refresh = page.getByRole("button", { name: "Consultar situação" });
  // The invitation is queued asynchronously and the UI only polls every 30 s while NOT_STARTED.
  await expect.poll(async () => {
    const text = (await invitationStatus.textContent()) ?? "";
    if (!/Na fila de envio|Convite enviado|Convite entregue|Envio aceito/.test(text) && await refresh.isVisible()) await refresh.click({ timeout: 2_000 }).catch(() => undefined);
    return text;
  }, { message: "delegated invitation status to leave NOT_STARTED", timeout: 45_000, intervals: [1_000, 2_000, 3_000] }).toMatch(/Na fila de envio|Convite enviado|Convite entregue|Envio aceito/);
});

test("DSM E2E-035 fluxo atual sem etapa de origem conclui e mostra o estado real", async ({ page }, testInfo) => {
  await completeRealJourney(page, testInfo);
  await onboardingExpectCreated(page);
  await expect(page.getByText("Vistoria criada:")).toBeVisible();
  await expect(page.getByLabel("Senha")).toHaveCount(0);
});

test("DSM E2E-036 /complete consulta status real após a conclusão", async ({ page }, testInfo) => {
  test.setTimeout(150_000);
  await completeRealJourney(page, testInfo, { delegate: true });
  // Navegar antes da resposta cancelaria o CompleteOnboarding em andamento (context canceled) e a sessão nunca seria concluída.
  await onboardingExpectCreated(page);
  await page.goto("/complete");
  await expect(page.getByRole("heading", { name: "Acompanhe a vistoria" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Consultar situação" })).toBeVisible();
  const refresh = page.getByRole("button", { name: "Consultar situação" });
  // The request is confirmed asynchronously (outbox → worker); poll the real status instead of the 5 s default.
  await expect.poll(async () => {
    const text = (await page.getByRole("status").first().textContent()) ?? "";
    if (/Aguardando confirmação/.test(text) && await refresh.isVisible()) await refresh.click({ timeout: 2_000 }).catch(() => undefined);
    return text;
  }, { message: "real onboarding status after completion", timeout: 60_000, intervals: [1_000, 2_000, 3_000] }).toMatch(/Convite|Configuração aceita|Vistoria criada/);
});

test("DSM E2E-050 reinício real confirma descarte dos campos ainda não enviados", async ({ page }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local de Inspection, Keycloak e Mailpit");
  const onboarding = new OnboardingPage(page);
  const email = `ana.reinicio.${Date.now()}@example.test`;
  await onboarding.open();
  await onboarding.start("Ana Reinício", email);
  await onboarding.verify("654321");
  await page.getByLabel("Nome da imobiliária").fill("Rascunho descartável");
  await page.getByRole("button", { name: "Iniciar novo cadastro" }).click();
  await expect(page.getByRole("heading", { name: "Iniciar novo cadastro?" })).toBeVisible();
  const logoutResponse = page.waitForResponse((response) => /\/onboarding\/logout(?:\?|$)/.test(response.url()));
  await page.getByRole("button", { name: "Descartar e começar novamente" }).click();
  await logoutResponse;
  await expect(page.getByRole("heading", { name: "Comece sua primeira vistoria" })).toBeVisible();
  await expect(page.getByLabel("Seu e-mail")).toHaveValue("");
  await page.reload();
  await expect(page.getByRole("heading", { name: "Comece sua primeira vistoria" })).toBeVisible();
  await expect(page.getByLabel("Nome da imobiliária")).toHaveCount(0);
  expect(await page.evaluate(() => Object.keys(sessionStorage).filter((key) => key.startsWith("inspection.onboarding.draft.")))).toEqual([]);
});

test("DSM E2E-055 agência existente é reutilizada em uma nova sessão real", async ({ page }, testInfo) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local de Inspection, Keycloak e Mailpit");
  const initial = await completeRealJourney(page, testInfo);
  await onboardingExpectCreated(page);
  await page.getByRole("button", { name: "Iniciar novo cadastro" }).click();
  await page.getByRole("button", { name: "Descartar e começar novamente" }).click();
  await page.getByLabel("Seu nome").fill("Ana DSM");
  await page.getByLabel("Seu e-mail").fill(initial.email);
  await page.getByRole("button", { name: "Enviar código" }).click();
  await page.getByLabel("Código de confirmação").fill("654321");
  await page.getByRole("button", { name: "Confirmar e continuar" }).click();
  await expect(page.getByText(`Encontramos a imobiliária ${initial.agency} vinculada a este e-mail.`)).toBeVisible();
  await expect(page.getByLabel("Nome da imobiliária")).toHaveValue(initial.agency);
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Imóvel", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Imobiliária", exact: true })).toHaveCount(0);
});

async function onboardingExpectCreated(page: import("@playwright/test").Page) {
  await expect(page.getByRole("heading", { name: "Acompanhe a vistoria" })).toBeVisible();
  await expect(page.getByText(/A vistoria .* foi criada\./)).toBeVisible();
}
