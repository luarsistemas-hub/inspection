import type { ExternalCaptureBootstrapQuery } from "@/graphql/generated";
import { mediaCountForRequirement, type CaptureDraft } from "./drafts";
import { requirementState } from "./guided-flow";

type Requirement = ExternalCaptureBootstrapQuery["externalCapture"]["requirements"][number];
type Answer = ExternalCaptureBootstrapQuery["externalCapture"]["answers"][number];
type Environment = ExternalCaptureBootstrapQuery["externalCapture"]["environments"][number];

export const ENVIRONMENT_TYPES = [
  ["LIVING_ROOM", "Sala de estar"], ["DINING_ROOM", "Sala de jantar"], ["KITCHEN", "Cozinha"], ["BEDROOM", "Quarto"],
  ["SUITE", "Suíte"], ["BATHROOM", "Banheiro"], ["TOILET", "Lavabo"], ["LAUNDRY", "Lavanderia"],
  ["SERVICE_AREA", "Área de serviço"], ["BALCONY", "Varanda"], ["GARAGE", "Garagem"], ["HALLWAY", "Corredor"],
  ["ENTRANCE", "Entrada"], ["OFFICE", "Escritório"], ["STORAGE", "Depósito"], ["EXTERNAL_AREA", "Área externa"],
  ["POOL_AREA", "Área da piscina"], ["GOURMET_AREA", "Área gourmet"], ["OTHER", "Outro"]
] as const;

/** Impossibility reasons accepted by declareCaptureImpossibility for environment requirements. */
export const IMPOSSIBILITY_REASONS = [
  ["LOCKED", "Trancado"], ["NO_ACCESS", "Sem acesso"], ["DOES_NOT_EXIST", "Não existe"], ["UNSAFE", "Não é seguro"], ["OTHER", "Outro"]
] as const;

export function impossibilityReasonLabel(code: string): string {
  return IMPOSSIBILITY_REASONS.find(([value]) => value === code)?.[1] ?? code;
}

export function environmentProgress(requirements: Requirement[], answers: Answer[], drafts: CaptureDraft[]) {
  const completed = requirements.filter((item) => requirementState(item, answers, drafts) === "complete").length;
  return { completed, total: requirements.length, pending: requirements.filter((item) => requirementState(item, answers, drafts) !== "complete") };
}

/** Finds the next incomplete, possible environment using local and server media counts. */
export function nextEnvironmentAfterSave(requirements: Requirement[], answers: Answer[], drafts: CaptureDraft[], currentKey: string): Requirement | undefined {
  return requirements.find((item) => item.key.startsWith("env:") && item.key !== currentKey
    && !answers.find((answer) => answer.requirementKey === item.key)?.impossibilityReason
    && mediaCountForRequirement(item.key, answers, drafts) < item.minimumMedia);
}

export function validateEnvironmentName(typeCode: string, name: string): string | undefined {
  const trimmed = name.trim();
  if (typeCode === "OTHER" && !trimmed) return "Informe o nome do ambiente.";
  if ([...trimmed].some((character) => { const point = character.codePointAt(0) ?? 0; return point < 32 || point === 127; })) return "Remova os caracteres especiais do nome.";
  if ([...trimmed].length > 80) return "Use até 80 caracteres.";
  return undefined;
}

export function environmentNameForType(typeCode: string, name: string): string {
  const provided = name.trim();
  if (provided) return provided;
  return ENVIRONMENT_TYPES.find(([code]) => code === typeCode)?.[1] ?? "Ambiente";
}

export function normalizeEnvironmentName(name: string): string {
  return name.normalize("NFD").replace(/[\u0300-\u036f]/g, "").trim().replace(/\s+/g, " ").toLocaleLowerCase("pt-BR");
}

export function isDuplicateEnvironment(typeCode: string, name: string, existing: Array<{ typeCode: string | null; environmentTypeCode?: string | null; name: string; environmentName?: string | null }>): boolean {
  const normalized = normalizeEnvironmentName(name);
  return existing.some((environment) => (environment.typeCode ?? environment.environmentTypeCode) === typeCode && normalizeEnvironmentName(environment.name ?? environment.environmentName ?? "") === normalized);
}

export function addedEnvironmentLabel(environment: Pick<Environment, "addedDuringCapture">): string | undefined {
  return environment.addedDuringCapture ? "adicionado na vistoria" : undefined;
}

export function duplicateEnvironmentHint(code?: string, message?: string): string | undefined {
  const identifier = `${code ?? ""} ${message ?? ""}`.toUpperCase();
  if (identifier.includes("DUPLICATE_ENVIRONMENT")) return "Esse ambiente já existe. Diferencie o nome, por exemplo: Quarto 2.";
  if (identifier.includes("LIMIT_ENVIRONMENTS")) return "Esta vistoria já atingiu o limite de 60 ambientes.";
  if (identifier.includes("ENVIRONMENT CAPTURE IS NOT ENABLED")) return "Não é possível adicionar ambientes nesta vistoria.";
  return message ? "Não foi possível adicionar o ambiente. Confira os dados e tente novamente." : undefined;
}

export function unresolvedEnvironmentNames(requirements: Requirement[], answers: Answer[], drafts: CaptureDraft[]): string[] {
  return environmentProgress(requirements, answers, drafts).pending.map((item) => item.environmentName || item.label);
}

export function environmentListItems(requirements: Requirement[], answers: Answer[], drafts: CaptureDraft[], environments: Environment[], warnings: Record<string, string[]>): Array<{ key: string; label: string; status: string; added: string | undefined; hasWarning: boolean }> {
  return requirements.filter((item) => item.key.startsWith("env:")).map((item) => {
    const state = requirementState(item, answers, drafts);
    const environment = environments.find((entry) => entry.requirementKey === item.key);
    return { key: item.key, label: item.environmentName || environment?.name || environmentTypeLabel(item.label) || item.label || environmentTypeLabel(item.environmentTypeCode) || item.instructions || "Ambiente", status: state === "complete" ? "Concluído" : state === "pending" ? "Envio pendente" : "Pendente", added: environment ? addedEnvironmentLabel(environment) : undefined, hasWarning: Boolean(warnings[item.key]?.length) };
  });
}

export function environmentTypeLabel(typeCode: string | null | undefined): string | undefined {
  if (!typeCode) return undefined;
  return ENVIRONMENT_TYPES.find(([code]) => code === typeCode)?.[1];
}

export function captureReviewURL(handoff: { inspectionId: string; originVersionId: string | null } | null | undefined, dashboardURL: string): string | undefined {
  if (!handoff || !dashboardURL.trim()) return undefined;
  const path = handoff.originVersionId ? `/baselines/review/${handoff.originVersionId}` : `/baselines/review/inspection/${handoff.inspectionId}`;
  return `${dashboardURL.replace(/\/$/, "")}${path}`;
}

export function captureReviewPresentation(handoff: { inspectionId: string; originVersionId: string | null } | null | undefined, dashboardURL: string): { href?: string; preparing: boolean } {
  return { href: captureReviewURL(handoff, dashboardURL), preparing: Boolean(handoff && !handoff.originVersionId) };
}
