import { CAPTURE_LOCATION_TIMEOUT_MS, MAX_GPS_ACCURACY_METERS, type CaptureLocationFailure } from "@/pwa/capture-location";
import { environmentTypeLabel } from "@/pwa/environment-flow";

const statuses: Record<string, string> = { OPEN: "Aberta", ACCEPTANCE_REQUIRED: "Aguardando aceite", CONFIRMED: "Confirmada", PENDING: "Pendente", IN_PROGRESS: "Em andamento", SUBMITTED: "Enviada", EXPIRED: "Expirada", REVOKED: "Revogada", INVALIDATED: "Invalidada" };
const sections: Record<string, string> = { property: "Imóvel", agency: "Imobiliária", participant: "Responsável pela vistoria" };
const labels: Record<string, string> = { "Property overview": "Visão geral do imóvel", Overview: "Visão geral do imóvel", property: "Imóvel" };
const comparisons: Record<string, string> = { CHECKLIST_ONLY: "Primeira vistoria do imóvel", FIXED_ORIGIN: "Comparar com fotos de referência" };
const sources: Record<string, string> = { CAMERA_ONLY: "Câmera obrigatória", CAMERA_DEFAULT: "Câmera ou galeria conforme a política" };
const referenceSources: Record<string, string> = { MANUAL: "Manual", SCHEDULED: "Agenda", MILESTONE: "Marco do projeto", ONBOARDING: "Cadastro inicial", FIXED_ORIGIN: "Fotos de referência" };
const mediaStatuses: Record<string, string> = { SCREENED: "Bloqueada para revisão", REJECTED: "Recusada", PURGED: "Removida", ABORTED: "Interrompida", READY: "Pronta", PROCESSING: "Em processamento" };
export type MediaFeedback = { tone: "info" | "success" | "warning"; title: string; message: string; guidance: string };
const mediaFeedbacks: Record<string, MediaFeedback> = {
  SCREENED: {
    tone: "warning",
    title: "Imagem bloqueada para revisão",
    message: "A análise automática encontrou um conteúdo que precisa ser revisado para este requisito.",
    guidance: "A foto não poderá ser enviada nesta vistoria. Tire outra foto ou aguarde a revisão do gestor."
  },
  REJECTED: {
    tone: "warning",
    title: "Imagem recusada",
    message: "Não foi possível validar esta imagem para a vistoria.",
    guidance: "Escolha outra foto, com boa iluminação e sem elementos que impeçam a análise."
  },
  PURGED: {
    tone: "warning",
    title: "Imagem removida",
    message: "Esta mídia não está mais disponível para a vistoria.",
    guidance: "Envie uma nova foto para continuar."
  },
  ABORTED: {
    tone: "warning",
    title: "Envio interrompido",
    message: "O envio desta imagem foi interrompido antes da conclusão.",
    guidance: "Verifique sua conexão e use “Retomar envio” para tentar novamente."
  },
  READY: {
    tone: "success",
    title: "Imagem pronta",
    message: "A imagem foi enviada e verificada pelo servidor.",
    guidance: "Ela já pode ser usada nesta vistoria."
  },
  PROCESSING: {
    tone: "info",
    title: "Verificando imagem",
    message: "A imagem foi recebida e está sendo analisada.",
    guidance: "Você poderá continuar quando a verificação terminar."
  }
};
export function presentCaptureStatus(value: string) { return statuses[value] ?? "Situação não reconhecida"; }
export function presentRequirementSection(value: string) { return sections[value] ?? environmentTypeLabel(value) ?? (value.trim() || "Seção não informada"); }
export function presentRequirementLabel(value: string) { return labels[value] ?? environmentTypeLabel(value) ?? (value || "Requisito não reconhecido"); }
export function presentRequirementOption(section: string, label: string) {
  const presentedSection = presentRequirementSection(section);
  const presentedLabel = presentRequirementLabel(label);
  return presentedSection === presentedLabel ? presentedLabel : `${presentedSection}: ${presentedLabel}`;
}
export function presentComparisonMode(value: string) { return comparisons[value] ?? "Comparação não reconhecida"; }
export function presentCaptureSource(value: string) { return sources[value] ?? "Origem não reconhecida"; }
export function presentCaptureReference(value: string) { return referenceSources[value] ?? "Origem não reconhecida"; }
export function presentMediaStatus(value: string) { return mediaStatuses[value] ?? "Situação não reconhecida"; }
export function presentMediaFeedback(value: string): MediaFeedback { return mediaFeedbacks[value] ?? { tone: "info", title: "Status da imagem", message: "A situação desta imagem ainda não está disponível.", guidance: "Aguarde alguns instantes e atualize a captura se necessário." }; }
export function presentSubmissionBlock(input: { online: boolean; pending: number; blocked: number; allRequirementsSatisfied: boolean }): string {
  if (!input.online) return "Conecte-se para verificar e enviar a vistoria.";
  if (input.blocked > 0) return input.blocked === 1
    ? "1 imagem foi bloqueada pela análise automática. Tire outra foto ou aguarde a revisão do gestor."
    : `${input.blocked} imagens foram bloqueadas pela análise automática. Tire outras fotos ou aguarde a revisão do gestor.`;
  if (input.pending > 0) return `Aguarde ${input.pending} envio(s) pendente(s) antes de confirmar.`;
  if (!input.allRequirementsSatisfied) return "Ainda não há evidências válidas para todos os requisitos. Volte às evidências, envie outra foto ou registre uma justificativa de impossibilidade.";
  return "";
}
export function presentUploadFailure(error: unknown): string {
  const message = typeof error === "object" && error !== null && "message" in error ? String((error as { message?: unknown }).message ?? "") : error instanceof Error ? error.message : String(error ?? "");
  const code = typeof error === "object" && error !== null && "code" in error ? String((error as { code?: unknown }).code ?? "") : "";
  if (/media verification and screening are pending|media processing pending/i.test(message)) return "A foto foi recebida, mas a verificação ainda não terminou. Aguarde alguns segundos e toque em “Retomar envio”.";
  if (/environment requirement is required/i.test(message)) return "O ambiente escolhido não está disponível para esta foto. Atualize a captura e tente novamente.";
  if (/required GPS must be accurate within the guided window/i.test(message)) return "O servidor recusou a localização desta foto por falta de precisão ou por estar fora do prazo de captura. Substitua a foto e confirme novamente com a localização atual.";
  if (/network|fetch/i.test(message)) return "Não foi possível concluir agora por causa da conexão. Verifique sua internet e toque em “Retomar envio”.";
  if (code === "UNAUTHENTICATED" || code === "SESSION_EXPIRED") return "Sua sessão expirou. Solicite um novo código para retomar o envio.";
  return presentCaptureError(error, "Não foi possível enviar a foto. Ela continua salva neste dispositivo. Toque em “Retomar envio” para tentar novamente.");
}

