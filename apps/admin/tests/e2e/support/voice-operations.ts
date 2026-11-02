import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, type APIRequestContext, type Page } from "@playwright/test";
import { installFakeGetUserMedia } from "./get-user-media";
import { navigateAdmin } from "./auth";

const workspace = () => resolve(process.cwd(), "../..");
const mailpitURL = process.env.PLAYWRIGHT_MAILPIT_URL ?? "http://localhost:8026";
const apiURL = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";
const wiremockURL = process.env.INSPECTION_E2E_WIREMOCK_URL ?? "http://localhost:18080";
const wiremockFixtures = resolve(workspace(), "deploy/wiremock/litellm/fixtures");

type MailSummary = { ID: string; Subject: string; To: Array<{ Address: string }> };
type MailList = { messages: MailSummary[] };
type MailDetail = { Text: string };

export type VoiceNoteObservation = { noteId?: string; clientNoteId?: string; csrfToken?: string };
export type AudioNoteSnapshot = { id: string; status: string; audioAvailability: string; audioDeletionStatus: string; revisions: Array<{ kind: string; text: string }> };
const captureSessions = new WeakMap<Page, VoiceNoteObservation>();

/** Creates an isolated QA inspection and returns its disposable Capture invite. */
export function seedVoiceCaptureURL(mediaKind: "PHOTO" | "VIDEO" = "PHOTO"): { captureURL: string; inspectionId: string } {
  const output = execFileSync("./scripts/local.sh", ["seed"], {
    cwd: workspace(),
    encoding: "utf8",
    env: { ...process.env, INSPECTION_QA_VOICE: "1", INSPECTION_QA_MEDIA_KIND: mediaKind },
  });
  const invite = output.match(/Capture URL: (\S+)/)?.[1];
  const inspectionId = output.match(/Inspection ID: ([0-9a-f-]{36})/i)?.[1];
  if (!invite || !inspectionId) throw new Error("O seed local não retornou a URL do Capture e o ID da vistoria.");
  return { captureURL: invite, inspectionId };
}
async function existingOTPIDs(request: APIRequestContext): Promise<Set<string>> {
  const response = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!response.ok()) throw new Error(`Mailpit retornou HTTP ${response.status()} ao listar mensagens.`);
  const body = await response.json() as MailList;
  return new Set(body.messages.filter((message) => message.Subject === "Seu código de acesso" && message.To.some((recipient) => recipient.Address === "qa.inspection@example.test")).map((message) => message.ID));
}

async function latestOTP(request: APIRequestContext, previous: Set<string>): Promise<string> {
  const response = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!response.ok()) return "";
  const body = await response.json() as MailList;
  const message = body.messages.find((candidate) => !previous.has(candidate.ID) && candidate.Subject === "Seu código de acesso" && candidate.To.some((recipient) => recipient.Address === "qa.inspection@example.test"));
  if (!message) return "";
  const detail = await request.get(`${mailpitURL}/api/v1/message/${message.ID}`);
  if (!detail.ok()) return "";
  return (((await detail.json()) as MailDetail).Text ?? "").match(/Código:\s*(\d{6})/)?.[1] ?? "";
}

