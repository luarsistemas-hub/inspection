import { execFileSync } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { expect, type APIRequestContext, type Page } from "@playwright/test";
import { loginAsLocalAdmin } from "./auth";

const workspace = resolve(process.cwd(), "../..");
const apiURL = process.env.INSPECTION_E2E_GRAPHQL_URL ?? "http://localhost:8080/graphql";
const mailpitURL = process.env.PLAYWRIGHT_MAILPIT_URL ?? "http://localhost:8026";
const wiremockURL = process.env.INSPECTION_E2E_WIREMOCK_URL ?? "http://localhost:18080";

type MailMessage = { ID: string; Subject: string; To: Array<{ Address: string }> };
type GraphQLResult<T> = { data?: T; errors?: Array<{ message: string }> };
type CaptureSession = {
  request: APIRequestContext;
  csrf: string;
  linkToken: string;
  inspectionId: string;
  assetId: string;
  bootstrap: {
    responsibilityId: string;
    disclosureVersion: string;
    mediaKind: string;
    requirements: Array<{ key: string; label: string; section: string }>;
  };
};
export type StackNote = { id: string; inspectionId: string; currentRevisionId: string; text: string; version: number; environmentId: string; sourceMediaId?: string; photoMediaId?: string; videoAttemptId?: string; startMs?: number; endMs?: number };
export type CatalogMapping = { id: string; remove: () => Promise<void> };

export function seedCaptureURL(mediaKind: "PHOTO" | "VIDEO" = "PHOTO", voiceEligible = true): { url: string; inspectionId: string } {
  const output = execFileSync("./scripts/local.sh", ["seed"], {
    cwd: workspace,
    encoding: "utf8",
    env: { ...process.env, INSPECTION_QA_MEDIA_KIND: mediaKind, INSPECTION_QA_VOICE: voiceEligible ? "1" : "0" },
  });
  const captureURL = output.match(/Capture URL: (\S+)/)?.[1];
  const inspectionId = output.match(/Inspection ID: ([0-9a-f-]+)/i)?.[1];
  if (!captureURL || !inspectionId) throw new Error("O seed local não retornou os dados da vistoria de QA.");
  return { url: captureURL, inspectionId };
}

function tokenFromCaptureURL(captureURL: string): string {
  const url = new URL(captureURL);
  const queryToken = url.searchParams.get("token") ?? url.searchParams.get("linkToken");
  const pathToken = url.pathname.split("/").filter(Boolean).at(-1);
  const token = queryToken ?? pathToken;
  if (!token || token.length < 24) throw new Error("A URL do Capture não contém o convite descartável esperado.");
  return decodeURIComponent(token);
}

async function graphql<T>(request: APIRequestContext, query: string, variables?: Record<string, unknown>, csrf?: string): Promise<T> {
  const response = await request.post(apiURL, {
    data: { query, variables },
    headers: csrf ? { "X-CSRF-Token": csrf } : undefined,
  });
  const body = await response.json() as GraphQLResult<T>;
  if (!response.ok() || body.errors?.length || !body.data) {
    throw new Error(`GraphQL Capture falhou (${response.status()}): ${body.errors?.map((error) => error.message).join("; ") ?? "resposta vazia"}`);
  }
  return body.data;
}

async function latestOTP(request: APIRequestContext, existing: Set<string>): Promise<string> {
  const response = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!response.ok()) return "";
  const messages = (await response.json() as { messages: MailMessage[] }).messages;
  const match = messages.find((message) => !existing.has(message.ID)
    && message.Subject === "Seu código de acesso"
    && message.To.some((recipient) => recipient.Address === "qa.inspection@example.test"));
  if (!match) return "";
  const detail = await request.get(`${mailpitURL}/api/v1/message/${match.ID}`);
  if (!detail.ok()) return "";
  const body = await detail.json() as { Text?: string };
  return body.Text?.match(/Código:\s*(\d{6})/)?.[1] ?? "";
}

