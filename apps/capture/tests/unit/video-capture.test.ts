import "fake-indexeddb/auto";
import { Blob as NodeBlob } from "node:buffer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appendVideoChunk, finalizeVideoDraft, listVideoDrafts, loadVideoDraft, openVideoDatabase, readVideoChunk, saveVideoDraft, videoChunks, type VideoDraft } from "@/pwa/video-drafts";
import { hashVideoBlobs } from "@/pwa/video-hash";
import { startVideoRecorder } from "@/pwa/video-recorder";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MutedVideoPreview } from "../../app/capture/[linkToken]/video-capture";

const draft = (id: string): VideoDraft => ({ id, responsibilityId: "r", kind: "INSPECTION", source: "CAMERA", contentType: "video/webm", maxDurationSeconds: 900, maxBytes: 1 << 30, expiresAt: "2099-01-01T00:00:00Z", state: "INCOMPLETE", chunkCount: 0, sizeBytes: 0, parts: [] });
const hashStored = async (id: string, count: number) => hashVideoBlobs(await videoChunks(id, count));

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
    await appendVideoChunk(item.id, 0, new NodeBlob(["a"]));
    await appendVideoChunk(item.id, 1, new NodeBlob(["b"]));
    await appendVideoChunk(item.id, 2, new NodeBlob(["c"]));
    const saved = await finalizeVideoDraft({ ...item, chunkCount: 3, sizeBytes: 3 }, hashStored);
    expect(saved.sha256).toBe("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
    expect((await loadVideoDraft(item.id))?.state).toBe("SAVED");
  });

  it("persists video chunks as ArrayBuffer and restores their Blob content and type", async () => {
    const source = new NodeBlob(["video-data"], { type: "video/webm" });
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
        transaction.objectStore("videoChunks").add({ attemptId: "legacy-blob-chunk", sequence: 0, blob: new NodeBlob(["legacy-data"], { type: "video/mp4" }) });
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

  it("UT-011 does not mark a draft SAVED when its final transaction aborts", async () => {
    const item = draft("abort-final");
    await saveVideoDraft(item);
    await appendVideoChunk(item.id, 0, new NodeBlob(["a"]));
    const original = IDBObjectStore.prototype.put;
    vi.spyOn(IDBObjectStore.prototype, "put").mockImplementation(function (value, key) {
      const request = original.call(this, value, key);
      if ((value as VideoDraft).state === "SAVED") queueMicrotask(() => this.transaction.abort());
      return request;
    });
    await expect(finalizeVideoDraft({ ...item, chunkCount: 1, sizeBytes: 1 }, hashStored)).rejects.toBeDefined();
    expect((await loadVideoDraft(item.id))?.state).toBe("UNSAVED");
  });

  it("UT-012 refuses a missing sequence between stored chunks", async () => {
    const item = draft("missing-middle");
    await appendVideoChunk(item.id, 0, new NodeBlob(["a"]));
    await appendVideoChunk(item.id, 2, new NodeBlob(["c"]));
    await expect(finalizeVideoDraft({ ...item, chunkCount: 3, sizeBytes: 2 }, hashStored)).rejects.toThrow(/Parte 1 ausente/);
  });

  it("UT-013 reads a 1 GiB logical Blob in 1 MiB slices and never requests its whole buffer", async () => {
    const slice = vi.fn(() => new NodeBlob(["x"]));
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
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: vi.fn().mockResolvedValue({ getTracks: () => [{ stop: stopTrack }] }) } });
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
      constructor() { instances.push(this); }
    }
    vi.stubGlobal("MediaRecorder", FakeRecorder);
    const updates: number[] = [];
    await startVideoRecorder(draft("wall-clock"), hashStored, (update) => updates.push(update.elapsedSeconds), () => now);
    now = 900_000;
    await vi.advanceTimersByTimeAsync(250);
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
      await appendVideoChunk(item.id, 0, new NodeBlob(["x"]));
    }
    const readChunk = vi.spyOn(IDBObjectStore.prototype, "get");
    const listed = await listVideoDrafts("target");
    expect(listed).toHaveLength(50);
    expect(listed.every((item) => item.responsibilityId === "target")).toBe(true);
    expect(readChunk).not.toHaveBeenCalled();
  });

  it("leaves an unexpected recorder stop incomplete and reports the interruption", async () => {
    const stopTrack = vi.fn();
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: vi.fn().mockResolvedValue({ getTracks: () => [{ stop: stopTrack }] }) } });
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
    const updates: string[] = [];
    await startVideoRecorder(draft("unexpected-stop"), hashStored, (update) => { if (update.error) updates.push(update.error); });
    recorders[0].stopUnexpectedly();
    expect(stopTrack).toHaveBeenCalled();
    expect(updates).toEqual([expect.stringContaining("interrompida")]);
    expect((await loadVideoDraft("unexpected-stop"))?.state).toBe("INCOMPLETE");
  });

  it("does not finalize a recorder that emitted an error", async () => {
    const stopTrack = vi.fn();
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: vi.fn().mockResolvedValue({ getTracks: () => [{ stop: stopTrack }] }) } });
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
    const session = await startVideoRecorder(draft("recorder-error"), hashStored, () => {});
    recorders[0].onerror?.();
    await expect(session.stop()).rejects.toThrow("interrompida");
    expect(stopTrack).toHaveBeenCalled();
    expect((await loadVideoDraft("recorder-error"))?.state).toBe("INCOMPLETE");
  });

  it("releases the camera if MediaRecorder cannot be constructed", async () => {
    const stopTrack = vi.fn();
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: vi.fn().mockResolvedValue({ getTracks: () => [{ stop: stopTrack }] }) } });
    class BrokenRecorder {
      static isTypeSupported = () => true;
      constructor() { throw new Error("recorder initialization failed"); }
    }
    vi.stubGlobal("MediaRecorder", BrokenRecorder);
    await expect(startVideoRecorder(draft("broken-recorder"), hashStored, () => {})).rejects.toThrow("recorder initialization failed");
    expect(stopTrack).toHaveBeenCalledOnce();
  });
});
