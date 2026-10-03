import { expect, test } from "@playwright/test";
import { loginAsLocalAdmin } from "./support/auth";

test.describe("cross-product customer handoff", () => {
  test.skip(process.env.INSPECTION_E2E_CROSS_PRODUCT !== "true", "real cross-product credentials are required; use the parity gate");

  test("E2E-047 switches to the customer projection before opening shared work", async ({ page }) => {
    await loginAsLocalAdmin(page, "/reports");
    const contextPicker = page.getByLabel("Contexto de acesso");
    await expect(contextPicker).toBeVisible();
    const customerOption = contextPicker.locator("option").filter({ hasText: "Visualizador cliente" });
    await expect(customerOption).toHaveCount(1);
    await contextPicker.selectOption((await customerOption.getAttribute("value")) ?? "");

    await page.goto("/portfolio");
    await page.getByRole("button", { name: "Carregar portfólio" }).click();
    await expect(page.getByRole("status")).toContainText(/Portfólio autorizado|Ainda não há itens publicados|não está disponível/);

    await expect(page.getByRole("link", { name: "Abrir Administração" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: /Publicar|Invalidar|Solicitar complemento|Promover/ })).toHaveCount(0);
  });
});
