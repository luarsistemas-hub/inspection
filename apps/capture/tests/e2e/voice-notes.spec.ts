import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { installFakeGetUserMedia } from "./support/get-user-media";
import { VoiceBackend, authenticate, holdToSpeak, installFakeMediaRecorder, localAudioRecords, savePhoto, stopButton, voiceButton } from "./support/voice-notes";

test.use({ serviceWorkers: "block" });

type ObservedWindow = typeof window & { observedMediaConstraints?: MediaStreamConstraints[] };
const microphoneRequests = (page: Page) => page.evaluate(() => ((window as ObservedWindow).observedMediaConstraints ?? []).filter((constraints) => constraints.audio === true && constraints.video === false).length);

/** Mocks the room-clip journey: OTP, voice policy, one room, an admitted attempt and every audio-note operation. */
async function installVideoJourney(page: Page, noteRequests: Array<Record<string, unknown>>, accepts: Array<Record<string, unknown>> = []): Promise<void> {
  await installFakeGetUserMedia(page, { kind: "canvas", width: 640, height: 480, captureConstraints: true });
  await installFakeMediaRecorder(page);

  await page.route("**/graphql", async (route) => {
    const body = JSON.parse(route.request().postData() ?? "{}") as { query?: string; variables?: { input?: Record<string, unknown> } };
    const query = body.query ?? "";
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("CaptureVoicePolicy")) return respond({ voicePolicy: { enabled: true, tenantNoteLimit: 100, tenantAudioLimitMs: 600000, inspectionNoteLimit: 20, inspectionAudioLimitMs: 120000, version: 1 } });
    if (query.includes("externalCapture")) return respond({ externalCapture: {
      responsibilityId: "voice-e2e-responsibility", recaptureRequestId: null, status: "OPEN", confirmationOnly: false,
      kind: "INSPECTION", mediaKind: "VIDEO", disclosureVersion: "video-disclosure-v1", captureLayout: "ENVIRONMENTS",
      reference: { comparisonMode: "CHECKLIST_ONLY", mediaKind: "VIDEO" }, referenceItems: [], policy: { allowGallery: false, maxDurationSeconds: 30 },
      requirements: [{ key: "env:living", section: "ambientes", label: "Sala", environmentName: "Sala", instructions: "Sala", required: true, minimumMedia: 1, maximumMedia: 3, descriptionRequired: false, captureSourcePolicy: "CAMERA_ONLY", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: false }],
      environments: [{ requirementKey: "env:living", identityId: "environment-1", typeCode: "LIVING_ROOM", name: "Sala", expected: true, addedDuringCapture: false, mediaCount: 0, status: "OPEN" }], answers: []
    } });
    if (query.includes("acceptProcessing")) { accepts.push(body.variables?.input ?? {}); return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } }); }
    if (query.includes("beginVideoCapture")) return respond({ beginVideoCapture: { attempt: { id: "video-attempt-1", kind: "CURRENT", acquisitionSource: "CAMERA", maxDurationSeconds: 10, maxBytes: 10_485_760, policyVersion: 1, admittedAt: "2026-10-01T00:00:00Z", expiresAt: "2099-01-01T00:00:00Z", status: "ACTIVE" }, userErrors: [] } });
    if (query.includes("createAudioNoteUpload")) {
      if (body.variables?.input) noteRequests.push(body.variables.input);
      return respond({ createAudioNoteUpload: { noteId: "audio-note-1", upload: { url: "/upload/audio", method: "PUT", headers: { "x-upload-proof": "proof-1" }, expiresAt: "2099-01-01T00:00:00Z" }, userErrors: [] } });
    }
    if (query.includes("completeAudioNoteUpload")) return respond({ completeAudioNoteUpload: { note: { id: "audio-note-1", status: "QUEUED", version: 1, audioDeletionStatus: "PENDING" }, userErrors: [] } });
    if (query.includes("audioNote")) return respond({ audioNote: { id: "audio-note-1", environmentId: "environment-1", requirementKey: "env:living", targetKind: "VIDEO_INTERVAL", photoMediaId: null, videoAttemptId: "video-attempt-1", startMs: 0, endMs: 1000, durationMs: 1000, status: "COMPLETED", anchorStatus: "PENDING", highlighted: false, version: 1, currentRevisionId: "revision-1", audioAvailability: "DELETED", audioDeletionStatus: "DELETED", revisions: [{ id: "revision-1", revisionNumber: 1, text: "cinco pratos", kind: "MODEL", createdAt: "2026-10-01T00:00:00Z" }] } });
    throw new Error(`Operação inesperada: ${query.slice(0, 100)}`);
  });
  await page.route("**/upload/audio", (route) => route.fulfill({ status: 200 }));
}

