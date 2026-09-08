import { expect, test } from "@playwright/test";

test("Dashboard starts with a safe authentication boundary", async ({ page }) => { await page.goto("/"); await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible(); await expect(page.getByText("Nenhum dado operacional foi carregado.")).toBeVisible(); });
