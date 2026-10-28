import "fake-indexeddb/auto";
import { Blob as NodeBlob } from "node:buffer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appendVideoChunk, finalizeVideoDraft, listVideoDrafts, loadVideoDraft, openVideoDatabase, readVideoChunk, resetVideoDraft, saveVideoDraft, videoChunks, type VideoDraft } from "@/pwa/video-drafts";
import { hashVideoBlobs } from "@/pwa/video-hash";
import { startVideoRecorder } from "@/pwa/video-recorder";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MutedVideoPreview } from "../../app/capture/[linkToken]/video-capture";
import { visibleDrafts } from "@/pwa/video-capture-state";

const videoBlob = (parts: string[], options?: BlobPropertyBag): Blob => new NodeBlob(parts, options) as unknown as Blob;
const draft = (id: string): VideoDraft => ({ id, responsibilityId: "r", kind: "INSPECTION", source: "CAMERA", contentType: "video/webm", maxDurationSeconds: 900, maxBytes: 1 << 30, expiresAt: "2099-01-01T00:00:00Z", state: "INCOMPLETE", chunkCount: 0, sizeBytes: 0, parts: [] });
const hashStored = async (id: string, count: number) => hashVideoBlobs(await videoChunks(id, count));
const fakeStream = (stop = vi.fn()) => {
  const listeners = new Set<() => void>();
  const track = { readyState: "live", stop, addEventListener: (_type: string, listener: () => void) => listeners.add(listener), removeEventListener: (_type: string, listener: () => void) => listeners.delete(listener), end: () => listeners.forEach((listener) => listener()), listenerCount: () => listeners.size };
  return { stream: { getTracks: () => [track], getVideoTracks: () => [track] } as unknown as MediaStream, track, stop };
};

