const classifications: Record<string, string> = { NORMAL: "Sem alterações relevantes", ATTENTION: "Requer atenção", CRITICAL: "Crítica" };
const statuses: Record<string, string> = { PLANNED: "Planejada", INVITED: "Convite enviado", IN_PROGRESS: "Em andamento", SUBMITTED: "Enviada", ANALYZING: "Em análise", RECAPTURE_PENDING: "Complemento solicitado", COMPLETED: "Concluída", CANCELED: "Cancelada", INVALIDATED: "Invalidada", ACTIVE: "Ativa", CLOSED: "Encerrado", PENDING: "Pendente", MANUAL: "Manual", AUTOMATIC: "Automática", CONSOLIDATED: "Consolidado", HISTORICAL: "Histórico", SCHEDULED: "Agendada", MILESTONE: "Marco do projeto" };
const sources: Record<string, string> = { SCHEDULED: "Agenda", MILESTONE: "Marco do projeto", MANUAL: "Manual" };
const projectStages: Record<string, string> = { inspection: "Vistoria", "initial-inspection": "Vistoria inicial", review: "Revisão", approval: "Aprovação", delivery: "Entrega", origin: "Origem", after: "Após", planned: "Etapa planejada" };
export function presentClassification(value: string | null | undefined) { return classifications[value ?? ""] ?? "Situação não reconhecida"; }
export function presentDashboardStatus(value: string | null | undefined) { return statuses[value ?? ""] ?? "Situação não reconhecida"; }
export function presentInspectionSource(value: string | null | undefined) { return sources[value ?? ""] ?? "Origem não reconhecida"; }
export function presentReportMode(value: string | null | undefined) { return statuses[value ?? ""] ?? "Modalidade não reconhecida"; }
export function presentNotificationChannel(value: string | null | undefined) { return ({ EMAIL: "E-mail", SMS: "SMS", WHATSAPP: "WhatsApp" } as Record<string, string>)[value ?? ""] ?? "Canal não reconhecido"; }
export function presentProjectStage(value: string | null | undefined, key?: string | null) { return projectStages[key ?? ""] ?? projectStages[value ?? ""] ?? (value && /^[\u00C0-\u024F\s-]+$/.test(value) ? value : "Etapa da vistoria"); }
