"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { usePathname } from "next/navigation";
import { beginPKCE } from "@/auth/pkce";
import { clearProtectedContext, hasAdminAccess, restoreMembershipContext, setIdentity, setMembershipContext, type AdminIdentity } from "@/auth/session";
import { graphql, graphqlIdentity, type GraphQLFailure } from "@/graphql/client";
import { AdminAccessDocument, AdminAssetsDocument, AdminAuditDocument, AdminCatalogsDocument, AdminGovernanceDocument, AdminIdentityDocument, AdminOrganizationDocument, BootstrapTenantDocument, type AdminAccessQuery, type AdminAssetsQuery, type AdminAuditQuery, type AdminCatalogsQuery, type AdminGovernanceQuery, type AdminIdentityQuery, type AdminOrganizationQuery, type BootstrapTenantMutation } from "@/graphql/generated";

type Section = "Visão administrativa" | "Organização" | "Identidade e acesso" | "Participação" | "Configuração de inspeção" | "Governança" | "Auditoria";
type Row = Record<string, string | number | null>;
type PageData = { title: string; responsibility: string; rows: Row[]; hasNextPage: boolean };

const pages: Record<string, { title: Section; responsibility: string; href: string; primary: string }> = {
  "/overview": { title: "Visão administrativa", responsibility: "Contexto e saúde da configuração do tenant", href: "/overview", primary: "Revisar contexto" },
  "/organization": { title: "Organização", responsibility: "Tenant, unidades e origem administrativa", href: "/organization", primary: "Criar unidade" },
  "/access": { title: "Identidade e acesso", responsibility: "Usuários, convites, papéis e acesso efetivo", href: "/access", primary: "Convidar usuário" },
  "/catalogs": { title: "Participação", responsibility: "Participantes, contatos e segmentos", href: "/catalogs", primary: "Criar participante" },
  "/assets": { title: "Configuração de inspeção", responsibility: "Templates, perfis de análise e ativos", href: "/assets", primary: "Registrar ativo" },
  "/governance": { title: "Governança", responsibility: "Publicação, entregas, retenção e operações", href: "/governance", primary: "Configurar política" },
  "/audit": { title: "Auditoria", responsibility: "Eventos, histórico, uso e exportações autorizadas", href: "/audit", primary: "Exportar filtros" },
};
const navGroups = [
  ["Visão administrativa", [["Resumo do tenant", "/overview"]]], ["Organização", [["Tenant e unidades", "/organization"]]],
  ["Identidade e acesso", [["Usuários e permissões", "/access"]]], ["Participação", [["Participantes e segmentos", "/catalogs"]]],
  ["Configuração de inspeção", [["Templates, perfis e ativos", "/assets"]]], ["Governança", [["Políticas e entregas", "/governance"], ["Auditoria e uso", "/audit"]]],
] as const;
const emptyID = "00000000-0000-0000-0000-000000000000";
const toRow = (values: Record<string, string | number | boolean | null | undefined>): Row => Object.fromEntries(Object.entries(values).map(([key, value]) => [key, typeof value === "boolean" ? (value ? "Sim" : "Não") : value ?? "—"]));
const currentPage = (pathname: string) => pages[pathname] ?? pages["/overview"];

