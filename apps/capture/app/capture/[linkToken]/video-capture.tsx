"use client";

import React, { type ChangeEvent, useEffect, useRef, useState } from "react";
import { Alert, Button, Card, Field, Icon, Input, ProductIdentity, Select, Status, Textarea, ThemeSelector } from "@inspection/design-system";
import type { ExternalCaptureBootstrapQuery } from "@/graphql/generated";
import { captureOperations, graphql } from "@/graphql/client";
import { throwOnUserErrors } from "@/pwa/mutation-errors";
import { appendVideoChunk, discardVideoDraft, finalizeVideoDraft, listVideoDrafts, loadVideoDraft, resetVideoDraft, saveVideoDraft, type VideoDraft, videoChunks } from "@/pwa/video-drafts";
import { startVideoRecorder, supportedVideoMime, type VideoRecorderSession } from "@/pwa/video-recorder";
import { clipIsTooShort, clipQualityWarnings, loadQualityState, saveQualityState, type CaptureQualityCode } from "@/pwa/quality";
import { attachPreview, canOpenCamera, openCameraCancellable, playPreview, releaseCamera } from "@/pwa/camera-session";
import { acquireScreenWakeLock } from "@/pwa/wake-lock";
import { captureErrorMessage as message, recoverCompletedVideoDrafts, reusableEnvironmentAttempt, unsupportedRecorderMessage, visibleDrafts } from "@/pwa/video-capture-state";
import { refreshVideoReceipt, uploadVideoDraft } from "@/pwa/video-uploads";
import { RegisterServiceWorker } from "@/components/register-service-worker";
import { presentReplacementNotice } from "./presentation";
import { EnvironmentDeclaration } from "./environment-declaration";
import { ReviewHandoff } from "./review-handoff";
import { CameraHint, CameraShutter, CameraSideButton, CameraStage, CameraThumbnail } from "./camera-stage";
import { EnvironmentTypeIcon } from "./environment-type-icon";

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
  captureLayout?: string | null;
  answers: External["answers"];
  environments: External["environments"];
  online: boolean;
  onSessionFailure: (error: unknown) => boolean;
  onDeclareEnvironment?: (typeCode: string, name: string, report?: (message: string) => void) => Promise<string | undefined>;
  onManageEnvironment?: (requirementKey: string, name: string, remove: boolean, report?: (message: string) => void) => Promise<boolean>;
  reviewHandoff?: External["reviewHandoff"];
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