export async function authenticateCapture(request: APIRequestContext, captureURL: string, inspectionId: string, options: { allowVoiceDisabled?: boolean } = {}): Promise<CaptureSession> {
  const linkToken = tokenFromCaptureURL(captureURL);
  const before = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  const existing = new Set(before.ok() ? ((await before.json() as { messages: MailMessage[] }).messages).map((message) => message.ID) : []);
  const requested = await graphql<{ requestInvitationOtp: { status: string; userErrors: Array<{ message: string }> } }>(request,
    "mutation RequestInvitationOtp($input: RequestInvitationOtpInput!) { requestInvitationOtp(input: $input) { status userErrors { message } } }",
    { input: { linkToken, clientMutationId: randomUUID() } });
  if (requested.requestInvitationOtp.userErrors.length) throw new Error(requested.requestInvitationOtp.userErrors[0].message);
  let code = "";
  await expect.poll(async () => { code = await latestOTP(request, existing); return code; }, { timeout: 30_000, message: "OTP do participante chegar ao Mailpit local" }).toMatch(/^\d{6}$/);
  const verified = await graphql<{ verifyInvitationOtp: { csrfToken: string; userErrors: Array<{ message: string }> } }>(request,
    "mutation VerifyInvitationOtp($input: VerifyInvitationOtpInput!) { verifyInvitationOtp(input: $input) { csrfToken userErrors { message } } }",
    { input: { linkToken, code, clientMutationId: randomUUID() } });
  if (verified.verifyInvitationOtp.userErrors.length) throw new Error(verified.verifyInvitationOtp.userErrors[0].message);
  const csrf = verified.verifyInvitationOtp.csrfToken;
  const { externalCapture } = await graphql<{ externalCapture: CaptureSession["bootstrap"] }>(request,
    "query ExternalCaptureBootstrap { externalCapture { responsibilityId disclosureVersion mediaKind requirements { key label section } } }", undefined, csrf);
  // The seed's disposable invitation belongs to the inspection whose ID it prints.
  // reviewHandoff stays null until the inspection has entered review.
  const session: CaptureSession = { request, csrf, linkToken, inspectionId, assetId: "", bootstrap: externalCapture };
  const voice = await graphql<{ voicePolicy: { enabled: boolean } }>(request, "query CaptureVoicePolicy { voicePolicy { enabled } }", undefined, csrf);
  if (!voice.voicePolicy.enabled && !options.allowVoiceDisabled) throw new Error("A política de voz do seed local não está habilitada.");
  const accepted = await graphql<{ acceptProcessing: { status: string; userErrors: Array<{ message: string }> } }>(request,
    "mutation AcceptProcessing($input: AcceptProcessingInput!) { acceptProcessing(input: $input) { status userErrors { message } } }",
    { input: { disclosureVersion: externalCapture.disclosureVersion, photoProcessing: true, videoProcessing: externalCapture.mediaKind === "VIDEO", voiceProcessing: voice.voicePolicy.enabled, voiceConsentVersion: voice.voicePolicy.enabled ? "voice-v1" : null, aiAnalysis: true, gpsUse: true, clientMutationId: randomUUID() } }, csrf);
  if (accepted.acceptProcessing.userErrors.length) throw new Error(accepted.acceptProcessing.userErrors[0].message);
  const refreshed = await graphql<{ externalCapture: CaptureSession["bootstrap"] }>(request,
    "query ExternalCaptureBootstrap { externalCapture { responsibilityId disclosureVersion mediaKind requirements { key label section } } }", undefined, csrf);
  session.bootstrap = refreshed.externalCapture;
  return session;
}

function mediaFixture(kind: "photo" | "video" | "audio"): { path: string; contentType: string; durationMs: number; cleanup: () => void } {
  const directory = mkdtempSync(resolve(tmpdir(), "inspection-e2e-voice-"));
  const path = resolve(directory, kind === "photo" ? "evidence.png" : kind === "video" ? "evidence.mp4" : "note.webm");
  if (kind === "photo") execFileSync("ffmpeg", ["-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=white:s=640x480", "-frames:v", "1", "-y", path]);
  if (kind === "video") execFileSync("ffmpeg", ["-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=320x240:r=15:d=2", "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-y", path]);
  if (kind === "audio") execFileSync("ffmpeg", ["-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:a", "libopus", "-b:a", "32k", "-f", "webm", "-y", path]);
  return { path, contentType: kind === "photo" ? "image/png" : kind === "video" ? "video/mp4" : "audio/webm;codecs=opus", durationMs: kind === "audio" ? 2000 : 0, cleanup: () => rmSync(directory, { recursive: true, force: true }) };
}

