import "fake-indexeddb/auto";
import { describe, expect, it, vi } from "vitest";
import { uploadDraft, type UploadProgress } from "@/pwa/uploads";
import { loadDraft, type CaptureDraft } from "@/pwa/drafts";

describe("multipart reconciliation", () => {
  it("UT-061 and UT-062 presigns only missing parts and keeps completion idempotent", async () => {
    const draft: CaptureDraft = { id: "d", responsibilityId: "r", blob: new Blob(["image"], { type: "image/jpeg" }), sha256: "hash", mediaId: "media", uploadId: "upload", parts: [{ number: 1, complete: true, etag: "done" }], metadata: { requirementKey: "k", description: "d", source: "camera", capturedAt: "now" } };
    const fetch = vi.fn().mockImplementation(() => new Response(JSON.stringify({ data: { presignMediaParts: { parts: [], userErrors: [] }, completeMediaUpload: { userErrors: [] }, saveCaptureMetadata: { userErrors: [] } } }), { status: 200 }));
    vi.stubGlobal("fetch", fetch); await uploadDraft(draft); const calls = fetch.mock.calls.map((call) => String(call[1]?.body)); expect(calls.some((body) => body.includes("presignMediaParts") && body.includes("[1]"))).toBe(false); vi.unstubAllGlobals();
  });

  it("retries metadata while media processing is pending and persists the verified state", async () => {
    let metadataAttempts = 0;
    const fetch = vi.fn().mockImplementation(async (_input: string, init?: RequestInit) => {
      const body = String(init?.body ?? "");
      if (body.includes("saveCaptureMetadata")) {
        metadataAttempts += 1;
        const userErrors = metadataAttempts < 3 ? [{ message: "media processing pending" }] : [];
        return new Response(JSON.stringify({ data: { saveCaptureMetadata: { userErrors } } }), { status: 200 });
      }
      return new Response(JSON.stringify({ data: { completeMediaUpload: { userErrors: [] } } }), { status: 200 });
    });
    vi.stubGlobal("fetch", fetch);
    const result = await uploadDraft({ id: "retry", responsibilityId: "r", blob: new Blob(["image"], { type: "image/jpeg" }), sha256: "hash", mediaId: "media", uploadId: "upload", parts: [{ number: 1, complete: true, etag: "done" }], metadata: { requirementKey: "k", description: "d", source: "camera", capturedAt: "now" } });
    expect(metadataAttempts).toBe(3);
    expect(result.metadataSaved).toBe(true);
    vi.unstubAllGlobals();
  });

  it("retries the API INVALID_STATE envelope and reports monotonic progress", async () => {
    let metadataAttempts = 0;
    const progress: UploadProgress[] = [];
    const fetch = vi.fn().mockImplementation(async (_input: string, init?: RequestInit) => {
      const body = String(init?.body ?? "");
      if (body.includes("saveCaptureMetadata")) {
        metadataAttempts += 1;
        if (metadataAttempts < 3) return new Response(JSON.stringify({ errors: [{ message: "media verification and screening are pending", extensions: { code: "INVALID_STATE", field: "mediaId" } }] }), { status: 200 });
        return new Response(JSON.stringify({ data: { saveCaptureMetadata: { media: { id: "media", status: "READY" }, userErrors: [] } } }), { status: 200 });
      }
      return new Response(JSON.stringify({ data: { completeMediaUpload: { media: { id: "media", status: "VERIFIED" }, userErrors: [] } } }), { status: 200 });
    });
    vi.stubGlobal("fetch", fetch);

    const result = await uploadDraft({ id: "progress", responsibilityId: "r", blob: new Blob(["image"], { type: "image/jpeg" }), sha256: "hash", mediaId: "media", uploadId: "upload", parts: [{ number: 1, complete: true, etag: "done" }], metadata: { requirementKey: "k", description: "d", source: "camera", capturedAt: "now" } }, (value) => progress.push(value));

    expect(metadataAttempts).toBe(3);
    expect(result.metadataSaved).toBe(true);
    expect(progress.map((value) => value.phase)).toEqual(["preparing", "uploading", "verifying", "saving", "verifying", "saving", "verifying", "saving", "complete"]);
    expect(progress.map((value) => value.percent)).toEqual([...progress.map((value) => value.percent)].sort((left, right) => left - right));
    expect(progress.at(-1)).toMatchObject({ phase: "complete", percent: 100 });
    vi.unstubAllGlobals();
  });

  it("waits for post-upload verification before the first metadata mutation", async () => {
    const waits: number[] = [];
    const fetch = vi.fn().mockImplementation(async (_input: string, init?: RequestInit) => {
      const body = String(init?.body ?? "");
      if (body.includes("saveCaptureMetadata")) return new Response(JSON.stringify({ data: { saveCaptureMetadata: { media: { id: "media", status: "READY" }, userErrors: [] } } }), { status: 200 });
      return new Response(JSON.stringify({ data: { completeMediaUpload: { media: { id: "media", status: "VERIFIED" }, userErrors: [] } } }), { status: 200 });
    });
    vi.stubGlobal("fetch", fetch);

    await uploadDraft({ id: "initial-wait", responsibilityId: "r", blob: new Blob(["image"], { type: "image/jpeg" }), sha256: "hash", mediaId: "media", uploadId: "upload", parts: [{ number: 1, complete: true, etag: "done" }], metadata: { requirementKey: "k", description: "d", source: "camera", capturedAt: "now" } }, undefined, { wait: async (milliseconds) => { waits.push(milliseconds); } });

    expect(waits[0]).toBe(1000);
    expect(fetch.mock.calls.map((call) => String(call[1]?.body)).findIndex((body) => body.includes("saveCaptureMetadata"))).toBeGreaterThan(-1);
    vi.unstubAllGlobals();
  });

  it("does not retry a definitive INVALID_STATE error", async () => {
    let metadataAttempts = 0;
    const fetch = vi.fn().mockImplementation(async (_input: string, init?: RequestInit) => {
      const body = String(init?.body ?? "");
      if (body.includes("saveCaptureMetadata")) {
        metadataAttempts += 1;
        return new Response(JSON.stringify({ errors: [{ message: "capture is unavailable", extensions: { code: "INVALID_STATE", field: "responsibility" } }] }), { status: 200 });
      }
      return new Response(JSON.stringify({ data: { completeMediaUpload: { media: { id: "media", status: "VERIFIED" }, userErrors: [] } } }), { status: 200 });
    });
    vi.stubGlobal("fetch", fetch);

    await expect(uploadDraft({ id: "definitive-error", responsibilityId: "r", blob: new Blob(["image"], { type: "image/jpeg" }), sha256: "hash", mediaId: "media", uploadId: "upload", parts: [{ number: 1, complete: true, etag: "done" }], metadata: { requirementKey: "k", description: "d", source: "camera", capturedAt: "now" } })).rejects.toMatchObject({ code: "INVALID_STATE" });
    expect(metadataAttempts).toBe(1);
    vi.unstubAllGlobals();
  });

  it("keeps the draft resumable after exhausting media processing retries", async () => {
    let metadataAttempts = 0;
    const fetch = vi.fn().mockImplementation(async (_input: string, init?: RequestInit) => {
      const body = String(init?.body ?? "");
      if (body.includes("saveCaptureMetadata")) {
        metadataAttempts += 1;
        return new Response(JSON.stringify({ errors: [{ message: "media verification and screening are pending", extensions: { code: "INVALID_STATE", field: "mediaId" } }] }), { status: 200 });
      }
      return new Response(JSON.stringify({ data: { completeMediaUpload: { media: { id: "media", status: "VERIFIED" }, userErrors: [] } } }), { status: 200 });
    });
    vi.stubGlobal("fetch", fetch);

    const promise = uploadDraft({ id: "retry-limit", responsibilityId: "r", blob: new Blob(["image"], { type: "image/jpeg" }), sha256: "hash", mediaId: "media", uploadId: "upload", parts: [{ number: 1, complete: true, etag: "done" }], metadata: { requirementKey: "k", description: "d", source: "camera", capturedAt: "now" } }, undefined, { wait: async () => undefined });
    await expect(promise).rejects.toMatchObject({ code: "INVALID_STATE" });
    expect(metadataAttempts).toBe(30);
    expect((await loadDraft("retry-limit"))?.metadataSaved).not.toBe(true);
    vi.unstubAllGlobals();
  });

  it("requests and persists SCREENED metadata status for the false-positive flow", async () => {
    const fetch = vi.fn().mockImplementation((_input: string, init?: RequestInit) => {
      const body = String(init?.body ?? "");
      const data = body.includes("saveCaptureMetadata")
        ? { saveCaptureMetadata: { media: { id: "media", status: "SCREENED", replacesMediaId: "blocked-media" }, userErrors: [] } }
        : { completeMediaUpload: { userErrors: [] } };
      return new Response(JSON.stringify({ data }), { status: 200 });
    });
    const draft: CaptureDraft = { id: "screened", responsibilityId: "r", blob: new Blob(["image"], { type: "image/jpeg" }), sha256: "hash", mediaId: "media", uploadId: "upload", parts: [{ number: 1, complete: true, etag: "done" }], metadata: { requirementKey: "k", description: "d", source: "camera", capturedAt: "now" } };
    vi.stubGlobal("fetch", fetch);

    const result = await uploadDraft(draft);
    const saved = await loadDraft("screened");
    const metadataRequest = fetch.mock.calls.map((call) => JSON.parse(String(call[1]?.body))).find((body) => String(body.query).includes("saveCaptureMetadata"));

    expect(metadataRequest.query).toContain("media {");
    expect(result.mediaStatus).toBe("SCREENED");
    expect(result.replacesMediaId).toBe("blocked-media");
    expect(saved?.mediaStatus).toBe("SCREENED");
    expect(saved?.replacesMediaId).toBe("blocked-media");
    vi.unstubAllGlobals();
  });

  it("sends the draft source and optional capture metadata unchanged", async () => {
    const fetch = vi.fn().mockImplementation(() => new Response(JSON.stringify({ data: { completeMediaUpload: { userErrors: [] }, saveCaptureMetadata: { userErrors: [] } } }), { status: 200 }));
    const gps = { latitude: -23.55, longitude: -46.63, accuracyMeters: 8, capturedAt: "2026-09-09T10:00:00.000Z", windowStartedAt: "2026-09-09T10:00:00.000Z" };
    const deviceContext = { platform: "test", language: "pt-BR" };
    vi.stubGlobal("fetch", fetch);
    await uploadDraft({ id: "metadata", responsibilityId: "r", blob: new Blob(["image"], { type: "image/jpeg" }), sha256: "hash", mediaId: "media", uploadId: "upload", parts: [{ number: 1, complete: true, etag: "done" }], metadata: { requirementKey: "k", description: "", source: "gallery", capturedAt: gps.capturedAt, gps, deviceContext } });
    const metadataRequest = fetch.mock.calls.map((call) => JSON.parse(String(call[1]?.body))).find((body) => String(body.query).includes("saveCaptureMetadata"));
    expect(metadataRequest.variables).toMatchObject({ input: { captureSource: "GALLERY", gps, deviceContext } });
    vi.unstubAllGlobals();
  });

  it("serializes uploads across tabs when Web Locks are unavailable", async () => {
    let createCalls = 0;
    let createRelease: (() => void) | undefined;
    const createStarted = new Promise<void>((resolve) => { createRelease = resolve; });
    const fetch = vi.fn().mockImplementation(async (_input: string, init?: RequestInit) => {
      if (init?.method === "PUT") return new Response(null, { status: 200, headers: { etag: "etag" } });
      const body = String(init?.body ?? "");
      if (body.includes("createMediaUpload")) {
        createCalls += 1;
        createRelease?.();
        await new Promise((resolve) => setTimeout(resolve, 20));
        return new Response(JSON.stringify({ data: { createMediaUpload: { upload: { mediaId: "media", uploadId: "upload" }, userErrors: [] } } }), { status: 200 });
      }
      return new Response(JSON.stringify({ data: {
        presignMediaParts: { parts: [{ partNumber: 1, url: "https://upload.test/part" }], userErrors: [] },
        completeMediaUpload: { userErrors: [] }, saveCaptureMetadata: { userErrors: [] }
      } }), { status: 200 });
    });
    const draft: CaptureDraft = { id: "cross-tab", responsibilityId: "r", blob: new Blob(["image"], { type: "image/jpeg" }), sha256: "hash", parts: [{ number: 1, complete: false }], metadata: { requirementKey: "k", description: "d", source: "camera", capturedAt: "now" } };
    vi.stubGlobal("fetch", fetch);
    const first = uploadDraft(draft);
    await createStarted;
    const second = uploadDraft(draft);
    await Promise.all([first, second]);
    expect(createCalls).toBe(1);
    vi.unstubAllGlobals();
  });
});
