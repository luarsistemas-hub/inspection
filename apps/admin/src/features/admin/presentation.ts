const roles: Record<string, string> = {
  TENANT_ADMIN: "Administrador da imobiliária", MANAGER: "Gestor", EMPLOYEE: "Operador", VIEWER: "Visualizador",
  CUSTOMER_VIEWER: "Visualizador cliente", ORGANIZATION_ADMIN: "Administrador da organização", ACCESS_ADMIN: "Administrador de acessos",
  PARTICIPATION_ADMIN: "Administrador de participantes", INSPECTION_CONFIG_ADMIN: "Administrador de vistorias", GOVERNANCE_ADMIN: "Administrador de governança", AUDITOR: "Auditor",
};
const statuses: Record<string, string> = { ACTIVE: "Ativa", INACTIVE: "Inativa", SUSPENDED: "Suspensa", PENDING: "Pendente", PENDING_ACTIVATION: "Aguardando ativação", CONFIGURED: "Configurada", READY: "Pronta", FAILED: "Falhou", CANCELED: "Cancelada", Ativa: "Ativa", Inativa: "Inativa", Suspensa: "Suspensa", Pendente: "Pendente", Configurada: "Configurada", Pronta: "Pronta", Falhou: "Falhou", Cancelada: "Cancelada", Manual: "Manual", Automática: "Automática" };
export function presentAdminRole(value: string) { return roles[value] ?? "Perfil não reconhecido"; }
export function presentAdminStatus(value: string | number | null | undefined) { return statuses[String(value)] ?? "Situação não reconhecida"; }
export function presentAdminScope(value: string) { return ({ TENANT: "Imobiliária", BUSINESS_UNIT: "Unidade", ASSET: "Imóvel", PROJECT: "Projeto de vistoria" } as Record<string, string>)[value] ?? "Abrangência não reconhecida"; }
export function presentPublicationMode(value: string) { return ({ MANUAL: "Manual", AUTOMATIC: "Automática" } as Record<string, string>)[value] ?? "Modo de publicação não reconhecido"; }