async function uploadMedia(session: CaptureSession, requirementKey: string, media: ReturnType<typeof mediaFixture>, attemptId?: string, replacesMediaId?: string): Promise<string> {
  const bytes = readFileSync(media.path);
  const sha256 = createHash("sha256").update(bytes).digest("hex");
  const created = await graphql<{ createMediaUpload: { upload: { mediaId: string; partSizeBytes: number } | null; userErrors: Array<{ message: string }> } }>(session.request,
    "mutation CreateMediaUpload($input: CreateMediaUploadInput!) { createMediaUpload(input: $input) { upload { mediaId partSizeBytes } userErrors { message } } }",
    { input: { requirementKey, contentType: media.contentType, sizeBytes: bytes.length, sha256, attemptId: attemptId ?? null, clientMutationId: randomUUID() } }, session.csrf);
  if (created.createMediaUpload.userErrors.length || !created.createMediaUpload.upload) throw new Error(created.createMediaUpload.userErrors[0]?.message ?? "A API não iniciou o envio de mídia.");
  const mediaId = created.createMediaUpload.upload.mediaId;
  const signed = await graphql<{ presignMediaParts: { parts: Array<{ partNumber: number; url: string }>; userErrors: Array<{ message: string }> } }>(session.request,
    "mutation PresignMediaParts($input: PresignMediaPartsInput!) { presignMediaParts(input: $input) { parts { partNumber url } userErrors { message } } }",
    { input: { mediaId, partNumbers: [1], clientMutationId: randomUUID() } }, session.csrf);
  if (signed.presignMediaParts.userErrors.length || !signed.presignMediaParts.parts[0]) throw new Error(signed.presignMediaParts.userErrors[0]?.message ?? "A API não autorizou a parte de mídia.");
  const part = signed.presignMediaParts.parts[0];
  const upload = await session.request.put(part.url, { data: bytes, headers: { "Content-Type": media.contentType } });
  const etag = upload.headers().etag?.replaceAll('"', "");
  if (!upload.ok() || !etag) throw new Error(`MinIO rejeitou a mídia de QA (${upload.status()}).`);
  const completed = await graphql<{ completeMediaUpload: { userErrors: Array<{ message: string }> } }>(session.request,
    "mutation CompleteMediaUpload($input: CompleteMediaUploadInput!) { completeMediaUpload(input: $input) { userErrors { message } } }",
    { input: { mediaId, parts: [{ partNumber: 1, etag }], clientMutationId: randomUUID() } }, session.csrf);
  if (completed.completeMediaUpload.userErrors.length) throw new Error(completed.completeMediaUpload.userErrors[0].message);
  let saved = false;
  for (let attempt = 0; attempt < 30 && !saved; attempt += 1) {
    try {
      const metadata = await graphql<{ saveCaptureMetadata: { userErrors: Array<{ message: string; code: string }> } }>(session.request,
        "mutation SaveCaptureMetadata($input: SaveCaptureMetadataInput!) { saveCaptureMetadata(input: $input) { userErrors { message code } } }",
        { input: { mediaId, requirementKey, description: "Evidência de QA para catálogo e revisão de voz.", captureSource: "CAMERA", gps: null, deviceContext: { test: "voice-catalog-stack" }, replacesMediaId: replacesMediaId ?? null, clientMutationId: `metadata:${mediaId}` } }, session.csrf);
      const errors = metadata.saveCaptureMetadata.userErrors;
      if (!errors.length) saved = true;
      else if (!errors.some((error) => error.code === "INVALID_STATE" || /processamento|prepara/i.test(error.message))) throw new Error(errors[0].message);
      else await new Promise((resolveDelay) => setTimeout(resolveDelay, 1_000));
    } catch (error) {
      if (!/processamento|prepara|pending/i.test(error instanceof Error ? error.message : String(error)) || attempt === 29) throw error;
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 1_000));
    }
  }
  if (!saved) throw new Error("A mídia de QA permaneceu em processamento e não pôde ser anexada à captura.");
  return mediaId;
}

async function waitMediaReady(session: CaptureSession, mediaId: string): Promise<void> {
  await expect.poll(async () => {
    const result = await graphql<{ externalCapture: { answers: Array<{ mediaIds: string[] }> } }>(session.request,
      "query ExternalCaptureBootstrap { externalCapture { answers { mediaIds } } }", undefined, session.csrf);
    return result.externalCapture.answers.some((answer) => answer.mediaIds.includes(mediaId));
  }, { timeout: 60_000, message: `mídia ${mediaId} ficar anexada à captura após a triagem` }).toBe(true);
}

async function waitVideoReady(session: CaptureSession, mediaId: string): Promise<void> {
  await expect.poll(async () => {
    const result = await graphql<{ videoPreparation: { stage: string; sourceAvailable: boolean } | null }>(session.request,
      "query VideoPreparation($mediaId: ID!) { videoPreparation(mediaId: $mediaId) { stage sourceAvailable } }", { mediaId }, session.csrf);
    return result.videoPreparation?.stage === "READY" && result.videoPreparation.sourceAvailable;
  }, { timeout: 90_000, message: `o worker preparar o vídeo ${mediaId} e os quadros de contexto` }).toBe(true);
}

