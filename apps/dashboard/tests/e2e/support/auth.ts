import { expect, type Page } from "@playwright/test";

export function installRuntimeGuards(page: Page) {
  const failures: string[] = [];
  page.on("console", (message) => { if (message.type() === "error") failures.push(`console: ${message.text()}`); });
  page.on("pageerror", (error) => failures.push(`page: ${error.message}`));
  page.on("response", async (response) => {
    if (!response.url().endsWith("/graphql")) return;
    try {
      const body = await response.json() as { errors?: Array<{ message?: string }> };
      for (const error of body.errors ?? []) failures.push(`graphql: ${error.message ?? "unknown error"}`);
    } catch {
      // Non-JSON responses are covered by the transport unit tests.
    }
  });
  return async () => expect(failures, failures.join("\n")).toEqual([]);
}

export async function loginAsLocalAdmin(page: Page, returnTo: string): Promise<void> {
  await page.goto(returnTo);
  const signIn = page.getByRole("button", { name: "Entrar no Painel" });
  const navigation = page.getByRole("navigation", { name: "Painel" });
  await Promise.race([signIn.waitFor({ state: "visible" }), navigation.waitFor({ state: "visible" })]);
  if (await signIn.isVisible()) await signIn.click();
  const username = page.locator("#username");
  await Promise.race([username.waitFor({ state: "visible" }), navigation.waitFor({ state: "visible" })]);
  if (await username.isVisible()) {
    await username.fill(process.env.INSPECTION_E2E_USERNAME ?? "admin");
    await page.locator("#password").fill(process.env.INSPECTION_E2E_PASSWORD ?? "admin");
    await page.locator("button[type=submit]").click();
  }
  await expect(navigation).toBeVisible({ timeout: 15_000 });
  if (!page.url().endsWith(returnTo)) await page.goto(returnTo);
  await expect(page.getByRole("heading", { name: /Vistorias|Projetos|Triagem|Portfólio|Relatórios|Laudos|Notificações|Agenda de vistorias/ })).toBeVisible({ timeout: 15_000 });
}
