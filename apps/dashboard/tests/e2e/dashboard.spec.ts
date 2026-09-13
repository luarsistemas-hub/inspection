import { expect, test } from "@playwright/test";

test("Painel starts with a safe authentication boundary", async ({ page }) => { await page.goto("/"); await expect(page.getByRole("heading", { name: "Painel" })).toBeVisible(); await expect(page.getByText("Nenhum dado operacional foi carregado.")).toBeVisible(); });
