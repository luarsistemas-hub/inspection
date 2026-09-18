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
    guidance: "Confira se a foto mostra somente o imóvel. Se a análise estiver incorreta, explique o motivo e envie uma declaração de falso positivo."
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
    title: "Upload interrompido",
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
export function presentRequirementSection(value: string) { return sections[value] ?? (value.trim() || "Seção não informada"); }
export function presentRequirementLabel(value: string) { return labels[value] ?? (value || "Requisito não reconhecido"); }
export function presentComparisonMode(value: string) { return comparisons[value] ?? "Comparação não reconhecida"; }
export function presentCaptureSource(value: string) { return sources[value] ?? "Origem não reconhecida"; }
export function presentCaptureReference(value: string) { return referenceSources[value] ?? "Origem não reconhecida"; }
export function presentMediaStatus(value: string) { return mediaStatuses[value] ?? "Situação não reconhecida"; }
export function presentMediaFeedback(value: string): MediaFeedback { return mediaFeedbacks[value] ?? { tone: "info", title: "Status da imagem", message: "A situação desta imagem ainda não está disponível.", guidance: "Aguarde alguns instantes e atualize a captura se necessário." }; }
export function presentSubmissionBlock(input: { online: boolean; pending: number; blocked: number; allRequirementsSatisfied: boolean }): string {
  if (!input.online) return "Conecte-se para verificar e enviar a vistoria.";
  if (input.blocked > 0) return input.blocked === 1
    ? "1 imagem foi bloqueada pela análise automática. Volte às evidências para substituir a foto ou declarar um falso positivo."
    : `${input.blocked} imagens foram bloqueadas pela análise automática. Volte às evidências para substituir as fotos ou declarar um falso positivo.`;
  if (input.pending > 0) return `Aguarde ${input.pending} envio(s) pendente(s) antes de confirmar.`;
  if (!input.allRequirementsSatisfied) return "Ainda não há evidências válidas para todos os requisitos. Volte às evidências, envie outra foto ou registre uma justificativa de impossibilidade.";
  return "";
}
export function presentUploadFailure(error: unknown): string {
  const message = typeof error === "object" && error !== null && "message" in error ? String((error as { message?: unknown }).message ?? "") : error instanceof Error ? error.message : String(error ?? "");
  if (/media verification and screening are pending|media processing pending/i.test(message)) return "A foto foi recebida, mas a verificação ainda não terminou. Aguarde alguns segundos e toque em “Retomar envio”.";
  if (/network|fetch/i.test(message)) return "Não foi possível concluir agora por causa da conexão. Verifique sua internet e toque em “Retomar envio”.";
  return message || "Não foi possível concluir o envio. Toque em “Retomar envio” para tentar novamente.";
}