export async function createPhoto(session: CaptureSession, replacesMediaId?: string): Promise<{ mediaId: string; requirementKey: string }> {
  const requirement = session.bootstrap.requirements.find((candidate) => candidate.section.toUpperCase().includes("AMBIENTE")) ?? session.bootstrap.requirements[0];
  if (!requirement) throw new Error("O seed não criou requisito de foto para a vistoria.");
  const fixture = mediaFixture("photo");
  try {
    const mediaId = await uploadMedia(session, requirement.key, fixture, undefined, replacesMediaId);
    await waitMediaReady(session, mediaId);
    return { mediaId, requirementKey: requirement.key };
  } finally { fixture.cleanup(); }
}

export async function createVideo(session: CaptureSession): Promise<{ mediaId: string; attemptId: string; requirementKey: string }> {
  const requirement = session.bootstrap.requirements.find((candidate) => candidate.key.toLowerCase().includes("video")) ?? session.bootstrap.requirements[0];
  if (!requirement) throw new Error("O seed não criou requisito de vídeo para a vistoria.");
  const begun = await graphql<{ beginVideoCapture: { attempt: { id: string } | null; userErrors: Array<{ message: string }> } }>(session.request,
    "mutation BeginVideoCapture($input: BeginVideoCaptureInput!) { beginVideoCapture(input: $input) { attempt { id } userErrors { message } } }",
    { input: { acquisitionSource: "CAMERA", requirementKey: requirement.key, replacesMediaId: null, clientMutationId: randomUUID() } }, session.csrf);
  if (begun.beginVideoCapture.userErrors.length || !begun.beginVideoCapture.attempt) throw new Error(begun.beginVideoCapture.userErrors[0]?.message ?? "A API não iniciou a tentativa de vídeo.");
  const fixture = mediaFixture("video");
  try {
    const mediaId = await uploadMedia(session, requirement.key, fixture, begun.beginVideoCapture.attempt.id);
    await waitMediaReady(session, mediaId);
    await waitVideoReady(session, mediaId);
    return { mediaId, attemptId: begun.beginVideoCapture.attempt.id, requirementKey: requirement.key };
  } finally { fixture.cleanup(); }
}

export async function createVoiceNote(session: CaptureSession, anchor: { requirementKey: string; photoMediaId?: string; videoAttemptId?: string; startMs?: number; endMs?: number }, id = randomUUID(), options: { waitForTranscription?: boolean } = {}): Promise<StackNote> {
  const fixture = mediaFixture("audio");
  try {
    const bytes = readFileSync(fixture.path);
    const created = await graphql<{ createAudioNoteUpload: { noteId: string | null; upload: { url: string; method: string; headers: Record<string, string>; expiresAt: string } | null; userErrors: Array<{ message: string }> } }>(session.request,
      "mutation CreateAudioNoteUpload($input: CreateAudioNoteUploadInput!) { createAudioNoteUpload(input: $input) { noteId upload { url method headers expiresAt } userErrors { message } } }",
      { input: { clientNoteId: id, requirementKey: anchor.requirementKey, photoMediaId: anchor.photoMediaId ?? null, videoAttemptId: anchor.videoAttemptId ?? null, startMs: anchor.startMs ?? null, endMs: anchor.endMs ?? null, timeBasis: anchor.videoAttemptId ? "CAPTURE_CLOCK" : null, consentVersion: "voice-v1", disclosureVersion: session.bootstrap.disclosureVersion, contentType: fixture.contentType, contentSha256: createHash("sha256").update(bytes).digest("hex"), sizeBytes: bytes.length, durationMs: fixture.durationMs, clientMutationId: `${id}:create` } }, session.csrf);
    if (created.createAudioNoteUpload.userErrors.length || !created.createAudioNoteUpload.noteId || !created.createAudioNoteUpload.upload) throw new Error(created.createAudioNoteUpload.userErrors[0]?.message ?? "A API não iniciou o envio da anotação de voz.");
    const intent = created.createAudioNoteUpload.upload;
    const proof = intent.headers["x-upload-proof"] ?? intent.headers["X-Upload-Proof"];
    const put = await session.request.fetch(intent.url, { method: intent.method || "PUT", data: bytes, headers: intent.headers });
    if (!put.ok() || !proof) throw new Error("O armazenamento não recebeu o áudio WebM/Opus de QA.");
    const completed = await graphql<{ completeAudioNoteUpload: { note: { id: string; status: string; version: number } | null; userErrors: Array<{ message: string }> } }>(session.request,
      "mutation CompleteAudioNoteUpload($input: CompleteAudioNoteUploadInput!) { completeAudioNoteUpload(input: $input) { note { id status version } userErrors { message } } }",
      { input: { noteId: created.createAudioNoteUpload.noteId, uploadProof: proof, clientMutationId: `${id}:complete` } }, session.csrf);
    if (completed.completeAudioNoteUpload.userErrors.length || !completed.completeAudioNoteUpload.note) throw new Error(completed.completeAudioNoteUpload.userErrors[0]?.message ?? "A API não confirmou a nota.");
    if (options.waitForTranscription === false) return { id: completed.completeAudioNoteUpload.note.id, inspectionId: session.inspectionId, currentRevisionId: "", text: "", version: completed.completeAudioNoteUpload.note.version, environmentId: "", photoMediaId: anchor.photoMediaId, videoAttemptId: anchor.videoAttemptId, startMs: anchor.startMs, endMs: anchor.endMs };
    return await waitForTranscription(session, completed.completeAudioNoteUpload.note.id);
  } finally { fixture.cleanup(); }
}

