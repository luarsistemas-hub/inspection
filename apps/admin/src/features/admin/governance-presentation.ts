import type * as G from "@/graphql/generated";
import { presentPublicationMode } from "./presentation";

export type AdminRow = Record<string, string | number | null>;

function makeRow(values: Record<string, string | number | boolean | null | undefined>): AdminRow {
  return Object.fromEntries(Object.entries(values).map(([key, value]) => [key, typeof value === "boolean" ? (value ? "Sim" : "Não") : value ?? "—"]));
}

/** Keeps governance policy, retention, and delivery availability independent. */
export function buildGovernanceRows(
  policy: PromiseSettledResult<G.AdminPublicationPolicyQuery>,
  retention: PromiseSettledResult<G.AdminRetentionPoliciesQuery>,
  deliveries: PromiseSettledResult<G.AdminNotificationDeliveriesQuery>,
): { rows: AdminRow[]; hasNextPage: boolean; endCursor: string | null } {
  const rows: AdminRow[] = [];
  if (policy.status === "fulfilled" && policy.value.publicationPolicy && Number.isSafeInteger(policy.value.publicationPolicy.version) && policy.value.publicationPolicy.version > 0) rows.push(makeRow({ Recurso: "Política de publicação", Situação: presentPublicationMode(policy.value.publicationPolicy.mode), Versão: policy.value.publicationPolicy.version }));
  else rows.push(makeRow({ Recurso: "Política de publicação", Situação: "Indisponível", Detalhe: "Não foi possível carregar a política de publicação." }));

  if (retention.status === "fulfilled") {
    rows.push(...(retention.value.retentionPolicies.nodes.length ? retention.value.retentionPolicies.nodes.map((item) => makeRow({ Recurso: "Retenção", ID: item.id, Situação: "Configurada", Evidências: `${item.evidenceDays} dias`, Operacional: `${item.operationalDays} dias`, Segurança: `${item.securityDays} dias`, Versão: item.version })) : [makeRow({ Recurso: "Retenção", Situação: "Nenhuma regra configurada" })]));
  } else rows.push(makeRow({ Recurso: "Retenção", Situação: "Indisponível", Detalhe: "Não foi possível carregar as regras de retenção." }));

  if (deliveries.status === "fulfilled") {
    rows.push(...(deliveries.value.notificationDeliveries.nodes.length ? deliveries.value.notificationDeliveries.nodes.map((item) => makeRow({ Recurso: "Entrega", ID: item.id, Vistoria: item.inspectionId, Destinatário: item.recipientMasked, Situação: item.status, Código: item.failureCode, inspectionId: item.inspectionId, responsibilityVersion: item.responsibilityVersion, canCorrectResponsibleEmail: item.canCorrectResponsibleEmail, Atualizado: item.updatedAt })) : [makeRow({ Recurso: "Entrega", Situação: "Nenhuma entrega encontrada" })]));
  } else rows.push(makeRow({ Recurso: "Entrega", Situação: "Indisponível", Detalhe: "Não foi possível carregar as entregas." }));

  const pageInfo = deliveries.status === "fulfilled" ? deliveries.value.notificationDeliveries.pageInfo : undefined;
  return { rows, hasNextPage: pageInfo?.hasNextPage ?? false, endCursor: pageInfo?.endCursor ?? null };
}
