import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { expect, test, type APIRequestContext } from "@playwright/test";
import { installRuntimeGuards } from "./support/runtime";

type MailpitMessage = { Snippet?: string; Subject?: string; To?: Array<{ Address?: string }>; Created?: string };

function seedCaptureURL(): string {
  const workspace = resolve(process.cwd(), "../..");
  const output = execFileSync("./scripts/local.sh", ["seed"], { cwd: workspace, encoding: "utf8" });
  const url = output.match(/Capture URL: (\S+)/)?.[1];
  if (!url) throw new Error("local seed did not return a Capture URL");
  return url;
}

async function latestOTP(request: APIRequestContext, since: string): Promise<string> {
  const response = await request.get("http://localhost:8026/api/v1/messages?limit=20");
  const body = await response.json() as { messages?: MailpitMessage[] };
  const messages = [...(body.messages ?? [])].sort((left, right) => (right.Created ?? "").localeCompare(left.Created ?? ""));
  return messages.find((message) => (message.Created ?? "") > since && message.Subject === "Seu codigo de acesso" && message.To?.some((target) => target.Address === "qa.inspection@example.test"))?.Snippet?.match(/Codigo:\s*(\d{6})/)?.[1] ?? "";
}

test.describe("authenticated Capture against the local stack", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack");

  test("completes the guided OTP, consent, review and submission flow", async ({ page, request }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    const startedAt = new Date().toISOString();
    await page.goto(seedCaptureURL());
    await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
    let code = "";
    await expect.poll(async () => { code = await latestOTP(request, startedAt); return code; }, { timeout: 15_000 }).toMatch(/^\d{6}$/);
    await page.getByLabel("Código de seis dígitos").fill(code);
    await page.getByRole("button", { name: "Confirmar código" }).click();
    await expect(page.getByRole("listitem").filter({ hasText: "Autorizações" })).toHaveAttribute("aria-current", "step");
    await page.getByLabel("Processamento das fotos").check();
    await page.getByLabel("Análise por inteligência artificial").check();
    await page.getByLabel("Localização quando necessária").check();
    await page.getByRole("button", { name: "Aceitar e continuar" }).click();
    await expect(page.getByRole("heading", { name: "Adicione as evidências" })).toBeVisible();
    await expect(page.getByRole("listitem").filter({ hasText: "Evidências" })).toHaveAttribute("aria-current", "step");
    const impossibility = page.getByLabel("Impossibilidade");
    const canDeclareImpossibility = await impossibility.isVisible();
    if (canDeclareImpossibility) {
      await impossibility.fill("A área está inacessível com segurança durante a vistoria.");
      await page.getByRole("button", { name: "Registrar impossibilidade" }).click();
      await expect(page.getByRole("status")).toContainText("Justificativa registrada.");
    }
    await page.getByRole("button", { name: "Revisar inspeção" }).click();
    await expect(page.getByRole("heading", { name: "Revise antes de enviar" })).toBeVisible();
    await expect(page.getByRole("listitem").filter({ hasText: "Revisão" })).toHaveAttribute("aria-current", "step");
    await page.getByRole("button", { name: canDeclareImpossibility ? "Enviar inspeção completa" : "Confirmar envio incompleto" }).click();
    await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
    await assertRuntimeClean();
  });
});
