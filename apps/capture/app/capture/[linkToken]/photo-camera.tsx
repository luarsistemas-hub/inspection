"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@inspection/design-system";
import { CameraGlyph, CameraHint, CameraShutter, CameraSideButton, CameraStage, CameraThumbnail, CameraToast, type CameraStripItem } from "./camera-stage";
import { attachPreview, classifyCameraError, isInAppBrowser, openPhotoCameraCancellable, playPreview, releaseCamera } from "@/pwa/camera-session";
import { captureFrame } from "@/pwa/frame-capture";
import { presentCameraFailure } from "./presentation";
import { acquireScreenWakeLock } from "@/pwa/wake-lock";

type CameraFailureContent = { title: string; message: string; category: ReturnType<typeof classifyCameraError> };

/** Live rear camera for environment photos, rendered with CameraStage. */
export function PhotoCamera(props: {
  environmentLabel: string; photoCount: number; minimumMedia: number; maximumMedia: number; instructions?: string;
  items: CameraStripItem[]; onSelectItem: (key: string) => void;
  galleryAllowed: boolean; onGallery: () => void; onDeviceCamera: () => void;
  impossibilityAllowed: boolean; onImpossible: () => void; onClose: () => void;
  onCapture: (file: File, requirementKey: string) => Promise<void>; onCaptureError: (error: unknown) => void;
  preview?: { url: string; warnings: string[] }; onUsePreview: () => Promise<boolean>; onRetake: () => void;
  busy: boolean; locating: boolean; statusPill?: "uploading" | "offline"; lastPhotoURL?: string;
  toast?: { title: string; text?: string; tone?: "success" | "warning"; action?: { label: string; onPress: () => void } };
  onDismissToast: () => void; notice?: string;
}): React.JSX.Element {
  const videoRef = useRef<HTMLVideoElement>(null);
  const streamRef = useRef<MediaStream | undefined>(undefined);
  const generationRef = useRef(0);
  const openingRef = useRef(false);
  const reopenRequestedRef = useRef(false);
  const reopenAfterVisibilityRef = useRef(false);
  const failureRef = useRef(false);
  const mountedRef = useRef(true);
  const capturingRef = useRef(false);
  const wakeLockReleaseRef = useRef<(() => Promise<void>) | undefined>(undefined);
  const [previewReady, setPreviewReady] = useState(false);
  const [previewBlocked, setPreviewBlocked] = useState(false);
  const [aspectRatio, setAspectRatio] = useState(0.75);
  const [failure, setFailure] = useState<CameraFailureContent>();
  const [helpOpen, setHelpOpen] = useState(false);
  const [capturing, setCapturing] = useState(false);
  const [photoSaved, setPhotoSaved] = useState(false);
  const [flash, setFlash] = useState(false);
  const [hintOpen, setHintOpen] = useState(true);
  const priorKeyRef = useRef(props.items.find((item) => item.current)?.key);
  const currentKey = props.items.find((item) => item.current)?.key ?? "";
  const track = () => streamRef.current?.getVideoTracks()[0];

  const updatePreviewState = useCallback(() => {
    const video = videoRef.current;
    const activeTrack = streamRef.current?.getVideoTracks()[0];
    const ready = Boolean(video && video.readyState >= 2 && video.videoWidth > 0 && activeTrack?.readyState === "live" && !activeTrack.muted);
    setPreviewReady(ready);
    if (video && video.videoWidth > 0 && video.videoHeight > 0) setAspectRatio(video.videoWidth / video.videoHeight);
  }, []);

  const release = useCallback(() => {
    generationRef.current += 1;
    const stream = streamRef.current;
    streamRef.current = undefined;
    const video = videoRef.current;
    if (video && video.srcObject === stream) video.srcObject = null;
    releaseCamera(stream);
    setPreviewReady(false);
  }, []);

  const open = useCallback(async (retry = false) => {
    if (!mountedRef.current || props.preview) return;
    if (retry) { generationRef.current += 1; failureRef.current = false; setFailure(undefined); }
    if (openingRef.current) { reopenRequestedRef.current = true; return; }
    openingRef.current = true;
    const generation = ++generationRef.current;
    failureRef.current = false;
    setFailure(undefined);
    setPreviewReady(false);
    try {
      const stream = await openPhotoCameraCancellable(() => !mountedRef.current || generationRef.current !== generation || document.visibilityState === "hidden");
      if (!stream) return;
      if (!mountedRef.current || generationRef.current !== generation) { releaseCamera(stream); return; }
      streamRef.current = stream;
      const activeTrack = stream.getVideoTracks()[0];
      if (!activeTrack || activeTrack.readyState !== "live") {
        // Release without bumping the generation so the catch still reports the failure.
        streamRef.current = undefined;
        releaseCamera(stream);
        throw new Error("camera-frame-unavailable");
      }
      activeTrack.addEventListener("ended", updatePreviewState);
      activeTrack.addEventListener("mute", updatePreviewState);
      const video = videoRef.current;
      if (!video) { release(); return; }
      const result = await attachPreview(video, stream);
      if (generationRef.current !== generation) { releaseCamera(stream); return; }
      setPreviewBlocked(result === "blocked");
      updatePreviewState();
    } catch (error) {
      if (mountedRef.current && generationRef.current === generation) {
        failureRef.current = true;
        const failureKind = classifyCameraError(error);
        setFailure({ ...presentCameraFailure(failureKind, isInAppBrowser(navigator.userAgent)), category: failureKind });
      }
    } finally {
      openingRef.current = false;
      if (reopenRequestedRef.current) {
        reopenRequestedRef.current = false;
        if (mountedRef.current && document.visibilityState !== "hidden" && !failureRef.current && !props.preview) void open();
      }
    }
  }, [props.preview, release, updatePreviewState]);

  useEffect(() => {
    mountedRef.current = true;
    void open();
    void acquireScreenWakeLock().then((releaseWakeLock) => {
      if (mountedRef.current) wakeLockReleaseRef.current = releaseWakeLock;
      else void releaseWakeLock();
    });
    return () => {
      mountedRef.current = false;
      generationRef.current += 1;
      releaseCamera(streamRef.current);
      streamRef.current = undefined;
      const wakeRelease = wakeLockReleaseRef.current;
      wakeLockReleaseRef.current = undefined;
      void wakeRelease?.();
    };
    // Camera opens once on mount; retries are explicit user actions.
  }, []);

  useEffect(() => {
    const onVisibilityChange = () => {
      if (document.visibilityState === "hidden") {
        generationRef.current += 1;
        reopenAfterVisibilityRef.current = true;
        reopenRequestedRef.current = false;
        releaseCamera(streamRef.current);
        streamRef.current = undefined;
        if (videoRef.current) videoRef.current.srcObject = null;
        setPreviewReady(false);
        const wakeRelease = wakeLockReleaseRef.current;
        wakeLockReleaseRef.current = undefined;
        void wakeRelease?.();
      } else {
        void acquireScreenWakeLock().then((releaseWakeLock) => {
          if (mountedRef.current) wakeLockReleaseRef.current = releaseWakeLock;
          else void releaseWakeLock();
        });
        if (!failureRef.current && !props.preview) {
          reopenAfterVisibilityRef.current = false;
          if (openingRef.current) reopenRequestedRef.current = true;
          else void open();
        }
      }
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => document.removeEventListener("visibilitychange", onVisibilityChange);
  }, [open, props.preview]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    video.addEventListener("playing", updatePreviewState);
    video.addEventListener("pause", updatePreviewState);
    video.addEventListener("loadeddata", updatePreviewState);
    video.addEventListener("loadedmetadata", updatePreviewState);
    video.addEventListener("resize", updatePreviewState);
    return () => {
      video.removeEventListener("playing", updatePreviewState);
      video.removeEventListener("pause", updatePreviewState);
      video.removeEventListener("loadeddata", updatePreviewState);
      video.removeEventListener("loadedmetadata", updatePreviewState);
      video.removeEventListener("resize", updatePreviewState);
    };
  }, [updatePreviewState]);

  useEffect(() => {
    if (priorKeyRef.current !== currentKey) { priorKeyRef.current = currentKey; setHintOpen(true); setPhotoSaved(false); }
    const video = videoRef.current;
    if (props.preview && video && !video.paused) return;
    if (!props.preview && reopenAfterVisibilityRef.current && !streamRef.current && !failureRef.current) {
      reopenAfterVisibilityRef.current = false;
      void open();
      return;
    }
    if (!props.preview && video?.paused && streamRef.current) {
      void playPreview(video).then((result) => { setPreviewBlocked(result === "blocked"); updatePreviewState(); });
    }
  }, [currentKey, props.preview, updatePreviewState]);

  const activatePreview = async () => {
    const video = videoRef.current;
    if (!video) return;
    const result = await playPreview(video);
    setPreviewBlocked(result === "blocked");
    updatePreviewState();
  };

  const takePhoto = async () => {
    if (capturingRef.current || !previewReady || props.busy || props.preview || !videoRef.current) return;
    capturingRef.current = true;
    setCapturing(true);
    setPhotoSaved(false);
    const requirementKey = currentKey;
    if (!(typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches)) {
      setFlash(true);
      window.setTimeout(() => setFlash(false), 250);
    }
    try {
      const file = await captureFrame(videoRef.current, { track: track() });
      await props.onCapture(file, requirementKey);
    } catch (error) { props.onCaptureError(error); }
    finally { capturingRef.current = false; setCapturing(false); }
  };

  const warningsText = props.preview?.warnings.length ? `Esta foto pode estar ${props.preview.warnings.includes("DARK") && props.preview.warnings.includes("BLURRY") ? "com pouca luz e pouco nítida" : props.preview.warnings.includes("DARK") ? "com pouca luz" : "pouco nítida"}. Você pode tirar outra ou usar mesmo assim.` : undefined;
  const usePreview = async () => { const saved = await props.onUsePreview(); setPhotoSaved(saved); };
  const failureSheet = failure && !props.preview ? { title: failure.title, onClose: props.onClose, content: <><p>{failure.message}</p><div className="photo-camera__sheet-actions"><Button variant="primary" onClick={() => void open(true)}>Tentar novamente</Button>{failure.category !== "insecure" && <Button variant="secondary" onClick={props.onDeviceCamera}>Usar câmera do aparelho</Button>}{props.galleryAllowed && <Button variant="secondary" onClick={props.onGallery}>Escolher da galeria</Button>}<Button variant="text" onClick={props.onClose}>Voltar</Button></div></> } : undefined;
  const sheet = helpOpen ? { title: "Como fazer uma boa foto", onClose: () => setHelpOpen(false), content: <div className="photo-camera__sheet-actions"><ul><li>Acenda as luzes e abra as cortinas.</li><li>Segure o celular com as duas mãos.</li><li>Use a vertical para itens e a horizontal para ambientes amplos.</li></ul>{props.impossibilityAllowed && <Button variant="secondary" onClick={props.onImpossible}>Não consigo fotografar este ambiente</Button>}<Button variant="primary" onClick={() => setHelpOpen(false)}>Entendi</Button></div> } : failureSheet;
  const statusPill = props.statusPill === "uploading" ? <><CameraGlyph name="upload" /> Enviando…</> : props.statusPill === "offline" ? <><CameraGlyph name="offline" /> Salvo no aparelho</> : undefined;
  const captureToast = props.toast && !props.preview ? <CameraToast title={props.toast.title} text={props.toast.text} tone={props.toast.tone} action={props.toast.action} onDismiss={props.onDismissToast} /> : undefined;
  const review = props.preview ? <div className="photo-camera__review">{warningsText && <p>{warningsText}</p>}<div><button type="button" disabled={props.busy || capturing} onClick={props.onRetake}>Tirar outra</button><button type="button" disabled={props.busy || capturing} onClick={() => void usePreview()}>{props.locating ? "Obtendo localização…" : "Usar esta foto"}</button></div></div> : captureToast;

  return <CameraStage label={`Câmera · ${props.environmentLabel}`} backLabel="Ambientes" onBack={() => { if (!capturingRef.current) props.onClose(); }} title={props.environmentLabel} subtitle={props.photoCount >= props.minimumMedia ? `${props.photoCount} ${props.photoCount === 1 ? "foto" : "fotos"} · máx. ${props.maximumMedia}` : `${props.photoCount} de ${props.minimumMedia} ${props.minimumMedia === 1 ? "foto" : "fotos"}`} statusPill={statusPill} onHelp={() => setHelpOpen(true)} aspectRatio={aspectRatio}
    media={<div className="photo-camera__media">{!props.preview && previewBlocked && <button type="button" onClick={() => void activatePreview()}>Ativar prévia</button>}{props.preview && <img src={props.preview.url} alt="Prévia da foto capturada" />}{<video ref={videoRef} autoPlay muted playsInline aria-label="Imagem ao vivo da câmera" style={{ visibility: props.preview ? "hidden" : undefined }} />}{failure && !props.preview && <svg className="photo-camera__failure-icon" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2"><path d="M3 7h3l2-3h8l2 3h3v12H3z" /><circle cx="12" cy="13" r="4" /></svg>}{flash && <div className="photo-camera__flash" aria-hidden="true" />}</div>}
    overlayTop={failure && !props.preview ? undefined : props.preview ? undefined : <CameraHint title="Mostre o ambiente inteiro" text={props.instructions || "Comece de um canto. Inclua piso, paredes e móveis."} open={hintOpen} onToggle={setHintOpen} />}
    overlayBottom={review}
    areaLabel="Área exata da foto"
    strip={{ label: "Ambientes da vistoria", items: props.items, onSelect: (key) => { if (!capturingRef.current && !props.preview && !props.busy) props.onSelectItem(key); }, disabled: props.busy || capturing || Boolean(props.preview) }}
    left={<CameraThumbnail url={props.lastPhotoURL} count={props.photoCount} label={`Ver ${props.photoCount} ${props.photoCount === 1 ? "foto" : "fotos"} de ${props.environmentLabel}`} onPress={() => { if (!capturingRef.current) props.onClose(); }} busy={props.busy || capturing} />}
    center={<CameraShutter mode="photo" label="Fotografar" onPress={() => void takePhoto()} disabled={!previewReady || props.busy || capturing || Boolean(props.preview)} />}
    caption="Fotografar"
    right={<CameraSideButton icon={props.galleryAllowed ? "gallery" : "tips"} label={props.galleryAllowed ? "Galeria" : "Dicas"} onPress={props.galleryAllowed ? props.onGallery : () => setHelpOpen(true)} disabled={props.busy || capturing || Boolean(props.preview)} />}
    showGuides={!failure || Boolean(props.preview)} sheet={sheet} notice={props.preview ? props.notice : undefined} liveMessage={props.preview ? "Foto capturada. Revise antes de usar." : photoSaved ? "Foto salva." : previewReady ? "Câmera pronta." : failure ? "" : "Abrindo câmera."} />;
}
