import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { expect, type APIRequestContext, type Page } from "@playwright/test";
import { loginAsLocalAdminConsole as loginAsAdmin, loginAsLocalDashboard as loginAsDashboardAdmin } from "./cross-app-auth";
import { photoFixture } from "./voice-notes";
import { installFakeGetUserMedia } from "./get-user-media";

export type SeededCapture = { url: string; inspectionId: string };
export type MediaKind = "PHOTO" | "VIDEO";

const workspace = resolve(process.cwd(), "../..");
const mailpitURL = process.env.PLAYWRIGHT_MAILPIT_URL ?? "http://localhost:8026";
const wiremockURL = process.env.PLAYWRIGHT_WIREMOCK_URL ?? "http://localhost:18080";
const transientWireMockMappings = new Set<string>();

type MailMessage = { ID: string; Subject: string; To: Array<{ Address: string }> };

/** Creates a fresh local QA inspection with voice policy enabled. */
export function seedVoiceCapture(mediaKind: MediaKind = "PHOTO"): SeededCapture {
  const output = execFileSync("./scripts/local.sh", ["seed"], {
    cwd: workspace,
    encoding: "utf8",
    env: { ...process.env, INSPECTION_QA_VOICE: "1", INSPECTION_QA_MEDIA_KIND: mediaKind },
  });
  const rawURL = output.match(/Capture URL: (\S+)/)?.[1];
  const inspectionId = output.match(/Inspection ID: ([0-9a-f-]+)/i)?.[1];
  if (!rawURL || !inspectionId) throw new Error("O seed local não retornou Capture URL e Inspection ID.");
  const seeded = new URL(rawURL);
  const baseURL = new URL(process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:3003");
  return { url: new URL(`${seeded.pathname}${seeded.search}`, baseURL).toString(), inspectionId };
}

async function existingOTPIDs(request: APIRequestContext): Promise<Set<string>> {
  const response = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!response.ok()) throw new Error("Não foi possível consultar o Mailpit local.");
  const result = await response.json() as { messages: MailMessage[] };
  return new Set(result.messages.filter((message) => message.Subject === "Seu código de acesso" && message.To.some(({ Address }) => Address === "qa.inspection@example.test")).map(({ ID }) => ID));
}

async function nextOTP(request: APIRequestContext, seen: Set<string>): Promise<string> {
  const response = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!response.ok()) return "";
  const result = await response.json() as { messages: MailMessage[] };
  const message = result.messages.find((candidate) => !seen.has(candidate.ID) && candidate.Subject === "Seu código de acesso" && candidate.To.some(({ Address }) => Address === "qa.inspection@example.test"));
  if (!message) return "";
  const detail = await request.get(`${mailpitURL}/api/v1/message/${message.ID}`);
  if (!detail.ok()) return "";
  const body = await detail.json() as { Text?: string };
  return body.Text?.match(/Código:\s*(\d{6})/)?.[1] ?? "";
}

/** Authenticates a real seeded invitation and accepts the separate voice choice. */
export async function authenticateCapture(page: Page, request: APIRequestContext, url: string, mediaKind: MediaKind, voice = true): Promise<void> {
  await installFakeGetUserMedia(page, { kind: "canvas", width: 640, height: 360, captureConstraints: true });
  const seen = await existingOTPIDs(request);
  await page.goto(url);
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  let code = "";
  await expect.poll(async () => { code = await nextOTP(request, seen); return code; }, { timeout: 30_000, message: "O código OTP da captura deve chegar ao Mailpit local." }).toMatch(/^\d{6}$/);
  await page.getByLabel("Código de seis dígitos").fill(code);
  await page.getByRole("button", { name: "Confirmar código" }).click();
  const mediaConsent = mediaKind === "VIDEO" ? "Processamento do vídeo e imagens derivadas" : "Processamento das fotos";
  await expect(page.getByLabel(mediaConsent)).toBeVisible();
  await page.getByLabel(mediaConsent).check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  if (voice) await page.getByLabel("Permitir anotações de voz (opcional)").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  if (mediaKind === "VIDEO") {
    await expect(page.getByRole("heading", { name: "Ambientes do imóvel" })).toBeVisible({ timeout: 20_000 });
    await expect(page.getByRole("button", { name: "Gravar Sala", exact: true })).toBeVisible();
  } else {
    await expect(page.getByRole("region", { name: "Ambientes da vistoria" })).toBeVisible({ timeout: 20_000 });
    await expect(page.getByRole("navigation", { name: "Lista de ambientes" }).getByRole("button", { name: /Cozinha/ })).toBeVisible();
  }
}

