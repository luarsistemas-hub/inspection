"use client";

import React, { type ChangeEvent, useEffect, useRef, useState } from "react";
import { Alert, Button, Card, ProductIdentity, Status, ThemeSelector } from "@inspection/design-system";
import type { ExternalCaptureBootstrapQuery } from "@/graphql/generated";
import { captureOperations, graphql } from "@/graphql/client";
import { throwOnUserErrors } from "@/pwa/mutation-errors";
import { appendVideoChunk, discardVideoDraft, finalizeVideoDraft, listVideoDrafts, loadVideoDraft, saveVideoDraft, type VideoDraft, videoChunks } from "@/pwa/video-drafts";
import { startVideoRecorder, supportedVideoMime, type VideoRecorderSession } from "@/pwa/video-recorder";
import { attachPreview, canOpenCamera, openCameraCancellable, playPreview, releaseCamera } from "@/pwa/camera-session";
import { acquireScreenWakeLock } from "@/pwa/wake-lock";
import { captureErrorMessage as message, visibleDrafts } from "@/pwa/video-capture-state";
import { refreshVideoReceipt, uploadVideoDraft } from "@/pwa/video-uploads";
import { RegisterServiceWorker } from "@/components/register-service-worker";
import { presentReplacementNotice } from "./presentation";

type Phase = "idle" | "opening" | "ready" | "starting" | "recording" | "finalizing" | "preview" | "uploading" | "received" | "submitted";
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