export function presentCaptureError(error: unknown, fallback: string): string {
  const message = typeof error === "object" && error !== null && "message" in error
    ? String((error as { message?: unknown }).message ?? "")
    : error instanceof Error ? error.message : "";
  const code = typeof error === "object" && error !== null && "code" in error ? String((error as { code?: unknown }).code ?? "") : "";
  if (code === "UNAUTHENTICATED" || code === "SESSION_EXPIRED") return "Sua sessão expirou. Solicite um novo código para continuar.";
  if (message === "Image decoding is unavailable") return "Este aparelho não conseguiu abrir a imagem. Escolha outra foto em um formato permitido.";
  if (message === "Invalid image dimensions") return "A imagem está vazia ou danificada. Escolha outra foto.";
  if (message === "Canvas is unavailable") return "Este navegador não conseguiu preparar a imagem. Use outro navegador ou tire uma nova foto.";
  if (/^(Use |Abra |Confira |Aguarde |Conecte-se|Grave novamente|Falha ao verificar vídeo|Não foi possível|A câmera |O dispositivo |Este vídeo |O vídeo |Este convite |A gravação |O arquivo |A foto |A API |A localização |Esta vistoria |Este requisito |O ambiente escolhido )/i.test(message)) return message;
  return fallback;
}

export function presentCaptureLocationFailure(failure: CaptureLocationFailure): string {
  const retry = "A foto continua na prévia. Tente confirmá-la novamente.";
  if (failure === "insecure") return "A localização exige uma conexão segura. Abra o link HTTPS do convite e confirme a foto novamente.";
  if (failure === "unsupported") return "Este navegador não oferece acesso à localização. Abra o convite em outro navegador ou dispositivo.";
  if (failure === "denied") return `O acesso à localização foi negado. Confira a permissão deste site e os Serviços de Localização do dispositivo. ${retry}`;
  if (failure === "timeout") return `A localização demorou mais de ${CAPTURE_LOCATION_TIMEOUT_MS / 1000} segundos. Confira o sinal de localização e a conexão Wi-Fi ou móvel. ${retry}`;
  return `O dispositivo não conseguiu determinar sua localização. Confira os Serviços de Localização e a conexão Wi-Fi ou móvel. ${retry}`;
}

export function presentCaptureLocationAccuracy(accuracyMeters: number): string {
  return `A localização foi encontrada, mas a precisão é de aproximadamente ${Math.ceil(accuracyMeters)} m. Este requisito exige até ${MAX_GPS_ACCURACY_METERS} m. Ative a localização precisa, aguarde o sinal melhorar e confirme a foto novamente; ela continua na prévia.`;
}

export function presentDraftPersistenceFailure(error: unknown): string {
  const name = typeof error === "object" && error !== null && "name" in error ? String((error as { name?: unknown }).name ?? "") : "";
  if (name === "QuotaExceededError") return "Não foi possível salvar a foto neste dispositivo. A prévia ainda não foi salva; libere espaço e tente novamente.";
  return "Não foi possível salvar a foto neste dispositivo. A prévia ainda não foi salva; tente novamente.";
}

export function presentUploadProgress(progress: { phase: string; percent: number; completedParts: number; totalParts: number }): string {
  if (progress.phase === "preparing") return "Preparando envio";
  if (progress.phase === "uploading") return progress.totalParts > 0 ? `Enviando partes (${progress.completedParts}/${progress.totalParts})` : "Enviando foto";
  if (progress.phase === "verifying") return "Aguardando verificação da foto";
  if (progress.phase === "saving") return "Salvando metadados";
  if (progress.phase === "complete") return "Envio concluído";
  return "Não foi possível concluir o envio. Toque em “Retomar envio” para tentar novamente.";
}

export function presentReplacementNotice(deadlineAt?: string | null): string {
  const base = "Precisamos de uma nova gravação. O vídeo anterior não pôde ser analisado.";
  const deadline = deadlineAt ? new Date(deadlineAt) : undefined;
  if (!deadline || Number.isNaN(deadline.getTime())) return base;
  return `${base} Envie até ${new Intl.DateTimeFormat("pt-BR", { dateStyle: "long", timeStyle: "short", timeZone: "America/Sao_Paulo" }).format(deadline)}.`;
}
