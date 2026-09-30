"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AdaptiveNavigation, Alert, Button, Checkbox, DataTable, Dialog, Field, Icon, IconButton, Input, PageHeader, Pagination, ProductIdentity, Recovery, Select, Status, Textarea, ThemeSelector, type AdaptiveNavigationItem } from "@inspection/design-system";
import { beginPKCE } from "@/auth/pkce";
import { clearProtectedContext, hasAdminAccess, hasAdminRouteAccess, hasAnalysisPromptAccess, restoreMembershipContext, setIdentity, setMembershipContext, type AdminIdentity } from "@/auth/session";
import { graphql, graphqlIdentity, type GraphQLFailure } from "@/graphql/client";
import * as G from "@/graphql/generated";
import { presentAdminRole, presentAdminScope, presentAdminStatus } from "./presentation";
import { buildGovernanceRows } from "./governance-presentation";
import { isHistoryForResource } from "./history";
import { RequestGuard } from "./request-guard";
import { LLMUsagePage } from "../llm-usage/llm-usage-page";
import { AddressFieldStyles, AddressForm, emptyPostalAddress, type PostalAddress, type PostalLookup } from "@inspection/address";

type Row = Record<string, string | number | null>;
type Collection = { title: string; responsibility: string; columns: string[]; rows: Row[]; hasNextPage: boolean; endCursor: string | null };
type HistoryPage = { events: G.AdminHistoryQuery["auditEvents"]["nodes"]; hasNextPage: boolean; endCursor: string | null };
type MembershipOption = G.AdminIdentityQuery["me"]["memberships"][number];
type Action = "unit" | "invite" | "participant" | "asset" | "policy";
type AdminFormOptions = { units: G.AdminOrganizationQuery["businessUnits"]["nodes"]; segments: G.AdminCatalogsQuery["segmentDefinitions"]["nodes"]; templates: G.AdminCatalogsQuery["templates"]["nodes"] };