export async function expectVoiceNoteIneligible(session: CaptureSession, anchor: { requirementKey: string; videoAttemptId: string }): Promise<{ code: string; message: string }> {
  const result = await graphql<{ createAudioNoteUpload: { userErrors: Array<{ code: string; message: string }> } }>(session.request,
    "mutation CreateAudioNoteUpload($input: CreateAudioNoteUploadInput!) { createAudioNoteUpload(input: $input) { userErrors { code message } } }",
    { input: { clientNoteId: randomUUID(), requirementKey: anchor.requirementKey, videoAttemptId: anchor.videoAttemptId, startMs: 500, endMs: 1500, timeBasis: "CAPTURE_CLOCK", consentVersion: "voice-v1", disclosureVersion: session.bootstrap.disclosureVersion, contentType: "audio/webm;codecs=opus", contentSha256: "a".repeat(64), sizeBytes: 1024, durationMs: 1000, clientMutationId: randomUUID() } }, session.csrf);
  const userError = result.createAudioNoteUpload.userErrors[0];
  if (!userError) throw new Error("A API aceitou uma anotação de voz para esta vistoria inelegível.");
  return userError;
}

export async function readVoiceNote(session: CaptureSession, noteId: string): Promise<StackNote> {
  const result = await graphql<{ audioNote: (StackNote & { revisions: Array<{ id: string; text: string }> }) | null }>(session.request,
    "query AudioNote($id: ID!) { audioNote(id: $id) { id inspectionId environmentId sourceMediaId photoMediaId currentRevisionId version revisions { id text } } }", { id: noteId }, session.csrf);
  const note = result.audioNote;
  const revision = note?.revisions.find((candidate) => candidate.id === note.currentRevisionId);
  if (!note || !revision) throw new Error(`A nota ${noteId} não tem uma revisão atual acessível.`);
  return { ...note, text: revision.text };
}

export async function submitCapture(session: CaptureSession): Promise<void> {
  const result = await graphql<{ submitCapture: { userErrors: Array<{ message: string }>; submission: unknown } }>(session.request,
    "mutation SubmitCapture($input: SubmitCaptureInput!) { submitCapture(input: $input) { submission { id complete } userErrors { message } } }",
    { input: { confirmIncomplete: true, clientMutationId: randomUUID() } }, session.csrf);
  if (result.submitCapture.userErrors.length || !result.submitCapture.submission) throw new Error(result.submitCapture.userErrors[0]?.message ?? "A captura não foi concluída.");
}

/** Advances only the clock for a source created by this QA spec. */
export function advanceVideoSourceExpiryClock(mediaId: string): void {
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(mediaId)) throw new Error("ID de mídia de QA inválido.");
  execFileSync("docker", ["exec", "inspection-postgres", "psql", "-U", "postgres", "-d", "inspection", "-v", "ON_ERROR_STOP=1", "-c", `UPDATE media.video_sources SET source_expiry_at = now() - interval '1 minute' WHERE media_id = '${mediaId}'`], { stdio: "pipe" });
}

function videoSourceStatus(mediaId: string): string {
  return execFileSync("docker", ["exec", "inspection-postgres", "psql", "-U", "postgres", "-d", "inspection", "-Atq", "-c", `SELECT status FROM media.video_sources WHERE media_id = '${mediaId}'`], { encoding: "utf8" }).trim();
}

