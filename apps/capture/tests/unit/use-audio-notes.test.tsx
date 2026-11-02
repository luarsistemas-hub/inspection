import "fake-indexeddb/auto";
import React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createElement, mount, settle, silenceActWarnings, unmountAll, waitFor } from "./support/dom";
import { deleteAudioDraft, saveAudioDraft, updateAudioDraft, type AudioDraft } from "@/pwa/audio-drafts";
import { useAudioNotes, type AudioNotesState } from "@/pwa/use-audio-notes";

const api = vi.hoisted(() => ({ graphql: vi.fn(), resume: vi.fn() }));
vi.mock("@/graphql/client", () => ({ graphql: api.graphql }));
vi.mock("@/pwa/audio-uploads", () => ({ resumeAudioDrafts: api.resume }));

const draft = (overrides: Partial<AudioDraft> = {}): AudioDraft => ({ id: "note-1", responsibilityId: "responsibility-1", requirementKey: "env:living", disclosureVersion: "pt-BR-1", consentVersion: "voice-v1", target: { kind: "PHOTO", photoDraftId: "p1", mediaId: "photo-1" }, durationMs: 800, contentType: "audio/webm", sizeBytes: 5, sha256: "a".repeat(64), state: "RECEIVED", noteId: "server-1", createdAt: "2026-11-01T00:00:00Z", ...overrides });
const serverNote = (status: string, audioDeletionStatus = "PENDING", audioAvailability = "AVAILABLE") => ({ audioNote: { id: "server-1", status, version: 1, revisions: [], highlighted: false, textConfirmed: false, noSpeech: false, anchorStatus: "VALID", audioAvailability, audioDeletionStatus } });

let latest: AudioNotesState;
function Harness(props: { online: boolean; enabled?: boolean }) {
  latest = useAudioNotes({ responsibilityId: "responsibility-1", online: props.online, enabled: props.enabled ?? true });
  return React.createElement("p", null, latest.items.map((item) => `${item.draft.id}:${item.note?.status ?? "-"}`).join(","));
}
const text = (container: HTMLElement) => container.textContent ?? "";

describe("useAudioNotes", () => {
  beforeEach(async () => {
    silenceActWarnings();
    api.graphql.mockReset(); api.resume.mockReset().mockResolvedValue([]);
    await new Promise<void>((resolve, reject) => { const request = indexedDB.deleteDatabase("inspection-audio-notes-v1"); request.onsuccess = () => resolve(); request.onerror = () => reject(request.error); });
  });
  afterEach(() => { unmountAll(); vi.useRealTimers(); vi.restoreAllMocks(); });

  it("IT-070 / IT-071 resumes pending uploads when the page opens online and again when the connection returns", async () => {
    await saveAudioDraft({ ...draft({ id: "pending", state: "PENDING_UPLOAD", noteId: undefined }), audio: new Blob(["voice"]) });
    const view = mount(createElement(Harness, { online: false }));
    await settle(6);
    expect(api.resume).not.toHaveBeenCalled();
    expect(text(view.container)).toBe("pending:-");
    view.rerender(createElement(Harness, { online: true }));
    await waitFor(() => api.resume.mock.calls.length === 1, "resume after reconnection");
    expect(api.resume.mock.calls[0][0].map((item: AudioDraft) => item.id)).toEqual(["pending"]);
  });

  it("IT-098 shows the server's processing state and polls only while a note is still being processed", async () => {
    await saveAudioDraft({ ...draft(), audio: new Blob(["voice"]) });
    await updateAudioDraft(draft());
    api.graphql.mockResolvedValue(serverNote("PROCESSING"));
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const view = mount(createElement(Harness, { online: true }));
    await waitFor(() => text(view.container) === "note-1:PROCESSING", "processing status");
    const before = api.graphql.mock.calls.length;
    api.graphql.mockResolvedValue(serverNote("COMPLETED", "DELETED", "DELETED"));
    await act(async () => { vi.advanceTimersByTime(5_000); });
    await waitFor(() => text(view.container) === "note-1:COMPLETED", "completed status");
    const after = api.graphql.mock.calls.length;
    expect(after).toBeGreaterThan(before);
    await act(async () => { vi.advanceTimersByTime(30_000); });
    await settle(4);
    expect(api.graphql.mock.calls.length).toBe(after);
  });

  it("continues polling after transcription until the temporary audio deletion is confirmed", async () => {
    await saveAudioDraft({ ...draft(), audio: new Blob(["voice"]) });
    await updateAudioDraft(draft());
    api.graphql.mockResolvedValue(serverNote("COMPLETED", "SCHEDULED", "AVAILABLE"));
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const view = mount(createElement(Harness, { online: true }));
    await waitFor(() => text(view.container) === "note-1:COMPLETED", "completed transcription with scheduled audio deletion");
    const before = api.graphql.mock.calls.length;
    api.graphql.mockResolvedValue(serverNote("COMPLETED", "DELETED", "DELETED"));
    await act(async () => { vi.advanceTimersByTime(5_000); });
    await waitFor(() => api.graphql.mock.calls.length > before, "poll for audio deletion");
    const after = api.graphql.mock.calls.length;
    await act(async () => { vi.advanceTimersByTime(30_000); });
    await settle(4);
    expect(api.graphql.mock.calls.length).toBe(after);
  });

  it("keeps the last confirmed status while offline and does not ask the server", async () => {
    await saveAudioDraft({ ...draft(), audio: new Blob(["voice"]) });
    await updateAudioDraft(draft());
    api.graphql.mockResolvedValue(serverNote("QUEUED"));
    const view = mount(createElement(Harness, { online: true }));
    await waitFor(() => text(view.container) === "note-1:QUEUED", "queued status");
    const calls = api.graphql.mock.calls.length;
    view.rerender(createElement(Harness, { online: false }));
    await act(async () => { await latest.reload(); });
    expect(api.graphql.mock.calls.length).toBe(calls);
    expect(text(view.container)).toBe("note-1:QUEUED");
  });

  it("IT-078 converges when another tab changes or removes a note", async () => {
    await saveAudioDraft({ ...draft({ id: "other", state: "PENDING_UPLOAD", noteId: undefined }), audio: new Blob(["voice"]) });
    const view = mount(createElement(Harness, { online: false }));
    await waitFor(() => text(view.container) === "other:-", "initial list");
    await deleteAudioDraft("other", "responsibility-1");
    await waitFor(() => text(view.container) === "", "list after another tab deleted the note");
  });

  it("does nothing, and reads nothing from the server, while voice is not enabled", async () => {
    await saveAudioDraft({ ...draft(), audio: new Blob(["voice"]) });
    await updateAudioDraft(draft());
    const view = mount(createElement(Harness, { online: true, enabled: false }));
    await settle(6);
    expect(api.graphql).not.toHaveBeenCalled();
    expect(api.resume).not.toHaveBeenCalled();
    expect(text(view.container)).toBe("");
  });
});
