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

  test.describe("Agendas in the Brazil timezone", () => {
    test.use({ timezoneId: "Pacific/Honolulu" });

    test("sends the local start time in São Paulo for every available frequency", async ({ page }) => {
      const assertRuntimeClean = installRuntimeGuards(page);
      await loginAsLocalAdmin(page, "/schedules");
      const start = `${new Date(Date.now() + 2 * 86_400_000).toISOString().slice(0, 10)}T23:32`;
      for (const [frequency, label] of [["DAILY", "Diariamente"], ["WEEKLY", "Semanalmente"], ["MONTHLY", "Mensalmente"], ["YEARLY", "Anualmente"]]) {
        await page.getByRole("button", { name: "Nova agenda" }).click();
        const dialog = page.getByRole("dialog", { name: "Nova agenda" });
        const form = dialog.locator("form");
        await expect(form).toBeVisible();
        await chooseEligibleDependencies(form);
        await expect(form.getByLabel("Fuso horário")).toHaveCount(0);
        await form.getByLabel("Primeira vistoria").fill(start);
        const repeat = form.getByLabel("Repetir");
        await expect(repeat.locator("option")).toHaveText(["Diariamente", "Semanalmente", "Mensalmente", "Anualmente"]);
        await repeat.selectOption(frequency);
        const { request, body } = await mutationResponse(page, "CreateSchedule", form.getByRole("button", { name: "Criar agenda" }));
        expect(request.variables.input).toMatchObject({ startsAt: start, timezone: "America/Sao_Paulo", rrule: `FREQ=${frequency}` });
        expect(await repeat.locator("option:checked").textContent()).toBe(label);
        expect(body.errors).toBeUndefined();
        expect(body.data?.createSchedule?.userErrors).toEqual([]);
        await expect(dialog).toBeHidden();
      }

      const localStart = new Date(`${start}:00-03:00`);
      const startLabel = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short", timeZone: "America/Sao_Paulo" }).format(localStart);
      const firstSchedule = page.locator(".schedules-collection > li").filter({ hasText: `Próxima vistoria: ${startLabel}` }).first();
      await expect(firstSchedule).toBeVisible();
      await firstSchedule.getByRole("button", { name: "Editar agenda" }).click();
      const editDialog = page.getByRole("dialog", { name: "Editar agenda" });
      await expect(editDialog).toBeVisible();
      await editDialog.getByLabel("Repetir").selectOption("WEEKLY");
      const { request: updateRequest, body: updateBody } = await mutationResponse(page, "UpdateSchedule", editDialog.getByRole("button", { name: "Salvar alterações" }));
      expect(updateRequest.variables.input).toMatchObject({ rrule: "FREQ=WEEKLY", timezone: "America/Sao_Paulo" });
      expect(updateBody.errors).toBeUndefined();
      expect(updateBody.data?.updateSchedule?.userErrors).toEqual([]);
      await assertRuntimeClean();
    });
  });

  test("Vistorias sends explicit instants for both dates", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/inspections");
    await page.getByRole("button", { name: "Nova vistoria" }).click();
    const dialog = page.getByRole("dialog", { name: "Nova vistoria" });
    const form = dialog.locator("form");
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
    await expect(dialog).toBeHidden();
    await assertRuntimeClean();
  });

  test("Projetos has a working mediator in the GraphQL resolver", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/projects");
    await page.getByRole("button", { name: "Novo projeto" }).click();
    const dialog = page.getByRole("dialog", { name: "Novo projeto" });
    const form = dialog.locator("form");
    await expect(form).toBeVisible();
    await chooseEligibleDependencies(form);
    const { body } = await mutationResponse(page, "CreateProject", form.getByRole("button", { name: "Criar projeto" }));
    expect(body.errors).toBeUndefined();
    expect(body.data?.createProject?.userErrors).toEqual([]);
    await expect(dialog).toBeHidden();
    await assertRuntimeClean();
  });

  test("inspection draft can be kept or discarded without moving the board", async ({ page }) => {
    await loginAsLocalAdmin(page, "/inspections");
    const opener = page.getByRole("button", { name: "Nova vistoria" });
    const board = page.locator("#inspection-collection");
    const before = await board.boundingBox();
    await opener.click();
    const dialog = page.getByRole("dialog", { name: "Nova vistoria" });
    await dialog.getByLabel("Vencimento").fill("2030-05-10T12:00");
    page.once("dialog", (confirm) => confirm.dismiss());
    await dialog.getByRole("button", { name: "Cancelar", exact: true }).click();
    await expect(dialog).toBeVisible();
    await expect(dialog.getByLabel("Vencimento")).toHaveValue("2030-05-10T12:00");
    page.once("dialog", (confirm) => confirm.accept());
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(opener).toBeFocused();
    expect(await board.boundingBox()).toEqual(before);
  });

  test("Escape closes an open selector before it closes the inspection dialog", async ({ page }) => {
    await loginAsLocalAdmin(page, "/inspections");
    await page.getByRole("button", { name: "Nova vistoria" }).click();
    const dialog = page.getByRole("dialog", { name: "Nova vistoria" });
    const asset = dialog.getByRole("combobox", { name: "Imóvel" });
    await asset.focus();
    await expect(dialog.getByRole("listbox", { name: "Imóvel" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("listbox", { name: "Imóvel" })).toBeHidden();
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
  });
});
