import "fake-indexeddb/auto";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { listAudioDrafts, removeAudioBytes, saveAudioDraft } from "@/pwa/audio-drafts";
import { openMicrophone, startAudioRecorder, type AudioRecorderUpdate } from "@/pwa/audio-recorder";
import { uploadAudioDraft } from "@/pwa/audio-uploads";

class FakeMediaRecorder {
  static isTypeSupported = vi.fn(() => true);
  state: "inactive" | "recording" = "inactive";
  mimeType: string;
  ondataavailable?: (event: { data: Blob }) => void;
  onstop?: () => void;
  onerror?: () => void;
  constructor(_stream: MediaStream, options?: MediaRecorderOptions) { this.mimeType = options?.mimeType ?? "audio/webm"; }
  start() { this.state = "recording"; }
  stop() { this.ondataavailable?.({ data: new Blob(["voice"], { type: this.mimeType }) }); this.state = "inactive"; this.onstop?.(); }
}

function stream() {
  const track = new EventTarget() as MediaStreamTrack;
  Object.defineProperties(track, { readyState: { value: "live" }, stop: { value: vi.fn() } });
  return { getTracks: () => [track], getAudioTracks: () => [track] } as unknown as MediaStream;
}

const identity = {
  id: "note-one", responsibilityId: "responsibility-one", requirementKey: "env:living", disclosureVersion: "disclosure-v1", consentVersion: "voice-v1",
  target: { kind: "VIDEO" as const, attemptId: "attempt-one", startMs: 1_000, endMs: 1_800, timeBasis: "CAPTURE_CLOCK" as const }
};

