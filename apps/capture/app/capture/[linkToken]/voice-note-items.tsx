"use client";

import React, { useId, useRef, useState } from "react";
import { Button, Field, Status, Textarea } from "@inspection/design-system";
import { confirmNoteText, discardNote, retryNote, reviseNoteText, setNoteHighlight, validateNoteText } from "@/pwa/audio-note-actions";
import { describeAnchor, describeNote, maximumNoteTextLength, presentVoiceError, waitsForPhoto, type NoteItem } from "@/pwa/audio-notes-state";
import { uploadAudioDraft } from "@/pwa/audio-uploads";

type Props = {
  items: NoteItem[];
  online: boolean;
  reload: () => Promise<void>;
  /** Accessible name of the list. */
  label: string;
  /** Visible title; omitted when the surrounding section already names the list. */
  title?: string;
  emptyText?: string;
  /** Names a note inside its group; defaults to the anchor description. */
  titleFor?: (item: NoteItem, index: number) => string;
};

const discardableStatuses = new Set(["UPLOADING", "QUEUED", "RETRY_WAIT", "FAILED", "COMPLETED"]);
const closedStatuses = new Set(["CANCELED", "EXPIRED"]);
const anchorNotes: Record<string, string> = {
  PENDING: "A posição no vídeo será conferida quando o clipe for recebido.",
  INVALID: "O trecho informado não coincide com o vídeo; esta anotação não será usada em sugestões."
};

/** Lists voice notes with honest per-note status and the review actions the open capture allows. */
export function VoiceNoteItems({ items, online, reload, label, title, emptyText, titleFor }: Props) {
  const headingId = useId();
  const container = useRef<HTMLElement>(null);
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState<string>();

  /** Runs one action for one note at a time, then reloads so the list shows the confirmed state. */
  const run = async (id: string, action: () => Promise<void>, success = "", options: { refocus?: boolean } = {}) => {
    if (busy) return;
    setBusy(id); setNotice("");
    try { await action(); setNotice(success); }
    catch (error) { setNotice(presentVoiceError(error)); }
    finally {
      await reload().catch(() => undefined);
      setBusy(undefined);
      if (options.refocus) requestAnimationFrame(() => { const row = container.current?.querySelector<HTMLElement>(`[data-note-id="${id}"]`); (row ?? container.current)?.focus(); });
    }
  };

  return <section ref={container} tabIndex={-1} className="capture-voice-notes__list" aria-labelledby={title ? headingId : undefined} aria-label={title ? undefined : label}>
    {title && <h4 id={headingId}>{title}</h4>}
    <p role="status" aria-live="polite" className="capture-voice-notes__notice">{notice}</p>
    {items.length === 0 ? (emptyText ? <p className="capture-voice-notes__empty">{emptyText}</p> : null) : <ul aria-label={label}>
      {items.map((item, index) => <VoiceNoteRow key={item.draft.id} item={item} index={index} online={online} busy={busy === item.draft.id} locked={busy !== undefined} title={titleFor ? titleFor(item, index) : describeAnchor(item.draft)} run={run} />)}
    </ul>}
  </section>;
}

type RowProps = {
  item: NoteItem; index: number; online: boolean; busy: boolean; locked: boolean; title: string;
  run: (id: string, action: () => Promise<void>, success?: string, options?: { refocus?: boolean }) => Promise<void>;
};

