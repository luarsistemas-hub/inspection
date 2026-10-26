import { isSuperAdmin, type DashboardIdentity } from "@/auth/session";

export type Capability = { audience: "internal" | "customer"; canMutate: boolean; canPublish: boolean; canPromoteOrigin: boolean; canUseAdmin: boolean; canManageTriage: boolean; canReadTrail: boolean; links: Array<[string, string]>; home: string };
const customerLinks: Array<[string, string]> = [["Portfólio", "/portfolio"], ["Inventários", "/baselines"], ["Laudos publicados", "/reports"], ["Notificações", "/notifications"]];
const viewerLinks: Array<[string, string]> = [["Início", "/tenants/current"], ["Agenda de vistorias", "/schedules"], ["Vistorias", "/inspections"], ["Projetos de vistoria", "/projects"], ["Inventários", "/baselines"], ["Triagem", "/triage"], ["Laudos de vistoria", "/reports"], ["Notificações", "/notifications"]];
const operatorLinks: Array<[string, string]> = [...viewerLinks];
const auditorLinks: Array<[string, string]> = [["Inventários", "/baselines"]];

export function composeCapabilities(identity: Pick<DashboardIdentity, "roles" | "entitlements">): Capability {
  const customer = identity.roles.includes("CUSTOMER_VIEWER");
  // An auditor without another Dashboard role only reads inventories and their trail.
  const auditorOnly = identity.roles.length > 0 && identity.roles.every((role) => role === "AUDITOR");
  const viewer = customer || auditorOnly || identity.roles.includes("VIEWER"); const superAdmin = isSuperAdmin(identity); const manager = identity.roles.includes("MANAGER") || superAdmin;
  const canPromoteOrigin = manager || identity.roles.includes("TENANT_ADMIN");
  if (customer) return { audience: "customer", canMutate: false, canPublish: false, canPromoteOrigin: false, canUseAdmin: false, canManageTriage: false, canReadTrail: false, links: customerLinks, home: "Imóveis e projetos compartilhados" };
  const canManageTriage = manager || identity.roles.includes("TENANT_ADMIN");
  // The inventory trail is authorized for auditors, managers and tenant admins.
  const canReadTrail = canManageTriage || identity.roles.includes("AUDITOR");
  const links = auditorOnly ? auditorLinks : canManageTriage ? [...viewerLinks, ["Revisão de privacidade", "/privacy-review"] as [string, string]] : viewerLinks;
  return { audience: "internal", canMutate: !viewer, canPublish: manager, canPromoteOrigin, canUseAdmin: superAdmin, canManageTriage, canReadTrail, links, home: manager ? "Trabalho prioritário no seu escopo" : viewer ? "Acompanhamento somente leitura" : "Seu trabalho operacional" };
}
