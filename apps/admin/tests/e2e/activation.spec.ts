import { expect, test } from "@playwright/test";

test("E2E-011 activates an owner with a distinct OTP before normal PKCE login", async ({ page }) => {
  const calls: string[] = [];
  await page.route("**/graphql", async (route) => {
    const query = String((route.request().postDataJSON() as { query: string }).query);
    calls.push(query);
    const field = query.includes("RequestAdminActivationOtp") ? "requestAdminActivationOtp" : query.includes("VerifyAdminActivationOtp") ? "verifyAdminActivationOtp" : "setAdminInitialPassword";
    await route.fulfill({ headers: field === "requestAdminActivationOtp" ? { "X-CSRF-Token": "activation-proof" } : undefined, json: { data: { [field]: { userErrors: [] } } } });
  });
  await page.goto("/activate");
  await page.getByRole("button", { name: "Enviar código de ativação" }).click();
  await page.getByLabel("Código de ativação").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Nova senha", { exact: true }).fill("uma-senha-longa");
  await page.getByLabel("Confirme a nova senha").fill("uma-senha-longa");
  await page.getByRole("button", { name: "Criar senha" }).click();
  await expect(page.getByRole("button", { name: "Entrar na administração" })).toBeVisible();
  expect(calls).toHaveLength(3);
  expect(calls[0]).not.toContain("VerifyOnboardingOtp");
});
