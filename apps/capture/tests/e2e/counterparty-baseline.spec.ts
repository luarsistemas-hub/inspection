import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

test.use({ serviceWorkers: "block" });

function baselineFixture(readOnly: boolean, newerVersionExists: boolean) {
  return { counterpartyBaseline: { deadlineAt: readOnly ? "2025-01-01T00:00:00Z" : "2099-01-01T00:00:00Z", response: "PENDING", respondedAt: null, resolution: "OPEN", resolutionReason: null, adjustmentStatus: null, newerVersionExists, readOnly, baseline: { hasInventory: true, environments: [{ typeCode: "BEDROOM", name: "Quarto", items: [{ typeCode: "BED", name: "Cama", material: "Madeira", color: "Branca", quantity: 1, condition: "GOOD", observation: "OBSERVED", limitation: null, evidence: [{ kind: "PHOTO", position: 1 }] }] }] } } };
}

async function configureCounterparty(page: import("@playwright/test").Page, options: { readOnly?: boolean; newer?: boolean; response?: "PENDING" | "ACKNOWLEDGED" | "CONTESTED" }) {
  let readOnly = options.readOnly ?? false;
  let responseStatus = options.response ?? "PENDING";
  let otpRequests = 0;
  await page.route("**/graphql", async (route) => {
    const { query = "" } = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestCounterpartyOtp")) { otpRequests += 1; return respond({ requestCounterpartyOtp: { status: "SENT", userErrors: [], clientMutationId: "otp" } }); }
    if (query.includes("verifyCounterpartyOtp")) return respond({ verifyCounterpartyOtp: { status: "VERIFIED", csrfToken: "counterparty-csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [], clientMutationId: "verify" } });
    if (query.includes("counterpartyBaseline")) return respond({ counterpartyBaseline: { ...baselineFixture(readOnly, options.newer ?? false).counterpartyBaseline, response: responseStatus } });
    if (query.includes("acknowledgeBaseline")) { responseStatus = "ACKNOWLEDGED"; return respond({ acknowledgeBaseline: { response: responseStatus, respondedAt: "2026-10-25T12:00:00Z", userErrors: [], clientMutationId: "ack" } }); }
    if (query.includes("contestBaseline")) { responseStatus = "CONTESTED"; readOnly = true; return respond({ contestBaseline: { response: responseStatus, respondedAt: "2026-10-25T12:00:00Z", userErrors: [], clientMutationId: "contest" } }); }
    throw new Error(`Unexpected operation: ${query.slice(0, 100)}`);
  });
  await page.goto("/baseline/counterparty-token");
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  expect(otpRequests).toBe(0);
  await page.getByRole("button", { name: "Enviar código" }).click();
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  return { setReadOnly(value: boolean) { readOnly = value; } };
}

test("E2E-012 counterparty OTP, baseline contest with 4,000-character counter and read-only deadline", async ({ page }) => {
  const counterparty = await configureCounterparty(page, {});
  await expect(page.getByRole("heading", { name: "Quarto" })).toBeVisible();
  await expect(page.getByText("Cama")).toBeVisible();
  await expect(page.locator("input[type=file]")).toHaveCount(0);
  const statement = page.getByLabel("Manifestação");
  await statement.fill("x".repeat(4001));
  await expect(page.getByText("4.001 / 4.000 caracteres")).toBeVisible();
  await expect(page.getByRole("button", { name: "Enviar manifestação" })).toBeDisabled();
  await statement.fill("Discordo da condição registrada.");
  await page.getByRole("button", { name: "Enviar manifestação" }).click();
  await expect(page.getByRole("heading", { name: "Obrigado" })).toBeVisible();

  counterparty.setReadOnly(true);
  await page.goto("/baseline/counterparty-token");
  await page.getByRole("button", { name: "Enviar código" }).click();
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByText("Prazo encerrado")).toBeVisible();
  await expect(page.getByRole("button", { name: "Confirmar ciência" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Enviar manifestação" })).toHaveCount(0);
  const accessibility = await new AxeBuilder({ page: page as never }).analyze();
  expect(accessibility.violations).toEqual([]);
});

test("E2E-013 counterparty acknowledgment and newer-version notice on reopen", async ({ page }) => {
  await configureCounterparty(page, { newer: true });
  await expect(page.getByText("Há uma versão mais recente desta vistoria.")).toBeVisible();
  await page.getByRole("button", { name: "Confirmar ciência" }).click();
  await expect(page.getByRole("heading", { name: "Obrigado" })).toBeVisible();
  await page.goto("/baseline/counterparty-token");
  await page.getByRole("button", { name: "Enviar código" }).click();
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByText("Há uma versão mais recente desta vistoria.")).toBeVisible();
  const accessibility = await new AxeBuilder({ page: page as never }).analyze();
  expect(accessibility.violations).toEqual([]);
});
