import type * as G from "@/graphql/generated";
import { conditionLabel, counterpartyResponseLabel } from "../inventory-presentation";

/** InventoryTrail is the append-only history of one inventory version. */
export type InventoryTrail = G.DashboardInventoryTrailQuery["inventoryTrail"];
/** TrailEntry is one suggestion, decision or counterparty event. */
export type TrailEntry = InventoryTrail["decisions"][number];

const actionLabels: Record<string, string> = {
  AI_SUGGESTED: "Sugestão da IA", ACCEPT: "Item aceito", REJECT: "Item rejeitado", EDIT: "Item editado", ADD: "Item adicionado",
  MERGE: "Itens mesclados", SPLIT: "Item dividido", REMOVE_ENVIRONMENT: "Ambiente removido", ACTIVATE: "Versão ativada",
  DISCARD: "Rascunho descartado", INVITATION_CREATED: "Convite enviado à contraparte", RESPONDED: "Resposta da contraparte",
  NO_RESPONSE: "Sem resposta no prazo", RESOLVED: "Contestação resolvida",
};
const environmentActionLabels: Record<string, string> = { ADD: "Ambiente adicionado", EDIT: "Ambiente editado" };
const rejectionReasons: Record<string, string> = { NOT_EXISTS: "não existe no imóvel", NOT_RELEVANT: "não é relevante para o inventário", DUPLICATE: "item duplicado", PERSONAL_OBJECT: "objeto pessoal", OTHER: "outro motivo" };
const resolutions: Record<string, string> = { ACCEPTED: "acolhida", DECLINED: "não acolhida", OPEN: "em análise" };

/** trailActionLabel names what happened in one trail entry. */
export function trailActionLabel(entry: Pick<TrailEntry, "action" | "targetType">) {
  if (entry.targetType === "ENVIRONMENT" && environmentActionLabels[entry.action]) return environmentActionLabels[entry.action];
  return actionLabels[entry.action] ?? "Registro de auditoria";
}

/** field reads an audit snapshot field written either in camelCase or as a Go field name. */
function field(value: Record<string, unknown> | null | undefined, name: string): unknown {
  if (!value) return undefined;
  return value[name] ?? value[name.charAt(0).toUpperCase() + name.slice(1)];
}

function text(value: unknown) {
  return typeof value === "string" && value.trim() ? value : undefined;
}

/** trailDetails summarizes the target, the change and the justification of one entry. */
export function trailDetails(entry: TrailEntry): string[] {
  const details: string[] = [];
  const name = text(field(entry.after, "name")) ?? text(field(entry.before, "name"));
  if (name) details.push(`${entry.targetType === "ENVIRONMENT" ? "Ambiente" : "Item"}: ${name}`);
  const before = text(field(entry.before, "condition"));
  const after = text(field(entry.after, "condition"));
  if (after && before && before !== after) details.push(`Condição: ${conditionLabel(before)} → ${conditionLabel(after)}`);
  else if (after && entry.action === "AI_SUGGESTED") details.push(`Condição sugerida: ${conditionLabel(after)}`);
  const evidence = field(entry.after, "evidenceIds");
  if (Array.isArray(evidence)) details.push(`${evidence.length} evidência(s) citada(s)`);
  const response = text(field(entry.after, "response"));
  if (response) details.push(`Resposta: ${counterpartyResponseLabel(response)}`);
  const resolution = text(field(entry.after, "resolution"));
  if (resolution) details.push(`Contestação ${resolutions[resolution] ?? "resolvida"}`);
  const deadline = text(field(entry.after, "deadlineAt"));
  if (deadline) details.push(`Prazo: ${formatTrailTime(deadline)}`);
  const rejection = text(field(entry.after, "rejectionReason"));
  if (entry.action === "REJECT" && rejection) details.push(`Motivo da rejeição: ${rejectionReasons[rejection] ?? rejection}`);
  if (entry.reason) details.push(`Justificativa: ${entry.reason}`);
  if (entry.statement) details.push(`Manifestação: ${entry.statement}`);
  if (entry.resolutionReason) details.push(`Motivo da resolução: ${entry.resolutionReason}`);
  return details;
}

/** trailAuthor names the member who acted, or says the entry was automatic. */
export function trailAuthor(entry: Pick<TrailEntry, "actorMembershipId" | "action" | "targetType">, names: Map<string, string>) {
  if (entry.actorMembershipId) return names.get(entry.actorMembershipId) ?? "Membro da equipe";
  if (entry.action === "AI_SUGGESTED") return "Análise automática";
  return entry.targetType === "COUNTERPARTY" ? "Contraparte" : "Sistema";
}

/** sortTrail orders entries chronologically without changing the API arrays. */
export function sortTrail(entries: TrailEntry[]) {
  return entries.slice().sort((left, right) => left.occurredAt.localeCompare(right.occurredAt));
}

/** formatTrailTime renders an audit instant in Brazilian Portuguese. */
export function formatTrailTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(date);
}
