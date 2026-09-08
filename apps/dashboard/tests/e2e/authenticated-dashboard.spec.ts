import { expect, test } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin } from "./support/auth";

test.describe("authenticated Dashboard against the local stack", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");

  test("loads triage and seeded notifications, then marks one as read", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/inspections");
    await page.getByRole("button", { name: "Atualizar prioridades" }).click();
    await expect(page.getByRole("status")).toContainText(/Prioridades atualizadas|Não há trabalho/);
    await page.getByRole("link", { name: "Notificações", exact: true }).click();
    await expect(page.getByText("Inspeção QA disponível").first()).toBeVisible();
    await page.getByRole("button", { name: "Marcar como lida" }).first().click();
    await expect(page.getByText("Lida").first()).toBeVisible();
    await assertRuntimeClean();
  });
});
