import { expect, test } from "@playwright/test";
import { deflateSync } from "node:zlib";
import { installFakeGetUserMedia } from "./support/get-user-media";
import { installRuntimeGuards } from "./support/runtime";
import { startVideoRecording } from "./support/video-capture";

test.use({ serviceWorkers: "block" });

const requirement = (key: string, name: string, typeCode: string, addedDuringCapture = false, mediaKind: "PHOTO" | "VIDEO" = "PHOTO") => ({ key, section: name, label: name, instructions: name, required: true, minimumMedia: 1, maximumMedia: mediaKind === "VIDEO" ? 3 : 20, descriptionRequired: false, captureSourcePolicy: "CAMERA_DEFAULT", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: true, environmentIdentityId: `identity-${key}`, environmentTypeCode: typeCode, environmentName: name, expected: !addedDuringCapture, addedDuringCapture });
const environment = (key: string, name: string, typeCode: string, addedDuringCapture: boolean) => ({ requirementKey: key, identityId: `identity-${key}`, typeCode, name, expected: !addedDuringCapture, addedDuringCapture, mediaCount: 0, status: "PENDING" });
const bootstrap = (overrides: Record<string, unknown> = {}) => ({ responsibilityId: "environment-responsibility", recaptureRequestId: null, status: "OPEN", confirmationOnly: false, kind: "INSPECTION", mediaKind: "PHOTO", disclosureVersion: "v1", captureLayout: "ENVIRONMENTS", reference: { comparisonMode: "CHECKLIST_ONLY" }, referenceItems: [], policy: { allowGallery: true, gpsRequired: false, maxDurationSeconds: 900 }, requirements: [], environments: [], reviewHandoff: null, answers: [], ...overrides });

function pngChunk(type: string, data: Buffer): Buffer {
  const typeBytes = Buffer.from(type);
  const chunk = Buffer.alloc(12 + data.length);
  chunk.writeUInt32BE(data.length, 0);
  typeBytes.copy(chunk, 4);
  data.copy(chunk, 8);
  let crc = 0xffffffff;
  for (const byte of chunk.subarray(4, 8 + data.length)) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
  }
  chunk.writeUInt32BE((crc ^ 0xffffffff) >>> 0, 8 + data.length);
  return chunk;
}

function photoFixture(seed: string, dark = false): Buffer {
  const width = 64;
  const height = 64;
  const pixels = Buffer.alloc(height * (1 + width * 3));
  const seedValue = [...seed].reduce((value, char) => value + char.charCodeAt(0), 0);
  for (let y = 0; y < height; y += 1) {
    const row = y * (1 + width * 3);
    pixels[row] = 0;
    for (let x = 0; x < width; x += 1) {
      const texture = ((x * 17 + y * 31 + seedValue * 13) % 7) < 3;
      const value = dark ? (texture ? 8 : 24) : (texture ? 110 + seedValue % 20 : 220 - seedValue % 20);
      const offset = row + 1 + x * 3;
      pixels[offset] = value;
      pixels[offset + 1] = dark ? value : Math.min(255, value + (x % 3) * 4);
      pixels[offset + 2] = dark ? value : Math.max(0, value - (y % 3) * 4);
    }
  }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8;
  header[9] = 2;
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    pngChunk("IHDR", header),
    pngChunk("IDAT", deflateSync(pixels)),
    pngChunk("IEND", Buffer.alloc(0))
  ]);
}

async function authenticateCapture(page: import("@playwright/test").Page, token: string) {
  await page.goto(`/capture/${token}`);
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel(/Processamento das fotos|Processamento do vídeo e imagens derivadas/).check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
}

