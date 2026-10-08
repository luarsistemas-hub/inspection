"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { FormEvent, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AccountMenu, AdaptiveNavigation, Alert, Button, Checkbox, Dialog, Field, Icon, IconButton, Input, PageHeader, Pagination, Recovery, Select, Status, Textarea, ThemeSelector, type AdaptiveNavigationItem } from "@inspection/design-system";
import { beginPKCE, endAdminSSO } from "@/auth/pkce";
import { clearProtectedContext, clearSession, getIDToken, hasAdminAccess, hasAdminRouteAccess, hasAnalysisPromptAccess, restoreMembershipContext, setIdentity, setMembershipContext, type AdminIdentity } from "@/auth/session";
import { graphql, graphqlIdentity, type GraphQLFailure } from "@/graphql/client";
import * as G from "@/graphql/generated";
import { presentAdminRole, presentAdminScope, presentAdminStatus } from "./presentation";
import { buildGovernanceRows } from "./governance-presentation";
import { isHistoryForResource } from "./history";
import { RequestGuard } from "./request-guard";
import { LLMUsagePage } from "../llm-usage/llm-usage-page";
import { AddressFieldStyles, AddressForm, emptyPostalAddress, type PostalAddress, type PostalLookup } from "@inspection/address";
import { AssetAttributeFields, normalizeAssetAttributes, type SegmentAttributeSchema } from "./asset-attributes";

type Row = Record<string, string | number | null>;
type Collection = { title: string; responsibility: string; columns: string[]; rows: Row[]; hasNextPage: boolean; endCursor: string | null };
type HistoryPage = { events: G.AdminHistoryQuery["auditEvents"]["nodes"]; hasNextPage: boolean; endCursor: string | null };
type MembershipOption = G.AdminIdentityQuery["me"]["memberships"][number];
type Action = "unit" | "invite" | "participant" | "asset" | "policy";
type AdminFormOptions = { units: G.AdminOrganizationQuery["businessUnits"]["nodes"]; segments: G.AdminCatalogsQuery["segmentDefinitions"]["nodes"]; templates: G.AdminCatalogsQuery["templates"]["nodes"] };

const pages: Record<string, { title: string; responsibility: string; primary: string }> = {
  "/overview": { title: "Minha operação", responsibility: "Contexto e saúde da configuração da imobiliária", primary: "Revisar contexto" },
  "/organization": { title: "Organização", responsibility: "Unidades e estrutura da sua operação.", primary: "Criar unidade" },
  "/access": { title: "Identidade e acesso", responsibility: "Usuários, convites, papéis e acesso efetivo", primary: "Convidar usuário" },
  "/catalogs": { title: "Responsáveis pela vistoria", responsibility: "Responsáveis, contatos e segmentos", primary: "Criar responsável" },
  "/assets": { title: "Configuração de vistorias", responsibility: "Modelos de vistoria e imóveis", primary: "Registrar imóvel" },
  "/templates": { title: "Modelos de vistoria", responsibility: "Checklists reutilizáveis associados ao segmento do imóvel.", primary: "Novo modelo" },
  "/prompts": { title: "Prompts de análise", responsibility: "Instrução global usada pela análise visual de imóveis", primary: "Atualizar prompt" },
  "/governance": { title: "Governança", responsibility: "Publicação, entregas e retenção", primary: "Configurar política" },
  "/audit": { title: "Auditoria", responsibility: "Eventos, histórico, uso e exportações autorizadas", primary: "Exportar filtros" },
  "/llm-usage": { title: "Consumo de LLM", responsibility: "Chamadas, tokens e custos informados pelo gateway em todos os tenants", primary: "Atualizar" },
};
const navItems = [
  ["Visão geral", "/overview"],
  ["Organização", "/organization"],
  ["Usuários e acessos", "/access"],
  ["Responsáveis pela vistoria", "/catalogs"],
  ["Configuração", "/assets"],
  ["Modelos de vistoria", "/templates"],
  ["Prompts de análise", "/prompts"],
  ["Governança", "/governance"],
  ["Auditoria", "/audit"],
  ["Consumo de LLM", "/llm-usage"],
] as const;
const emptyID = "00000000-0000-0000-0000-000000000000";
const makeRow = (values: Record<string, string | number | boolean | null | undefined>): Row => Object.fromEntries(Object.entries(values).map(([key, value]) => [key, typeof value === "boolean" ? (value ? "Sim" : "Não") : value ?? "—"]));
const failureText = (failure: unknown) => failure instanceof Error ? failure.message : (failure as GraphQLFailure).message ?? "Não foi possível concluir a operação.";

