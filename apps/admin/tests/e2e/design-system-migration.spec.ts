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
    const info = page.getByRole("button", { name: "Informações sobre Organização" });
    await expect(info).toBeVisible();
    await expect(info).toHaveAttribute("aria-expanded", "false");
    await expect(page.getByRole("button", { name: "Criar unidade" })).toBeVisible();
    if (width === 768) await expect(page.locator("nav.inspection-adaptive-navigation__sidebar")).toBeVisible();
    const horizontalOverflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    expect(horizontalOverflow, `unexpected page overflow at ${width}px`).toBe(false);

    const axePage = page as unknown as ConstructorParameters<typeof AxeBuilder>[0]["page"];
    const pageScan = await new AxeBuilder({ page: axePage }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22a", "wcag22aa"]).analyze();
    expect(pageScan.violations, `axe violations at ${width}px`).toEqual([]);
  }

  const pageInfo = page.getByRole("button", { name: "Informações sobre Organização" });
  await pageInfo.focus();
  await pageInfo.press("Enter");
  await expect(pageInfo).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByText("Unidades e estrutura da sua operação.")).toBeVisible();
  await pageInfo.press("Enter");
  await expect(page.getByText("Unidades e estrutura da sua operação.")).toBeHidden();

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

test("E2E-065 Admin secondary navigation and prompt metadata fit a 320px viewport in both themes", async ({ page }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");
  await loginAsLocalAdmin(page, "/prompts");
  await page.setViewportSize({ width: 320, height: 568 });

  const bottomBar = page.locator("nav.inspection-adaptive-navigation__compact");
  await expect(bottomBar).toBeVisible();
  await bottomBar.getByRole("button", { name: /Mais destinos/ }).click();
  const destinations = page.getByRole("navigation", { name: "Navegação administrativa — outros destinos" });
  await expect(destinations.getByRole("link", { name: "Prompts de análise" })).toBeVisible();
  await page.getByRole("button", { name: "Fechar" }).click();

  for (const theme of ["light", "dark"]) {
    await page.getByLabel("Aparência").selectOption(theme);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
    const overflowingMetadata = await page.locator(".prompt-editor-meta > span").evaluateAll((items) => items.filter((item) => item.scrollWidth > item.clientWidth).map((item) => item.textContent));
    expect(overflowingMetadata).toEqual([]);
  }

  await bottomBar.getByRole("link", { name: "Organização" }).click();
  const mobileCollection = page.locator(".admin-mobile-collection");
  await expect(mobileCollection).toBeVisible();
  await expect(page.locator(".admin-content > .inspection-data-table")).toBeHidden();
  await mobileCollection.getByRole("button", { name: "Abrir detalhes" }).first().click();
  await expect(page.getByRole("dialog")).toBeVisible();
});
