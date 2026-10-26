import type { GraphQLFailure } from "@/graphql/client";
import type * as G from "@/graphql/generated";

/** Inventory draft as returned to the review screen. */
export type InventoryDraft = NonNullable<G.DashboardInventoryDraftQuery["inventoryDraft"]>;
/** One environment of the draft under review. */
export type ReviewEnvironment = InventoryDraft["environments"][number];
/** One item of the draft under review. */
export type ReviewItem = ReviewEnvironment["items"][number];
/** One baseline version listed in the history. */
export type BaselineVersion = G.DashboardBaselineVersionsQuery["baselineVersions"]["nodes"][number];

/** newClientMutationId identifies one baseline mutation attempt. */
export const newClientMutationId = () => `baseline-${crypto.randomUUID()}`;

/** failureText returns the user-facing text of a failed operation. */
export function failureText(error: unknown) {
  return error instanceof Error ? error.message : (error as GraphQLFailure).message ?? "Não foi possível concluir a operação.";
}

/** throwUserErrors turns payload userErrors into an Error carrying the first code. */
export function throwUserErrors(errors: Array<{ code: string; message: string }>) {
  if (errors.length) throw Object.assign(new Error(errors.map((issue) => issue.message).join(" · ")), { code: errors[0].code });
}

/** isExpectedVersionConflict reports a stale expectedVersion rejection. */
export function isExpectedVersionConflict(error: unknown) {
  return typeof error === "object" && error !== null && "code" in error && (error as { code?: string }).code === "CONFLICT";
}

/** buildReviewGroups keeps the API environment and item order for review. */
export function buildReviewGroups(draft: InventoryDraft) {
  return draft.environments.map((environment) => ({ ...environment, items: environment.items.slice() }));
}

/** buildActivationSummary counts included, rejected and not-reviewed items. */
export function buildActivationSummary(items: Array<{ decision: string }>, pending: number) {
  return {
    included: items.filter((item) => ["ACCEPTED", "EDITED", "ADDED"].includes(item.decision)).length,
    rejected: items.filter((item) => item.decision === "REJECTED").length,
    notReviewed: pending,
  };
}

/** isPreparingDraft reports a draft whose analysis is still running. */
export function isPreparingDraft(draft?: { version: { draftState?: string | null } } | null) {
  return draft?.version.draftState === "PREPARING";
}

/** inventoryAvailabilityLabel describes the retention state of evidence. */
export function inventoryAvailabilityLabel(value?: string | null) {
  if (value === "EXPIRED" || value === "EXPIRED_FRAMES_PRESERVED") return "original não disponível; quadro preservado";
  if (value === "ACTIVE_BASELINE" || value === "WHILE_BASELINE_ACTIVE") return "disponível enquanto o inventário estiver ativo";
  if (value === "CONTESTATION_HOLD") return "mantido enquanto a contestação estiver aberta";
  if (value === "LEGAL_HOLD") return "mantido por retenção legal";
  if (/^\d{4}-\d{2}-\d{2}$/.test(value ?? "")) return `original disponível até ${new Intl.DateTimeFormat("pt-BR", { dateStyle: "medium", timeZone: "UTC" }).format(new Date(`${value}T00:00:00Z`))}`;
  if (value === "AVAILABLE" || value === "RETAINED") return "original disponível";
  return "evidência indisponível";
}

/** decisionLabel names an item review decision. */
export function decisionLabel(decision: string) {
  return ({ ACCEPTED: "Aceito", EDITED: "Editado", ADDED: "Adicionado", REJECTED: "Rejeitado", PENDING: "Pendente" } as Record<string, string>)[decision] ?? "Registrado";
}

/** formatEvidenceTime renders a video moment as mm:ss. */
export function formatEvidenceTime(milliseconds: number) {
  const seconds = Math.floor(milliseconds / 1000);
  return `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
}
