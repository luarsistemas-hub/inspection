import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { expect, test, type APIRequestContext } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin } from "./support/auth";

type MailSummary = { ID: string; Created: string; Subject: string; To: Array<{ Address: string }> };
type MailList = { messages: MailSummary[] };
type MailDetail = { Text: string };
const mailpitURL = process.env.PLAYWRIGHT_MAILPIT_URL ?? "http://localhost:8026";

async function latestCaptureOTP(request: APIRequestContext): Promise<{ id: string; code: string } | undefined> {
  const listResponse = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!listResponse.ok()) return undefined;
  const list = await listResponse.json() as MailList;
  const message = list.messages
    .filter((candidate) => candidate.Subject === "Seu código de acesso" && candidate.To.some((recipient) => recipient.Address === "qa.inspection@example.test"))
    .sort((left, right) => Date.parse(right.Created) - Date.parse(left.Created))[0];
  if (!message) return undefined;
  const detailResponse = await request.get(`${mailpitURL}/api/v1/message/${message.ID}`);
  if (!detailResponse.ok()) return undefined;
  const body = ((await detailResponse.json()) as MailDetail).Text ?? "";
  const code = body.match(/Código:\s*(\d{6})/)?.[1];
  return code ? { id: message.ID, code } : undefined;
}

test.describe("Dashboard to Capture recapture journey", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack and QA seed");

  test("requests a replacement and records the authoritative operation outcome", async ({ page, request }) => {
    test.setTimeout(60_000);
    const output = execFileSync("./scripts/local.sh", ["seed"], { cwd: resolve(process.cwd(), "../.."), encoding: "utf8" });
    const captureURL = output.match(/Capture URL: (\S+)/)?.[1];
    expect(captureURL).toBeTruthy();
    const capture = await page.context().newPage();
    const previousOTP = await latestCaptureOTP(request);
    await capture.goto(captureURL!);
    await expect(capture.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
    let code = "";
    await expect.poll(async () => {
      const latest = await latestCaptureOTP(request);
      code = latest?.id !== previousOTP?.id ? latest?.code ?? "" : "";
      return code;
    }, { timeout: 10_000, message: "Capture OTP to arrive in local Mailpit" }).toMatch(/^\d{6}$/);
    await capture.getByLabel("Código de seis dígitos").fill(code);
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
    const refreshInspections = async () => {
      const responsePromise = page.waitForResponse((response) => response.url().endsWith("/graphql") && response.request().postData()?.includes("query Inspections") === true);
      await page.getByRole("button", { name: "Buscar vistorias" }).click();
      expect((await responsePromise).ok()).toBe(true);
    };
    await refreshInspections();
    await page.getByRole("combobox", { name: "Situação" }).selectOption("concluidas");
    await refreshInspections();
    const item = page.locator("[data-inspection-id]").filter({ hasText: "Concluída" }).first();
    await expect(item).toBeVisible({ timeout: 15_000 });
    await item.locator(".inspection-action-menu summary").click();
    const responsePromise = page.waitForResponse((response) => response.url().endsWith("/graphql") && response.request().postData()?.includes("mutation RequestRecapture") === true);
    await item.getByRole("button", { name: "Solicitar complemento" }).click();
    const dialog = page.getByRole("dialog", { name: "Solicitar complemento" });
    await dialog.getByLabel("Requisito do complemento").fill("fachada-geral");
    const deadline = new Date(Date.now() + 86_400_000);
    await dialog.getByLabel("Prazo final").fill(deadline.toISOString().slice(0, 16));
    await dialog.getByLabel("Motivo do complemento").fill("A imagem precisa de mais detalhes.");
    await dialog.getByRole("button", { name: "Solicitar complemento", exact: true }).click();
    const response = await responsePromise;
    const body = await response.json() as { errors?: unknown[]; data?: { requestRecapture?: { userErrors?: unknown[] } } };
    expect(body.errors).toBeUndefined();
    expect(body.data?.requestRecapture?.userErrors).toEqual([]);
    await expect(page.getByRole("status").filter({ hasText: "Complemento solicitado" })).toBeVisible();
    await assertRuntimeClean();
  });
});
