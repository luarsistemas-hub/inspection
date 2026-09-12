import { expect, test } from "@playwright/test";
import { OnboardingPage } from "./onboarding-page";

const definition = { schemaVersion: 1, version: 1, segment: "REAL_ESTATE", segmentVersion: "real-estate-v1", purposes: [], templates: [], analysisProfile: "default", originModes: [], steps: [
  { key: "agency", label: "Agência", position: 1, required: true, fields: [{ key: "name", label: "Nome da agência", type: "text", required: true, placeholder: null, options: [] }] },
] };

test("E2E-002 resumes the server-confirmed step in the same browser", async ({ page }) => {
  let verified = false;
  await page.route("**/graphql", async (route) => {
    const request = route.request();
    if (request.method() === "OPTIONS") return route.fulfill({ status: 204, headers: { "Access-Control-Allow-Origin": "http://localhost:3004", "Access-Control-Allow-Credentials": "true", "Access-Control-Allow-Headers": "Content-Type, X-CSRF-Token" } });
    const body = request.postDataJSON() as { query: string };
    if (body.query.includes("OnboardingDefinition")) return route.fulfill({ json: { data: { onboardingDefinition: definition } } });
    if (body.query.includes("RequestOnboardingOtp")) return route.fulfill({ json: { data: { requestOnboardingOtp: { sessionLocator: "opaque-locator", userErrors: [], clientMutationId: "request" } } } });
    if (body.query.includes("VerifyOnboardingOtp")) { verified = true; return route.fulfill({ headers: { "X-CSRF-Token": "csrf" }, json: { data: { verifyOnboardingOtp: { session: { id: "session", state: "IDENTITY_VERIFIED", currentStep: "agency", version: 1, expiresAt: "2030-01-01T00:00:00Z", definition: {} }, userErrors: [], clientMutationId: "verify" } } } }); }
    return route.fulfill({ json: { data: { onboardingSession: verified ? { id: "session", state: "IDENTITY_VERIFIED", currentStep: "agency", version: 1, expiresAt: "2030-01-01T00:00:00Z", definition: {} } : null } } });
  });
  const onboarding = new OnboardingPage(page);
  await onboarding.open();
  await onboarding.start("Ana", "ana@example.com");
  await onboarding.verify("123456");
  await onboarding.expectStep("Agência");
  await page.reload();
  await onboarding.expectStep("Agência");
  await expect(page.getByRole("button", { name: "Salvar e continuar" })).toBeVisible();
});
