import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { loginAsLocalAdmin } from "./support/auth";

const viewportWidths = [320, 360, 768, 1440];

test("E2E-039 Admin organization stays operable and accessible across supported desktop browsers and widths", async ({ page }, testInfo) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");
  test.skip(!["chromium", "webkit"].includes(testInfo.project.name), "E2E-039 owns desktop Chromium and WebKit runs");

  await loginAsLocalAdmin(page, "/organization");

  for (const width of viewportWidths) {
    await page.setViewportSize({ width, height: 900 });
    await expect(page.getByRole("heading", { level: 1, name: "Organização" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Criar unidade" })).toBeVisible();
    const horizontalOverflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    expect(horizontalOverflow, `unexpected page overflow at ${width}px`).toBe(false);

    const axePage = page as unknown as ConstructorParameters<typeof AxeBuilder>[0]["page"];
    const pageScan = await new AxeBuilder({ page: axePage }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22a", "wcag22aa"]).analyze();
    expect(pageScan.violations, `axe violations at ${width}px`).toEqual([]);
  }

  await page.getByRole("button", { name: "Criar unidade" }).click();
  const dialog = page.locator(".inspection-dialog");
  const form = page.getByRole("form", { name: "Operação administrativa" });
  await expect(dialog).toBeVisible();
  await expect(page.getByRole("dialog", { name: "Criar unidade" })).toBeVisible();
  await expect(form).toBeVisible();
  await page.getByLabel("Código da unidade").fill("UN01");
  await page.getByRole("button", { name: "Salvar operação" }).click();
  await expect.poll(() => page.getByLabel("Nome da unidade").evaluate((element: HTMLInputElement) => element.validationMessage)).not.toBe("");
  const axePage = page as unknown as ConstructorParameters<typeof AxeBuilder>[0]["page"];
  const errorScan = await new AxeBuilder({ page: axePage }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22a", "wcag22aa"]).include(".inspection-dialog").analyze();
  expect(errorScan.violations).toEqual([]);
  await expect(form).toBeVisible();
});
