import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { expect, test, type APIRequestContext } from "@playwright/test";
import { installRuntimeGuards } from "./support/runtime";

type MailSummary = { ID: string; Subject: string; To: Array<{ Address: string }> };
type MailList = { messages: MailSummary[] };
type MailDetail = { Text: string };
const mailpitURL = process.env.PLAYWRIGHT_MAILPIT_URL ?? "http://localhost:8026";

function seedCaptureURL(): string {
  const workspace = resolve(process.cwd(), "../..");
  const output = execFileSync("./scripts/local.sh", ["seed"], { cwd: workspace, encoding: "utf8" });
  const url = output.match(/Capture URL: (\S+)/)?.[1];
  if (!url) throw new Error("local seed did not return a Capture URL");
  const seeded = new URL(url);
  const baseURL = new URL(process.env.PLAYWRIGHT_BASE_URL ?? `http://127.0.0.1:${process.env.PLAYWRIGHT_PORT ?? "3103"}`);
  return new URL(`${seeded.pathname}${seeded.search}`, baseURL).toString();
}

async function latestCaptureOTP(request: APIRequestContext): Promise<string> {
  const listResponse = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!listResponse.ok()) return "";
  const list = await listResponse.json() as MailList;
  const message = list.messages.find((candidate) => candidate.Subject === "Seu código de acesso" && candidate.To.some((recipient) => recipient.Address === "qa.inspection@example.test"));
  if (!message) return "";
  const detailResponse = await request.get(`${mailpitURL}/api/v1/message/${message.ID}`);
  if (!detailResponse.ok()) return "";
  const body = ((await detailResponse.json()) as MailDetail).Text ?? "";
  return body.match(/Código:\s*(\d{6})/)?.[1] ?? "";
}

test.describe("authenticated Capture against the local stack", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack");

  test("E2E-032 completes the real invitation, consent, review and submission flow", async ({ page, request }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await page.goto(seedCaptureURL());
    await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
    await page.getByRole("button", { name: "Solicitar outro código" }).click();
    let code = "";
    await expect.poll(async () => {
      code = await latestCaptureOTP(request);
      return code;
    }, { message: "Capture OTP to arrive in local Mailpit" }).toMatch(/^\d{6}$/);
    await page.getByLabel("Código de seis dígitos").fill(code);
    await page.getByRole("button", { name: "Confirmar código" }).click();
    await expect(page.getByRole("listitem").filter({ hasText: "Autorizações" })).toHaveAttribute("aria-current", "step");
    await page.getByLabel("Processamento das fotos").check();
    await page.getByLabel("Análise por inteligência artificial").check();
    await page.getByLabel("Localização quando necessária").check();
    await page.getByRole("button", { name: "Aceitar e continuar" }).click();
    await expect(page.getByRole("heading", { name: "Adicione as evidências" })).toBeVisible();
    await expect(page.getByRole("listitem").filter({ hasText: "Evidências" })).toHaveAttribute("aria-current", "step");
    const impossibility = page.getByRole("button", { name: "Não consegue fotografar?" });
    const canDeclareImpossibility = await impossibility.isVisible();
    if (canDeclareImpossibility) {
      await impossibility.click();
      await page.getByLabel("Justificativa da impossibilidade").fill("A área está inacessível com segurança durante a vistoria.");
      await page.getByRole("button", { name: "Registrar impossibilidade" }).click();
      await expect(page.getByRole("status")).toContainText("Justificativa registrada.");
    }
    await page.getByRole("button", { name: "Revisar vistoria" }).click();
    await expect(page.getByRole("heading", { name: "Revise antes de enviar" })).toBeVisible();
    await expect(page.getByRole("listitem").filter({ hasText: "Revisão" })).toHaveAttribute("aria-current", "step");
    await page.getByRole("button", { name: canDeclareImpossibility ? "Enviar vistoria completa" : "Confirmar envio incompleto" }).click();
    await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
    await assertRuntimeClean();
  });
});