test("E2E-003: autoriza voz separadamente, grava nota no clipe e retoma o status após recebimento", async ({ page }) => {
  const noteRequests: Array<Record<string, unknown>> = [];
  await installVideoJourney(page, noteRequests);

  await page.goto("/capture/voice-e2e-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByLabel("Permitir anotações de voz (opcional)")).toBeVisible();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByLabel("Permitir anotações de voz (opcional)").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Gravar Sala", exact: true }).click();
  await page.getByRole("button", { name: "Gravar vídeo", exact: true }).click();
  const stage = page.getByRole("dialog", { name: "Câmera de vídeo · Sala" });
  const activatePreview = stage.getByRole("button", { name: "Ativar prévia" });
  if (await activatePreview.isVisible()) await activatePreview.click();
  await stage.getByRole("button", { name: "Iniciar gravação" }).click();

  const voice = page.getByRole("button", { name: "Segure para falar ou pressione Enter ou Espaço", exact: true });
  await expect(voice).toBeEnabled();
  await voice.hover();
  await page.mouse.down();
  await expect(page.getByRole("button", { name: "Encerrar anotação de voz" })).toBeVisible();
  await page.waitForTimeout(750);
  await page.mouse.up();
  await expect(page.getByText("Anotação recebida e aguardando processamento.")).toBeVisible();
  // The clip keeps recording while the note is saved and sent.
  await expect(stage.getByRole("timer")).toBeVisible();
  await stage.getByRole("button", { name: "Parar e revisar" }).click();
  // After the clip, the note is listed under its own clip with an approximate interval.
  await expect(page.getByRole("heading", { name: "Clipe 1" })).toBeVisible();
  await expect(page.getByText(/Vídeo · \d+–\d+ s \(aproximado\)/)).toBeVisible();
  await page.getByText("Revisar anotação").click();
  await expect(page.getByText("Texto original: cinco pratos")).toBeVisible();
  await expect.poll(() => noteRequests.length).toBe(1);
  expect(noteRequests[0]).toMatchObject({ videoAttemptId: "video-attempt-1", requirementKey: "env:living", consentVersion: "voice-v1", disclosureVersion: "video-disclosure-v1", timeBasis: "CAPTURE_CLOCK" });
  const mediaConstraints = await page.evaluate(() => (window as typeof window & { observedMediaConstraints?: MediaStreamConstraints[] }).observedMediaConstraints ?? []);
  expect(mediaConstraints).toContainEqual(expect.objectContaining({ audio: false, video: expect.anything() }));
  expect(mediaConstraints).toContainEqual({ audio: true, video: false });
});

