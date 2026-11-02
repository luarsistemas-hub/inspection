"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { graphql } from "@/graphql/client";
import { CaptureAudioNoteDocument } from "@/graphql/generated";
import { audioDraftsChangedEvent, listAudioDrafts } from "./audio-drafts";
import { resumeAudioDrafts } from "./audio-uploads";
import type { NoteItem, ServerNote } from "./audio-notes-state";

const unsettledStatuses = new Set(["UPLOADING", "QUEUED", "PROCESSING", "RETRY_WAIT"]);
const pendingAudioDeletionStatuses = new Set(["PENDING", "SCHEDULED", "PROCESSING", "FAILED"]);
const pollIntervalMs = 5_000;

export type AudioNotesState = { items: NoteItem[]; loaded: boolean; reload: () => Promise<void> };

/**
 * Keeps local notes and the server's view of them in sync for one responsibility:
 * resumes pending uploads when the page opens or the connection returns, and polls only while a note is still processing.
 */
export function useAudioNotes(input: { responsibilityId: string; online: boolean; enabled: boolean }): AudioNotesState {
  const { responsibilityId, online, enabled } = input;
  const [items, setItems] = useState<NoteItem[]>([]);
  const [loaded, setLoaded] = useState(false);
  const known = useRef<Record<string, ServerNote>>({});
  const onlineRef = useRef(online);
  onlineRef.current = online;
  const generation = useRef(0);

  const reload = useCallback(async () => {
    if (!enabled || !responsibilityId) return;
    const current = ++generation.current;
    const drafts = await listAudioDrafts(responsibilityId).catch(() => []);
    if (onlineRef.current) {
      await Promise.all(drafts.filter((draft) => draft.noteId && draft.state === "RECEIVED").map(async (draft) => {
        try {
          const note = (await graphql(CaptureAudioNoteDocument, { id: draft.noteId! })).audioNote;
          if (note) known.current[draft.id] = note; else delete known.current[draft.id];
        } catch { /* keep the last confirmed state while the server is unreachable */ }
      }));
    }
    if (current !== generation.current) return;
    const ids = new Set(drafts.map((draft) => draft.id));
    for (const id of Object.keys(known.current)) if (!ids.has(id)) delete known.current[id];
    setItems(drafts.map((draft) => ({ draft, note: known.current[draft.id] })));
    setLoaded(true);
  }, [enabled, responsibilityId]);

  useEffect(() => { void reload(); }, [reload]);
  useEffect(() => {
    if (!enabled) return;
    const onChanged = () => { void reload(); };
    window.addEventListener(audioDraftsChangedEvent, onChanged);
    return () => window.removeEventListener(audioDraftsChangedEvent, onChanged);
  }, [enabled, reload]);
  useEffect(() => {
    if (!enabled || !online || !responsibilityId) return;
    let canceled = false;
    void listAudioDrafts(responsibilityId).then((drafts) => resumeAudioDrafts(drafts)).catch(() => undefined).finally(() => { if (!canceled) void reload(); });
    return () => { canceled = true; };
  }, [enabled, online, responsibilityId, reload]);

  const unsettled = online && items.some((item) => item.draft.state === "RECEIVED" && item.note !== undefined && (
    unsettledStatuses.has(item.note.status) || pendingAudioDeletionStatuses.has(item.note.audioDeletionStatus)
  ));
  useEffect(() => {
    if (!enabled || !unsettled) return;
    const interval = setInterval(() => { void reload(); }, pollIntervalMs);
    return () => clearInterval(interval);
  }, [enabled, unsettled, reload]);

  return { items, loaded, reload };
}
