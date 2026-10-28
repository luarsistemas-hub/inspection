import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { installFakeGetUserMedia } from "./support/get-user-media";
import { startVideoRecording } from "./support/video-capture";

test.use({ serviceWorkers: "block" });

test("IT-088 / IT-099 shows the current-only baseline explanation without requesting reference images", async ({ page }) => {
  const referenceRequests: string[] = [];
  page.on("request", (request) => {
    const url = request.url();
    if (url.includes("/media/video-evidence/") || url.includes("referenceItems")) referenceRequests.push(url);
  });
  await page.route("**/graphql", async (route) => {
    const { query = "" } = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: {
      responsibilityId: "responsibility-current-only", recaptureRequestId: null, status: "OPEN", confirmationOnly: false,
      kind: "INSPECTION", mediaKind: "VIDEO", disclosureVersion: "video-v1",
      reference: { comparisonMode: "CHECKLIST_ONLY", mediaKind: "VIDEO" }, referenceItems: [],
      policy: { allowGallery: false, maxDurationSeconds: 900 },
      requirements: [{ key: "video:main", section: "main", label: "Vídeo principal", instructions: "Grave o imóvel", required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "CAMERA_ONLY", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: false }], answers: []
    } });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    throw new Error(`Unexpected operation: ${query.slice(0, 100)}`);
  });

  await page.goto("/capture/current-only-video-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();

  await expect(page.getByText("Sem comparação histórica nesta captura. O resultado descreverá apenas o que for visível no vídeo.", { exact: true })).toBeVisible();
  await expect(page.locator("img[alt*='referência']")).toHaveCount(0);
  expect(referenceRequests).toEqual([]);
});

test("câmera de vídeo em ambientes mantém a faixa desabilitada durante a gravação", async ({ page }) => {
  await installFakeGetUserMedia(page, { kind: "canvas", width: 640, height: 480 });
  await page.setViewportSize({ width: 320, height: 568 });
  await page.route("**/graphql", async (route) => {
    const { query = "" } = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: {
      responsibilityId: "environment-video-camera", recaptureRequestId: null, status: "OPEN", confirmationOnly: false,
      kind: "INSPECTION", mediaKind: "VIDEO", disclosureVersion: "video-v1", captureLayout: "ENVIRONMENTS",
      reference: { comparisonMode: "CHECKLIST_ONLY", mediaKind: "VIDEO" }, referenceItems: [],
      policy: { allowGallery: false, maxDurationSeconds: 10 },
      requirements: ["Sala", "Cozinha"].map((name, index) => ({ key: `env:${index}`, section: "ambientes", label: name, environmentName: name, instructions: name, required: true, minimumMedia: 1, maximumMedia: 3, descriptionRequired: false, captureSourcePolicy: "CAMERA_ONLY", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: false })),
      environments: [], answers: [],
    } });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    if (query.includes("beginVideoCapture")) return respond({ beginVideoCapture: { attempt: { id: "environment-attempt", kind: "CURRENT", acquisitionSource: "CAMERA", maxDurationSeconds: 10, maxBytes: 10485760, policyVersion: 1, admittedAt: "2026-10-27T00:00:00Z", expiresAt: "2099-01-01T00:00:00Z", status: "ACTIVE" }, userErrors: [] } });
    throw new Error(`Operação inesperada: ${query.slice(0, 80)}`);
  });
  await page.goto("/capture/environment-video-camera");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Abrir câmera" }).click();
  const stage = page.getByRole("dialog", { name: "Câmera de vídeo · Sala" });
  await expect(stage.getByRole("group", { name: "Ambientes da vistoria" }).getByRole("button", { name: /Sala/ })).toBeEnabled();
  const activatePreview = stage.getByRole("button", { name: "Ativar prévia" });
  if (await activatePreview.isVisible()) await activatePreview.click();
  await expect(stage.getByRole("button", { name: "Iniciar gravação" })).toBeEnabled();
  await stage.getByRole("button", { name: "Iniciar gravação" }).click();
  await expect(stage.getByRole("timer")).toContainText("00:00 / 00:10");
  await expect(stage.getByRole("group", { name: "Ambientes da vistoria" }).getByRole("button", { name: /Sala/ })).toBeDisabled();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test("IT-223 shows the requested video item, reason, deadline and origin frame to the participant", async ({ page }) => {
  await page.route("**/graphql", async (route) => {
    const { query = "" } = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: {
      responsibilityId: "responsibility-recapture", recaptureRequestId: "request-1", recaptureDeadlineAt: "2026-10-18T12:00:00Z",
      status: "OPEN", confirmationOnly: false, kind: "RECAPTURE", mediaKind: "VIDEO", disclosureVersion: "video-v1",
      reference: { comparisonMode: "FIXED_ORIGIN", mediaKind: "VIDEO" },
      referenceItems: [{ mediaId: "frame-1", evidenceId: "frame-1", sourceMediaId: "media-1", timestampMs: 1250, environment: "Cozinha", component: "Pia", view: "Frente", ambiguous: false, gap: false, requirementKey: "video:component-1", description: "Cozinha · Pia", reason: "A pia está opaca", availability: "AVAILABLE", imageUrl: "/media/video-evidence/frame-1?responsibilityId=responsibility-recapture", imageUrlExpiresAt: null }],
      policy: { allowGallery: false, maxDurationSeconds: 900 }, requirements: [{ key: "video:supplement", section: "complemento", label: "Vídeo complementar", instructions: "Grave os itens selecionados", required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "CAMERA_ONLY", comparisonTarget: "FIXED_ORIGIN", impossibilityAllowed: false }], answers: []
    } });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    throw new Error(`Unexpected operation: ${query.slice(0, 100)}`);
  });

  await page.goto("/capture/video-recapture-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Registrar complemento em vídeo" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Itens a complementar" })).toBeVisible();
  await expect(page.getByText("Cozinha · Pia")).toBeVisible();
  await expect(page.getByText("Motivo: A pia está opaca")).toBeVisible();
  await expect(page.getByText(/Prazo:.*2026/)).toBeVisible();
  await expect(page.getByRole("img", { name: "Imagem de referência: Cozinha · Pia" })).toHaveAttribute("src", "/media/video-evidence/frame-1?responsibilityId=responsibility-recapture");
});

