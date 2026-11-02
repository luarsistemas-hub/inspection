import { describe, expect, it } from "vitest";
import { frameStatusLabel, groupAudioNotes, shouldPollInspectionNotes, sumVoiceSummary, type ListedAudioNote } from "@/features/dashboard/voice-notes-model";

const note = (id: string, videoAttemptId: string, startMs: number) => ({
  id, inspectionId: "inspection-a", environmentId: "room-a", environmentName: "Sala", responsibilityId: "responsibility-a", requirementKey: "sala",
  targetKind: "VIDEO_INTERVAL", photoMediaId: null, videoAttemptId, sourceMediaId: null, startMs, endMs: startMs + 1000, durationMs: 1000,
  status: "COMPLETED", anchorStatus: "VALID", anchor: { kind: "VIDEO_INTERVAL", status: "VALID", mediaId: null, mediaStatus: "READY", approximate: true, previewUrl: null, playbackUrl: null, frames: [], framesTruncated: false, visualCoverage: "NONE" },
  highlighted: false, highlightSource: null, version: 1, currentRevisionId: null, textConfirmed: false, noSpeech: false, audioAvailability: "UNAVAILABLE", audioDeletionStatus: "DELETED", createdAt: "2026-10-31T12:00:00Z", updatedAt: "2026-10-31T12:00:00Z", revisions: [],
} as ListedAudioNote);

describe("Dashboard voice presentation models", () => {
  it("IT-152 groups by room and immutable clip, then orders notes by interval", () => {
    const grouped = groupAudioNotes([note("late", "clip-a", 9000), note("other-clip", "clip-b", 1000), note("early", "clip-a", 1000)]);
    expect(grouped).toHaveLength(1);
    expect(grouped[0].clips).toHaveLength(2);
    expect(grouped[0].clips[0].notes.map((item) => item.id)).toEqual(["early", "late"]);
    expect(grouped[0].clips[1].notes.map((item) => item.id)).toEqual(["other-clip"]);
  });

  it("IT-141 labels visual context availability independently", () => {
    expect(frameStatusLabel("AVAILABLE")).toBe("Disponível");
    expect(frameStatusLabel("EXPIRED")).toBe("Expirado");
  });

  it("IT-136/170/178 sums API voice counters per room without inferring item decisions", () => {
    expect(sumVoiceSummary([
      { voiceSummary: { pendingVoiceItems: 2, conflictItems: 1, staleSuggestions: 3, notesWithoutItem: 4 } },
      { voiceSummary: { pendingVoiceItems: 1, conflictItems: 0, staleSuggestions: 2, notesWithoutItem: 1 } },
    ])).toEqual({ pendingVoiceItems: 3, conflictItems: 1, staleSuggestions: 5, notesWithoutItem: 5 });
  });

  it("polls only while a note can progress or regeneration is running", () => {
    expect(shouldPollInspectionNotes([], false)).toBe(false);
    expect(shouldPollInspectionNotes([note("pending", "clip-a", 0)], false)).toBe(false);
    for (const status of ["UPLOADED", "TRANSCRIBING", "REGENERATION_RUNNING"] as const) {
      expect(shouldPollInspectionNotes([{ status }], false)).toBe(true);
    }
    for (const status of ["COMPLETED", "FAILED", "DISCARDED"] as const) {
      expect(shouldPollInspectionNotes([{ status }], false)).toBe(false);
    }
    expect(shouldPollInspectionNotes([], true)).toBe(true);
  });
});
