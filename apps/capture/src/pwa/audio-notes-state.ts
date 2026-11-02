import type { CaptureAudioNoteQuery } from "@/graphql/generated";
import type { AudioDraft } from "./audio-drafts";

/** Version of the separate voice disclosure; never reused for the visual consent. */
export const voiceConsentVersion = "voice-v1";
/** Longest correction text accepted by the API. */
export const maximumNoteTextLength = 12_000;

export type ServerNote = NonNullable<CaptureAudioNoteQuery["audioNote"]>;
export type NoteItem = { draft: AudioDraft; note?: ServerNote };
export type NoteTone = "info" | "success" | "warning" | "danger";

/** A voice failure whose message is already safe Brazilian Portuguese for the interface. */
export class VoiceFlowError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = "VoiceFlowError";
    this.code = code;
  }
}

/** Converts a getUserMedia failure into a stable, safe explanation that never blocks visual capture. */
export function microphoneFailure(error: unknown): VoiceFlowError {
  const name = error instanceof DOMException || (typeof error === "object" && error !== null && "name" in error) ? String((error as { name: unknown }).name) : "";
  if (name === "NotAllowedError" || name === "SecurityError" || name === "PermissionDeniedError") return new VoiceFlowError("MICROPHONE_DENIED", "O acesso ao microfone foi negado. Libere o microfone nas configurações do navegador para gravar anotações. Fotos e vídeos continuam disponíveis.");
  if (name === "NotFoundError" || name === "OverconstrainedError") return new VoiceFlowError("MICROPHONE_MISSING", "Nenhum microfone está disponível neste dispositivo. Fotos e vídeos continuam disponíveis.");
  if (name === "NotReadableError" || name === "AbortError") return new VoiceFlowError("MICROPHONE_BUSY", "O microfone está ocupado ou indisponível. Feche outros aplicativos e tente novamente.");
  return new VoiceFlowError("MICROPHONE_FAILED", "Não foi possível abrir o microfone. Tente novamente; fotos e vídeos continuam disponíveis.");
}

const failureMessages: Record<string, string> = {
  LIMIT_REACHED: "O limite de uso de voz foi atingido. Esta anotação não foi enviada; fotos e vídeos continuam disponíveis.",
  VOICE_NOT_ENABLED: "A anotação de voz não está habilitada para esta vistoria. Fotos e vídeos continuam disponíveis.",
  CONSENT_REQUIRED: "A autorização de voz não está ativa neste convite. Ative as anotações de voz para enviar esta anotação.",
  INVALID_AUDIO: "Não foi possível validar o áudio enviado. Grave uma nova anotação.",
  NOT_FOUND: "A foto ou o vídeo desta anotação não está mais disponível nesta captura.",
  INVALID_STATE: "Esta anotação não pode mais ser alterada: a captura foi encerrada ou a gravação expirou.",
  CONFLICT: "A anotação foi alterada em outro lugar. Os dados mais recentes foram carregados; confira e tente novamente.",
  INVALID_INPUT: "Confira os dados da anotação e tente novamente.",
  UNAUTHENTICATED: "Sua sessão de captura expirou. Solicite um novo acesso; a anotação continua salva neste dispositivo.",
  SESSION_EXPIRED: "Sua sessão de captura expirou. Solicite um novo acesso; a anotação continua salva neste dispositivo.",
  FORBIDDEN: "Você não tem permissão para alterar esta anotação.",
  RETRY_UNAVAILABLE: "Não é possível tentar a transcrição novamente. Corrija o texto ou grave uma nova anotação.",
  EXPIRED: "O áudio expirou e não pode mais ser processado.",
  NETWORK_ERROR: "Sem conexão com o servidor. A anotação continua salva neste dispositivo; tente novamente quando a conexão voltar.",
  RATE_LIMITED: "Muitas tentativas em pouco tempo. Aguarde um instante e tente novamente."
};

const genericFailure = "Não foi possível concluir a operação com a anotação. Tente novamente.";

/** Returns the stable code carried by a mutation, transport or voice-flow failure. */
export function voiceErrorCode(error: unknown): string | undefined {
  return typeof error === "object" && error !== null && "code" in error && typeof (error as { code: unknown }).code === "string" ? (error as { code: string }).code : undefined;
}

/** Maps any failure to safe Portuguese text; library, database and provider messages never reach the interface. */
export function presentVoiceError(error: unknown, fallback = genericFailure): string {
  if (error instanceof VoiceFlowError) return error.message;
  const code = voiceErrorCode(error);
  return (code && failureMessages[code]) || fallback;
}

/** A rejection the participant cannot fix by retrying; the note is kept only so it can be discarded explicitly. */
export const terminalVoiceCodes: ReadonlySet<string> = new Set(["LIMIT_REACHED", "VOICE_NOT_ENABLED", "CONSENT_REQUIRED", "INVALID_AUDIO", "NOT_FOUND", "INVALID_STATE", "CONFLICT", "INVALID_INPUT", "EXPIRED"]);
export const sessionVoiceCodes: ReadonlySet<string> = new Set(["UNAUTHENTICATED", "SESSION_EXPIRED"]);