export async function waitForVideoRetention(session: CaptureSession, noteId: string, mediaId: string): Promise<void> {
  await expect.poll(async () => {
    const result = await graphql<{ audioNote: { anchor: { mediaStatus: string; frames: Array<{ status: string }> } } | null }>(session.request,
      "query AudioNote($id: ID!) { audioNote(id: $id) { anchor { mediaStatus frames { status } } } }", { id: noteId }, session.csrf);
    const anchor = result.audioNote?.anchor;
    return videoSourceStatus(mediaId) === "EXPIRED" && Boolean(anchor && ["UNAVAILABLE", "PURGED"].includes(anchor.mediaStatus) && anchor.frames.length > 0 && anchor.frames.every((frame) => ["AVAILABLE", "EXPIRED"].includes(frame.status)));
  }, { timeout: 120_000, message: "o scheduler expirar a fonte e preservar os quadros de contexto" }).toBe(true);
}

async function waitForTranscription(session: CaptureSession, noteId: string): Promise<StackNote> {
  let note: StackNote | undefined;
  await expect.poll(async () => {
    const result = await graphql<{ audioNote: (StackNote & { status: string; revisions: Array<{ id: string; text: string }> }) | null }>(session.request,
      "query AudioNote($id: ID!) { audioNote(id: $id) { id inspectionId environmentId sourceMediaId currentRevisionId version status revisions { id text } } }", { id: noteId }, session.csrf);
    const current = result.audioNote;
    if (!current || current.status !== "COMPLETED" || !current.currentRevisionId) return "";
    const revision = current.revisions.find((item) => item.id === current.currentRevisionId);
    if (!revision) return "";
    note = { ...current, text: revision.text };
    return revision.text;
  }, { timeout: 60_000, message: `a transcrição da nota ${noteId} ficar pronta` }).not.toBe("");
  if (!note) throw new Error(`A nota ${noteId} ficou pronta sem revisão atual.`);
  return note;
}

export async function installTranscriptionTextMapping(text: string, delayMs = 0): Promise<CatalogMapping> {
  const mapping = {
    persistent: false,
    priority: 1,
    request: { method: "POST", urlPath: "/v1/chat/completions", bodyPatterns: [
      { matchesJsonPath: { expression: "$.metadata.comparison_mode", equalTo: "TRANSCRIPTION" } },
      { matchesJsonPath: "$.messages[1].content[?(@.type == 'input_audio')]" },
    ] },
    response: { status: 200, fixedDelayMilliseconds: delayMs, headers: { "Content-Type": "application/json", "X-LiteLLM-Response-Cost": "0.0001" }, jsonBody: { id: "qa-transcription", model: "stub/transcription", provider: "stub", choices: [{ message: { content: JSON.stringify({ transcript: text, noSpeech: false }) } }], usage: { prompt_tokens: 10, completion_tokens: 4 }, cost: 0.0001 } },
  };
  const response = await fetch(`${wiremockURL}/__admin/mappings`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(mapping) });
  if (!response.ok) throw new Error(`WireMock não registrou o mapping de transcrição (${response.status}).`);
  const { id } = await response.json() as { id: string };
  return { id, remove: async () => { await fetch(`${wiremockURL}/__admin/mappings/${id}`, { method: "DELETE" }); } };
}

export async function installCatalogMapping(note: StackNote, options: { visualMediaId?: string; visibleIdentity?: boolean; quantityConflict?: boolean } = {}): Promise<CatalogMapping> {
  const item = {
    typeCode: "OTHER", name: "Pratos", material: null, color: "branco", quantity: 5,
    condition: "GOOD", observation: options.visibleIdentity ? "OBSERVED" : "NOT_ASSESSABLE",
    limitation: options.visibleIdentity ? null : "Quantidade informada por voz; identidade visual não confirmada.", confidenceBps: 8700,
    evidence: options.visibleIdentity && options.visualMediaId ? [{ mediaId: options.visualMediaId, frameId: null }] : [],
    voice: [{ noteId: "{{citation.0}}", revisionId: "{{citation.1}}", attributes: options.visibleIdentity ? ["quantity"] : ["name", "quantity"] }],
    conflicts: options.quantityConflict ? [{ attribute: "quantity", visualValue: "4" }] : [],
  };
  const output = JSON.stringify({ environments: [{ typeCode: "KITCHEN", name: "Cozinha", items: [item] }] });
  const citationRegex = "noteId.{0,20}([0-9a-f-]{36}).{0,20}revisionId.{0,20}([0-9a-f-]{36})";
  const responseBody = `{{regexExtract request.body '${citationRegex}' 'citation'}}` + JSON.stringify({
    id: "{{jsonPath request.body '$.metadata.comparison_mode'}}", model: "stub/inventory", provider: "stub",
    choices: [{ message: { content: output } }], usage: { prompt_tokens: 18, completion_tokens: 12 }, cost: 0.0002,
  });
  const mapping = {
    persistent: false,
    priority: 1,
    request: { method: "POST", urlPath: "/v1/chat/completions", bodyPatterns: [
      { matchesJsonPath: { expression: "$.metadata.comparison_mode", equalTo: "INVENTORY_CATALOG" } },
      { contains: note.id }, { contains: note.currentRevisionId },
    ] },
    response: { status: 200, transformers: ["response-template"], headers: { "Content-Type": "application/json", "X-LiteLLM-Response-Cost": "0.0002" }, body: responseBody },
  };
  const response = await fetch(`${wiremockURL}/__admin/mappings`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(mapping) });
  if (!response.ok) throw new Error(`WireMock não registrou o mapping de catálogo (${response.status}).`);
  const { id } = await response.json() as { id: string };
  return { id, remove: async () => { await fetch(`${wiremockURL}/__admin/mappings/${id}`, { method: "DELETE" }); } };
}

