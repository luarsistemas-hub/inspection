"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@inspection/design-system";
import { prepareVideoPlaybackSession } from "@/auth/video-playback-session";
import { graphql } from "@/graphql/client";
import * as G from "@/graphql/generated";
import { EvidenceImage } from "../report-visual";
import { newClientMutationId, throwUserErrors } from "./review-model";

type AudioNote = NonNullable<G.DashboardAudioNoteQuery["audioNote"]>;
const textLimit = 10000;

/** Formats a clip interval without implying word-level timing. */
export function approximateInterval(startMs: number | null, endMs: number | null) {
  if (startMs == null || endMs == null || startMs < 0 || endMs <= startMs) return "Intervalo de vídeo inválido ou pendente de validação.";
  return `Intervalo aproximado ${formatTime(startMs)}–${formatTime(endMs)}`;
}

function formatTime(value: number) {
  const seconds = Math.floor(value / 1000);
  return `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
}

/** Provides a stable PT-BR label for an anchor media state. */
export function anchorMediaLabel(status: string) {
  return ({ PENDING: "Mídia em preparação", PREPARING: "Mídia em preparação", READY: "Mídia disponível", UNAVAILABLE: "Vídeo original indisponível", PURGED: "Vídeo original indisponível", REPLACED: "Mídia substituída; esta nota continua ligada à mídia original" } as Record<string, string>)[status.toUpperCase()] ?? "Estado da mídia indisponível";
}

export function authorLabel(authorName: string | null | undefined, kind: string) {
  if (kind.toUpperCase() === "MODEL" || kind.toUpperCase() === "TRANSCRIPT") return "Transcrição automática";
  return authorName?.trim() || "Autor não identificado";
}

/** Checks the editable transcript limit without changing the user's text. */
export function isTranscriptWithinLimit(value: string) { return value.length <= textLimit; }

function revisionDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Data indisponível" : date.toLocaleString("pt-BR");
}

/** Shows transcript history, correction, confirmation and the exact media anchor. */
export function AudioNoteReview({ noteId, revisionId, attributeName, environmentName: environmentNameProp }: { noteId: string; revisionId?: string; attributeName?: string; environmentName?: string }) {
  const [note, setNote] = useState<AudioNote>();
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [text, setText] = useState("");
  const [retryText, setRetryText] = useState<string>();
  const [historyRevisionId, setHistoryRevisionId] = useState(revisionId ?? "");
  const [confirmHighlightRemoval, setConfirmHighlightRemoval] = useState(false);
  const [mediaError, setMediaError] = useState(false);
  const [playbackError, setPlaybackError] = useState(false);
  const mutationId = useRef("");
  const noteRef = useRef<AudioNote | undefined>(undefined);
  noteRef.current = note;
  useEffect(() => { setMediaError(false); setPlaybackError(false); setRetryText(undefined); mutationId.current = ""; }, [noteId]);

  const reload = async (preserveText = false) => {
    setLoading(true);
    try {
      const result = await graphql<G.DashboardAudioNoteQuery, G.DashboardAudioNoteQueryVariables>(G.DashboardAudioNoteDocument, { id: noteId });
      if (!result.audioNote) { setNote(undefined); setError("A anotação não está disponível para esta revisão."); return; }
      setNote(result.audioNote);
      const current = result.audioNote.revisions.find((revision) => revision.id === result.audioNote?.currentRevisionId) ?? result.audioNote.revisions.at(-1);
      if (!preserveText) setText(current?.text ?? "");
      setHistoryRevisionId((value) => value || revisionId || result.audioNote?.currentRevisionId || "");
      setError("");
    } catch {
      setError("Não foi possível carregar a anotação. Tente novamente.");
    } finally { setLoading(false); }
  };
  useEffect(() => { void reload(); }, [noteId, revisionId]);
  useEffect(() => {
    const timer = window.setInterval(() => {
      const mediaStatus = noteRef.current?.anchor.mediaStatus.toUpperCase();
      if (mediaStatus === "PENDING" || mediaStatus === "PREPARING") void reload(true);
    }, 2000);
    return () => window.clearInterval(timer);
  }, [noteId, revisionId]);

  const citedRevision = useMemo(() => note?.revisions.find((revision) => revision.id === revisionId), [note, revisionId]);
  const selectedRevision = useMemo(() => note?.revisions.find((revision) => revision.id === historyRevisionId), [note, historyRevisionId]);
  const currentRevision = note?.revisions.find((revision) => revision.id === note.currentRevisionId) ?? note?.revisions.at(-1);
  const run = async (action: () => Promise<void>, preserveDraft = false) => {
    if (!note || busy) return;
    setBusy(true); setError("");
    try { await action(); setRetryText(undefined); mutationId.current = ""; await reload(); }
    catch (failure) {
      const conflict = typeof failure === "object" && failure !== null && "code" in failure && (failure as { code?: string }).code === "CONFLICT";
      if (conflict) {
        await reload();
        setError("A anotação mudou desde a última leitura. A versão mais recente está exibida; revise-a antes de salvar novamente.");
      } else {
        setRetryText(preserveDraft ? text : undefined);
        await reload(true);
        setError("Não foi possível salvar a anotação. O último texto salvo foi mantido; tente novamente.");
      }
    } finally { setBusy(false); }
  };

  if (loading && !note) return <p role="status">Carregando anotação de voz…</p>;
  if (!note) return <p role="alert">{error || "A anotação não está disponível para esta revisão."}</p>;
  const video = note.anchor.kind.toUpperCase() === "VIDEO_INTERVAL";
  const anchor = note.anchor;
  const mediaStatus = anchor.mediaStatus.toUpperCase();
  const noSpeech = note.noSpeech || note.status.toUpperCase() === "NO_SPEECH";
  const original = note.revisions.find((revision) => ["MODEL", "TRANSCRIPT", "DECLARATION"].includes(revision.kind.toUpperCase()));
  const startMs = note.startMs;
  const endMs = note.endMs;
  const seekToInterval = (element: HTMLVideoElement) => {
    if (startMs != null && startMs >= 0 && Number.isFinite(element.duration) && startMs / 1000 < element.duration) element.currentTime = startMs / 1000;
  };

  return <section className="audio-note-review" aria-label={attributeName ? `Revisão da anotação de voz para ${attributeName}` : "Revisão da anotação de voz"}>
    {attributeName && <h4>Anotação de voz · {attributeName}</h4>}
    <p><strong>Informado por voz</strong> · A confirmação abaixo se aplica somente ao texto; não decide nenhum item do inventário.</p>
    {(environmentNameProp || note.environmentName) && <p>Cômodo: {environmentNameProp || note.environmentName}</p>}
    {note.textConfirmed && <p className="audio-note-confirmed" role="status">Texto confirmado · somente leitura</p>}
    {noSpeech ? <p role="status">Nenhuma fala identificada</p> : <>
      <div className="audio-note-text-columns">
        <section aria-label="Texto original"><h5>Texto original</h5>{original ? <><blockquote>{original.text}</blockquote><p>{authorLabel(original.authorName, original.kind)} · {revisionDate(original.createdAt)}</p></> : <p>Texto original indisponível.</p>}</section>
        <section aria-label="Revisão atual"><h5>Revisão atual</h5>{currentRevision ? <><blockquote>{currentRevision.text}</blockquote><p>{authorLabel(currentRevision.authorName, currentRevision.kind)} · {revisionDate(currentRevision.createdAt)}</p></> : <p>Ainda não há transcrição disponível.</p>}</section>
      </div>
      {citedRevision && <p className="audio-note-citation">Texto citado pelo inventário · revisão {citedRevision.revisionNumber}: {citedRevision.text}</p>}
      {note.revisions.length > 0 && <details>
        <summary>Histórico de texto ({note.revisions.length} versões)</summary>
        <ol>{note.revisions.map((revision) => <li key={revision.id}>
          <button type="button" onClick={() => setHistoryRevisionId(revision.id)}>Revisão {revision.revisionNumber}</button>
          <span> · {authorLabel(revision.authorName, revision.kind)} · {revisionDate(revision.createdAt)}</span>
          {selectedRevision?.id === revision.id && <blockquote>{revision.text}</blockquote>}
        </li>)}</ol>
      </details>}
      {!note.textConfirmed && note.revisions.length > 0 && <>
        <label>Corrigir texto da anotação<textarea value={text} onChange={(event) => setText(event.target.value)} aria-describedby="audio-note-character-count" /></label>
        <p id="audio-note-character-count" role={text.length > textLimit ? "alert" : undefined}>{text.length} / {textLimit} caracteres{ text.length > textLimit ? " · O limite foi excedido; reduza o texto para salvar." : ""}</p>
        {retryText && <p role="status">Há uma tentativa de correção não confirmada. O texto salvo continua visível.</p>}
      </>}
    </>}

    <section className="audio-note-anchor" aria-label="Âncora da anotação">
      <h5>{video ? "Vídeo associado" : "Foto associada"}</h5>
      {mediaStatus === "REPLACED" && <p role="status">{anchorMediaLabel(mediaStatus)}</p>}
      {(mediaStatus === "UNAVAILABLE" || mediaStatus === "PURGED") && <p role="status">Vídeo original indisponível</p>}
      {(mediaStatus === "PENDING" || mediaStatus === "PREPARING") && <p role="status">Mídia em preparação <Button variant="secondary" onClick={() => void reload()}>Atualizar</Button></p>}
      {video && anchor.status.toUpperCase() === "INVALID" && <p role="status">Associação inválida: fora da linha do tempo do vídeo; não entra no catálogo</p>}
      {anchor.status.toUpperCase() === "NO_ACCESS" || (!anchor.previewUrl && !anchor.playbackUrl && !anchor.frames.some((frame) => frame.url) && mediaStatus === "READY") ? <p role="status">Sem acesso à mídia</p> : null}
      {!video && anchor.previewUrl && !mediaError && <figure><img src={anchor.previewUrl} alt={`Foto do cômodo ${note.environmentName ?? "da vistoria"}`} onError={() => setMediaError(true)} /><figcaption>{note.environmentName ?? "Cômodo da vistoria"}</figcaption></figure>}
      {!video && mediaError && <p role="status">A prévia da foto não está disponível.</p>}
      {video && startMs != null && endMs != null && <p>{approximateInterval(startMs, endMs)}</p>}
      {video && anchor.playbackUrl && ["READY", "REPLACED"].includes(mediaStatus) && <VoiceVideo url={anchor.playbackUrl} startMs={startMs} onError={() => setPlaybackError(true)} />}
      {video && playbackError && <p role="status">Não foi possível reproduzir o vídeo. O texto e o intervalo continuam disponíveis.</p>}
      {anchor.visualCoverage.toUpperCase() === "LIMITED" && <p role="status">Sem quadro próximo ao intervalo</p>}
      {anchor.frames.length > 0 && <section aria-label="Contexto visual"><h6>Contexto visual</h6><p>Os quadros são referências visuais e não comprovam o conteúdo da fala.</p><div className="audio-note-frames">{anchor.frames.map((frame) => <figure key={frame.id}>
        {frame.url && frame.status.toUpperCase() === "AVAILABLE" ? <EvidenceImage url={frame.url} alt={`Contexto visual em ${formatTime(frame.timestampMs)}`} /> : <span role="status">Quadro indisponível</span>}
        <figcaption>{formatTime(frame.timestampMs)} · {frame.status.toUpperCase() === "AVAILABLE" ? "Disponível" : "Expirado"}</figcaption>
      </figure>)}</div></section>}
      {anchor.framesTruncated && <p role="status">Mostrando alguns quadros; outros não foram revisados</p>}
    </section>

    {note.highlighted && <p className="audio-note-highlight" role="status">Destaque do participante</p>}
    {error && <p role="alert">{error}</p>}
    <div className="audio-note-actions">
      {!noSpeech && !note.textConfirmed && note.revisions.length > 0 && <Button variant="secondary" disabled={busy || !text.trim() || text.length > textLimit || text.trim() === currentRevision?.text} onClick={() => {
        if (!mutationId.current) mutationId.current = newClientMutationId();
        void run(async () => {
          const result = await graphql<G.ReviseAudioNoteMutation, G.ReviseAudioNoteMutationVariables>(G.ReviseAudioNoteDocument, { input: { noteId: note.id, text: text.trim(), revisionKind: "HUMAN_EDIT", expectedVersion: note.version, clientMutationId: mutationId.current } });
          throwUserErrors(result.reviseAudioNote.userErrors);
        }, true);
      }}>Salvar correção</Button>}
      {retryText && !note.textConfirmed && <Button variant="secondary" disabled={busy || retryText.length > textLimit || !retryText.trim()} onClick={() => {
        if (!mutationId.current) mutationId.current = newClientMutationId();
        void run(async () => {
          const result = await graphql<G.ReviseAudioNoteMutation, G.ReviseAudioNoteMutationVariables>(G.ReviseAudioNoteDocument, { input: { noteId: note.id, text: retryText.trim(), revisionKind: "HUMAN_EDIT", expectedVersion: note.version, clientMutationId: mutationId.current } });
          throwUserErrors(result.reviseAudioNote.userErrors);
        }, true);
      }}>Tentar salvar correção novamente</Button>}
      {!noSpeech && !note.textConfirmed && note.status.toUpperCase() === "COMPLETED" && note.revisions.length > 0 && <Button variant="secondary" disabled={busy || !currentRevision || text.trim() !== currentRevision.text} onClick={() => void run(async () => {
        const result = await graphql<G.ConfirmAudioNoteTextMutation, G.ConfirmAudioNoteTextMutationVariables>(G.ConfirmAudioNoteTextDocument, { input: { noteId: note.id, expectedVersion: note.version, clientMutationId: newClientMutationId() } });
        throwUserErrors(result.confirmAudioNoteText.userErrors);
      })}>Confirmar texto</Button>}
      {note.highlighted && <Button variant="secondary" onClick={() => setConfirmHighlightRemoval(true)}>Remover destaque</Button>}
    </div>
    {confirmHighlightRemoval && <div role="alertdialog" aria-labelledby="remove-highlight-title" aria-describedby="remove-highlight-description">
      <h5 id="remove-highlight-title">Remover destaque da anotação?</h5><p id="remove-highlight-description">Esta ação remove apenas o destaque e não altera nenhum item do inventário.</p>
      <Button variant="secondary" disabled={busy} onClick={() => setConfirmHighlightRemoval(false)}>Manter destaque</Button>
      <Button variant="secondary" disabled={busy} onClick={() => void run(async () => {
        const result = await graphql<G.SetAudioNoteHighlightMutation, G.SetAudioNoteHighlightMutationVariables>(G.SetAudioNoteHighlightDocument, { input: { noteId: note.id, highlighted: false, expectedVersion: note.version, clientMutationId: newClientMutationId() } });
        throwUserErrors(result.setAudioNoteHighlight.userErrors);
        setConfirmHighlightRemoval(false);
      })}>Remover destaque</Button>
    </div>}
  </section>;
}

function VoiceVideo({ url, startMs, onError }: { url: string; startMs: number | null; onError: () => void }) {
  const [ready, setReady] = useState(false);
  const playbackProxyUrl = `/api/media/audio-note-video?${new URLSearchParams({ path: url })}`;
  useEffect(() => { let active = true; void prepareVideoPlaybackSession().then(() => { if (active) setReady(true); }).catch(onError); return () => { active = false; }; }, [url]);
  if (!ready) return <p role="status">Preparando reprodução…</p>;
  return <video src={playbackProxyUrl} controls playsInline preload="metadata" aria-label="Reprodução do vídeo da anotação" onLoadedMetadata={(event) => { if (startMs != null && startMs >= 0) { const video = event.currentTarget; if (Number.isFinite(video.duration) && startMs / 1000 < video.duration) video.currentTime = startMs / 1000; } }} onError={onError} />;
}