export function AdminShell({ section }: { section?: string }) {
  const pathname = usePathname(); const router = useRouter(); const params = useSearchParams();
  const page = useMemo(() => pages[pathname] ?? pages["/overview"], [pathname]);
  const [identityData, setIdentityData] = useState<G.AdminIdentityQuery>(); const [membershipOptions, setMembershipOptions] = useState<MembershipOption[]>([]);
  const [status, setStatus] = useState("Carregando contexto de identidade…"); const [collection, setCollection] = useState<Collection>();
  const [loading, setLoading] = useState(false); const [error, setError] = useState<string>(); const [needsBootstrap, setNeedsBootstrap] = useState(false); const [bootstrapPending, setBootstrapPending] = useState(false);
  const [analysisPrompt, setAnalysisPrompt] = useState<G.AdminAnalysisPromptQuery["analysisPrompt"]>();
  const [action, setAction] = useState<Action>(); const [detail, setDetail] = useState<Row>(); const [addressEdit, setAddressEdit] = useState<Row>(); const [history, setHistory] = useState<HistoryPage>(); const [historyError, setHistoryError] = useState<string>(); const [correction, setCorrection] = useState<Row>();
  const [focusedRowId, setFocusedRowId] = useState<string>();
  const request = useRef<AbortController | undefined>(undefined); const historyRequest = useRef<AbortController | undefined>(undefined); const collectionGuard = useRef(new RequestGuard()).current; const historyGeneration = useRef(0); const query = params.get("search") ?? ""; const cursor = params.get("after");
  const updateParams = useCallback((updates: Record<string, string | null>) => { const next = new URLSearchParams(params.toString()); Object.entries(updates).forEach(([key, value]) => value ? next.set(key, value) : next.delete(key)); router.replace(`${pathname}${next.size ? `?${next}` : ""}`); }, [params, pathname, router]);

  const establishIdentity = useCallback((result: G.AdminIdentityQuery) => {
    const stored = restoreMembershipContext(); const selected = result.me.memberships.find((item) => item.id === stored?.membershipId && item.status === "ACTIVE") ?? result.me.memberships.find((item) => item.status === "ACTIVE");
    if (!selected) { clearProtectedContext(); setStatus("Selecione uma associação ativa para carregar dados administrativos."); return; }
    const scope = stored?.scope ?? (selected.scopes.map((item) => item.kind).join(", ") || "Tenant");
    const identity: AdminIdentity = { tenantId: selected.tenantId, tenantName: result.tenant?.name ?? "Imobiliária", roles: result.me.roles, entitlements: result.me.productEntitlements, membershipId: selected.id, scope };
    setIdentity(identity); setMembershipContext(selected.id, scope); setStatus(`Contexto ativo: ${result.tenant?.name ?? "Imobiliária"}`);
  }, []);
  const loadIdentity = useCallback(async (signal?: AbortSignal) => {
    const selector = await graphqlIdentity<G.AdminIdentityQuery>(G.AdminIdentityDocument, undefined, signal); const active = selector.me.memberships.filter((item) => item.status === "ACTIVE");
    setMembershipOptions(active); if (!active.length) { setIdentityData(selector); clearProtectedContext(); setNeedsBootstrap(selector.me.tenantId === emptyID && selector.me.memberships.length === 0); setStatus("Selecione uma associação ativa para carregar dados administrativos."); return; }
    const stored = restoreMembershipContext(); setMembershipContext(active.find((item) => item.id === stored?.membershipId)?.id ?? active[0].id, stored?.scope ?? "Tenant");
    const result = await graphql<G.AdminIdentityQuery>(G.AdminIdentityDocument, undefined, signal); setIdentityData(result); establishIdentity(result); setNeedsBootstrap(false);
  }, [establishIdentity]);
  useEffect(() => { const controller = new AbortController(); void loadIdentity(controller.signal).catch((failure: GraphQLFailure) => setError(failure.code === "FORBIDDEN" || failure.code === "UNAUTHENTICATED" ? "Acesso administrativo não autorizado. Nenhuma configuração foi carregada." : failure.message)); return () => controller.abort(); }, [loadIdentity]);

  const load = useCallback(async () => {
    if (!identityData || !hasAdminAccess() || !hasAdminRouteAccess(pathname, identityData.me.roles) || pathname === "/llm-usage" || pathname === "/templates") return; request.current?.abort(); const controller = new AbortController(); request.current = controller; const currentGeneration = collectionGuard.begin();
    setLoading(true); setError(undefined); const variables = { first: 25, after: cursor, search: query || null };
    try { let next: Collection;
      switch (pathname) {
        case "/organization": { const r = await graphql<G.AdminOrganizationQuery>(G.AdminOrganizationDocument, { ...variables, first: 100 }, controller.signal); next = { ...pages[pathname], columns: ["ID", "Unidade", "Código", "Situação", "Versão"], rows: r.businessUnits.nodes.map((x) => makeRow({ ID: x.id, Unidade: x.name, Código: x.code, Situação: x.status, Versão: x.version })), hasNextPage: r.businessUnits.pageInfo.hasNextPage, endCursor: r.businessUnits.pageInfo.endCursor }; break; }
        case "/access": { const r = await graphql<G.AdminAccessQuery>(G.AdminAccessDocument, variables, controller.signal); next = { ...pages[pathname], columns: ["ID", "Usuário", "E-mail", "Perfil de acesso", "Abrangência", "Situação", "Convite", "Versão"], rows: r.memberships.nodes.map((x) => makeRow({ ID: x.id, membershipId: x.id, Usuário: x.name, "E-mail": x.email, "Perfil de acesso": presentAdminRole(x.role), Abrangência: x.scopes.map((s) => presentAdminScope(s.kind)).join(", ") || "Imobiliária", Situação: x.status, Convite: presentInvitationStatus(x.invitationStatus), rawInvitationStatus: x.invitationStatus, Versão: x.version })), hasNextPage: r.memberships.pageInfo.hasNextPage, endCursor: r.memberships.pageInfo.endCursor }; break; }
        case "/catalogs": { const r = await graphql<G.AdminCatalogsQuery>(G.AdminCatalogsDocument, variables); next = { ...pages[pathname], columns: ["ID", "Responsável pela vistoria", "E-mail", "Confirmação do e-mail", "Situação", "Contatos", "Versão"], rows: r.participants.nodes.map((x) => { const emails = x.contacts.filter((contact) => contact.channel === "EMAIL" && contact.active); return makeRow({ ID: x.id, "Responsável pela vistoria": x.name, "E-mail": emails.map((contact) => contact.value).join(", ") || "—", "Confirmação do e-mail": emails.length ? emails.every((contact) => contact.verified) ? "Confirmado" : "Aguardando primeiro acesso" : "Não informado", Situação: x.status, Contatos: x.contacts.length, Versão: x.version }); }), hasNextPage: r.participants.pageInfo.hasNextPage, endCursor: r.participants.pageInfo.endCursor }; break; }
        case "/assets": { const r = await graphql<G.AdminAssetsQuery>(G.AdminAssetsDocument, variables); next = { ...pages[pathname], columns: ["ID", "Imóvel", "Código do imóvel", "Endereço", "Cadastro do endereço", "Abrangência", "Situação", "Versão"], rows: r.assets.nodes.map((x) => makeRow({ ID: x.id, assetId: x.id, Imóvel: x.name, "Código do imóvel": x.externalKey, Endereço: x.address, "Cadastro do endereço": x.addressStatus === "COMPLETE" ? "Completo" : x.addressStatus === "INCOMPLETE" ? "Incompleto" : "Legado", Abrangência: "Unidade vinculada", Situação: x.status, Versão: x.version, businessUnitId: x.businessUnitId, segmentVersionId: x.segmentVersionId, templateId: x.templateId, latitudeE6: x.latitudeE6, longitudeE6: x.longitudeE6, geofenceMeters: x.geofenceMeters, addressStatus: x.addressStatus, addressDetails: JSON.stringify(x.addressDetails), attributes: JSON.stringify(x.attributes), policyOverrides: JSON.stringify(x.policyOverrides), assignments: JSON.stringify(x.assignments) })), hasNextPage: r.assets.pageInfo.hasNextPage, endCursor: r.assets.pageInfo.endCursor }; break; }
        case "/prompts": { const r = await graphql<G.AdminAnalysisPromptQuery>(G.AdminAnalysisPromptDocument, undefined, controller.signal); setAnalysisPrompt(r.analysisPrompt); next = { ...pages[pathname], columns: ["Tipo", "Modelo", "Confiança mínima", "Revisão", "Digest", "Atualizado"], rows: [makeRow({ Tipo: r.analysisPrompt.analysisType, Modelo: r.analysisPrompt.modelAlias, "Confiança mínima": `${r.analysisPrompt.minimumConfidenceBps / 100}%`, Revisão: r.analysisPrompt.revision, Digest: r.analysisPrompt.canonicalDigest, Atualizado: r.analysisPrompt.updatedAt })], hasNextPage: false, endCursor: null }; break; }
        case "/governance": {
          const [policy, retention, deliveries] = await Promise.allSettled([
            graphql<G.AdminPublicationPolicyQuery>(G.AdminPublicationPolicyDocument),
            graphql<G.AdminRetentionPoliciesQuery>(G.AdminRetentionPoliciesDocument),
            graphql<G.AdminNotificationDeliveriesQuery>(G.AdminNotificationDeliveriesDocument, variables),
          ]);
          const governance = buildGovernanceRows(policy, retention, deliveries);
          next = { ...pages[pathname], columns: ["Recurso", "ID", "Vistoria", "Destinatário", "Situação", "Código", "Atualizado", "Detalhe"], ...governance };
          break;
        }
        case "/audit": { const r = await graphql<G.AdminAuditQuery>(G.AdminAuditDocument, variables); next = { ...pages[pathname], columns: ["ID", "Data/hora", "Ação", "Recurso", "Abrangência", "Resultado"], rows: r.auditEvents.nodes.map((x) => makeRow({ ID: x.id, targetId: x.targetId, "Data/hora": x.occurredAt, Ação: x.action, Recurso: x.targetType, Abrangência: identityData.tenant?.name, Resultado: x.outcome })), hasNextPage: r.auditEvents.pageInfo.hasNextPage, endCursor: r.auditEvents.pageInfo.endCursor }; setStatus(`${pages[pathname].title} atualizado. Uso: ${r.usageSummary.requests} requisições · custo ${r.usageSummary.cost == null ? "restrito" : r.usageSummary.cost}`); break; }
        default: next = { ...pages["/overview"], columns: ["Contexto", "Valor"], rows: [makeRow({ Contexto: "Imobiliária", Valor: identityData.tenant?.name }), makeRow({ Contexto: "Papel atual", Valor: identityData.me.roles.map(presentAdminRole).join(", ") || "Sem papel" }), makeRow({ Contexto: "Abrangência efetiva", Valor: identityData.me.effectiveScopes.map((x) => presentAdminScope(x.kind)).join(", ") || "Imobiliária" })], hasNextPage: false, endCursor: null };
      }
      if (collectionGuard.isCurrent(currentGeneration, controller.signal)) { setCollection(next); setStatus(`${next.title} atualizado. ${next.rows.length} registros visíveis.`); }
    } catch (failure) { if (!controller.signal.aborted && collectionGuard.isCurrent(currentGeneration)) setError(failureText(failure)); } finally { if (collectionGuard.isCurrent(currentGeneration)) setLoading(false); }
  }, [collectionGuard, cursor, identityData, pathname, query]);
  useEffect(() => { if (!identityData || !hasAdminAccess() || !hasAdminRouteAccess(pathname, identityData.me.roles) || pathname === "/llm-usage") return; const timer = window.setTimeout(() => void load(), query ? 250 : 0); return () => { window.clearTimeout(timer); request.current?.abort(); }; }, [identityData, load, pathname, query]);

  const selectMembership = async (membershipId: string) => { if (!membershipOptions.some((item) => item.id === membershipId)) return; request.current?.abort(); collectionGuard.invalidate(); historyRequest.current?.abort(); historyGeneration.current += 1; clearProtectedContext(); setCollection(undefined); setDetail(undefined); setHistory(undefined); setHistoryError(undefined); setError(undefined); updateParams({ after: null }); setMembershipContext(membershipId); setStatus("Trocando o contexto de acesso. Os dados anteriores foram descartados."); try { const result = await graphql<G.AdminIdentityQuery>(G.AdminIdentityDocument); setIdentityData(result); establishIdentity(result); } catch (failure) { setError(failureText(failure)); } };
  const showHistory = async (item: Row, after: string | null = null, append = false) => { historyRequest.current?.abort(); const controller = new AbortController(); historyRequest.current = controller; const currentGeneration = ++historyGeneration.current; setDetail(item); if (!append) setHistory(undefined); setHistoryError(undefined); try { const result = await graphql<G.AdminHistoryQuery>(G.AdminHistoryDocument, { first: 25, after }, controller.signal); if (currentGeneration === historyGeneration.current && !controller.signal.aborted) { const events = result.auditEvents.nodes.filter((event) => isHistoryForResource(item, event.targetId)); setHistory((current) => ({ events: append && current ? [...current.events, ...events] : events, hasNextPage: result.auditEvents.pageInfo.hasNextPage, endCursor: result.auditEvents.pageInfo.endCursor })); } } catch (failure) { if (!controller.signal.aborted && currentGeneration === historyGeneration.current) setHistoryError(failureText(failure)); } };
  const exportCsv = () => { if (!collection) return; const csv = [collection.columns, ...collection.rows.map((x) => collection.columns.map((c) => String(x[c] ?? "").replaceAll('"', '""')))].map((line) => line.map((value) => `"${value}"`).join(",")).join("\n"); const url = URL.createObjectURL(new Blob([csv], { type: "text/csv;charset=utf-8" })); const link = document.createElement("a"); link.href = url; link.download = `${pathname.slice(1)}-registros-exibidos.csv`; link.click(); URL.revokeObjectURL(url); setStatus(`CSV preparado com ${collection.rows.length} registros autorizados atualmente exibidos nesta página.`); };
  const bootstrap = async () => { if (bootstrapPending) return; setBootstrapPending(true); setError(undefined); try { await graphqlIdentity<G.BootstrapTenantMutation>(G.BootstrapTenantDocument, { input: { name: "Minha operação", businessUnitCode: "MATRIZ", businessUnitName: "Matriz", clientMutationId: "local-bootstrap-admin" } }); await loadIdentity(); } catch (failure) { setError(failureText(failure)); } finally { setBootstrapPending(false); } };
  const permitted = hasAdminAccess(); const routePermitted = permitted && hasAdminRouteAccess(pathname, identityData?.me.roles ?? []); const canInviteInternalUsers = identityData?.me.roles.some((role) => ["TENANT_ADMIN", "ACCESS_ADMIN", "ORGANIZATION_ADMIN"].includes(role)) ?? false; const promptPermitted = permitted && hasAnalysisPromptAccess() && Boolean(identityData?.me.roles.some((role) => role === "TENANT_ADMIN" || role === "INSPECTION_CONFIG_ADMIN") && identityData.me.productEntitlements.includes("ADMIN")); const activeMembership = identityData?.me.memberships.find((x) => x.id === restoreMembershipContext()?.membershipId); const tenantName = identityData?.tenant?.name ?? "Não selecionado";
  const profileName = identityData?.tenant?.name?.trim() || "Imobiliária";
  const visibleCollection = collection ? { ...collection, rows: pathname === "/organization" && query.trim() ? collection.rows.filter((row) => `${row.Unidade ?? ""} ${row.Código ?? ""}`.toLocaleLowerCase("pt-BR").includes(query.trim().toLocaleLowerCase("pt-BR"))) : collection.rows } : undefined;
  const policyVersion = collection?.rows.find((row) => row.Recurso === "Política de publicação")?.Versão;
  const currentPolicyVersion = typeof policyVersion === "number" && Number.isSafeInteger(policyVersion) && policyVersion > 0 ? policyVersion : undefined;
  const primaryAction = () => {
    if (pathname === "/overview") return void router.push("/organization");
    if (pathname === "/templates") return;
    if (pathname === "/audit") return exportCsv();
    if (pathname === "/access" && !canInviteInternalUsers) return;
    setAction(pathname === "/organization" ? "unit" : pathname === "/access" ? "invite" : pathname === "/catalogs" ? "participant" : pathname === "/assets" ? "asset" : "policy");
  };
  if (needsBootstrap) return <AdminGate title="Primeiro acesso" description="Crie a operação local para configurar o ambiente de trabalho."><ThemeSelector /><Recovery kind={error ? "error" : "unavailable"} title="Configuração inicial">{error ?? status}</Recovery><Button disabled={bootstrapPending} isPending={bootstrapPending} pendingLabel="Criando…" onClick={() => void bootstrap()}>Criar operação local</Button></AdminGate>;
  if (error && !identityData) return <AdminGate title="Administração" description="Entre com uma conta autorizada para continuar."><ThemeSelector /><div className="admin-gate-notice" role="status">{error}</div><Button onClick={() => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", pathname)}>Entrar com conta administrativa</Button><a href={process.env.NEXT_PUBLIC_DASHBOARD_URL ?? "http://localhost:3002"}>Ir para o Painel</a></AdminGate>;
  const visibleNavItems = navItems.filter(([, href]) => Boolean(identityData && hasAdminRouteAccess(href, identityData.me.roles)) && (href !== "/prompts" || promptPermitted) && (href !== "/llm-usage" || identityData?.me.canViewLLMCosts));
  const primaryAdminHrefs = ["/overview", "/organization", "/access"];
  const navigationItem = ([label, href]: readonly [string, string]): AdaptiveNavigationItem => ({ href, label, icon: href === "/overview" ? "layout-dashboard" : href === "/organization" ? "building-2" : href === "/access" ? "users" : href === "/catalogs" ? "clipboard-check" : href === "/assets" || href === "/templates" ? "building-2" : href === "/prompts" ? "sparkles" : href === "/governance" ? "shield-check" : href === "/audit" ? "history" : "chart-no-axes-combined", group: href === "/governance" || href === "/audit" ? "Governança" : href === "/prompts" || href === "/llm-usage" ? "Análise" : "Cadastros" });
  const primaryAdminItems = visibleNavItems.filter(([, href]) => primaryAdminHrefs.includes(href)).map(navigationItem);
  const secondaryAdminItems = visibleNavItems.filter(([, href]) => !primaryAdminHrefs.includes(href)).map(navigationItem);
  secondaryAdminItems.push({ href: process.env.NEXT_PUBLIC_DASHBOARD_URL ?? "http://localhost:3002", label: "Abrir Painel", icon: "arrow-up-right" });
  return <main className={`admin-shell${pathname === "/overview" ? " admin-shell--overview" : ""}`}>
    <a className="skip-link" href="#admin-content">Pular para o conteúdo</a>
    <header className="admin-header">
      <Link className="admin-brand" href="/overview" aria-label="Inspection Administração: visão geral"><span className="admin-brand-mark" aria-hidden="true">I</span><span className="admin-brand-desktop">Administração <small>/ {tenantName}</small></span><span className="admin-brand-mobile">Inspection</span></Link>
      <div className="admin-header-context">
        <ThemeSelector />
        <AccountMenu
          name={profileName}
          description={activeMembership ? presentAdminRole(activeMembership.role) : "Acesso administrativo"}
          onSignOut={() => {
            const idToken = getIDToken();
            request.current?.abort(); historyRequest.current?.abort(); collectionGuard.invalidate();
            clearProtectedContext(); clearSession(); sessionStorage.removeItem("inspection.admin.pkce");
            endAdminSSO(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", idToken);
          }}
        >
          {membershipOptions.length > 1 ? <Field label="Imobiliária ativa"><Select aria-label="Imobiliária ativa" value={activeMembership?.id ?? ""} onChange={(event) => void selectMembership(event.target.value)}>{membershipOptions.map((x) => <option key={x.id} value={x.id}>{x.name} · {presentAdminRole(x.role)}</option>)}</Select></Field> : null}
        </AccountMenu>
      </div>
    </header>
    <div className="admin-layout">
      <AdaptiveNavigation label="Navegação administrativa" activeHref={pathname} primaryItems={primaryAdminItems} secondaryItems={secondaryAdminItems} renderLink={(item, className, onNavigate) => item.href.startsWith("/") ? <Link key={item.href} aria-current={pathname === item.href ? "page" : undefined} className={className} href={item.href} title={item.label} onClick={onNavigate} prefetch={false}><span className="inspection-adaptive-navigation__icon"><Icon name={item.icon} size={22} /></span><span>{item.label}</span></Link> : <a key={item.href} className={className} href={item.href} title={item.label} onClick={onNavigate}><span className="inspection-adaptive-navigation__icon"><Icon name={item.icon} size={22} /></span><span>{item.label}</span></a>} />
      <section id="admin-content" className="admin-content" aria-labelledby="page-title">
        <PageHeader id="page-title" emphasis="plain" breadcrumbs={<><Link href="/overview">Administração</Link><span aria-hidden="true">/</span><span>{page.title}</span></>} title={pathname === "/overview" ? page.title : section ?? page.title} description={page.responsibility} descriptionMode="disclosure" actions={pathname === "/llm-usage" || pathname === "/templates" || (pathname === "/access" && !canInviteInternalUsers) ? null : <Button disabled={!routePermitted || loading || pathname === "/prompts" || (pathname === "/audit" && !collection)} onClick={primaryAction} isPending={loading && pathname === "/overview"} pendingLabel="Atualizando…">{page.primary}</Button>} />
        {pathname === "/llm-usage" ? <LLMUsagePage permitted={Boolean(identityData?.me.canViewLLMCosts)} /> : !routePermitted || (pathname === "/prompts" && !promptPermitted) ? <Recovery kind="denied" title="Acesso restrito">Você não tem permissão para acessar este recurso neste escopo.</Recovery> : pathname === "/prompts" ? error ? <Recovery kind="error" title="Não foi possível carregar o prompt" onRetry={() => void load()}>{error}</Recovery> : <AnalysisPromptEditor prompt={analysisPrompt} onSaved={() => { setCollection(undefined); void load(); }} /> : pathname === "/templates" ? <TemplateEditorPage /> : <>
          {pathname !== "/overview" && <div className="collection-toolbar"><Field label={pathname === "/organization" ? "Buscar unidade" : "Buscar nesta coleção"}><Input value={query} onChange={(event) => updateParams({ search: event.target.value || null, after: null })} placeholder={pathname === "/organization" ? "Nome ou código da unidade" : pathname === "/assets" ? "Nome, código, endereço, CEP ou cidade" : "Nome, identificador ou contexto"} /></Field>{query && <Button variant="secondary" onClick={() => updateParams({ search: null, after: null })}>Limpar busca</Button>}</div>}
          <p role="status" className="status-line">{loading ? `Carregando ${page.title.toLowerCase()}…` : status}</p>
          {error ? <Recovery kind="error" title="Não foi possível carregar a coleção" onRetry={() => void load()}>{error}</Recovery> : pathname === "/overview" ? <AdminOverview data={collection} tenantName={tenantName} /> : <div className="admin-focus-workspace"><div className="admin-focus-list"><CollectionView data={visibleCollection} query={query} tenantName={tenantName} focusedRowId={focusedRowId} onFocus={setFocusedRowId} onHistory={showHistory} onNext={(after) => updateParams({ after })} /></div>{visibleCollection?.rows.length ? <AdminFocusDetail data={visibleCollection} focusedRowId={focusedRowId} onHistory={showHistory} /> : null}</div>}
        </>}
      </section>
    </div>
    {detail && <HistoryPanel detail={detail} history={history} error={historyError} onRetry={() => void showHistory(detail)} onNext={() => { if (history?.endCursor) void showHistory(detail, history.endCursor, true); }} onClose={() => { historyRequest.current?.abort(); historyGeneration.current += 1; setDetail(undefined); setHistory(undefined); setHistoryError(undefined); }} onCorrect={setCorrection} onAddressEdit={setAddressEdit} onResend={async (row) => { try { const result = await graphql<G.AdminResendInternalUserInvitationMutation>(G.AdminResendInternalUserInvitationDocument, { input: { membershipId: String(row.membershipId), clientMutationId: `admin-resend-${Date.now()}` } }); if (result.resendInternalUserInvitation.userErrors.length) throw new Error(result.resendInternalUserInvitation.userErrors[0].message); setStatus(`Convite enviado para ${result.resendInternalUserInvitation.membership?.email ?? "o usuário"}.`); await load(); } catch (failure) { setHistoryError(failureText(failure)); } }} />}
    {correction && <AdminCorrectionForm detail={correction} onClose={() => setCorrection(undefined)} onSuccess={() => { setCorrection(undefined); setDetail(undefined); setCollection(undefined); void load(); }} />}
    {addressEdit && <AssetEditForm detail={addressEdit} onClose={() => setAddressEdit(undefined)} onSuccess={() => { setAddressEdit(undefined); setDetail(undefined); setCollection(undefined); void load(); }} />}
    {action && <ActionForm action={action} currentVersion={action === "unit" ? identityData?.tenant?.version : action === "policy" && currentPolicyVersion !== undefined ? Number(currentPolicyVersion) : undefined} onClose={() => setAction(undefined)} onSuccess={() => { setAction(undefined); setCollection(undefined); void load(); }} />}
  </main>;
}

function AdminGate({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return <main className="admin-denial"><aside className="admin-gate-story"><div className="admin-gate-story-brand"><span className="admin-brand-mark" aria-hidden="true">I</span><span>Inspection</span></div><div><h2>Cada vistoria, com clareza.</h2><p>Planeje vistorias e reúna evidências para decidir com segurança.</p></div><small>Administração</small></aside><section className="admin-gate-form"><span className="admin-gate-mobile-brand">Inspection</span><h1>{title}</h1><p>{description}</p>{children}<small>Português (Brasil) · Acesso seguro</small></section></main>;
}

type TemplateRequirementDraft = { key: string; section: string; label: string; instructions: string; minimumCount: number; maximumCount: number; preserved?: Record<string, unknown> };
type TemplateDefinitionDraft = { schemaVersion: number; segmentVersionId: string; participantRoles: string[]; defaultComparisonMode: string; requirements: Array<Record<string, unknown>>; analysisType: "REAL_ESTATE"; policy: { gpsRequired: boolean; geofenceMeters: number; allowGallery: boolean } };

function TemplateEditorPage() {
  const [catalogs, setCatalogs] = useState<G.AdminCatalogsQuery>();
  const [selectedTemplateId, setSelectedTemplateId] = useState("");
  const [isEditingTemplate, setIsEditingTemplate] = useState(false);
  const [segmentVersionId, setSegmentVersionId] = useState("");
  const [key, setKey] = useState(""); const [name, setName] = useState("");
  const [requirements, setRequirements] = useState<TemplateRequirementDraft[]>([{ key: "overview", section: "Geral", label: "Visão geral", instructions: "", minimumCount: 1, maximumCount: 5 }]);
  const [gpsRequired, setGpsRequired] = useState(false); const [allowGallery, setAllowGallery] = useState(true); const [geofenceMeters, setGeofenceMeters] = useState(150);
  const [participantRoles, setParticipantRoles] = useState<string[]>(["TENANT_PARTICIPANT", "PROPERTY_OWNER"]); const [comparisonMode, setComparisonMode] = useState("CHECKLIST_ONLY");
  const [busy, setBusy] = useState(false); const [status, setStatus] = useState("Carregando modelos e segmentos…"); const [error, setError] = useState("");
  const selectedTemplate = catalogs?.templates.nodes.find((template) => template.id === selectedTemplateId);

  const loadCatalogs = useCallback(async () => {
    try {
      const result = await graphql<G.AdminCatalogsQuery>(G.AdminCatalogsDocument, { first: 100, after: null, search: null });
      setCatalogs(result);
      setSelectedTemplateId((current) => current || result.templates.nodes[0]?.id || "");
      const activeSegment = result.segmentDefinitions.nodes.find((segment) => segment.activeVersionId);
      if (activeSegment?.activeVersionId) setSegmentVersionId((current) => current || activeSegment.activeVersionId!);
      setStatus(`${result.templates.nodes.length} modelo(s) carregados.`);
    } catch (failure) { setError(failureText(failure)); setStatus("Não foi possível carregar os modelos."); }
  }, []);
  useEffect(() => { void loadCatalogs(); }, [loadCatalogs]);

  const selectedTemplateKey = selectedTemplate?.key; const selectedTemplateName = selectedTemplate?.name; const activeVersionId = selectedTemplate?.activeVersionId;
  useEffect(() => {
    if (!activeVersionId) return;
    let cancelled = false;
    void graphql<G.AdminTemplateVersionQuery>(G.AdminTemplateVersionDocument, { id: activeVersionId }).then((result) => {
      if (cancelled || !result.templateVersion) return;
      const definition = result.templateVersion.definition as TemplateDefinitionDraft;
      setKey(selectedTemplateKey ?? ""); setName(selectedTemplateName ?? ""); setSegmentVersionId(definition.segmentVersionId);
      if (definition.participantRoles?.length) setParticipantRoles(definition.participantRoles); setComparisonMode(definition.defaultComparisonMode || "CHECKLIST_ONLY");
      setRequirements(definition.requirements.map((requirement) => ({ key: String(requirement.key), section: String(requirement.section), label: String(requirement.label), instructions: String(requirement.instructions ?? ""), minimumCount: Number(requirement.minimumCount ?? 0), maximumCount: Number(requirement.maximumCount ?? 5), preserved: requirement })));
      setGpsRequired(Boolean(definition.policy?.gpsRequired)); setAllowGallery(Boolean(definition.policy?.allowGallery)); setGeofenceMeters(Number(definition.policy?.geofenceMeters ?? 150));
      setError(""); setStatus(`Versão ativa ${result.templateVersion.versionNumber}. Abra o checklist para editar; a publicação criará uma versão nova.`);
    }).catch((failure) => { if (!cancelled) setError(failureText(failure)); });
    return () => { cancelled = true; };
  }, [activeVersionId, selectedTemplateKey, selectedTemplateName]);

  const resetDraft = () => { setIsEditingTemplate(true); setSelectedTemplateId(""); setKey(""); setName(""); setRequirements([{ key: "overview", section: "Geral", label: "Visão geral", instructions: "", minimumCount: 1, maximumCount: 5 }]); setGpsRequired(false); setAllowGallery(true); setGeofenceMeters(150); setParticipantRoles(["TENANT_PARTICIPANT", "PROPERTY_OWNER"]); setComparisonMode("CHECKLIST_ONLY"); setError(""); setStatus("Novo modelo: escolha um segmento e monte o checklist."); };
  const updateRequirement = (index: number, field: keyof TemplateRequirementDraft, value: string) => setRequirements((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, [field]: field === "minimumCount" || field === "maximumCount" ? Number(value) : value } : item));
  const publish = async (event: FormEvent) => {
    event.preventDefault(); if (busy) return;
    setBusy(true); setError("");
    const clientMutationId = `template-publish-${crypto.randomUUID()}`;
    const definition: TemplateDefinitionDraft = {
      schemaVersion: 1, segmentVersionId, participantRoles, defaultComparisonMode: comparisonMode,
      requirements: requirements.map(({ preserved, ...requirement }) => ({ evidenceKind: "PHOTO", required: true, descriptionRequired: false, captureSourcePolicy: "CAMERA_DEFAULT", ...preserved, ...requirement })),
      analysisType: "REAL_ESTATE", policy: { gpsRequired, geofenceMeters, allowGallery },
    };
    try {
      const published = await graphql<G.AdminPublishTemplateVersionMutation>(G.AdminPublishTemplateVersionDocument, { input: { key, name, definition, clientMutationId } });
      const payload = published.publishTemplateVersion;
      if (payload.userErrors.length || !payload.template || !payload.version) throw new Error(payload.userErrors[0]?.message ?? "Não foi possível publicar a versão.");
      const activated = await graphql<G.AdminActivateTemplateVersionMutation>(G.AdminActivateTemplateVersionDocument, { input: { versionId: payload.version.id, expectedVersion: payload.template.version, clientMutationId: `template-activate-${crypto.randomUUID()}` } });
      if (activated.activateTemplateVersion.userErrors.length) throw new Error(activated.activateTemplateVersion.userErrors[0].message);
      setSelectedTemplateId(activated.activateTemplateVersion.template?.id ?? "");
      setStatus(`Modelo publicado e ativado na versão ${activated.activateTemplateVersion.version?.versionNumber}.`);
      await loadCatalogs();
    } catch (failure) { setError(failureText(failure)); }
    finally { setBusy(false); }
  };

  return <div className="admin-template-editor">
    <div className="template-editor-toolbar"><label>Modelo existente<select aria-label="Modelo existente" value={selectedTemplateId} onChange={(event) => { setSelectedTemplateId(event.target.value); setIsEditingTemplate(false); }}><option value="">Selecione um modelo</option>{catalogs?.templates.nodes.map((template) => <option key={template.id} value={template.id}>{template.name} · {template.key}</option>)}</select></label><Button type="button" variant="secondary" onClick={resetDraft}>Novo modelo</Button></div>
    <p role="status">{status}</p>
    <div className="admin-template-workspace">{isEditingTemplate ? <form className="admin-form" onSubmit={publish}>
      <Field label="Chave do modelo" required><Input required value={key} onChange={(event) => setKey(event.target.value)} disabled={Boolean(selectedTemplateId)} /></Field>
      <Field label="Nome do modelo" required><Input required value={name} onChange={(event) => setName(event.target.value)} /></Field>
      <Field label="Segmento do imóvel" required><Select required value={segmentVersionId} onChange={(event) => setSegmentVersionId(event.target.value)}>{catalogs?.segmentDefinitions.nodes.filter((segment) => segment.activeVersionId).map((segment) => <option key={segment.id} value={segment.activeVersionId!}>{segment.name} · {segment.key}</option>)}</Select></Field>
      <fieldset className="template-requirements"><legend>Checklist de captura</legend><p>A comparação e o vínculo com projeto são escolhidos na vistoria, então este modelo define somente os itens de captura.</p>{requirements.map((requirement, index) => <fieldset className="template-requirement" key={`${index}-${requirement.key}`}><legend>Item {index + 1}</legend><Field label="Chave" required><Input required value={requirement.key} onChange={(event) => updateRequirement(index, "key", event.target.value)} /></Field><Field label="Seção" required><Input required value={requirement.section} onChange={(event) => updateRequirement(index, "section", event.target.value)} /></Field><Field label="O que fotografar" required><Input required value={requirement.label} onChange={(event) => updateRequirement(index, "label", event.target.value)} /></Field><Field label="Orientação"><Textarea value={requirement.instructions} onChange={(event) => updateRequirement(index, "instructions", event.target.value)} /></Field><div className="template-count-fields"><Field label="Mínimo de fotos"><Input type="number" min={0} max={200} value={requirement.minimumCount} onChange={(event) => updateRequirement(index, "minimumCount", event.target.value)} /></Field><Field label="Máximo de fotos"><Input type="number" min={1} max={200} value={requirement.maximumCount} onChange={(event) => updateRequirement(index, "maximumCount", event.target.value)} /></Field></div><Button type="button" variant="secondary" disabled={requirements.length <= 1} onClick={() => setRequirements((current) => current.filter((_, itemIndex) => itemIndex !== index))}>Remover item</Button></fieldset>)}<Button type="button" variant="secondary" onClick={() => setRequirements((current) => [...current, { key: `item-${current.length + 1}`, section: "Geral", label: "Novo item", instructions: "", minimumCount: 0, maximumCount: 5 }])}>Adicionar item</Button></fieldset>
      <fieldset className="template-policy"><legend>Captura</legend><Checkbox label="Exigir localização GPS" checked={gpsRequired} onChange={(event) => setGpsRequired(event.target.checked)} /><Checkbox label="Permitir escolher fotos da galeria" checked={allowGallery} onChange={(event) => setAllowGallery(event.target.checked)} /><Field label="Raio permitido em metros"><Input type="number" min={25} max={10000} value={geofenceMeters} onChange={(event) => setGeofenceMeters(Number(event.target.value))} /></Field></fieldset>
      {error && <Alert tone="danger">{error}</Alert>}<Button type="submit" isPending={busy} pendingLabel="Publicando e ativando…" disabled={!catalogs?.segmentDefinitions.nodes.some((segment) => segment.activeVersionId === segmentVersionId)}>Publicar nova versão do modelo</Button>
    </form> : <section className="admin-template-summary"><span className="admin-focus-eyebrow">Modelo selecionado</span><h2>{selectedTemplate?.name ?? "Seus modelos de vistoria"}</h2><dl><div><dt>Nome do modelo</dt><dd>{selectedTemplate?.name ?? "Selecione um modelo abaixo ou crie um novo."}</dd></div><div><dt>Chave</dt><dd>{key || "—"}</dd></div></dl><div className="admin-template-checklist"><div className="admin-template-checklist-heading"><h3>Checklist de captura</h3>{selectedTemplate && <Button type="button" variant="secondary" onClick={() => setIsEditingTemplate(true)}>Editar</Button>}</div><p>{selectedTemplate ? `${requirements.length} item(ns) · ${catalogs?.segmentDefinitions.nodes.find((segment) => segment.activeVersionId === segmentVersionId)?.name ?? "Segmento do imóvel"}` : "O checklist aparece após selecionar um modelo."}</p>{selectedTemplate && <button className="admin-template-open" type="button" onClick={() => setIsEditingTemplate(true)}>Abrir checklist →</button>}</div></section>}<aside className="admin-focus-detail admin-template-detail"><span className="admin-focus-eyebrow">Identificação</span><h2>{selectedTemplate?.name ?? "Novo modelo"}</h2><dl><div><dt>Chave</dt><dd>{key || "A definir"}</dd></div><div><dt>Segmento</dt><dd>{catalogs?.segmentDefinitions.nodes.find((segment) => segment.activeVersionId === segmentVersionId)?.name ?? "A definir"}</dd></div><div><dt>Checklist</dt><dd>{requirements.length} item(ns) de captura</dd></div><div><dt>Versão</dt><dd>{selectedTemplate?.version ?? "Nova"}</dd></div></dl></aside></div>
    <h2>Modelos ativos</h2><ul className="admin-template-list">{catalogs?.templates.nodes.map((template) => <li key={template.id}><div><strong>{template.name}</strong><span>{template.key} · v{template.version} · {template.activeVersionId ? "Ativo" : "Sem versão ativa"}</span></div><Button type="button" variant="secondary" onClick={() => { setSelectedTemplateId(template.id); setIsEditingTemplate(false); }}>Abrir modelo</Button></li>)}</ul>
  </div>;
}

function AnalysisPromptEditor({ prompt, onSaved }: { prompt?: G.AdminAnalysisPromptQuery["analysisPrompt"]; onSaved: () => void }) {
  const [systemPrompt, setSystemPrompt] = useState(prompt?.systemPrompt ?? "");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  useEffect(() => { setSystemPrompt(prompt?.systemPrompt ?? ""); setConfirmed(false); }, [prompt?.systemPrompt]);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!prompt) return;
    setBusy(true); setError(undefined);
    try {
      const result = await graphql<G.AdminUpdateAnalysisPromptMutation>(G.AdminUpdateAnalysisPromptDocument, { input: { analysisType: "REAL_ESTATE", systemPrompt, expectedRevision: prompt.revision, clientMutationId: `admin-prompt-${Date.now()}` } });
      const errors = result.updateAnalysisPrompt.userErrors;
      if (errors.length) throw new Error(errors[0].message);
      onSaved();
    } catch (failure) { setError(failureText(failure)); } finally { setBusy(false); }
  };
  if (!prompt) return <Recovery kind="loading" title="Carregando prompt global">A configuração atual será exibida quando estiver disponível.</Recovery>;
  return <form className="prompt-editor" onSubmit={submit} aria-label="Editar prompt global de análise">
    <div className="prompt-editor-main"><Field label="Instrução do sistema" required><Textarea required minLength={1} maxLength={20000} rows={18} value={systemPrompt} onChange={(event) => setSystemPrompt(event.target.value)} /></Field>
    <p className="prompt-editor-help">{systemPrompt.length}/20000 caracteres. O schema de saída, o alias do modelo e o limiar de confiança são fixos. Cada alteração gera uma nova revisão e um registro de auditoria sem armazenar o conteúdo anterior. As mudanças afetam somente inspeções criadas depois da gravação.</p>
    <Checkbox label="Confirmo a alteração do prompt global." checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} />
    {error && <Alert tone="danger">{error}</Alert>}
    <Button type="submit" isPending={busy} pendingLabel="Salvando…" disabled={!confirmed || systemPrompt === prompt.systemPrompt}>Salvar nova revisão</Button>
    </div><aside className="admin-focus-detail prompt-editor-meta"><span className="admin-focus-eyebrow">Parâmetros</span><h2>Instrução global</h2><dl><div><dt>Tipo</dt><dd>Imóveis</dd></div><div><dt>Modelo</dt><dd>{prompt.modelAlias}</dd></div><div><dt>Confiança mínima</dt><dd>{prompt.minimumConfidenceBps / 100}%</dd></div><div><dt>Revisão</dt><dd>{prompt.revision}</dd></div><div><dt>Digest</dt><dd>{prompt.canonicalDigest}</dd></div><div><dt>Atualizado</dt><dd>{prompt.updatedAt}</dd></div></dl></aside>
  </form>;
}

