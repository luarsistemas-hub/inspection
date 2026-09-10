import { expect, test } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin } from "./support/auth";

test.describe("authenticated Admin against the local stack", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");

  test("executes every Admin query without permission or GraphQL errors", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/organization");
    await expect(page.locator(".admin-header")).toContainText("Inspection / Admin");
    await expect(page.getByRole("navigation", { name: "Navegação administrativa" }).getByRole("link", { name: "Organização" })).toHaveAttribute("aria-current", "page");
    await expect(page.getByRole("columnheader", { name: "Unidade" })).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "ID", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Abrir detalhes →" }).first()).toBeVisible();
    await expect(page.getByText("Você não tem permissão para acessar este recurso neste escopo.")).toHaveCount(0);
    for (const [label, route] of [["Visão geral", "/overview"], ["Organização", "/organization"], ["Usuários e acessos", "/access"], ["Participantes", "/catalogs"], ["Configuração", "/assets"], ["Governança", "/governance"], ["Auditoria", "/audit"]] as const) {
      await page.getByRole("link", { name: label }).click();
      await expect(page).toHaveURL(new RegExp(`${route}(?:\\?|$)`));
      await expect(page.locator(".status-line")).toContainText("atualizado");
    }
    await assertRuntimeClean();
  });
});