const pages: Record<string, { title: string; responsibility: string; primary: string }> = {
  "/overview": { title: "Visão administrativa", responsibility: "Contexto e saúde da configuração da imobiliária", primary: "Revisar contexto" },
  "/organization": { title: "Organização", responsibility: "Unidades e estrutura da sua operação.", primary: "Criar unidade" },
  "/access": { title: "Identidade e acesso", responsibility: "Usuários, convites, papéis e acesso efetivo", primary: "Convidar usuário" },
  "/catalogs": { title: "Responsáveis pela vistoria", responsibility: "Responsáveis, contatos e segmentos", primary: "Criar responsável" },
  "/assets": { title: "Configuração de vistorias", responsibility: "Modelos de vistoria e imóveis", primary: "Registrar imóvel" },
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
    if (!identityData || !hasAdminAccess() || !hasAdminRouteAccess(pathname, identityData.me.roles) || pathname === "/llm-usage") return; request.current?.abort(); const controller = new AbortController(); request.current = controller; const currentGeneration = collectionGuard.begin();
    setLoading(true); setError(undefined); const variables = { first: 25, after: cursor, search: query || null };
    try { let next: Collection;
      switch (pathname) {
        case "/organization": { const r = await graphql<G.AdminOrganizationQuery>(G.AdminOrganizationDocument, variables, controller.signal); next = { ...pages[pathname], columns: ["ID", "Unidade", "Código", "Situação", "Versão"], rows: r.businessUnits.nodes.map((x) => makeRow({ ID: x.id, Unidade: x.name, Código: x.code, Situação: x.status, Versão: x.version })), hasNextPage: r.businessUnits.pageInfo.hasNextPage, endCursor: r.businessUnits.pageInfo.endCursor }; break; }
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
  const policyVersion = collection?.rows.find((row) => row.Recurso === "Política de publicação")?.Versão;
  const currentPolicyVersion = typeof policyVersion === "number" && Number.isSafeInteger(policyVersion) && policyVersion > 0 ? policyVersion : undefined;
  const primaryAction = () => {
    if (pathname === "/overview") return void load();
    if (pathname === "/audit") return exportCsv();
    if (pathname === "/access" && !canInviteInternalUsers) return;
    setAction(pathname === "/organization" ? "unit" : pathname === "/access" ? "invite" : pathname === "/catalogs" ? "participant" : pathname === "/assets" ? "asset" : "policy");
  };
  if (needsBootstrap) return <main className="admin-denial"><ThemeSelector /><Recovery kind={error ? "error" : "unavailable"} title="Primeiro acesso">{error ?? status}</Recovery><Button disabled={bootstrapPending} isPending={bootstrapPending} pendingLabel="Criando…" onClick={() => void bootstrap()}>Criar operação local</Button></main>;
  if (error && !identityData) return <main className="admin-denial"><ThemeSelector /><Recovery kind="denied" title="Administração">{error}</Recovery><Button onClick={() => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", pathname)}>Entrar com conta administrativa</Button><a href={process.env.NEXT_PUBLIC_DASHBOARD_URL ?? "http://localhost:3002"}>Ir para o Painel</a></main>;
  const visibleNavItems = navItems.filter(([, href]) => Boolean(identityData && hasAdminRouteAccess(href, identityData.me.roles)) && (href !== "/prompts" || promptPermitted) && (href !== "/llm-usage" || identityData?.me.canViewLLMCosts));
  const primaryAdminHrefs = ["/overview", "/organization", "/access"];
  const navigationItem = ([label, href]: readonly [string, string]): AdaptiveNavigationItem => ({ href, label, icon: href === "/overview" ? "layout-dashboard" : href === "/organization" ? "building-2" : href === "/access" ? "users" : href === "/catalogs" ? "clipboard-check" : href === "/assets" ? "building-2" : href === "/prompts" ? "sparkles" : href === "/governance" ? "shield-check" : href === "/audit" ? "history" : "chart-no-axes-combined", group: href === "/governance" || href === "/audit" ? "Governança" : href === "/prompts" || href === "/llm-usage" ? "Análise" : "Cadastros" });
  const primaryAdminItems = visibleNavItems.filter(([, href]) => primaryAdminHrefs.includes(href)).map(navigationItem);
  const secondaryAdminItems = visibleNavItems.filter(([, href]) => !primaryAdminHrefs.includes(href)).map(navigationItem);
  secondaryAdminItems.push({ href: process.env.NEXT_PUBLIC_DASHBOARD_URL ?? "http://localhost:3002", label: "Abrir Painel", icon: "arrow-up-right" });
  return <main className={`admin-shell${pathname === "/overview" ? " admin-shell--overview" : ""}`}>
    <a className="skip-link" href="#admin-content">Pular para o conteúdo</a>
    <header className="admin-header">
      <ProductIdentity product="Admin" context={tenantName} />
      <div className="admin-header-context">{membershipOptions.length > 1 && <Field label="Imobiliária"><Select aria-label="Imobiliária ativa" value={activeMembership?.id ?? ""} onChange={(event) => void selectMembership(event.target.value)}>{membershipOptions.map((x) => <option key={x.id} value={x.id}>{presentAdminRole(x.role)} · {x.tenantId}</option>)}</Select></Field>}<ThemeSelector /><span className="admin-tenant-name">{tenantName}</span><span className="admin-role">{identityData?.me.roles.map(presentAdminRole).join(", ") || "Acesso pendente"}</span><span className="admin-avatar" aria-label={`Contexto ${tenantName}`}>{initials(tenantName)}</span></div>
    </header>
    <div className="admin-layout">
      <AdaptiveNavigation label="Navegação administrativa" activeHref={pathname} primaryItems={primaryAdminItems} secondaryItems={secondaryAdminItems} renderLink={(item, className, onNavigate) => item.href.startsWith("/") ? <Link key={item.href} aria-current={pathname === item.href ? "page" : undefined} className={className} href={item.href} onClick={onNavigate} prefetch={false}><span className="inspection-adaptive-navigation__icon"><Icon name={item.icon} size={22} /></span><span>{item.label}</span></Link> : <a key={item.href} className={className} href={item.href} onClick={onNavigate}><span className="inspection-adaptive-navigation__icon"><Icon name={item.icon} size={22} /></span><span>{item.label}</span></a>} />
      <section id="admin-content" className="admin-content" aria-labelledby="page-title">
        <PageHeader id="page-title" emphasis={pathname === "/overview" ? "brand" : "plain"} breadcrumbs={<><Link href="/overview">Administração</Link><span aria-hidden="true">/</span><span>{page.title}</span></>} title={section ?? page.title} description={page.responsibility} descriptionMode="disclosure" actions={pathname === "/llm-usage" || (pathname === "/access" && !canInviteInternalUsers) ? null : <Button disabled={!routePermitted || loading || pathname === "/prompts" || (pathname === "/audit" && !collection)} onClick={primaryAction} isPending={loading && pathname === "/overview"} pendingLabel="Atualizando…">{page.primary}</Button>} />
        {pathname === "/llm-usage" ? <LLMUsagePage permitted={Boolean(identityData?.me.canViewLLMCosts)} /> : !routePermitted || (pathname === "/prompts" && !promptPermitted) ? <Recovery kind="denied" title="Acesso restrito">Você não tem permissão para acessar este recurso neste escopo.</Recovery> : pathname === "/prompts" ? error ? <Recovery kind="error" title="Não foi possível carregar o prompt" onRetry={() => void load()}>{error}</Recovery> : <AnalysisPromptEditor prompt={analysisPrompt} onSaved={() => { setCollection(undefined); void load(); }} /> : <>
          <div className="collection-toolbar"><Field label={pathname === "/organization" ? "Buscar unidade" : "Buscar nesta coleção"}><Input value={query} onChange={(event) => updateParams({ search: event.target.value || null, after: null })} placeholder={pathname === "/organization" ? "Nome ou código da unidade" : pathname === "/assets" ? "Nome, código, endereço, CEP ou cidade" : "Nome, identificador ou contexto"} /></Field>{query && <Button variant="secondary" onClick={() => updateParams({ search: null, after: null })}>Limpar busca</Button>}</div>
          <p role="status" className="status-line">{loading ? `Carregando ${page.title.toLowerCase()}…` : status}</p>
          {error ? <Recovery kind="error" title="Não foi possível carregar a coleção" onRetry={() => void load()}>{error}</Recovery> : <CollectionView data={collection} query={query} compact={pathname === "/organization"} tenantName={tenantName} onHistory={showHistory} onNext={(after) => updateParams({ after })} />}
        </>}
      </section>
    </div>
    {detail && <HistoryPanel detail={detail} history={history} error={historyError} onRetry={() => void showHistory(detail)} onNext={() => { if (history?.endCursor) void showHistory(detail, history.endCursor, true); }} onClose={() => { historyRequest.current?.abort(); historyGeneration.current += 1; setDetail(undefined); setHistory(undefined); setHistoryError(undefined); }} onCorrect={setCorrection} onAddressEdit={setAddressEdit} onResend={async (row) => { try { const result = await graphql<G.AdminResendInternalUserInvitationMutation>(G.AdminResendInternalUserInvitationDocument, { input: { membershipId: String(row.membershipId), clientMutationId: `admin-resend-${Date.now()}` } }); if (result.resendInternalUserInvitation.userErrors.length) throw new Error(result.resendInternalUserInvitation.userErrors[0].message); setStatus(`Convite enviado para ${result.resendInternalUserInvitation.membership?.email ?? "o usuário"}.`); await load(); } catch (failure) { setHistoryError(failureText(failure)); } }} />}
    {correction && <AdminCorrectionForm detail={correction} onClose={() => setCorrection(undefined)} onSuccess={() => { setCorrection(undefined); setDetail(undefined); setCollection(undefined); void load(); }} />}
    {addressEdit && <AssetAddressForm detail={addressEdit} onClose={() => setAddressEdit(undefined)} onSuccess={() => { setAddressEdit(undefined); setDetail(undefined); setCollection(undefined); void load(); }} />}
    {action && <ActionForm action={action} currentVersion={action === "unit" ? identityData?.tenant?.version : action === "policy" && currentPolicyVersion !== undefined ? Number(currentPolicyVersion) : undefined} onClose={() => setAction(undefined)} onSuccess={() => { setAction(undefined); setCollection(undefined); void load(); }} />}
  </main>;
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
    <div className="prompt-editor-meta"><span>Tipo: Imóveis</span><span>Modelo fixo: {prompt.modelAlias}</span><span>Confiança mínima: {prompt.minimumConfidenceBps / 100}%</span><span>Revisão: {prompt.revision}</span><span>Digest: {prompt.canonicalDigest}</span><span>Atualizado: {prompt.updatedAt}</span></div>
    <Field label="Instrução do sistema" required><Textarea required minLength={1} maxLength={20000} rows={18} value={systemPrompt} onChange={(event) => setSystemPrompt(event.target.value)} /></Field>
    <p className="prompt-editor-help">{systemPrompt.length}/20000 caracteres. O schema de saída, o alias do modelo e o limiar de confiança são fixos. Cada alteração gera uma nova revisão e um registro de auditoria sem armazenar o conteúdo anterior. As mudanças afetam somente inspeções criadas depois da gravação.</p>
    <Checkbox label="Confirmo a alteração do prompt global." checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} />
    {error && <Alert tone="danger">{error}</Alert>}
    <Button type="submit" isPending={busy} pendingLabel="Salvando…" disabled={!confirmed || systemPrompt === prompt.systemPrompt}>Salvar nova revisão</Button>
  </form>;
}

function CollectionView({ data, query, compact, tenantName, onHistory, onNext }: { data?: Collection; query: string; compact: boolean; tenantName: string; onHistory: (row: Row) => void; onNext: (cursor: string) => void }) {
  if (!data) return <Recovery kind="loading" title="Carregando registros">O contexto atual será preservado.</Recovery>;
  if (!data.rows.length) return <Recovery kind="empty" title={query ? "Nenhum resultado para os filtros atuais" : `Nenhum registro configurado em ${data.title}.`}>{query ? "Os filtros foram preservados. Ajuste a busca ou limpe os filtros." : "Use a ação principal para iniciar este recurso."}</Recovery>;
  const columns = compact ? ["Unidade", "Código", "Situação"] : data.columns.filter((column) => column !== "ID");
  const headingColumn = (data.title === "Auditoria" ? "Ação" : data.title === "Prompts de análise" ? "Tipo" : undefined) ?? columns.find((column) => column === "Unidade" || column === "Imóvel") ?? columns[0] ?? "Registro";
  const mobileColumns: Record<string, string[]> = {
    "Visão administrativa": ["Valor"], Organização: ["Código", "Situação"], "Identidade e acesso": ["Abrangência", "Situação"],
    "Responsáveis pela vistoria": ["Situação", "Contatos"], "Configuração de vistorias": ["Código do imóvel", "Situação"],
    "Prompts de análise": ["Modelo", "Revisão"], Governança: ["Situação", "Atualizado"], Auditoria: ["Ação", "Resultado", "Data/hora"],
  };
  const summaryColumns = (mobileColumns[data.title] ?? ["Situação"]).filter((column) => columns.includes(column));
  const renderValue = (item: Row, column: string) => {
    const value = String(item[column] ?? "—");
    if (column === "Situação") return <Status>{formatAdminStatus(value)}</Status>;
    if (/^(Atualizado|Data\/hora)$/.test(column) && value !== "—") {
      const date = new Date(value);
      return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(date);
    }
    const abbreviated = /^(ID|Digest|Vistoria)$/.test(column) && value.length > 30 ? `${value.slice(0, 12)}…${value.slice(-8)}` : value;
    return <span className="inspection-compact-text" title={value}>{abbreviated}</span>;
  };
  return <><DataTable density="compact" caption={`${data.title}: coleção administrativa`} mobileLabel={data.title} columns={[...columns.map((column) => ({ id: column, label: column, align: ["Versão", "Contatos"].includes(column) ? "end" as const : "start" as const })), { id: "action", label: "Ação", align: "center", action: true }]}>{data.rows.map((item, index) => <tr key={`${item.ID ?? index}`}>{columns.map((column) => <td key={column} data-label={column} data-align={["Versão", "Contatos"].includes(column) ? "end" : undefined}>{column === headingColumn ? <strong>{renderValue(item, column)}</strong> : renderValue(item, column)}</td>)}<td data-label="Ação" data-align="center" data-action><IconButton label={`Abrir detalhes de ${String(item[headingColumn] ?? "registro")}`} tooltip="Abrir detalhes" icon="eye" onPress={() => onHistory(item)} /></td></tr>)}</DataTable><ul className="admin-mobile-collection" aria-label={`${data.title}: registros`}>{data.rows.map((item, index) => <li key={`${item.ID ?? index}`}><article><div className="admin-mobile-row"><h2>{String(item[headingColumn] ?? "Registro")}</h2><IconButton label={`Abrir detalhes de ${String(item[headingColumn] ?? "registro")}`} tooltip="Abrir detalhes" icon="eye" onPress={() => onHistory(item)} /></div><dl>{summaryColumns.map((column) => <div key={column}><dt>{column}</dt><dd>{renderValue(item, column)}</dd></div>)}</dl></article></li>)}</ul><div className="admin-collection-foot"><span>{compact ? "Nome em destaque. Identificadores no detalhe." : `${data.rows.length} registros · ${data.hasNextPage ? "mais páginas disponíveis" : "fim da consulta"}`}</span><span>Contexto: {tenantName}</span></div>{data.hasNextPage && data.endCursor && <Pagination page={1} hasNextPage onPrevious={() => undefined} onNext={() => onNext(data.endCursor!)} />}</>;
}

function HistoryPanel({ detail, history, error, onRetry, onNext, onClose, onCorrect, onAddressEdit, onResend }: { detail: Row; history?: HistoryPage; error?: string; onRetry: () => void; onNext: () => void; onClose: () => void; onCorrect: (row: Row) => void; onAddressEdit: (row: Row) => void; onResend: (row: Row) => void }) {
  const titleColumn = ["Unidade", "Imóvel", "Recurso", "Responsável pela vistoria", "Perfil de acesso", "Ação", "Contexto", "Tipo"].find((key) => detail[key] != null);
  const title = titleColumn ? `Detalhes: ${String(detail[titleColumn])}` : "Detalhe e histórico";
  return <Dialog isOpen onClose={onClose} title={title} size="wide"><section className="detail-panel"><dl>{Object.entries(detail).filter(([key]) => !["inspectionId", "responsibilityVersion", "canCorrectResponsibleEmail", "targetId", "assetId", "businessUnitId", "segmentVersionId", "templateId", "latitudeE6", "longitudeE6", "geofenceMeters", "addressStatus", "addressDetails", "attributes", "policyOverrides", "assignments"].includes(key)).map(([key, value]) => <div key={key}><dt>{key}</dt><dd>{value}</dd></div>)}</dl>{detail.canCorrectResponsibleEmail === "Sim" && detail.inspectionId && <Button variant="secondary" onClick={() => onCorrect(detail)}>Corrigir e reenviar</Button>}{detail.assetId && <Button variant="secondary" onClick={() => onAddressEdit(detail)}>{detail.addressStatus === "COMPLETE" ? "Editar endereço" : "Completar endereço"}</Button>}{detail.membershipId && ["PENDING", "SENT", "FAILED", "CLAIMED", "OTP_SENT", "VERIFIED"].includes(String(detail.rawInvitationStatus)) && <Button variant="secondary" onClick={() => onResend(detail)}>Reenviar convite</Button>}<h3>Histórico auditável</h3>{error ? <Recovery kind="error" title="Não foi possível carregar o histórico" onRetry={onRetry}>{error}</Recovery> : history ? history.events.length ? <ul>{history.events.map((event) => <li key={event.id}>{new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(new Date(event.occurredAt))} · {event.action} · {event.outcome}{event.reason ? ` · ${event.reason}` : ""}</li>)}</ul> : history.hasNextPage ? <p role="status">Nenhum evento deste recurso nesta página; há mais histórico disponível.</p> : <Recovery kind="empty" title="Nenhum evento encontrado para este recurso.">A coleção não retornou eventos deste recurso.</Recovery> : <p role="status">Carregando histórico…</p>}{history?.hasNextPage && history.endCursor && <Button variant="secondary" onClick={onNext}>Carregar mais histórico</Button>}<Button variant="secondary" onClick={onClose}>Fechar detalhe</Button></section></Dialog>;
}

function AssetAddressForm({ detail, onClose, onSuccess }: { detail: Row; onClose: () => void; onSuccess: () => void }) {
  const parse = <T,>(key: string, fallback: T): T => { try { if (!detail[key]) return fallback; const value = JSON.parse(String(detail[key])) as T | null; return value ?? fallback; } catch { return fallback; } };
  const [address, setAddress] = useState<PostalAddress>(() => parse("addressDetails", emptyPostalAddress()));
  const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const submitting = useRef(false);
  const lookup = async (postalCode: string): Promise<PostalLookup | null> => { const result = await graphql<G.AdminLookupPostalCodeQuery>(G.AdminLookupPostalCodeDocument, { postalCode }); return result.lookupPostalCode.found ? result.lookupPostalCode : null; };
  const submit = async (event: FormEvent) => {
    event.preventDefault(); if (submitting.current) return; submitting.current = true; setBusy(true); setError("");
    try {
      const result = await graphql<G.AdminUpdateAssetMutation>(G.AdminUpdateAssetDocument, { input: { assetId: String(detail.assetId), expectedVersion: Number(detail.Versão), clientMutationId: `admin-address-${Date.now()}`, asset: { businessUnitId: String(detail.businessUnitId), segmentVersionId: String(detail.segmentVersionId), templateId: detail.templateId === "—" ? null : String(detail.templateId), name: String(detail.Imóvel), externalKey: String(detail["Código do imóvel"]), address: "", addressDetails: address, latitudeE6: detail.latitudeE6 === "—" ? null : Number(detail.latitudeE6), longitudeE6: detail.longitudeE6 === "—" ? null : Number(detail.longitudeE6), geofenceMeters: Number(detail.geofenceMeters), attributes: parse("attributes", {}), policyOverrides: parse("policyOverrides", {}), assignments: parse<Array<{ participantId: string; role: string }>>("assignments", []).map(({ participantId, role }) => ({ participantId, role })) } } });
      const errors = result.updateAsset.userErrors; if (errors.length) throw new Error(errors[0].message); onSuccess();
    } catch (failure) { setError(failureText(failure)); } finally { submitting.current = false; setBusy(false); }
  };
  return <Dialog isOpen onClose={onClose} title={detail.addressStatus === "COMPLETE" ? "Editar endereço do imóvel" : "Completar endereço do imóvel"} size="wide" isDismissable={!busy}><form className="admin-form" onSubmit={submit} aria-label="Endereço do imóvel"><AddressFieldStyles /><p>Os demais dados do imóvel serão preservados.</p>{detail.addressStatus !== "COMPLETE" && <p>Endereço atual: <strong>{detail.Endereço}</strong></p>}<AddressForm value={address} onChange={setAddress} onLookup={lookup} prefix="admin-asset-address" disabled={busy} />{error && <Alert tone="danger">{error}</Alert>}<div className="inspection-inline"><Button type="button" variant="secondary" onClick={onClose} disabled={busy}>Cancelar</Button><Button type="submit" isPending={busy} pendingLabel="Salvando…">Salvar endereço</Button></div></form></Dialog>;
}

function AdminCorrectionForm({ detail, onClose, onSuccess }: { detail: Row; onClose: () => void; onSuccess: () => void }) {
  const [email, setEmail] = useState(""); const [confirmation, setConfirmation] = useState(""); const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const submitting = useRef(false);
  const submit = async (event: FormEvent) => { event.preventDefault(); if (submitting.current) return; submitting.current = true; setBusy(true); setError(""); try { const result = await graphql<G.AdminCorrectInspectionResponsibleEmailMutation>(G.AdminCorrectInspectionResponsibleEmailDocument, { input: { inspectionId: String(detail.inspectionId), email, emailConfirmation: confirmation, expectedResponsibilityVersion: Number(detail.responsibilityVersion), clientMutationId: `admin-correct-${Date.now()}` } }); const payload = result.correctInspectionResponsibleEmail; if (payload.userErrors.length) { setError(payload.userErrors[0].message); return; } onSuccess(); } catch (failure) { setError(failureText(failure)); } finally { submitting.current = false; setBusy(false); } };
  return <Dialog isOpen onClose={onClose} title="Corrigir e reenviar" isDismissable={!busy}><form className="admin-form" onSubmit={submit} aria-label="Corrigir e-mail do responsável"><p>O convite atual será invalidado e um novo link será enviado.</p><Field label="Novo e-mail" required><Input required type="email" value={email} onChange={(event) => setEmail(event.target.value)} /></Field><Field label="Confirme o novo e-mail" required error={confirmation && email !== confirmation ? "Os e-mails não coincidem." : undefined}><Input required type="email" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} /></Field>{error && <Alert tone="danger">{error}</Alert>}<div className="inspection-inline"><Button type="button" variant="secondary" onClick={onClose} disabled={busy}>Cancelar</Button><Button type="submit" isPending={busy} pendingLabel="Reenviando…">Corrigir e reenviar</Button></div></form></Dialog>;
}

function ActionForm({ action, currentVersion, onClose, onSuccess }: { action: Action; currentVersion?: number; onClose: () => void; onSuccess: () => void }) {
  const [values, setValues] = useState<Record<string, string>>({}); const [busy, setBusy] = useState(false); const [error, setError] = useState<string>(); const [options, setOptions] = useState<AdminFormOptions>(); const [optionsLoading, setOptionsLoading] = useState(false); const submitting = useRef(false);
  const [address, setAddress] = useState<PostalAddress>(emptyPostalAddress());
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
  const setValue = (name: string, value: string) => setValues((current) => ({ ...current, [name]: value }));
  const lookupAddress = async (postalCode: string): Promise<PostalLookup | null> => { const result = await graphql<G.AdminLookupPostalCodeQuery>(G.AdminLookupPostalCodeDocument, { postalCode }); return result.lookupPostalCode.found ? result.lookupPostalCode : null; };
  const submit = async (event: FormEvent) => { event.preventDefault(); if (submitting.current) return; if ((action === "participant" || action === "invite") && values.email !== values.emailConfirmation) { setError("Os e-mails não coincidem."); return; } submitting.current = true; setBusy(true); setError(undefined); const clientMutationId = `admin-${action}-${Date.now()}`; try { let errors: Array<{ message: string }>; switch (action) { case "unit": { if (!currentVersion) throw new Error("A versão atual da imobiliária ainda não foi carregada."); const r = await graphql<G.AdminCreateBusinessUnitMutation>(G.AdminCreateBusinessUnitDocument, { input: { code: values.code, name: values.name, expectedTenantVersion: currentVersion, clientMutationId } }); errors = r.createBusinessUnit.userErrors; break; } case "invite": { const r = await graphql<G.AdminInviteInternalUserMutation>(G.AdminInviteInternalUserDocument, { input: { name: values.name, email: values.email, role: values.role, scopes: [], clientMutationId } }); errors = r.inviteInternalUser.userErrors; break; } case "participant": { const r = await graphql<G.AdminUpsertParticipantMutation>(G.AdminUpsertParticipantDocument, { input: { businessUnitId: values.businessUnitId, name: values.name, segmentRole: values.segmentRole, contacts: [{ channel: "EMAIL", value: values.email }], clientMutationId } }); errors = r.upsertParticipant.userErrors; break; } case "asset": { const r = await graphql<G.AdminRegisterAssetMutation>(G.AdminRegisterAssetDocument, { input: { asset: { businessUnitId: values.businessUnitId, segmentVersionId: values.segmentVersionId, templateId: values.templateId || null, name: values.name, externalKey: values.externalKey, address: "", addressDetails: address, attributes: {}, assignments: [] }, clientMutationId } }); errors = r.registerAsset.userErrors; break; } default: { if (currentVersion === undefined) throw new Error("A versão atual da política ainda não foi carregada."); const r = await graphql<G.AdminConfigurePublicationPolicyMutation>(G.AdminConfigurePublicationPolicyDocument, { input: { mode: values.mode, expectedVersion: currentVersion, clientMutationId } }); errors = r.configurePublicationPolicy.userErrors; } } if (errors.length) throw new Error(errors[0].message); onSuccess(); } catch (failure) { setError(failureText(failure)); } finally { submitting.current = false; setBusy(false); } };
  const title = action === "unit" ? "Criar unidade" : action === "invite" ? "Convidar usuário" : action === "participant" ? "Criar responsável pela vistoria" : action === "asset" ? "Registrar imóvel" : "Configurar política";
  const roleOptions: Array<[string, string]> = [["TENANT_ADMIN", "Administrador da imobiliária"], ["MANAGER", "Gestor"], ["EMPLOYEE", "Operador"], ["VIEWER", "Visualizador"], ["CUSTOMER_VIEWER", "Visualizador cliente"], ["ORGANIZATION_ADMIN", "Administrador de organização"], ["ACCESS_ADMIN", "Administrador de acessos"], ["PARTICIPATION_ADMIN", "Administrador de participação"], ["INSPECTION_CONFIG_ADMIN", "Administrador de vistorias"], ["GOVERNANCE_ADMIN", "Administrador de governança"], ["AUDITOR", "Auditor"]];
  const toChoice = (value: string, label: string): [string, string] => [value, label];
  const select = (name: string, label: string, choices: Array<[string, string]>, required = true) => <Field key={name} label={label} required={required}><Select required={required} value={values[name] ?? ""} onChange={(event) => { const value = event.target.value; if (action === "asset" && name === "businessUnitId" && values.businessUnitId && values.businessUnitId !== value) setValues((current) => ({ ...current, businessUnitId: value, segmentVersionId: "", templateId: "" })); else setValue(name, value); }}><option value="" disabled={required}>{required ? "Selecione" : "Nenhum"}</option>{choices.map(([value, choiceLabel]) => <option key={value} value={value}>{choiceLabel}</option>)}</Select></Field>;
  const compatibleTemplates = options?.templates.filter((template) => template.segmentVersionId === values.segmentVersionId) ?? [];
  useEffect(() => { if (action === "asset" && values.templateId && !compatibleTemplates.some((template) => template.id === values.templateId)) setValue("templateId", ""); }, [action, compatibleTemplates, values.templateId]);
  const textField = (name: string, label: string) => <Field label={label} required><Input required value={values[name] ?? ""} onChange={(event) => setValue(name, event.target.value)} /></Field>;
  return <Dialog isOpen onClose={onClose} title={title} isDismissable={!busy}><form className="admin-form" onSubmit={submit} aria-label="Operação administrativa"><p>O contexto atual será auditado. Revise a abrangência antes de salvar.</p>{action === "unit" && <>{textField("code", "Código da unidade")}{textField("name", "Nome da unidade")}</>}{action === "invite" && <>{textField("name", "Nome do usuário")}<Field label="E-mail" required><Input type="email" required value={values.email ?? ""} onChange={(event) => setValue("email", event.target.value)} /></Field><Field label="Confirme o e-mail" required error={values.emailConfirmation && values.email !== values.emailConfirmation ? "Os e-mails não coincidem." : undefined}><Input type="email" required value={values.emailConfirmation ?? ""} onChange={(event) => setValue("emailConfirmation", event.target.value)} /></Field>{select("role", "Perfil de acesso", roleOptions)}<p>Enviaremos um convite para confirmar o e-mail e ativar este acesso.</p></>}{action === "participant" && <>{optionsLoading && <p role="status">Carregando unidades…</p>}{options && select("businessUnitId", "Unidade", options.units.map((unit) => toChoice(unit.id, `${unit.name} · ${unit.code}`)))}{textField("name", "Nome do responsável")}{<Field label="E-mail" required><Input type="email" required value={values.email ?? ""} onChange={(event) => setValue("email", event.target.value)} /></Field>}<Field label="Confirme o e-mail" required error={values.emailConfirmation && values.email !== values.emailConfirmation ? "Os e-mails não coincidem." : undefined}><Input type="email" required value={values.emailConfirmation ?? ""} onChange={(event) => setValue("emailConfirmation", event.target.value)} /></Field>{textField("segmentRole", "Função no segmento")}<p>O e-mail só será contatado quando uma vistoria for atribuída a este responsável.</p></>}{action === "asset" && <><AddressFieldStyles />{optionsLoading && <p role="status">Carregando opções de configuração…</p>}{options && <>{select("businessUnitId", "Unidade", options.units.map((unit) => toChoice(unit.id, `${unit.name} · ${unit.code}`)))}{select("segmentVersionId", "Versão do segmento", options.segments.map((segment) => toChoice(segment.activeVersionId!, `${segment.name} · ${segment.key}`)))}{select("templateId", "Modelo de vistoria", compatibleTemplates.map((template) => toChoice(template.id, `${template.name} · ${template.key}`)), false)}</>}{textField("name", "Nome do imóvel")}{textField("externalKey", "Código do imóvel")}<AddressForm value={address} onChange={setAddress} onLookup={lookupAddress} prefix="admin-new-asset-address" disabled={busy} /></>}{action === "policy" && select("mode", "Modo de publicação", [["MANUAL", "Manual"], ["AUTOMATIC", "Automático"]])}{error && <Alert tone="danger">{error}</Alert>}<div className="inspection-inline"><Button type="button" variant="secondary" onClick={onClose} disabled={busy}>Cancelar</Button><Button type="submit" isPending={busy} pendingLabel="Salvando…" disabled={optionsLoading || ((action === "unit" || action === "policy") && currentVersion === undefined) || ((action === "participant" || action === "asset") && !options)}>Salvar operação</Button></div></form></Dialog>;
}

function initials(name: string): string {
  const value = name.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part[0]).join("").toUpperCase();
  return value || "OP";
}

function formatAdminStatus(value: string | number | null): string { return presentAdminStatus(value); }

function presentInvitationStatus(value: string): string { if (value === "ACCEPTED") return "Ativo"; if (value === "FAILED") return "Falha no envio"; if (["NONE", ""].includes(value)) return "Não enviado"; return "Pendente"; }
