import { expect, test } from "@playwright/test";
import { collectionRows, installRuntimeGuards, loginAsLocalAdmin, navigateAdmin } from "./support/auth";

test.describe("authenticated Admin against the local stack", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");

  test("E2E-008 creates and reloads a unit in the active tenant", async ({ page }) => {
    await loginAsLocalAdmin(page, "/organization");
    await expect(page.getByRole("status")).toContainText("Organização atualizado");

    const existingUnit = collectionRows(page).filter({ hasText: "UN01" });
    if (await existingUnit.count() === 0) {
      await page.getByRole("button", { name: "Criar unidade" }).click();
      await page.getByLabel("Código da unidade").fill("UN01");
      await page.getByLabel("Nome da unidade").fill("Unidade Norte");
      await page.getByRole("button", { name: "Salvar operação" }).click();
    }

    await expect(collectionRows(page).filter({ hasText: "UN01" })).toContainText("Unidade Norte");
    await navigateAdmin(page, "Visão geral");
    await navigateAdmin(page, "Organização");
    await expect(collectionRows(page).filter({ hasText: "UN01" })).toContainText("Unidade Norte");
  });

  test("E2E-009 invites an internal employee and shows the pending membership", async ({ page }) => {
    await loginAsLocalAdmin(page, "/access");
    await expect(page.locator(".status-line")).toContainText("atualizado");
    await page.getByRole("button", { name: "Convidar usuário" }).click();
    await page.getByLabel("Nome do usuário").fill("Operador QA");
    const inviteEmail = process.env.INSPECTION_E2E_INVITE_EMAIL ?? "qa-inspection-employee@example.test";
    await page.getByLabel("E-mail", { exact: true }).fill(inviteEmail);
    await page.getByLabel("Confirme o e-mail").fill(inviteEmail);
    await page.getByLabel("Perfil de acesso").selectOption("EMPLOYEE");
    await page.getByRole("button", { name: "Salvar operação" }).click();
    const employee = collectionRows(page).filter({ hasText: "Operador" });
    await expect(employee).toContainText("Imobiliária");
    await expect(employee).toContainText("Pendente");
  });

  test("E2E-010 creates a participant under an active unit", async ({ page }) => {
    await loginAsLocalAdmin(page, "/catalogs");
    await expect(page.locator(".status-line")).toContainText("atualizado");
    if (await collectionRows(page).filter({ hasText: "Ana QA" }).count() === 0) {
      await page.getByRole("button", { name: "Criar responsável" }).click();
      const unit = page.getByLabel("Unidade");
      await expect(unit.locator("option").nth(1)).toBeAttached();
      await unit.selectOption({ index: 1 });
      await page.getByLabel("Nome do responsável").fill("Ana QA");
      await page.getByLabel("E-mail", { exact: true }).fill("ana-qa@example.test");
      await page.getByLabel("Confirme o e-mail").fill("ana-qa@example.test");
      await page.getByLabel("Função no segmento").fill("OWNER");
      await page.getByRole("button", { name: "Salvar operação" }).click();
    }
    await expect(collectionRows(page).filter({ hasText: "Ana QA" })).toBeVisible();
  });

  test("E2E-011 registers an asset with its compatible seeded configuration", async ({ page }) => {
    await loginAsLocalAdmin(page, "/assets");
    await expect(page.locator(".status-line")).toContainText("atualizado");
    if (await collectionRows(page).filter({ hasText: "APT101" }).count() === 0) {
      await page.getByRole("button", { name: "Registrar imóvel" }).click();
      const unit = page.getByLabel("Unidade");
      await expect(unit.locator("option").nth(1)).toBeAttached();
      await unit.selectOption({ index: 1 });
      const segment = page.getByLabel("Versão do segmento");
      await expect(segment.locator("option").nth(1)).toBeAttached();
      await segment.selectOption({ index: 1 });
      const template = page.getByLabel("Modelo de vistoria");
      await expect(template.locator("option").nth(1)).toBeAttached();
      await template.selectOption({ index: 1 });
      await page.getByLabel("Tipo do imóvel").fill("APARTMENT");
      await page.getByLabel("Nome do imóvel").fill("Apartamento 101");
      await page.getByLabel("Código do imóvel").fill("APT101");
      await page.getByRole("textbox", { name: /^CEP/ }).fill("01001-000");
      const lookupResponse = page.waitForResponse((response) => response.url().endsWith("/graphql") && response.request().postData()?.includes("AdminLookupPostalCode") === true);
      await page.getByRole("button", { name: "Buscar CEP" }).click();
      await lookupResponse;
      await page.getByRole("textbox", { name: "Número *" }).fill("101");
      await page.getByRole("button", { name: "Salvar operação" }).click();
    }
    await expect(collectionRows(page).filter({ hasText: "APT101" })).toBeVisible();
  });

  test("E2E-012 updates the publication policy with its loaded version", async ({ page }) => {
    await loginAsLocalAdmin(page, "/governance");
    await expect(page.locator(".status-line")).toContainText("atualizado");
    const policy = collectionRows(page).filter({ hasText: "Política de publicação" });
    const currentPolicy = await policy.innerText();
    const targetMode = currentPolicy.includes("Manual") ? "AUTOMATIC" : "MANUAL";
    await page.getByRole("button", { name: "Configurar política" }).click();
    await page.getByLabel("Modo de publicação").selectOption(targetMode);
    await page.getByRole("button", { name: "Salvar operação" }).click();
    await expect(collectionRows(page).filter({ hasText: "Política de publicação" })).toContainText(targetMode === "MANUAL" ? "Manual" : "Automática");
  });

  test("E2E-013 opens and closes a seeded resource history", async ({ page }) => {
    await loginAsLocalAdmin(page, "/audit");
    await expect(page.locator(".status-line")).toContainText("atualizado");
    const firstRow = collectionRows(page).first();
    const details = firstRow.getByRole("button", { name: "Abrir detalhes" });
    await details.click();
    await expect(page.getByRole("dialog")).toBeVisible();
    await page.getByRole("button", { name: "Fechar detalhe" }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByRole("table", { name: "Auditoria: coleção administrativa" }).or(page.getByRole("list", { name: "Auditoria: registros" }))).toBeVisible();
  });

  test("E2E-014 saves and reloads the seeded analysis prompt", async ({ page }) => {
    await loginAsLocalAdmin(page, "/prompts");
    const prompt = page.getByLabel("Instrução do sistema");
    await expect(prompt).toBeVisible();
    const original = await prompt.inputValue();
    const updated = `${original}\n\nContexto QA administrativo.`;
    await page.getByLabel("Instrução do sistema").fill(updated);
    await page.getByLabel("Confirmo a alteração do prompt global.").check();
    await page.getByRole("button", { name: "Salvar nova revisão" }).click();
    await expect(page.getByLabel("Instrução do sistema")).toHaveValue(/Contexto QA administrativo\./);
    await navigateAdmin(page, "Visão geral");
    await navigateAdmin(page, "Prompts de análise");
    await expect(page.getByLabel("Instrução do sistema")).toHaveValue(/Contexto QA administrativo\./);
  });

  test("E2E-015 filters and inspects a seeded live LLM call", async ({ page }) => {
    await loginAsLocalAdmin(page, "/llm-usage");
    await page.locator(".llm-usage-advanced-filters > summary").click();
    const start = page.getByLabel("Início");
    const end = page.getByLabel("Fim");
    const to = new Date();
    const from = new Date(to.getTime() - 24 * 60 * 60 * 1000);
    const localDateTime = (date: Date) => new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
    await start.fill(localDateTime(from));
    await end.fill(localDateTime(to));
    await page.getByLabel("Tenant").fill("Minha operação");
    const tenantChoice = page.getByRole("button", { name: /Minha operação ·/ });
    await expect(tenantChoice).toBeVisible();
    await tenantChoice.click();
    await page.getByLabel("Modo").selectOption("LIVE");
    await page.getByRole("button", { name: "Aplicar" }).click();
    await expect.poll(() => new URL(page.url()).searchParams.get("tenantId")).toBeTruthy();

    const calls = page.getByRole("table", { name: "Chamadas de LLM" });
    const seededCall = calls.getByRole("row").filter({ hasText: "qa-vision-model" });
    await expect(seededCall.first()).toBeVisible();
    const inspectionId = await seededCall.first().locator('[data-label="Inspeção"]').innerText();
    const inspectionFilter = page.getByLabel("Inspection ID");
    await inspectionFilter.fill(inspectionId);
    await expect(inspectionFilter).toHaveValue(inspectionId);
    await inspectionFilter.press("Enter");
    await expect.poll(() => new URL(page.url()).searchParams.get("inspectionId")).toBe(inspectionId);
    const selectedCall = calls.getByRole("row").filter({ hasText: inspectionId });
    await expect(selectedCall.first()).toContainText("qa-vision-model");
    await selectedCall.first().getByRole("button", { name: "Abrir detalhes" }).click();
    const detail = page.getByRole("dialog", { name: "Detalhes da chamada de LLM" });
    await expect(detail).toContainText("qa-vision-model");
    await expect(detail).toContainText("LIVE");
    await expect(detail).toContainText(inspectionId);
  });

  test("executes every Admin query without permission or GraphQL errors", async ({ page }) => {
    test.setTimeout(90_000);
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/organization");
    await expect(page.locator(".admin-header")).toContainText("Inspeção · Admin");
    await expect(page.getByRole("navigation", { name: "Navegação administrativa" }).getByRole("link", { name: "Organização" })).toHaveAttribute("aria-current", "page");
    await expect(page.getByRole("columnheader", { name: "Unidade" }).or(page.getByRole("list", { name: "Organização: registros" }))).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "ID", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Abrir detalhes" }).first()).toBeVisible();
    await expect(page.getByText("Você não tem permissão para acessar este recurso neste escopo.")).toHaveCount(0);
    for (const [label, route] of [["Visão geral", "/overview"], ["Organização", "/organization"], ["Usuários e acessos", "/access"], ["Responsáveis pela vistoria", "/catalogs"], ["Configuração", "/assets"], ["Governança", "/governance"], ["Auditoria", "/audit"], ["Consumo de LLM", "/llm-usage"]] as const) {
      await navigateAdmin(page, label);
      await expect(page).toHaveURL(new RegExp(`${route}(?:\\?|$)`), { timeout: 15_000 });
      if (route === "/llm-usage") {
        await page.locator(".llm-usage-advanced-filters > summary").click();
        await page.getByRole("combobox", { name: "Modo" }).selectOption("MOCK");
        await page.getByRole("button", { name: "Aplicar" }).click();
        await expect(page.getByLabel("Resumo do consumo de LLM")).toBeVisible();
        await expect(page.getByRole("table", { name: "Chamadas de LLM" }).or(page.getByRole("heading", { name: "Nenhuma chamada encontrada" }))).toBeVisible();
      } else await expect(page.locator(".status-line")).toContainText("atualizado");
      await expect(page.getByRole("columnheader", { name: "ID", exact: true })).toHaveCount(0);
    }
    await assertRuntimeClean();
  });
});
