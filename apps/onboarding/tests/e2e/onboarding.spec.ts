import { expect, test } from "@playwright/test";
import { OnboardingPage } from "./onboarding-page";

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
  await page.evaluate(() => (window as Window & { turnstileMockSolve?: (token: string) => void }).turnstileMockSolve?.("expired-token"));
  await expect(requestButton).toBeEnabled();
  await page.evaluate(() => (window as Window & { turnstileMockExpire?: () => void }).turnstileMockExpire?.());
  await expect(requestButton).toBeDisabled();
  await expect(page.getByText("A verificação expirou. Faça a verificação novamente.")).toBeVisible();
  await page.getByRole("button", { name: "Tentar novamente" }).click();
  await page.evaluate(() => (window as Window & { turnstileMockSolve?: (token: string) => void }).turnstileMockSolve?.("token-1"));
  await expect(requestButton).toBeEnabled();
  await page.getByLabel("Seu nome").fill("Ana Teste");
  await page.getByLabel("Seu e-mail").fill("ana@example.test");
  await requestButton.click();
  await expect(page.getByText("Conclua a verificação de segurança e tente novamente.")).toBeVisible();
  await expect(requestButton).toBeDisabled();
  await page.evaluate(() => (window as Window & { turnstileMockSolve?: (token: string) => void }).turnstileMockSolve?.("token-2"));
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
  await page.evaluate(() => (window as Window & { turnstileMockSolve?: (token: string) => void }).turnstileMockSolve?.("retry-token"));
  await expect(requestButton).toBeEnabled();
});

test("completes the real onboarding flow and creates the first vistoria", async ({ page }, testInfo) => {
  const onboarding = new OnboardingPage(page);
  const suffix = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const deadline = new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);

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
  await onboarding.saveProperty({ postalCode: "01001-000", street: "Praça da Sé", number: "123", district: "Sé", city: "São Paulo", state: "SP", propertyType: "APARTMENT", purpose: "RENTAL", deadline });
  await onboarding.expectStep("Fotos de referência");
  await onboarding.saveChecklistOrigin();
  await onboarding.expectStep("Responsável pela vistoria");
  await onboarding.saveSelfParticipant("Ana E2E", `ana.${suffix}@example.test`);
  await onboarding.submit();
  await onboarding.expectCreated();
});
