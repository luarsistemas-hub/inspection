import type * as G from "@/graphql/generated";

export type ListedAudioNote = G.DashboardAudioNotesQuery["audioNotes"]["nodes"][number];

/** Keeps note polling active only while a note or inventory regeneration can still progress. */
export function shouldPollInspectionNotes(notes: Array<Pick<ListedAudioNote, "status">>, regenerationRunning: boolean) {
  return regenerationRunning || notes.some((note) => !["COMPLETED", "FAILED", "DISCARDED"].includes(note.status.toUpperCase()));
}

/** Groups notes by room and immutable capture target, preserving each clip timeline. */
export function groupAudioNotes(notes: ListedAudioNote[]) {
  const rooms = new Map<string, { name: string; clips: Map<string, { key: string; label: string; notes: ListedAudioNote[] }> }>();
  for (const note of notes) {
    const roomKey = note.environmentId;
    const room = rooms.get(roomKey) ?? { name: note.environmentName?.trim() || "Cômodo não identificado", clips: new Map() };
    const video = note.anchor.kind.toUpperCase() === "VIDEO_INTERVAL";
    const targetKey = video ? `video:${note.videoAttemptId ?? note.sourceMediaId ?? note.id}` : `photo:${note.photoMediaId ?? note.id}`;
    const clip = room.clips.get(targetKey) ?? { key: targetKey, label: video ? "Clipe de vídeo" : "Foto", notes: [] };
    clip.notes.push(note);
    room.clips.set(targetKey, clip);
    rooms.set(roomKey, room);
  }
  return [...rooms.entries()].map(([key, room]) => ({ key, ...room, clips: [...room.clips.values()].map((clip) => ({ ...clip, notes: clip.notes.slice().sort((a, b) => (a.startMs ?? Number.MAX_SAFE_INTEGER) - (b.startMs ?? Number.MAX_SAFE_INTEGER)) })) }));
}

/** Returns a human-readable state for retained video frames. */
export function frameStatusLabel(status: string) {
  return status.toUpperCase() === "AVAILABLE" ? "Disponível" : "Expirado";
}

/** Adds per-room voice totals without deriving them from item counts. */
export function sumVoiceSummary(environments: Array<{ voiceSummary: { pendingVoiceItems: number; conflictItems: number; staleSuggestions: number; notesWithoutItem: number } }>) {
  return environments.reduce((total, environment) => ({
    pendingVoiceItems: total.pendingVoiceItems + environment.voiceSummary.pendingVoiceItems,
    conflictItems: total.conflictItems + environment.voiceSummary.conflictItems,
    staleSuggestions: total.staleSuggestions + environment.voiceSummary.staleSuggestions,
    notesWithoutItem: total.notesWithoutItem + environment.voiceSummary.notesWithoutItem,
  }), { pendingVoiceItems: 0, conflictItems: 0, staleSuggestions: 0, notesWithoutItem: 0 });
}
