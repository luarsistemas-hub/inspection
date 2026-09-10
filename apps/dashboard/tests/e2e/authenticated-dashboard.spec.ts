import { expect, test } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin } from "./support/auth";

test.describe("authenticated Dashboard against the local stack", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");

  test("loads triage and seeded notifications, then marks one as read", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/triage");
    await page.getByRole("button", { name: "Atualizar prioridades" }).click();
    await expect(page.getByRole("status")).toContainText(/Resumo e fila atualizados|Não há trabalho/);
    await page.getByRole("navigation", { name: "Dashboard" }).getByRole("link", { name: /^Notificações(?: \(\d+\))?$/ }).click();
    const notice = page.getByRole("listitem").filter({ hasText: "Inspeção QA disponível" }).first();
    await expect(notice).toBeVisible();
    const markAsRead = notice.getByRole("button", { name: "Marcar como lida" });
    if (await markAsRead.isVisible()) await markAsRead.click();
    await expect(notice).toContainText("Lida");
    await assertRuntimeClean();
  });

  test("switches inspection views, preserves records and restores the preference", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    let inspectionRequests = 0;
    page.on("request", (request) => {
      if (request.url().endsWith("/graphql") && request.postData()?.includes("query Inspections")) inspectionRequests += 1;
    });
    await loginAsLocalAdmin(page, "/inspections");
    await page.getByRole("button", { name: "Carregar inspeções" }).click();
    await expect(page.getByText(/Inspeções atualizadas|Nenhuma inspeção encontrada/)).toBeVisible();

    const selector = page.getByRole("group", { name: "Visualização das inspeções" });
    const records = page.locator("[data-inspection-id]");
    await expect(records.first()).toBeVisible();
    const recordCount = await records.count();
    const requestsAfterLoad = inspectionRequests;

    for (const view of ["Quadro", "Agenda", "Lista"]) {
      await selector.getByRole("button", { name: view, exact: true }).click();
      await expect(selector.getByRole("button", { name: view, exact: true })).toHaveAttribute("aria-pressed", "true");
      await expect(page.locator("[data-inspection-id]")).toHaveCount(recordCount);
      await expect(page.locator(view === "Quadro" ? ".inspection-board" : view === "Agenda" ? ".inspection-agenda-layout" : ".inspection-table-box")).toBeVisible();
    }
    expect(inspectionRequests).toBe(requestsAfterLoad);

    await selector.getByRole("button", { name: "Quadro", exact: true }).click();
    await page.reload();
    await loginAsLocalAdmin(page, "/inspections");
    await expect(page.getByRole("button", { name: "Quadro", exact: true })).toHaveAttribute("aria-pressed", "true");
    await page.getByRole("button", { name: "Carregar inspeções" }).click();
    await expect(page.locator("[data-inspection-id]")).toHaveCount(recordCount);
    await page.locator(".inspection-action-menu summary").first().click();
    await expect(page.getByRole("button", { name: "Cancelar" }).first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Invalidar" }).first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Solicitar recaptura" }).first()).toBeVisible();

    await page.getByRole("button", { name: "Lista", exact: true }).focus();
    await expect(page.getByRole("button", { name: "Lista", exact: true })).toBeFocused();
    await assertRuntimeClean();
  });
});
