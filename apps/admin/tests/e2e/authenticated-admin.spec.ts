import { expect, test } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin } from "./support/auth";

test.describe("authenticated Admin against the local stack", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");

  test("executes Organization, Access and Audit queries without GraphQL errors", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/organization");
    for (const label of ["Organização", "Acessos", "Auditoria"]) {
      await page.getByRole("link", { name: label }).click();
      await page.getByRole("button", { name: "Consultar" }).click();
      await expect(page.getByRole("status")).toContainText(/Consulta paginada concluída/);
    }
    await assertRuntimeClean();
  });
});