/** Message shown right after a finalized note, by what ended it. */
export function savedNoteMessage(cause: string, limitKind: "duration" | "size" = "duration"): string {
  switch (cause) {
    case "limit": return limitKind === "size" ? "O limite de 5 MB foi atingido. A anotação válida foi salva; você pode iniciar outra." : "O limite de 60 segundos foi atingido. A anotação válida foi salva; você pode iniciar outra.";
    case "device": return "O microfone foi interrompido. A parte gravada foi salva neste dispositivo.";
    case "focus": return "A gravação foi encerrada porque a página perdeu o foco. A anotação foi salva neste dispositivo.";
    case "background": return "A gravação foi encerrada porque a página saiu de primeiro plano. A anotação foi salva neste dispositivo.";
    case "video-end": return "A anotação terminou junto com o vídeo e foi salva neste dispositivo.";
    default: return "Anotação salva neste dispositivo; o envio ainda está pendente.";
  }
}

/** True while a photo note still waits for its photo to have a server media identity. */
export function waitsForPhoto(draft: AudioDraft): boolean {
  return draft.target.kind === "PHOTO" && !draft.target.mediaId && (draft.state === "PENDING_UPLOAD" || draft.state === "FAILED");
}

const serverStatusLabels: Record<string, [string, NoteTone]> = {
  UPLOADING: ["Envio em andamento", "info"],
  QUEUED: ["Na fila de transcrição", "info"],
  PROCESSING: ["Transcrevendo", "info"],
  RETRY_WAIT: ["Nova tentativa agendada", "warning"],
  FAILED: ["Transcrição não concluída", "danger"],
  EXPIRED: ["Anotação expirada", "warning"],
  CANCELED: ["Anotação descartada", "info"]
};

/** One honest status per note: local state until the server confirms receipt, then the server's processing state. */
export function describeNote(item: NoteItem, online: boolean): { label: string; tone: NoteTone; settled: boolean } {
  const { draft, note } = item;
  if (note && draft.state === "RECEIVED") {
    if (note.status === "COMPLETED") {
      if (note.noSpeech) return { label: "Nenhuma fala foi identificada; nada será sugerido por esta anotação", tone: "info", settled: true };
      return note.textConfirmed ? { label: "Texto confirmado", tone: "success", settled: true } : { label: "Texto pronto para revisão", tone: "success", settled: true };
    }
    const mapped = serverStatusLabels[note.status];
    if (mapped) return { label: mapped[0], tone: mapped[1], settled: ["FAILED", "EXPIRED", "CANCELED"].includes(note.status) };
    return { label: "Áudio recebido para processamento", tone: "info", settled: false };
  }
  switch (draft.state) {
    case "RECEIVED": return { label: "Áudio recebido. O status será atualizado quando houver conexão", tone: "info", settled: false };
    case "UNSENDABLE": return { label: draft.statusMessage || "Esta anotação não pode ser enviada. Descarte-a e grave novamente", tone: "danger", settled: true };
    case "FAILED": return { label: draft.statusMessage || "Falha no envio; áudio mantido neste dispositivo", tone: "warning", settled: false };
    default:
      if (waitsForPhoto(draft)) return { label: "Aguardando o envio da foto; o áudio fica neste dispositivo", tone: "info", settled: false };
      return { label: online ? "Envio pendente" : "Salva neste dispositivo; envio pendente até a conexão voltar", tone: "warning", settled: false };
  }
}

/** Short anchor label; video notes show an approximate capture interval, photo notes never show a timestamp. */
export function describeAnchor(draft: AudioDraft): string {
  if (draft.target.kind === "PHOTO") return "Foto";
  return `Vídeo · ${Math.floor(draft.target.startMs / 1000)}–${Math.ceil(draft.target.endMs / 1000)} s (aproximado)`;
}

/** Counts used by the review step so pending or unsendable notes are named, never hidden behind a spinner. */
export function summarizeNotes(items: NoteItem[]): { total: number; received: number; pending: number; failed: number; unsendable: number } {
  const summary = { total: items.length, received: 0, pending: 0, failed: 0, unsendable: 0 };
  for (const item of items) {
    const { draft } = item;
    if (draft.state === "RECEIVED") summary.received += 1;
    else if (draft.state === "UNSENDABLE") summary.unsendable += 1;
    else if (draft.state === "FAILED") summary.failed += 1;
    else summary.pending += 1;
  }
  return summary;
}

/** Why the voice control cannot start, or undefined when it can. */
export function voiceBlockReason(input: { enabled: boolean; hasTarget: boolean; kind: "PHOTO" | "VIDEO"; limitReached: boolean }): string | undefined {
  if (!input.enabled) return "A anotação de voz não foi autorizada para esta captura. Ative as anotações de voz para gravar.";
  if (input.limitReached) return "O limite de anotações de voz foi atingido. Fotos e vídeos continuam disponíveis.";
  if (!input.hasTarget) return input.kind === "PHOTO" ? "Selecione ou tire uma foto antes de gravar uma anotação." : "Inicie a gravação do vídeo para anotar por voz.";
  return undefined;
}

/** Small deterministic digest so an edited text never reuses another edit's mutation identity. */
export function textDigest(text: string): string {
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) { hash ^= text.charCodeAt(index); hash = Math.imul(hash, 16777619); }
  return (hash >>> 0).toString(36);
}