/** Selects one seeded room and uploads a valid photo through its environment camera flow. */
export async function saveEnvironmentPhoto(page: Page, room: "Cozinha" | "Sala", seed: string, voiceConsent = true): Promise<void> {
  const rooms = page.getByRole("navigation", { name: "Lista de ambientes" });
  await rooms.getByRole("button", { name: new RegExp(`^${room}\\b`) }).click();
  const galleryButton = page.getByRole("button", { name: "Escolher da galeria", exact: true });
  await expect(galleryButton).toBeVisible();
  await galleryButton.click();
  const photoInput = page.getByLabel("Escolher uma foto da galeria", { exact: true });
  await expect(photoInput).toBeAttached();
  await photoInput.setInputFiles({ name: `${seed}.png`, mimeType: "image/png", buffer: photoFixture(seed) });
  await page.getByRole("button", { name: "Usar esta foto", exact: true }).click();
  const voiceRegion = page.getByRole("region", { name: `Anotações de voz · ${room}`, exact: true });
  if (voiceConsent) {
    await expect(voiceRegion).toBeVisible();
    await expect(voiceRegion.getByRole("button", { name: "Segure para falar ou pressione Enter ou Espaço", exact: true })).toBeEnabled({ timeout: 30_000 });
  } else {
    await expect(page.getByRole("heading", { name: "Anotações de voz (opcional)" })).toBeVisible();
  }
}

/** Opens the voice disclosure and authorizes voice when the initial consent omitted it. */
export async function authorizeVoiceFromDisclosure(page: Page, subject: "foto" | "clipe"): Promise<void> {
  const heading = page.getByRole("heading", { name: "Anotações de voz (opcional)" });
  if (!(await heading.isVisible().catch(() => false))) return;
  await page.getByRole("button", { name: "Ativar anotações de voz" }).click();
  await expect(page.getByText(new RegExp(`gravar uma anotação curta para explicar cada ${subject}`))).toBeVisible();
  await expect(page.getByText(/O áudio temporário é enviado para transcrição e removido/)).toBeVisible();
  await page.getByLabel("Permitir anotações de voz (opcional)").check();
  await page.getByRole("button", { name: "Autorizar anotações de voz" }).click();
  await expect(heading).toHaveCount(0);
}

/** Captures a real audio-only WebM/Opus stream from a synthetic oscillator using native MediaRecorder. */
export async function recordVoiceNote(page: Page, durationMs = 900): Promise<void> {
  const control = page.getByRole("button", { name: "Segure para falar ou pressione Enter ou Espaço", exact: true });
  await expect(control).toBeEnabled();
  await control.hover();
  await page.mouse.down();
  await expect(page.getByRole("button", { name: "Encerrar anotação de voz" })).toBeVisible();
  await page.waitForTimeout(durationMs);
  await page.mouse.up();
}

export async function recordVideoWithVoice(page: Page): Promise<void> {
  const room = page.getByRole("button", { name: "Gravar Sala", exact: true });
  await room.click();
  await page.getByRole("button", { name: "Gravar vídeo", exact: true }).click();
  const stage = page.getByRole("dialog", { name: "Câmera de vídeo · Sala" });
  const activatePreview = stage.getByRole("button", { name: "Ativar prévia" });
  if (await activatePreview.isVisible()) await activatePreview.click();
  const begin = stage.getByRole("button", { name: "Iniciar gravação", exact: true });
  await expect(begin).toBeEnabled();
  await begin.click();
  await expect(stage.getByRole("timer")).toBeVisible();
  await recordVoiceNote(page);
  await expect(page.getByText("Anotação recebida e aguardando processamento.")).toBeVisible({ timeout: 30_000 });
  await expect(stage.getByRole("timer")).toContainText(/^00:0[3-9]/, { timeout: 10_000 });
  await stage.getByRole("button", { name: "Parar e revisar" }).click();
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();
  await page.getByRole("button", { name: "Confirmar e enviar" }).click();
  await expect(page.getByText("Vídeo recebido", { exact: true })).toBeVisible({ timeout: 60_000 });
}

