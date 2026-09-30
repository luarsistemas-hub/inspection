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

export async function navigateAdmin(page: Page, label: string): Promise<void> {
  const navigation = page.getByRole("navigation", { name: "Navegação administrativa", exact: true });
  const primaryDestination = ["Visão geral", "Organização", "Usuários e acessos"].includes(label);
  if (primaryDestination || (page.viewportSize()?.width ?? 1200) >= 1200) {
    await navigation.getByRole("link", { name: label, exact: true }).click();
    return;
  }
  await navigation.getByRole("button", { name: /Mais destinos/ }).click();
  await page.getByRole("navigation", { name: "Navegação administrativa — outros destinos" }).getByRole("link", { name: label, exact: true }).click();
}

export const collectionRows = (page: Page) => page.locator(".inspection-data-table tbody tr:visible, .admin-mobile-collection > li:visible");

/** Supplies a deterministic OIDC redirect and token for GraphQL-mocked Admin tests. */
export async function mockAdminOidc(page: Page): Promise<void> {
  await page.route(/\/protocol\/openid-connect\/auth(?:\?|$)/, async (route) => {
    const request = new URL(route.request().url());
    const callback = request.searchParams.get("redirect_uri");
    const state = request.searchParams.get("state");
    if (!callback || !state) throw new Error("OIDC mock needs a callback and state");
    const destination = new URL(callback);
    destination.searchParams.set("code", "admin-e2e-code");
    destination.searchParams.set("state", state);
    await route.fulfill({ status: 302, headers: { location: destination.toString() }, body: "" });
  });
  await page.route(/\/protocol\/openid-connect\/token(?:\?|$)/, (route) =>
    route.fulfill({ json: { access_token: "admin-e2e-access-token", token_type: "Bearer" } }));
}

export async function loginAsLocalAdmin(page: Page, returnTo: string): Promise<void> {
  if (process.env.INSPECTION_E2E_MOCKS === "true") {
    await mockAdminOidc(page);
    await page.goto(returnTo);
    await page.getByRole("button", { name: "Entrar com conta administrativa" }).click();
    await page.waitForURL(`**${returnTo}`);
    return;
  }
  await page.goto(returnTo);
  await page.getByRole("button", { name: "Entrar com conta administrativa" }).click();
  await page.waitForURL(/localhost:8081\/realms\/inspection\/protocol\/openid-connect\/auth/);
  await page.locator("#username").fill(process.env.INSPECTION_E2E_USERNAME ?? "admin");
  await page.locator("#password").fill(process.env.INSPECTION_E2E_PASSWORD ?? "admin");
  await page.locator("button[type=submit]").click();
  await page.waitForURL(`**${returnTo}`);
  await expect(page.getByRole("heading", { level: 1, name: /Organização|Identidade e acesso|Acessos|Responsáveis pela vistoria|Configuração de vistorias|Governança|Auditoria|Prompts de análise|Visão administrativa|Consumo de LLM/ })).toBeVisible();
}