/** Authenticates a fresh seeded Capture invite with the local Mailpit OTP. */
export async function authenticateVoiceCapture(page: Page, request: APIRequestContext, inviteURL: string | ReturnType<typeof seedVoiceCaptureURL> = seedVoiceCaptureURL()): Promise<void> {
  page.setDefaultTimeout(15_000);
  page.setDefaultNavigationTimeout(30_000);
  const session: VoiceNoteObservation = {};
  captureSessions.set(page, session);
  page.on("response", async (response) => {
    if (!response.url().endsWith("/graphql")) return;
    try {
      const body = await response.json() as { data?: { verifyInvitationOtp?: { csrfToken?: string } } };
      session.csrfToken ??= body.data?.verifyInvitationOtp?.csrfToken;
    } catch { /* Ignore other GraphQL response shapes. */ }
  });
  const previous = await existingOTPIDs(request);
  await page.goto(typeof inviteURL === "string" ? inviteURL : inviteURL.captureURL);
  await page.bringToFront();
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  let code = "";
  await expect.poll(async () => { code = await latestOTP(request, previous); return code; }, { timeout: 30_000, message: "Aguardar o OTP da captura no Mailpit local" }).toMatch(/^\d{6}$/);
  await page.getByLabel("Código de seis dígitos").fill(code);
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByLabel(/Processamento das fotos|Processamento do vídeo e imagens derivadas/)).toBeVisible({ timeout: 15_000 });
  await page.getByLabel(/Processamento das fotos|Processamento do vídeo e imagens derivadas/).check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByLabel("Permitir anotações de voz (opcional)").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  const rooms = page.getByRole("navigation", { name: "Lista de ambientes", exact: true });
  await expect(rooms).toBeVisible({ timeout: 15_000 });
  await rooms.getByRole("button", { name: /^Cozinha(?:\s|$)/ }).click();
  await page.getByRole("button", { name: "Abrir câmera", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "Câmera · Cozinha", exact: true })).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: "Fotografar", exact: true }).click();
  await expect(page.getByRole("button", { name: "Usar esta foto", exact: true })).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: "Usar esta foto", exact: true }).click();
  const camera = page.getByRole("dialog", { name: "Câmera · Cozinha", exact: true });
  if (await camera.isVisible().catch(() => false)) await camera.getByRole("button", { name: "Ambientes", exact: true }).click({ timeout: 5_000 });
  await expect(camera).toBeHidden({ timeout: 10_000 });
  await page.getByRole("button", { name: "Abrir câmera", exact: true }).click();
  const nextCamera = page.getByRole("dialog", { name: "Câmera · Sala", exact: true });
  await expect(nextCamera).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: "Fotografar", exact: true }).click();
  await expect(page.getByRole("button", { name: "Usar esta foto", exact: true })).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: "Usar esta foto", exact: true }).click();
  if (await nextCamera.isVisible().catch(() => false)) await nextCamera.getByRole("button", { name: "Ambientes", exact: true }).click({ timeout: 5_000 });
  await expect(nextCamera).toBeHidden({ timeout: 10_000 });
  const voiceNotes = page.locator(".capture-voice-photo-notes");
  await expect(voiceNotes).toBeVisible({ timeout: 10_000 });
  await expect(voiceNotes).toHaveAttribute("aria-label", "Anotações de voz · Sala");
  const photoChoices = voiceNotes.getByRole("radiogroup", { name: "Foto da anotação", exact: true });
  await expect(photoChoices).toBeVisible({ timeout: 45_000 });
  await expect(photoChoices.getByRole("radio").first()).toBeChecked();
  await expect(voiceNotes.getByRole("button", { name: "Segure para falar ou pressione Enter ou Espaço", exact: true })).toBeVisible({ timeout: 15_000 });
}

/** Logs into local Admin with bounded waits for each OIDC stage and preserves the requested route. */
export async function loginVoiceOperationsAdmin(page: Page): Promise<void> {
  await page.goto("/voice-policy");
  const signIn = page.getByRole("button", { name: "Entrar com conta administrativa", exact: true });
  if (await signIn.isVisible().catch(() => false)) {
    await signIn.click();
    await page.waitForURL(/localhost:8081\/realms\/inspection\/protocol\/openid-connect\/auth|localhost:3000\/auth\/callback/, { timeout: 20_000 });
    const username = page.locator("#username");
    const needsCredentials = await username.waitFor({ state: "visible", timeout: 12_000 }).then(() => true, () => false);
    if (needsCredentials) {
      await username.fill(process.env.INSPECTION_E2E_USERNAME ?? "admin");
      await page.locator("#password").fill(process.env.INSPECTION_E2E_PASSWORD ?? "admin");
      await page.locator("button[type=submit]").click();
    }
  }
  await expect(page).toHaveURL(/\/voice-policy(?:\?|$)/, { timeout: 30_000 });
  await expect(page.getByRole("heading", { level: 1, name: "Anotações por voz" })).toBeVisible({ timeout: 15_000 });
}

/** Uses an oscillator-backed MediaStream with Chromium's native MediaRecorder. */
export async function prepareRealVoiceRecorder(page: Page): Promise<void> {
  await installFakeGetUserMedia(page, { kind: "canvas", width: 1280, height: 720, highEntropy: true, captureConstraints: true });
  // The helper overrides getUserMedia only; MediaRecorder remains Chromium's native WebM/Opus implementation.
}

