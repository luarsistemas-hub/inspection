import { expect, type Page } from "@playwright/test";

// Copies of the Admin and Dashboard login helpers: each app builds its tests
// in its own Docker context, so specs cannot import across apps.

export async function loginAsLocalAdminConsole(page: Page, returnTo: string): Promise<void> {
  const adminOrigin = process.env.PLAYWRIGHT_ADMIN_URL ?? "http://localhost:3000";
  const destination = new URL(returnTo, adminOrigin).toString();
  await page.goto(destination);
  await page.getByRole("button", { name: "Entrar com conta administrativa" }).click();
  await page.waitForURL(/localhost:8081\/realms\/inspection\/protocol\/openid-connect\/auth/);
  await page.locator("#username").fill(process.env.INSPECTION_E2E_USERNAME ?? "admin");
  await page.locator("#password").fill(process.env.INSPECTION_E2E_PASSWORD ?? "admin");
  await page.locator("button[type=submit]").click();
  await page.waitForURL(destination);
  await expect(page.getByRole("heading", { level: 1, name: /Organização|Identidade e acesso|Acessos|Responsáveis pela vistoria|Configuração de vistorias|Governança|Auditoria|Prompts de análise|Visão administrativa|Consumo de LLM|Anotações por voz/ })).toBeVisible();
}

export async function loginAsLocalDashboard(page: Page, returnTo: string): Promise<void> {
  await page.goto(returnTo);
  const signIn = page.getByRole("button", { name: "Entrar no Painel" });
  const navigation = page.getByRole("navigation", { name: "Painel" });
  await expect.poll(async () => await signIn.isVisible() || await navigation.isVisible(), { timeout: 15_000 }).toBe(true);
  if (await signIn.isVisible()) await signIn.click();
  const username = page.locator("#username");
  await expect.poll(async () => await username.isVisible() || await navigation.isVisible(), { timeout: 15_000 }).toBe(true);
  if (await username.isVisible()) {
    await username.fill(process.env.INSPECTION_E2E_USERNAME ?? "admin");
    await page.locator("#password").fill(process.env.INSPECTION_E2E_PASSWORD ?? "admin");
    await page.locator("button[type=submit]").click();
  }
  await expect(navigation).toBeVisible({ timeout: 15_000 });
  if (!page.url().endsWith(returnTo)) await page.goto(returnTo);
  await expect(page.getByRole("heading", { name: /Vistorias|Projetos|Triagem|Portfólio|Relatórios|Laudos|Notificações|Agenda de vistorias/ })).toBeVisible({ timeout: 15_000 });
}
