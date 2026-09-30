import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

test("Cobalto themes stay readable and within the viewport on the dashboard entry", async ({ page }, testInfo) => {
  test.skip(!["chromium", "webkit"].includes(testInfo.project.name), "The viewport and screenshot check runs in Chromium and WebKit.");
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  await page.goto("/inspections");

  const appearance = page.getByRole("combobox", { name: "Aparência" });
  await expect(appearance).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  const axe = await new AxeBuilder({ page: page as unknown as ConstructorParameters<typeof AxeBuilder>[0]["page"] }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(axe.violations).toEqual([]);

  for (const width of [320, 360, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `horizontal overflow at ${width}px`).toBe(true);
    if (width === 360 || width === 1440) await page.screenshot({ path: testInfo.outputPath(`dashboard-light-${width}.png`), fullPage: true });
  }

  await appearance.selectOption("dark");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe("dark");
  await expect.poll(() => page.evaluate(() => localStorage.getItem("inspection.theme"))).toBe("dark");
  await page.reload();
  await expect(page.getByRole("combobox", { name: "Aparência" })).toHaveValue("dark");
  await page.setViewportSize({ width: 360, height: 800 });
  await page.screenshot({ path: testInfo.outputPath("dashboard-dark-360.png"), fullPage: true });
});
