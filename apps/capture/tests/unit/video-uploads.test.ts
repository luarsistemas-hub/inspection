import "fake-indexeddb/auto";
import { Blob as NodeBlob } from "node:buffer";
import { afterEach, describe, expect, it, vi } from "vitest";
import { appendVideoChunk, loadVideoDraft, saveVideoDraft, type VideoDraft } from "@/pwa/video-drafts";
import { refreshVideoReceipt, uploadVideoDraft } from "@/pwa/video-uploads";

const draft = (id: string): VideoDraft => ({ id, responsibilityId: "r", kind: "INSPECTION", source: "CAMERA", contentType: "video/webm", maxDurationSeconds: 900, maxBytes: 1 << 30, expiresAt: "2099-01-01T00:00:00Z", state: "CONFIRMED", chunkCount: 3, sizeBytes: 3, sha256: "a".repeat(64), mediaId: `media-${id}`, uploadId: `upload-${id}`, partSizeBytes: 1, parts: [{ number: 1, etag: "etag-1" }] });

async function prepare(item: VideoDraft) {
  for (let sequence = 0; sequence < 3; sequence += 1) await appendVideoChunk(item.id, sequence, new NodeBlob(["abc"[sequence]]));
  await saveVideoDraft(item);
}

describe("video multipart transport", () => {
  afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

  it("resumes only parts missing from the authoritative server state", async () => {
    const item = { ...draft("missing-parts"), parts: [] }; await prepare(item);
    const puts: string[] = [];
    vi.stubGlobal("fetch", vi.fn().mockImplementation(async (url: string, init: RequestInit) => {
      if (init.method === "PUT") { puts.push(url); return new Response(null, { status: 200, headers: { etag: `etag-${url.at(-1)}` } }); }
      const request = JSON.parse(String(init.body)) as { query: string; variables: { input?: { partNumbers?: number[] } } };
      const q = request.query;
      if (q.includes("videoUploadStatus")) return Response.json({ data: { videoUploadStatus: { mediaId: item.mediaId, status: "UPLOADING", expiresAt: item.expiresAt, receivedParts: [1], confirmedParts: [{ partNumber: 1, etag: "etag-1", sizeBytes: 1 }], missingParts: [2, 3] } } });
      if (q.includes("presignMediaParts")) { expect(request.variables.input?.partNumbers).toEqual([2, 3]); return Response.json({ data: { presignMediaParts: { parts: [{ partNumber: 2, url: "https://storage.test/part-2", expiresAt: item.expiresAt }, { partNumber: 3, url: "https://storage.test/part-3", expiresAt: item.expiresAt }], userErrors: [] } } }); }
      if (q.includes("completeMediaUpload")) { expect(request.variables.input).toMatchObject({ parts: [{ partNumber: 1, etag: "etag-1" }, { partNumber: 2, etag: "etag-2" }, { partNumber: 3, etag: "etag-3" }] }); return Response.json({ data: { completeMediaUpload: { media: { id: item.mediaId, status: "RECEIVED" }, userErrors: [] } } }); }
      if (q.includes("saveCaptureMetadata")) return Response.json({ data: { saveCaptureMetadata: { media: { id: item.mediaId, status: "RECEIVED" }, userErrors: [] } } });
      throw new Error(`Unexpected GraphQL operation ${q}`);
    }));
    const result = await uploadVideoDraft(item);
    expect(puts).toEqual(["https://storage.test/part-2", "https://storage.test/part-3"]);
    expect(result.state).toBe("RECEIVED");
    expect(result.mediaId).toBe(item.mediaId);
  });

  it("reconciles a lost final completion response and reuses one media identity", async () => {
    const item = { ...draft("lost-completion"), parts: [{ number: 1, etag: "etag-1" }, { number: 2, etag: "etag-2" }, { number: 3, etag: "etag-3" }] };
    await prepare(item);
    let statusCalls = 0;
    let completes = 0;
    vi.stubGlobal("fetch", vi.fn().mockImplementation(async (_url: string, init: RequestInit) => {
      const request = JSON.parse(String(init.body)) as { query: string };
      const q = request.query;
      if (q.includes("videoUploadStatus")) { statusCalls += 1; return Response.json({ data: { videoUploadStatus: { mediaId: item.mediaId, status: statusCalls === 1 ? "UPLOADING" : "RECEIVED", expiresAt: item.expiresAt, receivedParts: [1, 2, 3], confirmedParts: [1, 2, 3].map((partNumber) => ({ partNumber, etag: `etag-${partNumber}`, sizeBytes: 1 })), missingParts: [] } } }); }
      if (q.includes("completeMediaUpload")) { completes += 1; throw new TypeError("connection lost after commit"); }
      if (q.includes("saveCaptureMetadata")) return Response.json({ data: { saveCaptureMetadata: { media: { id: item.mediaId, status: "RECEIVED" }, userErrors: [] } } });
      throw new Error(`Unexpected GraphQL operation ${q}`);
    }));
    const [first, second] = await Promise.all([uploadVideoDraft(item), uploadVideoDraft(item)]);
    expect(first.mediaId).toBe(item.mediaId);
    expect(second.mediaId).toBe(item.mediaId);
    expect(completes).toBe(1);
    expect((await loadVideoDraft(item.id))?.state).toBe("RECEIVED");
  });

  it("refreshes a received receipt before preparation has a job", async () => {
    const item = { ...draft("received-before-submission"), state: "RECEIVED" as const };
    await prepare(item);
    let preparationQueries = 0;
    vi.stubGlobal("fetch", vi.fn().mockImplementation(async (_url: string, init: RequestInit) => {
      const query = (JSON.parse(String(init.body)) as { query: string }).query;
      if (query.includes("videoPreparation")) { preparationQueries += 1; return Response.json({ errors: [{ message: "video preparation not found", extensions: { code: "NOT_FOUND" } }], data: null }); }
      if (query.includes("videoUploadStatus")) return Response.json({ data: { videoUploadStatus: { mediaId: item.mediaId, status: "RECEIVED", expiresAt: item.expiresAt, receivedParts: [], confirmedParts: [], missingParts: [] } } });
      throw new Error(`Unexpected GraphQL operation ${query}`);
    }));
    const refreshed = await refreshVideoReceipt(item);
    // Without a preparation job the receipt stays authoritative.
    expect(refreshed.preparationStatus).toBe("RECEIVED");
    expect(preparationQueries).toBe(1);
  });

  it("IT-122 surfaces a failed preparation behind a RECEIVED receipt", async () => {
    const item = { ...draft("received-checksum-mismatch"), state: "RECEIVED" as const };
    await prepare(item);
    vi.stubGlobal("fetch", vi.fn().mockImplementation(async (_url: string, init: RequestInit) => {
      const query = (JSON.parse(String(init.body)) as { query: string }).query;
      if (query.includes("videoPreparation")) return Response.json({ data: { videoPreparation: { mediaId: item.mediaId, stage: "FAILED", errorCode: "CHECKSUM_MISMATCH", retryable: false, processedFrames: 0, sourceAvailable: false } } });
      if (query.includes("videoUploadStatus")) return Response.json({ data: { videoUploadStatus: { mediaId: item.mediaId, status: "RECEIVED", expiresAt: item.expiresAt, receivedParts: [], confirmedParts: [], missingParts: [] } } });
      throw new Error(`Unexpected GraphQL operation ${query}`);
    }));
    const refreshed = await refreshVideoReceipt(item);
    expect(refreshed.preparationStatus).toBe("CHECKSUM_MISMATCH");
  });
});
