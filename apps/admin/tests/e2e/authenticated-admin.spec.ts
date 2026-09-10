import { expect, test } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin } from "./support/auth";

test.describe("authenticated Admin against the local stack", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");

  test("executes every Admin query without permission or GraphQL errors", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/organization");
    await expect(page.locator(".admin-header")).toContainText("Papel: TENANT_ADMIN");
    await expect(page.getByText("Você não tem permissão para acessar este recurso neste escopo.")).toHaveCount(0);
    for (const label of ["Resumo do tenant", "Tenant e unidades", "Usuários e permissões", "Participantes e segmentos", "Templates, perfis e ativos", "Políticas e entregas", "Auditoria e uso"]) {
      await page.getByRole("link", { name: label }).click();
      await page.getByRole("button", { name: "Atualizar" }).click();
      await expect(page.locator(".status-line")).toContainText(/atualizado|Carregando/);
    }
    await assertRuntimeClean();
  });
});