export function VideoCapture({ responsibilityId, kind, recaptureRequestId, recaptureDeadlineAt, policy, requirements, referenceItems, comparisonMode, captureLayout, answers, environments, online, onSessionFailure, onDeclareEnvironment, onManageEnvironment, reviewHandoff }: Props) {
  const [drafts, setDrafts] = useState<VideoDraft[]>([]);
  const [current, setCurrent] = useState<VideoDraft>();
  const [phase, setPhaseState] = useState<Phase>("idle");
  const phaseRef = useRef<Phase>("idle");
  const setPhase = (next: Phase) => { phaseRef.current = next; setPhaseState(next); };
  const [elapsed, setElapsed] = useState(0);
  const elapsedRef = useRef(0);
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
  const [selectedEnvironmentKey, setSelectedEnvironmentKey] = useState("");
  const [environmentView, setEnvironmentView] = useState<"list" | "add" | "detail" | "review">(requirements.some((item) => item.key.startsWith("env:")) ? "list" : "add");
  const [editingEnvironmentKey, setEditingEnvironmentKey] = useState("");
  const [environmentNameDraft, setEnvironmentNameDraft] = useState("");
  const [pendingRemovalKey, setPendingRemovalKey] = useState("");
  const [managingEnvironment, setManagingEnvironment] = useState(false);
  const [replaceEnvironmentMediaId, setReplaceEnvironmentMediaId] = useState<string>();
  const [clipDuration, setClipDuration] = useState<number>();
  const [impossibilityReasonCode, setImpossibilityReasonCode] = useState("NO_ACCESS");
  const [impossibilityReasonText, setImpossibilityReasonText] = useState("");
  const [hintOpen, setHintOpen] = useState(true);
  const [helpOpen, setHelpOpen] = useState(false);
  const [captureAnswers, setCaptureAnswers] = useState(answers);
  const [localQualityWarnings, setLocalQualityWarnings] = useState<Record<string, CaptureQualityCode[]>>({});
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
  const selectedRequirement = requirements.find((item) => item.key === (captureLayout === "ENVIRONMENTS" ? selectedEnvironmentKey : kind === "RECAPTURE" ? "video:supplement" : "video:main"));
  const allowFile = policy.allowGallery === true && Boolean(selectedRequirement) && selectedRequirement?.captureSourcePolicy !== "CAMERA_ONLY";
  const supportedMime = supportedVideoMime();
  const policyLimit = typeof policy.maxDurationSeconds === "number" && policy.maxDurationSeconds > 0 ? policy.maxDurationSeconds : 900;
  const scope = comparisonMode === "FIXED_ORIGIN" && referenceItems.length > 0 ? "comparison" : "general";

  useEffect(() => { setLocalQualityWarnings(loadQualityState(responsibilityId).warnings); void listVideoDrafts(responsibilityId).then(async (loaded) => { const environmentLayout = captureLayout === "ENVIRONMENTS"; if (environmentLayout) await Promise.all(loaded.filter((draft) => draft.state === "INCOMPLETE").map((draft) => discardVideoDraft(draft).catch(() => {}))); setDrafts(recoverCompletedVideoDrafts(loaded, environmentLayout)); }).catch(() => setNotice("Não foi possível ler gravações locais neste dispositivo.")); }, [responsibilityId, captureLayout]);
  useEffect(() => { if (captureLayout !== "ENVIRONMENTS") return; if (selectedEnvironmentKey && !requirements.some((item) => item.key === selectedEnvironmentKey)) setSelectedEnvironmentKey(""); else if (!selectedEnvironmentKey) setSelectedEnvironmentKey(requirements.find((item) => item.key.startsWith("env:"))?.key ?? ""); }, [captureLayout, requirements, selectedEnvironmentKey]);
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
        const saved = { ...(await activeSession.stop()), durationSeconds: elapsedRef.current };
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
    const requirementKey = captureLayout === "ENVIRONMENTS" ? selectedEnvironmentKey : undefined;
    if (captureLayout === "ENVIRONMENTS" && !requirementKey) throw new Error("Adicione ou selecione um ambiente antes de iniciar a captura.");
    const replacesMediaId = replaceEnvironmentMediaId ?? (current?.mediaId && blockedVideoStatuses.has(current.preparationStatus ?? "") && !needsSensitiveReview(current.preparationStatus) ? current.mediaId : undefined);
    if (requirementKey) {
      const requirement = requirements.find((item) => item.key === requirementKey);
      const answerIds = captureAnswers.find((answer) => answer.requirementKey === requirementKey)?.mediaIds ?? [];
      const clips = drafts.filter((draft) => draft.requirementKey === requirementKey && draft.mediaId && !blockedVideoStatuses.has(draft.preparationStatus ?? ""));
      const serverCount = Math.max(answerIds.length, environments.find((item) => item.requirementKey === requirementKey)?.mediaCount ?? 0);
      if (!replacesMediaId && Math.max(clips.length, serverCount) >= (requirement?.maximumMedia ?? 3)) throw new Error("Este ambiente permite até 3 clipes.");
    }
    const admittedDraft = captureLayout === "ENVIRONMENTS" ? reusableEnvironmentAttempt(admitted.current, requirementKey) : admitted.current;
    const localDrafts = admittedDraft ? [] : await listVideoDrafts(responsibilityId);
    const reusable = admittedDraft ?? localDrafts.filter((draft) => draft.source === source && draft.requirementKey === requirementKey && !draft.mediaId && ["READY", "INCOMPLETE", "UNSAVED"].includes(draft.state) && Date.parse(draft.expiresAt) > Date.now()).sort((left, right) => Date.parse(right.expiresAt) - Date.parse(left.expiresAt))[0];
    if (!replacesMediaId && reusable && reusable.source === source && !reusable.mediaId && Date.parse(reusable.expiresAt) > Date.now()) {
      const bootstrap = (await graphql(captureOperations.bootstrap)).externalCapture;
      if (bootstrap.responsibilityId !== responsibilityId || bootstrap.status !== "OPEN") throw new Error("Este convite não permite mais gravações. Solicite uma nova autorização.");
      const persisted = await loadVideoDraft(reusable.id);
      if (persisted && !["READY", "INCOMPLETE", "UNSAVED"].includes(persisted.state)) throw new Error("Revise ou conclua o vídeo salvo antes de iniciar outra gravação.");
      const ready = persisted ? await resetVideoDraft(persisted) : reusable;
      const restarted: VideoDraft = { ...ready, state: "INCOMPLETE" };
      await saveVideoDraft(restarted);
      admitted.current = restarted;
      setDrafts((items) => [...items.filter((item) => item.id !== restarted.id), restarted]);
      return restarted;
    }
    const result = await graphql(captureOperations.beginVideo, { input: { requirementKey: requirementKey ?? null, acquisitionSource: source, replacesMediaId: replacesMediaId ?? null, clientMutationId: crypto.randomUUID() } });
    throwOnUserErrors(result.beginVideoCapture);
    const attempt = result.beginVideoCapture.attempt;
    if (!attempt) throw new Error("O servidor não autorizou uma tentativa de vídeo.");
    const draft: VideoDraft = { id: attempt.id, responsibilityId, kind, source, contentType: "", maxDurationSeconds: attempt.maxDurationSeconds, maxBytes: attempt.maxBytes, expiresAt: attempt.expiresAt, state: "INCOMPLETE", chunkCount: 0, sizeBytes: 0, requirementKey, replacesMediaId, parts: [] };
    admitted.current = draft;
    await saveVideoDraft(draft);
    setDrafts((items) => [...items.filter((item) => item.id !== draft.id), draft]);
    setReplaceEnvironmentMediaId(undefined);
    return draft;
  };
  const start = async () => {
    if (busy || actionPending.current || session.current || !cameraStream || !previewReady) return;
    actionPending.current = true; setBusy(true); setNotice(""); setStoppedAtLimit(false); limitNotified.current = false; elapsedRef.current = 0; setElapsed(0); setPhase("starting");
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
        elapsedRef.current = update.elapsedSeconds; setElapsed(update.elapsedSeconds);
        if (update.error) setNotice(message(new Error(update.error)));
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
    try { const ready = await resetVideoDraft(candidate); admitted.current = ready; setDrafts((items) => [...items.filter((item) => item.id !== ready.id), ready]); setCurrent(undefined); setPreviewURL(undefined); setStoppedAtLimit(false); setPhase("idle"); setNotice("Vídeo descartado. Você pode gravar novamente dentro da tentativa autorizada."); }
    catch (error) { setNotice(message(error)); } finally { setBusy(false); }
  };
  const resume = async (draft: VideoDraft) => {
    if (actionPending.current || !online) return;
    actionPending.current = true; setBusy(true); setPhase("uploading");
    try {
      if (draft.requirementKey) { const warnings = clipQualityWarnings(draft.durationSeconds ?? 0); setLocalQualityWarnings((current) => { const next = { ...current, [draft.requirementKey!]: warnings }; saveQualityState(responsibilityId, { ...loadQualityState(responsibilityId), warnings: next }); return next; }); }
      const bootstrap = await graphql(captureOperations.bootstrap);
      if (bootstrap.externalCapture.responsibilityId !== responsibilityId || bootstrap.externalCapture.status !== "OPEN") throw new Error("Este convite não permite mais envio. Solicite uma nova autorização.");
      const received = await uploadVideoDraft(draft, (done, total) => { setCompletedParts(done); setTotalParts(total); });
      updateDraft(received); setPhase("received"); setNotice("Vídeo recebido. Aguarde a confirmação do status.");
    } catch (error) { if (!onSessionFailure(error)) { setDrafts(await listVideoDrafts(responsibilityId).catch(() => drafts)); setPhase("idle"); setNotice(message(error)); } }
    finally { actionPending.current = false; setBusy(false); }
  };
  const reportIgnoredWarning = (requirementKey: string, code: string) => {
    void graphql(captureOperations.qualityWarning, { input: { requirementKey, code, ignored: true, clientMutationId: `${responsibilityId}:${requirementKey}:${code}:ignored` } }).then((result) => throwOnUserErrors(result.recordCaptureQualityWarning)).catch(() => {});
    setLocalQualityWarnings((current) => { const next = { ...current, [requirementKey]: (current[requirementKey] ?? []).filter((warning) => warning !== code) }; saveQualityState(responsibilityId, { ...loadQualityState(responsibilityId), warnings: next }); return next; });
  };
  const submit = async () => {
    if (actionPending.current || !online || (captureLayout === "ENVIRONMENTS" ? !allEnvironmentsResolved : !current?.mediaId || current.state !== "RECEIVED" || blockedVideoStatuses.has(current.preparationStatus ?? ""))) return;
    actionPending.current = true; setBusy(true);
    try {
      if (kind === "RECAPTURE") {
        if (!recaptureRequestId) throw new Error("A solicitação de complemento não está disponível.");
        const result = await graphql(captureOperations.submitRecapture, { input: { requestId: recaptureRequestId, confirmIncomplete: false, clientMutationId: `${current?.id ?? crypto.randomUUID()}:submit` } });
        throwOnUserErrors(result.submitRecapture);
      } else {
        const result = await graphql(captureOperations.submit, { input: { confirmIncomplete: false, clientMutationId: `${current?.id ?? crypto.randomUUID()}:submit` } });
        throwOnUserErrors(result.submitCapture);
      }
      setPhase("submitted"); setNotice("Envio confirmado.");
    } catch (error) {
      try { const external = (await graphql(captureOperations.bootstrap)).externalCapture; if (external.status === "COMPLETED") { setPhase("submitted"); return; } } catch { /* Keep the pending state when reconciliation is unavailable. */ }
      if (!onSessionFailure(error)) setNotice(message(error));
    } finally { actionPending.current = false; setBusy(false); }
  };
  // Renames and removals run one at a time, and their feedback shows in this journey.
  const manageEnvironment = async (requirementKey: string, name: string, remove: boolean) => {
    if (!onManageEnvironment || managingEnvironment) return false;
    setManagingEnvironment(true);
    try { return await onManageEnvironment(requirementKey, name, remove, setNotice); } finally { setManagingEnvironment(false); }
  };
  const shownDrafts = visibleDrafts(drafts, phase);
  const environmentRequirements = captureLayout === "ENVIRONMENTS" ? requirements.filter((item) => item.key.startsWith("env:")) : [];
  const clipsForEnvironment = (key: string) => drafts.filter((draft) => draft.requirementKey === key && draft.state === "RECEIVED" && draft.mediaId && !drafts.some((replacement) => replacement.replacesMediaId === draft.mediaId));
  const environmentClipCount = (key: string) => Math.max(clipsForEnvironment(key).length, captureAnswers.find((answer) => answer.requirementKey === key)?.mediaIds.length ?? 0, environments.find((item) => item.requirementKey === key)?.mediaCount ?? 0);
  const readyEnvironmentKeys = new Set(environmentRequirements.filter((item) => Boolean(captureAnswers.find((answer) => answer.requirementKey === item.key)?.impossibilityReason) || clipsForEnvironment(item.key).length + (captureAnswers.find((answer) => answer.requirementKey === item.key)?.mediaIds.length ?? 0) >= item.minimumMedia).map((item) => item.key));
  const allEnvironmentsResolved = environmentRequirements.length > 0 && environmentRequirements.every((item) => readyEnvironmentKeys.has(item.key));
  const declareImpossible = async (requirementKey: string) => {
    if (impossibilityReasonCode === "OTHER" && !impossibilityReasonText.trim()) { setNotice("Explique o motivo para declarar a impossibilidade."); return; }
    try { const reasonText = impossibilityReasonText.trim(); const answer = captureAnswers.find((item) => item.requirementKey === requirementKey); const response = await graphql(captureOperations.impossibility, { input: { requirementKey, reason: reasonText || impossibilityReasonCode, reasonCode: impossibilityReasonCode, reasonText: impossibilityReasonCode === "OTHER" ? reasonText : reasonText || null, expectedVersion: answer?.version ?? null, clientMutationId: crypto.randomUUID() } }); throwOnUserErrors(response.declareCaptureImpossibility); setCaptureAnswers((current) => current.some((item) => item.requirementKey === requirementKey) ? current.map((item) => item.requirementKey === requirementKey ? { ...item, impossibilityReason: reasonText || impossibilityReasonCode, mediaIds: [], version: item.version + 1 } : item) : [...current, { requirementKey, impossibilityReason: reasonText || impossibilityReasonCode, mediaIds: [], version: 1 }]); setNotice("Ambiente marcado como impossibilidade."); }
    catch (error) { if (!onSessionFailure(error)) setNotice(message(error)); }
  };
  const cameraMode = ["opening", "ready", "starting", "recording", "finalizing"].includes(phase);
  const liveMessage = phase === "opening" ? "Abrindo câmera." : phase === "ready" ? "Câmera pronta. Ainda não está gravando." : phase === "starting" ? "Preparando gravação." : phase === "recording" ? "Gravando." : phase === "finalizing" ? "Finalizando vídeo…" : phase === "preview" ? "Vídeo salvo neste dispositivo. Revise antes de enviar." : phase === "uploading" ? "Enviando vídeo." : phase === "received" ? "Vídeo recebido." : phase === "submitted" ? "Envio confirmado." : "";
  const references = kind === "RECAPTURE" && referenceItems.length > 0 ? <section aria-labelledby="recapture-items-heading"><h3 id="recapture-items-heading">Itens a complementar</h3>{recaptureDeadlineAt && <p>Prazo: {new Intl.DateTimeFormat("pt-BR", { dateStyle: "long", timeStyle: "short" }).format(new Date(recaptureDeadlineAt))}</p>}<p>Grave um único vídeo mostrando os itens solicitados.</p><ul>{referenceItems.map((item) => <li key={`${item.requirementKey}:${item.evidenceId ?? item.mediaId}`}><strong>{item.description || [item.environment, item.component].filter(Boolean).join(" · ")}</strong>{item.view ? <span> · {item.view}</span> : null}{item.reason ? <p>Motivo: {item.reason}</p> : null}{item.imageUrl && item.availability === "AVAILABLE" ? <img src={item.imageUrl} alt={`Imagem de referência: ${item.description}`} loading="lazy" style={{ display: "block", maxWidth: 180 }} /> : <p>Imagem de referência indisponível.</p>}</li>)}</ul></section> : scope === "comparison" ? <section><h3>Referência de origem</h3><p>Use os ambientes e componentes abaixo como orientação. Grave um só vídeo; não é necessário criar um clipe por item.</p><ul>{referenceItems.map((item) => <li key={item.requirementKey}><strong>{item.environment || item.description}</strong>{item.component ? ` · ${item.component}` : ""}{item.ambiguous ? " · identidade incerta" : ""}{item.gap ? " · vista incompleta na origem" : ""}{item.imageUrl && <img src={item.imageUrl} alt={`Referência: ${item.description}`} loading="lazy" style={{ display: "block", maxWidth: 180 }} />}</li>)}</ul></section> : <p>Sem comparação histórica nesta captura. O resultado descreverá apenas o que for visível no vídeo.</p>;
  const readyToRecord = previewReady && !previewBlocked && Boolean(cameraStream?.getVideoTracks().some((track) => track.readyState === "live"));
  const selectedEnvironment = environmentRequirements.find((item) => item.key === selectedEnvironmentKey);
  const selectedEnvironmentLabel = selectedEnvironment?.environmentName || selectedEnvironment?.label || "Ambiente";
  const selectedEnvironmentCount = selectedEnvironment ? environmentClipCount(selectedEnvironment.key) : 0;
  const chooseEnvironment = (key: string) => { setSelectedEnvironmentKey(key); setPhase("idle"); setCurrent(undefined); setReplaceEnvironmentMediaId(undefined); setEditingEnvironmentKey(""); setPendingRemovalKey(""); setEnvironmentView("detail"); };
  const maxDuration = current?.maxDurationSeconds ?? drafts[0]?.maxDurationSeconds ?? policyLimit;
  const closeCamera = () => {
    generation.current += 1;
    actionPending.current = false;
    setBusy(false);
    releaseCamera(cameraStreamRef.current);
    cameraStreamRef.current = undefined;
    setCameraStream(undefined);
    setPreviewReady(false);
    setPhase("idle");
  };
  const videoLiveRegion = <div className="capture-camera-live" role="status" aria-live="polite">{liveMessage}</div>;
  const cameraHelpSheet = helpOpen ? {
    title: "Como gravar",
    onClose: () => setHelpOpen(false),
    content: <div className="camera-stage__sheet-actions"><ul><li>Filme devagar e sem pausas bruscas.</li><li>Mostre piso, paredes e teto com boa luz.</li><li>Aproxime-se dos detalhes importantes.</li></ul>{references}<Button variant="primary" onClick={() => setHelpOpen(false)}>Entendi</Button></div>,
  } : undefined;
  const cameraNotice = notice || !online ? <>{notice && <Alert tone="warning">{notice}</Alert>}{!online && <Alert tone="warning">Sem conexão. Uma gravação só fica recuperável após finalização e salvamento neste dispositivo; o envio exige conexão e autorização vigente.</Alert>}</> : undefined;
  const cameraStage = cameraMode ? <CameraStage
    label={`Câmera de vídeo${captureLayout === "ENVIRONMENTS" && selectedEnvironment ? ` · ${selectedEnvironment.environmentName || selectedEnvironment.label}` : ""}`}
    backLabel="Fechar câmera"
    onBack={phase === "ready" || phase === "opening" ? closeCamera : undefined}
    title={captureLayout === "ENVIRONMENTS" ? selectedEnvironment?.environmentName || selectedEnvironment?.label || "Gravação por ambiente" : kind === "ORIGIN" ? "Registrar vídeo de origem" : kind === "RECAPTURE" ? "Registrar complemento em vídeo" : kind === "VIDEO_REPLACEMENT" ? "Gravar a vistoria novamente" : "Registrar vistoria em vídeo"}
    subtitle={captureLayout === "ENVIRONMENTS" && selectedEnvironment ? `${environmentClipCount(selectedEnvironment.key)} de ${selectedEnvironment.maximumMedia} clipes` : `Limite de ${clock(maxDuration)}`}
    onHelp={() => setHelpOpen(true)}
    aspectRatio={videoRef.current && videoRef.current.videoWidth > 0 && videoRef.current.videoHeight > 0 ? videoRef.current.videoWidth / videoRef.current.videoHeight : 0.75}
    media={<video ref={videoRef} autoPlay muted playsInline onPlaying={(event) => { const video = event.currentTarget; const live = cameraStream?.getVideoTracks().some((track) => track.readyState === "live"); setPreviewReady(video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA && video.videoWidth > 0 && video.videoHeight > 0 && Boolean(live)); }} onPause={() => setPreviewReady(false)} aria-label="Imagem ao vivo da câmera" />}
    overlayTop={phase === "recording" ? <><div className="video-camera__timer" role="timer"><span aria-hidden="true" />{clock(elapsed)} / {clock(maxDuration)}</div><p className="video-camera__recording-note">Sem áudio · Mantenha esta página aberta até salvar.</p></> : phase === "ready" ? <CameraHint title={captureLayout === "ENVIRONMENTS" ? "Grave o ambiente inteiro" : "Grave a vistoria"} text={captureLayout === "ENVIRONMENTS" ? "Gire devagar de um canto ao outro." : "Filme devagar, mantenha a câmera estável, mostre os ambientes com boa luz e aproxime detalhes importantes."} open={hintOpen} onToggle={setHintOpen} /> : ["opening", "starting", "finalizing"].includes(phase) ? <span className="video-camera__phase">{phase === "opening" ? "Abrindo câmera…" : phase === "starting" ? "Preparando gravação…" : "Finalizando vídeo…"}</span> : undefined}
    overlayBottom={phase === "ready" && previewBlocked ? <Button variant="secondary" onClick={() => { if (videoRef.current) void playPreview(videoRef.current).then((result) => setPreviewBlocked(result === "blocked")); }}>Ativar prévia</Button> : undefined}
    areaLabel={phase === "recording" ? undefined : "Área exata do vídeo"}
    areaLabelVisibleWithOverlay
    strip={captureLayout === "ENVIRONMENTS" ? { label: "Ambientes da vistoria", items: environmentRequirements.map((item) => ({ key: item.key, label: item.environmentName || item.label, detail: `${environmentClipCount(item.key)}/${item.maximumMedia}`, done: readyEnvironmentKeys.has(item.key), current: item.key === selectedEnvironmentKey })), disabled: phase !== "ready" || busy, onSelect: (key) => { if (phase === "ready" && !busy) { setSelectedEnvironmentKey(key); setCurrent(undefined); setReplaceEnvironmentMediaId(undefined); } } } : undefined}
    left={<CameraThumbnail count={captureLayout === "ENVIRONMENTS" && selectedEnvironment ? environmentClipCount(selectedEnvironment.key) : drafts.length} label={captureLayout === "ENVIRONMENTS" && selectedEnvironment ? `${environmentClipCount(selectedEnvironment.key)} clipes de ${selectedEnvironment.environmentName || selectedEnvironment.label}` : `${drafts.length} gravações neste dispositivo`} />}
    center={phase === "ready" ? <CameraShutter mode="video" label="Iniciar gravação" onPress={() => void start()} disabled={busy || !readyToRecord} /> : phase === "recording" ? <CameraShutter mode="recording" label="Parar e revisar" onPress={() => void finish()} disabled={busy} progress={elapsed / maxDuration} /> : <CameraShutter mode="video" label={phase === "opening" ? "Abrindo câmera…" : phase === "starting" ? "Preparando gravação…" : "Finalizando vídeo…"} onPress={() => {}} disabled />}
    caption={phase === "ready" ? "Gravar" : phase === "recording" ? "Parar" : "Aguarde"}
    right={<CameraSideButton icon="tips" label="Dicas" onPress={() => setHelpOpen(true)} />}
    sheet={cameraHelpSheet}
    notice={cameraNotice}
    liveRegion={videoLiveRegion}
  /> : null;
  if (captureLayout === "ENVIRONMENTS" && !cameraMode) {
    const currentView = phase === "submitted" ? "done" : environmentView === "detail" && !selectedEnvironment ? "list" : environmentView;
    const environmentCard = (item: (typeof environmentRequirements)[number]) => {
      const label = item.environmentName || item.label;
      const count = environmentClipCount(item.key);
      const complete = readyEnvironmentKeys.has(item.key);
      const environment = environments.find((entry) => entry.requirementKey === item.key);
      return <article className="capture-video-room" key={item.key}>
        <div className="capture-video-room-head"><span className="capture-video-room-icon"><EnvironmentTypeIcon typeCode={item.environmentTypeCode ?? "OTHER"} /></span><div className="capture-video-room-copy"><h3>{label}</h3><span className={complete ? "capture-video-room-status capture-video-room-status--done" : "capture-video-room-status"}>{complete ? "✓ " : ""}{count ? `${count} vídeo${count > 1 ? "s" : ""} recebido${count > 1 ? "s" : ""}` : complete ? "Impossibilidade registrada" : "Aguardando vídeo"}</span></div>{environment?.addedDuringCapture && onManageEnvironment && <Button variant="text" className="capture-video-room-delete" aria-label={`Remover ${label}`} onClick={() => setPendingRemovalKey(item.key)}><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M4 7h16M9 7V4h6v3m3 0-1 13H7L6 7m4 4v6m4-6v6" /></svg></Button>}</div>
        <Button className="capture-video-room-cta" variant={count ? "secondary" : "primary"} onClick={() => chooseEnvironment(item.key)}>{count ? "Ver e gerenciar vídeos" : <> <Icon name="camera" size={18} /> Gravar {label}</>}</Button>
        {pendingRemovalKey === item.key && onManageEnvironment && <div className="capture-video-room-confirm" role="alert"><strong>Remover {label}?</strong><p>Este ambiente e seus vídeos sairão desta vistoria.</p><div><Button variant="secondary" onClick={() => setPendingRemovalKey("")}>Manter ambiente</Button><Button variant="text" disabled={busy || managingEnvironment} isPending={managingEnvironment} onClick={() => void manageEnvironment(item.key, "", true).then((ok) => { if (ok) { setPendingRemovalKey(""); if (selectedEnvironmentKey === item.key) setSelectedEnvironmentKey(""); setEnvironmentView("list"); } })}>Remover ambiente</Button></div></div>}
      </article>;
    };
    return <main className="capture-shell capture-video-journey-shell"><RegisterServiceWorker /><div className="capture-video-journey">
      <header className="capture-video-journey-header"><div className="capture-video-journey-brand"><strong>inspection</strong><span>/ capture</span></div><span>Vistoria em vídeo</span></header>
      <div className="capture-video-journey-content">
        {currentView === "done" ? <><div className="capture-video-journey-eyebrow">Confirmação</div><h1>Vistoria enviada</h1><p>Recebemos sua vistoria. Os vídeos seguem para verificação.</p><div className="capture-video-journey-success" role="status"><strong>✓ {environmentRequirements.length} ambiente{environmentRequirements.length === 1 ? "" : "s"} registrado{environmentRequirements.length === 1 ? "" : "s"}</strong></div><div className="capture-video-review-list" aria-label="Ambientes registrados">{environmentRequirements.map((item) => <div key={item.key}><strong>{item.environmentName || item.label}</strong><span>Registrado</span></div>)}</div><ReviewHandoff handoff={reviewHandoff} /></> : null}
        {currentView === "add" ? <>{environmentRequirements.length > 0 ? <Button variant="text" className="capture-video-back" onClick={() => setEnvironmentView("list")}>← Voltar aos ambientes</Button> : <div className="capture-video-journey-steps" aria-label="Etapa 1 de 3"><span className="is-current" /><span /><span /></div>}<div className="capture-video-journey-eyebrow">Escolher ambiente</div><h1>{environmentRequirements.length ? "Adicionar ambiente" : "Por onde vamos começar?"}</h1><p>Escolha o ambiente que você quer registrar agora.</p>{onDeclareEnvironment && <EnvironmentDeclaration layout="tiles" busy={busy} existing={environments.map((item) => ({ typeCode: item.typeCode, name: item.name }))} onDeclare={async (typeCode, name) => { const key = await onDeclareEnvironment(typeCode, name, setNotice); if (key) { setSelectedEnvironmentKey(key); setEnvironmentView("list"); } return key; }} />}</> : null}
        {currentView === "list" ? <><div className="capture-video-journey-eyebrow">Sua vistoria</div><h1>{environmentRequirements.length ? "Ambientes do imóvel" : "Vamos começar?"}</h1><p>{environmentRequirements.length ? "Adicione os ambientes e registre um de cada vez." : "Adicione o primeiro ambiente para gravar seu vídeo."}</p>{environmentRequirements.length ? <><div className="capture-video-progress-label">{readyEnvironmentKeys.size} de {environmentRequirements.length} ambientes concluídos</div><div className="capture-video-progress" role="progressbar" aria-valuemin={0} aria-valuemax={environmentRequirements.length} aria-valuenow={readyEnvironmentKeys.size} aria-label="Ambientes concluídos"><span style={{ width: `${readyEnvironmentKeys.size / environmentRequirements.length * 100}%` }} /></div><div className="capture-video-room-list">{environmentRequirements.map(environmentCard)}</div></> : <div className="capture-video-empty"><span className="capture-video-room-icon"><Icon name="building-2" /></span><p>Nenhum ambiente adicionado</p></div>}<Button variant="secondary" className="capture-video-add-button" onClick={() => setEnvironmentView("add")}>＋ Adicionar ambiente</Button></> : null}
        {currentView === "detail" && selectedEnvironment ? <><Button variant="text" className="capture-video-back" onClick={() => { setPhase("idle"); setEnvironmentView("list"); }}>← Todos os ambientes</Button><div className="capture-video-journey-eyebrow">Ambiente adicionado</div><h1>Grave este ambiente</h1><p>Tudo pronto para registrar este ambiente.</p><section className="capture-video-detail-panel" aria-label={`Ambiente ${selectedEnvironmentLabel}`}><div className="capture-video-detail-header"><div><h2>{selectedEnvironmentLabel}</h2><span className={readyEnvironmentKeys.has(selectedEnvironment.key) ? "capture-video-room-status capture-video-room-status--done" : "capture-video-room-status"}>{selectedEnvironmentCount ? `✓ ${selectedEnvironmentCount} vídeo${selectedEnvironmentCount > 1 ? "s" : ""} recebido${selectedEnvironmentCount > 1 ? "s" : ""}` : "Aguardando vídeo"}</span></div>{environments.find((entry) => entry.requirementKey === selectedEnvironment.key)?.addedDuringCapture && onManageEnvironment && <div className="capture-video-detail-actions"><Button variant="text" aria-label={`Renomear ${selectedEnvironmentLabel}`} onClick={() => { setEditingEnvironmentKey(selectedEnvironment.key); setEnvironmentNameDraft(selectedEnvironmentLabel); }}>✎</Button><Button variant="text" aria-label={`Remover ${selectedEnvironmentLabel}`} onClick={() => setPendingRemovalKey(selectedEnvironment.key)}>×</Button></div>}</div>
          {editingEnvironmentKey === selectedEnvironment.key && onManageEnvironment && <div className="capture-video-detail-edit"><Field label="Nome do ambiente"><Input aria-label="Nome do ambiente" value={environmentNameDraft} maxLength={80} onChange={(event) => setEnvironmentNameDraft(event.target.value)} /></Field><div><Button variant="text" onClick={() => setEditingEnvironmentKey("")}>Cancelar</Button><Button disabled={busy || managingEnvironment || !environmentNameDraft.trim()} isPending={managingEnvironment} onClick={() => void manageEnvironment(selectedEnvironment.key, environmentNameDraft, false).then((ok) => { if (ok) setEditingEnvironmentKey(""); })}>Salvar nome</Button></div></div>}
          {pendingRemovalKey === selectedEnvironment.key && onManageEnvironment && <div className="capture-video-room-confirm" role="alert"><strong>Remover {selectedEnvironmentLabel}?</strong><p>Este ambiente e seus vídeos sairão desta vistoria.</p><div><Button variant="secondary" onClick={() => setPendingRemovalKey("")}>Manter ambiente</Button><Button variant="text" disabled={busy || managingEnvironment} isPending={managingEnvironment} onClick={() => void manageEnvironment(selectedEnvironment.key, "", true).then((ok) => { if (ok) { setPendingRemovalKey(""); setSelectedEnvironmentKey(""); setEnvironmentView("list"); } })}>Remover ambiente</Button></div></div>}
          {phase === "preview" && current ? <div className="capture-video-detail-capture"><h3>Revise a gravação</h3>{stoppedAtLimit && <Alert tone="warning">O limite desta gravação foi atingido. Revise o vídeo antes de confirmar o envio.</Alert>}{previewURL && <MutedVideoPreview src={previewURL} onCanPlay={(event) => { const duration = event.currentTarget.duration; setClipDuration(duration); setPlayable(duration >= 1 && duration <= current.maxDurationSeconds); }} onLoadedMetadata={(event) => { const duration = event.currentTarget.duration; setClipDuration(duration); setPlayable(duration >= 1 && duration <= current.maxDurationSeconds); }} onError={() => { setPlayable(false); setNotice("Este dispositivo não conseguiu reproduzir o vídeo. Grave novamente em um navegador compatível."); }} />}<p>{clock(elapsed)} de até {clock(current.maxDurationSeconds)}. O vídeo só será enviado após sua confirmação.</p><Button variant="secondary" disabled={busy} onClick={() => void discard()}>Descartar e gravar novamente</Button><Button disabled={!playable || busy || !online} onClick={() => void resume(current)}>Confirmar e enviar</Button></div> : phase === "uploading" ? <p role="status">Enviando vídeo: {completedParts} de {totalParts || "…"} partes recebidas. Mantenha esta página aberta.</p> : selectedEnvironmentCount > 0 ? <div className="capture-video-detail-capture"><div className="capture-video-journey-success" role="status"><Status tone="success">Vídeo recebido</Status><p>A verificação do vídeo continua. Seu envio está registrado.</p></div><div className="capture-video-detail-links"><Button variant="text" disabled={busy || selectedEnvironmentCount >= selectedEnvironment.maximumMedia} onClick={() => { setReplaceEnvironmentMediaId(undefined); void open(); }}>Adicionar clipe</Button>{clipsForEnvironment(selectedEnvironment.key).slice(-1).map((clip) => <Button key={clip.id} variant="text" onClick={() => { setReplaceEnvironmentMediaId(clip.mediaId); void open(); }}>Substituir último vídeo</Button>)}</div></div> : <div className="capture-video-detail-capture"><div className="capture-video-detail-tip"><strong>Mostre o ambiente inteiro</strong><p>Filme devagar, com boa luz. Aproxime o celular dos detalhes importantes.</p></div><Button disabled={busy || !supportedMime || !canOpenCamera()} onClick={() => void open()}><Icon name="camera" size={18} /> Gravar vídeo</Button>{allowFile && <><Button variant="text" disabled={busy} onClick={() => fileInput.current?.click()}>Usar vídeo da galeria</Button><input ref={fileInput} type="file" accept="video/mp4,video/quicktime,video/webm" hidden onChange={(event) => void importFile(event)} /></>}<small>Até 3 clipes por ambiente, se precisar.</small></div>}
          {localQualityWarnings[selectedEnvironment.key]?.map((warning) => <Alert key={warning} tone="warning">Este clipe pode precisar de uma nova tentativa. <Button variant="text" onClick={() => reportIgnoredWarning(selectedEnvironment.key, warning)}>Continuar mesmo assim</Button></Alert>)}
          {!supportedMime && <div className="capture-video-impossibility"><Field label="Motivo"><Select aria-label="Motivo da impossibilidade do ambiente" value={impossibilityReasonCode} onChange={(event) => setImpossibilityReasonCode(event.target.value)}><option value="LOCKED">Trancado</option><option value="NO_ACCESS">Sem acesso</option><option value="DOES_NOT_EXIST">Não existe</option><option value="UNSAFE">Não é seguro</option><option value="OTHER">Outro</option></Select></Field>{impossibilityReasonCode === "OTHER" && <Field label="Detalhes"><Textarea aria-label="Detalhes da impossibilidade do ambiente" value={impossibilityReasonText} onChange={(event) => setImpossibilityReasonText(event.target.value)} /></Field>}<Button variant="secondary" disabled={impossibilityReasonCode === "OTHER" && !impossibilityReasonText.trim()} onClick={() => void declareImpossible(selectedEnvironment.key)}>Declarar impossibilidade</Button></div>}
        </section>{shownDrafts.some((draft) => draft.requirementKey === selectedEnvironment.key && draft.state !== "RECEIVED") && <details className="capture-video-local-drafts"><summary>Envios salvos neste dispositivo</summary><ul>{shownDrafts.filter((draft) => draft.requirementKey === selectedEnvironment.key).map((draft) => <li key={draft.id}><span>{draft.state === "SAVED" ? "Salvo localmente" : draft.state === "CONFIRMED" ? "Envio pendente" : draft.state === "RECEIVED" ? "Recebido" : "Gravação incompleta"}</span>{draft.state === "SAVED" && <Button variant="text" onClick={() => void showPreview(draft)}>Revisar</Button>}{draft.state === "CONFIRMED" && <Button variant="text" onClick={() => void resume(draft)}>Retomar envio</Button>}{(draft.state === "INCOMPLETE" || draft.state === "UNSAVED") && <Button variant="text" onClick={() => void discard(draft)}>Descartar</Button>}</li>)}</ul></details>}</> : null}
        {currentView === "review" ? <><Button variant="text" className="capture-video-back" onClick={() => setEnvironmentView("list")}>← Voltar</Button><div className="capture-video-journey-eyebrow">Última etapa</div><h1>Revise sua vistoria</h1><p>Confira os ambientes antes de confirmar.</p><div className="capture-video-review-list">{environmentRequirements.map((item) => <div key={item.key}><strong>{item.environmentName || item.label}</strong><span>{environmentClipCount(item.key)} vídeo{environmentClipCount(item.key) === 1 ? "" : "s"} recebido{environmentClipCount(item.key) === 1 ? "" : "s"}</span></div>)}</div></> : null}
        {notice && <Alert tone="warning" aria-live="polite">{notice}</Alert>}{!online && <Alert tone="warning">Sem conexão. O envio exige conexão e autorização vigente.</Alert>}{!supportedMime && currentView === "detail" && <Alert tone="warning">{unsupportedRecorderMessage(true)}</Alert>}{supportedMime && !canOpenCamera() && currentView === "detail" && <Alert tone="warning">Este navegador não oferece acesso à câmera. Abra o convite no Safari ou Chrome atualizado do aparelho.</Alert>}
      </div>
      {currentView === "review" ? <footer className="capture-video-journey-footer"><Button disabled={busy || !online || !allEnvironmentsResolved} isPending={busy} onClick={() => void submit()}>Confirmar vistoria</Button></footer> : currentView === "list" && environmentRequirements.length > 0 ? <footer className="capture-video-journey-footer">{allEnvironmentsResolved ? <Button onClick={() => setEnvironmentView("review")}>Revisar vistoria</Button> : <span>Falta registrar {environmentRequirements.length - readyEnvironmentKeys.size} ambiente{environmentRequirements.length - readyEnvironmentKeys.size === 1 ? "" : "s"}.</span>}</footer> : currentView === "detail" && selectedEnvironmentCount > 0 ? <footer className="capture-video-journey-footer"><Button variant="secondary" onClick={() => setEnvironmentView("add")}>＋ Adicionar próximo ambiente</Button>{allEnvironmentsResolved && <Button onClick={() => setEnvironmentView("review")}>Revisar vistoria</Button>}</footer> : null}
      <div className="capture-video-journey-legal">Convite pessoal · Não compartilhe seu acesso</div>
    </div></main>;
  }
  return <main className={`capture-shell ${cameraMode ? "capture-camera-shell" : ""}`}><RegisterServiceWorker /><div className={`capture-workspace ${cameraMode ? "capture-camera-workspace" : ""}`}>
    <header className={`capture-intro ${cameraMode ? "capture-camera-intro" : ""}`}><div className="capture-utility-bar"><ProductIdentity product="Capture" />{!cameraMode && <ThemeSelector />}</div><h1>{kind === "ORIGIN" ? "Registrar vídeo de origem" : kind === "RECAPTURE" ? "Registrar complemento em vídeo" : kind === "VIDEO_REPLACEMENT" ? "Gravar a vistoria novamente" : "Registrar vistoria em vídeo"}</h1></header>
    <Card className={`capture-card capture-card--wide ${cameraMode ? "capture-camera-card" : ""}`}>
      {phase === "submitted" && <section role="status"><Status tone="success">Confirmação concluída</Status><h2>Recebemos sua confirmação</h2><h3>Ambientes registrados</h3><ul aria-label="Ambientes registrados">{environmentRequirements.map((item) => <li key={item.key}>{item.environmentName || item.label}</li>)}</ul><ReviewHandoff handoff={reviewHandoff} /></section>}
      {!cameraMode && <><h2>{captureLayout === "ENVIRONMENTS" ? "Um clipe por ambiente" : "Um vídeo contínuo"}</h2><p>{captureLayout === "ENVIRONMENTS" ? "Escolha um ambiente e grave até três clipes curtos. Você pode regravar cada ambiente separadamente." : `Filme devagar, mantenha a câmera estável, mostre os ambientes com boa luz e aproxime detalhes importantes. O tempo máximo autorizado é ${clock(current?.maxDurationSeconds ?? drafts[0]?.maxDurationSeconds ?? policyLimit)}. O limite de tempo não comprova cobertura de todo o imóvel.`}</p>{kind === "VIDEO_REPLACEMENT" && <p role="status">{presentReplacementNotice(recaptureDeadlineAt)}</p>}{captureLayout !== "ENVIRONMENTS" && references}</>}
      {cameraMode ? cameraStage : videoLiveRegion}
      {!cameraMode && !online && <Alert tone="warning">Sem conexão. Uma gravação só fica recuperável após finalização e salvamento neste dispositivo; o envio exige conexão e autorização vigente.</Alert>}
      {!supportedMime && <Alert tone="warning">{unsupportedRecorderMessage(captureLayout === "ENVIRONMENTS")} A gravação não continua em segundo plano; fechar o navegador antes de finalizar pode perder o vídeo.{captureLayout === "ENVIRONMENTS" && selectedEnvironmentKey && <div><Field label="Motivo"><Select aria-label="Motivo da impossibilidade do ambiente" value={impossibilityReasonCode} onChange={(event) => setImpossibilityReasonCode(event.target.value)}><option value="LOCKED">Trancado</option><option value="NO_ACCESS">Sem acesso</option><option value="DOES_NOT_EXIST">Não existe</option><option value="UNSAFE">Não é seguro</option><option value="OTHER">Outro</option></Select></Field>{impossibilityReasonCode === "OTHER" && <Field label="Detalhes"><Textarea aria-label="Detalhes da impossibilidade do ambiente" value={impossibilityReasonText} onChange={(event) => setImpossibilityReasonText(event.target.value)} /></Field>}<Button variant="secondary" disabled={impossibilityReasonCode === "OTHER" && !impossibilityReasonText.trim()} onClick={() => void declareImpossible(selectedEnvironmentKey)}>Declarar impossibilidade</Button></div>}</Alert>}
      {supportedMime && !canOpenCamera() && <Alert tone="warning">Este navegador não oferece acesso à câmera. Abra o convite no Safari ou Chrome atualizado do aparelho.</Alert>}
      {phase === "idle" && <>{!cameraMode && captureLayout === "ENVIRONMENTS" && <p className="capture-video-next-step" role="status">{selectedEnvironment ? <>Próximo passo: grave <strong>{selectedEnvironment.environmentName || selectedEnvironment.label}</strong>. Você poderá rever o clipe antes de enviar.</> : "Escolha um ambiente acima para liberar a câmera ou a importação de vídeo."}</p>}{cameraMode && <p>{captureLayout === "ENVIRONMENTS" ? `Grave o ambiente selecionado. O limite é ${clock(policyLimit)} por clipe.` : `Filme devagar, mantenha a câmera estável, mostre os ambientes com boa luz e aproxime detalhes importantes. O tempo máximo autorizado é ${clock(current?.maxDurationSeconds ?? drafts[0]?.maxDurationSeconds ?? policyLimit)}.`}</p>}{!cameraMode && notice && <Alert tone="warning" aria-live="polite">{notice}</Alert>}<div className="capture-actions"><Button disabled={busy || !supportedMime || !canOpenCamera() || captureLayout === "ENVIRONMENTS" && !selectedEnvironment || captureLayout !== "ENVIRONMENTS" && drafts.some((item) => ["SAVED", "CONFIRMED", "RECEIVED"].includes(item.state))} onClick={() => void open()}>Abrir câmera</Button>{allowFile && <><Button variant="secondary" disabled={busy} onClick={() => fileInput.current?.click()}>Importar vídeo</Button><input ref={fileInput} type="file" accept="video/mp4,video/quicktime,video/webm" hidden onChange={(event) => void importFile(event)} /></>}</div></>}
      {phase === "preview" && current && <div><h3>Revise a gravação</h3>{stoppedAtLimit && <Alert tone="warning">O limite desta gravação foi atingido. Revise o vídeo antes de confirmar o envio.</Alert>}{clipDuration !== undefined && clipQualityWarnings(clipDuration).length > 0 && !clipIsTooShort(clipDuration) && <Alert tone="warning">Este clipe é curto. Se puder, grave novamente; você também pode continuar.</Alert>}{previewURL && <MutedVideoPreview src={previewURL} onCanPlay={(event) => { const duration = event.currentTarget.duration; setClipDuration(duration); if (duration < 1) { setPlayable(false); setNotice("Grave novamente: o clipe precisa ter pelo menos 1 segundo."); } else if (duration <= current.maxDurationSeconds) setPlayable(true); }} onLoadedMetadata={(event) => { const duration = event.currentTarget.duration; setClipDuration(duration); if (clipIsTooShort(duration)) { setPlayable(false); setNotice("Grave novamente: o clipe precisa ter pelo menos 1 segundo."); } else if (duration > current.maxDurationSeconds) { setPlayable(false); setNotice("Este vídeo excede o limite da tentativa. Grave novamente."); } else setPlayable(true); }} onError={() => { setPlayable(false); setNotice("Este dispositivo não conseguiu reproduzir o vídeo. Grave novamente em um navegador compatível."); }} />}<p>{clock(elapsed)} de até {clock(current.maxDurationSeconds)}. O vídeo só será enviado após sua confirmação.</p><div className="capture-actions"><Button variant="secondary" disabled={busy} onClick={() => void discard()}>Descartar e gravar novamente</Button><Button disabled={!playable || busy || !online} onClick={() => void resume(current)}>Confirmar e enviar</Button></div></div>}
      {phase === "uploading" && <p>Enviando vídeo: {completedParts} de {totalParts || "…"} partes recebidas. Mantenha esta página aberta enquanto o envio estiver em andamento.</p>}
      {(phase === "received" || phase === "submitted") && <div><Status tone={blockedVideoStatuses.has(current?.preparationStatus ?? "") ? "warning" : "success"}>Vídeo recebido</Status>{needsSensitiveReview(current?.preparationStatus) ? <p>Recebemos seu vídeo, mas a análise está pausada para uma revisão de privacidade. Você será avisado se for preciso gravar novamente.</p> : <p>Estado do vídeo: {preparationLabel(current?.preparationStatus)}.</p>}{blockedVideoStatuses.has(current?.preparationStatus ?? "") && !needsSensitiveReview(current?.preparationStatus) && <Alert tone="warning">A preparação desta gravação foi interrompida. O gestor da vistoria poderá solicitar uma nova gravação.</Alert>}{phase === "received" && captureLayout !== "ENVIRONMENTS" && !blockedVideoStatuses.has(current?.preparationStatus ?? "") && <Button disabled={busy || !online} onClick={() => void submit()}>Confirmar vistoria</Button>}</div>}
      {phase === "idle" && captureLayout === "ENVIRONMENTS" && allEnvironmentsResolved && <Button disabled={busy || !online} isPending={busy} onClick={() => void submit()}>Enviar vistoria</Button>}
      {shownDrafts.length > 0 && phase !== "submitted" && <section><h3>Gravações neste dispositivo</h3><ul>{shownDrafts.map((draft) => <li key={draft.id}><span>{draft.state === "INCOMPLETE" ? "Gravação incompleta; não há recuperação garantida" : draft.state === "UNSAVED" ? "Salvamento falhou" : draft.state === "RECEIVED" ? `Recebida · ${preparationLabel(draft.preparationStatus)}` : draft.state === "CONFIRMED" ? "Envio pendente" : "Salva localmente; envio pendente"}</span>{draft.state === "SAVED" && <Button variant="secondary" disabled={busy} onClick={() => void showPreview(draft).catch((error) => setNotice(message(error)))}>Revisar</Button>}{(draft.state === "INCOMPLETE" || draft.state === "UNSAVED") && <Button variant="secondary" disabled={busy} onClick={() => void discard(draft)}>Descartar e gravar novamente</Button>}{(draft.state === "CONFIRMED" || draft.state === "RECEIVED") && <Button variant="secondary" disabled={busy || !online} onClick={() => void (draft.state === "RECEIVED" ? refreshVideoReceipt(draft).then((updated) => { updateDraft(updated); setPhase("received"); }).catch((error) => setNotice(message(error))) : resume(draft))}>{draft.state === "RECEIVED" ? "Atualizar status" : "Retomar envio"}</Button>}</li>)}</ul></section>}
      {!cameraMode && notice && <Alert tone="warning">{notice}</Alert>}
    </Card><p className="capture-privacy-note">Acesso pessoal vinculado ao convite. Não compartilhe este link.</p>
  </div></main>;
}