async function uploadPhoto(page: import("@playwright/test").Page, name: string, dark = false) {
  await page.getByRole("button", { name: new RegExp(name) }).click();
  await page.getByLabel("Tirar foto").setInputFiles({ name: `${name}.png`, mimeType: "image/png", buffer: photoFixture(name, dark) });
  await page.getByRole("button", { name: "Usar esta foto" }).click();
  if (dark) {
    await expect(page.getByText(/pouca luz/)).toBeVisible();
    await page.getByRole("button", { name: "Continuar mesmo assim", exact: true }).click();
  }
  await expect(page.getByRole("heading", { name: "Envios" })).toBeVisible();
  await expect(page.getByText(/\d+ concluído(?:s)? · 0 pendentes/)).toBeVisible();
  await expect(page.getByRole("button", { name: new RegExp(`${name}.*Concluído`) })).toBeVisible();
}

async function configurePhotoCapture(page: import("@playwright/test").Page, initial = bootstrap()) {
  const external = initial as ReturnType<typeof bootstrap>;
  let mediaSequence = 0;
  const qualityReports: Array<{ requirementKey: string; code: string; ignored: boolean }> = [];
  await page.route("**/photo-part", (route) => route.fulfill({ status: 200, headers: { etag: `photo-part-${mediaSequence}` } }));
  await page.route("**/graphql", async (route) => {
    const { query = "", variables = {} } = JSON.parse(route.request().postData() ?? "{}") as { query?: string; variables?: { input?: Record<string, unknown> } };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "capture-csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: external });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    if (query.includes("declareCaptureEnvironment")) {
      const input = variables.input ?? {};
      const name = String(input.name ?? "Ambiente"); const typeCode = String(input.typeCode ?? "OTHER");
      if (typeCode === "BEDROOM" && name === "Quarto") return respond({ declareCaptureEnvironment: { environment: null, userErrors: [{ code: "CONFLICT", field: "name", message: "DUPLICATE_ENVIRONMENT" }] } });
      const key = `env:${typeCode.toLowerCase()}:${name.toLowerCase().replaceAll(" ", "-")}`;
      const reqs = external.requirements as unknown[]; const envs = external.environments as unknown[];
      const nextRequirement = requirement(key, name, typeCode, true);
      (external as Record<string, unknown>).requirements = [...reqs, nextRequirement];
      (external as Record<string, unknown>).environments = [...envs, environment(key, name, typeCode, true)];
      return respond({ declareCaptureEnvironment: { environment: environment(key, name, typeCode, true), userErrors: [] } });
    }
    if (query.includes("declareCaptureImpossibility")) {
      const key = String(variables.input?.requirementKey);
      const current = external.answers as Array<Record<string, unknown>>;
      (external as Record<string, unknown>).answers = [...current.filter((answer) => answer.requirementKey !== key), { requirementKey: key, mediaIds: [], impossibilityReason: String(variables.input?.reasonCode ?? "LOCKED"), version: 1 }];
      return respond({ declareCaptureImpossibility: { status: "OPEN", userErrors: [] } });
    }
    if (query.includes("recordCaptureQualityWarning")) { qualityReports.push(variables.input as { requirementKey: string; code: string; ignored: boolean }); return respond({ recordCaptureQualityWarning: { answer: null, userErrors: [] } }); }
    if (query.includes("createMediaUpload")) {
      mediaSequence += 1;
      return respond({ createMediaUpload: { upload: { mediaId: `photo-${mediaSequence}`, uploadId: `upload-${mediaSequence}`, expiresAt: "2099-01-01T00:00:00Z", partSizeBytes: 1024 * 1024 }, userErrors: [] } });
    }
    if (query.includes("presignMediaParts")) return respond({ presignMediaParts: { parts: [{ partNumber: 1, url: new URL("/photo-part", page.url()).href, expiresAt: "2099-01-01T00:00:00Z" }], userErrors: [] } });
    if (query.includes("completeMediaUpload")) return respond({ completeMediaUpload: { media: { id: `photo-${mediaSequence}`, status: "VERIFIED" }, userErrors: [] } });
    if (query.includes("saveCaptureMetadata")) {
      const input = variables.input ?? {}; const key = String(input.requirementKey); const id = String(input.mediaId ?? `photo-${mediaSequence}`);
      const current = external.answers as Array<{ requirementKey: string; mediaIds: string[]; impossibilityReason: string | null; version: number }>;
      (external as Record<string, unknown>).answers = [...current.filter((answer) => answer.requirementKey !== key), { requirementKey: key, mediaIds: [...new Set([...(current.find((answer) => answer.requirementKey === key)?.mediaIds ?? []), id])], impossibilityReason: null, version: 1 }];
      return respond({ saveCaptureMetadata: { media: { id, status: "READY", replacesMediaId: null }, userErrors: [] } });
    }
    if (query.includes("submitCapture")) { (external as Record<string, unknown>).status = "COMPLETED"; return respond({ submitCapture: { submission: { id: "submission", complete: true, requiresAttention: false, submittedAt: "2026-10-25T00:00:00Z" }, userErrors: [] } }); }
    throw new Error(`Unexpected operation ${query.slice(0, 90)}`);
  });
  return qualityReports;
}

