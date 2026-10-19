import { expect, test, type Page } from "@playwright/test";
import { installFakeGetUserMedia } from "./support/get-user-media";
import { installRuntimeGuards } from "./support/runtime";

test.use({ serviceWorkers: "block" });

async function openCameraOnlyVideoInvite(page: Page, rooms: string[] = []): Promise<void> {
  const referenceItems = rooms.map((environment, index) => ({
    mediaId: `origin-media-${index}`, evidenceId: `frame-${index}`, sourceMediaId: `origin-media-${index}`,
    timestampMs: index * 1000, environment, component: "", view: "geral", ambiguous: false, gap: false,
    requirementKey: `origin:${index}`, description: environment, availability: "AVAILABLE", imageUrl: null, imageUrlExpiresAt: null
  }));
  await page.route("**/graphql", async (route) => {
    const { query = "" } = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: { responsibilityId: "recorder-failure", recaptureRequestId: null, status: "OPEN", confirmationOnly: false, kind: "INSPECTION", mediaKind: "VIDEO", disclosureVersion: "video-v1", reference: { comparisonMode: rooms.length ? "FIXED_ORIGIN" : "CHECKLIST_ONLY" }, referenceItems, policy: { allowGallery: false, maxDurationSeconds: 900 }, requirements: [{ key: "video:main", section: "main", label: "Vídeo principal", instructions: "Grave o imóvel", required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "CAMERA_ONLY", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: false }], answers: [] } });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    if (query.includes("beginVideoCapture")) return respond({ beginVideoCapture: { attempt: { id: "recorder-failure-attempt", kind: "CURRENT", acquisitionSource: "CAMERA", maxDurationSeconds: 900, maxBytes: 1073741824, policyVersion: 1, admittedAt: "2026-10-15T00:00:00Z", expiresAt: "2099-01-01T00:00:00Z", status: "ACTIVE" }, userErrors: [] } });
    throw new Error(`Unexpected operation: ${query.slice(0, 100)}`);
  });
  await page.goto("/capture/recorder-failure-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Registrar vistoria em vídeo" })).toBeVisible();
}

async function installSyntheticRecorder(page: Page): Promise<void> {
  await installFakeGetUserMedia(page, { kind: "tracks" });
  await page.addInitScript(() => {
    Object.defineProperty(window, "MediaRecorder", { configurable: true, value: class {
      static isTypeSupported() { return true; }
      mimeType = "video/webm";
      state = "inactive";
      onstop?: () => void;
      ondataavailable?: (event: { data: Blob }) => void;
      chunkCount = 0;
      constructor() { Object.assign(window, { syntheticRecorder: this }); }
      start() { this.state = "recording"; }
      emitChunk(value = "video-data") { this.chunkCount += 1; this.ondataavailable?.({ data: new Blob([value], { type: "video/webm" }) }); }
      stop() { this.emitChunk("final-video-data"); this.state = "inactive"; this.onstop?.(); }
      stopUnexpectedly() { this.state = "inactive"; this.onstop?.(); }
    } });
  });
}

test("IT-107 disables recording when no MediaRecorder MIME type is supported", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(window, "MediaRecorder", { configurable: true, value: class { static isTypeSupported() { return false; } } });
  });
  await openCameraOnlyVideoInvite(page);
  await expect(page.getByText("Este navegador não oferece um formato de gravação compatível", { exact: false })).toBeVisible();
  await expect(page.getByText("Abra o convite no Safari ou Chrome atualizado", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "Começar gravação" })).toBeDisabled();
});

test("IT-110 shows camera permission recovery without a photo fallback", async ({ page }) => {
  await installFakeGetUserMedia(page, { kind: "permission-denied" });
  await openCameraOnlyVideoInvite(page);
  await page.getByRole("button", { name: "Começar gravação" }).click();
  await expect(page.getByText("Câmera bloqueada. Libere o acesso nas configurações do navegador", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: /foto|galeria|escolher vídeo/i })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toHaveCount(0);
});

