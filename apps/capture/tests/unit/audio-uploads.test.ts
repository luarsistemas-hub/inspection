import "fake-indexeddb/auto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { listAudioDrafts, saveAudioDraft, type AudioDraft } from "@/pwa/audio-drafts";
import { flushAudioDrafts, resumeAudioDrafts, uploadAudioDraft } from "@/pwa/audio-uploads";
import { saveDraft, type CaptureDraft } from "@/pwa/drafts";

const responsibilityId = "responsibility-1";
const audio = () => new Blob(["voice"], { type: "audio/webm" });
const note = (overrides: Partial<AudioDraft> = {}): AudioDraft => ({ id: "note-1", responsibilityId, requirementKey: "env:living", disclosureVersion: "pt-BR-1", consentVersion: "voice-v1", target: { kind: "PHOTO", photoDraftId: "photo-draft-1" }, durationMs: 800, contentType: "audio/webm", sizeBytes: 5, sha256: "c".repeat(64), state: "PENDING_UPLOAD", audio: audio(), createdAt: "2026-11-01T00:00:00Z", ...overrides });
const photo = (overrides: Partial<CaptureDraft> = {}): CaptureDraft => ({ id: "photo-draft-1", responsibilityId, blob: new Blob(["img"], { type: "image/png" }), sha256: "d".repeat(64), uploadId: "upload-1", mediaId: "photo-1", mediaStatus: "READY", metadataSaved: true, parts: [{ number: 1, etag: "etag", complete: true }], metadata: { requirementKey: "env:living", description: "", source: "camera", capturedAt: "2026-11-01T00:00:00Z" }, ...overrides });

type Calls = { create: Array<Record<string, unknown>>; complete: Array<Record<string, unknown>>; put: number; query: number };
/** A programmable fake of the GraphQL endpoint and the private object store. */
function server(options: { createFailure?: { code: string; message: string }; completeFailure?: { code: string; message: string }; putStatus?: number; offline?: boolean; existingStatus?: string } = {}) {
  const calls: Calls = { create: [], complete: [], put: 0, query: 0 };
  const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    if (options.offline) throw new TypeError("Failed to fetch");
    if (init?.method === "PUT") { calls.put += 1; return new Response(null, { status: options.putStatus ?? 200 }); }
    const body = JSON.parse(String(init?.body)) as { query: string; variables: { input?: Record<string, unknown> } };
    const ok = (data: unknown) => new Response(JSON.stringify({ data }), { status: 200 });
    if (body.query.includes("CreateAudioNoteUpload")) {
      calls.create.push(body.variables.input ?? {});
      if (options.createFailure) return ok({ createAudioNoteUpload: { noteId: null, upload: null, userErrors: [{ ...options.createFailure, field: null, correlationId: null }], clientMutationId: "x" } });
      return ok({ createAudioNoteUpload: { noteId: "server-note", upload: { url: "https://upload.invalid/audio", method: "PUT", headers: { "x-upload-proof": "proof" }, expiresAt: new Date(Date.now() + 60_000).toISOString() }, userErrors: [], clientMutationId: "x" } });
    }
    if (body.query.includes("CompleteAudioNoteUpload")) {
      calls.complete.push(body.variables.input ?? {});
      if (options.completeFailure) return ok({ completeAudioNoteUpload: { note: null, userErrors: [{ ...options.completeFailure, field: null, correlationId: null }], clientMutationId: "x" } });
      return ok({ completeAudioNoteUpload: { note: { id: "server-note", status: "QUEUED", version: 1, audioDeletionStatus: "PENDING" }, userErrors: [], clientMutationId: "x" } });
    }
    calls.query += 1;
    return ok({ audioNote: options.existingStatus ? { id: "server-note", status: options.existingStatus, version: 1, revisions: [] } : null });
  });
  vi.stubGlobal("fetch", fetchMock);
  return { calls, fetchMock };
}

