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
  await expect(page.getByRole("heading", { name: /Vistorias|Projetos|Triagem|Portfólio|Relatórios|Laudos|Notificações|Agenda de vistorias|Inventários|Revisar inventário|Inventário pronto|Preparando inventário|Não foi possível preparar o inventário/ }).first()).toBeVisible({ timeout: 15_000 });
}

/** Authenticates deterministic GraphQL tests without depending on Keycloak. */
export async function loginAsMockAdmin(page: Page, returnTo: string): Promise<void> {
  const state = "dashboard-e2e-state";
  await page.route(/\/protocol\/openid-connect\/token(?:\?|$)/, (route) => route.fulfill({ status: 200, contentType: "application/json", headers: { "access-control-allow-origin": "*" }, body: JSON.stringify({ access_token: "dashboard-e2e-access-token" }) }));
  await page.addInitScript((options) => sessionStorage.setItem("inspection.dashboard.pkce", JSON.stringify({ verifier: "dashboard-e2e-verifier", state: options.state, returnTo: options.path })), { state, path: returnTo });
  await page.goto(`/auth/callback?code=dashboard-e2e-code&state=${state}`);
  await expect(page.getByRole("navigation", { name: "Painel" })).toBeVisible();
  await expect(page.getByRole("heading", { name: /Vistorias|Projetos|Triagem|Portfólio|Relatórios|Laudos|Notificações|Agenda de vistorias|Inventários/ })).toBeVisible();
}

export async function openDashboardRoute(page: Page, label: string): Promise<void> {
  const navigation = page.getByRole("navigation", { name: "Painel" });
  const links = navigation.getByRole("link", { name: label, exact: true }).filter({ visible: true });
  if (await links.count() > 0) {
    await links.first().click();
    return;
  }
  await navigation.getByRole("button", { name: /^Mais destinos/ }).click();
  await page.getByRole("navigation", { name: "Painel — outros destinos" }).getByRole("link", { name: label, exact: true }).click();
}


/** Authenticates the local Admin in cross-app end-to-end journeys. */
export async function loginAsLocalAdminPortal(page: Page, returnTo: string): Promise<void> {
  await page.goto(returnTo);
  await page.getByRole("button", { name: "Entrar com conta administrativa" }).click();
  await page.waitForURL(/localhost:8081\/realms\/inspection\/protocol\/openid-connect\/auth/);
  await page.locator("#username").fill(process.env.INSPECTION_E2E_USERNAME ?? "admin");
  await page.locator("#password").fill(process.env.INSPECTION_E2E_PASSWORD ?? "admin");
  await page.locator("button[type=submit]").click();
  await page.waitForURL(`**${returnTo}`);
  await expect(page.getByRole("heading", { level: 1, name: /Organização|Identidade e acesso|Acessos|Responsáveis pela vistoria|Configuração de vistorias|Governança|Auditoria|Prompts de análise|Visão administrativa|Consumo de LLM/ })).toBeVisible();
}