function CollectionView({ data, query, tenantName, focusedRowId, onFocus, onHistory, onNext }: { data?: Collection; query: string; tenantName: string; focusedRowId?: string; onFocus: (id: string) => void; onHistory: (row: Row) => void; onNext: (cursor: string) => void }) {
  if (!data) return <Recovery kind="loading" title="Carregando registros">O contexto atual será preservado.</Recovery>;
  if (!data.rows.length) return <Recovery kind="empty" title={query ? "Nenhum resultado para os filtros atuais" : `Nenhum registro configurado em ${data.title}.`}>{query ? "Ajuste a busca ou limpe os filtros." : "Use a ação principal para iniciar este recurso."}</Recovery>;
  const activeRowId = data.rows.some((row, index) => String(row.ID ?? index) === focusedRowId) ? focusedRowId : String(data.rows[0].ID ?? 0);
  const titleColumn = ({ Organização: "Unidade", "Identidade e acesso": "Usuário", "Responsáveis pela vistoria": "Responsável pela vistoria", "Configuração de vistorias": "Imóvel", Governança: "Recurso", Auditoria: "Ação" } as Record<string, string>)[data.title] ?? data.columns.find((column) => column !== "ID") ?? "Registro";
  const secondaryColumns = ({ Organização: ["Código"], "Identidade e acesso": ["Perfil de acesso", "Abrangência", "E-mail"], "Responsáveis pela vistoria": ["E-mail", "Confirmação do e-mail"], "Configuração de vistorias": ["Código do imóvel", "Endereço"], Governança: ["Detalhe", "Atualizado"], Auditoria: ["Recurso", "Abrangência"] } as Record<string, string[]>)[data.title] ?? [];
  const stateColumn = data.title === "Auditoria" ? "Resultado" : "Situação";
  const formatCell = (row: Row, column: string) => {
    const value = String(row[column] ?? "—");
    if (column === "Situação") return formatAdminStatus(value);
    if ((column === "Atualizado" || column === "Data/hora") && value !== "—") { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(date); }
    return value;
  };
  const auditDay = (row: Row) => { const date = new Date(String(row["Data/hora"] ?? "")); return Number.isNaN(date.getTime()) ? "Sem data" : new Intl.DateTimeFormat("pt-BR", { dateStyle: "full" }).format(date); };
  return <><ol className="admin-focus-rows" aria-label={`${data.title}: registros`}>
    {data.rows.map((item, index) => {
      const id = String(item.ID ?? index);
      const day = data.title === "Auditoria" ? auditDay(item) : null;
      const previousDay = index > 0 ? auditDay(data.rows[index - 1]) : null;
      const subtitle = secondaryColumns.filter((column) => item[column] != null && String(item[column]) !== "—").map((column) => formatCell(item, column)).join(" · ");
      const date = data.title === "Auditoria" && item["Data/hora"] ? new Date(String(item["Data/hora"])) : null;
      const time = date && !Number.isNaN(date.getTime()) ? new Intl.DateTimeFormat("pt-BR", { hour: "2-digit", minute: "2-digit" }).format(date) : null;
      return <li key={id}>{day !== null && day !== previousDay && <h2 className="admin-focus-day">{day}</h2>}<div className="admin-focus-row" data-focused={id === activeRowId}>
        <div className="admin-focus-row-main"><button className="admin-row-focus admin-row-title-desktop" type="button" aria-pressed={id === activeRowId} onClick={() => onFocus(id)}>{String(item[titleColumn] ?? "Registro")}</button><button className="admin-row-focus admin-row-title-mobile" type="button" onClick={() => onHistory(item)}>{String(item[titleColumn] ?? "Registro")}</button>{subtitle && <span>{subtitle}</span>}</div>
        <div className="admin-focus-row-side">{time && <time dateTime={String(item["Data/hora"])}>{time}</time>}{item[stateColumn] != null && <Status>{formatCell(item, stateColumn)}</Status>}<IconButton label={`Abrir detalhes de ${String(item[titleColumn] ?? "registro")}`} tooltip="Abrir detalhes" icon="eye" onPress={() => onHistory(item)} /></div>
      </div></li>;
    })}
  </ol><div className="admin-collection-foot"><span>{data.rows.length} registros · {data.hasNextPage ? "mais páginas disponíveis" : "fim da consulta"}</span><span>Contexto: {tenantName}</span></div>{data.hasNextPage && data.endCursor && <Pagination page={1} hasNextPage onPrevious={() => undefined} onNext={() => onNext(data.endCursor!)} />}</>;
}

