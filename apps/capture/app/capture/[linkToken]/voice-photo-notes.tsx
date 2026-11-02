"use client";

import React, { useEffect, useId, useMemo, useRef, useState } from "react";
import type { AudioNotesState } from "@/pwa/use-audio-notes";
import type { AudioDraft } from "@/pwa/audio-drafts";
import type { NoteItem } from "@/pwa/audio-notes-state";
import { VoiceNoteControl } from "./voice-note-control";
import { VoiceNoteItems } from "./voice-note-items";
import { VoiceOptIn } from "./voice-opt-in";

/** One saved photo of the open room; `mediaId` appears only after the photo itself reached the server. */
export type VoicePhoto = { draftId: string; mediaId?: string; blob: Blob; capturedAt: string };
export type VoicePolicySummary = { inspectionNoteLimit?: number | null; inspectionAudioLimitMs?: number | null };

type Props = {
  responsibilityId: string;
  requirementKey: string;
  environmentLabel: string;
  disclosureVersion: string;
  consentVersion: string;
  consented: boolean;
  online: boolean;
  photos: VoicePhoto[];
  voice: AudioNotesState;
  policy?: VoicePolicySummary;
  onEnableVoice: () => Promise<void>;
};

const belongsTo = (draft: AudioDraft, photo: VoicePhoto) => draft.target.kind === "PHOTO" && (draft.target.photoDraftId === photo.draftId || Boolean(photo.mediaId && draft.target.mediaId === photo.mediaId));

/** Voice notes for the photos of one room: choose the photo, record, and see each note under its own photo. */
export function PhotoVoiceNotes(props: Props) {
  const { photos, voice, requirementKey, consented } = props;
  const legendId = useId();
  const [selectedId, setSelectedId] = useState<string>();
  const [recording, setRecording] = useState(false);
  const [notice, setNotice] = useState("");
  const [urls, setUrls] = useState<Record<string, string>>({});
  const ordered = useMemo(() => [...photos].sort((left, right) => Date.parse(left.capturedAt) - Date.parse(right.capturedAt)), [photos]);
  const knownPhotos = useRef(0);

  useEffect(() => {
    const next: Record<string, string> = {};
    for (const photo of ordered) next[photo.draftId] = URL.createObjectURL(photo.blob);
    setUrls(next);
    return () => { Object.values(next).forEach((url) => URL.revokeObjectURL(url)); };
  }, [ordered]);
  useEffect(() => {
    if (recording) return;
    if (ordered.length > knownPhotos.current) setSelectedId(ordered.at(-1)?.draftId);
    else if (selectedId && !ordered.some((photo) => photo.draftId === selectedId)) setSelectedId(ordered.at(-1)?.draftId);
    knownPhotos.current = ordered.length;
  }, [ordered, recording, selectedId]);

  const roomItems = useMemo(() => voice.items.filter((item) => item.draft.requirementKey === requirementKey && item.draft.target.kind === "PHOTO"), [voice.items, requirementKey]);
  const selected = ordered.find((photo) => photo.draftId === selectedId) ?? ordered.at(-1);
  const orphans = roomItems.filter((item) => !ordered.some((photo) => belongsTo(item.draft, photo)));
  const limitReached = voice.items.some((item) => item.draft.state === "UNSENDABLE" && item.draft.failureCode === "LIMIT_REACHED");
  const limit = props.policy;
  const limitText = limit?.inspectionNoteLimit && limit.inspectionAudioLimitMs ? `Limite desta vistoria: até ${limit.inspectionNoteLimit} anotações e ${Math.floor(limit.inspectionAudioLimitMs / 60_000)} min de áudio. Cada anotação tem no máximo 60 segundos.` : "Cada anotação tem no máximo 60 segundos.";

  if (!consented) return <VoiceOptIn subject="foto" onEnable={async () => { await props.onEnableVoice(); setNotice("Anotações de voz ativadas. O microfone só será solicitado quando você iniciar uma anotação."); }} />;

  const notesFor = (photo: VoicePhoto): NoteItem[] => roomItems.filter((item) => belongsTo(item.draft, photo));
  const photoName = (index: number) => `Foto ${index + 1}`;

  return <section className="capture-voice-notes capture-voice-photo-notes" aria-label={`Anotações de voz · ${props.environmentLabel}`}>
    <h3>Anotações de voz · {props.environmentLabel}</h3>
    {notice && <p role="status" className="capture-voice-notes__notice">{notice}</p>}
    <p className="capture-voice-notes__hint">{limitText}</p>
    {ordered.length > 0 && <fieldset className="capture-voice-photos" disabled={recording}>
      <legend id={legendId}>Foto da anotação</legend>
      <div className="capture-voice-photos__grid" role="radiogroup" aria-labelledby={legendId}>
        {ordered.map((photo, index) => <label key={photo.draftId} className={`capture-voice-photo${photo.draftId === selected?.draftId ? " capture-voice-photo--selected" : ""}`}>
          <input type="radio" name={`voice-photo-${requirementKey}`} checked={photo.draftId === selected?.draftId} onChange={() => setSelectedId(photo.draftId)} />
          {urls[photo.draftId] && <img src={urls[photo.draftId]} alt={`${photoName(index)} de ${ordered.length} · ${props.environmentLabel}`} />}
          <span>{photoName(index)} · {notesFor(photo).length === 1 ? "1 anotação" : `${notesFor(photo).length} anotações`}</span>
        </label>)}
      </div>
      {recording && <p className="capture-voice-notes__hint">A foto fica fixa até você terminar a anotação.</p>}
    </fieldset>}
    <VoiceNoteControl responsibilityId={props.responsibilityId} requirementKey={requirementKey} environmentLabel={props.environmentLabel} disclosureVersion={props.disclosureVersion} consentVersion={props.consentVersion}
      enabled={consented} online={props.online} onRecordingChange={setRecording} blockedReason={limitReached ? "O limite de anotações de voz foi atingido. Fotos e vídeos continuam disponíveis." : undefined}
      target={selected ? { kind: "PHOTO", photoDraftId: selected.draftId, ...(selected.mediaId ? { mediaId: selected.mediaId } : {}) } : undefined} />
    {ordered.map((photo, index) => {
      const items = notesFor(photo);
      if (items.length === 0 && photo.draftId !== selected?.draftId) return null;
      return <VoiceNoteItems key={photo.draftId} items={items} online={props.online} reload={voice.reload} label={`Anotações de ${photoName(index)}`} title={`Anotações da ${photoName(index).toLowerCase()}`} emptyText="Nenhuma anotação nesta foto." titleFor={(_, position) => `Anotação ${position + 1}`} />;
    })}
    {orphans.length > 0 && <VoiceNoteItems items={orphans} online={props.online} reload={voice.reload} label="Anotações de fotos que saíram da vistoria" title="Fotos substituídas ou removidas" titleFor={(_, position) => `Anotação ${position + 1}`} />}
  </section>;
}