async function declareEnvironment(page: import("@playwright/test").Page, type: string, name: string) {
  await page.getByLabel("Tipo de ambiente").selectOption(type);
  await page.getByLabel("Nome do ambiente").fill(name);
  await page.getByRole("button", { name: "Continuar", exact: true }).click();
  await expect(page.getByRole("button", { name: new RegExp(name) }).last()).toBeVisible();
}

test("E2E-001 OTP, consent, declare environments, ignore dark warning, impossibility and submit", async ({ page }) => {
  const qualityReports = await configurePhotoCapture(page);
  await authenticateCapture(page, "photo-cataloging");
  await declareEnvironment(page, "LIVING_ROOM", "Sala");
  await declareEnvironment(page, "KITCHEN", "Cozinha");
  await declareEnvironment(page, "OTHER", "Depósito");
  for (const name of ["Sala", "Cozinha", "Depósito"]) await expect(page.getByRole("button", { name: new RegExp(name) })).toBeVisible();
  await expect(page.getByRole("button", { name: /Depósito.*adicionado na vistoria/ })).toBeVisible();
  await uploadPhoto(page, "Sala", true);
  await expect.poll(() => qualityReports).toContainEqual(expect.objectContaining({ code: "DARK", ignored: true }));
  await uploadPhoto(page, "Cozinha");
  await uploadPhoto(page, "Depósito");
  await declareEnvironment(page, "BATHROOM", "Banheiro");
  await page.getByRole("button", { name: /Banheiro/ }).click();
  await page.getByRole("button", { name: "Não consegue fotografar?" }).click();
  await page.getByLabel("Motivo da impossibilidade").selectOption("LOCKED");
  await page.getByRole("button", { name: "Registrar impossibilidade" }).click();
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await page.getByRole("button", { name: "Enviar vistoria completa" }).click();
  await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
});

