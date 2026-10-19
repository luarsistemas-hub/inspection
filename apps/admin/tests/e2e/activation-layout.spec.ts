import { expect, test } from "@playwright/test";

for (const route of ["/activate", "/activate/invitation"]) {
  test(`${route} keeps activation content together`, async ({ page }) => {
    await page.goto(`${route}?token=layout-fixture`);

    const heading = page.getByRole("heading", { level: 1 });
    const form = page.locator(".admin-activation form");
    const button = form.getByRole("button", { name: /Enviar código/ });

    await expect(heading).toBeVisible();
    await expect(form).toBeVisible();
    await expect(page.locator(".admin-gate-story-brand")).toBeVisible();

    const headingBox = await heading.boundingBox();
    const formBox = await form.boundingBox();
    const buttonBox = await button.boundingBox();
    const stepBox = await form.locator(".inspection-steps__item").first().boundingBox();
    const viewportWidth = page.viewportSize()?.width ?? 0;

    expect(headingBox).not.toBeNull();
    expect(formBox).not.toBeNull();
    expect(buttonBox).not.toBeNull();
    expect(stepBox).not.toBeNull();
    expect(formBox!.y - headingBox!.y).toBeLessThan(240);
    expect(buttonBox!.height).toBeLessThan(70);
    expect(stepBox!.height).toBeLessThan(65);
    expect(formBox!.x + formBox!.width).toBeLessThanOrEqual(viewportWidth + 1);
  });
}
