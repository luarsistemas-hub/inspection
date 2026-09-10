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
  await page.getByRole("button", { name: "Entrar com conta administrativa" }).click();
  await page.waitForURL(/localhost:8081\/realms\/inspection\/protocol\/openid-connect\/auth/);
  await page.locator("#username").fill(process.env.INSPECTION_E2E_USERNAME ?? "admin");
  await page.locator("#password").fill(process.env.INSPECTION_E2E_PASSWORD ?? "admin");
  await page.locator("button[type=submit]").click();
  await page.waitForURL(`**${returnTo}`);
  await expect(page.getByRole("heading", { level: 1, name: /Organização|Acessos|Catálogos|Ativos|Governança|Auditoria/ })).toBeVisible();
}