test("E2E-002 records Sala and Cozinha clips, re-records Cozinha and submits environment confirmation", async ({ page }) => {
  test.setTimeout(90_000);
  await installFakeGetUserMedia(page, { kind: "canvas", width: 320, height: 180 });
  await page.addInitScript(() => {
    const NativeRecorder = window.MediaRecorder;
    if (NativeRecorder) window.MediaRecorder = class extends NativeRecorder {
      constructor(stream: MediaStream, options?: MediaRecorderOptions) { super(stream, options); (window as typeof window & { captureRecorder?: MediaRecorder }).captureRecorder = this; }
    };
  });
  const external = bootstrap({ mediaKind: "VIDEO", requirements: [], environments: [] });
  let currentRequirement = 0; let uploadId = ""; let isReceived = false; let attempts = 0;
  const replacementMediaIds: Array<string | null> = [];
  await page.route("**/video-part", (route) => route.fulfill({ status: 200, headers: { etag: `video-etag-${attempts}` } }));
  await page.route("**/graphql", async (route) => {
    const { query = "", variables = {} } = JSON.parse(route.request().postData() ?? "{}") as { query?: string; variables?: { input?: Record<string, unknown> } };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "capture-csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: external });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    if (query.includes("declareCaptureEnvironment")) {
      const input = variables.input ?? {}; const typeCode = String(input.typeCode); const name = String(input.name); const key = `env:${typeCode.toLowerCase()}:${name.toLowerCase()}`;
      (external as Record<string, unknown>).requirements = [...external.requirements as unknown[], requirement(key, name, typeCode, true, "VIDEO")];
      (external as Record<string, unknown>).environments = [...external.environments as unknown[], environment(key, name, typeCode, true)];
      return respond({ declareCaptureEnvironment: { environment: environment(key, name, typeCode, true), userErrors: [] } });
    }
    if (query.includes("beginVideoCapture")) { attempts += 1; currentRequirement = Number(attempts); return respond({ beginVideoCapture: { attempt: { id: `attempt-${attempts}`, kind: "CURRENT", acquisitionSource: "CAMERA", maxDurationSeconds: 60, maxBytes: 1073741824, policyVersion: 1, admittedAt: "2026-10-25T00:00:00Z", expiresAt: "2099-01-01T00:00:00Z", status: "ACTIVE" }, userErrors: [] } }); }
    if (query.includes("createMediaUpload")) { uploadId = `video-${currentRequirement}`; isReceived = false; return respond({ createMediaUpload: { upload: { mediaId: uploadId, uploadId: `upload-${uploadId}`, partSizeBytes: 5 * 1024 * 1024, expiresAt: "2099-01-01T00:00:00Z" }, userErrors: [] } }); }
    if (query.includes("videoUploadStatus")) return respond({ videoUploadStatus: { mediaId: uploadId, status: isReceived ? "RECEIVED" : "UPLOADING", expiresAt: "2099-01-01T00:00:00Z", receivedParts: [], confirmedParts: [], missingParts: [1] } });
    if (query.includes("presignMediaParts")) return respond({ presignMediaParts: { parts: [{ partNumber: 1, url: new URL("/video-part", page.url()).href, expiresAt: "2099-01-01T00:00:00Z" }], userErrors: [] } });
    if (query.includes("completeMediaUpload")) { isReceived = true; return respond({ completeMediaUpload: { media: { id: uploadId, status: "RECEIVED" }, userErrors: [] } }); }
    if (query.includes("saveCaptureMetadata")) {
      const replacesMediaId = variables.input?.replacesMediaId == null ? null : String(variables.input.replacesMediaId);
      replacementMediaIds.push(replacesMediaId);
      return respond({ saveCaptureMetadata: { media: { id: uploadId, status: "RECEIVED", replacesMediaId }, userErrors: [] } });
    }
    if (query.includes("submitCapture")) { (external as Record<string, unknown>).status = "COMPLETED"; return respond({ submitCapture: { submission: { id: "video-submission", complete: true, requiresAttention: false, submittedAt: "2026-10-25T00:00:00Z" }, userErrors: [] } }); }
    throw new Error(`Unexpected operation ${query.slice(0, 90)}`);
  });
  const assertRuntime = installRuntimeGuards(page);
  await authenticateCapture(page, "video-cataloging");
  await expect(page.getByRole("heading", { name: "Um clipe por ambiente" })).toBeVisible();
  await declareEnvironment(page, "LIVING_ROOM", "Sala");
  await declareEnvironment(page, "KITCHEN", "Cozinha");
  const record = async (room: string) => {
    const environmentButton = page.getByRole("button", { name: new RegExp(`${room} ·`) });
    await expect(environmentButton).toBeEnabled();
    await environmentButton.click();
    await startVideoRecording(page);
    await expect(page.getByRole("status").filter({ hasText: "Gravando." })).toBeVisible();
    await page.waitForTimeout(2200);
    await page.getByRole("button", { name: "Parar e revisar" }).click();
    await expect(page.getByRole("heading", { name: "Revise a gravação" })).toBeVisible();
    await page.getByRole("button", { name: "Confirmar e enviar" }).click();
    await expect(page.locator(".inspection-status").getByText("Vídeo recebido", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: new RegExp(`${room} · 1/3 · Concluído`) })).toBeEnabled();
  };
  await record("Sala");
  await record("Cozinha");
  const cozinhaItem = page.getByRole("listitem").filter({ has: page.getByRole("button", { name: /Cozinha · 1\/3 · Concluído/ }) });
  await cozinhaItem.getByRole("button", { name: "Gravar novamente" }).click();
  await startVideoRecording(page);
  await expect(page.getByRole("status").filter({ hasText: "Gravando." })).toBeVisible();
  await page.waitForTimeout(2200);
  await page.getByRole("button", { name: "Parar e revisar" }).click();
  await page.getByRole("button", { name: "Confirmar e enviar" }).click();
  await expect(page.locator(".inspection-status").getByText("Vídeo recebido", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /Cozinha · 1\/3 · Concluído/ })).toBeEnabled();
  await expect(cozinhaItem.getByRole("button", { name: "Gravar novamente" })).toHaveCount(1);
  expect(replacementMediaIds).toEqual([null, null, "video-2"]);
  await page.getByRole("button", { name: /Sala/ }).click();
  await expect(page.getByRole("button", { name: "Enviar vistoria" })).toBeEnabled();
  await page.getByRole("button", { name: "Enviar vistoria" }).click();
  await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Gravar novamente" })).toHaveCount(0);
  await expect(page.getByRole("list", { name: "Ambientes registrados" }).getByRole("listitem")).toHaveText(["Sala", "Cozinha"]);
  await assertRuntime();
});