/** Records a valid two-second WebM/Opus note and waits until the API accepts it. */
export async function recordRealVoiceNote(page: Page): Promise<VoiceNoteObservation> {
  await page.bringToFront();
  const observation: VoiceNoteObservation = { csrfToken: captureSessions.get(page)?.csrfToken };
  page.on("response", async (response) => {
    if (!response.url().endsWith("/graphql")) return;
    try {
      const body = await response.json() as { data?: { createAudioNoteUpload?: { noteId?: string } } };
      observation.noteId ??= body.data?.createAudioNoteUpload?.noteId;
    } catch { /* Other API responses do not contain note data. */ }
  });
  page.on("request", async (outgoing) => {
    if (!outgoing.url().endsWith("/graphql")) return;
    try {
      const body = outgoing.postDataJSON() as { query?: string; variables?: { input?: { clientNoteId?: string } } } | null;
      if (body?.query?.includes("createAudioNoteUpload")) observation.clientNoteId = body.variables?.input?.clientNoteId;
    } catch { /* Ignore non-JSON requests. */ }
  });

  const voice = page.locator(".capture-voice-photo-notes").getByRole("button", { name: "Segure para falar ou pressione Enter ou Espaço", exact: true });
  await voice.hover();
  await page.mouse.down();
  await expect(page.locator(".capture-voice-photo-notes").getByRole("button", { name: "Encerrar anotação de voz", exact: true })).toBeVisible({ timeout: 10_000 });
  const startedAt = Date.now();
  await expect.poll(() => Date.now() - startedAt, { timeout: 5_000, message: "Gravar dois segundos de áudio WebM/Opus real" }).toBeGreaterThanOrEqual(2_000);
  await page.mouse.up();
  await expect(page.getByText("Anotação recebida e aguardando processamento.", { exact: true })).toBeVisible({ timeout: 20_000 });
  await expect.poll(() => Boolean(observation.noteId && observation.clientNoteId), { timeout: 10_000, message: "Capturar os identificadores da nota aceita pela API" }).toBe(true);
  expect(observation.csrfToken).toBeTruthy();
  return observation;
}

/** Queries the accepted note through the participant's authenticated GraphQL session. */
export async function getAudioNote(page: Page, observation: VoiceNoteObservation): Promise<AudioNoteSnapshot> {
  if (!observation.noteId || !observation.csrfToken) throw new Error("A captura não forneceu uma sessão de verificação da nota.");
  const response = await page.context().request.post(apiURL, {
    headers: { "X-CSRF-Token": observation.csrfToken },
    data: { query: "query VoiceOperationsAudioNote($id: ID!) { audioNote(id: $id) { id status audioAvailability audioDeletionStatus revisions { kind text } } }", variables: { id: observation.noteId } },
  });
  if (!response.ok()) throw new Error(`A consulta autenticada da nota retornou HTTP ${response.status()}.`);
  const body = await response.json() as { data?: { audioNote?: AudioNoteSnapshot }; errors?: Array<{ message?: string }> };
  if (body.errors?.length || !body.data?.audioNote) throw new Error(`A consulta autenticada da nota falhou: ${body.errors?.[0]?.message ?? "nota indisponível"}`);
  return body.data.audioNote;
}

export async function installTransientTranscriptionMapping(request: APIRequestContext): Promise<string> {
  const mapping = JSON.parse(readFileSync(resolve(wiremockFixtures, "transcription-transient-failure.json.example"), "utf8")) as Record<string, unknown>;
  mapping.scenarioName = `voice-operations-${crypto.randomUUID()}`;
  mapping.requiredScenarioState = "Started";
  mapping.newScenarioState = "TransientFailureInjected";
  const response = await request.post(`${wiremockURL}/__admin/mappings`, { data: mapping });
  if (!response.ok()) throw new Error(`WireMock recusou o fixture transitório: HTTP ${response.status()}.`);
  const body = await response.json() as { uuid?: string; id?: string };
  return body.uuid ?? body.id ?? (() => { throw new Error("WireMock não retornou o ID do mapping transitório."); })();
}

export async function installMissingCostTranscriptionMapping(request: APIRequestContext): Promise<string> {
  const mapping = JSON.parse(readFileSync(resolve(wiremockFixtures, "transcription-missing-cost-header.json.example"), "utf8")) as Record<string, unknown>;
  const response = await request.post(`${wiremockURL}/__admin/mappings`, { data: mapping });
  if (!response.ok()) throw new Error(`WireMock recusou o fixture sem header de custo: HTTP ${response.status()}.`);
  const body = await response.json() as { uuid?: string; id?: string };
  return body.uuid ?? body.id ?? (() => { throw new Error("WireMock não retornou o ID do mapping sem custo."); })();
}

export async function removeWiremockMapping(request: APIRequestContext, id: string): Promise<void> {
  const response = await request.delete(`${wiremockURL}/__admin/mappings/${id}`);
  if (!response.ok() && response.status() !== 404) throw new Error(`Não foi possível remover o mapping temporário ${id}: HTTP ${response.status()}.`);
}

type VoicePolicyValues = { enabled: boolean; tenantNoteLimit: string; tenantMinutes: string; inspectionNoteLimit: string; inspectionMinutes: string };