test("IT-017 / IT-018 (vídeo): recusa a voz, não vê controle durante o clipe e, ao ativar no ambiente, só grava após um novo gesto", async ({ page }) => {
  const noteRequests: Array<Record<string, unknown>> = [];
  const accepts: Array<Record<string, unknown>> = [];
  await installVideoJourney(page, noteRequests, accepts);
  await page.goto("/capture/voice-e2e-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  expect(accepts[0]).toMatchObject({ voiceProcessing: false, voiceConsentVersion: null });
  await page.getByRole("button", { name: "Gravar Sala", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Anotações de voz (opcional)" })).toBeVisible();
  await page.getByRole("button", { name: "Ativar anotações de voz" }).click();
  await page.getByLabel("Permitir anotações de voz (opcional)").check();
  await page.getByRole("button", { name: "Autorizar anotações de voz" }).click();
  await expect(page.getByRole("heading", { name: "Anotações de voz (opcional)" })).toHaveCount(0);
  expect(accepts[1]).toMatchObject({ voiceProcessing: true, voiceConsentVersion: "voice-v1" });
  expect(await microphoneRequests(page)).toBe(0);
  await page.getByRole("button", { name: "Gravar vídeo", exact: true }).click();
  const stage = page.getByRole("dialog", { name: "Câmera de vídeo · Sala" });
  const activatePreview = stage.getByRole("button", { name: "Ativar prévia" });
  if (await activatePreview.isVisible()) await activatePreview.click();
  await stage.getByRole("button", { name: "Iniciar gravação" }).click();
  await expect(voiceButton(page)).toBeEnabled();
  expect(await microphoneRequests(page)).toBe(0);
  await holdToSpeak(page);
  await expect(page.getByText("Anotação recebida e aguardando processamento.")).toBeVisible();
  expect(noteRequests[0]).toMatchObject({ videoAttemptId: "video-attempt-1", consentVersion: "voice-v1", timeBasis: "CAPTURE_CLOCK" });
});

test("IT-017 (vídeo): sem autorização de voz o clipe é gravado sem nenhum controle de voz sobre a câmera", async ({ page }) => {
  const noteRequests: Array<Record<string, unknown>> = [];
  await installVideoJourney(page, noteRequests);
  await page.goto("/capture/voice-e2e-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Gravar Sala", exact: true }).click();
  await page.getByRole("button", { name: "Gravar vídeo", exact: true }).click();
  const stage = page.getByRole("dialog", { name: "Câmera de vídeo · Sala" });
  const activatePreview = stage.getByRole("button", { name: "Ativar prévia" });
  if (await activatePreview.isVisible()) await activatePreview.click();
  await stage.getByRole("button", { name: "Iniciar gravação" }).click();
  await expect(stage.getByRole("timer")).toBeVisible();
  await expect(voiceButton(page)).toHaveCount(0);
  await expect(stage.getByRole("button", { name: "Parar e revisar" })).toBeVisible();
  expect(await microphoneRequests(page)).toBe(0);
  expect(noteRequests).toHaveLength(0);
});

test("política de voz lenta não bloqueia o consentimento e mantém a voz desativada", async ({ page }) => {
  await page.route("**/graphql", async (route) => {
    const body = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const query = body.query ?? "";
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("CaptureVoicePolicy")) return; // never answered: simulates a stalled optional lookup
    if (query.includes("externalCapture")) return respond({ externalCapture: {
      responsibilityId: "voice-slow-responsibility", recaptureRequestId: null, status: "OPEN", confirmationOnly: false,
      kind: "INSPECTION", mediaKind: "VIDEO", disclosureVersion: "video-disclosure-v1", captureLayout: "ENVIRONMENTS",
      reference: { comparisonMode: "CHECKLIST_ONLY", mediaKind: "VIDEO" }, referenceItems: [], policy: { allowGallery: false, maxDurationSeconds: 10 },
      requirements: [{ key: "env:living", section: "ambientes", label: "Sala", environmentName: "Sala", instructions: "Sala", required: true, minimumMedia: 1, maximumMedia: 3, descriptionRequired: false, captureSourcePolicy: "CAMERA_ONLY", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: false }],
      environments: [{ requirementKey: "env:living", identityId: "environment-1", typeCode: "LIVING_ROOM", name: "Sala", expected: true, addedDuringCapture: false, mediaCount: 0, status: "OPEN" }], answers: []
    } });
    throw new Error(`Operação inesperada: ${query.slice(0, 100)}`);
  });
  await page.goto("/capture/voice-slow-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByLabel("Processamento do vídeo e imagens derivadas")).toBeVisible({ timeout: 15_000 });
  await expect(page.getByLabel("Permitir anotações de voz (opcional)")).toHaveCount(0);
});



async function openPhotoCapture(page: Page, options: { voice: boolean; microphone?: "available" | "denied" } = { voice: true }): Promise<VoiceBackend> {
  const backend = new VoiceBackend();
  await installFakeGetUserMedia(page, options.microphone === "denied" ? { kind: "permission-denied" } : { kind: "canvas", width: 640, height: 480, captureConstraints: true });
  await installFakeMediaRecorder(page);
  await backend.install(page);
  await authenticate(page, { voice: options.voice });
  return backend;
}

/** After a reload the session lives only in memory, so the OTP and the authorizations are asked again. */
async function reauthenticate(page: Page, voice: boolean, beforeAccept?: () => Promise<void>) {
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel(/Processamento das fotos/).check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  if (voice) await page.getByLabel("Permitir anotações de voz (opcional)").check();
  await beforeAccept?.();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
}

const noteRows = (page: Page) => page.locator("[data-note-id]");
const received = (page: Page) => page.getByText("Anotação recebida e aguardando processamento.");

test("E2E-002 / IT-017 / IT-018 / IT-019 / IT-021 / IT-025 / IT-026 / IT-027: recusa a voz, envia a foto e só grava após ativar e pressionar de novo", async ({ page }) => {
  const backend = await openPhotoCapture(page, { voice: false });
  expect(backend.calls.accept[0]).toMatchObject({ voiceProcessing: false, voiceConsentVersion: null });
  await savePhoto(page, "Sala", "sala-um", 1);
  // Declined voice leaves photo capture intact and offers no recording control.
  await expect(voiceButton(page)).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Anotações de voz (opcional)" })).toBeVisible();
  await page.getByRole("button", { name: "Ativar anotações de voz" }).click();
  await expect(page.getByText(/O microfone só será solicitado quando você iniciar uma anotação/)).toBeVisible();
  const authorize = page.getByRole("button", { name: "Autorizar anotações de voz" });
  await expect(authorize).toBeDisabled();
  await page.getByLabel("Permitir anotações de voz (opcional)").check();
  await authorize.click();
  await expect(voiceButton(page)).toBeVisible();
  expect(backend.calls.accept).toHaveLength(2);
  expect(backend.calls.accept[1]).toMatchObject({ voiceProcessing: true, voiceConsentVersion: "voice-v1", disclosureVersion: "pt-BR-1" });
  // Enabling the option never opens the microphone; only a new press does.
  expect(await microphoneRequests(page)).toBe(0);
  expect(backend.calls.create).toHaveLength(0);
  await holdToSpeak(page);
  await expect(received(page)).toBeVisible();
  expect(await microphoneRequests(page)).toBe(1);
  expect(backend.calls.create).toHaveLength(1);
  expect(backend.calls.create[0]).toMatchObject({ photoMediaId: "photo-1", requirementKey: "env:living", consentVersion: "voice-v1", videoAttemptId: null, startMs: null, endMs: null });
  // The photo that was saved before voice existed still submits normally.
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await expect(page.getByRole("region", { name: "Resumo das anotações de voz" })).toBeVisible();
  await page.getByRole("button", { name: "Enviar vistoria completa" }).click();
  await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
  expect(await localAudioRecords(page)).toHaveLength(0);
});

test("E2E-004 / IT-044 / IT-045 / IT-046 / IT-056: duas notas sobre uma foto sobrevivem ao recarregamento e não mostram tempo de vídeo", async ({ page }) => {
  const backend = await openPhotoCapture(page);
  await savePhoto(page, "Sala", "sala-foto-um", 1);
  await savePhoto(page, "Sala", "sala-foto-dois", 2);
  // Notes belong to the photo chosen when they were recorded, and the choice is fixed while recording.
  await page.getByRole("radio", { name: /Foto 1 de 2/ }).check();
  await voiceButton(page).hover();
  await page.mouse.down();
  await expect(stopButton(page)).toBeVisible();
  await expect(page.getByRole("radio", { name: /Foto 2 de 2/ })).toBeDisabled();
  await page.waitForTimeout(700);
  await page.mouse.up();
  await expect(received(page)).toBeVisible();
  await holdToSpeak(page);
  await expect(page.getByRole("list", { name: "Anotações de Foto 1" }).getByRole("listitem")).toHaveCount(2);
  expect(backend.calls.create.map((call) => call.photoMediaId)).toEqual(["photo-1", "photo-1"]);
  await page.reload();
  await reauthenticate(page, true);
  await page.getByRole("button", { name: /Sala/ }).first().click();
  const first = page.getByRole("list", { name: "Anotações de Foto 1" });
  await expect(first.getByRole("listitem")).toHaveCount(2);
  await expect(first.getByText("Anotação 1")).toBeVisible();
  await expect(first.getByText("Anotação 2")).toBeVisible();
  await expect(page.getByText(/aproximado|Vídeo ·/)).toHaveCount(0);
  await expect(page.getByRole("list", { name: "Anotações de Foto 2" })).toHaveCount(0);
  expect(backend.calls.create).toHaveLength(2);
});

test("E2E-005 / IT-057 / IT-058 / IT-060 / IT-064 / IT-066 / IT-067: teclado inicia e encerra com Espaço e Enter, ignora repetição e mostra a nota uma só vez", async ({ page }) => {
  const backend = await openPhotoCapture(page);
  await savePhoto(page, "Sala", "sala-teclado", 1);
  await voiceButton(page).focus();
  // Key repeat of a held Space is one activation, never start-then-stop.
  await page.keyboard.down("Space");
  await page.keyboard.down("Space");
  await page.keyboard.down("Space");
  await page.keyboard.up("Space");
  await expect(stopButton(page)).toBeVisible();
  await expect(page.getByText(/Pressione Enter ou Espaço novamente para encerrar/)).toBeVisible();
  await page.keyboard.press("a");
  await page.waitForTimeout(800);
  await expect(stopButton(page)).toBeVisible();
  await page.keyboard.press("Enter");
  await expect(received(page)).toBeVisible();
  await expect(voiceButton(page)).toBeFocused();
  await expect(page.getByRole("list", { name: "Anotações de Foto 1" }).getByRole("listitem")).toHaveCount(1);
  expect(backend.calls.create).toHaveLength(1);
  expect(backend.calls.complete).toHaveLength(1);
  expect(await microphoneRequests(page)).toBe(1);
  // Assistive-technology activation (a click with no pointer) toggles the same way.
  await page.waitForTimeout(500);
  await voiceButton(page).dispatchEvent("click");
  await expect(stopButton(page)).toBeVisible();
  await page.waitForTimeout(700);
  await stopButton(page).dispatchEvent("click");
  await expect(page.getByRole("list", { name: "Anotações de Foto 1" }).getByRole("listitem")).toHaveCount(2);
});

/** Application-level connectivity: the API is unreachable and the page sees the browser go offline or online. WebKit's network emulation would also break local Blob reads, which a real offline device does not. */
async function setConnectivity(page: Page, backend: VoiceBackend, online: boolean) {
  backend.offline = !online;
  await page.evaluate((isOnline) => {
    Object.defineProperty(navigator, "onLine", { configurable: true, get: () => isOnline });
    window.dispatchEvent(new Event(isOnline ? "online" : "offline"));
  }, online);
}

test("E2E-006 / IT-070 / IT-071 / IT-079 / IT-080: grava sem conexão, recarrega, reconecta e envia uma só vez removendo o áudio local", async ({ page }) => {
  const backend = await openPhotoCapture(page);
  await savePhoto(page, "Sala", "sala-offline", 1);
  await setConnectivity(page, backend, false);
  await expect.poll(() => page.evaluate(() => navigator.onLine)).toBe(false);
  await holdToSpeak(page);
  await expect(page.getByText("Anotação salva neste dispositivo; o envio está pendente até a conexão voltar.")).toBeVisible();
  await expect(page.getByText("Salva neste dispositivo; envio pendente até a conexão voltar")).toBeVisible();
  expect(backend.calls.create).toHaveLength(0);
  const pending = await localAudioRecords(page);
  expect(pending).toHaveLength(1);
  expect(pending[0]).toMatchObject({ state: "PENDING_UPLOAD", hasBytes: true, target: { kind: "PHOTO", mediaId: "photo-1" } });
  expect(pending[0].byteLength).toBeGreaterThan(0);
  expect(backend.calls.create).toHaveLength(0);

  // The new document starts offline too; neither the OTP attempt nor draft recovery can reach the backend.
  await page.addInitScript(() => Object.defineProperty(navigator, "onLine", { configurable: true, get: () => false }));
  await page.reload();
  await expect.poll(() => page.evaluate(() => navigator.onLine)).toBe(false);
  await expect(page.getByText(/Não foi possível conectar\. Verifique sua internet/)).toBeVisible();
  const restored = await localAudioRecords(page);
  expect(restored).toHaveLength(1);
  expect(restored[0]).toMatchObject({ id: pending[0].id, state: "PENDING_UPLOAD", hasBytes: true, target: pending[0].target });
  expect(restored[0].byteLength).toBe(pending[0].byteLength);
  expect(backend.calls.create).toHaveLength(0);
  expect(backend.calls.upload).toBe(0);

  await page.getByRole("button", { name: "Solicitar outro código" }).click();
  await expect(page.getByText(/Não foi possível conectar\. Verifique sua internet e tente solicitar o código novamente/)).toBeVisible();
  await expect(page.getByText(/Failed to fetch|NETWORK_ERROR|TypeError/)).toHaveCount(0);
  expect(backend.calls.create).toHaveLength(0);
  expect(backend.calls.upload).toBe(0);

  await setConnectivity(page, backend, true);
  // Ticking the voice box is not an accepted authorization: nothing is sent until the server has it.
  await reauthenticate(page, true, async () => { await page.waitForTimeout(800); expect(backend.calls.create).toHaveLength(0); });
  await expect.poll(() => backend.calls.complete.length).toBe(1);
  await page.getByRole("button", { name: /Sala/ }).first().click();
  await expect(page.getByText("Texto pronto para revisão")).toBeVisible();
  const settled = await localAudioRecords(page);
  expect(settled).toHaveLength(1);
  expect(settled[0]).toMatchObject({ id: pending[0].id, state: "RECEIVED", hasBytes: false, byteLength: 0, noteId: "audio-note-1", target: pending[0].target });
  expect(backend.calls.create).toHaveLength(1);
  expect(backend.calls.upload).toBe(1);
  expect(backend.calls.create[0]).toMatchObject({ clientNoteId: pending[0].id, photoMediaId: pending[0].target.mediaId });
  expect(backend.calls.complete).toHaveLength(1);
});

test("E2E-007 / IT-084 / IT-085 / IT-086 / IT-088 / IT-090 / IT-094: corrige uma nota, descarta outra e só a corrigida segue elegível", async ({ page }) => {
  const backend = await openPhotoCapture(page);
  await savePhoto(page, "Sala", "sala-revisao", 1);
  await holdToSpeak(page);
  await expect(page.getByRole("list", { name: "Anotações de Foto 1" }).getByRole("listitem")).toHaveCount(1);
  await holdToSpeak(page);
  await expect(noteRows(page)).toHaveCount(2);
  const [first, second] = [noteRows(page).nth(0), noteRows(page).nth(1)];
  await first.getByText("Revisar anotação").click();
  await expect(first.getByText("Texto original: cinco pratos")).toBeVisible();
  const editor = first.getByLabel("Corrigir texto");
  await expect(first.getByText("12 de 12.000 caracteres")).toBeVisible();
  // An empty correction is rejected locally with a Portuguese message and keeps the last revision.
  await editor.fill("");
  await expect(first.getByText("Digite o texto da anotação antes de salvar.")).toBeVisible();
  await expect(first.getByRole("button", { name: "Salvar correção" })).toBeDisabled();
  await editor.fill("seis pratos");
  await first.getByRole("button", { name: "Salvar correção" }).click();
  await expect(page.getByText("Correção salva sem solicitar outra transcrição.")).toBeVisible();
  await expect(first.getByText("Texto atual: seis pratos")).toBeVisible();
  await expect(first.getByText("Texto original: cinco pratos")).toBeVisible();
  expect(backend.calls.revise).toHaveLength(1);
  expect(backend.calls.revise[0]).toMatchObject({ text: "seis pratos", revisionKind: "HUMAN_EDIT", expectedVersion: 1 });
  // Discarding asks for confirmation first, then excludes the note from future suggestions.
  await second.getByText("Revisar anotação").click();
  await second.getByRole("button", { name: "Descartar anotação" }).click();
  await expect(second.getByText("Descartar esta anotação? Ela deixará de ser usada em sugestões.")).toBeVisible();
  await second.getByRole("button", { name: "Sim, descartar" }).click();
  await expect(second.getByText("Anotação descartada", { exact: true })).toBeVisible();
  await expect(second.getByRole("button", { name: "Destacar anotação" })).toHaveCount(0);
  expect(backend.calls.discard).toHaveLength(1);
  expect(backend.eligibleNotes().map((note) => note.revisions.at(-1)?.text)).toEqual(["seis pratos"]);
  await expect(first.getByRole("button", { name: "Destacar anotação" })).toBeVisible();
});

test("E2E-009 (participante) / IT-113 / IT-117 / IT-122: destaca e remove o destaque sem alterar o texto", async ({ page }) => {
  const backend = await openPhotoCapture(page);
  await savePhoto(page, "Sala", "sala-destaque", 1);
  await holdToSpeak(page);
  await expect(noteRows(page)).toHaveCount(1);
  const row = noteRows(page).first();
  await row.getByText("Revisar anotação").click();
  const highlight = row.getByRole("button", { name: "Destacar anotação" });
  await expect(highlight).toHaveAttribute("aria-pressed", "false");
  await highlight.click();
  await expect(highlight).toHaveAttribute("aria-pressed", "true");
  await expect(row.getByText("Destacada", { exact: true })).toBeVisible();
  await highlight.click();
  await expect(highlight).toHaveAttribute("aria-pressed", "false");
  await expect(row.getByText("Destacada", { exact: true })).toHaveCount(0);
  expect(backend.calls.highlight.map((call) => call.highlighted)).toEqual([true, false]);
  expect(backend.notes.get("audio-note-1")?.revisions.at(-1)?.text).toBe("cinco pratos");
});

test("IT-023 / IT-063: microfone negado mostra explicação em português e mantém a captura visual", async ({ page }) => {
  const backend = await openPhotoCapture(page, { voice: true, microphone: "denied" });
  await savePhoto(page, "Sala", "sala-negada", 1);
  await voiceButton(page).hover();
  await page.mouse.down();
  await page.mouse.up();
  await expect(page.getByText(/O acesso ao microfone foi negado/)).toBeVisible();
  await expect(voiceButton(page)).toBeVisible();
  expect(backend.calls.create).toHaveLength(0);
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await expect(page.getByRole("button", { name: "Enviar vistoria completa" })).toBeEnabled();
});

test("IT-039 / IT-065 / IT-040: aba em segundo plano encerra a nota uma única vez e a mostra salva", async ({ page }) => {
  const backend = await openPhotoCapture(page);
  await savePhoto(page, "Sala", "sala-segundo-plano", 1);
  await voiceButton(page).focus();
  await page.keyboard.press("Enter");
  await expect(stopButton(page)).toBeVisible();
  await page.waitForTimeout(700);
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
    document.dispatchEvent(new Event("visibilitychange"));
    document.dispatchEvent(new Event("visibilitychange"));
    window.dispatchEvent(new Event("pagehide"));
  });
  await expect(page.getByText(/A gravação foi encerrada porque a página saiu de primeiro plano/)).toBeVisible();
  await expect(page.getByRole("list", { name: "Anotações de Foto 1" }).getByRole("listitem")).toHaveCount(1);
  expect(backend.calls.create).toHaveLength(1);
});