test("IT-140 keeps camera-only admission invalid after NotReadableError", async ({ page }) => {
  await installFakeGetUserMedia(page, { kind: "not-readable" });
  await openCameraOnlyVideoInvite(page);
  await page.getByRole("button", { name: "Começar gravação" }).click();
  await expect(page.getByText("A câmera está ocupada ou indisponível. Feche outros aplicativos e tente novamente.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /escolher vídeo/i })).toHaveCount(0);
  await expect(page.locator('input[type="file"][accept^="video/"]')).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toHaveCount(0);
});

test("IT-108 refuses an empty finalized recording", async ({ page }) => {
  await installFakeGetUserMedia(page, { kind: "tracks" });
  await page.addInitScript(() => {
    Object.defineProperty(window, "MediaRecorder", { configurable: true, value: class {
      static isTypeSupported() { return true; }
      mimeType = "video/webm";
      state = "inactive";
      onstop?: () => void;
      ondataavailable?: (event: { data: Blob }) => void;
      start() { this.state = "recording"; }
      stop() { this.ondataavailable?.({ data: new Blob() }); this.state = "inactive"; this.onstop?.(); }
    } });
  });
  await openCameraOnlyVideoInvite(page);
  await page.getByRole("button", { name: "Começar gravação" }).click();
  await expect(page.getByText(/Gravando 00:/)).toBeVisible();
  await page.getByRole("button", { name: "Parar e revisar" }).click();
  await expect(page.getByText("A gravação não contém vídeo utilizável", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toHaveCount(0);
  await expect(page.getByText("Gravação incompleta; não há recuperação garantida")).toBeVisible();
});

test("IT-106 stops at the wall-clock limit after irregular MediaRecorder chunks", async ({ page }) => {
  const assertRuntimeClean = installRuntimeGuards(page);
  let uploadMutations = 0;
  await page.route("**/graphql", async (route) => {
    const { query = "" } = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    if (/createMediaUpload|presignMediaParts|completeMediaUpload|submitCapture/.test(query)) uploadMutations += 1;
    await route.fallback();
  });
  await installSyntheticRecorder(page);
  await page.addInitScript(() => {
    const originalNow = Date.now.bind(Date);
    let offset = 0;
    Date.now = () => originalNow() + offset;
    Object.assign(window, { advanceRecorderClock: (milliseconds: number) => { offset += milliseconds; } });
  });
  await openCameraOnlyVideoInvite(page);
  await page.getByRole("button", { name: "Começar gravação" }).click();
  await expect(page.getByText(/Gravando 00:/)).toBeVisible();
  for (const elapsed of [1_237, 18_041, 411_002]) {
    await page.evaluate((milliseconds) => (window as typeof window & { advanceRecorderClock(milliseconds: number): void }).advanceRecorderClock(milliseconds), elapsed);
    await page.evaluate(() => {
      const recorder = (window as typeof window & { syntheticRecorder: { emitChunk(value: string): void; chunkCount: number } }).syntheticRecorder;
      recorder.emitChunk(`irregular-${recorder.chunkCount}`);
    });
  }
  await expect.poll(() => page.evaluate(() => (window as typeof window & { syntheticRecorder: { chunkCount: number } }).syntheticRecorder.chunkCount)).toBe(3);
  await page.evaluate(() => (window as typeof window & { advanceRecorderClock(milliseconds: number): void }).advanceRecorderClock(469_721));
  await expect(page.getByText("O limite desta gravação foi atingido. Revise o vídeo antes de confirmar o envio.")).toBeVisible();
  await expect(page.getByText("15:00 de até 15:00.", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "Parar e revisar" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeVisible();
  expect(uploadMutations).toBe(0);
  await assertRuntimeClean();
});

test("IT-094 stops current-only capture at 15:00 and makes no whole-property coverage claim", async ({ page }) => {
  const assertRuntimeClean = installRuntimeGuards(page);
  await installSyntheticRecorder(page);
  await page.addInitScript(() => {
    const originalNow = Date.now.bind(Date);
    let offset = 0;
    Date.now = () => originalNow() + offset;
    Object.assign(window, { advanceRecorderClock: (milliseconds: number) => { offset += milliseconds; } });
  });
  await openCameraOnlyVideoInvite(page);
  await expect(page.getByText("Sem comparação histórica nesta captura. O resultado descreverá apenas o que for visível no vídeo.", { exact: true })).toBeVisible();
  await expect(page.getByText("O limite de tempo não comprova cobertura de todo o imóvel.", { exact: false })).toBeVisible();
  await page.getByRole("button", { name: "Começar gravação" }).click();
  await expect(page.getByText(/Gravando 00:/)).toBeVisible();
  await page.evaluate(() => (window as typeof window & { advanceRecorderClock(milliseconds: number): void }).advanceRecorderClock(900_001));
  await expect(page.getByText("O limite desta gravação foi atingido. Revise o vídeo antes de confirmar o envio.")).toBeVisible();
  await expect(page.getByText("15:00 de até 15:00.", { exact: false })).toBeVisible();
  await expect(page.getByText(/imóvel completo|cobertura completa|todos os ambientes/i)).toHaveCount(0);
  await assertRuntimeClean();
});

test("IT-116 keeps one continuous preview and timer across twenty guided rooms", async ({ page }) => {
  const assertRuntimeClean = installRuntimeGuards(page);
  await installSyntheticRecorder(page);
  await page.addInitScript(() => {
    const originalNow = Date.now.bind(Date);
    let offset = 0;
    Date.now = () => originalNow() + offset;
    Object.assign(window, { advanceRecorderClock: () => { offset += 900_001; } });
  });
  const rooms = Array.from({ length: 20 }, (_, index) => `Ambiente ${index + 1}`);
  await openCameraOnlyVideoInvite(page, rooms);
  await expect(page.getByRole("heading", { name: "Referência de origem" })).toBeVisible();
  for (const room of rooms) await expect(page.getByRole("listitem").filter({ hasText: room }).first()).toBeVisible();
  await page.getByRole("button", { name: "Começar gravação" }).click();
  await expect(page.getByText(/Gravando 00:/)).toBeVisible();
  await page.evaluate(() => (window as typeof window & { advanceRecorderClock(): void }).advanceRecorderClock());
  await expect(page.getByText("15:00 de até 15:00.", { exact: false })).toBeVisible();
  await expect(page.locator("video")).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toHaveCount(1);
  await assertRuntimeClean();
});

test("IT-112 keeps an interrupted draft incomplete until a new take is finalized and previewed", async ({ page }) => {
  const assertRuntimeClean = installRuntimeGuards(page);
  await installSyntheticRecorder(page);
  await openCameraOnlyVideoInvite(page);
  await page.getByRole("button", { name: "Começar gravação" }).click();
  await expect(page.getByText(/Gravando 00:/)).toBeVisible();
  await page.evaluate(() => (window as typeof window & { syntheticRecorder: { stopUnexpectedly(): void } }).syntheticRecorder.stopUnexpectedly());
  await expect(page.getByText("Gravação incompleta; não há recuperação garantida")).toBeVisible();
  await openCameraOnlyVideoInvite(page);
  await expect(page.getByText("Gravação incompleta; não há recuperação garantida")).toBeVisible();
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toHaveCount(0);
  await page.getByRole("button", { name: "Descartar e gravar novamente" }).click();
  await page.getByRole("button", { name: "Começar gravação" }).click();
  await expect(page.getByText(/Gravando 00:/)).toBeVisible();
  await page.getByRole("button", { name: "Parar e revisar" }).click();
  const preview = page.locator("video");
  await expect(preview).toBeVisible();
  await preview.evaluate((node) => {
    Object.defineProperty(node, "duration", { configurable: true, value: 1 });
    node.dispatchEvent(new Event("loadedmetadata"));
    node.dispatchEvent(new Event("canplay"));
  });
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();
  await assertRuntimeClean();
});

test("IT-121 never claims local save when the final IndexedDB write aborts", async ({ page }) => {
  const assertRuntimeClean = installRuntimeGuards(page);
  await installSyntheticRecorder(page);
  await page.addInitScript(() => {
    const original = IDBObjectStore.prototype.put;
    IDBObjectStore.prototype.put = function (value, key) {
      const request = original.call(this, value, key);
      if (this.name === "videoDrafts" && value?.state === "SAVED") queueMicrotask(() => this.transaction.abort());
      return request;
    };
  });
  await openCameraOnlyVideoInvite(page);
  await page.getByRole("button", { name: "Começar gravação" }).click();
  await expect(page.getByText(/Gravando 00:/)).toBeVisible();
  await page.getByRole("button", { name: "Parar e revisar" }).click();
  await expect(page.getByRole("button", { name: "Começar gravação" })).toBeVisible();
  await expect(page.getByText("Salva localmente; envio pendente")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toHaveCount(0);
  await openCameraOnlyVideoInvite(page);
  await expect(page.getByText("Salvamento falhou")).toBeVisible();
  await expect(page.getByText("Salva localmente; envio pendente")).toHaveCount(0);
  await assertRuntimeClean();
});

test("IT-123 does not claim recovery after the browser deletes the local draft database", async ({ page }) => {
  const assertRuntimeClean = installRuntimeGuards(page);
  await installSyntheticRecorder(page);
  await openCameraOnlyVideoInvite(page);
  await page.getByRole("button", { name: "Começar gravação" }).click();
  await expect(page.getByText(/Gravando 00:/)).toBeVisible();
  await page.getByRole("button", { name: "Parar e revisar" }).click();
  await expect(page.getByText("Salva localmente; envio pendente")).toBeVisible();
  await page.evaluate(async () => {
    await new Promise<void>((resolve, reject) => {
      const request = indexedDB.deleteDatabase("inspection-video-capture-v1");
      request.onsuccess = () => resolve();
      request.onerror = () => reject(request.error);
      request.onblocked = () => reject(new Error("draft database remained open"));
    });
  });
  await openCameraOnlyVideoInvite(page);
  await expect(page.getByRole("heading", { name: "Gravações neste dispositivo" })).toHaveCount(0);
  await expect(page.getByText("Salva localmente; envio pendente")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Retomar envio" })).toHaveCount(0);
  await assertRuntimeClean();
});