function AdminFocusDetail({ data, focusedRowId, onHistory }: { data: Collection; focusedRowId?: string; onHistory: (row: Row) => void }) {
  const row = data.rows.find((item, index) => String(item.ID ?? index) === focusedRowId) ?? data.rows[0];
  if (!row) return null;
  const visible = data.columns.filter((column) => column !== "ID" && row[column] != null);
  const titleKey = visible.find((column) => ["Unidade", "Usuário", "Responsável pela vistoria", "Imóvel", "Ação", "Recurso", "Contexto", "Tipo"].includes(column)) ?? visible[0] ?? "Registro";
  const displayValue = (column: string) => {
    const value = String(row[column] ?? "—");
    if (column === "Situação") return formatAdminStatus(value);
    if ((column === "Atualizado" || column === "Data/hora") && value !== "—") {
      const date = new Date(value);
      if (!Number.isNaN(date.getTime())) return new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(date);
    }
    return value;
  };
  return <aside className="admin-focus-detail" aria-label="Resumo do registro selecionado"><span className="admin-focus-eyebrow">Registro selecionado</span><h2>{String(row[titleKey] ?? data.title)}</h2><p>{data.responsibility}</p><dl>{visible.filter((column) => column !== titleKey).slice(0, 6).map((column) => <div key={column}><dt>{column}</dt><dd>{displayValue(column)}</dd></div>)}</dl><Button variant="secondary" onClick={() => onHistory(row)}>Ver detalhes e histórico <span aria-hidden="true">→</span></Button></aside>;
}

