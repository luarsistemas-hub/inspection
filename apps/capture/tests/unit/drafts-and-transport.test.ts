import "fake-indexeddb/auto";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { clearCaptureCsrfToken, clearCaptureLinkToken, getCaptureCsrfToken, getCaptureLinkToken, setCaptureCsrfToken, setCaptureLinkToken } from "@/auth/capture-session";
import { graphql } from "@/graphql/client";
import { ExternalCaptureBootstrapDocument } from "@/graphql/generated";
import { isBlockedMediaStatus, isFalsePositiveActionable, isGalleryAllowed, isTerminalBootstrapStatus, requirementsSatisfied } from "@/pwa/capture-policy";
import { type CaptureDraft, hasDraftCapacity, loadDraft, loadDraftsForResponsibility, mediaCountForRequirement, persistDraftMediaStatus, readyForSubmission, removeDraftsForResponsibility, saveDraft } from "@/pwa/drafts";

const draft = (id = "draft-1"): CaptureDraft => ({ id, responsibilityId: "responsibility-1", blob: new Blob(["photo"], { type: "image/jpeg" }), sha256: "hash", parts: [{ number: 1, complete: false }], metadata: { requirementKey: "front", description: "Fachada", source: "camera", capturedAt: "2026-09-07T00:00:00Z" } });

