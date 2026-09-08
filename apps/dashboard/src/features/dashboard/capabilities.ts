import type { DashboardIdentity } from "@/auth/session";

export type Capability = { audience: "internal" | "customer"; canMutate: boolean; canPublish: boolean; canUseAdmin: boolean; links: Array<[string, string]>; home: string };
const customerLinks: Array<[string, string]> = [["Portfólio", "/portfolio"], ["Relatórios publicados", "/reports"], ["Notificações", "/notifications"]];
const viewerLinks: Array<[string, string]> = [["Início", "/tenants/current"], ["Inspeções", "/inspections"], ["Projetos", "/projects"], ["Triagem", "/triage"], ["Relatórios", "/reports"], ["Notificações", "/notifications"]];
const operatorLinks: Array<[string, string]> = [...viewerLinks];

export function composeCapabilities(identity: Pick<DashboardIdentity, "roles" | "entitlements">): Capability {
  const customer = identity.roles.includes("CUSTOMER_VIEWER"); const viewer = customer || identity.roles.includes("VIEWER"); const manager = identity.roles.includes("MANAGER") || identity.roles.includes("TENANT_ADMIN");
  if (customer) return { audience: "customer", canMutate: false, canPublish: false, canUseAdmin: false, links: customerLinks, home: "Ativos e projetos compartilhados" };
  return { audience: "internal", canMutate: !viewer, canPublish: manager, canUseAdmin: identity.entitlements.includes("ADMIN") && identity.roles.includes("TENANT_ADMIN"), links: viewerLinks, home: manager ? "Trabalho prioritário no seu escopo" : viewer ? "Acompanhamento somente leitura" : "Seu trabalho operacional" };
}