export async function installCatalogItemsMapping(notes: StackNote[], items: Array<{ name: string; quantity: number; noteIndexes: number[] }>): Promise<CatalogMapping> {
  if (!notes.length || items.some((item) => !item.noteIndexes.length || item.noteIndexes.some((index) => !notes[index]?.currentRevisionId))) {
    throw new Error("O catálogo de QA precisa citar revisões prontas de notas enviadas.");
  }
  const templates = notes.map((_, index) => {
    const preceding = Array.from({ length: index }, () => "(?:noteId.{0,20}[0-9a-f-]{36}.{0,20}revisionId.{0,20}[0-9a-f-]{36}.{0,500})").join("");
    const pattern = `${preceding}noteId.{0,20}([0-9a-f-]{36}).{0,20}revisionId.{0,20}([0-9a-f-]{36})`;
    return `{{regexExtract request.body '${pattern}' 'citation${index}'}}`;
  }).join("");
  const catalogItems = items.map((item) => ({
    typeCode: "OTHER", name: item.name, material: null, color: "branco", quantity: item.quantity,
    condition: "GOOD", observation: "NOT_ASSESSABLE", limitation: "Item declarado por voz.", confidenceBps: 8700,
    evidence: [], voice: item.noteIndexes.map((index) => ({ noteId: `{{citation${index}.0}}`, revisionId: `{{citation${index}.1}}`, attributes: ["name", "quantity"] })), conflicts: [],
  }));
  const output = JSON.stringify({ environments: [{ typeCode: "KITCHEN", name: "Cozinha", items: catalogItems }] });
  const responseBody = templates + JSON.stringify({
    id: "{{jsonPath request.body '$.metadata.comparison_mode'}}", model: "stub/inventory", provider: "stub",
    choices: [{ message: { content: output } }], usage: { prompt_tokens: 18, completion_tokens: 12 }, cost: 0.0002,
  });
  const mapping = {
    persistent: false,
    priority: 1,
    request: { method: "POST", urlPath: "/v1/chat/completions", bodyPatterns: [
      { matchesJsonPath: { expression: "$.metadata.comparison_mode", equalTo: "INVENTORY_CATALOG" } },
      ...notes.flatMap((note) => [{ contains: note.id }, { contains: note.currentRevisionId }]),
    ] },
    response: { status: 200, transformers: ["response-template"], headers: { "Content-Type": "application/json", "X-LiteLLM-Response-Cost": "0.0002" }, body: responseBody },
  };
  const response = await fetch(`${wiremockURL}/__admin/mappings`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(mapping) });
  if (!response.ok) throw new Error(`WireMock não registrou o mapping múltiplo de catálogo (${response.status}).`);
  const { id } = await response.json() as { id: string };
  return { id, remove: async () => { await fetch(`${wiremockURL}/__admin/mappings/${id}`, { method: "DELETE" }); } };
}

