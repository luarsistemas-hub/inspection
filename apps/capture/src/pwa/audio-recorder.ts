import { saveAudioDraft, type AudioDraft } from "./audio-drafts";
import { VoiceFlowError, microphoneFailure, savedNoteMessage } from "./audio-notes-state";

export const maximumAudioDurationMs = 60_000;
export const maximumAudioBytes = 5 * 1024 * 1024;
export const minimumAudioDurationMs = 500;
const audioMimeTypes = ["audio/webm;codecs=opus", "audio/ogg;codecs=opus", "audio/mp4", "audio/webm"];

export type AudioRecorderState = "preparing" | "recording" | "finalizing" | "saved" | "discarded" | "failed";
export type AudioRecorderUpdate = { state: AudioRecorderState; elapsedMs: number; limitReached?: boolean; message?: string };
/** Every reason a note can end; all of them share one idempotent finalizer. */
export type AudioStopCause = "release" | "cancel" | "focus" | "background" | "video-end" | "device" | "limit";
export type AudioRecorderSession = { startedAt: number; active: () => boolean; stop: (reason?: Exclude<AudioStopCause, "limit" | "device">) => Promise<AudioDraft | undefined> };

export function supportedAudioMime(): string | undefined {
  if (typeof MediaRecorder === "undefined") return undefined;
  return audioMimeTypes.find((type) => MediaRecorder.isTypeSupported(type));
}

/** Opens an audio-only stream after an explicit gesture and stops late permission results after cancellation. */
export async function openMicrophone(isCancelled: () => boolean): Promise<MediaStream | undefined> {
  if (globalThis.isSecureContext === false) throw new VoiceFlowError("MICROPHONE_INSECURE", "A gravação de voz só funciona em uma conexão segura.");
  if (typeof navigator === "undefined" || typeof navigator.mediaDevices?.getUserMedia !== "function") throw new VoiceFlowError("MICROPHONE_UNSUPPORTED", "Este navegador não oferece gravação de voz. A captura de fotos e vídeos continua disponível.");
  let stream: MediaStream;
  try { stream = await navigator.mediaDevices.getUserMedia({ audio: true, video: false }); }
  catch (error) { throw microphoneFailure(error); }
  if (isCancelled()) { stream.getTracks().forEach((track) => track.stop()); return undefined; }
  const audioTrack = stream.getAudioTracks()[0];
  if (!audioTrack || audioTrack.readyState !== "live") { stream.getTracks().forEach((track) => track.stop()); throw new VoiceFlowError("MICROPHONE_UNAVAILABLE", "O microfone ficou indisponível. Tente novamente."); }
  return stream;
}