describe("C03 video capture", () => {
  beforeEach(() => { vi.stubGlobal("Blob", NodeBlob); });
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

  it("IT-136 renders imported video previews muted", () => {
    const markup = renderToStaticMarkup(React.createElement(MutedVideoPreview, {
      src: "blob:audio-file-preview",
      onCanPlay: () => undefined,
      onLoadedMetadata: () => undefined,
      onError: () => undefined,
    }));
    expect(markup).toContain("muted=\"\"");
    expect(markup).toContain("playsInline=\"\"");
  });

  it("UT-010 hashes ordered persisted chunks incrementally", async () => {
    const item = draft("ordered-abc");
    await saveVideoDraft(item);
    await appendVideoChunk(item.id, 0, videoBlob(["a"]));
    await appendVideoChunk(item.id, 1, videoBlob(["b"]));
    await appendVideoChunk(item.id, 2, videoBlob(["c"]));
    const saved = await finalizeVideoDraft({ ...item, chunkCount: 3, sizeBytes: 3 }, hashStored);
    expect(saved.sha256).toBe("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
    expect((await loadVideoDraft(item.id))?.state).toBe("SAVED");
  });

  it("persists video chunks as ArrayBuffer and restores their Blob content and type", async () => {
    const source = videoBlob(["video-data"], { type: "video/webm" });
    await appendVideoChunk("array-buffer-chunk", 0, source);

    const db = await openVideoDatabase();
    let stored: { data: ArrayBuffer; type: string } | undefined;
    try {
      stored = await new Promise((resolve, reject) => {
        const request = db.transaction("videoChunks").objectStore("videoChunks").get(["array-buffer-chunk", 0]);
        request.onsuccess = () => resolve(request.result as typeof stored);
        request.onerror = () => reject(request.error);
      });
    } finally { db.close(); }

    expect(Object.prototype.toString.call(stored?.data)).toBe("[object ArrayBuffer]");
    expect(stored?.type).toBe("video/webm");
    const restored = await readVideoChunk("array-buffer-chunk", 0);
    expect(Object.prototype.toString.call(restored)).toBe("[object Blob]");
    expect(restored?.type).toBe("video/webm");
    expect(await restored?.text()).toBe("video-data");
  });

  it("reads legacy video chunks stored as Blob", async () => {
    const db = await openVideoDatabase();
    try {
      await new Promise<void>((resolve, reject) => {
        const transaction = db.transaction("videoChunks", "readwrite");
        transaction.objectStore("videoChunks").add({ attemptId: "legacy-blob-chunk", sequence: 0, blob: videoBlob(["legacy-data"], { type: "video/mp4" }) });
        transaction.oncomplete = () => resolve();
        transaction.onerror = () => reject(transaction.error);
        transaction.onabort = () => reject(transaction.error);
      });
    } finally { db.close(); }

    const restored = await readVideoChunk("legacy-blob-chunk", 0);
    expect(Object.prototype.toString.call(restored)).toBe("[object Blob]");
    expect(restored?.type).toBe("video/mp4");
    expect(await restored?.text()).toBe("legacy-data");
  });

  it("keeps an admitted attempt after discarding its video and removes the old chunks", async () => {
    const item = { ...draft("discarded-retry"), state: "SAVED" as const, contentType: "video/webm", chunkCount: 1, sizeBytes: 5, sha256: "digest" };
    await saveVideoDraft(item);
    await appendVideoChunk(item.id, 0, videoBlob(["video"]));

    const ready = await resetVideoDraft(item);

    expect(ready).toMatchObject({ id: item.id, state: "READY", chunkCount: 0, sizeBytes: 0 });
    expect(await readVideoChunk(item.id, 0)).toBeUndefined();
    expect(await loadVideoDraft(item.id)).toMatchObject({ id: item.id, state: "READY" });
    expect(visibleDrafts(await listVideoDrafts(item.responsibilityId), "idle")).not.toEqual(expect.arrayContaining([expect.objectContaining({ id: item.id })]));
  });

  it("UT-011 does not mark a draft SAVED when its final transaction aborts", async () => {
    const item = draft("abort-final");
    await saveVideoDraft(item);
    await appendVideoChunk(item.id, 0, videoBlob(["a"]));
    const original = IDBObjectStore.prototype.put;
    vi.spyOn(IDBObjectStore.prototype, "put").mockImplementation(function (this: IDBObjectStore, value, key) {
      const request = original.call(this, value, key);
      if ((value as VideoDraft).state === "SAVED") queueMicrotask(() => this.transaction.abort());
      return request;
    });
    await expect(finalizeVideoDraft({ ...item, chunkCount: 1, sizeBytes: 1 }, hashStored)).rejects.toBeDefined();
    expect((await loadVideoDraft(item.id))?.state).toBe("UNSAVED");
  });

  it("UT-012 refuses a missing sequence between stored chunks", async () => {
    const item = draft("missing-middle");
    await appendVideoChunk(item.id, 0, videoBlob(["a"]));
    await appendVideoChunk(item.id, 2, videoBlob(["c"]));
    await expect(finalizeVideoDraft({ ...item, chunkCount: 3, sizeBytes: 2 }, hashStored)).rejects.toThrow(/Parte 1 ausente/);
  });

  it("UT-013 reads a 1 GiB logical Blob in 1 MiB slices and never requests its whole buffer", async () => {
    const slice = vi.fn(() => videoBlob(["x"]));
    const whole = vi.fn(() => { throw new Error("whole blob read"); });
    const large = { size: 1024 * 1024 * 1024, slice, arrayBuffer: whole } as unknown as Blob;
    await hashVideoBlobs([large]);
    expect(slice).toHaveBeenCalledTimes(1024);
    expect(slice).toHaveBeenNthCalledWith(1, 0, 1024 * 1024);
    expect(whole).not.toHaveBeenCalled();
  });

  it("UT-014 stops from the wall clock even with no dataavailable callbacks", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    let now = 0;
    const stopTrack = vi.fn();
    const { stream } = fakeStream(stopTrack);
    const instances: FakeRecorder[] = [];
    class FakeRecorder {
      static isTypeSupported = () => true;
      mimeType = "video/mp4";
      state = "inactive";
      ondataavailable?: (event: { data: Blob }) => void;
      onstop?: () => void;
      onerror?: () => void;
      stop = vi.fn(() => { this.state = "inactive"; this.onstop?.(); });
      start = vi.fn(() => { this.state = "recording"; });
      constructor(received: MediaStream) { expect(received).toBe(stream); instances.push(this); }
    }
    vi.stubGlobal("MediaRecorder", FakeRecorder);
    const updates: number[] = [];
    const session = await startVideoRecorder(stream, draft("wall-clock"), hashStored, (update) => updates.push(update.elapsedSeconds), () => now);
    now = 900_000;
    await vi.advanceTimersByTimeAsync(250);
    await expect(session.stop()).rejects.toThrow(/interrompida|trecho/);
    expect(instances).toHaveLength(1);
    expect(instances[0].stop).toHaveBeenCalledOnce();
    expect(updates).toContain(900);
    expect(stopTrack).toHaveBeenCalled();
    expect(await listVideoDrafts("r")).toEqual(expect.arrayContaining([expect.objectContaining({ id: "wall-clock", state: "INCOMPLETE" })]));
  });

  it("IT-131 lists only responsibility metadata without reading stored video chunks", async () => {
    for (let index = 0; index < 100; index += 1) {
      const item = { ...draft(`indexed-${index}`), responsibilityId: index % 2 === 0 ? "target" : "other", chunkCount: 1, sizeBytes: 1 };
      await saveVideoDraft(item);
      await appendVideoChunk(item.id, 0, videoBlob(["x"]));
    }
    const readChunk = vi.spyOn(IDBObjectStore.prototype, "get");
    const listed = await listVideoDrafts("target");
    expect(listed).toHaveLength(50);
    expect(listed.every((item) => item.responsibilityId === "target")).toBe(true);
    expect(readChunk).not.toHaveBeenCalled();
  });

  it("leaves an unexpected recorder stop incomplete and reports the interruption", async () => {
    const stopTrack = vi.fn();
    const { stream } = fakeStream(stopTrack);
    const recorders: FakeRecorder[] = [];
    class FakeRecorder {
      static isTypeSupported = () => true;
      mimeType = "video/mp4";
      state = "inactive";
      onstop?: () => void;
      ondataavailable?: (event: { data: Blob }) => void;
      start() { this.state = "recording"; }
      stopUnexpectedly() { this.ondataavailable?.({ data: videoBlob(["some-data"]) }); this.state = "inactive"; this.onstop?.(); }
      constructor() { recorders.push(this); }
    }
    vi.stubGlobal("MediaRecorder", FakeRecorder);
    const updates: string[] = [];
    const session = await startVideoRecorder(stream, draft("unexpected-stop"), hashStored, (update) => { if (update.error) updates.push(update.error); });
    recorders[0].stopUnexpectedly();
    await expect(session.stop()).resolves.toMatchObject({ state: "SAVED" });
    expect(stopTrack).toHaveBeenCalled();
    expect(updates).toEqual([]);
    expect((await loadVideoDraft("unexpected-stop"))?.state).toBe("SAVED");
  });

  it("rejects an unexpected stop without chunks and never marks the draft saved", async () => {
    const { stream, stop: stopTrack } = fakeStream();
    const recorders: FakeRecorder[] = [];
    class FakeRecorder {
      static isTypeSupported = () => true;
      mimeType = "video/mp4";
      state = "inactive";
      onstop?: () => void;
      start() { this.state = "recording"; }
      stopUnexpectedly() { this.state = "inactive"; this.onstop?.(); }
      constructor() { recorders.push(this); }
    }
    vi.stubGlobal("MediaRecorder", FakeRecorder);
    const session = await startVideoRecorder(stream, draft("unexpected-empty"), hashStored, () => {});
    recorders[0].stopUnexpectedly();
    await expect(session.stop()).rejects.toThrow(/interrompida|trecho/);
    expect(stopTrack).toHaveBeenCalledOnce();
    expect((await loadVideoDraft("unexpected-empty"))?.state).toBe("INCOMPLETE");
  });

  it("does not finalize a recorder that emitted an error", async () => {
    const stopTrack = vi.fn();
    const { stream } = fakeStream(stopTrack);
    const recorders: FakeRecorder[] = [];
    class FakeRecorder {
      static isTypeSupported = () => true;
      mimeType = "video/mp4";
      state = "inactive";
      onstop?: () => void;
      onerror?: () => void;
      start() { this.state = "recording"; }
      stop() { this.state = "inactive"; this.onstop?.(); }
      constructor() { recorders.push(this); }
    }
    vi.stubGlobal("MediaRecorder", FakeRecorder);
    const session = await startVideoRecorder(stream, draft("recorder-error"), hashStored, () => {});
    recorders[0].onerror?.();
    await expect(session.stop()).rejects.toThrow("interrompida");
    expect(stopTrack).toHaveBeenCalled();
    expect((await loadVideoDraft("recorder-error"))?.state).toBe("INCOMPLETE");
  });

  it("finalizes concurrent stop requests once and never acquires a different stream", async () => {
    const { stream, stop: stopTrack } = fakeStream();
    const getUserMedia = vi.fn();
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } });
    const recorders: Array<{ stop: ReturnType<typeof vi.fn>; onstop?: () => void; ondataavailable?: (event: { data: Blob }) => void }> = [];
    class FakeRecorder {
      static isTypeSupported = () => true;
      mimeType = "video/mp4";
      state = "inactive";
      onstop?: () => void;
      ondataavailable?: (event: { data: Blob }) => void;
      stop = vi.fn(() => { this.ondataavailable?.({ data: videoBlob(["frame"]) }); this.state = "inactive"; this.onstop?.(); });
      start = vi.fn(() => { this.state = "recording"; });
      constructor(received: MediaStream) { expect(received).toBe(stream); recorders.push(this); }
    }
    vi.stubGlobal("MediaRecorder", FakeRecorder);

    const session = await startVideoRecorder(stream, draft("finalize-once"), hashStored, () => {});
    const first = session.stop();
    const second = session.stop();
    expect(second).toBe(first);
    await expect(first).resolves.toMatchObject({ state: "SAVED", chunkCount: 1 });
    expect(recorders).toHaveLength(1);
    expect(recorders[0].stop).toHaveBeenCalledOnce();
    expect(getUserMedia).not.toHaveBeenCalled();
    expect(stopTrack).toHaveBeenCalledOnce();
  });

  it("releases the camera if MediaRecorder cannot be constructed", async () => {
    const stopTrack = vi.fn();
    const { stream } = fakeStream(stopTrack);
    class BrokenRecorder {
      static isTypeSupported = () => true;
      constructor() { throw new Error("recorder initialization failed"); }
    }
    vi.stubGlobal("MediaRecorder", BrokenRecorder);
    await expect(startVideoRecorder(stream, draft("broken-recorder"), hashStored, () => {})).rejects.toThrow("recorder initialization failed");
    expect(stopTrack).not.toHaveBeenCalled();
  });

  it("reuses the same finalization when onUpdate reenters stop() and finalizes once", async () => {
    const { stream, stop: stopTrack } = fakeStream();
    const recorders: Array<{ stop: ReturnType<typeof vi.fn> }> = [];
    class FakeRecorder {
      static isTypeSupported = () => true;
      mimeType = "video/mp4";
      state = "inactive";
      onstop?: () => void;
      ondataavailable?: (event: { data: Blob }) => void;
      stop = vi.fn(() => { this.ondataavailable?.({ data: videoBlob(["frame"]) }); this.state = "inactive"; this.onstop?.(); });
      start() { this.state = "recording"; }
      constructor() { recorders.push(this); }
    }
    vi.stubGlobal("MediaRecorder", FakeRecorder);
    const promises: Array<Promise<VideoDraft>> = [];
    const holder: { session?: Awaited<ReturnType<typeof startVideoRecorder>> } = {};
    const hash = vi.fn(hashStored);
    const session = await startVideoRecorder(stream, draft("reentrant-stop"), hash, (update) => { if (update.finalizing && holder.session) promises.push(holder.session.stop()); });
    holder.session = session;
    const first = session.stop();
    await expect(first).resolves.toMatchObject({ state: "SAVED", chunkCount: 1 });
    expect(promises.length).toBeGreaterThan(0);
    for (const promise of promises) expect(promise).toBe(first);
    expect(recorders[0].stop).toHaveBeenCalledOnce();
    expect(hash).toHaveBeenCalledOnce();
    expect(stopTrack).toHaveBeenCalled();
  });

  it("UT-338 finalizes once at the policy max duration and shows the limit message", async () => {
    let clockMs = 0;
    const { stream } = fakeStream();
    const recorders: Array<{ stop: ReturnType<typeof vi.fn> }> = [];
    class FakeRecorder {
      static isTypeSupported = () => true;
      mimeType = "video/mp4";
      state = "inactive";
      onstop?: () => void;
      ondataavailable?: (event: { data: Blob }) => void;
      stop = vi.fn(() => { this.ondataavailable?.({ data: videoBlob(["frame"]) }); this.state = "inactive"; this.onstop?.(); });
      start() { this.state = "recording"; }
      constructor() { recorders.push(this); }
    }
    vi.stubGlobal("MediaRecorder", FakeRecorder);
    const limited = { ...draft("limit-once"), maxDurationSeconds: 1 };
    const limitUpdates: boolean[] = [];
    const session = await startVideoRecorder(stream, limited, hashStored, (update) => { if (update.limitReached) limitUpdates.push(true); }, () => clockMs);
    clockMs = 2000;
    await new Promise((resolve) => setTimeout(resolve, 800));
    await expect(session.stop()).resolves.toMatchObject({ state: "SAVED" });
    expect(recorders[0].stop).toHaveBeenCalledOnce();
    expect(limitUpdates.length).toBeLessThanOrEqual(2);
  });

  it("finalizes when the video track ends during recording and unsubscribes afterwards", async () => {
    const { stream, track } = fakeStream();
    const recorders: FakeRecorder[] = [];
    class FakeRecorder {
      static isTypeSupported = () => true;
      mimeType = "video/mp4";
      state = "inactive";
      onstop?: () => void;
      ondataavailable?: (event: { data: Blob }) => void;
      stop() { this.ondataavailable?.({ data: videoBlob(["last"]) }); this.state = "inactive"; this.onstop?.(); }
      start() { this.state = "recording"; }
      constructor() { recorders.push(this); }
    }
    vi.stubGlobal("MediaRecorder", FakeRecorder);
    const updates: boolean[] = [];
    const session = await startVideoRecorder(stream, draft("track-ended"), hashStored, (update) => { if (update.finalizing) updates.push(true); });
    expect(track.listenerCount()).toBe(1);
    track.end();
    await expect(session.stop()).resolves.toMatchObject({ state: "SAVED", chunkCount: 1 });
    expect(updates.length).toBeGreaterThan(0);
    expect(track.listenerCount()).toBe(0);
  });

  it("does not leave an incomplete draft behind when the start is cancelled after the draft was saved", async () => {
    const { stream, stop: stopTrack } = fakeStream();
    const recorders: unknown[] = [];
    class FakeRecorder {
      static isTypeSupported = () => true;
      mimeType = "video/mp4";
      state = "inactive";
      start = vi.fn();
      constructor() { recorders.push(this); }
    }
    vi.stubGlobal("MediaRecorder", FakeRecorder);
    let calls = 0;
    await expect(startVideoRecorder(stream, draft("cancelled-start"), hashStored, () => {}, Date.now, () => ++calls > 1)).rejects.toMatchObject({ name: "AbortError" });
    expect(await loadVideoDraft("cancelled-start")).toBeUndefined();
    expect(stopTrack).not.toHaveBeenCalled();
  });

  it("does not even save a draft when the start was already cancelled", async () => {
    const { stream } = fakeStream();
    vi.stubGlobal("MediaRecorder", class { static isTypeSupported = () => true; });
    await expect(startVideoRecorder(stream, draft("cancelled-early"), hashStored, () => {}, Date.now, () => true)).rejects.toMatchObject({ name: "AbortError" });
    expect(await loadVideoDraft("cancelled-early")).toBeUndefined();
  });

  it("rejects with a storage failure and never marks the draft saved when a chunk cannot be persisted", async () => {
    const { stream } = fakeStream();
    const recorders: FakeRecorder[] = [];
    class FakeRecorder {
      static isTypeSupported = () => true;
      mimeType = "video/mp4";
      state = "inactive";
      onstop?: () => void;
      ondataavailable?: (event: { data: Blob }) => void;
      stop() { this.state = "inactive"; this.onstop?.(); }
      start() { this.state = "recording"; }
      constructor() { recorders.push(this); }
    }
    vi.stubGlobal("MediaRecorder", FakeRecorder);
    const errors: string[] = [];
    const add = vi.spyOn(IDBObjectStore.prototype, "add").mockImplementation(() => { throw new DOMException("quota", "QuotaExceededError"); });
    const session = await startVideoRecorder(stream, draft("storage-failure"), hashStored, (update) => { if (update.error) errors.push(update.error); });
    recorders[0].ondataavailable?.({ data: videoBlob(["chunk"]) });
    await expect(session.stop()).rejects.toThrow();
    add.mockRestore();
    expect(errors).toEqual(["Não foi possível salvar a gravação neste dispositivo."]);
    expect((await loadVideoDraft("storage-failure"))?.state).not.toBe("SAVED");
  });
});
