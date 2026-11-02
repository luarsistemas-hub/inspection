"use client";

import React, { useEffect, useId, useRef, useState, type KeyboardEvent, type MouseEvent, type PointerEvent } from "react";
import { Button } from "@inspection/design-system";
import { openMicrophone, startAudioRecorder, type AudioRecorderSession, type AudioStopCause } from "@/pwa/audio-recorder";
import type { AudioDraft } from "@/pwa/audio-drafts";
import { uploadAudioDraft } from "@/pwa/audio-uploads";
import { presentVoiceError, savedNoteMessage, voiceBlockReason, waitsForPhoto } from "@/pwa/audio-notes-state";

/** The anchor a new note will carry: one saved photo, or the live clip whose capture clock started at `clipStartedAt`. */
export type VoiceTarget = { kind: "PHOTO"; photoDraftId: string; mediaId?: string } | { kind: "VIDEO"; attemptId: string; clipStartedAt: number };

type Props = {
  responsibilityId: string;
  requirementKey: string;
  environmentLabel: string;
  disclosureVersion: string;
  consentVersion: string;
  enabled: boolean;
  online: boolean;
  target?: VoiceTarget;
  /** A reason the owner already knows, such as an exhausted allowance. */
  blockedReason?: string;
  /** Hides the explanatory copy when the control sits over the camera. */
  compact?: boolean;
  onRecordingChange?: (recording: boolean) => void;
};

type Phase = "idle" | "opening" | "recording" | "finalizing";

/** Lets the page keep the room and photo fixed while a note is being recorded. */
function reportVoiceActivity(recording: boolean, requirementKey: string) {
  window.dispatchEvent(new CustomEvent("inspection-audio-state", { detail: { recording, requirementKey } }));
}

const keyboardKeys = new Set(["Enter", " ", "Spacebar"]);
/** Window between a keyboard event and its synthesized click; a click outside it comes from assistive technology. */
const keyboardClickWindowMs = 350;