describe("audio note upload", () => {
  beforeEach(async () => {
    for (const name of ["inspection-audio-notes-v1", "inspection-capture-v3"]) await new Promise<void>((resolve, reject) => { const request = indexedDB.deleteDatabase(name); request.onsuccess = () => resolve(); request.onerror = () => reject(request.error); });
  });
  afterEach(() => { vi.unstubAllGlobals(); });

  it("IT-071 uploads once the photo exists on the server and deletes local audio only after the receipt", async () => {
    const { calls } = server();
    await saveDraft(photo());
    await saveAudioDraft(note());
    const received = await uploadAudioDraft(note());
    expect(received).toMatchObject({ state: "RECEIVED", noteId: "server-note", audio: undefined });
    expect(calls.create[0]).toMatchObject({ clientNoteId: "note-1", photoMediaId: "photo-1", videoAttemptId: null, startMs: null, endMs: null, consentVersion: "voice-v1", clientMutationId: "note-1:create" });
    expect(calls.complete[0]).toMatchObject({ noteId: "server-note", uploadProof: "proof", clientMutationId: "note-1:complete" });
    const [stored] = await listAudioDrafts(responsibilityId);
    expect(stored).toMatchObject({ state: "RECEIVED", audio: undefined, target: { kind: "PHOTO", photoDraftId: "photo-draft-1", mediaId: "photo-1" } });
  });

  it("IT-052 waits locally, without any request, until its own photo is durable", async () => {
    const { fetchMock } = server();
    await saveDraft(photo({ mediaId: undefined, mediaStatus: undefined, metadataSaved: false, uploadId: undefined, parts: [] }));
    await saveAudioDraft(note());
    const waiting = await uploadAudioDraft(note());
    expect(waiting.state).toBe("PENDING_UPLOAD");
    expect(fetchMock).not.toHaveBeenCalled();
    expect((await listAudioDrafts(responsibilityId))[0].audio?.size).toBe(5);
  });

  it("IT-055 / IT-082 never moves a note to another photo when its photo was replaced or removed", async () => {
    const { fetchMock } = server();
    await saveDraft(photo({ id: "replacement-photo", mediaId: "photo-2" }));
    await saveAudioDraft(note());
    await expect(uploadAudioDraft(note())).rejects.toMatchObject({ code: "NOT_FOUND" });
    expect(fetchMock).not.toHaveBeenCalled();
    const [stored] = await listAudioDrafts(responsibilityId);
    expect(stored).toMatchObject({ state: "UNSENDABLE", failureCode: "NOT_FOUND" });
    expect(stored.statusMessage).toMatch(/substituída ou removida.*não será anexada a outra foto/);
  });

  it("IT-040 / IT-053 / IT-078 shares one upload between concurrent triggers", async () => {
    const { calls } = server();
    await saveDraft(photo());
    await saveAudioDraft(note());
    const [first, second, third] = await Promise.all([uploadAudioDraft(note()), uploadAudioDraft(note()), resumeAudioDrafts([note()])]);
    expect(first).toBe(second);
    expect(third[0].draft?.state).toBe("RECEIVED");
    expect(calls.create).toHaveLength(1);
    expect(calls.complete).toHaveLength(1);
    expect(calls.put).toBe(1);
  });

  it("IT-079 keeps the note pending, with its anchor, when the connection drops mid-upload", async () => {
    server({ offline: true });
    await saveDraft(photo());
    await saveAudioDraft(note({ target: { kind: "VIDEO", attemptId: "attempt-1", startMs: 1000, endMs: 1800, timeBasis: "CAPTURE_CLOCK" } }));
    await expect(uploadAudioDraft(note())).rejects.toBeDefined();
    const [stored] = await listAudioDrafts(responsibilityId);
    expect(stored).toMatchObject({ state: "FAILED", target: { kind: "VIDEO", attemptId: "attempt-1", startMs: 1000, endMs: 1800 } });
    expect(stored.audio?.size).toBe(5);
    const { calls } = server();
    const retried = await uploadAudioDraft(stored);
    expect(retried).toMatchObject({ state: "RECEIVED", target: { kind: "VIDEO", attemptId: "attempt-1", startMs: 1000, endMs: 1800 } });
    expect(calls.create[0]).toMatchObject({ videoAttemptId: "attempt-1", startMs: 1000, endMs: 1800, timeBasis: "CAPTURE_CLOCK", photoMediaId: null });
  });

  it("IT-079 keeps the bytes when the object store rejects the upload", async () => {
    const { calls } = server({ putStatus: 500 });
    await saveDraft(photo());
    await saveAudioDraft(note());
    await expect(uploadAudioDraft(note())).rejects.toMatchObject({ code: "UPLOAD_FAILED" });
    expect(calls.complete).toHaveLength(0);
    expect((await listAudioDrafts(responsibilityId))[0]).toMatchObject({ state: "FAILED" });
  });

  it("IT-022 / IT-076 treats an exhausted allowance as final, keeps the evidence, and stops retrying", async () => {
    const { calls } = server({ createFailure: { code: "LIMIT_REACHED", message: "O limite de uso de voz foi atingido." } });
    await saveDraft(photo());
    await saveAudioDraft(note());
    await expect(uploadAudioDraft(note())).rejects.toMatchObject({ code: "LIMIT_REACHED" });
    const [stored] = await listAudioDrafts(responsibilityId);
    expect(stored).toMatchObject({ state: "UNSENDABLE", failureCode: "LIMIT_REACHED" });
    expect(stored.audio?.size).toBe(5);
    expect(stored.statusMessage).toMatch(/limite de uso de voz.*fotos e vídeos continuam disponíveis/);
    const resumed = await resumeAudioDrafts([stored]);
    expect(resumed).toEqual([]);
    await expect(uploadAudioDraft(stored)).rejects.toMatchObject({ code: "LIMIT_REACHED" });
    expect(calls.create).toHaveLength(1);
  });

  it.each([
    ["IT-020 consent no longer active", "CONSENT_REQUIRED"],
    ["IT-047 photo outside the capture", "NOT_FOUND"],
    ["IT-103 audio rejected as invalid", "INVALID_AUDIO"]
  ])("%s becomes an unsendable note with Portuguese text", async (_name, code) => {
    server({ createFailure: { code, message: "mensagem do servidor" } });
    await saveDraft(photo());
    await saveAudioDraft(note());
    await expect(uploadAudioDraft(note())).rejects.toMatchObject({ code });
    const [stored] = await listAudioDrafts(responsibilityId);
    expect(stored.state).toBe("UNSENDABLE");
    expect(stored.statusMessage).toMatch(/[a-zç]/i);
    expect(stored.statusMessage).not.toMatch(/mensagem do servidor/);
  });

  it("IT-028 / IT-077 keeps the note for the renewed session instead of sending it under another identity", async () => {
    server({ createFailure: { code: "SESSION_EXPIRED", message: "sessão" } });
    await saveDraft(photo());
    await saveAudioDraft(note());
    await expect(uploadAudioDraft(note())).rejects.toMatchObject({ code: "SESSION_EXPIRED" });
    const [stored] = await listAudioDrafts(responsibilityId);
    expect(stored.state).toBe("FAILED");
    expect(stored.statusMessage).toMatch(/Solicite um novo acesso.*continua salva neste dispositivo/);
    expect(stored.audio?.size).toBe(5);
  });

  it("IT-080 after a lost acknowledgement shows the existing note instead of creating another job", async () => {
    const { calls } = server({ completeFailure: { code: "INVALID_STATE", message: "já concluída" }, existingStatus: "QUEUED" });
    await saveDraft(photo());
    await saveAudioDraft(note());
    const received = await uploadAudioDraft(note());
    expect(received).toMatchObject({ state: "RECEIVED", noteId: "server-note", audio: undefined });
    expect(calls.complete).toHaveLength(1);
    expect(calls.create).toHaveLength(1);
    expect((await listAudioDrafts(responsibilityId))[0].audio).toBeUndefined();
  });

  it("IT-074 marks a note with incomplete local audio as unsendable and never claims receipt", async () => {
    const { fetchMock } = server();
    await saveDraft(photo());
    await saveAudioDraft(note());
    await new Promise<void>((resolve, reject) => {
      const open = indexedDB.open("inspection-audio-notes-v1");
      open.onsuccess = () => {
        const tx = open.result.transaction("audioDrafts", "readwrite");
        const store = tx.objectStore("audioDrafts");
        const get = store.get("note-1");
        get.onsuccess = () => store.put({ ...get.result, audioBytes: new ArrayBuffer(1) });
        tx.oncomplete = () => { open.result.close(); resolve(); };
        tx.onerror = () => reject(tx.error);
      };
    });
    await expect(uploadAudioDraft(note())).rejects.toMatchObject({ code: "LOCAL_AUDIO_UNAVAILABLE" });
    expect(fetchMock).not.toHaveBeenCalled();
    expect((await listAudioDrafts(responsibilityId))[0].state).toBe("UNSENDABLE");
  });

  it("IT-072 flushes pending notes before submission and names those that could not be sent", async () => {
    server({ createFailure: { code: "LIMIT_REACHED", message: "limite" } });
    await saveDraft(photo());
    await saveAudioDraft(note({ id: "a" }));
    await saveAudioDraft(note({ id: "b", target: { kind: "PHOTO", photoDraftId: "missing-photo" } }));
    const remaining = await flushAudioDrafts(responsibilityId, 2_000);
    expect(remaining.map((item) => [item.id, item.state]).sort()).toEqual([["a", "UNSENDABLE"], ["b", "UNSENDABLE"]]);
  });

  it("IT-072 does not hold submission hostage when a request never answers", async () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => undefined)));
    await saveDraft(photo());
    await saveAudioDraft(note());
    const started = Date.now();
    const remaining = await flushAudioDrafts(responsibilityId, 60);
    expect(Date.now() - started).toBeLessThan(1_500);
    expect(remaining.map((item) => item.id)).toEqual(["note-1"]);
  });
});
