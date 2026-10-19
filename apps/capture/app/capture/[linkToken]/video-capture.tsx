"use client";

import React, { type ChangeEvent, useEffect, useRef, useState } from "react";
import { Alert, Button, Card, ProductIdentity, Status, ThemeSelector } from "@inspection/design-system";
import type { ExternalCaptureBootstrapQuery } from "@/graphql/generated";
import { captureOperations, graphql } from "@/graphql/client";
import { appendVideoChunk, discardVideoDraft, finalizeVideoDraft, listVideoDrafts, loadVideoDraft, saveVideoDraft, type VideoDraft, videoChunks } from "@/pwa/video-drafts";
import { startVideoRecorder, supportedVideoMime, type VideoRecorderSession } from "@/pwa/video-recorder";
import { refreshVideoReceipt, uploadVideoDraft } from "@/pwa/video-uploads";
import { RegisterServiceWorker } from "@/components/register-service-worker";

type External = ExternalCaptureBootstrapQuery["externalCapture"];
type Props = {
  responsibilityId: string;
  kind: string;
  recaptureRequestId?: string;
  recaptureDeadlineAt?: string;
  policy: External["policy"];
  requirements: External["requirements"];
  referenceItems: External["referenceItems"];
  comparisonMode: string;
  online: boolean;
  onSessionFailure: (error: unknown) => boolean;
};

function hashDraft(attemptId: string, chunkCount: number): Promise<string> {
  return new Promise((resolve, reject) => {
    const worker = new Worker(new URL("../../../src/pwa/video-hash.worker.ts", import.meta.url));
    worker.onmessage = ({ data }: MessageEvent<{ sha256?: string; error?: string }>) => {
      worker.terminate();
      if (data.sha256) resolve(data.sha256); else reject(new Error(data.error ?? "Falha ao verificar vídeo."));
    };
    worker.onerror = () => { worker.terminate(); reject(new Error("Falha ao verificar vídeo.")); };
    worker.postMessage({ attemptId, chunkCount });
  });
}

function clock(seconds: number): string {
  const whole = Math.floor(seconds);
  return `${Math.floor(whole / 60).toString().padStart(2, "0")}:${(whole % 60).toString().padStart(2, "0")}`;
}

const message = (error: unknown): string => {
  if (error instanceof DOMException && error.name === "NotAllowedError") return "Câmera bloqueada. Libere o acesso nas configurações do navegador e tente novamente.";
  if (error instanceof DOMException && error.name === "NotReadableError") return "A câmera está ocupada ou indisponível. Feche outros aplicativos e tente novamente.";
  if (error instanceof DOMException && error.name === "QuotaExceededError") return "O dispositivo ficou sem espaço. O vídeo não foi salvo; libere espaço e grave novamente.";
  return error instanceof Error ? error.message : "Não foi possível concluir a captura. Tente novamente.";
};

const blockedVideoStatuses = new Set(["SCREENED", "SENSITIVE_CONTENT", "REJECTED", "CHECKSUM_MISMATCH", "SOURCE_INVALID", "DURATION_EXCEEDED", "RESOURCE_LIMIT", "FRAME_INTEGRITY_FAILED"]);
const preparationLabel = (status?: string) => ({ SCREENED: "aguardando revisão de privacidade", SENSITIVE_CONTENT: "aguardando revisão de privacidade", READY: "pronto", QUEUED: "em preparação", PROCESSING: "em preparação", FAILED: "preparação interrompida", REJECTED: "gravação não aceita", CHECKSUM_MISMATCH: "gravação não aceita", SOURCE_INVALID: "gravação não aceita", DURATION_EXCEEDED: "gravação não aceita", RESOURCE_LIMIT: "gravação não aceita", FRAME_INTEGRITY_FAILED: "gravação não aceita" } as Record<string, string>)[status ?? ""] ?? "recebido";

export function MutedVideoPreview({ src, onCanPlay, onLoadedMetadata, onError }: {
  src: string;
  onCanPlay: React.ReactEventHandler<HTMLVideoElement>;
  onLoadedMetadata: React.ReactEventHandler<HTMLVideoElement>;
  onError: React.ReactEventHandler<HTMLVideoElement>;
}) {
  return <video src={src} controls muted playsInline onCanPlay={onCanPlay} onLoadedMetadata={onLoadedMetadata} onError={onError} style={{ width: "100%", maxHeight: 420 }} />;
}

