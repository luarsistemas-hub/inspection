import { describe, expect, it } from "vitest";
import { presentDraftPersistenceFailure, presentUploadProgress } from "../../app/capture/[linkToken]/presentation";
import { createDraftResumeGuard, isGalleryAllowed, isTerminalBootstrapStatus, mergeUploadedAnswer, requirementsSatisfied, updateImpossibleAnswer } from "@/pwa/capture-policy";
import { readyForSubmission, type CaptureDraft } from "@/pwa/drafts";

const requirement = {
  key: "front", section: "property", label: "Frente", instructions: null, required: true,
  minimumMedia: 1, maximumMedia: 1, descriptionRequired: false,
  captureSourcePolicy: "ANY", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: true
};

describe("Capture migration contracts", () => {
  it("UT-059 allows the gallery when responsibility and requirement policy permit it", () => {
    expect(isGalleryAllowed({ allowGallery: true }, requirement)).toBe(true);
  });

  it("UT-060 refuses the gallery for a camera-only requirement", () => {
    expect(isGalleryAllowed({ allowGallery: true }, { ...requirement, captureSourcePolicy: "CAMERA_ONLY" })).toBe(false);
  });

  it("UT-061 gates every terminal invitation status and permits an active status", () => {
    for (const status of ["EXPIRED", "REVOKED", "COMPLETED", "INVALIDATED"]) expect(isTerminalBootstrapStatus(status)).toBe(true);
    expect(isTerminalBootstrapStatus("OPEN")).toBe(false);
  });

  it("UT-062 replaces blocked front evidence without retaining it as ready evidence", () => {
    const answers = [{ requirementKey: "front", mediaIds: ["blocked-media"], impossibilityReason: null, version: 2 }];
    expect(mergeUploadedAnswer(answers, "front", "replacement-media", false, "blocked-media")).toEqual([
      { requirementKey: "front", mediaIds: ["replacement-media"], impossibilityReason: null, version: 3 }
    ]);
  });

  it("UT-063 preserves the answer version when the impossibility reason is unchanged", () => {
    const answers = [{ requirementKey: "front", mediaIds: [], impossibilityReason: "Acesso bloqueado", version: 4 }];
    expect(updateImpossibleAnswer(answers, "front", "Acesso bloqueado")[0].version).toBe(4);
  });

  it("UT-064 explains quota failure without announcing the preview as saved", () => {
    const message = presentDraftPersistenceFailure({ name: "QuotaExceededError" });
    expect(message).toContain("ainda não foi salva");
    expect(message).not.toMatch(/foto salva neste dispositivo/i);
  });

  it("UT-065 keeps a 100 percent transfer in server verification until readiness is returned", () => {
    expect(presentUploadProgress({ phase: "verifying", percent: 100, completedParts: 1, totalParts: 1 })).toBe("Aguardando verificação da foto");
    const processing: CaptureDraft = { id: "draft", responsibilityId: "responsibility", blob: new Blob(["photo"], { type: "image/jpeg" }), sha256: "hash", mediaId: "media", uploadId: "upload", mediaStatus: "PROCESSING", metadataSaved: true, parts: [{ number: 1, complete: true, etag: "etag" }], metadata: { requirementKey: "front", description: "", source: "camera", capturedAt: "now" } };
    expect(readyForSubmission([processing], true)).toBe(false);
    expect(requirementsSatisfied([requirement], [], [processing])).toBe(false);
  });

  it("UT-066 admits only one concurrent resume for a draft", async () => {
    const guard = createDraftResumeGuard();
    let uploadCount = 0;
    const upload = async () => { uploadCount += 1; await Promise.resolve(); };
    const activate = async () => {
      if (!guard.start("draft-1")) return;
      try { await upload(); } finally { guard.finish("draft-1"); }
    };

    await Promise.all([activate(), activate()]);

    expect(uploadCount).toBe(1);
    expect(guard.start("draft-1")).toBe(true);
  });
});
