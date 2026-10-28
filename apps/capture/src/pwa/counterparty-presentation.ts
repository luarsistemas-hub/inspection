export const CONDITION_LABELS: Record<string, string> = { NEW: "Novo", GOOD: "Bom", FAIR: "Regular", POOR: "Ruim", NOT_ASSESSABLE: "Não avaliável" };
export const OBSERVATION_LABELS: Record<string, string> = { OBSERVED: "Observado", PARTIAL: "Parcialmente observado", NOT_OBSERVED: "Não observado", NOT_ASSESSABLE: "Não avaliável" };

export const PURPOSE_LABELS: Record<string, string> = { MOVE_IN: "Entrada de locação", CONSTRUCTION_HANDOVER: "Entrega de obra", POST_RENOVATION: "Pós-reforma", INVENTORY_UPDATE: "Atualização do inventário", CONTESTATION_ADJUSTMENT: "Ajuste de contestação" };

export type CounterpartyOutcome = { tone: "info" | "success" | "warning"; message: string };

/**
 * counterpartyOutcome tells the participant what happened to their response:
 * acknowledgment, contestation under review, declined, or accepted with or
 * without an adjusted version. Pending responses have no outcome yet.
 */
export function counterpartyOutcome(state: { response?: string | null; resolution?: string | null; adjustmentStatus?: string | null }): CounterpartyOutcome | null {
  if (state.response === "ACKNOWLEDGED") return { tone: "success", message: "Resposta registrada: você confirmou ciência desta vistoria." };
  if (state.response !== "CONTESTED") return null;
  const resolution = state.resolution;
  if (resolution === "DECLINED") return { tone: "warning", message: "Sua manifestação foi analisada e não foi acolhida. A vistoria de referência foi mantida." };
  if (resolution !== "ACCEPTED") return { tone: "info", message: "Sua manifestação foi enviada e aguarda análise da equipe responsável." };
  if (state.adjustmentStatus === "DRAFT") return { tone: "success", message: "Sua manifestação foi acolhida. Uma versão ajustada da vistoria está em preparação." };
  if (state.adjustmentStatus === "ACTIVE" || state.adjustmentStatus === "SUPERSEDED") return { tone: "success", message: "Sua manifestação foi acolhida e uma versão ajustada da vistoria foi publicada." };
  return { tone: "success", message: "Sua manifestação foi acolhida, sem nova versão da vistoria." };
}

/** evidenceCaption labels one evidence of an item for the participant. */
export function evidenceCaption(evidence: { kind: string; timestampMs?: number | null }, index: number): string {
  if (evidence.kind !== "VIDEO_FRAME") return `Foto ${index + 1}`;
  if (evidence.timestampMs == null) return "Quadro do vídeo";
  const seconds = Math.floor(evidence.timestampMs / 1000);
  return `Quadro do vídeo · ${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

export function counterpartyErrorMessage(error: unknown): string {
  const code = typeof error === "object" && error !== null && "code" in error ? String(error.code) : "";
  if (code === "RATE_LIMITED") return "Muitas tentativas. Aguarde alguns minutos antes de tentar novamente.";
  if (code === "INVALID_OTP") return "O código está incorreto ou expirou. Confira a mensagem recebida.";
  if (code === "LOCKED") return "O acesso foi temporariamente bloqueado. Aguarde antes de tentar novamente.";
  if (code === "NOT_FOUND" || code === "UNKNOWN_TOKEN") return "Este convite não está disponível. Confira o link recebido.";
  if (code === "DEADLINE_PASSED") return "O prazo de resposta terminou. Atualize a página para consultar a vistoria.";
  if (code === "INVALID_STATE") return "A vistoria mudou desde a última consulta. Atualizamos as informações disponíveis.";
  return "Não foi possível concluir esta ação. Atualize a página e tente novamente.";
}

export function contestStatementState(statement: string): { count: number; valid: boolean; overLimit: boolean } {
  return { count: [...statement].length, valid: statement.trim().length > 0 && [...statement].length <= 4000, overLimit: [...statement].length > 4000 };
}

export function counterpartyCanRespond(readOnly: boolean, response?: string | null): boolean {
  return !readOnly && (!response || response === "PENDING");
}
