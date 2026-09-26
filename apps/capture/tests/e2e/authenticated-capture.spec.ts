import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { expect, test } from "@playwright/test";
import { installRuntimeGuards } from "./support/runtime";

function seedCaptureURL(): string {
  const workspace = resolve(process.cwd(), "../..");
  const output = execFileSync("./scripts/local.sh", ["seed"], { cwd: workspace, encoding: "utf8" });
  const url = output.match(/Capture URL: (\S+)/)?.[1];
  if (!url) throw new Error("local seed did not return a Capture URL");
  return url;
}

test.describe("authenticated Capture against the local stack", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack");

  test("completes the guided OTP, consent, review and submission flow", async ({ page }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    await page.goto(seedCaptureURL());
    await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
    await page.getByLabel("Código de seis dígitos").fill("654321");
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
