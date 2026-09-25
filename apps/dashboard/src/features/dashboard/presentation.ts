const classifications: Record<string, string> = { NORMAL: "Sem alertas identificados", ATTENTION: "Requer atenção", CRITICAL: "Crítica" };
const statuses: Record<string, string> = { PLANNED: "Planejada", INVITED: "Convite enviado", IN_PROGRESS: "Em andamento", SUBMITTED: "Enviada", ANALYZING: "Em análise", RECAPTURE_PENDING: "Complemento solicitado", COMPLETED: "Concluída", CANCELED: "Cancelada", INVALIDATED: "Invalidada", ACTIVE: "Ativa", CLOSED: "Encerrado", PENDING: "Pendente", MANUAL: "Manual", AUTOMATIC: "Automática", CONSOLIDATED: "Consolidado", HISTORICAL: "Histórico", SCHEDULED: "Agendada", MILESTONE: "Marco do projeto" };
const sources: Record<string, string> = { SCHEDULED: "Agenda", MILESTONE: "Marco do projeto", MANUAL: "Manual" };
const projectStages: Record<string, string> = { inspection: "Vistoria", "initial-inspection": "Vistoria inicial", review: "Revisão", approval: "Aprovação", delivery: "Entrega", origin: "Origem", after: "Após", planned: "Etapa planejada" };
export function presentClassification(value: string | null | undefined) { return classifications[value ?? ""] ?? "Situação não reconhecida"; }
export function presentDashboardStatus(value: string | null | undefined) { return statuses[value ?? ""] ?? "Situação não reconhecida"; }
export function presentInspectionSource(value: string | null | undefined) { return sources[value ?? ""] ?? "Origem não reconhecida"; }
export function presentReportMode(value: string | null | undefined) { return statuses[value ?? ""] ?? "Modalidade não reconhecida"; }
const analysisModes: Record<string, string> = { CURRENT_ONLY: "Análise atual", COMPARE_ORIGIN_CURRENT: "Comparação com referência" };
const analysisStatuses: Record<string, string> = { PENDING: "Pendente", COMPLETED: "Concluída", INCONCLUSIVE: "Inconclusiva", FAILED: "Falha técnica" };
const findingCategories: Record<string, string> = { CONSERVATION: "Conservação", INVENTORY: "Inventário", CLEANLINESS: "Limpeza", OBSTRUCTION: "Obstrução", EVIDENCE_QUALITY: "Qualidade da evidência" };
export function presentAnalysisMode(value: string | null | undefined) { return analysisModes[value ?? ""] ?? "Modo não informado"; }
export function presentAnalysisStatus(value: string | null | undefined) { return analysisStatuses[value ?? ""] ?? "Situação não informada"; }
export function presentFindingCategory(value: string | null | undefined) { return findingCategories[value ?? ""] ?? "Categoria não reconhecida"; }
export function presentNoRelevantChange(value: boolean | null | undefined) { return value === null || value === undefined ? "Comparação inconclusiva" : value ? "Sem alteração relevante identificada" : "Alteração relevante identificada"; }
export function presentNotificationChannel(value: string | null | undefined) { return ({ EMAIL: "E-mail", SMS: "SMS", WHATSAPP: "WhatsApp" } as Record<string, string>)[value ?? ""] ?? "Canal não reconhecido"; }
export function presentProjectStage(value: string | null | undefined, key?: string | null) { return projectStages[key ?? ""] ?? projectStages[value ?? ""] ?? (value && /^[\u00C0-\u024F\s-]+$/.test(value) ? value : "Etapa da vistoria"); }
