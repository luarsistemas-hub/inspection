"use client";

import { useEffect, useMemo, useState } from "react";
import { Button } from "@inspection/design-system";
import { graphql } from "@/graphql/client";
import * as G from "@/graphql/generated";
import { AudioNoteReview, approximateInterval } from "./baselines/audio-note-review";
import { groupAudioNotes } from "./voice-notes-model";

/** Lists a paged inspection transcript grouped by room and immutable media target. */
export function VoiceNotesPanel({ inspectionId }: { inspectionId: string }) {
  const [notes, setNotes] = useState<G.DashboardAudioNotesQuery["audioNotes"]["nodes"]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [hasMore, setHasMore] = useState(false);
  const [selectedId, setSelectedId] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [requestKey, setRequestKey] = useState(0);
  const load = async (after: string | null, append: boolean) => {
    setLoading(true); setError("");
    try {
      const result = await graphql<G.DashboardAudioNotesQuery, G.DashboardAudioNotesQueryVariables>(G.DashboardAudioNotesDocument, { inspectionId, environmentId: "", first: 25, after });
      const connection = result?.audioNotes;
      if (!connection || !Array.isArray(connection.nodes) || !connection.pageInfo) throw new Error("Resposta de anotações incompleta");
      setNotes((current) => append ? [...current, ...connection.nodes] : connection.nodes);
      setCursor(connection.pageInfo.endCursor ?? null);
      setHasMore(connection.pageInfo.hasNextPage);
      setSelectedId((current) => current ?? connection.nodes[0]?.id);
    } catch { setError("Não foi possível carregar as anotações de voz."); }
    finally { setLoading(false); }
  };
  useEffect(() => { void load(null, false); }, [inspectionId, requestKey]);
  const rooms = useMemo(() => groupAudioNotes(notes), [notes]);
  const selected = notes.find((note) => note.id === selectedId);
  const notePositions = new Map(notes.map((note, index) => [note.id, index + 1]));

  if (!loading && notes.length === 0 && !error) return null;
  return <section className="inspection-voice-notes" aria-labelledby="inspection-voice-notes-title">
    <header><div><h3 id="inspection-voice-notes-title">Anotações de voz</h3><p>Consulte cada nota no cômodo e na mídia em que foi registrada.</p></div><Button variant="secondary" disabled={loading} onClick={() => setRequestKey((value) => value + 1)}>Atualizar</Button></header>
    {error && <p role="alert">{error}</p>}
    {loading && notes.length === 0 && <p role="status">Carregando anotações de voz…</p>}
    {!loading && notes.length === 0 && <p>Nenhuma anotação de voz nesta vistoria.</p>}
    {notes.length > 0 && <div className="inspection-voice-notes-layout">
      <nav aria-label="Navegação das anotações de voz">
        {rooms.map((room) => <section key={room.key}><h4>{room.name}</h4>{room.clips.map((clip) => <section key={`${room.key}:${clip.key}`}>
          <h5>{clip.label}</h5><ol>{clip.notes.map((note) => <li key={note.id}><button type="button" aria-label={`Nota ${notePositions.get(note.id)}, ${note.anchor.kind.toUpperCase() === "VIDEO_INTERVAL" ? approximateInterval(note.startMs, note.endMs) : "foto associada"}, registrada em ${new Date(note.createdAt).toLocaleString("pt-BR")}`} aria-current={selectedId === note.id ? "true" : undefined} onClick={() => setSelectedId(note.id)}>{note.anchor.kind.toUpperCase() === "VIDEO_INTERVAL" ? approximateInterval(note.startMs, note.endMs) : "Abrir nota"}</button></li>)}</ol>
        </section>)}</section>)}
      </nav>
      <div>{selected ? <AudioNoteReview key={`${selected.id}:${requestKey}`} noteId={selected.id} environmentName={selected.environmentName ?? undefined} /> : <p role="status">Selecione uma anotação.</p>}</div>
    </div>}
    {hasMore && <Button variant="secondary" disabled={loading} onClick={() => void load(cursor, true)}>{loading ? "Carregando…" : "Carregar mais anotações"}</Button>}
  </section>;
}