function AdminOverview({ data, tenantName }: { data?: Collection; tenantName: string }) {
  if (!data) return <Recovery kind="loading" title="Carregando contexto">A configuração da operação será exibida em instantes.</Recovery>;
  return <div className="admin-focus-workspace admin-overview-workspace"><section className="admin-overview-context" aria-label="Seu contexto de trabalho"><span className="admin-focus-eyebrow">Seu contexto de trabalho</span><h2>{tenantName}</h2><dl>{data.rows.map((row, index) => <div key={String(row.Contexto ?? index)}><dt>{String(row.Contexto ?? "Informação")}</dt><dd>{String(row.Valor ?? "—")}</dd></div>)}</dl><div className="admin-overview-links"><Link href="/organization">Organização <span aria-hidden="true">→</span></Link><Link href="/access">Usuários e acessos <span aria-hidden="true">→</span></Link></div></section><aside className="admin-focus-detail"><span className="admin-focus-eyebrow">Selecionado</span><h2>Administração</h2><dl><div><dt>Operação</dt><dd>{tenantName}</dd></div><div><dt>Abrangência</dt><dd>{String(data.rows.find((row) => row.Contexto === "Abrangência efetiva")?.Valor ?? "Imobiliária")}</dd></div></dl><Link className="admin-focus-link" href="/organization">Revisar contexto <span aria-hidden="true">→</span></Link></aside></div>;
}

