import { expect, type Page } from "@playwright/test";
import { deflateSync } from "node:zlib";

/** Replaces MediaRecorder with a deterministic recorder so audio notes need no real microphone. */
export async function installFakeMediaRecorder(page: Page): Promise<void> {
  await page.addInitScript(() => {
    class FakeMediaRecorder {
      static isTypeSupported() { return true; }
      state: "inactive" | "recording" = "inactive";
      mimeType: string;
      ondataavailable?: (event: { data: Blob }) => void;
      onstop?: () => void;
      onerror?: () => void;
      private interval?: number;
      constructor(_stream: MediaStream, options?: MediaRecorderOptions) { this.mimeType = options?.mimeType ?? "audio/webm"; }
      start() {
        this.state = "recording";
        this.interval = window.setInterval(() => this.ondataavailable?.({ data: new Blob([new Uint8Array(512)], { type: this.mimeType }) }), 100);
      }
      stop() {
        if (this.interval) clearInterval(this.interval);
        this.ondataavailable?.({ data: new Blob([new Uint8Array(128)], { type: this.mimeType }) });
        this.state = "inactive";
        this.onstop?.();
      }
    }
    Object.defineProperty(window, "MediaRecorder", { configurable: true, value: FakeMediaRecorder });
  });
}

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

/** A small textured PNG; the seed makes each photo distinct so duplicates are not rejected. */
export function photoFixture(seed: string): Buffer {
  const width = 64;
  const height = 64;
  const pixels = Buffer.alloc(height * (1 + width * 3));
  const seedValue = [...seed].reduce((value, char) => value + char.charCodeAt(0), 0);
  for (let y = 0; y < height; y += 1) {
    const row = y * (1 + width * 3);
    for (let x = 0; x < width; x += 1) {
      const texture = ((x * 17 + y * 31 + seedValue * 13) % 7) < 3;
      const value = texture ? 110 + seedValue % 20 : 220 - seedValue % 20;
      const offset = row + 1 + x * 3;
      pixels[offset] = value;
      pixels[offset + 1] = Math.min(255, value + (x % 3) * 4);
      pixels[offset + 2] = Math.max(0, value - (y % 3) * 4);
    }
  }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8;
  header[9] = 2;
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), pngChunk("IHDR", header), pngChunk("IDAT", deflateSync(pixels)), pngChunk("IEND", Buffer.alloc(0))]);
}

type Revision = { id: string; revisionNumber: number; text: string; kind: string; createdAt: string };
export type FakeNote = {
  id: string; clientNoteId: string; photoMediaId: string | null; requirementKey: string; status: string; version: number; highlighted: boolean; textConfirmed: boolean;
  revisions: Revision[]; currentRevisionId: string | null; durationMs: number;
};
type Input = Record<string, unknown>;

/** A stateful stand-in for the Capture GraphQL API covering the photo journey and every audio-note operation. */
export class VoiceBackend {
  offline = false;
  voicePolicyEnabled = true;
  notes = new Map<string, FakeNote>();
  byClientId = new Map<string, string>();
  calls = { accept: [] as Input[], create: [] as Input[], upload: 0, complete: [] as Input[], revise: [] as Input[], discard: [] as Input[], highlight: [] as Input[], confirm: [] as Input[], submit: 0 };
  /** Rejects the next note admission with this stable code, once. */
  rejectNextCreate?: { code: string; message: string };
  private mediaSequence = 0;
  private noteSequence = 0;
  private external: Record<string, unknown>;
  textFor = (index: number): string => ["cinco pratos", "seis pratos", "dois copos", "três facas"][index - 1] ?? `anotação ${index}`;

  constructor(private readonly room = { key: "env:living", name: "Sala" }) {
    this.external = {
      responsibilityId: "voice-photo-responsibility", recaptureRequestId: null, status: "OPEN", confirmationOnly: false, kind: "INSPECTION", mediaKind: "PHOTO",
      disclosureVersion: "pt-BR-1", captureLayout: "ENVIRONMENTS", reference: { comparisonMode: "CHECKLIST_ONLY" }, referenceItems: [],
      policy: { allowGallery: true, gpsRequired: false, maxDurationSeconds: 900 },
      requirements: [{ key: room.key, section: room.name, label: room.name, instructions: room.name, required: true, minimumMedia: 1, maximumMedia: 20, descriptionRequired: false, captureSourcePolicy: "CAMERA_DEFAULT", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: true, environmentIdentityId: `identity-${room.key}`, environmentTypeCode: "LIVING_ROOM", environmentName: room.name, expected: true, addedDuringCapture: false }],
      environments: [{ requirementKey: room.key, identityId: `identity-${room.key}`, typeCode: "LIVING_ROOM", name: room.name, expected: true, addedDuringCapture: false, mediaCount: 0, status: "PENDING" }],
      reviewHandoff: null, answers: []
    };
  }