test("IT-022 / IT-076: limite de voz atingido mostra explicação, mantém a nota para descarte e bloqueia novas gravações", async ({ page }) => {
  const backend = await openPhotoCapture(page);
  backend.rejectNextCreate = { code: "LIMIT_REACHED", message: "O limite de uso de voz foi atingido." };
  await savePhoto(page, "Sala", "sala-limite", 1);
  await holdToSpeak(page);
  await expect(page.getByText(/O limite de uso de voz foi atingido\. Esta anotação não foi enviada/).first()).toBeVisible();
  await expect(voiceButton(page)).toHaveAttribute("aria-disabled", "true");
  const records = await localAudioRecords(page);
  expect(records[0]).toMatchObject({ state: "UNSENDABLE", hasBytes: true });
  await noteRows(page).first().getByRole("button", { name: "Descartar anotação" }).click();
  await expect(noteRows(page)).toHaveCount(0);
  expect(await localAudioRecords(page)).toHaveLength(0);
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await expect(page.getByRole("button", { name: "Enviar vistoria completa" })).toBeEnabled();
});

test("acessibilidade: a seção de voz tem nomes, estados e contraste sem violações detectáveis, gravando ou não", async ({ page }) => {
  await openPhotoCapture(page);
  await savePhoto(page, "Sala", "sala-axe", 1);
  await holdToSpeak(page);
  await expect(noteRows(page)).toHaveCount(1);
  await noteRows(page).first().getByText("Revisar anotação").click();
  const idle = await new AxeBuilder({ page: page as never }).include(".capture-voice-photo-notes").analyze();
  expect(idle.violations.map((violation) => `${violation.id}: ${violation.nodes.map((node) => node.target.join(" ")).join(" | ")}`)).toEqual([]);
  await voiceButton(page).focus();
  await page.keyboard.press("Enter");
  await expect(stopButton(page)).toBeVisible();
  await page.waitForTimeout(600);
  const recording = await new AxeBuilder({ page: page as never }).include(".capture-voice-photo-notes").analyze();
  expect(recording.violations.map((violation) => `${violation.id}: ${violation.nodes.map((node) => node.target.join(" ")).join(" | ")}`)).toEqual([]);
  await expect(page.locator(".capture-voice-notes__status[role=status]").first()).toHaveAttribute("aria-live", "polite");
  await page.keyboard.press("Enter");
  await expect(received(page)).toBeVisible();
  await page.emulateMedia({ colorScheme: "dark" });
  await page.waitForTimeout(600);
  const dark = await new AxeBuilder({ page: page as never }).include(".capture-voice-photo-notes").analyze();
  expect(dark.violations.map((violation) => `${violation.id}: ${violation.nodes.map((node) => node.target.join(" ")).join(" | ")}`)).toEqual([]);
});
