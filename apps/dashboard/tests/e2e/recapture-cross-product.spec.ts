import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { deflateSync } from "node:zlib";
import { expect, test, type APIRequestContext } from "@playwright/test";
import { installRuntimeGuards, loginAsLocalAdmin } from "./support/auth";

type MailSummary = { ID: string; Created: string; Subject: string; To: Array<{ Address: string }> };
type MailList = { messages: MailSummary[] };
type MailDetail = { Text: string };
type InspectionNode = { id: string; sourceReason?: string | null; status: string };
type InspectionsResponse = {
  data?: { inspections?: { nodes?: InspectionNode[]; pageInfo?: { endCursor?: string | null; hasNextPage: boolean } } };
};
const mailpitURL = process.env.PLAYWRIGHT_MAILPIT_URL ?? "http://localhost:8026";

function pngFixture(): Buffer {
  const width = 64;
  const height = 64;
  const pixels = Buffer.alloc(height * (1 + width * 3));
  for (let y = 0; y < height; y += 1) {
    const row = y * (1 + width * 3);
    pixels[row] = 0;
    for (let x = 0; x < width; x += 1) {
      const offset = row + 1 + x * 3;
      const value = 100 + ((x * 17 + y * 31) % 120);
      pixels[offset] = value;
      pixels[offset + 1] = Math.min(255, value + x % 5);
      pixels[offset + 2] = Math.max(0, value - y % 5);
    }
  }
  const chunk = (type: string, data: Buffer) => {
    const body = Buffer.concat([Buffer.from(type), data]);
    let crc = 0xffffffff;
    for (const byte of body) {
      crc ^= byte;
      for (let bit = 0; bit < 8; bit += 1) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
    }
    const result = Buffer.alloc(data.length + 12);
    result.writeUInt32BE(data.length, 0);
    body.copy(result, 4);
    result.writeUInt32BE((crc ^ 0xffffffff) >>> 0, data.length + 8);
    return result;
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8;
  header[9] = 2;
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(pixels)),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

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
    test.setTimeout(120_000);
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginAsLocalAdmin(page, "/inspections");
    const refreshInspections = async () => {
      const responsePromise = page.waitForResponse((response) => response.url().endsWith("/graphql") && response.request().postData()?.includes("query Inspections") === true);
      await page.getByRole("button", { name: "Buscar vistorias" }).click();
      const response = await responsePromise;
      expect(response.ok()).toBe(true);
      return (await response.json()) as InspectionsResponse;
    };
    const output = execFileSync("./scripts/local.sh", ["seed"], { cwd: resolve(process.cwd(), "../.."), encoding: "utf8" });
    const captureURL = output.match(/Capture URL: (\S+)/)?.[1];
    const inspectionID = output.match(/Inspection ID: ([\da-f-]{36})/i)?.[1];
    expect(captureURL).toBeTruthy();
    expect(inspectionID).toBeTruthy();
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
    await capture.getByRole("button", { name: "Escolher da galeria" }).click();
    await capture.getByLabel("Escolher uma foto da galeria").setInputFiles({
      name: "fachada.png",
      mimeType: "image/png",
      buffer: pngFixture(),
    });
    await expect(capture.getByRole("button", { name: "Usar esta foto" })).toBeVisible();
    await capture.getByRole("button", { name: "Usar esta foto" }).click();
    await expect(capture.getByText("Recebida e verificada pelo servidor")).toBeVisible({ timeout: 45_000 });
    await capture.getByRole("button", { name: "Revisar vistoria" }).click();
    await capture.getByRole("button", { name: "Enviar vistoria completa" }).click();
    await expect(capture.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
    await capture.close();

    await page.getByRole("combobox", { name: "Situação" }).selectOption("concluidas");
    await page.getByRole("textbox", { name: "Buscar vistoria" }).fill(inspectionID!);
    await expect.poll(async () => {
      const response = await refreshInspections();
      return (response.data?.inspections?.nodes ?? []).some((inspection) => inspection.id === inspectionID && inspection.status === "COMPLETED");
    }, { timeout: 90_000, intervals: [2_000], message: "the inspection created by this seed to reach a recapture-eligible state" }).toBeTruthy();
    const item = page.locator(`[data-inspection-id="${inspectionID}"]`);
    await expect(item).toBeVisible();
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
