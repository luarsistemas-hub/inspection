import type { VideoDraft } from "./video-drafts";

const cameraPhases = new Set(["opening", "ready", "starting", "recording", "finalizing"]);

/** The local list stays hidden while the camera is in use, so the take being recorded is never shown as incomplete or discardable. */
export function visibleDrafts(drafts: VideoDraft[], phase: string): VideoDraft[] {
  if (cameraPhases.has(phase)) return [];
  return drafts.some((draft) => draft.state === "READY") ? drafts.filter((draft) => draft.state !== "READY") : drafts;
}

export function captureErrorMessage(error: unknown): string {
  if (error instanceof DOMException && error.name === "NotAllowedError") return "Câmera bloqueada. Libere o acesso nas configurações do navegador e tente novamente.";
  if (error instanceof DOMException && error.name === "NotReadableError") return "A câmera está ocupada ou indisponível. Feche outros aplicativos e tente novamente.";
  if (error instanceof DOMException && (error.name === "NotFoundError" || error.name === "OverconstrainedError")) return "Nenhuma câmera disponível neste dispositivo.";
  if (error instanceof DOMException && error.name === "QuotaExceededError") return "O dispositivo ficou sem espaço. O vídeo não foi salvo; libere espaço e grave novamente.";
  const message = error instanceof Error ? error.message : "";
  if (/environment requirement is required/i.test(message)) return "O ambiente escolhido não está disponível para este vídeo. Atualize a captura e tente novamente.";
  if (/^(Use |Abra |Confira |Aguarde |Conecte-se|Grave novamente|Falha ao verificar vídeo|Não foi possível|A câmera |O dispositivo |Este vídeo |O vídeo |Este convite |A gravação |O servidor )/i.test(message)) return message;
  return "Não foi possível concluir o vídeo. Revise a gravação e tente novamente.";
}

/** An admitted attempt is reused only for the same environment and before its media exists. */
export function reusableEnvironmentAttempt(admitted: VideoDraft | undefined, requirementKey: string | undefined): VideoDraft | undefined {
  return admitted && admitted.requirementKey === requirementKey && !admitted.mediaId ? admitted : undefined;
}

/** Environment clips discard partial recordings on reopen; the continuous v1 video keeps them visible. */
export function recoverCompletedVideoDrafts(drafts: VideoDraft[], environmentLayout = true): VideoDraft[] {
  return environmentLayout ? drafts.filter((draft) => draft.state !== "INCOMPLETE") : drafts;
}

export function unsupportedRecorderMessage(environmentLayout = false): string {
  const base = "Este navegador não oferece um formato de gravação compatível. Abra o convite no Safari ou Chrome atualizado do aparelho.";
  return environmentLayout ? `${base} Se não puder usar outro navegador, declare que o ambiente não pôde ser registrado.` : base;
}