function VoiceNoteRow({ item, online, busy, locked, title, run }: RowProps) {
  const { draft, note } = item;
  const [edit, setEdit] = useState<string>();
  const [declaration, setDeclaration] = useState("");
  const [confirmingDiscard, setConfirmingDiscard] = useState(false);
  const status = describeNote(item, online);
  const revisions = note?.revisions ?? [];
  const current = revisions.find((revision) => revision.id === note?.currentRevisionId) ?? revisions.at(-1);
  const original = revisions[0];
  const closed = note ? closedStatuses.has(note.status) : false;
  const hasText = Boolean(note && note.status === "COMPLETED" && !note.noSpeech && current);
  const draftText = edit ?? current?.text ?? "";
  const textProblem = hasText && edit !== undefined ? validateNoteText(draftText) : undefined;
  const declarationProblem = declaration ? validateNoteText(declaration) : undefined;
  const canDiscardServer = Boolean(note && discardableStatuses.has(note.status));
  const localOnly = draft.state !== "RECEIVED";
  const waiting = waitsForPhoto(draft);

  return <li className="capture-voice-note" data-note-id={draft.id} data-note-state={note?.status ?? draft.state} tabIndex={-1}>
    <div className="capture-voice-note__summary">
      <strong>{title}</strong>
      {note?.highlighted && !closed && <Status tone="info">Destacada</Status>}
      <span className={`capture-voice-note__status capture-voice-note__status--${status.tone}`}>{status.label}</span>
    </div>
    {note && anchorNotes[note.anchorStatus] && <p className="capture-voice-note__anchor">{anchorNotes[note.anchorStatus]}</p>}

    {localOnly && <div className="capture-voice-note__actions">
      {draft.state !== "UNSENDABLE" && !waiting && <Button variant="secondary" disabled={!online || locked} isPending={busy} pendingLabel="Enviando…" onClick={() => void run(draft.id, async () => { await uploadAudioDraft(draft); }, "Anotação recebida e aguardando processamento.")}>Tentar enviar agora</Button>}
      <Button variant="text" disabled={locked} onClick={() => void run(draft.id, () => discardNote(item), "Anotação descartada deste dispositivo.", { refocus: true })}>Descartar anotação</Button>
    </div>}

    {note && !localOnly && <details className="capture-voice-note__review">
      <summary>Revisar anotação</summary>
      {hasText && current ? <>
        <p><strong>Texto original</strong>: {original?.text ?? current.text}</p>
        <p><strong>Texto atual</strong>: {current.text}</p>
      </> : note.status === "FAILED" ? <p role="status">A transcrição não foi concluída. Tente novamente ou escreva a descrição abaixo.</p>
        : note.status === "COMPLETED" && note.noSpeech ? <p role="status">Nenhuma fala foi identificada nesta anotação.</p>
        : closed ? <p role="status">Esta anotação não será usada.</p>
        : <p role="status">A transcrição ainda está em processamento. O áudio original não é exibido como texto concluído.</p>}
      <p>{note.audioAvailability === "AVAILABLE" ? "Áudio temporário ainda disponível para processamento." : "Áudio original indisponível; o texto pode ser revisado."}</p>

      {hasText && !closed && <>
        <Field label="Corrigir texto" error={textProblem} hint={`${draftText.length.toLocaleString("pt-BR")} de ${maximumNoteTextLength.toLocaleString("pt-BR")} caracteres`}>
          <Textarea value={draftText} onChange={(event) => setEdit(event.target.value)} />
        </Field>
        <div className="capture-voice-note__actions">
          <Button disabled={locked || edit === undefined || edit.trim() === current?.text.trim() || Boolean(textProblem)} onClick={() => void run(draft.id, async () => { await reviseNoteText(item, draftText, "HUMAN_EDIT"); setEdit(undefined); }, "Correção salva sem solicitar outra transcrição.")}>Salvar correção</Button>
          {!note.textConfirmed && <Button variant="secondary" disabled={locked} onClick={() => void run(draft.id, () => confirmNoteText(item), "Texto confirmado.")}>Confirmar texto</Button>}
        </div>
      </>}

      {note.status === "FAILED" && !closed && <div className="capture-voice-note__fallback">
        {note.audioAvailability === "AVAILABLE" && <Button variant="secondary" disabled={locked || !online} onClick={() => void run(draft.id, () => retryNote(item), "Nova tentativa de transcrição solicitada.")}>Tentar transcrição novamente</Button>}
        <Field label="Escrever a descrição" error={declarationProblem} hint="Seu texto será identificado como declaração sua, não como transcrição automática.">
          <Textarea value={declaration} onChange={(event) => setDeclaration(event.target.value)} />
        </Field>
        <Button disabled={locked || !declaration.trim() || Boolean(declarationProblem)} onClick={() => void run(draft.id, async () => { await reviseNoteText(item, declaration, "PARTICIPANT_DECLARATION"); setDeclaration(""); }, "Descrição salva como declaração sua.")}>Salvar minha descrição</Button>
      </div>}

      {!closed && <div className="capture-voice-note__actions">
        <Button variant="secondary" aria-pressed={note.highlighted} disabled={locked} onClick={() => void run(draft.id, () => setNoteHighlight(item, !note.highlighted), note.highlighted ? "Destaque removido." : "Anotação destacada para revisão.")}>Destacar anotação</Button>
        {canDiscardServer && !confirmingDiscard && <Button variant="text" disabled={locked} onClick={() => setConfirmingDiscard(true)}>Descartar anotação</Button>}
      </div>}
      {confirmingDiscard && <div className="capture-voice-note__confirm" role="group" aria-label="Confirmar descarte da anotação">
        <p>Descartar esta anotação? Ela deixará de ser usada em sugestões.</p>
        <Button variant="danger" disabled={locked} onClick={() => void run(draft.id, async () => { await discardNote(item); setConfirmingDiscard(false); }, "Anotação descartada e excluída de novas sugestões.", { refocus: true })}>Sim, descartar</Button>
        <Button variant="secondary" onClick={() => setConfirmingDiscard(false)}>Manter anotação</Button>
      </div>}
    </details>}
  </li>;
}