/** Deliberate voice control: hold to speak, or use Enter/Space (and assistive-technology activation) to start and stop. */
export function VoiceNoteControl(props: Props) {
  const { enabled, compact = false } = props;
  const statusId = useId();
  const hintId = useId();
  const [phase, setPhaseState] = useState<Phase>("idle");
  const [elapsedMs, setElapsedMs] = useState(0);
  const [message, setMessage] = useState("");
  const phaseRef = useRef<Phase>("idle");
  const session = useRef<AudioRecorderSession | undefined>(undefined);
  const mode = useRef<"hold" | "toggle">("hold");
  const pointerId = useRef<number | undefined>(undefined);
  const openToken = useRef(0);
  const lastKeyAt = useRef(-Infinity);
  const mounted = useRef(true);
  const latest = useRef(props);
  latest.current = props;

  const setPhase = (next: Phase) => { phaseRef.current = next; if (mounted.current) setPhaseState(next); };
  const say = (text: string) => { if (mounted.current) setMessage(text); };
  const report = (recording: boolean) => { reportVoiceActivity(recording, latest.current.requirementKey); latest.current.onRecordingChange?.(recording); };
  const blocked = voiceBlockReason({ enabled, hasTarget: Boolean(props.target), kind: props.target?.kind ?? "PHOTO", limitReached: Boolean(props.blockedReason) });
  const blockedText = props.blockedReason ?? blocked;

  /** Sends a saved note when it can be sent now; otherwise tells the truth about where it is. */
  const settle = async (draft: AudioDraft, cause: AudioStopCause): Promise<string> => {
    const lead = cause === "release" || cause === "cancel" ? "" : `${savedNoteMessage(cause)} `;
    if (!latest.current.online) return `${lead}Anotação salva neste dispositivo; o envio está pendente até a conexão voltar.`.trim();
    try {
      const sent = await uploadAudioDraft(draft);
      if (sent.state === "RECEIVED") return `${lead}Anotação recebida e aguardando processamento.`.trim();
      const detail = waitsForPhoto(sent) ? "Anotação salva neste dispositivo; será enviada assim que a foto for enviada." : "Anotação salva neste dispositivo; o envio continua em andamento.";
      return `${lead}${detail}`.trim();
    } catch (error) {
      return `${lead}${presentVoiceError(error, "Anotação salva neste dispositivo; o envio está pendente.")}`.trim();
    }
  };

  const finish = async (cause: AudioStopCause): Promise<void> => {
    const active = session.current;
    if (!active) return;
    session.current = undefined;
    setPhase("finalizing");
    say("Finalizando a anotação…");
    try {
      const draft = await active.stop(cause === "limit" || cause === "device" ? "release" : cause);
      if (draft) say(await settle(draft, cause));
    } catch (error) {
      say(presentVoiceError(error, "Não foi possível salvar a anotação. A captura visual continua disponível."));
    } finally {
      setPhase("idle");
      setElapsedMs(0);
      report(false);
    }
  };

  const cancelOpening = (text: string) => {
    openToken.current += 1;
    setPhase("idle");
    say(text);
    report(false);
  };

  const start = async (startMode: "hold" | "toggle"): Promise<void> => {
    if (phaseRef.current !== "idle") return;
    const current = latest.current;
    if (!current.enabled || !current.target || current.blockedReason) { say(current.blockedReason ?? voiceBlockReason({ enabled: current.enabled, hasTarget: Boolean(current.target), kind: current.target?.kind ?? "PHOTO", limitReached: false }) ?? ""); return; }
    mode.current = startMode;
    const token = ++openToken.current;
    setPhase("opening");
    say("Preparando o microfone…");
    report(true);
    const superseded = () => token !== openToken.current || !mounted.current;
    try {
      const stream = await openMicrophone(superseded);
      if (!stream) { if (token === openToken.current && mounted.current) cancelOpening("A solicitação do microfone foi cancelada. Pressione novamente para falar."); return; }
      const target = latest.current.target;
      if (!target) { stream.getTracks().forEach((track) => track.stop()); cancelOpening("Selecione uma foto antes de gravar uma anotação."); return; }
      const identity = {
        id: crypto.randomUUID(), responsibilityId: current.responsibilityId, requirementKey: current.requirementKey,
        disclosureVersion: current.disclosureVersion, consentVersion: current.consentVersion,
        target: target.kind === "PHOTO" ? { kind: "PHOTO" as const, photoDraftId: target.photoDraftId, ...(target.mediaId ? { mediaId: target.mediaId } : {}) } : { kind: "VIDEO" as const, attemptId: target.attemptId, startMs: 0, endMs: 1, timeBasis: "CAPTURE_CLOCK" as const }
      };
      const started = await startAudioRecorder({
        stream, identity,
        resolveTarget: target.kind === "VIDEO" ? (startedAt, endedAt) => ({ kind: "VIDEO", attemptId: target.attemptId, startMs: Math.max(0, Math.floor(startedAt - target.clipStartedAt)), endMs: Math.max(1, Math.floor(endedAt - target.clipStartedAt)), timeBasis: "CAPTURE_CLOCK" }) : undefined,
        onUpdate: (update) => {
          if (update.state === "recording") setElapsedMs(update.elapsedMs);
          if (update.state === "discarded" || update.state === "failed") say(update.message ?? "");
          // The recorder ended by itself (size/time limit or the microphone stopping): run the same finish path so the note is sent.
          if (update.state === "finalizing" && phaseRef.current === "recording") void finish(update.limitReached ? "limit" : "device");
        }
      });
      if (superseded()) { void started.stop("cancel").catch(() => undefined); return; }
      session.current = started;
      setPhase("recording");
      say(startMode === "hold" ? "Gravando. Solte o botão para encerrar." : "Gravando. Pressione Enter ou Espaço novamente para encerrar.");
    } catch (error) {
      if (token === openToken.current && mounted.current) cancelOpening(presentVoiceError(error, "Não foi possível iniciar a gravação. A captura visual continua disponível."));
    }
  };

  /** Ends whatever is in progress; used by every cause that is not an explicit stop gesture. */
  const interrupt = (cause: AudioStopCause) => {
    if (phaseRef.current === "opening") cancelOpening("A solicitação do microfone foi cancelada. Pressione novamente para falar.");
    else if (phaseRef.current === "recording") void finish(cause);
  };
  const toggle = () => {
    if (phaseRef.current === "idle") void start("toggle");
    else if (phaseRef.current === "opening") cancelOpening("A solicitação do microfone foi cancelada. Pressione novamente para falar.");
    else if (phaseRef.current === "recording") void finish("release");
  };
  const interruptRef = useRef(interrupt);
  interruptRef.current = interrupt;

  useEffect(() => {
    mounted.current = true;
    const release = (event: Event) => {
      const pointer = event as unknown as globalThis.PointerEvent;
      if (mode.current !== "hold" || pointerId.current === undefined || pointer.pointerId !== pointerId.current) return;
      pointerId.current = undefined;
      interruptRef.current(event.type === "pointercancel" ? "cancel" : "release");
    };
    const blur = () => interruptRef.current("focus");
    const hidden = () => { if (document.visibilityState === "hidden") interruptRef.current("background"); };
    const pagehide = () => interruptRef.current("background");
    const videoEnd = () => interruptRef.current("video-end");
    window.addEventListener("pointerup", release);
    window.addEventListener("pointercancel", release);
    window.addEventListener("blur", blur);
    window.addEventListener("pagehide", pagehide);
    window.addEventListener("inspection-video-end", videoEnd);
    document.addEventListener("visibilitychange", hidden);
    return () => {
      window.removeEventListener("pointerup", release);
      window.removeEventListener("pointercancel", release);
      window.removeEventListener("blur", blur);
      window.removeEventListener("pagehide", pagehide);
      window.removeEventListener("inspection-video-end", videoEnd);
      document.removeEventListener("visibilitychange", hidden);
      mounted.current = false;
      openToken.current += 1;
      const active = session.current;
      session.current = undefined;
      if (active) void active.stop("cancel").then((draft) => { if (draft) return uploadAudioDraft(draft).catch(() => undefined); }).catch(() => undefined).finally(() => report(false));
      else if (phaseRef.current === "opening") report(false);
    };
  }, []);

  const recording = phase === "recording";
  const opening = phase === "opening";
  const unavailable = Boolean(blockedText) || phase === "finalizing";
  const label = recording ? "Encerrar anotação de voz" : opening ? "Preparando o microfone. Pressione novamente para cancelar" : "Segure para falar ou pressione Enter ou Espaço";
  const visible = recording ? "Encerrar anotação" : opening ? "Preparando microfone…" : phase === "finalizing" ? "Salvando…" : "Segure para falar";
  const idleHint = "Segure para falar, ou use Enter ou Espaço para iniciar e encerrar. Na primeira vez o navegador pede permissão do microfone.";
  const status = message || (recording ? "Gravando." : blockedText ?? "");

  return <section className={`capture-voice-notes${compact ? " capture-voice-notes--compact" : ""}`} aria-label={`Gravar anotação de voz · ${props.environmentLabel}`}>
    {!compact && <p id={hintId} className="capture-voice-notes__hint">{props.target?.kind === "VIDEO" ? "O vídeo continua sem faixa de áudio. Cada anotação usa somente o microfone, depois da sua autorização." : "Cada anotação fica ligada à foto escolhida e usa o microfone somente enquanto você grava."} {idleHint}</p>}
    <Button type="button" variant={recording ? "danger" : "secondary"} className={`capture-voice-notes__button${recording ? " capture-voice-notes__button--recording" : ""}`} draggable={false}
      aria-label={label} aria-disabled={unavailable || undefined} aria-describedby={`${statusId}${compact ? "" : ` ${hintId}`}`}
      onPointerDown={(event: PointerEvent<HTMLButtonElement>) => {
        if (event.pointerType === "mouse" && event.button !== 0) return;
        if (unavailable) { if (phaseRef.current === "idle") say(blockedText ?? ""); return; }
        event.preventDefault();
        if (phaseRef.current === "recording" && mode.current === "toggle") { void finish("release"); return; }
        try { event.currentTarget.setPointerCapture(event.pointerId); } catch { /* window listeners still end the note */ }
        pointerId.current = event.pointerId;
        if (phaseRef.current === "idle") void start("hold");
      }}
      onKeyDown={(event: KeyboardEvent<HTMLButtonElement>) => {
        if (!keyboardKeys.has(event.key)) return;
        event.preventDefault();
        if (event.repeat) return;
        lastKeyAt.current = performance.now();
        if (!unavailable || phaseRef.current !== "idle") toggle(); else say(blockedText ?? "");
      }}
      onKeyUp={(event: KeyboardEvent<HTMLButtonElement>) => { if (keyboardKeys.has(event.key)) event.preventDefault(); }}
      onClick={(event: MouseEvent<HTMLButtonElement>) => {
        // Pointer clicks are handled on press/release; a click with no pointer and no recent key is assistive-technology activation.
        if (event.detail === 0 && performance.now() - lastKeyAt.current > keyboardClickWindowMs) { if (!unavailable || phaseRef.current !== "idle") toggle(); else say(blockedText ?? ""); }
      }}
      onContextMenu={(event) => event.preventDefault()}>
      {recording && <span className="capture-voice-notes__pulse" aria-hidden="true" />}{visible}{recording && <span className="capture-voice-notes__elapsed" aria-hidden="true"> · {Math.floor(elapsedMs / 1000)} s</span>}
    </Button>
    <p id={statusId} role="status" aria-live="polite" className="capture-voice-notes__status">{status}</p>
  </section>;
}
