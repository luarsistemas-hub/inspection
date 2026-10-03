import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { expect, test } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin } from "./support/auth";

test.describe("authenticated Dashboard against the local stack", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");

  test("shows unread notifications first, allows all notifications, and opens delivery status", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    execFileSync("./scripts/local.sh", ["seed"], { cwd: resolve(process.cwd(), "../.."), encoding: "utf8" });
    await loginAsLocalAdmin(page, "/triage");
    await page.getByRole("button", { name: "Atualizar fila" }).click();
    await expect(page.getByRole("region", { name: "Casos para revisão" }).locator(".triage-list-heading strong")).not.toHaveText("Carregando fila…");
    const primaryNavigation = page.getByRole("navigation", { name: "Painel" });
    const notificationLink = primaryNavigation.getByRole("link", { name: /^Notificações(?: \(\d+\))?$/ });
    if (await notificationLink.isVisible()) {
      await notificationLink.click();
    } else {
      await page.getByRole("button", { name: /^Mais destinos/ }).click();
      await page.getByRole("navigation", { name: "Painel — outros destinos" }).getByRole("link", { name: "Notificações", exact: true }).click();
    }
    const matchingNotices = page.locator(".notification-card").filter({ hasText: "Vistoria a iniciar · Imóvel QA" });
    const notice = matchingNotices.first();
    await expect(notice).toBeVisible({ timeout: 15_000 });
    const unreadNoticeCount = await matchingNotices.count();
    const markAsRead = notice.getByRole("button", { name: "Marcar como lida" });
    if (await markAsRead.isVisible()) {
      await markAsRead.click();
      await expect(matchingNotices).toHaveCount(unreadNoticeCount - 1);
      await page.getByRole("button", { name: "Todas", exact: true }).click();
      const readNotice = page.locator('.notification-card[data-read="true"]').filter({ hasText: "Vistoria a iniciar · Imóvel QA" }).first();
      await expect(readNotice).toContainText("Lida");
      await page.getByRole("button", { name: /Não lidas/ }).click();
      await expect(matchingNotices).toHaveCount(unreadNoticeCount - 1);
    }
    await page.getByRole("button", { name: "Acompanhar envios" }).click();
    const dialog = page.getByRole("dialog", { name: "Estado das entregas" });
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText("Solicitada, em processamento, aceita, enviada e entregue");
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await assertRuntimeClean();
  });

  test("switches inspection views, preserves records and restores the preference", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    let inspectionRequests = 0;
    page.on("request", (request) => {
      if (request.url().endsWith("/graphql") && request.postData()?.includes("query Inspections")) inspectionRequests += 1;
    });
    await loginAsLocalAdmin(page, "/inspections");
    await expect(page.getByText(/Vistorias atualizadas|Nenhuma vistoria encontrada/)).toBeVisible();

    const selector = page.getByRole("group", { name: "Visualização das vistorias" });
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
    await expect(page.getByText(/Vistorias atualizadas|Nenhuma vistoria encontrada/)).toBeVisible();
    await expect(page.locator("[data-inspection-id]")).toHaveCount(recordCount);
    await page.locator(".inspection-action-menu summary").first().click();
    await expect(page.getByRole("button", { name: "Cancelar" }).first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Invalidar" }).first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Solicitar complemento" }).first()).toBeVisible();

    await page.getByRole("button", { name: "Lista", exact: true }).focus();
    await expect(page.getByRole("button", { name: "Lista", exact: true })).toBeFocused();
    await assertRuntimeClean();
  });

  test("opens a generated report in a modal and follows browser history", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/reports");
    await expect(page.getByRole("heading", { name: "Laudos" })).toBeVisible();
    await expect(page.getByRole("searchbox", { name: "Buscar laudo" })).toBeVisible();
    await expect(page.locator(".report-table, .report-list-state").first()).toBeVisible();

    const openButton = page.getByRole("button", { name: /Abrir detalhes do laudo/ }).first();
    if (!(await openButton.isVisible().catch(() => false))) {
      await expect(page.getByRole("status")).toContainText(/Nenhum laudo/);
      await assertRuntimeClean();
      return;
    }

    await openButton.click();
    const dialog = page.getByRole("dialog", { name: "Laudo de vistoria" });
    await expect(dialog).toBeVisible();
    await expect(page).toHaveURL(/inspectionId=/);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(page).not.toHaveURL(/inspectionId=/);
    await page.goForward();
    await expect(dialog).toBeVisible();
    await page.reload();
    await expect(page).toHaveURL(/inspectionId=/);
    await expect(page.getByRole("button", { name: "Entrar no Painel" })).toBeVisible();
    await assertRuntimeClean();
  });
});