test("UT-339 unsupported video recorder offers impossibility and renders no photo input", async ({ page }) => {
  await page.addInitScript(() => { Object.defineProperty(window, "MediaRecorder", { configurable: true, value: undefined }); });
  const external = bootstrap({ mediaKind: "VIDEO", requirements: [], environments: [] });
  await page.route("**/graphql", async (route) => {
    const { query = "", variables = {} } = JSON.parse(route.request().postData() ?? "{}") as { query?: string; variables?: { input?: Record<string, unknown> } };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: external });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    if (query.includes("declareCaptureEnvironment")) {
      const typeCode = String(variables.input?.typeCode); const name = String(variables.input?.name); const key = `env:${typeCode}:${name}`;
      (external as Record<string, unknown>).requirements = [requirement(key, name, typeCode, true, "VIDEO")];
      (external as Record<string, unknown>).environments = [environment(key, name, typeCode, true)];
      return respond({ declareCaptureEnvironment: { environment: environment(key, name, typeCode, true), userErrors: [] } });
    }
    if (query.includes("declareCaptureImpossibility")) return respond({ declareCaptureImpossibility: { status: "OPEN", userErrors: [] } });
    throw new Error(`Unexpected operation ${query.slice(0, 90)}`);
  });
  await authenticateCapture(page, "unsupported-environment-video");
  await expect(page.getByText(/Safari ou Chrome atualizado/)).toBeVisible();
  await declareEnvironment(page, "OTHER", "Depósito");
  await expect(page.getByRole("button", { name: "Declarar impossibilidade" })).toBeVisible();
  await expect(page.locator('input[type="file"][accept*="image/"]')).toHaveCount(0);
});