const blockedVideoStatuses = new Set(["SCREENED", "SENSITIVE_CONTENT", "REJECTED", "CHECKSUM_MISMATCH", "SOURCE_INVALID", "DURATION_EXCEEDED", "RESOURCE_LIMIT", "FRAME_INTEGRITY_FAILED"]);
const needsSensitiveReview = (status?: string) => status === "SCREENED" || status === "SENSITIVE_CONTENT";
const preparationLabel = (status?: string) => ({ SCREENED: "aguardando revisão de privacidade", SENSITIVE_CONTENT: "aguardando revisão de privacidade", READY: "pronto", QUEUED: "em preparação", PROCESSING: "em preparação", PENDING_VALIDATION: "em preparação", FAILED: "preparação interrompida", REJECTED: "gravação não aceita", CHECKSUM_MISMATCH: "gravação não aceita", SOURCE_INVALID: "gravação não aceita", DURATION_EXCEEDED: "gravação não aceita", RESOURCE_LIMIT: "gravação não aceita", FRAME_INTEGRITY_FAILED: "gravação não aceita" } as Record<string, string>)[status ?? ""] ?? "recebido";

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
  const [phase, setPhaseState] = useState<Phase>("idle");
  const phaseRef = useRef<Phase>("idle");
  const setPhase = (next: Phase) => { phaseRef.current = next; setPhaseState(next); };
  const [elapsed, setElapsed] = useState(0);
  const [stoppedAtLimit, setStoppedAtLimit] = useState(false);
  const [previewURL, setPreviewURL] = useState<string>();
  const [playable, setPlayable] = useState(false);
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [completedParts, setCompletedParts] = useState(0);
  const [totalParts, setTotalParts] = useState(0);
  const [cameraStream, setCameraStream] = useState<MediaStream>();
  const [previewReady, setPreviewReady] = useState(false);
  const [previewBlocked, setPreviewBlocked] = useState(false);
  const videoRef = useRef<HTMLVideoElement>(null);
  const cameraStreamRef = useRef<MediaStream | undefined>(undefined);
  const wakeLockRelease = useRef<(() => Promise<void>) | undefined>(undefined);
  const limitNotified = useRef(false);
  const generation = useRef(0);
  const mounted = useRef(true);
  const session = useRef<VideoRecorderSession | undefined>(undefined);
  const finishPromise = useRef<Promise<void> | undefined>(undefined);
  const admitted = useRef<VideoDraft | undefined>(undefined);
  const actionPending = useRef(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const allowFile = policy.allowGallery === true && requirements.find((item) => item.key === (kind === "RECAPTURE" ? "video:supplement" : "video:main"))?.captureSourcePolicy !== "CAMERA_ONLY";
  const supportedMime = supportedVideoMime();
  const policyLimit = typeof policy.maxDurationSeconds === "number" && policy.maxDurationSeconds > 0 ? policy.maxDurationSeconds : 900;
  const scope = comparisonMode === "FIXED_ORIGIN" && referenceItems.length > 0 ? "comparison" : "general";

  useEffect(() => { void listVideoDrafts(responsibilityId).then(setDrafts).catch(() => setNotice("Não foi possível ler gravações locais neste dispositivo.")); }, [responsibilityId]);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; generation.current += 1; if (session.current) void session.current.stop().catch(() => {}); else releaseCamera(cameraStreamRef.current); void releaseWakeLock(); }; }, []);
  useEffect(() => {
    const video = videoRef.current;
    if (!video || !cameraStream) return;
    setPreviewReady(false); setPreviewBlocked(false);
    void attachPreview(video, cameraStream).then((result) => setPreviewBlocked(result === "blocked"));
    return () => { if (video.srcObject === cameraStream) video.srcObject = null; };
  }, [cameraStream]);
  useEffect(() => {
    if (!cameraStream) return;
    const tracks = cameraStream.getVideoTracks();
    // While recording, the recorder owns the stream and finalizes on its own ended listener.
    const onEnded = () => {
      if (phaseRef.current === "recording" || phaseRef.current === "finalizing") return;
      generation.current += 1; actionPending.current = false; setBusy(false);
      releaseCamera(cameraStreamRef.current); cameraStreamRef.current = undefined; setCameraStream(undefined); setPreviewReady(false);
      setPhase("idle"); setNotice("A câmera ficou indisponível. Tente abrir novamente.");
    };
    tracks.forEach((track) => track.addEventListener("ended", onEnded));
    return () => tracks.forEach((track) => track.removeEventListener("ended", onEnded));
  }, [cameraStream]);
  useEffect(() => {
    const interrupt = (force = false) => {
      if (!force && document.visibilityState !== "hidden") return;
      if (phase === "recording" || phase === "finalizing") { void finish(); return; }
      if (["opening", "ready", "starting"].includes(phase)) {
        generation.current += 1; actionPending.current = false; setBusy(false); setPhase("idle");
        if (phase !== "starting") { releaseCamera(cameraStreamRef.current); cameraStreamRef.current = undefined; setCameraStream(undefined); }
      }
    };
    const onVisibilityChange = () => interrupt();
    const onPageHide = () => interrupt(true);
    const onPageShow = (event: PageTransitionEvent) => { if (event.persisted) void listVideoDrafts(responsibilityId).then(setDrafts); };
    document.addEventListener("visibilitychange", onVisibilityChange); window.addEventListener("pagehide", onPageHide); window.addEventListener("pageshow", onPageShow);
    return () => { document.removeEventListener("visibilitychange", onVisibilityChange); window.removeEventListener("pagehide", onPageHide); window.removeEventListener("pageshow", onPageShow); };
  }, [phase, cameraStream, responsibilityId]);
  useEffect(() => () => { if (previewURL) URL.revokeObjectURL(previewURL); }, [previewURL]);
  useEffect(() => {
    if (!current?.mediaId || (phase !== "received" && phase !== "submitted")) return;
    const refresh = () => { void refreshVideoReceipt(current).then((updated) => { setCurrent(updated); setDrafts((items) => items.map((item) => item.id === updated.id ? updated : item)); }).catch((error) => { if (!onSessionFailure(error)) setNotice("O status de preparação está temporariamente indisponível."); }); };
    const interval = setInterval(refresh, 10_000);
    return () => clearInterval(interval);
  }, [current?.id, current?.mediaId, phase, onSessionFailure]);

  const releaseWakeLock = async () => { const release = wakeLockRelease.current; wakeLockRelease.current = undefined; await release?.(); };
  const updateDraft = (draft: VideoDraft) => { setCurrent(draft); setDrafts((items) => [...items.filter((item) => item.id !== draft.id), draft]); };
  const showPreview = async (draft: VideoDraft) => {
    const blobs = await videoChunks(draft.id, draft.chunkCount);
    const url = URL.createObjectURL(new Blob(blobs, { type: draft.contentType }));
    setPreviewURL(url); setPlayable(false); updateDraft(draft); setPhase("preview");
  };
  const finish = (): Promise<void> => {
    if (finishPromise.current) return finishPromise.current;
    const activeSession = session.current;
    if (!activeSession) return Promise.resolve();
    setPhase("finalizing"); setBusy(true);
    // Register the promise synchronously so reentrant callbacks reuse it.
    let release!: () => void;
    const finishing = new Promise<void>((resolve) => { release = resolve; });
    finishPromise.current = finishing;
    void (async () => {
      try {
        const saved = await activeSession.stop();
        session.current = undefined;
        await releaseWakeLock(); cameraStreamRef.current = undefined;
        if (!mounted.current) return;
        setCameraStream(undefined);
        await showPreview(saved);
        setNotice("Gravação finalizada e salva neste dispositivo. Revise a prévia antes de enviar.");
      } catch (error) {
        session.current = undefined; await releaseWakeLock(); cameraStreamRef.current = undefined;
        if (mounted.current) { setCameraStream(undefined); setPhase("idle"); setNotice(`${message(error)} A gravação incompleta pode não ser recuperável.`); void listVideoDrafts(responsibilityId).then(setDrafts).catch(() => {}); }
      } finally { if (mounted.current) setBusy(false); finishPromise.current = undefined; release(); }
    })();
    return finishing;
  };
  const open = async () => {
    if (busy || actionPending.current || !canOpenCamera()) return;
    actionPending.current = true; setBusy(true); setNotice(""); setPhase("opening");
    const operation = ++generation.current;
    try {
      const stream = await openCameraCancellable(() => operation !== generation.current || !mounted.current);
      if (!stream) return;
      const track = stream.getVideoTracks()[0];
      if (!track || track.readyState !== "live") { releaseCamera(stream); setPhase("idle"); setNotice("A câmera ficou indisponível. Tente abrir novamente."); return; }
      cameraStreamRef.current = stream; setCameraStream(stream); setPhase("ready");
    } catch (error) {
      if (operation === generation.current) {
        setPhase("idle");
        if (onSessionFailure(error)) { releaseCamera(cameraStreamRef.current); cameraStreamRef.current = undefined; setCameraStream(undefined); }
        else setNotice(message(error));
      }
    }
    finally { if (operation === generation.current) { setBusy(false); actionPending.current = false; } }
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
    if (busy || actionPending.current || session.current || !cameraStream || !previewReady) return;
    actionPending.current = true; setBusy(true); setNotice(""); setStoppedAtLimit(false); limitNotified.current = false; setPhase("starting");
    const operation = ++generation.current;
    let recorderStarted = false;
    try {
      const draft = await admit("CAMERA");
      if (operation !== generation.current || !mounted.current) {
        releaseCamera(cameraStream); cameraStreamRef.current = undefined;
        if (mounted.current) setCameraStream(undefined);
        return;
      }
      recorderStarted = true;
      const started = await startVideoRecorder(cameraStream, draft, hashDraft, (update) => {
        if (!mounted.current) return;
        setElapsed(update.elapsedSeconds);
        if (update.error) setNotice(update.error);
        if (update.limitReached && !limitNotified.current) { limitNotified.current = true; setStoppedAtLimit(true); setNotice("O limite desta gravação foi atingido. Finalize e revise o vídeo."); }
        if (update.finalizing || update.error || update.limitReached) void finish();
      }, Date.now, () => operation !== generation.current || !mounted.current);
      if (operation !== generation.current || !mounted.current) {
        void started.stop().catch(() => {}).finally(() => { if (mounted.current) void listVideoDrafts(responsibilityId).then(setDrafts).catch(() => {}); });
        cameraStreamRef.current = undefined;
        if (mounted.current) setCameraStream(undefined);
        return;
      }
      session.current = started; updateDraft(draft); setElapsed(0); setPhase("recording");
      void acquireScreenWakeLock().then((release) => {
        if (!mounted.current || session.current !== started || !started.active()) { void release(); return; }
        wakeLockRelease.current = release;
      });
    } catch (error) {
      if (operation !== generation.current || !mounted.current) {
        releaseCamera(cameraStream); cameraStreamRef.current = undefined;
        if (mounted.current) { setCameraStream(undefined); setPhase("idle"); }
        return;
      }
      if (recorderStarted) { releaseCamera(cameraStream); cameraStreamRef.current = undefined; setCameraStream(undefined); setPhase("idle"); }
      else setPhase("ready");
      if (onSessionFailure(error)) { releaseCamera(cameraStream); cameraStreamRef.current = undefined; setCameraStream(undefined); setPhase("idle"); }
      else setNotice(message(error));
    }
    finally { if (mounted.current) setBusy(false); actionPending.current = false; }
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
      updateDraft(received); setPhase("received"); setNotice("Vídeo recebido. Aguarde a confirmação do status.");
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
  const shownDrafts = visibleDrafts(drafts, phase);
  const cameraMode = ["opening", "ready", "starting", "recording", "finalizing"].includes(phase);
  const liveMessage = phase === "opening" ? "Abrindo câmera." : phase === "ready" ? "Câmera pronta. Ainda não está gravando." : phase === "starting" ? "Preparando gravação." : phase === "recording" ? "Gravando." : phase === "finalizing" ? "Finalizando vídeo…" : phase === "preview" ? "Vídeo salvo neste dispositivo. Revise antes de enviar." : phase === "uploading" ? "Enviando vídeo." : phase === "received" ? "Vídeo recebido." : phase === "submitted" ? "Envio confirmado." : "";
  const references = kind === "RECAPTURE" && referenceItems.length > 0 ? <section aria-labelledby="recapture-items-heading"><h3 id="recapture-items-heading">Itens a complementar</h3>{recaptureDeadlineAt && <p>Prazo: {new Intl.DateTimeFormat("pt-BR", { dateStyle: "long", timeStyle: "short" }).format(new Date(recaptureDeadlineAt))}</p>}<p>Grave um único vídeo mostrando os itens solicitados.</p><ul>{referenceItems.map((item) => <li key={`${item.requirementKey}:${item.evidenceId ?? item.mediaId}`}><strong>{item.description || [item.environment, item.component].filter(Boolean).join(" · ")}</strong>{item.view ? <span> · {item.view}</span> : null}{item.reason ? <p>Motivo: {item.reason}</p> : null}{item.imageUrl && item.availability === "AVAILABLE" ? <img src={item.imageUrl} alt={`Imagem de referência: ${item.description}`} loading="lazy" style={{ display: "block", maxWidth: 180 }} /> : <p>Imagem de referência indisponível.</p>}</li>)}</ul></section> : scope === "comparison" ? <section><h3>Referência de origem</h3><p>Use os ambientes e componentes abaixo como orientação. Grave um só vídeo; não é necessário criar um clipe por item.</p><ul>{referenceItems.map((item) => <li key={item.requirementKey}><strong>{item.environment || item.description}</strong>{item.component ? ` · ${item.component}` : ""}{item.ambiguous ? " · identidade incerta" : ""}{item.gap ? " · vista incompleta na origem" : ""}{item.imageUrl && <img src={item.imageUrl} alt={`Referência: ${item.description}`} loading="lazy" style={{ display: "block", maxWidth: 180 }} />}</li>)}</ul></section> : <p>Sem comparação histórica nesta captura. O resultado descreverá apenas o que for visível no vídeo.</p>;
  const readyToRecord = Boolean(videoRef.current && videoRef.current.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA && videoRef.current.videoWidth > 0 && videoRef.current.videoHeight > 0 && cameraStream?.getVideoTracks().some((track) => track.readyState === "live"));
  return <main className={`capture-shell ${cameraMode ? "capture-camera-shell" : ""}`}><RegisterServiceWorker /><div className={`capture-workspace ${cameraMode ? "capture-camera-workspace" : ""}`}>
    <header className={`capture-intro ${cameraMode ? "capture-camera-intro" : ""}`}><div className="capture-utility-bar"><ProductIdentity product="Capture" />{!cameraMode && <ThemeSelector />}</div><h1>{kind === "ORIGIN" ? "Registrar vídeo de origem" : kind === "RECAPTURE" ? "Registrar complemento em vídeo" : kind === "VIDEO_REPLACEMENT" ? "Gravar a vistoria novamente" : "Registrar vistoria em vídeo"}</h1></header>
    <Card className={`capture-card capture-card--wide ${cameraMode ? "capture-camera-card" : ""}`}>
      {!cameraMode && <><h2>Um vídeo contínuo</h2><p>Filme devagar, mantenha a câmera estável, mostre os ambientes com boa luz e aproxime detalhes importantes. O tempo máximo autorizado é {clock(current?.maxDurationSeconds ?? drafts[0]?.maxDurationSeconds ?? policyLimit)}. O limite de tempo não comprova cobertura de todo o imóvel.</p>{kind === "VIDEO_REPLACEMENT" && <p role="status">{presentReplacementNotice(recaptureDeadlineAt)}</p>}{references}</>}
      {cameraMode && <h2 className="capture-camera-title">{phase === "recording" ? "Gravação em andamento" : "Câmera em foco"}</h2>}
      {cameraMode && <div className="capture-camera-layout"><div className="capture-camera-preview"><video ref={videoRef} autoPlay muted playsInline onPlaying={(event) => { const video = event.currentTarget; const live = cameraStream?.getVideoTracks().some((track) => track.readyState === "live"); setPreviewReady(video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA && video.videoWidth > 0 && video.videoHeight > 0 && Boolean(live)); }} onLoadedMetadata={(event) => { const video = event.currentTarget; setPreviewReady(video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA && video.videoWidth > 0 && video.videoHeight > 0 && Boolean(cameraStream?.getVideoTracks().some((track) => track.readyState === "live"))); }} /></div><div className="capture-camera-controls">
        {phase === "ready" && <><p>Câmera pronta · ainda não está gravando</p>{previewBlocked && <Button variant="secondary" onClick={() => { if (videoRef.current) void playPreview(videoRef.current).then((result) => setPreviewBlocked(result === "blocked")); }}>Ativar prévia</Button>}<Button disabled={busy || !readyToRecord} onClick={() => void start()}>Iniciar gravação</Button><Button variant="secondary" disabled={busy} onClick={() => { generation.current += 1; releaseCamera(cameraStream); cameraStreamRef.current = undefined; setCameraStream(undefined); setPreviewReady(false); setPhase("idle"); }}>Fechar câmera</Button></>}
        {phase === "opening" && <p>Abrindo câmera…</p>}
        {phase === "starting" && <p>Preparando gravação…</p>}
        {phase === "recording" && <><strong>Gravando</strong><p role="timer">{clock(elapsed)} / {clock(current?.maxDurationSeconds ?? 900)}</p><p>Sem áudio · Filme devagar e mantenha a câmera estável.</p><Button disabled={busy} onClick={() => void finish()}>Parar e revisar</Button></>}
        {phase === "finalizing" && <p>Finalizando vídeo…</p>}
        <details className="capture-camera-references"><summary>Ver referências</summary>{references}</details>
      </div></div>}
      <div className="capture-camera-live" role="status" aria-live="polite">{liveMessage}</div>
      {!online && <Alert tone="warning">Sem conexão. Uma gravação só fica recuperável após finalização e salvamento neste dispositivo; o envio exige conexão e autorização vigente.</Alert>}
      {(!supportedMime || !canOpenCamera()) && <Alert tone="warning">Este navegador não oferece uma câmera ou formato de gravação compatível. Abra o convite no Safari ou Chrome atualizado do aparelho. A gravação não continua em segundo plano; fechar o navegador antes de finalizar pode perder o vídeo.</Alert>}
      {phase === "idle" && <>{cameraMode && <p>Filme devagar, mantenha a câmera estável, mostre os ambientes com boa luz e aproxime detalhes importantes. O tempo máximo autorizado é {clock(current?.maxDurationSeconds ?? drafts[0]?.maxDurationSeconds ?? policyLimit)}.</p>}<div className="capture-actions"><Button disabled={busy || !supportedMime || !canOpenCamera() || drafts.some((item) => ["SAVED", "CONFIRMED", "RECEIVED"].includes(item.state))} onClick={() => void open()}>Abrir câmera</Button>{allowFile && <><Button variant="secondary" disabled={busy} onClick={() => fileInput.current?.click()}>Escolher vídeo permitido</Button><input ref={fileInput} type="file" accept="video/mp4,video/quicktime,video/webm" hidden onChange={(event) => void importFile(event)} /></>}</div></>}
      {phase === "preview" && current && <div><h3>Revise a gravação</h3>{stoppedAtLimit && <Alert tone="warning">O limite desta gravação foi atingido. Revise o vídeo antes de confirmar o envio.</Alert>}{previewURL && <MutedVideoPreview src={previewURL} onCanPlay={(event) => { if (event.currentTarget.duration <= current.maxDurationSeconds) setPlayable(true); }} onLoadedMetadata={(event) => { if (event.currentTarget.duration > current.maxDurationSeconds) { setPlayable(false); setNotice("Este vídeo excede o limite da tentativa. Grave novamente."); } }} onError={() => { setPlayable(false); setNotice("Este dispositivo não conseguiu reproduzir o vídeo. Grave novamente em um navegador compatível."); }} />}<p>{clock(elapsed)} de até {clock(current.maxDurationSeconds)}. O vídeo só será enviado após sua confirmação.</p><div className="capture-actions"><Button variant="secondary" disabled={busy} onClick={() => void discard()}>Descartar e gravar novamente</Button><Button disabled={!playable || busy || !online} onClick={() => void resume(current)}>Confirmar e enviar</Button></div></div>}
      {phase === "uploading" && <p>Enviando vídeo: {completedParts} de {totalParts || "…"} partes recebidas. Mantenha esta página aberta enquanto o envio estiver em andamento.</p>}
      {(phase === "received" || phase === "submitted") && <div><Status tone={blockedVideoStatuses.has(current?.preparationStatus ?? "") ? "warning" : "success"}>Vídeo recebido</Status>{needsSensitiveReview(current?.preparationStatus) ? <p>Recebemos seu vídeo, mas a análise está pausada para uma revisão de privacidade. Você será avisado se for preciso gravar novamente.</p> : <p>Estado do vídeo: {preparationLabel(current?.preparationStatus)}.</p>}{blockedVideoStatuses.has(current?.preparationStatus ?? "") && !needsSensitiveReview(current?.preparationStatus) && <Alert tone="warning">A preparação desta gravação foi interrompida. O gestor da vistoria poderá solicitar uma nova gravação.</Alert>}{phase === "received" && !blockedVideoStatuses.has(current?.preparationStatus ?? "") && <Button disabled={busy || !online} onClick={() => void submit()}>Confirmar vistoria</Button>}</div>}
      {shownDrafts.length > 0 && phase !== "submitted" && <section><h3>Gravações neste dispositivo</h3><ul>{shownDrafts.map((draft) => <li key={draft.id}><span>{draft.state === "INCOMPLETE" ? "Gravação incompleta; não há recuperação garantida" : draft.state === "UNSAVED" ? "Salvamento falhou" : draft.state === "RECEIVED" ? `Recebida · ${preparationLabel(draft.preparationStatus)}` : draft.state === "CONFIRMED" ? "Envio pendente" : "Salva localmente; envio pendente"}</span>{draft.state === "SAVED" && <Button variant="secondary" disabled={busy} onClick={() => void showPreview(draft).catch((error) => setNotice(message(error)))}>Revisar</Button>}{(draft.state === "INCOMPLETE" || draft.state === "UNSAVED") && <Button variant="secondary" disabled={busy} onClick={() => void discard(draft)}>Descartar e gravar novamente</Button>}{(draft.state === "CONFIRMED" || draft.state === "RECEIVED") && <Button variant="secondary" disabled={busy || !online} onClick={() => void (draft.state === "RECEIVED" ? refreshVideoReceipt(draft).then((updated) => { updateDraft(updated); setPhase("received"); }).catch((error) => setNotice(message(error))) : resume(draft))}>{draft.state === "RECEIVED" ? "Atualizar status" : "Retomar envio"}</Button>}</li>)}</ul></section>}
      {notice && <Alert tone="warning">{notice}</Alert>}
    </Card><p className="capture-privacy-note">Acesso pessoal vinculado ao convite. Não compartilhe este link.</p>
  </div></main>;
}
