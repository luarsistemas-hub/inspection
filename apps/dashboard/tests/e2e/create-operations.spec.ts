import { expect, test, type Locator, type Page } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin } from "./support/auth";

test.describe("Dashboard creation forms against the local stack", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");

  async function chooseFirst(form: Locator, label: string) {
    await form.getByRole("combobox", { name: label }).click();
    await form.getByRole("listbox", { name: label }).getByRole("option").first().click();
  }

  async function chooseEligibleDependencies(form: Locator) {
    await chooseFirst(form, "Imóvel");
    await chooseFirst(form, "Responsável pela vistoria");
    await chooseFirst(form, "Modelo de vistoria");
  }

  async function mutationResponse(page: Page, operation: string, button: Locator) {
    const responsePromise = page.waitForResponse((response) => response.url().endsWith("/graphql") && response.request().postData()?.includes(`mutation ${operation}`) === true);
    await button.click();
    const response = await responsePromise;
    expect(response.ok()).toBe(true);
    return { request: response.request().postDataJSON() as { variables: { input: Record<string, unknown> } }, body: await response.json() as { errors?: unknown[]; data?: Record<string, { userErrors?: unknown[]; [key: string]: unknown }> } };
  }

  test("Agenda accepts a local start time in the selected IANA timezone", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/schedules");
    const form = page.locator("form").filter({ has: page.getByRole("heading", { name: "Nova agenda" }) });
    await expect(form).toBeVisible();
    await chooseEligibleDependencies(form);
    const tomorrow = new Date(Date.now() + 86_400_000).toISOString().slice(0, 10);
    await form.getByLabel("Início").fill(`${tomorrow}T23:32`);
    await form.getByRole("combobox", { name: "Fuso horário" }).fill("Africa/Accra");
    await form.getByRole("listbox", { name: "Fuso horário" }).getByRole("option", { name: "Africa/Accra" }).click();
    const { request, body } = await mutationResponse(page, "CreateSchedule", form.getByRole("button", { name: "Criar agenda" }));
    expect(request.variables.input).toMatchObject({ startsAt: `${tomorrow}T23:32`, timezone: "Africa/Accra" });
    expect(body.errors).toBeUndefined();
    expect(body.data?.createSchedule?.userErrors).toEqual([]);
    await assertRuntimeClean();
  });

  test("Vistorias sends explicit instants for both dates", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/inspections");
    await page.locator(".inspection-create-panel summary").click();
    const form = page.locator("form").filter({ has: page.getByRole("heading", { name: "Nova vistoria" }) });
    await expect(form).toBeVisible();
    await chooseFirst(form, "Imóvel");
    await chooseFirst(form, "Responsável pela vistoria");
    const tomorrow = new Date(Date.now() + 86_400_000).toISOString().slice(0, 10);
    const nextDay = new Date(Date.now() + 2 * 86_400_000).toISOString().slice(0, 10);
    await form.getByLabel("Vencimento").fill(`${tomorrow}T23:34`);
    await form.getByLabel("Prazo final").fill(`${nextDay}T23:34`);
    const { request, body } = await mutationResponse(page, "CreateInspection", form.getByRole("button", { name: "Criar vistoria" }));
    expect(request.variables.input.dueAt).toMatch(/Z$/);
    expect(request.variables.input.deadlineAt).toMatch(/Z$/);
    expect(body.errors).toBeUndefined();
    expect(body.data?.createInspection?.userErrors).toEqual([]);
    await assertRuntimeClean();
  });

  test("Projetos has a working mediator in the GraphQL resolver", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/projects");
    const form = page.locator("form").filter({ has: page.getByRole("heading", { name: "Novo projeto" }) });
    await expect(form).toBeVisible();
    await chooseEligibleDependencies(form);
    const { body } = await mutationResponse(page, "CreateProject", form.getByRole("button", { name: "Criar projeto" }));
    expect(body.errors).toBeUndefined();
    expect(body.data?.createProject?.userErrors).toEqual([]);
    await assertRuntimeClean();
  });
});