/** Records one isolated microphone stream and routes every stop cause through the same idempotent finalizer. */
export async function startAudioRecorder(input: {
  stream: MediaStream;
  identity: Omit<AudioDraft, "durationMs" | "contentType" | "sizeBytes" | "sha256" | "state" | "audio" | "createdAt">;
  resolveTarget?: (startedAt: number, endedAt: number) => AudioDraft["target"];
  now?: () => number;
  onUpdate: (update: AudioRecorderUpdate) => void;
  digest?: (blob: Blob) => Promise<string>;
}): Promise<AudioRecorderSession> {
  const mimeType = supportedAudioMime();
  if (!mimeType) { input.stream.getTracks().forEach((track) => track.stop()); throw new VoiceFlowError("AUDIO_FORMAT_UNSUPPORTED", "Este navegador não oferece um formato de áudio compatível. A captura visual continua disponível."); }
  const now = input.now ?? (() => performance.now());
  const recorder = new MediaRecorder(input.stream, { mimeType });
  const chunks: Blob[] = [];
  let sizeBytes = 0;
  let startedAt = 0;
  let finished: Promise<AudioDraft | undefined> | undefined;
  let recorderFailure: Error | undefined;
  let limitKind: "duration" | "size" = "duration";
  let timer: ReturnType<typeof setInterval> | undefined;
  const tracks = input.stream.getAudioTracks();
  const digest = input.digest ?? (async (blob: Blob) => {
    const hash = await crypto.subtle.digest("SHA-256", await blob.arrayBuffer());
    return Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, "0")).join("");
  });
  const cleanup = () => {
    if (timer) clearInterval(timer);
    timer = undefined;
    tracks.forEach((track) => track.removeEventListener("ended", onTrackEnded));
    input.stream.getTracks().forEach((track) => track.stop());
  };
  const elapsed = () => Math.max(0, now() - startedAt);
  const finalize = (reason: AudioStopCause = "release"): Promise<AudioDraft | undefined> => {
    if (finished) return finished;
    let resolve!: (draft: AudioDraft | undefined) => void;
    let reject!: (error: unknown) => void;
    // Assigned before any callback runs so a re-entrant stop reuses this run instead of starting another.
    finished = new Promise<AudioDraft | undefined>((done, fail) => { resolve = done; reject = fail; });
    input.onUpdate({ state: "finalizing", elapsedMs: Math.min(elapsed(), maximumAudioDurationMs), limitReached: reason === "limit" });
    const complete = async () => {
      cleanup();
      try {
        if (recorderFailure) throw recorderFailure;
        const durationMs = Math.floor(Math.min(elapsed(), maximumAudioDurationMs));
        const blob = new Blob(chunks, { type: recorder.mimeType || mimeType });
        if (durationMs < minimumAudioDurationMs) {
          input.onUpdate({ state: "discarded", elapsedMs: durationMs, message: "A gravação durou menos de meio segundo e não foi enviada. Segure novamente para falar." });
          resolve(undefined);
          return;
        }
        if (!blob.size || blob.size > maximumAudioBytes) throw new VoiceFlowError("AUDIO_TOO_LARGE", "A anotação ultrapassou o limite de 5 MB. Tente uma fala mais curta.");
        const target = input.resolveTarget?.(startedAt, startedAt + durationMs) ?? input.identity.target;
        const draft: AudioDraft = { ...input.identity, target, durationMs, contentType: blob.type || mimeType, sizeBytes: blob.size, sha256: await digest(blob), state: "PENDING_UPLOAD", audio: blob, createdAt: new Date().toISOString() };
        await saveAudioDraft(draft);
        input.onUpdate({ state: "saved", elapsedMs: durationMs, limitReached: reason === "limit", message: savedNoteMessage(reason, limitKind) });
        resolve(draft);
      } catch (error) {
        const message = error instanceof VoiceFlowError ? error.message : error instanceof DOMException && error.name === "QuotaExceededError" ? "O dispositivo ficou sem espaço. A anotação não foi salva; libere espaço e grave novamente." : error instanceof Error && error === recorderFailure ? "A gravação foi interrompida pelo navegador. A anotação não foi salva." : "Não foi possível salvar a anotação neste dispositivo.";
        input.onUpdate({ state: "failed", elapsedMs: Math.min(elapsed(), maximumAudioDurationMs), message });
        reject(new VoiceFlowError("AUDIO_NOT_SAVED", message));
      }
    };
    if (recorder.state === "inactive") void complete();
    else {
      recorder.onstop = () => { void complete(); };
      try { recorder.stop(); } catch (error) { recorderFailure = error instanceof Error ? error : new Error("A gravação foi interrompida pelo navegador."); void complete(); }
    }
    return finished;
  };
  function onTrackEnded() { void finalize("device").catch(() => {}); }
  recorder.ondataavailable = (event) => {
    if (!event.data.size) return;
    if (sizeBytes + event.data.size > maximumAudioBytes) { limitKind = "size"; void finalize("limit").catch(() => {}); return; }
    chunks.push(event.data);
    sizeBytes += event.data.size;
  };
  recorder.onerror = () => { recorderFailure = new Error("A gravação foi interrompida pelo navegador."); void finalize("device").catch(() => {}); };
  try {
    recorder.start(250);
    startedAt = now();
  } catch (error) {
    cleanup();
    throw error;
  }
  input.onUpdate({ state: "recording", elapsedMs: 0 });
  timer = setInterval(() => {
    const duration = elapsed();
    input.onUpdate({ state: "recording", elapsedMs: Math.min(duration, maximumAudioDurationMs), limitReached: duration >= maximumAudioDurationMs });
    if (duration >= maximumAudioDurationMs) void finalize("limit").catch(() => {});
  }, 100);
  tracks.forEach((track) => track.addEventListener("ended", onTrackEnded));
  return { startedAt, active: () => recorder.state === "recording" && !finished, stop: (reason = "release") => finalize(reason) };
}