/** Saves policy through Admin UI and returns the prior values for a finally restore. */
export async function configureVoiceAdmission(page: Page, enabled: boolean, onRead?: (values: VoicePolicyValues) => void): Promise<VoicePolicyValues> {
  if (new URL(page.url()).pathname !== "/voice-policy") await navigateAdmin(page, "Anotações por voz");
  const tenantNotes = page.getByLabel("Máximo de anotações por imobiliária");
  await expect(tenantNotes).toBeVisible({ timeout: 15_000 });
  const toggle = page.getByLabel("Habilitar novas anotações por voz");
  const before: VoicePolicyValues = {
    enabled: await toggle.isChecked(),
    tenantNoteLimit: await tenantNotes.inputValue(),
    tenantMinutes: String(Number(await page.getByLabel("Áudio total máximo por imobiliária (minutos)").inputValue())),
    inspectionNoteLimit: await page.getByLabel("Máximo de anotações por vistoria").inputValue(),
    inspectionMinutes: String(Number(await page.getByLabel("Áudio total máximo por vistoria (minutos)").inputValue())),
  };
  onRead?.(before);
  if (before.enabled !== enabled) await toggle.setChecked(enabled);
  await page.getByRole("button", { name: "Salvar política de voz" }).click();
  await expect(page.getByRole("status")).toContainText(enabled ? "Anotações por voz habilitadas." : "Anotações por voz desativadas.", { timeout: 15_000 });
  await expect(page.locator(".admin-focus-detail")).toContainText(enabled ? "Habilitada" : "Desabilitada");
  return before;
}

export async function restoreVoiceAdmission(page: Page, values: VoicePolicyValues): Promise<void> {
  if (values.enabled) {
    await configureVoiceAdmission(page, true);
  } else {
    if (new URL(page.url()).pathname !== "/voice-policy") await navigateAdmin(page, "Anotações por voz");
    await expect(page.getByLabel("Habilitar novas anotações por voz")).toBeVisible();
    await page.getByLabel("Máximo de anotações por imobiliária").fill(values.tenantNoteLimit);
    await page.getByLabel("Áudio total máximo por imobiliária (minutos)").fill(values.tenantMinutes);
    await page.getByLabel("Máximo de anotações por vistoria").fill(values.inspectionNoteLimit);
    await page.getByLabel("Áudio total máximo por vistoria (minutos)").fill(values.inspectionMinutes);
    await page.getByLabel("Habilitar novas anotações por voz").uncheck();
    await page.getByRole("button", { name: "Salvar política de voz" }).click();
    await expect(page.getByRole("status")).toContainText("Anotações por voz desativadas.");
  }
}

function dockerAWS(args: string[]): string {
  return execFileSync("docker", [
    "run", "--rm", "--network", process.env.INSPECTION_DOCKER_NETWORK ?? "inspection_default",
    "-e", "AWS_ACCESS_KEY_ID=inspection", "-e", "AWS_SECRET_ACCESS_KEY=inspection-local-secret", "-e", "AWS_DEFAULT_REGION=us-east-1",
    "amazon/aws-cli:2.37.9", "s3api", ...args, "--endpoint-url", "http://inspection-minio:9000",
  ], { cwd: workspace(), encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
}

/** Denies deletion only for this note's opaque audio prefix, preserving any previous bucket policy. */
export function denyAudioDeletionForNote(tenantId: string, clientNoteId: string): { restore: () => void } {
  let previousPolicy: string | undefined;
  try {
    const current = JSON.parse(dockerAWS(["get-bucket-policy", "--bucket", "inspection-private"]).trim()).Policy as string | Record<string, unknown>;
    previousPolicy = typeof current === "string" ? current : JSON.stringify(current);
  } catch (error) {
    const detail = error instanceof Error ? `${error.message} ${String((error as Error & { stderr?: Buffer | string }).stderr ?? "")}` : String(error);
    if (!/NoSuchBucketPolicy|NoSuchPolicy|status code: 404|HTTP 404/i.test(detail)) throw error;
  }
  const policy = {
    Version: "2012-10-17",
    Statement: [{
      Sid: `VoiceOperationsDenyDelete${clientNoteId.replace(/-/g, "")}`,
      Effect: "Deny",
      Principal: "*",
      Action: "s3:DeleteObject",
      Resource: `arn:aws:s3:::inspection-private/tenant/${tenantId}/original/${clientNoteId}/*`,
    }],
  };
  dockerAWS(["put-bucket-policy", "--bucket", "inspection-private", "--policy", JSON.stringify(policy)]);
  return { restore: () => {
    if (previousPolicy) dockerAWS(["put-bucket-policy", "--bucket", "inspection-private", "--policy", previousPolicy]);
    else dockerAWS(["delete-bucket-policy", "--bucket", "inspection-private"]);
  } };
}