describe("Capture drafts and transport", () => {
  beforeEach(async () => { clearCaptureCsrfToken(); clearCaptureLinkToken(); await removeDraftsForResponsibility("responsibility-1"); });

  it("restores the link token across a reload within the same tab", () => {
    setCaptureLinkToken("invite-token");
    expect(getCaptureLinkToken()).toBe("invite-token");
    clearCaptureLinkToken();
    expect(getCaptureLinkToken()).toBeUndefined();
  });

  it("UT-059 restores only a valid responsibility-scoped draft", async () => {
    await saveDraft(draft()); await saveDraft({ ...draft("other"), responsibilityId: "other" });
    expect(await loadDraftsForResponsibility("responsibility-1")).toHaveLength(1);
  });
  it("UT-060 ignores corrupt or version-mismatched data", async () => {
    const request = indexedDB.open("inspection-capture-v3");
    const db = await new Promise<IDBDatabase>((resolve) => { request.onsuccess = () => resolve(request.result); });
    const tx = db.transaction("drafts", "readwrite"); tx.objectStore("drafts").put({ id: "corrupt", responsibilityId: "responsibility-1", schemaVersion: 99 }); await new Promise<void>((resolve) => { tx.oncomplete = () => resolve(); }); db.close();
    expect(await loadDraftsForResponsibility("responsibility-1")).toEqual([]);
  });
  it("quarantines drafts with malformed parts or metadata before recovery", async () => {
    const request = indexedDB.open("inspection-capture-v3");
    const db = await new Promise<IDBDatabase>((resolve) => { request.onsuccess = () => resolve(request.result); });
    const tx = db.transaction("drafts", "readwrite");
    tx.objectStore("drafts").put({ id: "malformed", responsibilityId: "responsibility-1", schemaVersion: 1, blob: new Blob(["photo"], { type: "image/jpeg" }), sha256: "hash", parts: [], metadata: {} });
    await new Promise<void>((resolve) => { tx.oncomplete = () => resolve(); });
    db.close();

    expect(await loadDraft("malformed")).toBeUndefined();
    const quarantineRequest = indexedDB.open("inspection-capture-v3");
    const quarantineDb = await new Promise<IDBDatabase>((resolve) => { quarantineRequest.onsuccess = () => resolve(quarantineRequest.result); });
    const quarantineTx = quarantineDb.transaction(["drafts", "quarantine"], "readonly");
    const remaining = quarantineTx.objectStore("drafts").get("malformed");
    const quarantined = quarantineTx.objectStore("quarantine").getAll();
    const [remainingValue, quarantinedValues] = await Promise.all([
      new Promise<unknown>((resolve) => { remaining.onsuccess = () => resolve(remaining.result); }),
      new Promise<unknown[]>((resolve) => { quarantined.onsuccess = () => resolve(quarantined.result); }),
    ]);
    quarantineDb.close();
    expect(remainingValue).toBeUndefined();
    expect(quarantinedValues).toEqual(expect.arrayContaining([expect.objectContaining({ value: expect.objectContaining({ id: "malformed" }) })]));
  });
  it("restores a valid pre-upload offline draft with no multipart parts yet", async () => {
    const offlineDraft = { ...draft("offline"), parts: [], metadataSaved: false };
    await saveDraft(offlineDraft);
    expect(await loadDraft("offline")).toEqual(expect.objectContaining({ id: "offline", responsibilityId: offlineDraft.responsibilityId, parts: [], metadataSaved: false, schemaVersion: 1 }));
  });
  it("UT-063 removes actionable local state when bootstrap is final", async () => { await saveDraft(draft()); await removeDraftsForResponsibility("responsibility-1"); expect(await loadDraftsForResponsibility("responsibility-1")).toEqual([]); });
  it("persists the post-false-positive media status so SCREENED is no longer actionable", async () => { const screened = { ...draft(), mediaId: "media-1", mediaStatus: "SCREENED" }; await saveDraft(screened); await persistDraftMediaStatus(screened, "READY"); expect((await loadDraft("draft-1"))?.mediaStatus).toBe("READY"); });
  it("UT-065 warns before the storage quota threshold", async () => { vi.stubGlobal("navigator", { storage: { estimate: vi.fn().mockResolvedValue({ quota: 100, usage: 90 }) } }); expect(await hasDraftCapacity(1)).toBe(false); vi.unstubAllGlobals(); });
  it("UT-066 blocks finalization while any media is not verified", () => { expect(readyForSubmission([draft()], true)).toBe(false); expect(readyForSubmission([{ ...draft(), mediaId: "m", metadataSaved: true, parts: [{ number: 1, complete: true, etag: "e" }] }], true)).toBe(true); });
  it("allows an online submission with no media when every required answer is impossible", () => { expect(readyForSubmission([], true, true)).toBe(true); expect(readyForSubmission([{ ...draft(), mediaId: "m", metadataSaved: false, parts: [{ number: 1, complete: true, etag: "e" }] }], true, true)).toBe(false); });
  it("allows an explicitly confirmed incomplete submission while retaining upload guards", () => { expect(readyForSubmission([], true, false, true)).toBe(true); expect(readyForSubmission([{ ...draft(), mediaId: "m", metadataSaved: false, parts: [{ number: 1, complete: true, etag: "e" }] }], true, false, true)).toBe(false); expect(readyForSubmission([], false, false, true)).toBe(false); });
  it("counts answered and pending media once when enforcing a requirement maximum", () => {
    const answered = [{ requirementKey: "front", mediaIds: ["media-1"] }];
    const drafts = [{ ...draft(), metadata: { ...draft().metadata, requirementKey: "front" }, mediaId: "media-1" }, { ...draft("offline"), metadata: { ...draft().metadata, requirementKey: "front" }, mediaId: undefined }];
    expect(mediaCountForRequirement("front", answered, drafts)).toBe(2);
  });
  it("blocks terminal bootstrap states before capture", () => {
    expect(isTerminalBootstrapStatus("EXPIRED")).toBe(true);
    expect(isTerminalBootstrapStatus("REVOKED")).toBe(true);
    expect(isTerminalBootstrapStatus("COMPLETED")).toBe(true);
    expect(isTerminalBootstrapStatus("INVALIDATED")).toBe(true);
    expect(isTerminalBootstrapStatus("OPEN")).toBe(false);
  });
  it("enforces every required requirement's minimum media count", () => {
    const requirements = [{ key: "front", section: "A", label: "Frente", instructions: null, required: true, minimumMedia: 2, maximumMedia: 3, descriptionRequired: false, captureSourcePolicy: "ANY", impossibilityAllowed: false }];
    const answers = [{ requirementKey: "front", mediaIds: ["media-1"], impossibilityReason: null, version: 0 }];
    expect(requirementsSatisfied(requirements, answers, [])).toBe(false);
    expect(requirementsSatisfied(requirements, answers, [{ ...draft(), metadata: { ...draft().metadata, requirementKey: "front" }, mediaId: "media-2" }])).toBe(true);
  });
  it("does not treat server-screened media as a satisfied requirement or submission-ready", () => {
    const requirements = [{ key: "front", section: "A", label: "Frente", instructions: null, required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "ANY", impossibilityAllowed: false }];
    const screened = { ...draft(), mediaId: "screened", mediaStatus: "SCREENED", metadataSaved: true, parts: [{ number: 1, complete: true, etag: "etag" }] };
    expect(isBlockedMediaStatus(screened.mediaStatus)).toBe(true);
    expect(requirementsSatisfied(requirements, [{ requirementKey: "front", mediaIds: ["screened"], impossibilityReason: null, version: 0 }], [screened])).toBe(false);
    expect(readyForSubmission([screened], true, true)).toBe(false);
  });
  it("offers false-positive declaration only for SCREENED media", () => {
    expect(isFalsePositiveActionable("SCREENED")).toBe(true);
    expect(isFalsePositiveActionable("REJECTED")).toBe(false);
    expect(isFalsePositiveActionable("PURGED")).toBe(false);
    expect(isFalsePositiveActionable("ABORTED")).toBe(false);
  });
  it("allows gallery only when both policy and requirement permit it", () => {
    const requirement = { key: "front", section: "A", label: "Frente", instructions: null, required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "ANY", impossibilityAllowed: false };
    expect(isGalleryAllowed({ allowGallery: true }, requirement)).toBe(true);
    expect(isGalleryAllowed({ allowGallery: false }, requirement)).toBe(false);
    expect(isGalleryAllowed({ allowGallery: true }, { ...requirement, captureSourcePolicy: "CAMERA_ONLY" })).toBe(false);
  });
  it("UT-064 and UT-073 send only external CSRF with the session cookie", async () => { setCaptureCsrfToken("csrf-proof"); const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { externalCapture: {} } }), { status: 200 })); vi.stubGlobal("fetch", fetch); await graphql(ExternalCaptureBootstrapDocument); expect(fetch).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ credentials: "include", headers: expect.objectContaining({ "X-CSRF-Token": "csrf-proof" }) })); expect(fetch.mock.calls[0][1].headers.Authorization).toBeUndefined(); vi.unstubAllGlobals(); });
  it("clears capture authority but preserves the link for OTP reauthentication when GraphQL reports an expired session in a 200 response", async () => {
    setCaptureCsrfToken("csrf-proof"); setCaptureLinkToken("invite-token");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ errors: [{ message: "expired", extensions: { code: "SESSION_EXPIRED" } }] }), { status: 200 })));
    await expect(graphql(ExternalCaptureBootstrapDocument)).rejects.toMatchObject({ code: "SESSION_EXPIRED" });
    expect(getCaptureCsrfToken()).toBeUndefined(); expect(getCaptureLinkToken()).toBe("invite-token");
    vi.unstubAllGlobals();
  });
});