test("IT-075 authenticated V1 guidance shows O1 kitchen and bathroom stills", async ({ page }) => {
  const referenceItems = [
    { mediaId: "frame-kitchen", evidenceId: "frame-kitchen", sourceMediaId: "origin-o1", timestampMs: 10_000, environment: "Cozinha", component: "Pia", view: "Frente", ambiguous: false, gap: false, requirementKey: "video:main", description: "Cozinha · Pia", availability: "AVAILABLE", imageUrl: "/media/video-evidence/frame-kitchen?responsibilityId=v1", imageUrlExpiresAt: null },
    { mediaId: "frame-bathroom", evidenceId: "frame-bathroom", sourceMediaId: "origin-o1", timestampMs: 30_000, environment: "Banheiro", component: "Lavatório", view: "Frente", ambiguous: false, gap: false, requirementKey: "video:main", description: "Banheiro · Lavatório", availability: "AVAILABLE", imageUrl: "/media/video-evidence/frame-bathroom?responsibilityId=v1", imageUrlExpiresAt: null }
  ];
  await page.route("**/media/video-evidence/**", (route) => route.fulfill({ status: 200, contentType: "image/png", body: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/pRkAAAAASUVORK5CYII=", "base64") }));
  await page.route("**/graphql", async (route) => {
    const { query = "" } = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: {
      responsibilityId: "v1", recaptureRequestId: null, status: "OPEN", confirmationOnly: false, kind: "INSPECTION", mediaKind: "VIDEO", disclosureVersion: "video-v1",
      reference: { comparisonMode: "FIXED_ORIGIN", mediaKind: "VIDEO" }, referenceItems,
      policy: { allowGallery: false, maxDurationSeconds: 900 },
      requirements: [{ key: "video:main", section: "vistoria", label: "Vídeo principal", instructions: "Grave um vídeo contínuo", required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "CAMERA_ONLY", comparisonTarget: "FIXED_ORIGIN", impossibilityAllowed: false }], answers: []
    } });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    throw new Error(`Unexpected operation: ${query.slice(0, 100)}`);
  });
  await page.goto("/capture/v1-video-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Referência de origem" })).toBeVisible();
  await expect(page.getByText("Cozinha · Pia")).toBeVisible();
  await expect(page.getByText("Banheiro · Lavatório")).toBeVisible();
  await expect(page.getByRole("img", { name: "Referência: Cozinha · Pia" })).toBeVisible();
  await expect(page.getByRole("img", { name: "Referência: Banheiro · Lavatório" })).toBeVisible();
});

