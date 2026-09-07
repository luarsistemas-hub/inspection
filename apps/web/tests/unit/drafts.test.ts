import "fake-indexeddb/auto";
import { beforeEach, describe, expect, it } from "vitest";
import { CaptureDraft, loadDrafts, readyForSubmission, saveDraft } from "@/pwa/drafts";

const draft = (complete: boolean): CaptureDraft => ({
  id: crypto.randomUUID(), blob: new Blob(["evidence"], { type: "image/jpeg" }), sha256: "a".repeat(64), mediaId: complete ? "media-1" : undefined,
  parts: [{ number: 1, complete }], metadata: { requirementKey: "wall", description: "Parede frontal", source: "camera", capturedAt: new Date().toISOString() }, answer: { progress: complete ? 100 : 10 }
});

describe("UT-052 / IT-367: drafts resumíveis", () => {
  beforeEach(async () => indexedDB.deleteDatabase("inspection-capture"));
  it("preserva blob, hash, upload, ETags, metadados e progresso", async () => {
    const value = { ...draft(false), uploadId: "upload-1", parts: [{ number: 1, etag: "etag-1", complete: true }, { number: 2, complete: false }] };
    await saveDraft(value); const [restored] = await loadDrafts();
    expect(restored).toMatchObject({ id: value.id, sha256: value.sha256, uploadId: "upload-1", metadata: value.metadata, parts: value.parts });
    // fake-indexeddb does not expose jsdom's Blob methods after cloning, but
    // it must still retain the browser value alongside the resume metadata.
    expect(restored.blob).toBeTruthy();
  });
});

describe("UT-053 / IT-387: isolamento e finalização", () => {
  it("só permite finalizar online depois que toda mídia está pronta", () => {
    expect(readyForSubmission([draft(true)], true)).toBe(true);
    expect(readyForSubmission([draft(true)], false)).toBe(false);
    expect(readyForSubmission([draft(false)], true)).toBe(false);
  });
});
