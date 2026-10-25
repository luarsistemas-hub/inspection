export const CONDITION_LABELS: Record<string, string> = { NEW: "Novo", GOOD: "Bom", FAIR: "Regular", POOR: "Ruim", NOT_ASSESSABLE: "Não avaliável" };
export const OBSERVATION_LABELS: Record<string, string> = { OBSERVED: "Observado", PARTIAL: "Parcialmente observado", NOT_OBSERVED: "Não observado", NOT_ASSESSABLE: "Não avaliável" };

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
