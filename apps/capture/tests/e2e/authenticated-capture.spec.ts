import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { installFakeGetUserMedia } from "./support/get-user-media";
import { installRuntimeGuards } from "./support/runtime";

type MailSummary = { ID: string; Subject: string; To: Array<{ Address: string }> };
type MailList = { messages: MailSummary[] };
type MailDetail = { Text: string };
const mailpitURL = process.env.PLAYWRIGHT_MAILPIT_URL ?? "http://localhost:8026";

function seedCaptureURL(mediaKind: "PHOTO" | "VIDEO" = "PHOTO"): string {
  const workspace = resolve(process.cwd(), "../..");
  const output = execFileSync("./scripts/local.sh", ["seed"], { cwd: workspace, encoding: "utf8", env: { ...process.env, INSPECTION_QA_MEDIA_KIND: mediaKind, INSPECTION_QA_ENABLE_VIDEO_ADMISSION: mediaKind === "VIDEO" ? "true" : "false" } });
  const url = output.match(/Capture URL: (\S+)/)?.[1];
  if (!url) throw new Error("local seed did not return a Capture URL");
  const seeded = new URL(url);
  const baseURL = new URL(process.env.PLAYWRIGHT_BASE_URL ?? `http://127.0.0.1:${process.env.PLAYWRIGHT_PORT ?? "3103"}`);
  return new URL(`${seeded.pathname}${seeded.search}`, baseURL).toString();
}

async function captureOTPMessageIDs(request: APIRequestContext): Promise<Set<string>> {
  const listResponse = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!listResponse.ok()) return new Set();
  const list = await listResponse.json() as MailList;
  return new Set(list.messages.filter((candidate) => candidate.Subject === "Seu código de acesso" && candidate.To.some((recipient) => recipient.Address === "qa.inspection@example.test")).map((candidate) => candidate.ID));
}

async function latestCaptureOTP(request: APIRequestContext, existing: Set<string>): Promise<string> {
  const listResponse = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!listResponse.ok()) return "";
  const list = await listResponse.json() as MailList;
  const message = list.messages.find((candidate) => !existing.has(candidate.ID) && candidate.Subject === "Seu código de acesso" && candidate.To.some((recipient) => recipient.Address === "qa.inspection@example.test"));
  if (!message) return "";
  const detailResponse = await request.get(`${mailpitURL}/api/v1/message/${message.ID}`);
  if (!detailResponse.ok()) return "";
  const body = ((await detailResponse.json()) as MailDetail).Text ?? "";
  return body.match(/Código:\s*(\d{6})/)?.[1] ?? "";
}

async function authenticateVideoCapture(page: Page, request: APIRequestContext): Promise<string> {
  const url = seedCaptureURL("VIDEO");
  const existingMessages = await captureOTPMessageIDs(request);
  await page.goto(url);
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  let code = "";
  await expect.poll(async () => {
    code = await latestCaptureOTP(request, existingMessages);
    return code;
  }, { message: "VIDEO Capture OTP to arrive in local Mailpit" }).toMatch(/^\d{6}$/);
  await page.getByLabel("Código de seis dígitos").fill(code);
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByLabel("Processamento do vídeo e imagens derivadas")).toBeVisible({ timeout: 12_000 });
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Registrar vistoria em vídeo" })).toBeVisible();
  return url;
}

async function installCanvasCamera(page: Page, highEntropy = false): Promise<void> {
  await installFakeGetUserMedia(page, {
    kind: "canvas",
    width: highEntropy ? 640 : 320,
    height: highEntropy ? 360 : 180,
    highEntropy,
    captureConstraints: true
  });
}

async function authenticateAdminVideoPolicy(page: Page): Promise<void> {
  await page.goto("http://localhost:3000/video");
  await Promise.all([
    page.waitForURL(/localhost:8081\/realms\/inspection\/protocol\/openid-connect\/auth|localhost:3000\/auth\/callback/),
    page.getByRole("button", { name: "Entrar com conta administrativa" }).click()
  ]);
  const loginFormVisible = await page.locator("#username").waitFor({ state: "visible", timeout: 2_000 }).then(() => true, () => false);
  if (loginFormVisible) {
    await page.locator("#username").fill(process.env.INSPECTION_E2E_USERNAME ?? "admin");
    await page.locator("#password").fill(process.env.INSPECTION_E2E_PASSWORD ?? "admin");
    await page.locator("button[type=submit]").click();
  }
  await page.waitForURL("http://localhost:3000/video");
  await expect(page.getByLabel("Duração máxima por vídeo (minutos)")).toBeVisible();
}