function HistoryPanel({ detail, history, error, onRetry, onNext, onClose, onCorrect, onAddressEdit, onResend }: { detail: Row; history?: HistoryPage; error?: string; onRetry: () => void; onNext: () => void; onClose: () => void; onCorrect: (row: Row) => void; onAddressEdit: (row: Row) => void; onResend: (row: Row) => void }) {
  const titleColumn = ["Unidade", "Imóvel", "Recurso", "Responsável pela vistoria", "Perfil de acesso", "Ação", "Contexto", "Tipo"].find((key) => detail[key] != null);
  const title = titleColumn ? `Detalhes: ${String(detail[titleColumn])}` : "Detalhe e histórico";
  return <Dialog isOpen onClose={onClose} title={title} size="wide"><section className="detail-panel"><dl>{Object.entries(detail).filter(([key]) => !["inspectionId", "responsibilityVersion", "canCorrectResponsibleEmail", "targetId", "assetId", "businessUnitId", "segmentVersionId", "templateId", "latitudeE6", "longitudeE6", "geofenceMeters", "addressStatus", "addressDetails", "attributes", "policyOverrides", "assignments"].includes(key)).map(([key, value]) => <div key={key}><dt>{key}</dt><dd>{value}</dd></div>)}</dl>{detail.canCorrectResponsibleEmail === "Sim" && detail.inspectionId && <Button variant="secondary" onClick={() => onCorrect(detail)}>Corrigir e reenviar</Button>}{detail.assetId && <Button variant="secondary" onClick={() => onAddressEdit(detail)}>{detail["Situação"] === "DRAFT" ? "Completar cadastro" : "Editar imóvel"}</Button>}{detail.membershipId && ["PENDING", "SENT", "FAILED", "CLAIMED", "OTP_SENT", "VERIFIED"].includes(String(detail.rawInvitationStatus)) && <Button variant="secondary" onClick={() => onResend(detail)}>Reenviar convite</Button>}<h3>Histórico auditável</h3>{error ? <Recovery kind="error" title="Não foi possível carregar o histórico" onRetry={onRetry}>{error}</Recovery> : history ? history.events.length ? <ul>{history.events.map((event) => <li key={event.id}>{new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(new Date(event.occurredAt))} · {event.action} · {event.outcome}{event.reason ? ` · ${event.reason}` : ""}</li>)}</ul> : history.hasNextPage ? <p role="status">Nenhum evento deste recurso nesta página; há mais histórico disponível.</p> : <Recovery kind="empty" title="Nenhum evento encontrado para este recurso.">A coleção não retornou eventos deste recurso.</Recovery> : <p role="status">Carregando histórico…</p>}{history?.hasNextPage && history.endCursor && <Button variant="secondary" onClick={onNext}>Carregar mais histórico</Button>}<Button variant="secondary" onClick={onClose}>Fechar detalhe</Button></section></Dialog>;
}

