import { expect, test } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin } from "./support/auth";

test.describe("CLD E2E-001 · modelo v3", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "execute com a stack local e o seed de QA");
  test("admin publica componentes esperados e explica publicação automática do laudo novo", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/templates");
    await page.getByRole("button", { name: "Novo modelo" }).click();
    const form = page.locator(".admin-template-editor form");
    await form.getByLabel("Chave do modelo").fill(`qa-scope-${Date.now()}`);
    await form.getByLabel("Nome do modelo").fill("Escopo descritivo QA");
    await form.getByRole("button", { name: "Adicionar ambiente esperado" }).click();
    const room = form.locator("fieldset").filter({ has: form.getByLabel("Chave do ambiente") }).last();
    await room.getByLabel("Chave do ambiente").fill("kitchen");
    const roomName = room.getByLabel("Nome do ambiente");
    await roomName.pressSequentially("Cozinha");
    await expect(roomName).toBeFocused();
    await room.getByRole("button", { name: /Adicionar componente a Cozinha/ }).click();
    await room.getByLabel("Chave do componente").fill("piso");
    await room.getByLabel("Componente esperado").fill("Piso");
    const publish = page.waitForRequest((request) => request.url().endsWith("/graphql") && request.postData()?.includes("mutation AdminPublishTemplateVersion") === true);
    await form.getByRole("button", { name: "Publicar nova versão do modelo" }).click();
    const input = (await publish).postDataJSON().variables.input;
    expect(input.definition.schemaVersion).toBe(3);
    expect(input.definition.expectedRooms).toContainEqual(expect.objectContaining({ key: "kitchen", name: "Cozinha", components: expect.arrayContaining([expect.objectContaining({ key: "piso", label: "Piso" })]) }));
    await page.goto("/governance");
    await page.getByRole("button", { name: "Configurar política" }).click();
    await expect(page.getByText(/A política manual continua valendo para fluxos antigos\. Laudos descritivos e comparativos novos são publicados automaticamente/)).toBeVisible();
    await assertRuntimeClean();
  });

  test("publica vídeo em schema v2 e sem escopo descritivo", async ({ page }) => {
    await loginAsLocalAdmin(page, "/templates");
    await page.getByRole("button", { name: "Novo modelo" }).click();
    const form = page.locator(".admin-template-editor form");
    await form.getByLabel("Chave do modelo").fill(`qa-video-${Date.now()}`);
    await form.getByLabel("Nome do modelo").fill("Modelo de vídeo QA");
    await form.getByLabel("Inspeção por vídeo").check();
    await expect(form.getByText("Escopo esperado · schema v3")).toHaveCount(0);
    const publish = page.waitForRequest((request) => request.url().endsWith("/graphql") && request.postData()?.includes("mutation AdminPublishTemplateVersion") === true);
    await form.getByRole("button", { name: "Publicar nova versão do modelo" }).click();
    const input = (await publish).postDataJSON().variables.input;
    expect(input.definition).toMatchObject({ schemaVersion: 2, isVideo: true });
    expect(input.definition).not.toHaveProperty("expectedRooms");
    expect(input.definition).not.toHaveProperty("assetProfile");
  });
});