export function AdminShell({ section }: { section?: string }) {
  const pathname = usePathname(); const page = useMemo(() => currentPage(pathname), [pathname]);
  const [identityData, setIdentityData] = useState<AdminIdentityQuery>(); const [status, setStatus] = useState("Carregando contexto de identidade…");
  const [query, setQuery] = useState(""); const [data, setData] = useState<PageData>(); const [loading, setLoading] = useState(false); const [error, setError] = useState<string>();
  const [needsBootstrap, setNeedsBootstrap] = useState(false); const [bootstrapBusy, setBootstrapBusy] = useState(false);

  const establishIdentity = useCallback((result: AdminIdentityQuery) => {
    const stored = restoreMembershipContext(); const selected = result.me.memberships.find((item) => item.id === stored?.membershipId && item.status === "ACTIVE") ?? result.me.memberships.find((item) => item.status === "ACTIVE");
    if (!selected) { clearProtectedContext(); setStatus("Selecione uma associação ativa para carregar dados administrativos."); return; }
    const scope = stored?.scope ?? (selected.scopes.map((item) => item.kind).join(", ") || "Tenant");
    const identity: AdminIdentity = { tenantId: selected.tenantId, tenantName: result.tenant?.name ?? "Tenant", roles: result.me.roles, entitlements: result.me.productEntitlements, membershipId: selected.id, scope };
    setIdentity(identity); setMembershipContext(selected.id, scope); setStatus(result.tenant?.name ?? "Contexto selecionado");
  }, []);

  useEffect(() => { const controller = new AbortController(); void graphqlIdentity<AdminIdentityQuery>(AdminIdentityDocument, undefined, controller.signal).then((result) => { setIdentityData(result); if (!result.tenant && result.me.tenantId === emptyID) { setNeedsBootstrap(true); setStatus("Primeiro acesso: crie a operação local para continuar."); return; } establishIdentity(result); }).catch((failure: GraphQLFailure) => setError(failure.code === "FORBIDDEN" || failure.code === "UNAUTHENTICATED" ? "Acesso administrativo não autorizado. Nenhuma configuração foi carregada." : failure.message)); return () => controller.abort(); }, [establishIdentity]);

  const selectMembership = (membershipId: string) => {
    if (!identityData) return; clearProtectedContext(); setData(undefined); setError(undefined);
    const selected = identityData.me.memberships.find((item) => item.id === membershipId); if (!selected) return;
    const scope = selected.scopes.map((item) => item.kind).join(", ") || "Tenant";
    setIdentity({ tenantId: selected.tenantId, tenantName: identityData.tenant?.name ?? "Tenant", roles: identityData.me.roles, entitlements: identityData.me.productEntitlements, membershipId: selected.id, scope }); setMembershipContext(selected.id, scope); setStatus("Contexto alterado. Os dados anteriores foram descartados.");
  };

  const load = useCallback(async () => {
    if (!identityData || !hasAdminAccess()) return; const controller = new AbortController(); setLoading(true); setError(undefined);
    try {
      let next: PageData;
      switch (page.href) {
        case "/organization": { const result = await graphql<AdminOrganizationQuery>(AdminOrganizationDocument, { first: 25, after: null }, controller.signal); next = { ...page, rows: result.businessUnits.nodes.map((unit) => toRow({ ID: unit.id, Unidade: unit.name, Código: unit.code, Estado: unit.status, Versão: unit.version })), hasNextPage: result.businessUnits.pageInfo.hasNextPage }; break; }
        case "/access": { const result = await graphql<AdminAccessQuery>(AdminAccessDocument, { first: 25, after: null }, controller.signal); next = { ...page, rows: result.memberships.nodes.map((membership) => toRow({ ID: membership.id, Papel: membership.role, Escopo: membership.scopes.map((scope) => scope.kind).join(", ") || "Tenant", Estado: membership.status, Versão: membership.version })), hasNextPage: result.memberships.pageInfo.hasNextPage }; break; }
        case "/catalogs": { const result = await graphql<AdminCatalogsQuery>(AdminCatalogsDocument, { first: 25, after: null, search: query || null }, controller.signal); next = { ...page, rows: result.participants.nodes.map((participant) => toRow({ ID: participant.id, Participante: participant.name, Estado: participant.status, Contatos: participant.contacts.length, Versão: participant.version })), hasNextPage: result.participants.pageInfo.hasNextPage }; break; }
        case "/assets": { const result = await graphql<AdminAssetsQuery>(AdminAssetsDocument, { first: 25, after: null, search: query || null }, controller.signal); next = { ...page, rows: result.assets.nodes.map((asset) => toRow({ ID: asset.id, Ativo: asset.name, Chave: asset.externalKey, Escopo: asset.businessUnitId, Estado: asset.status, Versão: asset.version })), hasNextPage: result.assets.pageInfo.hasNextPage }; break; }
        case "/governance": { const result = await graphql<AdminGovernanceQuery>(AdminGovernanceDocument, { first: 25, after: null }, controller.signal); next = { ...page, rows: [toRow({ Política: "Publicação", Estado: result.publicationPolicy.mode, Versão: result.publicationPolicy.version }), ...result.retentionPolicies.nodes.map((policy) => toRow({ ID: policy.id, Política: "Retenção", Evidências: `${policy.evidenceDays} dias`, Operacional: `${policy.operationalDays} dias`, Segurança: `${policy.securityDays} dias`, Versão: policy.version })), ...result.notificationDeliveries.nodes.map((delivery) => toRow({ ID: delivery.id, Política: "Entrega", Estado: delivery.status, Atualizado: delivery.updatedAt }))], hasNextPage: result.retentionPolicies.pageInfo.hasNextPage || result.notificationDeliveries.pageInfo.hasNextPage }; break; }
        case "/audit": { const result = await graphql<AdminAuditQuery>(AdminAuditDocument, { first: 25, after: null }, controller.signal); next = { ...page, rows: result.auditEvents.nodes.map((event) => toRow({ ID: event.id, Ação: event.action, Recurso: event.targetType, Alvo: event.targetId, Resultado: event.outcome, Quando: event.occurredAt })), hasNextPage: result.auditEvents.pageInfo.hasNextPage }; break; }
        default: next = { ...page, rows: [toRow({ Contexto: "Tenant", Valor: identityData.tenant?.name ?? "Não selecionado" }), toRow({ Contexto: "Papel atual", Valor: identityData.me.roles.join(", ") || "Sem papel" }), toRow({ Contexto: "Escopo efetivo", Valor: identityData.me.effectiveScopes.map((scope) => scope.kind).join(", ") || "Tenant" })], hasNextPage: false };
      }
      setData(next); setStatus(`${next.title} atualizado. ${next.rows.length} registros visíveis.`);
    } catch (failure) { setError((failure as GraphQLFailure).message); } finally { setLoading(false); }
  }, [identityData, page, query]);
  useEffect(() => { if (identityData && hasAdminAccess()) void load(); }, [identityData, load]);

  const bootstrap = async () => { setBootstrapBusy(true); setError(undefined); try { const result = await graphqlIdentity<BootstrapTenantMutation>(BootstrapTenantDocument, { input: { name: "Minha operação", businessUnitCode: "MATRIZ", businessUnitName: "Matriz", clientMutationId: "local-bootstrap-admin" } }); const issue = result.createTenant.userErrors[0]; if (issue) throw new Error(issue.message); setStatus("Operação local criada. Atualize a sessão para selecionar a associação."); } catch (failure) { setError(failure instanceof Error ? failure.message : "Não foi possível criar a operação local."); } finally { setBootstrapBusy(false); } };
  const signIn = () => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", pathname);
  if (needsBootstrap) return <main className="admin-denial"><h1>Primeiro acesso</h1><p role="status">{status}</p><button disabled={bootstrapBusy} onClick={() => void bootstrap()}>{bootstrapBusy ? "Criando…" : "Criar operação local"}</button></main>;
  if (error && !identityData) return <main className="admin-denial"><h1>Administração</h1><p role="alert">{error}</p><button onClick={signIn}>Entrar com conta administrativa</button><a href={process.env.NEXT_PUBLIC_DASHBOARD_URL ?? "http://localhost:3002"}>Ir para o Dashboard</a></main>;
  const activeMembership = identityData?.me.memberships.find((item) => item.id === restoreMembershipContext()?.membershipId); const permitted = hasAdminAccess();
  return <main className="admin-shell"><a className="skip-link" href="#admin-content">Pular para o conteúdo</a><header className="admin-header"><div><strong>Inspection Admin</strong><span>Trilha de evidências · operação precisa</span></div><label>Associação ativa<select value={activeMembership?.id ?? ""} onChange={(event) => selectMembership(event.target.value)}>{identityData?.me.memberships.filter((item) => item.status === "ACTIVE").map((item) => <option key={item.id} value={item.id}>{item.role} · {item.tenantId}</option>)}</select></label><p><b>Tenant:</b> {identityData?.tenant?.name ?? "Não selecionado"}<br /><b>Escopo:</b> {activeMembership?.scopes.map((item) => item.kind).join(", ") || "Tenant"}</p><a className="dashboard-exit" href={process.env.NEXT_PUBLIC_DASHBOARD_URL ?? "http://localhost:3002"}>Abrir Dashboard</a></header><div className="admin-layout"><nav className="admin-nav" aria-label="Navegação administrativa">{navGroups.map(([group, items]) => <div key={group}><h2>{group}</h2>{items.map(([label, href]) => <Link key={href} href={href} aria-current={pathname === href ? "page" : undefined}>{label}</Link>)}</div>)}</nav><section id="admin-content" className="admin-content" aria-labelledby="page-title"><p className="breadcrumb">Administração / {page.title}</p><div className="page-heading"><div><h1 id="page-title">{section ?? page.title}</h1><p>{page.responsibility}</p></div><button disabled={!permitted || loading} onClick={() => void load()}>{loading ? "Atualizando…" : "Atualizar"}</button></div><p className="context-line"><b>Responsabilidade:</b> {page.responsibility} · <b>Papel:</b> {identityData?.me.roles.join(", ") || "—"}</p>{!permitted ? <div className="admin-state denied" role="alert">Você não tem permissão para acessar este recurso neste escopo.</div> : <><div className="collection-toolbar"><label>Buscar nesta coleção<input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Nome, identificador ou contexto" /></label><button className="secondary" onClick={() => { setQuery(""); void load(); }}>Limpar filtros</button><button>{page.primary}</button></div><p role="status" className="status-line">{loading ? `Carregando ${page.title.toLowerCase()}…` : status}</p>{error ? <div className="admin-state error" role="alert">{error} <button onClick={() => void load()}>Tentar novamente</button></div> : <Collection data={data} query={query} />}</>}</section></div></main>;
}

function Collection({ data, query }: { data?: PageData; query: string }) {
  if (!data) return <div className="admin-state loading" role="status">Carregando registros sem alterar o seu contexto…</div>;
  if (data.rows.length === 0) return <div className="admin-state empty"><h2>{query ? "Nenhum resultado para os filtros atuais" : "Ainda não há registros neste tenant"}</h2><p>{query ? "Os filtros foram preservados. Ajuste a busca ou limpe os filtros." : "Use a ação principal para iniciar este recurso."}</p></div>;
  const columns = Object.keys(data.rows[0]);
  return <><p className="collection-count">{data.rows.length} registros · atualização atual · {data.hasNextPage ? "mais páginas disponíveis" : "fim da página atual"}</p><div className="table-wrap"><table><caption>{data.title}: coleção administrativa</caption><thead><tr>{columns.map((column) => <th scope="col" key={column}>{column}</th>)}<th scope="col">Ações</th></tr></thead><tbody>{data.rows.map((record, index) => <tr key={`${record.ID ?? index}`}>{columns.map((column) => <td key={column} data-label={column} className={column === "ID" ? "copyable-id" : undefined}>{record[column]}</td>)}<td data-label="Ações"><button className="link-button" onClick={() => navigator.clipboard?.writeText(String(record.ID ?? ""))}>Copiar ID</button><button className="link-button">Ver histórico</button></td></tr>)}</tbody></table></div></>;
}
