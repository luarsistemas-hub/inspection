import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { expect, test } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin } from "./support/auth";

test.describe("Dashboard to Capture recapture journey", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");

  test.beforeEach(() => { execFileSync("docker", ["stop", "inspection-inspection-worker-1"]); });
  test.afterEach(() => { execFileSync("docker", ["start", "inspection-inspection-worker-1"]); });

  test("requests a replacement and records the authoritative operation outcome", async ({ page }) => {
    test.setTimeout(60_000);
    const output = execFileSync("./scripts/local.sh", ["seed"], { cwd: resolve(process.cwd(), "../.."), encoding: "utf8" });
    const captureURL = output.match(/Capture URL: (\S+)/)?.[1];
    expect(captureURL).toBeTruthy();
    const capture = await page.context().newPage();
    await capture.goto(captureURL!);
    await expect(capture.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
    await capture.getByLabel("Código de seis dígitos").fill("654321");
    await capture.getByRole("button", { name: "Confirmar código" }).click();
    await capture.getByLabel("Processamento das fotos").check();
    await capture.getByLabel("Análise por inteligência artificial").check();
    await capture.getByLabel("Localização quando necessária").check();
    await capture.getByRole("button", { name: "Aceitar e continuar" }).click();
    await expect(capture.getByRole("heading", { name: "Adicione as evidências" })).toBeVisible();
    await capture.getByRole("button", { name: "Não consegue fotografar?" }).click();
    await capture.getByLabel("Justificativa da impossibilidade").fill("A fachada está inacessível para a vistoria.");
    await capture.getByRole("button", { name: "Registrar impossibilidade" }).click();
    await capture.getByRole("button", { name: "Revisar vistoria" }).click();
    await capture.getByRole("button", { name: "Enviar vistoria completa" }).click();
    await expect(capture.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
    await capture.close();

    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/inspections");
    await page.getByRole("button", { name: "Carregar vistorias" }).click();
    await page.getByRole("combobox", { name: "Situação" }).selectOption("execucao");
    const item = page.locator("[data-inspection-id]").filter({ hasText: /Em análise|Enviada/ }).first();
    await expect(item).toBeVisible();
    const answers = ["A imagem precisa de mais detalhes.", new Date(Date.now() + 86_400_000).toISOString(), "fachada-geral"];
    page.on("dialog", (dialog) => void dialog.accept(answers.shift()));
    await item.locator(".inspection-action-menu summary").click();
    const responsePromise = page.waitForResponse((response) => response.url().endsWith("/graphql") && response.request().postData()?.includes("mutation RequestRecapture") === true);
    await item.getByRole("button", { name: "Solicitar complemento" }).click();
    const response = await responsePromise;
    const body = await response.json() as { errors?: unknown[]; data?: { requestRecapture?: { userErrors?: unknown[] } } };
    expect(body.errors).toBeUndefined();
    expect(body.data?.requestRecapture?.userErrors).toEqual([]);
    await expect(page.getByRole("status").filter({ hasText: "Complemento solicitado" })).toBeVisible();
    await assertRuntimeClean();
  });
});
