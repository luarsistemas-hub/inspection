import { expect, type Page } from "@playwright/test";

export async function startVideoRecording(page: Page): Promise<void> {
  const openCamera = page.getByRole("button", { name: "Abrir câmera" });
  const start = page.getByRole("button", { name: "Iniciar gravação" });
  await expect(openCamera.or(start)).toBeVisible();
  if (await openCamera.isVisible()) await openCamera.click();
  await expect(start).toBeEnabled();
  await start.click();
}
