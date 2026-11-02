import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { graphql } from "@/graphql/client";
import { AudioNoteReview, anchorMediaLabel, approximateInterval, authorLabel, canRetryTranscription, isTranscriptWithinLimit } from "@/features/dashboard/baselines/audio-note-review";

vi.mock("@/graphql/client", async (importOriginal) => ({ ...await importOriginal<typeof import("@/graphql/client")>(), graphql: vi.fn() }));

const note = {
  id: "note-1", inspectionId: "inspection-1", environmentId: "room-1", environmentName: "Cozinha", responsibilityId: "responsibility-1", requirementKey: "room-video", targetKind: "VIDEO_INTERVAL", photoMediaId: null, videoAttemptId: "attempt-v1", sourceMediaId: "media-v1", startMs: 12500, endMs: 18700, durationMs: 6200, status: "COMPLETED", anchorStatus: "VALID", anchor: { kind: "VIDEO_INTERVAL", status: "VALID", mediaId: "media-v1", mediaStatus: "UNAVAILABLE", approximate: true, previewUrl: null, playbackUrl: null, frames: [], framesTruncated: false, visualCoverage: "NONE" }, highlighted: false, highlightSource: null, version: 2, currentRevisionId: "revision-2", textConfirmed: false, noSpeech: false, audioAvailability: "UNAVAILABLE", audioDeletionStatus: "DELETED", createdAt: "2026-10-31T12:00:00Z", updatedAt: "2026-10-31T12:01:00Z", revisions: [
    { id: "revision-1", revisionNumber: 1, text: "seis pratos", kind: "MODEL", authorId: null, authorName: null, callId: "call-1", createdAt: "2026-10-31T12:00:00Z" },
    { id: "revision-2", revisionNumber: 2, text: "cinco pratos", kind: "HUMAN_EDIT", authorId: "reviewer-1", authorName: "Revisora QA", callId: null, createdAt: "2026-10-31T12:01:00Z" },
  ],
};

async function renderNote(anchorStatus = "VALID", overrides: Partial<typeof note> = {}) {
  vi.mocked(graphql).mockResolvedValueOnce({ audioNote: { ...note, anchorStatus, ...overrides } } as never);
  const host = document.createElement("div");
  const root = createRoot(host);
  await act(async () => {
    root.render(createElement(AudioNoteReview, { noteId: note.id, revisionId: "revision-1", attributeName: "quantidade" }));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  return { host, root };
}

describe("Dashboard audio note review", () => {
  beforeEach(() => { Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); vi.mocked(graphql).mockReset(); });

  it("UT-023 labels the cited voice value and approximate clip interval", async () => {
    expect(approximateInterval(12500, 18700)).toBe("Intervalo aproximado 00:12–00:18");
    const { host, root } = await renderNote();
    expect(host.textContent).toContain("Informado por voz");
    expect(host.textContent).toContain("seis pratos");
    expect(host.textContent).toContain("Intervalo aproximado 00:12–00:18");
    expect(host.textContent).toContain("Transcrição automática");
    expect(host.textContent).toContain("Revisora QA");
    expect(host.textContent).toContain("A confirmação abaixo se aplica somente ao texto");
    await act(async () => root.unmount());
  });

  it("UT-024 keeps transcript history when the source is unavailable and renders no signed URL", async () => {
    const { host, root } = await renderNote("SOURCE_UNAVAILABLE");
    expect(host.textContent).toContain("Vídeo original indisponível");
    expect(host.textContent).toContain("seis pratos");
    expect(host.innerHTML).not.toContain("https://");
    expect(host.innerHTML).not.toContain("?X-Amz-");
    await act(async () => root.unmount());
  });

  it("IT-126/131 presents authors, PT-BR anchor states and the full edit limit", () => {
    expect(authorLabel(null, "MODEL")).toBe("Transcrição automática");
    expect(authorLabel(null, "HUMAN_EDIT")).toBe("Autor não identificado");
    expect(anchorMediaLabel("PREPARING")).toBe("Mídia em preparação");
    expect(anchorMediaLabel("PURGED")).toBe("Vídeo original indisponível");
    expect(anchorMediaLabel("REPLACED")).toContain("mídia original");
    expect(isTranscriptWithinLimit("x".repeat(10000))).toBe(true);
    expect(isTranscriptWithinLimit("x".repeat(10001))).toBe(false);
    expect(approximateInterval(20000, 62000)).toBe("Intervalo aproximado 00:20–01:02");
  });

  it("offers another transcription only when failed audio remains available and unconfirmed", () => {
    expect(canRetryTranscription("FAILED", "AVAILABLE", false)).toBe(true);
    expect(canRetryTranscription("COMPLETED", "AVAILABLE", false)).toBe(false);
    expect(canRetryTranscription("FAILED", "UNAVAILABLE", false)).toBe(false);
    expect(canRetryTranscription("FAILED", "AVAILABLE", true)).toBe(false);
  });

  it("IT-130 shows an empty speech result without inventing transcript content", async () => {
    const { host, root } = await renderNote("VALID", { noSpeech: true });
    expect(host.textContent).toContain("Nenhuma fala identificada");
    expect(host.textContent).not.toContain("seis pratos");
    expect(host.textContent).not.toContain("cinco pratos");
    await act(async () => root.unmount());
  });
});
