import type { VideoDraft } from "./video-drafts";

const cameraPhases = new Set(["opening", "ready", "starting", "recording", "finalizing"]);

/** The local list stays hidden while the camera is in use, so the take being recorded is never shown as incomplete or discardable. */
export function visibleDrafts(drafts: VideoDraft[], phase: string): VideoDraft[] {
  return cameraPhases.has(phase) ? [] : drafts;
}

export function captureErrorMessage(error: unknown): string {
  if (error instanceof DOMException && error.name === "NotAllowedError") return "Câmera bloqueada. Libere o acesso nas configurações do navegador e tente novamente.";
  if (error instanceof DOMException && error.name === "NotReadableError") return "A câmera está ocupada ou indisponível. Feche outros aplicativos e tente novamente.";
  if (error instanceof DOMException && (error.name === "NotFoundError" || error.name === "OverconstrainedError")) return "Nenhuma câmera disponível neste dispositivo.";
  if (error instanceof DOMException && error.name === "QuotaExceededError") return "O dispositivo ficou sem espaço. O vídeo não foi salvo; libere espaço e grave novamente.";
  return error instanceof Error ? error.message : "Não foi possível concluir a captura. Tente novamente.";
}