/** Authenticates Dashboard with the configured local Keycloak account and opens one inspection. */
export async function openDashboardInspection(page: Page, inspectionId: string): Promise<void> {
  const returnTo = `/inspections?inspectionId=${encodeURIComponent(inspectionId)}`;
  await loginAsDashboardAdmin(page, `http://localhost:3002${returnTo}`);
  await expect(page).toHaveURL(new RegExp(`inspectionId=${encodeURIComponent(inspectionId)}`));
  await expect(page.getByRole("heading", { name: "Anotações de voz" })).toBeVisible({ timeout: 30_000 });
}

/** Logs in to the Admin voice-policy route using local Keycloak. */
export async function openAdminVoicePolicy(page: Page): Promise<void> {
  await loginAsAdmin(page, "/voice-policy");
  const policyHeading = page.getByRole("heading", { name: "Disponibilidade e limites" });
  await expect(policyHeading).toBeVisible({ timeout: 30_000 });
}

/** Adds the checked-in transient failure fixture to WireMock; callers must always remove its mapping. */
export async function installTransientTranscriptionFailure(request: APIRequestContext): Promise<string> {
  const fixturePath = resolve(workspace, "deploy/wiremock/litellm/fixtures/transcription-transient-failure.json.example");
  const fixture = JSON.parse(readFileSync(fixturePath, "utf8")) as Record<string, unknown>;
  const response = await request.post(`${wiremockURL}/__admin/mappings`, { data: { ...fixture, persistent: false, priority: 1 } });
  if (!response.ok()) throw new Error(`WireMock recusou a fixture transitória: ${response.status()}.`);
  const mapping = await response.json() as { id?: string };
  if (!mapping.id) throw new Error("WireMock não retornou o identificador do mapping transitório.");
  transientWireMockMappings.add(mapping.id);
  return mapping.id;
}

export async function removeWireMockMapping(id: string): Promise<void> {
  const response = await fetch(`${wiremockURL}/__admin/mappings/${encodeURIComponent(id)}`, { method: "DELETE" });
  if (!response.ok && response.status !== 404) throw new Error(`Não foi possível remover o mapping WireMock (${response.status}).`);
  transientWireMockMappings.delete(id);
}

/** Cleans transient fixtures even when Playwright has already closed its request fixture after a timeout. */
export async function removeAllTransientWireMockMappings(): Promise<void> {
  for (const id of [...transientWireMockMappings]) await removeWireMockMapping(id);
}

/** Downloads the Dashboard-authorized video proxy response to a temp file and returns ffprobe's streams. */
export async function probeDashboardVideo(page: Page, playbackUrl: string): Promise<{ streams: Array<{ codec_type?: string }> }> {
  const tempDir = mkdtempSync(join(tmpdir(), "inspection-voice-video-"));
  const path = join(tempDir, "capture-video.bin");
  try {
    const base64 = await page.evaluate(async (mediaURL) => {
      const proxy = `/api/media/audio-note-video?${new URLSearchParams({ path: mediaURL })}`;
      const response = await fetch(proxy, { signal: AbortSignal.timeout(30_000) });
      if (!response.ok) throw new Error(`Download de vídeo recusado (${response.status}).`);
      const bytes = new Uint8Array(await response.arrayBuffer());
      let binary = "";
      for (let offset = 0; offset < bytes.length; offset += 0x8000) binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
      return btoa(binary);
    }, playbackUrl);
    writeFileSync(path, Buffer.from(base64, "base64"));
    const output = execFileSync("ffprobe", ["-v", "error", "-show_entries", "stream=codec_type", "-of", "json", path], { encoding: "utf8" });
    return JSON.parse(output) as { streams: Array<{ codec_type?: string }> };
  } finally {
    rmSync(tempDir, { recursive: true, force: true });
  }
}
