import { expect, test } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin } from "./support/auth";

test.describe("Dashboard to Capture recapture journey", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");

  test("requests a replacement and records the authoritative operation outcome", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/inspections");
    await page.getByRole("button", { name: "Carregar inspeções" }).click();
    const item = page.locator("li").filter({ hasText: /SUBMITTED|COMPLETED|PENDING/ }).first();
    await expect(item).toBeVisible();
    page.once("dialog", (dialog) => void dialog.accept("evidence is insufficient"));
    page.once("dialog", (dialog) => void dialog.accept(new Date(Date.now() + 86_400_000).toISOString()));
    page.once("dialog", (dialog) => void dialog.accept("room-1"));
    await item.getByRole("button", { name: "Solicitar recaptura" }).click();
    await expect(page.getByRole("status")).toContainText(/Recaptura solicitada|Motivo|Prazo/);
    await assertRuntimeClean();
  });
});
