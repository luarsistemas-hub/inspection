"use client";

import React from "react";
import { describeNote, summarizeNotes, type NoteItem } from "@/pwa/audio-notes-state";

type Props = {
  items: NoteItem[];
  online: boolean;
  /** Names the room a note belongs to. */
  labelFor: (requirementKey: string) => string;
};

/** Review-step summary: names every note that is still pending or cannot be sent, without blocking visual submission. */
export function VoiceSubmissionSummary({ items, online, labelFor }: Props) {
  if (items.length === 0) return null;
  const summary = summarizeNotes(items);
  const open = items.filter((item) => item.draft.state !== "RECEIVED");
  return <section className="capture-voice-summary" aria-label="Resumo das anotações de voz">
    <h3>Anotações de voz</h3>
    <p role="status">{summary.total === 1 ? "1 anotação" : `${summary.total} anotações`} · {summary.received} recebida{summary.received === 1 ? "" : "s"} · {summary.pending + summary.failed} pendente{summary.pending + summary.failed === 1 ? "" : "s"} · {summary.unsendable} sem envio possível</p>
    {open.length > 0 && <>
      <ul aria-label="Anotações de voz que ainda não foram recebidas">{open.map((item) => <li key={item.draft.id}><strong>{labelFor(item.draft.requirementKey)}</strong>: {describeNote(item, online).label}</li>)}</ul>
      <p>As fotos e os vídeos não esperam pelas anotações. Anotações que ainda estiverem pendentes ao concluir serão removidas deste dispositivo e não serão enviadas.</p>
    </>}
  </section>;
}