test("E2E-003 expected baseline rooms, differentiated duplicate name, pending block and resolution", async ({ page }) => {
  const external = bootstrap({ reference: { comparisonMode: "FIXED_ORIGIN" }, requirements: [requirement("env:bedroom", "Quarto", "BEDROOM")], environments: [environment("env:bedroom", "Quarto", "BEDROOM", false)] });
  await configurePhotoCapture(page, external);
  await authenticateCapture(page, "comparative");
  await expect(page.getByRole("button", { name: /Quarto/ })).toBeVisible();
  await page.getByLabel("Tipo de ambiente").selectOption("BEDROOM");
  await page.getByLabel("Nome do ambiente").fill("Quarto");
  await expect(page.getByText(/Esse ambiente já existe.*Quarto 2/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Continuar" })).toBeDisabled();
  await page.getByLabel("Nome do ambiente").fill("Quarto 2");
  await page.getByRole("button", { name: "Continuar" }).click();
  await expect(page.getByRole("button", { name: /Quarto 2.*adicionado na vistoria/ })).toBeVisible();
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  const pendingEnvironments = page.getByRole("list", { name: "Ambientes pendentes" });
  await expect(pendingEnvironments.getByRole("listitem")).toHaveText(["Quarto", "Quarto 2"]);
  await expect(page.getByRole("button", { name: "Enviar vistoria completa" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Confirmar envio incompleto" })).toBeDisabled();
  await page.getByRole("button", { name: "Ver etapa" }).first().click();
  await page.getByRole("button", { name: "Não consegue fotografar?" }).click();
  await page.getByLabel("Motivo da impossibilidade").selectOption("LOCKED");
  await page.getByRole("button", { name: "Registrar impossibilidade" }).click();
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await expect(pendingEnvironments.getByRole("listitem")).toHaveText(["Quarto 2"]);
  await page.getByRole("button", { name: "Ver etapa" }).last().click();
  await page.getByRole("button", { name: "Não consegue fotografar?" }).click();
  await page.getByLabel("Motivo da impossibilidade").selectOption("LOCKED");
  await page.getByRole("button", { name: "Registrar impossibilidade" }).click();
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await page.getByRole("button", { name: "Enviar vistoria completa" }).click();
  await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
  await expect(page.getByRole("list", { name: "Ambientes registrados" }).getByRole("listitem")).toHaveText(["Quarto", "Quarto 2"]);
});

test("E2E-004 review handoff opens ready and preparing Dashboard routes only for flagged invitations", async ({ page }) => {
  const authenticateAndSubmit = async (token: string, handoff: { inspectionId: string; originVersionId: string | null } | null) => {
    let external = bootstrap({ captureLayout: null, requirements: [], reviewHandoff: handoff });
    await page.route("**/graphql", async (route) => {
      const { query = "" } = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
      const respond = (data: unknown) => route.fulfill({ json: { data } });
      if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
      if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
      if (query.includes("externalCapture")) return respond({ externalCapture: external });
      if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
      if (query.includes("submitCapture")) { external = { ...external, status: "COMPLETED" }; return respond({ submitCapture: { submission: { id: "submission", complete: true, requiresAttention: false, submittedAt: "2026-10-25T00:00:00Z" }, userErrors: [] } }); }
      throw new Error(`Unexpected operation ${query.slice(0, 90)}`);
    });
    await authenticateCapture(page, token);
    await page.getByRole("button", { name: "Revisar vistoria" }).click();
    await page.getByRole("button", { name: "Confirmar envio incompleto" }).click();
    await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
  };
  await authenticateAndSubmit("flagged-ready", { inspectionId: "inspection-review", originVersionId: "version-review" });
  await expect(page.getByRole("link", { name: "Revisar agora" })).toHaveAttribute("href", "http://localhost:3002/baselines/review/version-review");
  await page.unrouteAll();
  await authenticateAndSubmit("flagged-preparing", { inspectionId: "inspection-preparing", originVersionId: null });
  await expect(page.getByText(/revisão está sendo preparada/)).toBeVisible();
  await expect(page.getByRole("link", { name: "Revisar agora" })).toHaveAttribute("href", "http://localhost:3002/baselines/review/inspection/inspection-preparing");
  await page.unrouteAll();
  await authenticateAndSubmit("unflagged", null);
  await expect(page.getByRole("link", { name: "Revisar agora" })).toHaveCount(0);
});
