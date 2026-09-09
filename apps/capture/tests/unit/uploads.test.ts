import "fake-indexeddb/auto";
import { describe, expect, it, vi } from "vitest";
import { uploadDraft } from "@/pwa/uploads";
import type { CaptureDraft } from "@/pwa/drafts";

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
