const statuses: Record<string, string> = { OPEN: "Aberta", ACCEPTANCE_REQUIRED: "Aguardando aceite", CONFIRMED: "Confirmada", PENDING: "Pendente", IN_PROGRESS: "Em andamento", SUBMITTED: "Enviada", EXPIRED: "Expirada", REVOKED: "Revogada", INVALIDATED: "Invalidada" };
const sections: Record<string, string> = { property: "Imóvel", agency: "Imobiliária", participant: "Responsável pela vistoria" };
const labels: Record<string, string> = { "Property overview": "Visão geral do imóvel", Overview: "Visão geral do imóvel", property: "Imóvel" };
const comparisons: Record<string, string> = { CHECKLIST_ONLY: "Primeira vistoria do imóvel", FIXED_ORIGIN: "Comparar com fotos de referência" };
const sources: Record<string, string> = { CAMERA_ONLY: "Câmera obrigatória", CAMERA_DEFAULT: "Câmera ou galeria conforme a política" };
const referenceSources: Record<string, string> = { MANUAL: "Manual", SCHEDULED: "Agenda", MILESTONE: "Marco do projeto", ONBOARDING: "Cadastro inicial", FIXED_ORIGIN: "Fotos de referência" };
const mediaStatuses: Record<string, string> = { SCREENED: "Em análise", REJECTED: "Recusada", PURGED: "Removida", ABORTED: "Interrompida", READY: "Pronta", PROCESSING: "Em processamento" };
export function presentCaptureStatus(value: string) { return statuses[value] ?? "Situação não reconhecida"; }
export function presentRequirementSection(value: string) { return sections[value] ?? "Seção não reconhecida"; }
export function presentRequirementLabel(value: string) { return labels[value] ?? (value || "Requisito não reconhecido"); }
export function presentComparisonMode(value: string) { return comparisons[value] ?? "Comparação não reconhecida"; }
export function presentCaptureSource(value: string) { return sources[value] ?? "Origem não reconhecida"; }
export function presentCaptureReference(value: string) { return referenceSources[value] ?? "Origem não reconhecida"; }
export function presentMediaStatus(value: string) { return mediaStatuses[value] ?? "Situação não reconhecida"; }
