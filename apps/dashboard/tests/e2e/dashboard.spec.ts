import { expect, test } from "@playwright/test";

test("Painel starts with a safe authentication boundary", async ({ page }) => { await page.goto("/"); await expect(page.getByRole("heading", { name: "Painel" })).toBeVisible(); await expect(page.getByText("Nenhum dado operacional foi carregado.")).toBeVisible(); });

const protectedScreens = [
  ["Agenda", "/schedules"], ["Vistorias", "/inspections"], ["Projetos", "/projects"],
  ["Triagem", "/triage"], ["Laudos", "/reports"], ["Portfólio", "/portfolio"],
  ["Notificações", "/notifications"], ["Tenant", "/tenants/playwright-smoke"],
] as const;

for (const [screen, route] of protectedScreens) {
  test(`${screen} keeps its screen behind the Dashboard authentication boundary`, async ({ page }) => {
    await page.goto(route);
    await expect(page.getByRole("heading", { name: "Painel" })).toBeVisible();
    await expect(page.getByRole("status")).toContainText("Nenhum dado operacional foi carregado.");
    await expect(page.getByRole("button", { name: "Entrar no Painel" })).toBeVisible();
  });
}