test("@camera-focus E2E-008 records, previews and submits one continuous video (IT-102, IT-104, IT-105, IT-111, IT-113, IT-114)", async ({ page, browserName }) => {
  let received = false;
  let mediaCreates = 0;
  let recorderAdmissions = 0;
  let partPuts = 0;
  let submitted = 0;

  await installFakeGetUserMedia(page, { kind: "canvas", width: 320, height: 180, captureConstraints: true });
  await page.addInitScript(() => {
    localStorage.setItem("inspection.theme", "dark");
    const nativePlay = HTMLMediaElement.prototype.play;
    HTMLMediaElement.prototype.play = function () {
      if (this.srcObject && !(window as typeof window & { allowCameraPreview?: boolean }).allowCameraPreview) return Promise.reject(new DOMException("Autoplay blocked", "NotAllowedError"));
      return nativePlay.call(this);
    };
  });
  await page.route("**/upload-part", async (route) => { partPuts += 1; await route.fulfill({ status: 200, headers: { etag: `etag-${partPuts}` } }); });
  await page.route("**/graphql", async (route) => {
    const body = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const query = body.query ?? "";
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: { responsibilityId: "responsibility-video", recaptureRequestId: null, status: submitted ? "COMPLETED" : "OPEN", confirmationOnly: false, kind: "INSPECTION", mediaKind: "VIDEO", disclosureVersion: "video-v1", reference: { comparisonMode: "CHECKLIST_ONLY" }, referenceItems: [], policy: { allowGallery: false, maxDurationSeconds: 900 }, requirements: [{ key: "video:main", section: "main", label: "Vídeo principal", instructions: "Grave o imóvel", required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "CAMERA_ONLY", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: false }], answers: [] } });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    if (query.includes("beginVideoCapture")) { recorderAdmissions += 1; return respond({ beginVideoCapture: { attempt: { id: "attempt-1", kind: "CURRENT", acquisitionSource: "CAMERA", maxDurationSeconds: 900, maxBytes: 1073741824, policyVersion: 1, admittedAt: "2026-10-15T00:00:00Z", expiresAt: "2099-01-01T00:00:00Z", status: "ACTIVE" }, userErrors: [] } }); }
    if (query.includes("videoUploadStatus")) return respond({ videoUploadStatus: { mediaId: "media-1", status: received ? "RECEIVED" : "UPLOADING", expiresAt: "2099-01-01T00:00:00Z", receivedParts: [], confirmedParts: [], missingParts: [1] } });
    if (query.includes("createMediaUpload")) { mediaCreates += 1; return respond({ createMediaUpload: { upload: { mediaId: "media-1", uploadId: "upload-1", expiresAt: "2099-01-01T00:00:00Z", partSizeBytes: 5 * 1024 * 1024 }, userErrors: [] } }); }
    if (query.includes("presignMediaParts")) return respond({ presignMediaParts: { parts: [{ partNumber: 1, url: new URL("/upload-part", page.url()).toString(), expiresAt: "2099-01-01T00:00:00Z" }], userErrors: [] } });
    if (query.includes("completeMediaUpload")) { received = true; return respond({ completeMediaUpload: { media: { id: "media-1", status: "RECEIVED" }, userErrors: [] } }); }
    if (query.includes("saveCaptureMetadata")) return respond({ saveCaptureMetadata: { media: { id: "media-1", status: "RECEIVED", replacesMediaId: null }, userErrors: [] } });
    if (query.includes("submitCapture")) { submitted += 1; return respond({ submitCapture: { submission: { id: "submission-1", complete: true, requiresAttention: false, submittedAt: "2026-10-15T00:00:00Z" }, userErrors: [] } }); }
    throw new Error(`Unexpected operation: ${query.slice(0, 100)}`);
  });

  await page.goto("/capture/video-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Registrar vistoria em vídeo" })).toBeVisible();
  await expect(page.getByText("O tempo máximo autorizado é 15:00", { exact: false })).toBeVisible();
  await expect(page.getByText("Filme devagar, mantenha a câmera estável", { exact: false })).toBeVisible();
  await page.getByRole("button", { name: "Abrir câmera" }).click();
  await expect(page.getByRole("button", { name: "Ativar prévia" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Iniciar gravação" })).toBeDisabled();
  await page.evaluate(() => { (window as typeof window & { allowCameraPreview?: boolean }).allowCameraPreview = true; });
  await page.getByRole("button", { name: "Ativar prévia" }).click();
  await expect(page.getByRole("button", { name: "Iniciar gravação" })).toBeEnabled();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect(recorderAdmissions).toBe(0);
  await page.setViewportSize({ width: 320, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const buttonHeights = await page.locator(".camera-stage__controls button").evaluateAll((buttons) => buttons.map((button) => button.getBoundingClientRect().height));
  expect(buttonHeights.length).toBeGreaterThan(0);
  expect(buttonHeights.every((height) => height >= 48)).toBe(true);
  const accessibility = await new AxeBuilder({ page: page as never }).include(".camera-stage").analyze();
  expect(accessibility.violations).toEqual([]);
  await page.setViewportSize({ width: 844, height: 390 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await expect.poll(() => page.locator("video").evaluate((video: HTMLVideoElement) => video.videoWidth)).toBeGreaterThan(0);
  const previewTime = await page.locator("video").evaluate((video: HTMLVideoElement) => video.currentTime);
  await page.waitForTimeout(400);
  await expect.poll(() => page.locator("video").evaluate((video: HTMLVideoElement) => video.currentTime)).toBeGreaterThan(previewTime);
  expect(mediaCreates).toBe(0);
  expect(partPuts).toBe(0);
  await page.getByRole("button", { name: "Fechar câmera" }).click();
  await expect.poll(() => page.evaluate(() => (window as typeof window & { cameraTrackStops?: number }).cameraTrackStops ?? 0)).toBeGreaterThan(0);
  await page.getByRole("button", { name: "Abrir câmera" }).click();
  await expect(page.getByRole("button", { name: "Iniciar gravação" })).toBeEnabled();
  await page.evaluate(() => { const element = document.activeElement; if (element instanceof HTMLElement) element.blur(); });
  for (let index = 0; index < 8 && !(await page.getByRole("button", { name: "Iniciar gravação" }).evaluate((button) => button === document.activeElement)); index += 1) await page.keyboard.press("Tab");
  // WebKit skips buttons when tabbing by default, so only its keyboard activation is checked.
  if (browserName === "webkit") await page.getByRole("button", { name: "Iniciar gravação" }).focus();
  await expect(page.getByRole("button", { name: "Iniciar gravação" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByText(/00:/)).toBeVisible();
  await expect(page.getByRole("timer")).toBeVisible();
  expect(await page.getByRole("timer").getAttribute("aria-live")).toBeNull();
  await expect(page.getByRole("status")).toContainText("Gravando.");
  const recordingTime = await page.locator("video").evaluate((video: HTMLVideoElement) => video.currentTime);
  await page.waitForTimeout(400);
  await expect.poll(() => page.locator("video").evaluate((video: HTMLVideoElement) => video.currentTime)).toBeGreaterThan(recordingTime);
  await page.waitForTimeout(900);
  await page.evaluate(() => { const element = document.activeElement; if (element instanceof HTMLElement) element.blur(); });
  for (let index = 0; index < 8 && !(await page.getByRole("button", { name: "Parar e revisar" }).evaluate((button) => button === document.activeElement)); index += 1) await page.keyboard.press("Tab");
  // WebKit skips buttons when tabbing by default, so only its keyboard activation is checked.
  if (browserName === "webkit") await page.getByRole("button", { name: "Parar e revisar" }).focus();
  await expect(page.getByRole("button", { name: "Parar e revisar" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "Revise a gravação" })).toBeVisible();
  await page.getByRole("button", { name: "Descartar e gravar novamente" }).click();
  await startVideoRecording(page);
  await page.waitForTimeout(1200);
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toHaveCount(0);
  expect(recorderAdmissions).toBe(1);
  expect(await page.evaluate(() => (window as typeof window & { captureConstraints?: MediaStreamConstraints }).captureConstraints?.audio)).toBe(false);
  await page.waitForTimeout(1200);
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect(page.getByRole("heading", { name: "Revise a gravação" })).toBeVisible();
  expect(mediaCreates).toBe(0);
  expect(partPuts).toBe(0);
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();
  await page.getByRole("button", { name: "Descartar e gravar novamente" }).click();
  await startVideoRecording(page);
  await page.waitForTimeout(2200);
  // WebKit does not stop MediaRecorder when a canvas track is stopped, so signal the ended event its adapter observes.
  await page.locator("video").evaluate((element) => {
    const track = ((element as HTMLVideoElement).srcObject as MediaStream | null)?.getVideoTracks()[0];
    if (navigator.userAgent.includes("AppleWebKit") && !navigator.userAgent.includes("Chrome")) track?.dispatchEvent(new Event("ended"));
    else track?.stop();
  });
  await expect(page.getByRole("heading", { name: "Revise a gravação" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();
  expect(mediaCreates).toBe(0);
  expect(partPuts).toBe(0);
  await page.getByRole("button", { name: "Confirmar e enviar" }).dblclick();
  await expect(page.getByText("Vídeo recebido", { exact: true })).toBeVisible();
  expect(mediaCreates).toBe(1);
  expect(recorderAdmissions).toBe(1);
  expect(partPuts).toBe(1);
  await page.getByRole("button", { name: "Confirmar vistoria" }).click();
  await expect(page.getByText("Envio confirmado.", { exact: false }).last()).toBeVisible();
  expect(submitted).toBe(1);
});

test("E2E-036 explains a supported capture path in an unsupported embedded browser", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(window, "MediaRecorder", { configurable: true, value: undefined });
  });
  await page.route("**/graphql", async (route) => {
    const { query = "" } = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: { responsibilityId: "responsibility-video", recaptureRequestId: null, status: "OPEN", confirmationOnly: false, kind: "INSPECTION", mediaKind: "VIDEO", disclosureVersion: "video-v1", reference: { comparisonMode: "CHECKLIST_ONLY" }, referenceItems: [], policy: { allowGallery: false, maxDurationSeconds: 900 }, requirements: [{ key: "video:main", section: "main", label: "Vídeo principal", instructions: "Grave o imóvel", required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "CAMERA_ONLY", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: false }], answers: [] } });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    throw new Error(`Unexpected operation: ${query.slice(0, 100)}`);
  });

  await page.goto("/capture/unsupported-video-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Registrar vistoria em vídeo" })).toBeVisible();
  await expect(page.getByText("Abra o convite no Safari ou Chrome atualizado do aparelho", { exact: false })).toBeVisible();
  await expect(page.getByText("A gravação não continua em segundo plano", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "Abrir câmera" })).toBeDisabled();
});

test("IT-124 clears incomplete chunks before a new recording after storage quota failure", async ({ page }) => {
  test.skip(test.info().project.name !== "desktop-chrome", "The browser media fixture is validated in Chromium");
  test.setTimeout(60_000);
  let admissions = 0;
  await installFakeGetUserMedia(page, { kind: "canvas", width: 320, height: 180 });
  await page.addInitScript(() => {
    const NativeRecorder = window.MediaRecorder;
    window.MediaRecorder = class extends NativeRecorder {
      constructor(stream: MediaStream, options?: MediaRecorderOptions) {
        super(stream, options);
        (window as typeof window & { activeVideoRecorder?: MediaRecorder }).activeVideoRecorder = this;
      }
    };
    const add = IDBObjectStore.prototype.add;
    let videoChunks = 0;
    IDBObjectStore.prototype.add = function (value, key) {
      if (this.name === "videoChunks" && ++videoChunks === 2) throw new DOMException("Storage quota exceeded", "QuotaExceededError");
      return add.call(this, value, key);
    };
  });
  await page.route("**/graphql", async (route) => {
    const { query = "" } = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: { responsibilityId: "responsibility-quota", recaptureRequestId: null, status: "OPEN", confirmationOnly: false, kind: "INSPECTION", mediaKind: "VIDEO", disclosureVersion: "video-v1", reference: { comparisonMode: "CHECKLIST_ONLY" }, referenceItems: [], policy: { allowGallery: false, maxDurationSeconds: 600 }, requirements: [{ key: "video:main", section: "main", label: "Vídeo principal", instructions: "Grave o imóvel", required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "CAMERA_ONLY", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: false }], answers: [] } });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    if (query.includes("beginVideoCapture")) { admissions += 1; return respond({ beginVideoCapture: { attempt: { id: "quota-attempt", kind: "CURRENT", acquisitionSource: "CAMERA", maxDurationSeconds: 600, maxBytes: 1073741824, policyVersion: 1, admittedAt: "2026-10-15T00:00:00Z", expiresAt: "2099-01-01T00:00:00Z", status: "ACTIVE" }, userErrors: [] } }); }
    throw new Error(`Unexpected operation: ${query.slice(0, 100)}`);
  });

  await page.goto("/capture/quota-video-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await startVideoRecording(page);
  await expect(page.getByText(/00:/)).toBeVisible();
  await page.waitForTimeout(350);
  await page.evaluate(() => (window as typeof window & { activeVideoRecorder?: MediaRecorder }).activeVideoRecorder?.requestData());
  await page.waitForTimeout(350);
  await page.evaluate(() => (window as typeof window & { activeVideoRecorder?: MediaRecorder }).activeVideoRecorder?.requestData());
  await expect(page.getByText(/O dispositivo ficou sem espaço|Não foi possível salvar a gravação/)).toBeVisible({ timeout: 20_000 });
  await expect(page.getByRole("button", { name: "Abrir câmera" })).toBeEnabled();
  await startVideoRecording(page);
  await expect(page.getByText(/00:/)).toBeVisible();
  await page.waitForTimeout(1200);
  await page.getByRole("button", { name: "Parar e revisar" }).click();
  await expect(page.getByRole("heading", { name: "Revise a gravação" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled();
  expect(admissions).toBe(1);
});

test("IT-120/IT-128 keeps received distinct from ready and refreshes without another upload", async ({ page }) => {
  let uploadMutations = 0;
  let preparationReady = false;
  await page.route("**/graphql", async (route) => {
    const { query = "" } = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: { responsibilityId: "responsibility-received", recaptureRequestId: null, status: "OPEN", confirmationOnly: false, kind: "INSPECTION", mediaKind: "VIDEO", disclosureVersion: "video-v1", reference: { comparisonMode: "CHECKLIST_ONLY" }, referenceItems: [], policy: { allowGallery: false, maxDurationSeconds: 900 }, requirements: [{ key: "video:main", section: "main", label: "Vídeo principal", instructions: "Grave o imóvel", required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "CAMERA_ONLY", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: false }], answers: [] } });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    if (query.includes("videoUploadStatus")) return respond({ videoUploadStatus: { mediaId: "media-received", status: preparationReady ? "READY" : "RECEIVED", expiresAt: "2099-01-01T00:00:00Z", receivedParts: [1], confirmedParts: [{ partNumber: 1, etag: "etag-1", sizeBytes: 100 }], missingParts: [] } });
    if (query.includes("videoPreparation")) return respond({ videoPreparation: { mediaId: "media-received", stage: "READY", errorCode: "", retryable: false, processedFrames: 1, sourceAvailable: true } });
    if (/createMediaUpload|presignMediaParts|completeMediaUpload/.test(query)) { uploadMutations += 1; throw new Error("Received media must not start another upload"); }
    throw new Error(`Unexpected operation: ${query.slice(0, 100)}`);
  });

  await page.goto("/healthz");
  await page.evaluate(async () => {
    await new Promise<void>((resolve, reject) => {
      const request = indexedDB.open("inspection-video-capture-v1", 1);
      request.onupgradeneeded = () => {
        const db = request.result;
        db.createObjectStore("videoDrafts", { keyPath: "id" }).createIndex("responsibilityId", "responsibilityId");
        db.createObjectStore("videoChunks", { keyPath: ["attemptId", "sequence"] });
      };
      request.onerror = () => reject(request.error);
      request.onsuccess = () => {
        const db = request.result;
        const transaction = db.transaction("videoDrafts", "readwrite");
        transaction.objectStore("videoDrafts").put({ id: "attempt-received", responsibilityId: "responsibility-received", kind: "INSPECTION", source: "CAMERA", contentType: "video/webm", maxDurationSeconds: 900, maxBytes: 1073741824, expiresAt: "2099-01-01T00:00:00Z", state: "RECEIVED", chunkCount: 1, sizeBytes: 100, sha256: "a".repeat(64), mediaId: "media-received", uploadId: "upload-received", partSizeBytes: 5242880, parts: [{ number: 1, etag: "etag-1" }], metadataSaved: true, preparationStatus: "PENDING_VALIDATION" });
        transaction.oncomplete = () => { db.close(); resolve(); };
        transaction.onerror = () => { db.close(); reject(transaction.error); };
      };
    });
  });
  await page.goto("/capture/received-video-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByText("Recebida · em preparação")).toBeVisible();
  await page.getByRole("button", { name: "Atualizar status" }).click();
  await expect(page.getByText("Estado do vídeo: recebido.", { exact: true })).toBeVisible();
  await expect(page.getByText("Estado do vídeo: pronto.", { exact: true })).toHaveCount(0);
  preparationReady = true;
  await page.getByRole("button", { name: "Atualizar status" }).click();
  await expect(page.getByText("Estado do vídeo: pronto.", { exact: true })).toBeVisible();
  expect(uploadMutations).toBe(0);
});

test("IT-122 shows replacement guidance when server validation reports CHECKSUM_MISMATCH", async ({ page }) => {
  await page.route("**/graphql", async (route) => {
    const { query = "" } = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: { responsibilityId: "responsibility-checksum", recaptureRequestId: null, status: "OPEN", confirmationOnly: false, kind: "INSPECTION", mediaKind: "VIDEO", disclosureVersion: "video-v1", reference: { comparisonMode: "CHECKLIST_ONLY" }, referenceItems: [], policy: { allowGallery: false, maxDurationSeconds: 900 }, requirements: [{ key: "video:main", section: "main", label: "Vídeo principal", instructions: "Grave o imóvel", required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "CAMERA_ONLY", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: false }], answers: [] } });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    if (query.includes("videoUploadStatus")) return respond({ videoUploadStatus: { mediaId: "media-checksum", status: "RECEIVED", expiresAt: "2099-01-01T00:00:00Z", receivedParts: [1], confirmedParts: [{ partNumber: 1, etag: "etag-1", sizeBytes: 100 }], missingParts: [] } });
    if (query.includes("videoPreparation")) return respond({ videoPreparation: { mediaId: "media-checksum", stage: "FAILED", errorCode: "CHECKSUM_MISMATCH", retryable: false, processedFrames: 0, sourceAvailable: false } });
    throw new Error(`Unexpected operation: ${query.slice(0, 100)}`);
  });

  await page.goto("/healthz");
  await page.evaluate(async () => {
    await new Promise<void>((resolve, reject) => {
      const request = indexedDB.open("inspection-video-capture-v1", 1);
      request.onupgradeneeded = () => {
        const db = request.result;
        db.createObjectStore("videoDrafts", { keyPath: "id" }).createIndex("responsibilityId", "responsibilityId");
        db.createObjectStore("videoChunks", { keyPath: ["attemptId", "sequence"] });
      };
      request.onerror = () => reject(request.error);
      request.onsuccess = () => {
        const db = request.result;
        const transaction = db.transaction("videoDrafts", "readwrite");
        transaction.objectStore("videoDrafts").put({ id: "attempt-checksum", responsibilityId: "responsibility-checksum", kind: "INSPECTION", source: "CAMERA", contentType: "video/webm", maxDurationSeconds: 900, maxBytes: 1073741824, expiresAt: "2099-01-01T00:00:00Z", state: "RECEIVED", chunkCount: 1, sizeBytes: 100, sha256: "a".repeat(64), mediaId: "media-checksum", uploadId: "upload-checksum", partSizeBytes: 5242880, parts: [{ number: 1, etag: "etag-1" }], metadataSaved: true, preparationStatus: "PENDING_VALIDATION" });
        transaction.oncomplete = () => { db.close(); resolve(); };
        transaction.onerror = () => { db.close(); reject(transaction.error); };
      };
    });
  });
  await page.goto("/capture/checksum-video-invite");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Atualizar status" }).click();
  const preparationNotice = page.getByRole("status").filter({ hasText: "A preparação desta gravação foi interrompida." });
  await expect(preparationNotice).toContainText("O gestor da vistoria poderá solicitar uma nova gravação.");
  await expect(page.getByRole("button", { name: "Gravar substituição" })).toHaveCount(0);
});

test("E2E-027 physical iPhone Safari and Android Chrome capture", async () => {
  test.skip(true, "Physical iPhone and Android devices are unavailable; see .compozy/tasks/vistoria-video/device-validation-gate.md");
});

test("E2E-028 physical device camera denial and screen-lock recovery", async () => {
  test.skip(true, "Physical devices are unavailable; see .compozy/tasks/vistoria-video/device-validation-gate.md");
});
