import { expect, test } from "@playwright/test";

test("E2E-038 / IT-190: built dialog and combobox preserve keyboard close order and trigger focus", async ({ page }) => {
  await page.goto("/");
  const info = page.getByRole("button", { name: "Informações sobre vistoria" });
  await expect(info).toHaveAttribute("aria-expanded", "false");
  await info.focus();
  await info.press("Enter");
  await expect(info).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByText("A alteração será confirmada pelo servidor.")).toBeVisible();
  await info.press("Space");
  await expect(info).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByText("A alteração será confirmada pelo servidor.")).toBeHidden();

  const trigger = page.getByRole("button", { name: "Nova vistoria" });
  await trigger.focus();
  await trigger.press("Enter");
  await expect(page.getByRole("dialog", { name: "Nova vistoria" })).toBeVisible();
  const input = page.getByRole("combobox", { name: "Imóvel" });
  await input.focus();
  await input.press("ArrowDown");
  await input.press("Enter");
  await expect(page.getByTestId("selection")).toHaveText("assetA");
  await input.focus();
  await input.press("ArrowDown");
  await input.press("Escape");
  await expect(page.getByRole("dialog", { name: "Nova vistoria" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog", { name: "Nova vistoria" })).toBeHidden();
  await expect(trigger).toBeFocused();
});