function AssetEditForm({ detail, onClose, onSuccess }: { detail: Row; onClose: () => void; onSuccess: () => void }) {
  const parse = <T,>(key: string, fallback: T): T => { try { if (!detail[key]) return fallback; const value = JSON.parse(String(detail[key])) as T | null; return value ?? fallback; } catch { return fallback; } };
  const [address, setAddress] = useState<PostalAddress>(() => parse("addressDetails", emptyPostalAddress()));
  const [attributeSchema, setAttributeSchema] = useState<SegmentAttributeSchema>();
  const [attributeValues, setAttributeValues] = useState<Record<string, unknown>>(() => parse("attributes", {}));
  const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const submitting = useRef(false);
  useEffect(() => {
    let cancelled = false;
    void graphql<G.AdminSegmentDefinitionVersionQuery>(G.AdminSegmentDefinitionVersionDocument, { id: String(detail.segmentVersionId) }).then((result) => {
      if (!cancelled) setAttributeSchema(result.segmentDefinitionVersion?.schema as SegmentAttributeSchema | undefined);
    }).catch((failure) => { if (!cancelled) setError(failureText(failure)); });
    return () => { cancelled = true; };
  }, [detail.segmentVersionId]);
  const lookup = async (postalCode: string): Promise<PostalLookup | null> => { const result = await graphql<G.AdminLookupPostalCodeQuery>(G.AdminLookupPostalCodeDocument, { postalCode }); return result.lookupPostalCode.found ? result.lookupPostalCode : null; };
  const submit = async (event: FormEvent) => {
    event.preventDefault(); if (submitting.current) return; submitting.current = true; setBusy(true); setError("");
    try {
      if (!attributeSchema) throw new Error("Não foi possível carregar os atributos exigidos pelo segmento.");
      const result = await graphql<G.AdminUpdateAssetMutation>(G.AdminUpdateAssetDocument, { input: { assetId: String(detail.assetId), expectedVersion: Number(detail.Versão), clientMutationId: `admin-asset-${Date.now()}`, asset: { businessUnitId: String(detail.businessUnitId), segmentVersionId: String(detail.segmentVersionId), templateId: detail.templateId === "—" ? null : String(detail.templateId), name: String(detail.Imóvel), externalKey: String(detail["Código do imóvel"]), address: "", addressDetails: address, latitudeE6: detail.latitudeE6 === "—" ? null : Number(detail.latitudeE6), longitudeE6: detail.longitudeE6 === "—" ? null : Number(detail.longitudeE6), geofenceMeters: Number(detail.geofenceMeters), attributes: normalizeAssetAttributes(attributeSchema, attributeValues), policyOverrides: parse("policyOverrides", {}), assignments: parse<Array<{ participantId: string; role: string }>>("assignments", []).map(({ participantId, role }) => ({ participantId, role })) } } });
      const errors = result.updateAsset.userErrors; if (errors.length) throw new Error(errors[0].message); onSuccess();
    } catch (failure) { setError(failureText(failure)); } finally { submitting.current = false; setBusy(false); }
  };
  return <Dialog isOpen onClose={onClose} title={detail["Situação"] === "DRAFT" ? "Completar cadastro do imóvel" : "Editar imóvel"} size="wide" isDismissable={!busy}><form className="admin-form" onSubmit={submit} aria-label="Cadastro do imóvel"><AddressFieldStyles /><p>Revise os atributos do segmento para ativar um imóvel em rascunho.</p>{detail.addressStatus !== "COMPLETE" && <p>Endereço atual: <strong>{detail.Endereço}</strong></p>}<AddressForm value={address} onChange={setAddress} onLookup={lookup} prefix="admin-asset-address" disabled={busy} />{attributeSchema ? <AssetAttributeFields schema={attributeSchema} values={attributeValues} onChange={(key, value) => setAttributeValues((current) => ({ ...current, [key]: value }))} /> : <p role="status">Carregando atributos do segmento…</p>}{error && <Alert tone="danger">{error}</Alert>}<div className="inspection-inline"><Button type="button" variant="secondary" onClick={onClose} disabled={busy}>Cancelar</Button><Button type="submit" isPending={busy} pendingLabel="Salvando…" disabled={!attributeSchema}>Salvar imóvel</Button></div></form></Dialog>;
}

function AdminCorrectionForm({ detail, onClose, onSuccess }: { detail: Row; onClose: () => void; onSuccess: () => void }) {
  const [email, setEmail] = useState(""); const [confirmation, setConfirmation] = useState(""); const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const submitting = useRef(false);
  const submit = async (event: FormEvent) => { event.preventDefault(); if (submitting.current) return; submitting.current = true; setBusy(true); setError(""); try { const result = await graphql<G.AdminCorrectInspectionResponsibleEmailMutation>(G.AdminCorrectInspectionResponsibleEmailDocument, { input: { inspectionId: String(detail.inspectionId), email, emailConfirmation: confirmation, expectedResponsibilityVersion: Number(detail.responsibilityVersion), clientMutationId: `admin-correct-${Date.now()}` } }); const payload = result.correctInspectionResponsibleEmail; if (payload.userErrors.length) { setError(payload.userErrors[0].message); return; } onSuccess(); } catch (failure) { setError(failureText(failure)); } finally { submitting.current = false; setBusy(false); } };
  return <Dialog isOpen onClose={onClose} title="Corrigir e reenviar" isDismissable={!busy}><form className="admin-form" onSubmit={submit} aria-label="Corrigir e-mail do responsável"><p>O convite atual será invalidado e um novo link será enviado.</p><Field label="Novo e-mail" required><Input required type="email" value={email} onChange={(event) => setEmail(event.target.value)} /></Field><Field label="Confirme o novo e-mail" required error={confirmation && email !== confirmation ? "Os e-mails não coincidem." : undefined}><Input required type="email" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} /></Field>{error && <Alert tone="danger">{error}</Alert>}<div className="inspection-inline"><Button type="button" variant="secondary" onClick={onClose} disabled={busy}>Cancelar</Button><Button type="submit" isPending={busy} pendingLabel="Reenviando…">Corrigir e reenviar</Button></div></form></Dialog>;
}

