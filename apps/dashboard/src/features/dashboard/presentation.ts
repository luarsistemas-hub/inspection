const classifications: Record<string, string> = { NORMAL: "Sem alterações relevantes", ATTENTION: "Requer atenção", CRITICAL: "Crítica" };
const statuses: Record<string, string> = { PLANNED: "Planejada", INVITED: "Convite enviado", IN_PROGRESS: "Em andamento", SUBMITTED: "Enviada", ANALYZING: "Em análise", RECAPTURE_PENDING: "Complemento solicitado", COMPLETED: "Concluída", CANCELED: "Cancelada", INVALIDATED: "Invalidada", ACTIVE: "Ativa", CLOSED: "Encerrado", PENDING: "Pendente", MANUAL: "Manual", AUTOMATIC: "Automática", CONSOLIDATED: "Consolidado", HISTORICAL: "Histórico", SCHEDULED: "Agendada", MILESTONE: "Marco do projeto" };
const sources: Record<string, string> = { SCHEDULED: "Agenda", MILESTONE: "Marco do projeto", MANUAL: "Manual" };
const projectStages: Record<string, string> = { inspection: "Vistoria", "initial-inspection": "Vistoria inicial", review: "Revisão", approval: "Aprovação", delivery: "Entrega", origin: "Origem", after: "Após", planned: "Etapa planejada" };
export function presentClassification(value: string | null | undefined) { return classifications[value ?? ""] ?? "Situação não reconhecida"; }
export function presentDashboardStatus(value: string | null | undefined) { return statuses[value ?? ""] ?? "Situação não reconhecida"; }
export function presentInspectionSource(value: string | null | undefined) { return sources[value ?? ""] ?? "Origem não reconhecida"; }
export function presentReportMode(value: string | null | undefined) { return statuses[value ?? ""] ?? "Modalidade não reconhecida"; }
const coverageStatuses: Record<string, string> = { COMPLETE: "Cobertura completa", PARTIAL: "Cobertura parcial", INSUFFICIENT: "Evidência insuficiente" };
const comparisonStatuses: Record<string, string> = { CHANGED: "Mudança identificada", UNCHANGED: "Sem mudanças relevantes", INCONCLUSIVE: "Comparação inconclusiva", NOT_APPLICABLE: "Análise atual" };
const changeTypes: Record<string, string> = { CURRENT_CONDITION: "Condição atual", NEW_DAMAGE: "Dano novo", WORSENED: "Agravamento", REMOVED: "Remoção", ADDED: "Adição", REPLACED: "Substituição", MOVED: "Movimentação", IMPROVED: "Melhoria", NOT_APPLICABLE: "Evidência insuficiente" };
export function presentCoverageStatus(value: string | null | undefined) { return coverageStatuses[value ?? ""] ?? "Cobertura não informada"; }
export function presentComparisonStatus(value: string | null | undefined) { return comparisonStatuses[value ?? ""] ?? "Comparação não informada"; }
export function presentChangeType(value: string | null | undefined) { return changeTypes[value ?? ""] ?? "Alteração não reconhecida"; }
export function presentNotificationChannel(value: string | null | undefined) { return ({ EMAIL: "E-mail", SMS: "SMS", WHATSAPP: "WhatsApp" } as Record<string, string>)[value ?? ""] ?? "Canal não reconhecido"; }
export function presentProjectStage(value: string | null | undefined, key?: string | null) { return projectStages[key ?? ""] ?? projectStages[value ?? ""] ?? (value && /^[\u00C0-\u024F\s-]+$/.test(value) ? value : "Etapa da vistoria"); }