test.describe("authenticated Capture against the local stack", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "set INSPECTION_E2E_AUTH=true with the local stack");

  test("E2E-032 completes the real invitation, consent, review and submission flow", async ({ page, request }) => {
    const assertRuntimeClean = installRuntimeGuards(page);
    const url = seedCaptureURL();
    const existingMessages = await captureOTPMessageIDs(request);
    await page.goto(url);
    await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
    let code = "";
    await expect.poll(async () => {
      code = await latestCaptureOTP(request, existingMessages);
      return code;
    }, { message: "Capture OTP to arrive in local Mailpit" }).toMatch(/^\d{6}$/);
    await page.getByLabel("Código de seis dígitos").fill(code);
    await expect(page.getByRole("button", { name: "Confirmar código" })).toBeEnabled();
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

  test("E2E-008 records, previews and receives one video through the local API and MinIO", async ({ page, request }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "The browser media fixture is validated in Chromium");
    test.setTimeout(120_000);
    const assertRuntimeClean = installRuntimeGuards(page);
    await installCanvasCamera(page);
    await authenticateVideoCapture(page, request);
    await page.getByRole("button", { name: "Começar gravação" }).click();
    await expect(page.getByText(/Gravando 00:/)).toBeVisible();
    expect(await page.evaluate(() => (window as typeof window & { captureConstraints?: MediaStreamConstraints }).captureConstraints?.audio)).toBe(false);
    await page.waitForTimeout(1200);
    await page.getByRole("button", { name: "Parar e revisar" }).click();
    await expect(page.getByRole("heading", { name: "Revise a gravação" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();
    await page.getByRole("button", { name: "Descartar e gravar novamente" }).click();
    await page.getByRole("button", { name: "Começar gravação" }).click();
    await page.waitForTimeout(1200);
    await page.getByRole("button", { name: "Parar e revisar" }).click();
    await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();
    await page.getByRole("button", { name: "Confirmar e enviar" }).click();
    await expect(page.getByText("Vídeo recebido", { exact: true })).toBeVisible({ timeout: 30_000 });
    await assertRuntimeClean();
  });

  test("IT-115 requires renewed authorization when the session expires during recording", async ({ page, request, context }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "The browser media fixture is validated in Chromium");
    test.setTimeout(120_000);
    await installCanvasCamera(page);
    await authenticateVideoCapture(page, request);
    let uploadCreates = 0;
    page.on("request", (outgoing) => {
      if (outgoing.url().endsWith("/graphql") && String(outgoing.postDataJSON()?.query ?? "").includes("CreateMediaUpload")) uploadCreates += 1;
    });
    await page.getByRole("button", { name: "Começar gravação" }).click();
    await expect(page.getByText(/Gravando 00:/)).toBeVisible();
    await context.clearCookies();
    await page.waitForTimeout(1200);
    await page.getByRole("button", { name: "Parar e revisar" }).click();
    await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();
    await page.getByRole("button", { name: "Confirmar e enviar" }).click();
    await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
    await expect(page.getByText("Vídeo recebido", { exact: true })).toHaveCount(0);
    expect(uploadCreates).toBe(0);
  });

  test("IT-117/IT-118 keeps a finalized offline video for authenticated resume", async ({ page, request, context }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "The browser media fixture is validated in Chromium");
    test.setTimeout(120_000);
    const assertRuntimeClean = installRuntimeGuards(page);
    await installCanvasCamera(page);
    await authenticateVideoCapture(page, request);

    let createUploadRequests = 0;
    page.on("request", (outgoing) => {
      if (outgoing.url().endsWith("/graphql") && String(outgoing.postDataJSON()?.query ?? "").includes("CreateMediaUpload")) createUploadRequests += 1;
    });
    await page.getByRole("button", { name: "Começar gravação" }).click();
    await expect(page.getByText(/Gravando 00:/)).toBeVisible();
    await page.waitForTimeout(1200);
    await context.setOffline(true);
    await expect(page.getByText("Sem conexão.", { exact: false })).toBeVisible();
    await page.getByRole("button", { name: "Parar e revisar" }).click();
    await expect(page.getByText("Salva localmente; envio pendente", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeDisabled();
    expect(createUploadRequests).toBe(0);

    await context.setOffline(false);
    const existingMessages = await captureOTPMessageIDs(request);
    await page.reload();
    await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
    let code = "";
    await expect.poll(async () => {
      code = await latestCaptureOTP(request, existingMessages);
      return code;
    }, { message: "VIDEO Capture OTP after offline draft reload" }).toMatch(/^\d{6}$/);
    await page.getByLabel("Código de seis dígitos").fill(code);
    await page.getByRole("button", { name: "Confirmar código" }).click();
    await expect(page.getByLabel("Processamento do vídeo e imagens derivadas")).toBeVisible();
    await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
    await page.getByLabel("Análise por inteligência artificial").check();
    await page.getByLabel("Localização quando necessária").check();
    await page.getByRole("button", { name: "Aceitar e continuar" }).click();
    await expect(page.getByText("Salva localmente; envio pendente", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Revisar" }).click();
    await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();
    await page.getByRole("button", { name: "Confirmar e enviar" }).click();
    await expect(page.getByText("Vídeo recebido", { exact: true })).toBeVisible({ timeout: 30_000 });
    expect(createUploadRequests).toBe(1);
    await assertRuntimeClean();
  });

  test("IT-126 serializes two tabs resuming the same accepted video attempt", async ({ page, request, context }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "The browser media fixture is validated in Chromium");
    test.setTimeout(120_000);
    await installCanvasCamera(page);
    const inviteURL = await authenticateVideoCapture(page, request);
    await page.getByRole("button", { name: "Começar gravação" }).click();
    await expect(page.getByText(/Gravando 00:/)).toBeVisible();
    await page.waitForTimeout(1200);
    await page.getByRole("button", { name: "Parar e revisar" }).click();
    await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();

    const existingMessages = await captureOTPMessageIDs(request);
    const second = await context.newPage();
    let mediaCreates = 0;
    context.on("request", (outgoing) => {
      if (outgoing.url().endsWith("/graphql") && String(outgoing.postDataJSON()?.query ?? "").includes("CreateMediaUpload")) mediaCreates += 1;
    });
    try {
      await second.goto(inviteURL);
      await expect(second.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
      let code = "";
      await expect.poll(async () => { code = await latestCaptureOTP(request, existingMessages); return code; }).toMatch(/^\d{6}$/);
      await second.getByLabel("Código de seis dígitos").fill(code);
      await second.getByRole("button", { name: "Confirmar código" }).click();
      await expect(second.getByLabel("Processamento do vídeo e imagens derivadas")).toBeVisible({ timeout: 12_000 });
      await second.getByLabel("Processamento do vídeo e imagens derivadas").check();
      await second.getByLabel("Análise por inteligência artificial").check();
      await second.getByLabel("Localização quando necessária").check();
      await second.getByRole("button", { name: "Aceitar e continuar" }).click();
      await expect(second.getByRole("heading", { name: "Registrar vistoria em vídeo" })).toBeVisible();
      await expect(second.getByText("Salva localmente; envio pendente", { exact: true })).toBeVisible();
      await second.getByRole("button", { name: "Revisar" }).click();
      await expect(second.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();
      await Promise.all([
        page.getByRole("button", { name: "Confirmar e enviar" }).click(),
        second.getByRole("button", { name: "Confirmar e enviar" }).click()
      ]);
      await expect(page.getByText("Vídeo recebido", { exact: true })).toBeVisible({ timeout: 60_000 });
      await expect(second.getByText("Vídeo recebido", { exact: true })).toBeVisible({ timeout: 60_000 });
      expect(mediaCreates).toBe(1);
    } finally {
      await second.close();
    }
  });

  test("E2E-009 resumes only missing multipart parts after offline save and renewed access", async ({ page, request, context }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "The browser media fixture is validated in Chromium");
    test.setTimeout(180_000);
    let expectedNetworkFailures = 0;
    const assertRuntimeClean = installRuntimeGuards(page, (failure) => {
      if (failure !== "console: Failed to load resource: net::ERR_FAILED") return false;
      expectedNetworkFailures += 1;
      return true;
    });
    await installCanvasCamera(page, true);
    await authenticateVideoCapture(page, request);
    await page.getByRole("button", { name: "Começar gravação" }).click();
    await expect(page.getByText(/Gravando 00:/)).toBeVisible();
    await page.waitForTimeout(35_000);
    await context.setOffline(true);
    await page.getByRole("button", { name: "Parar e revisar" }).click();
    await expect(page.getByText("Salva localmente; envio pendente", { exact: true })).toBeVisible();
    const savedSize = await page.evaluate(async () => {
      const request = indexedDB.open("inspection-video-capture-v1");
      const database = await new Promise<IDBDatabase>((resolve, reject) => { request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error); });
      const drafts = await new Promise<Array<{ sizeBytes: number }>>((resolve, reject) => {
        const get = database.transaction("videoDrafts").objectStore("videoDrafts").getAll();
        get.onsuccess = () => resolve(get.result); get.onerror = () => reject(get.error);
      });
      database.close();
      return drafts[0]?.sizeBytes ?? 0;
    });
    expect(savedSize, "the recorded video must have at least three real MinIO parts").toBeGreaterThan(10 * 1024 * 1024);
    await context.setOffline(false);

    const existingMessages = await captureOTPMessageIDs(request);
    await page.reload();
    await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
    let code = "";
    await expect.poll(async () => { code = await latestCaptureOTP(request, existingMessages); return code; }).toMatch(/^\d{6}$/);
    await page.getByLabel("Código de seis dígitos").fill(code);
    await page.getByRole("button", { name: "Confirmar código" }).click();
    await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
    await page.getByLabel("Análise por inteligência artificial").check();
    await page.getByLabel("Localização quando necessária").check();
    await page.getByRole("button", { name: "Aceitar e continuar" }).click();
    await expect(page.getByText("Salva localmente; envio pendente", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Revisar" }).click();
    await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();

    const partPuts = new Map<string, number>();
    let createUploadRequests = 0;
    let interrupted = false;
    page.on("request", (outgoing) => {
      if (outgoing.url().endsWith("/graphql") && String(outgoing.postDataJSON()?.query ?? "").includes("CreateMediaUpload")) createUploadRequests += 1;
    });
    await page.route("**/*", async (route) => {
      const outgoing = route.request();
      if (outgoing.method() !== "PUT") return route.continue();
      const number = new URL(outgoing.url()).searchParams.get("partNumber") ?? "unknown";
      partPuts.set(number, (partPuts.get(number) ?? 0) + 1);
      if (number === "2" && !interrupted) { interrupted = true; return route.abort("failed"); }
      return route.continue();
    });
    await page.getByRole("button", { name: "Confirmar e enviar" }).click();
    await expect.poll(() => interrupted, { timeout: 60_000 }).toBe(true);
    await expect(page.getByText("Envio pendente", { exact: true })).toBeVisible({ timeout: 30_000 });
    expect(interrupted).toBe(true);
    expect(partPuts.get("1")).toBe(1);
    expect(partPuts.get("2")).toBe(1);

    const retryMessages = await captureOTPMessageIDs(request);
    await page.reload();
    await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
    await expect.poll(async () => { code = await latestCaptureOTP(request, retryMessages); return code; }).toMatch(/^\d{6}$/);
    await page.getByLabel("Código de seis dígitos").fill(code);
    await page.getByRole("button", { name: "Confirmar código" }).click();
    await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
    await page.getByLabel("Análise por inteligência artificial").check();
    await page.getByLabel("Localização quando necessária").check();
    await page.getByRole("button", { name: "Aceitar e continuar" }).click();
    await page.getByRole("button", { name: "Retomar envio" }).click();
    await expect(page.getByText("Vídeo recebido", { exact: true })).toBeVisible({ timeout: 60_000 });
    expect(partPuts.get("1"), "the confirmed first part must not be sent again").toBe(1);
    expect(partPuts.get("2")).toBe(2);
    expect(partPuts.get("3")).toBe(1);
    expect(createUploadRequests).toBe(1);
    await assertRuntimeClean();
    expect(expectedNetworkFailures).toBe(1);
  });

  test("E2E-036 guides an unsupported embedded browser through the real invitation", async ({ page, request }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "The unsupported browser capability fixture is validated in Chromium");
    test.setTimeout(120_000);
    const assertRuntimeClean = installRuntimeGuards(page);
    await page.addInitScript(() => {
      Object.defineProperty(window, "MediaRecorder", { configurable: true, value: undefined });
    });
    await authenticateVideoCapture(page, request);
    await expect(page.getByText("Abra o convite no Safari ou Chrome atualizado do aparelho", { exact: false })).toBeVisible();
    await expect(page.getByText("A gravação não continua em segundo plano", { exact: false })).toBeVisible();
    await expect(page.getByRole("button", { name: "Começar gravação" })).toBeDisabled();
    await assertRuntimeClean();
  });

  test("E2E-002 applies tenant video limits saved in Admin to a new Capture attempt", async ({ page, context, request }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "The cross-application QA fixture is validated in Chromium");
    test.setTimeout(120_000);
    await authenticateAdminVideoPolicy(page);

    const duration = page.getByLabel("Duração máxima por vídeo (minutos)");
    const originDays = page.getByLabel("Retenção dos vídeos de origem (dias)");
    const currentDays = page.getByLabel("Retenção dos vídeos atuais e complementares (dias)");
    await expect(duration).toBeVisible({ timeout: 15_000 });
    const previous = { duration: await duration.inputValue(), originDays: await originDays.inputValue(), currentDays: await currentDays.inputValue() };
    const savePolicy = async (minutes: string, origin: string, current: string) => {
      const priorStatus = await page.getByRole("status").textContent();
      await duration.fill(minutes); await originDays.fill(origin); await currentDays.fill(current);
      await page.getByRole("button", { name: "Salvar limites de vídeo" }).click();
      await expect.poll(() => page.getByRole("status").textContent()).not.toBe(priorStatus);
      await expect(page.getByRole("status")).toContainText(/Versão \d+ carregada/);
      await expect(duration).toHaveValue(minutes);
    };

    try {
      await savePolicy("10", "40", "7");
      const capture = await context.newPage();
      try {
        await installCanvasCamera(capture);
        await authenticateVideoCapture(capture, request);
        await expect(capture.getByText("O tempo máximo é 10:00", { exact: false })).toBeVisible();
        await capture.getByRole("button", { name: "Começar gravação" }).click();
        await expect(capture.getByText(/Gravando 00:00 \/ 10:00/)).toBeVisible();
        await capture.getByRole("button", { name: "Parar e revisar" }).click();
      } finally { await capture.close(); }
    } finally {
      await savePolicy(previous.duration, previous.originDays, previous.currentDays);
    }
  });

  test("IT-025 reloads the committed video policy after its save response is lost", async ({ page }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "The cross-application QA fixture is validated in Chromium");
    test.setTimeout(120_000);
    await authenticateAdminVideoPolicy(page);
    const duration = page.getByLabel("Duração máxima por vídeo (minutos)");
    const originDays = page.getByLabel("Retenção dos vídeos de origem (dias)");
    const currentDays = page.getByLabel("Retenção dos vídeos atuais e complementares (dias)");
    const previous = { duration: await duration.inputValue(), originDays: await originDays.inputValue(), currentDays: await currentDays.inputValue() };
    const versionText = await page.getByRole("status").textContent();
    const priorVersion = Number(versionText?.match(/Versão (\d+) carregada/)?.[1]);
    expect(priorVersion).toBeGreaterThan(0);
    const changedMinutes = previous.duration === "11" ? "12" : "11";
    let committed = false;
    const loseResponse = async (route: import("@playwright/test").Route) => {
      const body = route.request().postData() ?? "";
      if (!body.includes("configureVideoPolicy")) return route.continue();
      const response = await route.fetch();
      expect(response.ok(), "the server must commit before the response is lost").toBe(true);
      const payload = await response.json() as { data?: { configureVideoPolicy?: { policy?: { version: number }; userErrors?: unknown[] } } };
      expect(payload.data?.configureVideoPolicy?.userErrors).toEqual([]);
      expect(payload.data?.configureVideoPolicy?.policy?.version).toBe(priorVersion + 1);
      committed = true;
      await route.abort("failed");
    };
    await page.route("**/graphql", loseResponse);
    try {
      await duration.fill(changedMinutes);
      await page.getByRole("button", { name: "Salvar limites de vídeo" }).click();
      await expect.poll(() => committed).toBe(true);
      await expect(page.getByRole("status")).toContainText(`Versão ${priorVersion} carregada`);
      await expect(page.locator(".admin-form [role=alert]")).toBeVisible();
      await page.unroute("**/graphql", loseResponse);
      await page.reload();
      await authenticateAdminVideoPolicy(page);
      await expect(page.getByRole("status")).toContainText(`Versão ${priorVersion + 1} carregada`);
      await expect(duration).toHaveValue(changedMinutes);
    } finally {
      await page.unroute("**/graphql", loseResponse);
      if (await page.getByRole("button", { name: "Entrar com conta administrativa" }).isVisible()) await authenticateAdminVideoPolicy(page);
      await expect(duration).toBeVisible();
      await duration.fill(previous.duration);
      await originDays.fill(previous.originDays);
      await currentDays.fill(previous.currentDays);
      const beforeRestore = await page.getByRole("status").textContent();
      await page.getByRole("button", { name: "Salvar limites de vídeo" }).click();
      await expect.poll(() => page.getByRole("status").textContent()).not.toBe(beforeRestore);
      await expect(page.getByRole("status")).toContainText(/Versão \d+ carregada/);
      await expect(duration).toHaveValue(previous.duration);
    }
  });

  test("E2E-029 recovers a received video after the final upload response is lost", async ({ page, request }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "The browser media fixture is validated in Chromium");
    test.setTimeout(120_000);
    let expectedNetworkFailures = 0;
    const assertRuntimeClean = installRuntimeGuards(page, (failure) => {
      if (failure !== "console: Failed to load resource: net::ERR_FAILED") return false;
      expectedNetworkFailures += 1;
      return true;
    });
    await installCanvasCamera(page);
    await authenticateVideoCapture(page, request);

    await page.getByRole("button", { name: "Começar gravação" }).click();
    await expect(page.getByText(/Gravando 00:/)).toBeVisible();
    await page.waitForTimeout(1200);
    await page.getByRole("button", { name: "Parar e revisar" }).click();
    await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();

    let completionResponsesDropped = 0;
    let createUploadRequests = 0;
    page.on("request", (outgoing) => {
      if (outgoing.url().endsWith("/graphql") && String(outgoing.postDataJSON()?.query ?? "").includes("CreateMediaUpload")) createUploadRequests += 1;
    });
    await page.route("**/graphql", async (route) => {
      const query = String(route.request().postDataJSON()?.query ?? "");
      if (!query.includes("CompleteMediaUpload")) return route.continue();
      const completed = await route.fetch();
      expect(completed.ok()).toBe(true);
      completionResponsesDropped += 1;
      await route.abort("failed");
    });
    await page.getByRole("button", { name: "Confirmar e enviar" }).click();
    await expect(page.getByText("Vídeo recebido", { exact: true })).toBeVisible({ timeout: 30_000 });
    expect(completionResponsesDropped).toBe(1);
    expect(createUploadRequests).toBe(1);

    const existingMessages = await captureOTPMessageIDs(request);
    await page.reload();
    await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
    let code = "";
    await expect.poll(async () => {
      code = await latestCaptureOTP(request, existingMessages);
      return code;
    }, { message: "VIDEO Capture OTP after reload" }).toMatch(/^\d{6}$/);
    await page.getByLabel("Código de seis dígitos").fill(code);
    await page.getByRole("button", { name: "Confirmar código" }).click();
    await expect(page.getByLabel("Processamento do vídeo e imagens derivadas")).toBeVisible();
    await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
    await page.getByLabel("Análise por inteligência artificial").check();
    await page.getByLabel("Localização quando necessária").check();
    await page.getByRole("button", { name: "Aceitar e continuar" }).click();
    await expect(page.getByRole("button", { name: "Atualizar status" })).toBeVisible();
    await page.getByRole("button", { name: "Atualizar status" }).click();
    await expect(page.getByText("Vídeo recebido", { exact: true })).toBeVisible();
    expect(createUploadRequests).toBe(1);
    await assertRuntimeClean();
    expect(expectedNetworkFailures).toBe(1);
  });
});