export function VideoCapture({ responsibilityId, kind, recaptureRequestId, recaptureDeadlineAt, policy, requirements, referenceItems, comparisonMode, online, onSessionFailure }: Props) {
  const [drafts, setDrafts] = useState<VideoDraft[]>([]);
  const [current, setCurrent] = useState<VideoDraft>();
  const [phase, setPhase] = useState<"idle" | "recording" | "preview" | "uploading" | "received" | "submitted">("idle");
  const [elapsed, setElapsed] = useState(0);
  const [stoppedAtLimit, setStoppedAtLimit] = useState(false);
  const [previewURL, setPreviewURL] = useState<string>();
  const [playable, setPlayable] = useState(false);
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [completedParts, setCompletedParts] = useState(0);
  const [totalParts, setTotalParts] = useState(0);
  const session = useRef<VideoRecorderSession | undefined>(undefined);
  const admitted = useRef<VideoDraft | undefined>(undefined);
  const actionPending = useRef(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const allowFile = policy.allowGallery === true && requirements.find((item) => item.key === (kind === "RECAPTURE" ? "video:supplement" : "video:main"))?.captureSourcePolicy !== "CAMERA_ONLY";
  const supportedMime = supportedVideoMime();
  const policyLimit = typeof policy.maxDurationSeconds === "number" && policy.maxDurationSeconds > 0 ? policy.maxDurationSeconds : 900;
  const scope = comparisonMode === "FIXED_ORIGIN" && referenceItems.length > 0 ? "comparison" : "general";

  useEffect(() => { void listVideoDrafts(responsibilityId).then(setDrafts).catch(() => setNotice("Não foi possível ler gravações locais neste dispositivo.")); }, [responsibilityId]);
  useEffect(() => () => { if (previewURL) URL.revokeObjectURL(previewURL); }, [previewURL]);
  useEffect(() => {
    if (!current?.mediaId || (phase !== "received" && phase !== "submitted")) return;
    const refresh = () => { void refreshVideoReceipt(current).then((updated) => { setCurrent(updated); setDrafts((items) => items.map((item) => item.id === updated.id ? updated : item)); }).catch((error) => { if (!onSessionFailure(error)) setNotice("O status de preparação está temporariamente indisponível."); }); };
    const interval = setInterval(refresh, 10_000);
    return () => clearInterval(interval);
  }, [current?.id, current?.mediaId, phase, onSessionFailure]);

  const updateDraft = (draft: VideoDraft) => { setCurrent(draft); setDrafts((items) => [...items.filter((item) => item.id !== draft.id), draft]); };
  const showPreview = async (draft: VideoDraft) => {
    const blobs = await videoChunks(draft.id, draft.chunkCount);
    const url = URL.createObjectURL(new Blob(blobs, { type: draft.contentType }));
    setPreviewURL(url); setPlayable(false); updateDraft(draft); setPhase("preview");
  };
  const finish = async () => {
    if (!session.current) return;
    setBusy(true);
    try {
      const saved = await session.current.stop();
      session.current = undefined;
      await showPreview(saved);
      setNotice("Gravação finalizada e salva neste dispositivo. Revise a prévia antes de enviar.");
    } catch (error) { session.current = undefined; setPhase("idle"); setNotice(`${message(error)} A gravação incompleta pode não ser recuperável.`); }
    finally { setBusy(false); }
  };
  const admit = async (source: "CAMERA" | "FILE"): Promise<VideoDraft> => {
    const replacesMediaId = current?.mediaId && blockedVideoStatuses.has(current.preparationStatus ?? "") && !needsSensitiveReview(current.preparationStatus) ? current.mediaId : undefined;
    const reusable = admitted.current;
    if (!replacesMediaId && reusable && reusable.source === source && Date.parse(reusable.expiresAt) > Date.now()) {
      const bootstrap = (await graphql(captureOperations.bootstrap)).externalCapture;
      if (bootstrap.responsibilityId !== responsibilityId || bootstrap.status !== "OPEN") throw new Error("Este convite não permite mais gravações. Solicite uma nova autorização.");
      const persisted = await loadVideoDraft(reusable.id);
      if (persisted && persisted.state !== "INCOMPLETE" && persisted.state !== "UNSAVED") throw new Error("Revise ou conclua o vídeo salvo antes de iniciar outra gravação.");
      if (persisted) {
        await discardVideoDraft(persisted);
        setDrafts((items) => items.filter((item) => item.id !== persisted.id));
      }
      return { ...reusable, state: "INCOMPLETE", chunkCount: 0, sizeBytes: 0, sha256: undefined, parts: [], mediaId: undefined, uploadId: undefined };
    }
    const result = await graphql(captureOperations.beginVideo, { input: { acquisitionSource: source, replacesMediaId: replacesMediaId ?? null, clientMutationId: crypto.randomUUID() } });
    throwOnUserErrors(result.beginVideoCapture);
    const attempt = result.beginVideoCapture.attempt;
    if (!attempt) throw new Error("O servidor não autorizou uma tentativa de vídeo.");
    const draft: VideoDraft = { id: attempt.id, responsibilityId, kind, source, contentType: "", maxDurationSeconds: attempt.maxDurationSeconds, maxBytes: attempt.maxBytes, expiresAt: attempt.expiresAt, state: "INCOMPLETE", chunkCount: 0, sizeBytes: 0, replacesMediaId, parts: [] };
    admitted.current = draft;
    return draft;
  };
  const start = async () => {
    if (busy || actionPending.current || session.current) return;
    actionPending.current = true; setBusy(true); setNotice(""); setStoppedAtLimit(false);
    try {
      const draft = await admit("CAMERA");
      const started = await startVideoRecorder(draft, hashDraft, (update) => {
        setElapsed(update.elapsedSeconds);
        if (update.error) { setNotice(update.error); void finish(); }
        if (update.limitReached) { setStoppedAtLimit(true); setNotice("O limite desta gravação foi atingido. Finalize e revise o vídeo."); void finish(); }
      });
      session.current = started; updateDraft(draft); setElapsed(0); setPhase("recording");
    } catch (error) { if (!onSessionFailure(error)) setNotice(message(error)); }
    finally { setBusy(false); actionPending.current = false; }
  };
  const importFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]; event.target.value = "";
    if (!file || !allowFile || actionPending.current) return;
    actionPending.current = true; setBusy(true);
    try {
      if (!/^video\/(mp4|quicktime|webm)$/.test(file.type)) throw new Error("Use um vídeo MP4, MOV ou WebM compatível.");
      const draft = await admit("FILE");
      if (file.size < 1 || file.size > draft.maxBytes) throw new Error("O vídeo excede o limite de 1 GiB ou está vazio.");
      let next: VideoDraft = { ...draft, contentType: file.type };
      await saveVideoDraft(next);
      for (let offset = 0, sequence = 0; offset < file.size; offset += 1024 * 1024, sequence += 1) {
        const chunk = file.slice(offset, offset + 1024 * 1024);
        await appendVideoChunk(draft.id, sequence, chunk);
        next = { ...next, chunkCount: sequence + 1, sizeBytes: next.sizeBytes + chunk.size };
        await saveVideoDraft(next);
      }
      await showPreview(await finalizeVideoDraft(next, hashDraft));
      setNotice("Arquivo salvo neste dispositivo. Revise a prévia; áudio incidental não será analisado.");
    } catch (error) { if (!onSessionFailure(error)) setNotice(message(error)); }
    finally { actionPending.current = false; setBusy(false); }
  };
  const discard = async (candidate = current) => {
    if (!candidate || busy) return;
    setBusy(true);
    try { admitted.current = candidate; await discardVideoDraft(candidate); setDrafts((items) => items.filter((item) => item.id !== candidate.id)); setCurrent(undefined); setPreviewURL(undefined); setStoppedAtLimit(false); setPhase("idle"); setNotice("Vídeo descartado. Você pode gravar novamente dentro da tentativa autorizada."); }
    catch (error) { setNotice(message(error)); } finally { setBusy(false); }
  };
  const resume = async (draft: VideoDraft) => {
    if (actionPending.current || !online) return;
    actionPending.current = true; setBusy(true); setPhase("uploading");
    try {
      const bootstrap = await graphql(captureOperations.bootstrap);
      if (bootstrap.externalCapture.responsibilityId !== responsibilityId || bootstrap.externalCapture.status !== "OPEN") throw new Error("Este convite não permite mais envio. Solicite uma nova autorização.");
      const received = await uploadVideoDraft(draft, (done, total) => { setCompletedParts(done); setTotalParts(total); });
      updateDraft(received); setPhase("received"); setNotice("Vídeo recebido. A preparação das imagens continua no servidor.");
    } catch (error) { if (!onSessionFailure(error)) { setDrafts(await listVideoDrafts(responsibilityId).catch(() => drafts)); setPhase("idle"); setNotice(message(error)); } }
    finally { actionPending.current = false; setBusy(false); }
  };
  const submit = async () => {
    if (actionPending.current || !current?.mediaId || current.state !== "RECEIVED" || blockedVideoStatuses.has(current.preparationStatus ?? "") || !online) return;
    actionPending.current = true; setBusy(true);
    try {
      if (kind === "RECAPTURE") {
        if (!recaptureRequestId) throw new Error("A solicitação de complemento não está disponível.");
        const result = await graphql(captureOperations.submitRecapture, { input: { requestId: recaptureRequestId, confirmIncomplete: false, clientMutationId: `${current.id}:submit` } });
        throwOnUserErrors(result.submitRecapture);
      } else {
        const result = await graphql(captureOperations.submit, { input: { confirmIncomplete: false, clientMutationId: `${current.id}:submit` } });
        throwOnUserErrors(result.submitCapture);
      }
      setPhase("submitted"); setNotice("Envio confirmado.");
    } catch (error) {
      try { const external = (await graphql(captureOperations.bootstrap)).externalCapture; if (external.status === "COMPLETED") { setPhase("submitted"); return; } } catch { /* Keep the pending state when reconciliation is unavailable. */ }
      if (!onSessionFailure(error)) setNotice(message(error));
    } finally { actionPending.current = false; setBusy(false); }
  };
  return <main className="capture-shell"><RegisterServiceWorker /><div className="capture-workspace">
    <header className="capture-intro"><div className="capture-utility-bar"><ProductIdentity product="Capture" /><ThemeSelector /></div><h1>{kind === "ORIGIN" ? "Registrar vídeo de origem" : kind === "RECAPTURE" ? "Registrar complemento em vídeo" : "Registrar vistoria em vídeo"}</h1></header>
    <Card className="capture-card capture-card--wide">
      <h2>Um vídeo contínuo</h2>
      <p>Filme devagar, mantenha a câmera estável, mostre os ambientes com boa luz e aproxime detalhes importantes. O tempo máximo é {clock(current?.maxDurationSeconds ?? drafts[0]?.maxDurationSeconds ?? policyLimit)}. O limite de tempo não comprova cobertura de todo o imóvel.</p>
      {kind === "RECAPTURE" && referenceItems.length > 0 ? <section aria-labelledby="recapture-items-heading"><h3 id="recapture-items-heading">Itens a complementar</h3>{recaptureDeadlineAt && <p>Prazo: {new Intl.DateTimeFormat("pt-BR", { dateStyle: "long", timeStyle: "short" }).format(new Date(recaptureDeadlineAt))}</p>}<p>Grave um único vídeo mostrando os itens solicitados.</p><ul>{referenceItems.map((item) => <li key={`${item.requirementKey}:${item.evidenceId ?? item.mediaId}`}><strong>{item.description || [item.environment, item.component].filter(Boolean).join(" · ")}</strong>{item.view ? <span> · {item.view}</span> : null}{item.reason ? <p>Motivo: {item.reason}</p> : null}{item.imageUrl && item.availability === "AVAILABLE" ? <img src={item.imageUrl} alt={`Imagem de referência: ${item.description}`} loading="lazy" style={{ display: "block", maxWidth: 180 }} /> : <p>Imagem de referência indisponível.</p>}</li>)}</ul></section> : scope === "comparison" ? <section><h3>Referência de origem</h3><p>Use os ambientes e componentes abaixo como orientação. Grave um só vídeo; não é necessário criar um clipe por item.</p><ul>{referenceItems.map((item) => <li key={item.requirementKey}><strong>{item.environment || item.description}</strong>{item.component ? ` · ${item.component}` : ""}{item.ambiguous ? " · identidade incerta" : ""}{item.gap ? " · vista incompleta na origem" : ""}{item.imageUrl && <img src={item.imageUrl} alt={`Referência: ${item.description}`} loading="lazy" style={{ display: "block", maxWidth: 180 }} />}</li>)}</ul></section> : <p>Sem comparação histórica nesta captura. O resultado descreverá apenas o que for visível no vídeo.</p>}
      {!online && <Alert tone="warning">Sem conexão. Uma gravação só fica recuperável após finalização e salvamento neste dispositivo; o envio exige conexão e autorização vigente.</Alert>}
      {!supportedMime && <Alert tone="warning">Este navegador não oferece um formato de gravação compatível. Abra o convite no Safari ou Chrome atualizado do aparelho. A gravação não continua em segundo plano; fechar o navegador antes de finalizar pode perder o vídeo.</Alert>}
      {phase === "recording" && <div role="status"><strong>Gravando {clock(elapsed)} / {clock(current?.maxDurationSeconds ?? 900)}</strong><p>Uma única gravação está em andamento. A tela pode interromper a câmera ao bloquear ou fechar o navegador.</p><Button disabled={busy} onClick={() => void finish()}>Parar e revisar</Button></div>}
      {phase === "idle" && <div className="capture-actions"><Button disabled={busy || !supportedMime || drafts.some((item) => ["SAVED", "CONFIRMED", "RECEIVED"].includes(item.state))} onClick={() => void start()}>Começar gravação</Button>{allowFile && <><Button variant="secondary" disabled={busy} onClick={() => fileInput.current?.click()}>Escolher vídeo permitido</Button><input ref={fileInput} type="file" accept="video/mp4,video/quicktime,video/webm" hidden onChange={(event) => void importFile(event)} /></>}</div>}
      {phase === "preview" && current && <div><h3>Revise a gravação</h3>{stoppedAtLimit && <Alert tone="warning">O limite desta gravação foi atingido. Revise o vídeo antes de confirmar o envio.</Alert>}{previewURL && <MutedVideoPreview src={previewURL} onCanPlay={(event) => { if (event.currentTarget.duration <= current.maxDurationSeconds) setPlayable(true); }} onLoadedMetadata={(event) => { if (event.currentTarget.duration > current.maxDurationSeconds) { setPlayable(false); setNotice("Este vídeo excede o limite da tentativa. Grave novamente."); } }} onError={() => { setPlayable(false); setNotice("Este dispositivo não conseguiu reproduzir o vídeo. Grave novamente em um navegador compatível."); }} />}<p>{clock(elapsed)} de até {clock(current.maxDurationSeconds)}. O vídeo só será enviado após sua confirmação.</p><div className="capture-actions"><Button variant="secondary" disabled={busy} onClick={() => void discard()}>Descartar e gravar novamente</Button><Button disabled={!playable || busy || !online} onClick={() => void resume(current)}>Confirmar e enviar</Button></div></div>}
      {phase === "uploading" && <p role="status">Enviando vídeo: {completedParts} de {totalParts || "…"} partes recebidas. Mantenha esta página aberta enquanto o envio estiver em andamento.</p>}
      {(phase === "received" || phase === "submitted") && <div><Status tone={blockedVideoStatuses.has(current?.preparationStatus ?? "") ? "warning" : "success"}>Vídeo recebido</Status>{needsSensitiveReview(current?.preparationStatus) ? <p>Recebemos seu vídeo, mas a análise está pausada para uma revisão de privacidade. Você será avisado se for preciso gravar novamente.</p> : <p>Estado do vídeo: {preparationLabel(current?.preparationStatus)}.</p>}{blockedVideoStatuses.has(current?.preparationStatus ?? "") && !needsSensitiveReview(current?.preparationStatus) && <Alert tone="warning">A preparação desta gravação foi interrompida. O gestor da vistoria poderá solicitar uma nova gravação.</Alert>}{phase === "received" && !blockedVideoStatuses.has(current?.preparationStatus ?? "") && <Button disabled={busy || !online} onClick={() => void submit()}>Confirmar vistoria</Button>}</div>}
      {drafts.length > 0 && phase !== "submitted" && <section><h3>Gravações neste dispositivo</h3><ul>{drafts.map((draft) => <li key={draft.id}><span>{draft.state === "INCOMPLETE" ? "Gravação incompleta; não há recuperação garantida" : draft.state === "UNSAVED" ? "Salvamento falhou" : draft.state === "RECEIVED" ? `Recebida · ${draft.preparationStatus ?? "preparando"}` : draft.state === "CONFIRMED" ? "Envio pendente" : "Salva localmente; envio pendente"}</span>{draft.state === "SAVED" && <Button variant="secondary" disabled={busy} onClick={() => void showPreview(draft).catch((error) => setNotice(message(error)))}>Revisar</Button>}{(draft.state === "INCOMPLETE" || draft.state === "UNSAVED") && <Button variant="secondary" disabled={busy} onClick={() => void discard(draft)}>Descartar e gravar novamente</Button>}{(draft.state === "CONFIRMED" || draft.state === "RECEIVED") && <Button variant="secondary" disabled={busy || !online} onClick={() => void (draft.state === "RECEIVED" ? refreshVideoReceipt(draft).then((updated) => { updateDraft(updated); setPhase("received"); }).catch((error) => setNotice(message(error))) : resume(draft))}>{draft.state === "RECEIVED" ? "Atualizar status" : "Retomar envio"}</Button>}</li>)}</ul></section>}
      {notice && <Alert tone="warning">{notice}</Alert>}
    </Card><p className="capture-privacy-note">Acesso pessoal vinculado ao convite. Não compartilhe este link.</p>
  </div></main>;
}
