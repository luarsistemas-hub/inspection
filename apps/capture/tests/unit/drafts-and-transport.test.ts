import "fake-indexeddb/auto";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { clearCaptureCsrfToken, clearCaptureLinkToken, getCaptureLinkToken, setCaptureCsrfToken, setCaptureLinkToken } from "@/auth/capture-session";
import { graphql } from "@/graphql/client";
import { type CaptureDraft, hasDraftCapacity, loadDraftsForResponsibility, readyForSubmission, removeDraftsForResponsibility, saveDraft } from "@/pwa/drafts";

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
  it("UT-063 removes actionable local state when bootstrap is final", async () => { await saveDraft(draft()); await removeDraftsForResponsibility("responsibility-1"); expect(await loadDraftsForResponsibility("responsibility-1")).toEqual([]); });
  it("UT-065 warns before the storage quota threshold", async () => { vi.stubGlobal("navigator", { storage: { estimate: vi.fn().mockResolvedValue({ quota: 100, usage: 90 }) } }); expect(await hasDraftCapacity(1)).toBe(false); vi.unstubAllGlobals(); });
  it("UT-066 blocks finalization while any media is not verified", () => { expect(readyForSubmission([draft()], true)).toBe(false); expect(readyForSubmission([{ ...draft(), mediaId: "m", parts: [{ number: 1, complete: true, etag: "e" }] }], true)).toBe(true); });
  it("allows an online submission with no media when every required answer is impossible", () => { expect(readyForSubmission([], true, true)).toBe(true); expect(readyForSubmission([{ ...draft(), mediaId: "m", metadataSaved: false, parts: [{ number: 1, complete: true, etag: "e" }] }], true, true)).toBe(false); });
  it("UT-064 and UT-073 send only external CSRF with the session cookie", async () => { setCaptureCsrfToken("csrf-proof"); const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { ok: true } }), { status: 200 })); vi.stubGlobal("fetch", fetch); await graphql("query Test { ok }"); expect(fetch).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ credentials: "include", headers: expect.objectContaining({ "X-CSRF-Token": "csrf-proof" }) })); expect(fetch.mock.calls[0][1].headers.Authorization).toBeUndefined(); vi.unstubAllGlobals(); });
});