  /** Notes that remain usable for catalog suggestions: transcribed, not discarded. */
  eligibleNotes(): FakeNote[] { return [...this.notes.values()].filter((note) => note.status === "COMPLETED"); }

  private serverNote(note: FakeNote) {
    return {
      id: note.id, environmentId: "environment-1", requirementKey: note.requirementKey, targetKind: "PHOTO", photoMediaId: note.photoMediaId, videoAttemptId: null, startMs: null, endMs: null, durationMs: note.durationMs,
      status: note.status, anchorStatus: "VALID", highlighted: note.highlighted, version: note.version, currentRevisionId: note.currentRevisionId, textConfirmed: note.textConfirmed, noSpeech: false, failureCode: null,
      audioAvailability: note.status === "QUEUED" ? "AVAILABLE" : "DELETED", audioDeletionStatus: note.status === "QUEUED" ? "PENDING" : "DELETED", revisions: note.revisions
    };
  }

  async install(page: Page): Promise<void> {
    await page.route("**/photo-part", (route) => this.offline ? route.abort("internetdisconnected") : route.fulfill({ status: 200, headers: { etag: `photo-part-${this.mediaSequence}` } }));
    await page.route("**/upload/audio", (route) => this.offline ? route.abort("internetdisconnected") : (this.calls.upload += 1, route.fulfill({ status: 200 })));
    await page.route("**/graphql", async (route) => {
      if (this.offline) return route.abort("internetdisconnected");
      const { query = "", variables = {} } = JSON.parse(route.request().postData() ?? "{}") as { query?: string; variables?: { input?: Input; id?: string } };
      const input = variables.input ?? {};
      const respond = (data: unknown) => route.fulfill({ json: { data } });
      const external = this.external as { answers: Array<{ requirementKey: string; mediaIds: string[]; impossibilityReason: string | null; version: number }>; environments: Array<Record<string, unknown>> };
      const echo = { clientMutationId: String(input.clientMutationId ?? ""), userErrors: [] as unknown[] };
      const failure = (code: string, message: string) => ({ userErrors: [{ code, field: null, message, correlationId: null }], clientMutationId: String(input.clientMutationId ?? "") });
      if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
      if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "capture-csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
      if (query.includes("CaptureVoicePolicy")) return respond({ voicePolicy: this.voicePolicyEnabled ? { enabled: true, tenantNoteLimit: 100, tenantAudioLimitMs: 18_000_000, inspectionNoteLimit: 20, inspectionAudioLimitMs: 1_800_000, version: 1 } : { enabled: false, tenantNoteLimit: null, tenantAudioLimitMs: null, inspectionNoteLimit: null, inspectionAudioLimitMs: null, version: 1 } });
      if (query.includes("externalCapture")) return respond({ externalCapture: this.external });
      if (query.includes("acceptProcessing")) { this.calls.accept.push(input); return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } }); }
      if (query.includes("recordCaptureQualityWarning")) return respond({ recordCaptureQualityWarning: { answer: null, userErrors: [] } });
      if (query.includes("createMediaUpload")) { this.mediaSequence += 1; return respond({ createMediaUpload: { upload: { mediaId: `photo-${this.mediaSequence}`, uploadId: `upload-${this.mediaSequence}`, expiresAt: "2099-01-01T00:00:00Z", partSizeBytes: 1024 * 1024 }, userErrors: [] } }); }
      if (query.includes("presignMediaParts")) return respond({ presignMediaParts: { parts: [{ partNumber: 1, url: new URL("/photo-part", page.url()).href, expiresAt: "2099-01-01T00:00:00Z" }], userErrors: [] } });
      if (query.includes("completeMediaUpload")) return respond({ completeMediaUpload: { media: { id: `photo-${this.mediaSequence}`, status: "VERIFIED" }, userErrors: [] } });
      if (query.includes("saveCaptureMetadata")) {
        const key = String(input.requirementKey); const id = String(input.mediaId ?? `photo-${this.mediaSequence}`);
        const existing = external.answers.find((answer) => answer.requirementKey === key);
        external.answers = [...external.answers.filter((answer) => answer.requirementKey !== key), { requirementKey: key, mediaIds: [...new Set([...(existing?.mediaIds ?? []), id])], impossibilityReason: null, version: 1 }];
        return respond({ saveCaptureMetadata: { media: { id, status: "READY", replacesMediaId: null }, userErrors: [] } });
      }
      if (query.includes("submitCapture")) { this.calls.submit += 1; this.external.status = "COMPLETED"; return respond({ submitCapture: { submission: { id: "submission", complete: true, requiresAttention: false, submittedAt: "2026-11-01T00:00:00Z" }, userErrors: [] } }); }

