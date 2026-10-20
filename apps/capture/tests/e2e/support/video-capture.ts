import { expect, type Page } from "@playwright/test";

export async function startVideoRecording(page: Page): Promise<void> {
  const openCamera = page.getByRole("button", { name: "Abrir câmera" });
  if (await openCamera.count()) {
    await openCamera.click();
    const start = page.getByRole("button", { name: "Iniciar gravação" });
    await expect(start).toBeEnabled();
    await start.click();
  } else {
    await page.getByRole("button", { name: "Iniciar gravação" }).click();
  }
}