function ActionForm({ action, currentVersion, onClose, onSuccess }: { action: Action; currentVersion?: number; onClose: () => void; onSuccess: () => void }) {
  const [values, setValues] = useState<Record<string, string>>({}); const [busy, setBusy] = useState(false); const [error, setError] = useState<string>(); const [options, setOptions] = useState<AdminFormOptions>(); const [optionsLoading, setOptionsLoading] = useState(false); const submitting = useRef(false);
  const [address, setAddress] = useState<PostalAddress>(emptyPostalAddress());
  const [attributeSchema, setAttributeSchema] = useState<SegmentAttributeSchema>();
  const [attributeValues, setAttributeValues] = useState<Record<string, unknown>>({});
  useEffect(() => {
    if (action !== "participant" && action !== "asset") return;
    let cancelled = false;
    setOptionsLoading(true);
    void Promise.all([
      graphql<G.AdminOrganizationQuery>(G.AdminOrganizationDocument, { first: 100, after: null }),
      graphql<G.AdminCatalogsQuery>(G.AdminCatalogsDocument, { first: 100, after: null, search: null }),
    ]).then(([organization, catalogs]) => { if (!cancelled) setOptions({ units: organization.businessUnits.nodes.filter((unit) => unit.status === "ACTIVE"), segments: catalogs.segmentDefinitions.nodes.filter((segment) => segment.activeVersionId), templates: catalogs.templates.nodes.filter((template) => template.activeVersionId) }); }).catch((failure) => { if (!cancelled) setError(failureText(failure)); }).finally(() => { if (!cancelled) setOptionsLoading(false); });
    return () => { cancelled = true; };
  }, [action]);
  useEffect(() => {
    if (action !== "asset" || !values.segmentVersionId) { setAttributeSchema(undefined); return; }
    let cancelled = false;
    setAttributeSchema(undefined);
    setAttributeValues({});
    void graphql<G.AdminSegmentDefinitionVersionQuery>(G.AdminSegmentDefinitionVersionDocument, { id: values.segmentVersionId }).then((result) => {
      if (!cancelled) setAttributeSchema(result.segmentDefinitionVersion?.schema as SegmentAttributeSchema | undefined);
    }).catch((failure) => { if (!cancelled) setError(failureText(failure)); });
    return () => { cancelled = true; };
  }, [action, values.segmentVersionId]);
  const setValue = (name: string, value: string) => setValues((current) => ({ ...current, [name]: value }));
  const lookupAddress = async (postalCode: string): Promise<PostalLookup | null> => { const result = await graphql<G.AdminLookupPostalCodeQuery>(G.AdminLookupPostalCodeDocument, { postalCode }); return result.lookupPostalCode.found ? result.lookupPostalCode : null; };
  const submit = async (event: FormEvent) => { event.preventDefault(); if (submitting.current) return; if ((action === "participant" || action === "invite") && values.email !== values.emailConfirmation) { setError("Os e-mails não coincidem."); return; } submitting.current = true; setBusy(true); setError(undefined); const clientMutationId = `admin-${action}-${Date.now()}`; try { let errors: Array<{ message: string }>; switch (action) { case "unit": { if (!currentVersion) throw new Error("A versão atual da imobiliária ainda não foi carregada."); const r = await graphql<G.AdminCreateBusinessUnitMutation>(G.AdminCreateBusinessUnitDocument, { input: { code: values.code, name: values.name, expectedTenantVersion: currentVersion, clientMutationId } }); errors = r.createBusinessUnit.userErrors; break; } case "invite": { const r = await graphql<G.AdminInviteInternalUserMutation>(G.AdminInviteInternalUserDocument, { input: { name: values.name, email: values.email, role: values.role, scopes: [], clientMutationId } }); errors = r.inviteInternalUser.userErrors; break; } case "participant": { const r = await graphql<G.AdminUpsertParticipantMutation>(G.AdminUpsertParticipantDocument, { input: { businessUnitId: values.businessUnitId, name: values.name, segmentRole: values.segmentRole, contacts: [{ channel: "EMAIL", value: values.email }], clientMutationId } }); errors = r.upsertParticipant.userErrors; break; } case "asset": { if (!attributeSchema) throw new Error("Carregue um segmento válido antes de salvar o imóvel."); const r = await graphql<G.AdminRegisterAssetMutation>(G.AdminRegisterAssetDocument, { input: { asset: { businessUnitId: values.businessUnitId, segmentVersionId: values.segmentVersionId, templateId: values.templateId || null, name: values.name, externalKey: values.externalKey, address: "", addressDetails: address, attributes: normalizeAssetAttributes(attributeSchema, attributeValues), assignments: [] }, clientMutationId } }); errors = r.registerAsset.userErrors; break; } default: { if (currentVersion === undefined) throw new Error("A versão atual da política ainda não foi carregada."); const r = await graphql<G.AdminConfigurePublicationPolicyMutation>(G.AdminConfigurePublicationPolicyDocument, { input: { mode: values.mode, expectedVersion: currentVersion, clientMutationId } }); errors = r.configurePublicationPolicy.userErrors; } } if (errors.length) throw new Error(errors[0].message); onSuccess(); } catch (failure) { setError(failureText(failure)); } finally { submitting.current = false; setBusy(false); } };
  const title = action === "unit" ? "Criar unidade" : action === "invite" ? "Convidar usuário" : action === "participant" ? "Criar responsável pela vistoria" : action === "asset" ? "Registrar imóvel" : "Configurar política";
  const roleOptions: Array<[string, string]> = [["TENANT_ADMIN", "Administrador da imobiliária"], ["MANAGER", "Gestor"], ["EMPLOYEE", "Operador"], ["VIEWER", "Visualizador"], ["CUSTOMER_VIEWER", "Visualizador cliente"], ["ORGANIZATION_ADMIN", "Administrador de organização"], ["ACCESS_ADMIN", "Administrador de acessos"], ["PARTICIPATION_ADMIN", "Administrador de participação"], ["INSPECTION_CONFIG_ADMIN", "Administrador de vistorias"], ["GOVERNANCE_ADMIN", "Administrador de governança"], ["AUDITOR", "Auditor"]];
  const toChoice = (value: string, label: string): [string, string] => [value, label];
  const select = (name: string, label: string, choices: Array<[string, string]>, required = true) => <Field key={name} label={label} required={required}><Select required={required} value={values[name] ?? ""} onChange={(event) => { const value = event.target.value; if (action === "asset" && name === "businessUnitId" && values.businessUnitId && values.businessUnitId !== value) setValues((current) => ({ ...current, businessUnitId: value, segmentVersionId: "", templateId: "" })); else { setValue(name, value); if (action === "asset" && name === "segmentVersionId") setValue("templateId", ""); } }}><option value="" disabled={required}>{required ? "Selecione" : "Nenhum"}</option>{choices.map(([value, choiceLabel]) => <option key={value} value={value}>{choiceLabel}</option>)}</Select></Field>;
  const compatibleTemplates = options?.templates.filter((template) => template.segmentVersionId === values.segmentVersionId) ?? [];
  useEffect(() => { if (action === "asset" && values.templateId && !compatibleTemplates.some((template) => template.id === values.templateId)) setValue("templateId", ""); }, [action, compatibleTemplates, values.templateId]);
  const textField = (name: string, label: string) => <Field label={label} required><Input required value={values[name] ?? ""} onChange={(event) => setValue(name, event.target.value)} /></Field>;
  return <Dialog isOpen onClose={onClose} title={title} isDismissable={!busy}><form className="admin-form" onSubmit={submit} aria-label="Operação administrativa"><p>O contexto atual será auditado. Revise a abrangência antes de salvar.</p>{action === "unit" && <>{textField("code", "Código da unidade")}{textField("name", "Nome da unidade")}</>}{action === "invite" && <>{textField("name", "Nome do usuário")}<Field label="E-mail" required><Input type="email" required value={values.email ?? ""} onChange={(event) => setValue("email", event.target.value)} /></Field><Field label="Confirme o e-mail" required error={values.emailConfirmation && values.email !== values.emailConfirmation ? "Os e-mails não coincidem." : undefined}><Input type="email" required value={values.emailConfirmation ?? ""} onChange={(event) => setValue("emailConfirmation", event.target.value)} /></Field>{select("role", "Perfil de acesso", roleOptions)}<p>Enviaremos um convite para confirmar o e-mail e ativar este acesso.</p></>}{action === "participant" && <>{optionsLoading && <p role="status">Carregando unidades…</p>}{options && select("businessUnitId", "Unidade", options.units.map((unit) => toChoice(unit.id, `${unit.name} · ${unit.code}`)))}{textField("name", "Nome do responsável")}<Field label="E-mail" required><Input type="email" required value={values.email ?? ""} onChange={(event) => setValue("email", event.target.value)} /></Field><Field label="Confirme o e-mail" required error={values.emailConfirmation && values.email !== values.emailConfirmation ? "Os e-mails não coincidem." : undefined}><Input type="email" required value={values.emailConfirmation ?? ""} onChange={(event) => setValue("emailConfirmation", event.target.value)} /></Field>{textField("segmentRole", "Função no segmento")}<p>O e-mail só será contatado quando uma vistoria for atribuída a este responsável.</p></>}{action === "asset" && <><AddressFieldStyles />{optionsLoading && <p role="status">Carregando opções de configuração…</p>}{options && <>{select("businessUnitId", "Unidade", options.units.map((unit) => toChoice(unit.id, `${unit.name} · ${unit.code}`)))}{select("segmentVersionId", "Versão do segmento", options.segments.map((segment) => toChoice(segment.activeVersionId!, `${segment.name} · ${segment.key}`)))}{select("templateId", "Modelo de vistoria", compatibleTemplates.map((template) => toChoice(template.id, `${template.name} · ${template.key}`)), false)}</>}{values.segmentVersionId && (attributeSchema ? <AssetAttributeFields schema={attributeSchema} values={attributeValues} onChange={(key, value) => setAttributeValues((current) => ({ ...current, [key]: value }))} /> : <p role="status">Carregando atributos obrigatórios…</p>)}{textField("name", "Nome do imóvel")}{textField("externalKey", "Código do imóvel")}<AddressForm value={address} onChange={setAddress} onLookup={lookupAddress} prefix="admin-new-asset-address" disabled={busy} autoLookup layout="essential-fields" /></>}{action === "policy" && select("mode", "Modo de publicação", [["MANUAL", "Manual"], ["AUTOMATIC", "Automático"]])}{error && <Alert tone="danger">{error}</Alert>}<div className="inspection-inline"><Button type="button" variant="secondary" onClick={onClose} disabled={busy}>Cancelar</Button><Button type="submit" isPending={busy} pendingLabel="Salvando…" disabled={optionsLoading || ((action === "unit" || action === "policy") && currentVersion === undefined) || ((action === "participant" || action === "asset") && !options) || (action === "asset" && !attributeSchema)}>Salvar operação</Button></div></form></Dialog>;
}

function formatAdminStatus(value: string | number | null): string { return presentAdminStatus(value); }

function presentInvitationStatus(value: string): string { if (value === "ACCEPTED") return "Ativo"; if (value === "FAILED") return "Falha no envio"; if (["NONE", ""].includes(value)) return "Não enviado"; return "Pendente"; }