export async function validateCatalogMapping(mapping: CatalogMapping, notes: StackNote | StackNote[]): Promise<void> {
  const citations = Array.isArray(notes) ? notes : [notes];
  const voiceNotes = citations.map((note) => ({ noteId: note.id, revisionId: note.currentRevisionId }));
  const syntheticBody = { model: "stub/vision", messages: [{ role: "system", content: "QA" }, { role: "user", content: [{ type: "text", text: `Dados JSON: ${JSON.stringify({ voiceNotes })}` }, { type: "text", text: `evidenceId=${randomUUID()}; role=CURRENT` }] }], metadata: { comparison_mode: "INVENTORY_CATALOG" }, response_format: { type: "json_schema" } };
  const response = await fetch(`${wiremockURL}/v1/chat/completions`, { method: "POST", headers: { "Content-Type": "application/json", "X-Request-Id": `catalog-e2e-${mapping.id}` }, body: JSON.stringify(syntheticBody) });
  if (!response.ok) throw new Error(`Mapping de catálogo WireMock respondeu ${response.status}.`);
  const payload = await response.json() as { choices?: Array<{ message?: { content?: string } }> };
  const content = payload.choices?.[0]?.message?.content ?? "";
  const result = JSON.parse(content) as { environments?: Array<{ items?: Array<{ voice?: Array<{ noteId: string; revisionId: string; attributes: string[] }> }> }> };
  const returnedCitations = result.environments?.flatMap((environment) => environment.items ?? []).flatMap((catalogItem) => catalogItem.voice ?? []) ?? [];
  expect(returnedCitations).toEqual(expect.arrayContaining(citations.map((note) => ({ noteId: note.id, revisionId: note.currentRevisionId, attributes: expect.any(Array) }))));
}

export async function promoteFromDashboard(page: Page): Promise<void> {
  const promotion = page.getByRole("region", { name: "Promoção da vistoria para origem" });
  const promote = promotion.getByRole("button", { name: "Usar esta vistoria como origem" });
  await expect(promote).toBeVisible({ timeout: 90_000 });
  await promote.click();
  const dialog = page.getByRole("dialog", { name: "Usar esta vistoria como origem" });
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: /Usar como origem|Confirmar substituição/ }).click();
}

/** Opens a capture-generated draft, waiting for the inventory worker to finish preparing it. */
export async function openCaptureDraft(page: Page, inspectionId: string): Promise<void> {
  await loginAsLocalAdmin(page, `/baselines/review/inspection/${encodeURIComponent(inspectionId)}`);
  const reviewDraft = page.getByRole("button", { name: "Revisar inventário", exact: true });
  const retryDraft = page.getByRole("button", { name: "Abrir rascunho", exact: true });
  await expect(reviewDraft.or(retryDraft)).toBeVisible({ timeout: 120_000 });
  await reviewDraft.or(retryDraft).click();
  await expect(page.getByRole("heading", { name: "Revisar inventário" })).toBeVisible();
}

/** Removes a leftover QA draft so the next submitted capture can create its own draft. */
export async function clearPendingDashboardDraft(page: Page, inspectionId: string): Promise<void> {
  const versionsResponse = page.waitForResponse((response) => Boolean(response.url().endsWith("/graphql") && response.request().postData()?.includes("DashboardBaselineVersions")));
  await loginAsLocalAdmin(page, `/baselines/review/inspection/${encodeURIComponent(inspectionId)}`);
  const response = await versionsResponse;
  const payload = await response.json() as { data?: { baselineVersions?: { nodes?: Array<{ id: string; status: string }> } } };
  const draftId = payload.data?.baselineVersions?.nodes?.find((version) => version.status === "DRAFT")?.id;
  if (!draftId) return;
  await loginAsLocalAdmin(page, `/baselines/review/${encodeURIComponent(draftId)}`);
  await expect(page.getByRole("heading", { name: "Revisar inventário" })).toBeVisible();
  const discardButton = page.getByRole("button", { name: "Descartar rascunho", exact: true });
  await expect.poll(async () => {
    if (await discardButton.isVisible()) return true;
    const refresh = page.getByRole("button", { name: "Atualizar", exact: true });
    if (await refresh.isVisible()) await refresh.click();
    return discardButton.isVisible();
  }, { timeout: 120_000, intervals: [2_000, 3_000, 5_000], message: "rascunho pendente ficar pronto para descarte" }).toBe(true);
  await discardButton.click();
  const dialog = page.getByRole("dialog", { name: "Descartar rascunho" });
  await dialog.getByRole("button", { name: "Descartar rascunho", exact: true }).click();
  await expect(dialog).toHaveCount(0);
}

export function promotionStatus(page: Page) {
  return page.getByRole("region", { name: "Promoção da vistoria para origem" }).getByText(/Versão \d+ criada e ativa\./);
}

export async function wiremockAdminDelete(mapping: CatalogMapping | undefined): Promise<void> {
  if (mapping) await mapping.remove();
}

