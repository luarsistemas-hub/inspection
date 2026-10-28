import { appendVideoChunk, discardVideoDraft, finalizeVideoDraft, saveVideoDraft, type VideoDraft } from "./video-drafts";

const candidateTypes = ["video/mp4;codecs=avc1.42E01E", "video/mp4", "video/webm;codecs=vp9", "video/webm;codecs=vp8", "video/webm"];

export function supportedVideoMime(): string | undefined {
  if (typeof MediaRecorder === "undefined") return undefined;
  return candidateTypes.find((type) => MediaRecorder.isTypeSupported(type));
}

export type RecorderUpdate = { elapsedSeconds: number; limitReached: boolean; error?: string; finalizing?: boolean };
export type VideoRecorderSession = { stop: () => Promise<VideoDraft>; active: () => boolean };
export const videoDurationReachedLimit = (elapsedSeconds: number, maxDurationSeconds: number): boolean => elapsedSeconds >= maxDurationSeconds;

/**
 * Starts recording an already opened stream. Until `recorder.start()` succeeds the caller owns the stream and
 * must release it on failure; afterwards the session owns it and stops its tracks when finalizing.
 */
export async function startVideoRecorder(
  stream: MediaStream,
  draft: VideoDraft,
  hash: (attemptId: string, chunkCount: number) => Promise<string>,
  onUpdate: (update: RecorderUpdate) => void,
  now: () => number = Date.now,
  isCancelled: () => boolean = () => false
): Promise<VideoRecorderSession> {
  const mimeType = supportedVideoMime();
  if (!mimeType) throw new Error("Este navegador não oferece um formato de gravação compatível. Abra o convite no Safari ou Chrome atualizado.");
  const abort = () => new DOMException("A abertura da câmera foi cancelada.", "AbortError");
  if (isCancelled()) throw abort();
  const recorder = new MediaRecorder(stream, { mimeType, videoBitsPerSecond: 4_000_000 });
  let current: VideoDraft = { ...draft, contentType: recorder.mimeType || mimeType, state: "INCOMPLETE" };
  let writes: Promise<void> = Promise.resolve();
  let writeError: Error | undefined;
  let recorderError: Error | undefined;
  let stopped: Promise<VideoDraft> | undefined;
  let startedAt = 0;
  const timerRef: { value?: ReturnType<typeof setInterval> } = {};
  const videoTracks = stream.getVideoTracks();
  const elapsed = () => Math.max(0, (now() - startedAt) / 1000);
  const clearTimer = () => { if (timerRef.value) clearInterval(timerRef.value); timerRef.value = undefined; };
  const cleanup = () => {
    clearTimer();
    videoTracks.forEach((track) => track.removeEventListener("ended", onTrackEnded));
    stream.getTracks().forEach((track) => track.stop());
  };
  const finalizeQuietly = () => { void finalizeOnce().catch(() => {}); };
  function onTrackEnded() {
    onUpdate({ elapsedSeconds: elapsed(), limitReached: false, finalizing: true });
    finalizeQuietly();
  }
  function finalizeOnce(): Promise<VideoDraft> {
    if (stopped) return stopped;
    clearTimer();
    stopped = new Promise<VideoDraft>((resolve, reject) => {
      const finish = async () => {
        cleanup();
        try {
          await writes;
          if (recorderError) throw recorderError;
          if (writeError) throw writeError;
          if (current.chunkCount < 1) throw new Error("A gravação foi interrompida antes de salvar qualquer trecho. Grave novamente.");
          resolve(await finalizeVideoDraft(current, hash));
        } catch (error) { reject(error); }
      };
      if (recorder.state === "inactive") void finish();
      else {
        recorder.onstop = () => { void finish(); };
        try { recorder.stop(); } catch (error) { recorderError = error instanceof Error ? error : new Error("A gravação foi interrompida pelo navegador."); void finish(); }
      }
    });
    // Notify only after `stopped` is registered so a reentrant stop() reuses this promise.
    queueMicrotask(() => onUpdate({ elapsedSeconds: Math.min(elapsed(), draft.maxDurationSeconds), limitReached: elapsed() >= draft.maxDurationSeconds, finalizing: true }));
    return stopped;
  }
  await saveVideoDraft(current);
  recorder.ondataavailable = (event) => {
    if (event.data.size < 1) return;
    if (current.sizeBytes + event.data.size > draft.maxBytes) {
      writeError = new Error("O vídeo excedeu o limite de tamanho desta tentativa. Grave novamente.");
      onUpdate({ elapsedSeconds: elapsed(), limitReached: false, error: writeError.message });
      finalizeQuietly();
      return;
    }
    const sequence = current.chunkCount;
    const next = { ...current, chunkCount: sequence + 1, sizeBytes: current.sizeBytes + event.data.size };
    current = next;
    writes = writes.then(async () => { await appendVideoChunk(next.id, sequence, event.data); await saveVideoDraft(next); }).catch((error) => {
      writeError = error instanceof Error ? error : new Error("Não foi possível salvar a gravação neste dispositivo.");
      onUpdate({ elapsedSeconds: elapsed(), limitReached: false, error: "Não foi possível salvar a gravação neste dispositivo." });
      finalizeQuietly();
    });
  };
  recorder.onerror = () => {
    recorderError = new Error("A gravação foi interrompida pelo navegador.");
    onUpdate({ elapsedSeconds: elapsed(), limitReached: false, error: recorderError.message });
    finalizeQuietly();
  };
  // Unexpected browser stops use the same persistence path as an explicit stop.
  recorder.onstop = finalizeQuietly;
  if (isCancelled()) {
    await discardVideoDraft(current).catch(() => {});
    throw abort();
  }
  recorder.start(1000);
  startedAt = now();
  timerRef.value = setInterval(() => {
    const duration = elapsed();
    const limitReached = videoDurationReachedLimit(duration, draft.maxDurationSeconds);
    onUpdate({ elapsedSeconds: Math.min(duration, draft.maxDurationSeconds), limitReached });
    if (limitReached) finalizeQuietly();
  }, 250);
  videoTracks.forEach((track) => track.addEventListener("ended", onTrackEnded));
  return { stop: finalizeOnce, active: () => recorder.state === "recording" };
}
