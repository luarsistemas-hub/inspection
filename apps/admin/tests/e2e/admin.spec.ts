import { expect, test } from "@playwright/test";

const routes = [
  ["E2E-005–008", "/organization", "Organização"], ["E2E-009–013", "/access", "Acessos"],
  ["E2E-014–017", "/catalogs", "Catálogos"], ["E2E-016", "/assets", "Ativos"], ["E2E-018–021", "/governance", "Governança"], ["E2E-022", "/audit", "Auditoria"]
] as const;

for (const [caseID, route, heading] of routes) test(`${caseID} keeps ${heading} behind the Admin guard`, async ({ page }) => {
  await page.goto(route);
  await expect(page.getByRole("heading", { name: "Administração" })).toBeVisible();
  await expect(page.getByText("Acesso administrativo não autorizado. Nenhuma configuração foi carregada.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Entrar com conta administrativa" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Ir para o Dashboard" })).toBeVisible();
});