      if (query.includes("createAudioNoteUpload")) {
        this.calls.create.push(input);
        if (this.rejectNextCreate) { const rejected = this.rejectNextCreate; this.rejectNextCreate = undefined; return respond({ createAudioNoteUpload: { noteId: null, upload: null, ...failure(rejected.code, rejected.message) } }); }
        const clientNoteId = String(input.clientNoteId);
        let noteId = this.byClientId.get(clientNoteId);
        if (!noteId) {
          this.noteSequence += 1; noteId = `audio-note-${this.noteSequence}`;
          this.byClientId.set(clientNoteId, noteId);
          this.notes.set(noteId, { id: noteId, clientNoteId, photoMediaId: (input.photoMediaId as string | null) ?? null, requirementKey: String(input.requirementKey), status: "UPLOADING", version: 1, highlighted: false, textConfirmed: false, revisions: [], currentRevisionId: null, durationMs: Number(input.durationMs ?? 0) });
        }
        return respond({ createAudioNoteUpload: { noteId, upload: { url: new URL("/upload/audio", page.url()).href, method: "PUT", headers: { "x-upload-proof": `proof-${noteId}` }, expiresAt: "2099-01-01T00:00:00Z" }, ...echo } });
      }
      if (query.includes("completeAudioNoteUpload")) {
        this.calls.complete.push(input);
        const note = this.notes.get(String(input.noteId));
        if (!note) return respond({ completeAudioNoteUpload: { note: null, ...failure("NOT_FOUND", "A nota de áudio não foi encontrada.") } });
        if (note.status === "UPLOADING") note.status = "QUEUED";
        return respond({ completeAudioNoteUpload: { note: { id: note.id, status: note.status, version: note.version, audioDeletionStatus: "PENDING" }, ...echo } });
      }
      if (query.includes("reviseAudioNote")) {
        this.calls.revise.push(input);
        const note = this.notes.get(String(input.noteId));
        if (!note) return respond({ reviseAudioNote: { note: null, ...failure("NOT_FOUND", "A nota de áudio não foi encontrada.") } });
        if (note.version !== input.expectedVersion) return respond({ reviseAudioNote: { note: null, ...failure("CONFLICT", "A nota foi alterada. Atualize os dados e tente novamente.") } });
        const revision = { id: `${note.id}-r${note.revisions.length + 1}`, revisionNumber: note.revisions.length + 1, text: String(input.text), kind: String(input.revisionKind ?? "HUMAN_EDIT"), createdAt: "2026-11-01T00:00:00Z" };
        note.revisions.push(revision); note.currentRevisionId = revision.id; note.version += 1;
        return respond({ reviseAudioNote: { note: this.serverNote(note), ...echo } });
      }
      if (query.includes("discardAudioNote")) {
        this.calls.discard.push(input);
        const note = this.notes.get(String(input.noteId));
        if (!note) return respond({ discardAudioNote: { note: null, ...failure("NOT_FOUND", "A nota de áudio não foi encontrada.") } });
        if (note.status !== "CANCELED") { note.status = "CANCELED"; note.version += 1; }
        return respond({ discardAudioNote: { note: { id: note.id, status: note.status, version: note.version }, ...echo } });
      }
      if (query.includes("setAudioNoteHighlight")) {
        this.calls.highlight.push(input);
        const note = this.notes.get(String(input.noteId));
        if (!note) return respond({ setAudioNoteHighlight: { note: null, ...failure("NOT_FOUND", "A nota de áudio não foi encontrada.") } });
        note.highlighted = Boolean(input.highlighted); note.version += 1;
        return respond({ setAudioNoteHighlight: { note: { id: note.id, highlighted: note.highlighted, version: note.version }, ...echo } });
      }
      if (query.includes("confirmAudioNoteText")) {
        this.calls.confirm.push(input);
        const note = this.notes.get(String(input.noteId));
        if (!note) return respond({ confirmAudioNoteText: { note: null, ...failure("NOT_FOUND", "A nota de áudio não foi encontrada.") } });
        note.textConfirmed = true; note.version += 1;
        return respond({ confirmAudioNoteText: { note: { id: note.id, status: note.status, version: note.version }, ...echo } });
      }
      if (query.includes("audioNote(")) {
        const note = this.notes.get(String(variables.id));
        if (!note) return respond({ audioNote: null });
        if (note.status === "QUEUED") {
          // The worker "transcribes" on first read so the journey sees a processing state only transiently.
          const revision = { id: `${note.id}-r1`, revisionNumber: 1, text: this.textFor(this.noteSequenceOf(note.id)), kind: "MODEL", createdAt: "2026-11-01T00:00:00Z" };
          note.revisions = [revision]; note.currentRevisionId = revision.id; note.status = "COMPLETED";
        }
        return respond({ audioNote: this.serverNote(note) });
      }
      throw new Error(`Operação inesperada: ${query.slice(0, 100)}`);
    });
  }

  private noteSequenceOf(noteId: string): number { return Number(noteId.replace("audio-note-", "")); }
}