describe("audio note capture", () => {
  beforeEach(async () => {
    vi.stubGlobal("MediaRecorder", FakeMediaRecorder);
    await new Promise<void>((resolve, reject) => { const request = indexedDB.deleteDatabase("inspection-audio-notes-v1"); request.onsuccess = () => resolve(); request.onerror = () => reject(request.error); });
  });

  it("UT-007 finalizes a valid held note once and keeps its original clip interval", async () => {
    let time = 0;
    const updates: string[] = [];
    const session = await startAudioRecorder({ stream: stream(), identity, now: () => time, digest: async () => "a".repeat(64), onUpdate: (update) => updates.push(update.state) });
    time = 800;
    const first = session.stop("release");
    const duplicate = session.stop("release");
    const [draft, same] = await Promise.all([first, duplicate]);
    expect(draft).toMatchObject({ durationMs: 800, target: identity.target, state: "PENDING_UPLOAD" });
    expect(same).toEqual(draft);
    expect(updates.filter((state) => state === "finalizing")).toHaveLength(1);
    expect((await listAudioDrafts(identity.responsibilityId))[0].target).toEqual(identity.target);
  });

  it("finalizes once when the browser ends the microphone track", async () => {
    let time = 0;
    const microphone = stream();
    const updates: string[] = [];
    const session = await startAudioRecorder({ stream: microphone, identity: { ...identity, id: "interrupted" }, now: () => time, digest: async () => "d".repeat(64), onUpdate: (update) => updates.push(update.state) });
    time = 800;
    microphone.getAudioTracks()[0].dispatchEvent(new Event("ended"));
    const finalized = await session.stop("release");
    expect(finalized).toMatchObject({ id: "interrupted", durationMs: 800, target: identity.target, state: "PENDING_UPLOAD" });
    expect(updates.filter((state) => state === "finalizing")).toHaveLength(1);
    expect(await listAudioDrafts(identity.responsibilityId)).toHaveLength(1);
  });

  it("discards a sub-500 ms note without persistence", async () => {
    let time = 0;
    const session = await startAudioRecorder({ stream: stream(), identity: { ...identity, id: "short" }, now: () => time, onUpdate: () => undefined });
    time = 499;
    expect(await session.stop()).toBeUndefined();
    expect(await listAudioDrafts(identity.responsibilityId)).toHaveLength(0);
  });

  it("UT-008 stops a microphone stream granted after cancellation", async () => {
    let resolve!: (value: MediaStream) => void;
    const lateStream = stream();
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: vi.fn(() => new Promise<MediaStream>((done) => { resolve = done; })) } });
    let canceled = false;
    const opening = openMicrophone(() => canceled);
    canceled = true;
    resolve(lateStream);
    expect(await opening).toBeUndefined();
    expect((lateStream.getTracks()[0].stop as unknown as ReturnType<typeof vi.fn>)).toHaveBeenCalledOnce();
    vi.unstubAllGlobals();
  });

  it("removes local audio only after receipt while preserving the note anchor", async () => {
    const audio = new Blob(["voice"], { type: "audio/webm" });
    await saveAudioDraft({ ...identity, durationMs: 800, contentType: audio.type, sizeBytes: audio.size, sha256: "b".repeat(64), state: "PENDING_UPLOAD", audio, createdAt: new Date(0).toISOString() });
    await removeAudioBytes(identity.id, identity.responsibilityId);
    expect(await listAudioDrafts(identity.responsibilityId)).toMatchObject([{ id: identity.id, state: "RECEIVED", target: identity.target, audio: undefined }]);
  });

  it("resumes a staged upload and deletes audio only after server receipt", async () => {
    const audio = new Blob(["voice"], { type: "audio/webm" });
    const draft = { ...identity, durationMs: 800, contentType: audio.type, sizeBytes: audio.size, sha256: "c".repeat(64), state: "PENDING_UPLOAD" as const, audio, createdAt: new Date(0).toISOString() };
    await saveAudioDraft(draft);
    const fetch = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "PUT") return new Response(null, { status: 200 });
      const body = JSON.parse(String(init?.body)) as { query: string };
      if (body.query.includes("CreateAudioNoteUpload")) return new Response(JSON.stringify({ data: { createAudioNoteUpload: { noteId: "server-note", upload: { url: "https://upload.invalid", method: "PUT", headers: { "x-upload-proof": "proof" }, expiresAt: new Date(Date.now() + 60_000).toISOString() }, userErrors: [], clientMutationId: `${draft.id}:create` } } }), { status: 200 });
      return new Response(JSON.stringify({ data: { completeAudioNoteUpload: { note: { id: "server-note", status: "QUEUED", version: 1, audioDeletionStatus: "PENDING" }, userErrors: [], clientMutationId: `${draft.id}:complete` } } }), { status: 200 });
    });
    vi.stubGlobal("fetch", fetch);
    const received = await uploadAudioDraft(draft);
    expect(received).toMatchObject({ state: "RECEIVED", noteId: "server-note", target: identity.target, audio: undefined });
    expect(fetch).toHaveBeenCalledTimes(3);
    expect(await listAudioDrafts(identity.responsibilityId)).toMatchObject([{ id: draft.id, state: "RECEIVED", audio: undefined }]);
    vi.unstubAllGlobals();
  });

  it("IT-033 / IT-036 / IT-062 stops once at 60 seconds, saves the valid note and explains the limit", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    try {
      let time = 0;
      const updates: AudioRecorderUpdate[] = [];
      const session = await startAudioRecorder({ stream: stream(), identity: { ...identity, id: "limit-time" }, now: () => time, digest: async () => "e".repeat(64), onUpdate: (update) => updates.push(update) });
      time = 60_500;
      vi.advanceTimersByTime(300);
      const draft = await session.stop("release");
      expect(draft).toMatchObject({ id: "limit-time", durationMs: 60_000, state: "PENDING_UPLOAD" });
      expect(updates.filter((update) => update.state === "finalizing")).toHaveLength(1);
      expect(updates.find((update) => update.state === "saved")?.message).toMatch(/60 segundos/);
      expect(await listAudioDrafts(identity.responsibilityId)).toHaveLength(1);
    } finally { vi.useRealTimers(); }
  });

  it("IT-033 / IT-036 stops once at 5 MB, keeps the captured part and names the size limit", async () => {
    class BigChunkRecorder extends FakeMediaRecorder {
      start() { super.start(); setTimeout(() => { this.ondataavailable?.({ data: new Blob([new Uint8Array(3 * 1024 * 1024)]) }); this.ondataavailable?.({ data: new Blob([new Uint8Array(3 * 1024 * 1024)]) }); }, 5); }
    }
    vi.stubGlobal("MediaRecorder", BigChunkRecorder);
    let time = 0;
    const updates: AudioRecorderUpdate[] = [];
    const session = await startAudioRecorder({ stream: stream(), identity: { ...identity, id: "limit-size" }, now: () => time, digest: async () => "f".repeat(64), onUpdate: (update) => updates.push(update) });
    time = 4_000;
    await new Promise((resolve) => setTimeout(resolve, 40));
    const draft = await session.stop("release");
    expect(draft?.sizeBytes).toBeLessThanOrEqual(5 * 1024 * 1024);
    expect(draft?.sizeBytes).toBeGreaterThan(0);
    expect(updates.filter((update) => update.state === "finalizing")).toHaveLength(1);
    expect(updates.find((update) => update.state === "saved")?.message).toMatch(/5 MB/);
  });

  it("IT-073 does not claim a note was saved when device storage is full", async () => {
    const put = vi.spyOn(IDBObjectStore.prototype, "put").mockImplementation(() => { throw new DOMException("quota", "QuotaExceededError"); });
    try {
      let time = 0;
      const updates: AudioRecorderUpdate[] = [];
      const session = await startAudioRecorder({ stream: stream(), identity: { ...identity, id: "no-space" }, now: () => time, digest: async () => "9".repeat(64), onUpdate: (update) => updates.push(update) });
      time = 900;
      await expect(session.stop("release")).rejects.toBeDefined();
      expect(updates.some((update) => update.state === "saved")).toBe(false);
      expect(updates.find((update) => update.state === "failed")?.message).toMatch(/sem espaço.*não foi salva/);
    } finally { put.mockRestore(); }
    expect(await listAudioDrafts(identity.responsibilityId)).toHaveLength(0);
  });

  it("IT-023 / IT-037 turns a denied microphone into a Portuguese error without raw browser text", async () => {
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: vi.fn(async () => { throw new DOMException("Permission denied", "NotAllowedError"); }) } });
    await expect(openMicrophone(() => false)).rejects.toMatchObject({ code: "MICROPHONE_DENIED", message: expect.stringMatching(/acesso ao microfone foi negado/) });
    vi.unstubAllGlobals();
  });

  it("IT-039 / IT-065 reports a stop caused by the page leaving the foreground as one saved note", async () => {
    let time = 0;
    const updates: AudioRecorderUpdate[] = [];
    const session = await startAudioRecorder({ stream: stream(), identity: { ...identity, id: "background" }, now: () => time, digest: async () => "8".repeat(64), onUpdate: (update) => updates.push(update) });
    time = 900;
    const [first, second] = await Promise.all([session.stop("background"), session.stop("focus")]);
    expect(first).toBe(second);
    expect(updates.find((update) => update.state === "saved")?.message).toMatch(/primeiro plano/);
    expect(await listAudioDrafts(identity.responsibilityId)).toHaveLength(1);
  });
});
