import "fake-indexeddb/auto";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { audioDraftsChangedEvent, deleteAudioDraft, listAudioDrafts, loadAudioDraft, removeAudioDraftsForResponsibility, saveAudioDraft, updateAudioDraft, type AudioDraft } from "@/pwa/audio-drafts";

const blob = () => new Blob(["voice"], { type: "audio/webm" });
const draft = (overrides: Partial<AudioDraft> = {}): AudioDraft => ({ id: "note-1", responsibilityId: "responsibility-1", requirementKey: "env:living", disclosureVersion: "pt-BR-1", consentVersion: "voice-v1", target: { kind: "PHOTO", photoDraftId: "photo-draft-1" }, durationMs: 800, contentType: "audio/webm", sizeBytes: 5, sha256: "a".repeat(64), state: "PENDING_UPLOAD", audio: blob(), createdAt: "2026-11-01T00:00:00Z", ...overrides });

describe("local audio drafts", () => {
  beforeEach(async () => {
    await new Promise<void>((resolve, reject) => { const request = indexedDB.deleteDatabase("inspection-audio-notes-v1"); request.onsuccess = () => resolve(); request.onerror = () => reject(request.error); });
  });

  it("IT-070 restores a note with its photo anchor after a reload", async () => {
    await saveAudioDraft(draft());
    const [restored] = await listAudioDrafts("responsibility-1");
    expect(restored).toMatchObject({ id: "note-1", state: "PENDING_UPLOAD", target: { kind: "PHOTO", photoDraftId: "photo-draft-1" } });
    expect(restored.audio?.size).toBe(5);
  });

  it("never copies a note between invitation responsibilities", async () => {
    await saveAudioDraft(draft());
    expect(await listAudioDrafts("responsibility-2")).toEqual([]);
    expect(await loadAudioDraft("note-1", "responsibility-2")).toBeUndefined();
    await deleteAudioDraft("note-1", "responsibility-2");
    await updateAudioDraft({ ...draft({ responsibilityId: "responsibility-2" }), state: "FAILED" });
    expect(await listAudioDrafts("responsibility-1")).toHaveLength(1);
    expect((await listAudioDrafts("responsibility-1"))[0].state).toBe("PENDING_UPLOAD");
  });

  it("IT-075 lists nothing when no note exists", async () => {
    expect(await listAudioDrafts("responsibility-1")).toEqual([]);
  });

  it("IT-074 keeps a note with incomplete local bytes visible as unsendable", async () => {
    await saveAudioDraft(draft());
    await new Promise<void>((resolve, reject) => {
      const open = indexedDB.open("inspection-audio-notes-v1");
      open.onsuccess = () => {
        const tx = open.result.transaction("audioDrafts", "readwrite");
        const store = tx.objectStore("audioDrafts");
        const get = store.get("note-1");
        get.onsuccess = () => store.put({ ...get.result, audioBytes: new ArrayBuffer(2) });
        tx.oncomplete = () => { open.result.close(); resolve(); };
        tx.onerror = () => reject(tx.error);
      };
    });
    const [restored] = await listAudioDrafts("responsibility-1");
    expect(restored).toMatchObject({ id: "note-1", state: "UNSENDABLE", failureCode: "LOCAL_AUDIO_UNAVAILABLE" });
    expect(restored.statusMessage).toMatch(/incompleto/);
  });

  it("IT-071 drops the bytes when the receipt is durable and keeps only the metadata", async () => {
    await saveAudioDraft(draft());
    await updateAudioDraft({ ...draft(), state: "RECEIVED", noteId: "server-note" });
    const [received] = await listAudioDrafts("responsibility-1");
    expect(received).toMatchObject({ state: "RECEIVED", noteId: "server-note", target: { kind: "PHOTO", photoDraftId: "photo-draft-1" } });
    expect(received.audio).toBeUndefined();
  });

  it("deletes one note, and every note of a closed responsibility without touching another", async () => {
    await saveAudioDraft(draft({ id: "a" }));
    await saveAudioDraft(draft({ id: "b" }));
    await saveAudioDraft(draft({ id: "c", responsibilityId: "responsibility-2" }));
    await deleteAudioDraft("a", "responsibility-1");
    expect((await listAudioDrafts("responsibility-1")).map((item) => item.id)).toEqual(["b"]);
    await removeAudioDraftsForResponsibility("responsibility-1");
    expect(await listAudioDrafts("responsibility-1")).toEqual([]);
    expect((await listAudioDrafts("responsibility-2")).map((item) => item.id)).toEqual(["c"]);
  });

  it("IT-078 announces local changes so every open list converges", async () => {
    const changed = vi.fn();
    window.addEventListener(audioDraftsChangedEvent, changed);
    await saveAudioDraft(draft());
    await updateAudioDraft({ ...draft(), state: "FAILED" });
    await deleteAudioDraft("note-1", "responsibility-1");
    window.removeEventListener(audioDraftsChangedEvent, changed);
    expect(changed.mock.calls.length).toBeGreaterThanOrEqual(3);
  });

  it("rejects a note that fits no anchor, duration or size contract", async () => {
    await expect(saveAudioDraft(draft({ target: { kind: "PHOTO" } }))).rejects.toThrow();
    await expect(saveAudioDraft(draft({ durationMs: 499 }))).rejects.toThrow();
    await expect(saveAudioDraft(draft({ durationMs: 60_001 }))).rejects.toThrow();
    await expect(saveAudioDraft(draft({ target: { kind: "VIDEO", attemptId: "a", startMs: 5, endMs: 5, timeBasis: "CAPTURE_CLOCK" } }))).rejects.toThrow();
  });
});
