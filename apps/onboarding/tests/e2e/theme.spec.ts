import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

test("Cobalto themes preserve the onboarding entry at supported viewport widths", async ({ page }, testInfo) => {
  test.skip(!["viewport-320", "desktop-1440"].includes(testInfo.project.name), "The viewport and screenshot check runs once in WebKit and Chromium.");
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  await page.goto("/");

  const appearance = page.getByRole("button", { name: "Ativar tema escuro" });
  await expect(appearance).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  const info = page.getByRole("button", { name: "Informações sobre cadastro e salvamento" });
  await expect(info).toHaveAttribute("aria-expanded", "false");
  await info.click();
  await expect(page.getByText(/Os campos ficam neste navegador/)).toBeVisible();
  const reducedMotionDuration = await page.locator(".inspection-info-disclosure__panel").evaluate((element) => getComputedStyle(element).transitionDuration);
  expect(Number.parseFloat(reducedMotionDuration)).toBeLessThan(0.001);
  const axe = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(axe.violations).toEqual([]);

  for (const width of [320, 360, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `horizontal overflow at ${width}px`).toBe(true);
    if (width === 360 || width === 1440) await page.screenshot({ path: testInfo.outputPath(`onboarding-light-${width}.png`), fullPage: true });
  }

  await appearance.click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe("dark");
  await expect.poll(() => page.evaluate(() => localStorage.getItem("inspection.theme"))).toBe("dark");
  await page.reload();
  await expect(page.getByRole("button", { name: "Ativar tema claro" })).toBeVisible();
  await page.getByRole("button", { name: "Informações sobre cadastro e salvamento" }).click();
  await expect(page.getByText(/Os campos ficam neste navegador/)).toBeVisible();
  await page.setViewportSize({ width: 360, height: 800 });
  await page.screenshot({ path: testInfo.outputPath("onboarding-dark-360.png"), fullPage: true });
});
