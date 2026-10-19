import { appendVideoChunk, finalizeVideoDraft, saveVideoDraft, type VideoDraft } from "./video-drafts";

const candidateTypes = [
  "video/mp4;codecs=avc1.42E01E",
  "video/mp4",
  "video/webm;codecs=vp9",
  "video/webm;codecs=vp8",
  "video/webm"
];

export function supportedVideoMime(): string | undefined {
  if (typeof MediaRecorder === "undefined") return undefined;
  return candidateTypes.find((type) => MediaRecorder.isTypeSupported(type));
}

export type RecorderUpdate = { elapsedSeconds: number; limitReached: boolean; error?: string };
export type VideoRecorderSession = { stop: () => Promise<VideoDraft>; active: () => boolean };

export async function startVideoRecorder(
  draft: VideoDraft,
  hash: (attemptId: string, chunkCount: number) => Promise<string>,
  onUpdate: (update: RecorderUpdate) => void,
  now: () => number = Date.now
): Promise<VideoRecorderSession> {
  const mimeType = supportedVideoMime();
  if (!mimeType) throw new Error("Este navegador não oferece um formato de gravação compatível. Abra o convite no Safari ou Chrome atualizado.");
  const stream = await navigator.mediaDevices.getUserMedia({ audio: false, video: { facingMode: { ideal: "environment" }, width: { ideal: 1920 }, height: { ideal: 1080 }, frameRate: { ideal: 30 } } });
  let recorder: MediaRecorder;
  try { recorder = new MediaRecorder(stream, { mimeType, videoBitsPerSecond: 4_000_000 }); }
  catch (error) { stream.getTracks().forEach((track) => track.stop()); throw error; }
  let current: VideoDraft = { ...draft, contentType: recorder.mimeType || mimeType, state: "INCOMPLETE" };
  let writes: Promise<void> = Promise.resolve();
  let writeError: unknown;
  let recorderError: Error | undefined;
  let stopped: Promise<VideoDraft> | undefined;
  const startedAt = now();
  const timer: { id?: ReturnType<typeof setInterval> } = {};
  const cleanup = () => { if (timer.id) clearInterval(timer.id); stream.getTracks().forEach((track) => track.stop()); };
  try {
    await saveVideoDraft(current);
    recorder.onstop = () => {
      cleanup();
      onUpdate({ elapsedSeconds: (now() - startedAt) / 1000, limitReached: false, error: "A gravação foi interrompida pelo navegador. O vídeo incompleto pode não ser recuperável." });
    };
    recorder.onerror = () => {
      recorderError = new Error("A gravação foi interrompida pelo navegador.");
      onUpdate({ elapsedSeconds: (now() - startedAt) / 1000, limitReached: false, error: recorderError.message });
      void stop().catch(() => {});
    };
    recorder.ondataavailable = (event) => {
      if (event.data.size < 1) return;
      if (current.sizeBytes + event.data.size > draft.maxBytes) {
        writeError = new Error("O vídeo excedeu o limite de tamanho desta tentativa. Grave novamente.");
        onUpdate({ elapsedSeconds: (now() - startedAt) / 1000, limitReached: false, error: "O vídeo excedeu o limite de tamanho desta tentativa. Grave novamente." });
        void stop().catch(() => {});
        return;
      }
      const sequence = current.chunkCount;
      current = { ...current, chunkCount: sequence + 1, sizeBytes: current.sizeBytes + event.data.size };
      writes = writes.then(async () => {
        await appendVideoChunk(current.id, sequence, event.data);
        await saveVideoDraft(current);
      }).catch((error) => { writeError = error; onUpdate({ elapsedSeconds: (now() - startedAt) / 1000, limitReached: false, error: "Não foi possível salvar a gravação neste dispositivo." }); });
    };
    recorder.start(1000);
  } catch (error) { cleanup(); throw error; }

  function stop(): Promise<VideoDraft> {
    if (stopped) return stopped;
    stopped = new Promise<VideoDraft>((resolve, reject) => {
      recorder.onstop = async () => {
        cleanup();
        try {
          await writes;
          if (recorderError) throw recorderError;
          if (writeError) throw writeError;
          const finalized = await finalizeVideoDraft(current, hash);
          resolve(finalized);
        } catch (error) { reject(error); }
      };
      if (recorder.state !== "inactive") recorder.stop();
      else { cleanup(); reject(new Error("A gravação terminou antes da finalização.")); }
    });
    return stopped;
  }
  timer.id = setInterval(() => {
    const elapsedSeconds = Math.max(0, (now() - startedAt) / 1000);
    const limitReached = elapsedSeconds >= draft.maxDurationSeconds;
    onUpdate({ elapsedSeconds: Math.min(elapsedSeconds, draft.maxDurationSeconds), limitReached });
    if (limitReached) void stop().catch((error) => onUpdate({ elapsedSeconds: draft.maxDurationSeconds, limitReached: true, error: error instanceof Error ? error.message : "Gravação interrompida." }));
  }, 250);
  return { stop, active: () => recorder.state === "recording" };
}