/** Opens the capture, accepts the visual authorizations and, optionally, the separate voice one. */
export async function authenticate(page: Page, options: { voice: boolean; token?: string }): Promise<void> {
  await page.goto(`/capture/${options.token ?? "voice-photo-invite"}`);
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel(/Processamento das fotos|Processamento do vídeo e imagens derivadas/).check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  if (options.voice) await page.getByLabel("Permitir anotações de voz (opcional)").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
}

/** Opens the room and saves one photo through the file input, waiting until its upload finished. */
export async function savePhoto(page: Page, room: string, seed: string, expectedConcluded: number): Promise<void> {
  if (!(await page.getByLabel("Tirar foto").isVisible().catch(() => false))) await page.getByRole("button", { name: new RegExp(room) }).first().click();
  await page.getByLabel("Tirar foto").setInputFiles({ name: `${seed}.png`, mimeType: "image/png", buffer: photoFixture(seed) });
  await page.getByRole("button", { name: "Usar esta foto" }).click();
  await expect(page.getByText(new RegExp(`${expectedConcluded} concluído(?:s)? · 0 pendentes`))).toBeVisible();
}

export const voiceButton = (page: Page) => page.getByRole("button", { name: "Segure para falar ou pressione Enter ou Espaço", exact: true });
export const stopButton = (page: Page) => page.getByRole("button", { name: "Encerrar anotação de voz", exact: true });

/** Holds the voice control with the pointer for `holdMs` and releases it. */
export async function holdToSpeak(page: Page, holdMs = 750): Promise<void> {
  await voiceButton(page).hover();
  await page.mouse.down();
  await expect(stopButton(page)).toBeVisible();
  await page.waitForTimeout(holdMs);
  await page.mouse.up();
}

/** Reads local audio drafts straight from IndexedDB so tests can prove bytes are kept or removed. */
export async function localAudioRecords(page: Page): Promise<Array<{ id: string; state: string; hasBytes: boolean; byteLength: number; noteId?: string; target: { kind: string; photoDraftId?: string; mediaId?: string } }>> {
  return page.evaluate(() => new Promise((resolve, reject) => {
    const request = indexedDB.open("inspection-audio-notes-v1");
    request.onerror = () => reject(request.error);
    request.onsuccess = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains("audioDrafts")) { db.close(); resolve([]); return; }
      const all = db.transaction("audioDrafts").objectStore("audioDrafts").getAll();
      all.onsuccess = () => { db.close(); resolve((all.result as Array<Record<string, unknown>>).map((record) => { const bytes = record.audioBytes; return { id: String(record.id), state: String(record.state), hasBytes: bytes instanceof ArrayBuffer, byteLength: bytes instanceof ArrayBuffer ? bytes.byteLength : 0, noteId: record.noteId as string | undefined, target: record.target as { kind: string; photoDraftId?: string; mediaId?: string } }; })); };
      all.onerror = () => reject(all.error);
    };
  }));
}
