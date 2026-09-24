"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { FormEvent, ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { beginPKCE } from "@/auth/pkce";
import { clearProtectedContext, hasAdminAccess, hasAnalysisPromptAccess, restoreMembershipContext, setIdentity, setMembershipContext, type AdminIdentity } from "@/auth/session";
import { graphql, graphqlIdentity, type GraphQLFailure } from "@/graphql/client";
import * as G from "@/graphql/generated";
import { presentAdminRole, presentAdminScope, presentAdminStatus, presentPublicationMode } from "./presentation";
import { LLMUsagePage } from "../llm-usage/llm-usage-page";

type Row = Record<string, string | number | null>;
type Collection = { title: string; responsibility: string; columns: string[]; rows: Row[]; hasNextPage: boolean; endCursor: string | null };
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
  const [loading, setLoading] = useState(false); const [error, setError] = useState<string>(); const [needsBootstrap, setNeedsBootstrap] = useState(false);
  const [analysisPrompt, setAnalysisPrompt] = useState<G.AdminAnalysisPromptQuery["analysisPrompt"]>();
  const [action, setAction] = useState<Action>(); const [detail, setDetail] = useState<Row>(); const [history, setHistory] = useState<G.AdminHistoryQuery["auditEvents"]["nodes"]>(); const [correction, setCorrection] = useState<Row>();
  const request = useRef<AbortController | undefined>(undefined); const generation = useRef(0); const query = params.get("search") ?? ""; const cursor = params.get("after");
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
    if (!identityData || !hasAdminAccess() || pathname === "/llm-usage" || (pathname === "/prompts" && !identityData.me.roles.some((role) => role === "TENANT_ADMIN" || role === "INSPECTION_CONFIG_ADMIN"))) return; request.current?.abort(); const controller = new AbortController(); request.current = controller; const currentGeneration = ++generation.current;
    setLoading(true); setError(undefined); const variables = { first: 25, after: cursor, search: query || null };
    try { let next: Collection;
      switch (pathname) {
        case "/organization": { const r = await graphql<G.AdminOrganizationQuery>(G.AdminOrganizationDocument, variables, controller.signal); next = { ...pages[pathname], columns: ["ID", "Unidade", "Código", "Situação", "Versão"], rows: r.businessUnits.nodes.map((x) => makeRow({ ID: x.id, Unidade: x.name, Código: x.code, Situação: x.status, Versão: x.version })), hasNextPage: r.businessUnits.pageInfo.hasNextPage, endCursor: r.businessUnits.pageInfo.endCursor }; break; }
        case "/access": { const r = await graphql<G.AdminAccessQuery>(G.AdminAccessDocument, variables, controller.signal); next = { ...pages[pathname], columns: ["ID", "Perfil de acesso", "Abrangência", "Situação", "Versão"], rows: r.memberships.nodes.map((x) => makeRow({ ID: x.id, "Perfil de acesso": presentAdminRole(x.role), Abrangência: x.scopes.map((s) => presentAdminScope(s.kind)).join(", ") || "Imobiliária", Situação: x.status, Versão: x.version })), hasNextPage: r.memberships.pageInfo.hasNextPage, endCursor: r.memberships.pageInfo.endCursor }; break; }
        case "/catalogs": { const r = await graphql<G.AdminCatalogsQuery>(G.AdminCatalogsDocument, variables); next = { ...pages[pathname], columns: ["ID", "Responsável pela vistoria", "Situação", "Contatos", "Versão"], rows: r.participants.nodes.map((x) => makeRow({ ID: x.id, "Responsável pela vistoria": x.name, Situação: x.status, Contatos: x.contacts.length, Versão: x.version })), hasNextPage: r.participants.pageInfo.hasNextPage, endCursor: r.participants.pageInfo.endCursor }; break; }
        case "/assets": { const r = await graphql<G.AdminAssetsQuery>(G.AdminAssetsDocument, variables); next = { ...pages[pathname], columns: ["ID", "Imóvel", "Código do imóvel", "Abrangência", "Situação", "Versão"], rows: r.assets.nodes.map((x) => makeRow({ ID: x.id, Imóvel: x.name, "Código do imóvel": x.externalKey, Abrangência: "Unidade vinculada", Situação: x.status, Versão: x.version, businessUnitId: x.businessUnitId })), hasNextPage: r.assets.pageInfo.hasNextPage, endCursor: r.assets.pageInfo.endCursor }; break; }
        case "/prompts": { const r = await graphql<G.AdminAnalysisPromptQuery>(G.AdminAnalysisPromptDocument, undefined, controller.signal); setAnalysisPrompt(r.analysisPrompt); next = { ...pages[pathname], columns: ["Tipo", "Modelo", "Confiança mínima", "Revisão", "Digest", "Atualizado"], rows: [makeRow({ Tipo: r.analysisPrompt.analysisType, Modelo: r.analysisPrompt.modelAlias, "Confiança mínima": `${r.analysisPrompt.minimumConfidenceBps / 100}%`, Revisão: r.analysisPrompt.revision, Digest: r.analysisPrompt.canonicalDigest, Atualizado: r.analysisPrompt.updatedAt })], hasNextPage: false, endCursor: null }; break; }
        case "/governance": { const r = await graphql<G.AdminGovernanceQuery>(G.AdminGovernanceDocument, variables); next = { ...pages[pathname], columns: ["Recurso", "ID", "Vistoria", "Destinatário", "Situação", "Código", "Atualizado"], rows: [makeRow({ Recurso: "Política de publicação", Situação: presentPublicationMode(r.publicationPolicy.mode), Versão: r.publicationPolicy.version }), ...r.retentionPolicies.nodes.map((x) => makeRow({ Recurso: "Retenção", ID: x.id, Situação: "Configurada", Evidências: `${x.evidenceDays} dias`, Operacional: `${x.operationalDays} dias`, Segurança: `${x.securityDays} dias`, Versão: x.version })), ...r.notificationDeliveries.nodes.map((x) => makeRow({ Recurso: "Entrega", ID: x.id, Vistoria: x.inspectionId, Destinatário: x.recipientMasked, Situação: x.status, Código: x.failureCode, inspectionId: x.inspectionId, responsibilityVersion: x.responsibilityVersion, canCorrectResponsibleEmail: x.canCorrectResponsibleEmail, Atualizado: x.updatedAt }))], hasNextPage: r.retentionPolicies.pageInfo.hasNextPage || r.notificationDeliveries.pageInfo.hasNextPage, endCursor: r.retentionPolicies.pageInfo.endCursor ?? r.notificationDeliveries.pageInfo.endCursor }; break; }
        case "/audit": { const r = await graphql<G.AdminAuditQuery>(G.AdminAuditDocument, variables); next = { ...pages[pathname], columns: ["ID", "Data/hora", "Ação", "Recurso", "Abrangência", "Resultado"], rows: r.auditEvents.nodes.map((x) => makeRow({ ID: x.id, targetId: x.targetId, "Data/hora": x.occurredAt, Ação: x.action, Recurso: x.targetType, Abrangência: identityData.tenant?.name, Resultado: x.outcome })), hasNextPage: r.auditEvents.pageInfo.hasNextPage, endCursor: r.auditEvents.pageInfo.endCursor }; setStatus(`${pages[pathname].title} atualizado. Uso: ${r.usageSummary.requests} requisições · custo ${r.usageSummary.cost == null ? "restrito" : r.usageSummary.cost}`); break; }
        default: next = { ...pages["/overview"], columns: ["Contexto", "Valor"], rows: [makeRow({ Contexto: "Imobiliária", Valor: identityData.tenant?.name }), makeRow({ Contexto: "Papel atual", Valor: identityData.me.roles.map(presentAdminRole).join(", ") || "Sem papel" }), makeRow({ Contexto: "Abrangência efetiva", Valor: identityData.me.effectiveScopes.map((x) => presentAdminScope(x.kind)).join(", ") || "Imobiliária" })], hasNextPage: false, endCursor: null };
      }
      if (currentGeneration === generation.current && !controller.signal.aborted) { setCollection(next); setStatus(`${next.title} atualizado. ${next.rows.length} registros visíveis.`); }
    } catch (failure) { if (!controller.signal.aborted) setError(failureText(failure)); } finally { if (currentGeneration === generation.current) setLoading(false); }
  }, [cursor, identityData, pathname, query]);
  useEffect(() => { if (!identityData || !hasAdminAccess() || pathname === "/llm-usage" || (pathname === "/prompts" && !identityData.me.roles.some((role) => role === "TENANT_ADMIN" || role === "INSPECTION_CONFIG_ADMIN"))) return; const timer = window.setTimeout(() => void load(), query ? 250 : 0); return () => { window.clearTimeout(timer); request.current?.abort(); }; }, [identityData, load, pathname, query]);

  const selectMembership = async (membershipId: string) => { if (!membershipOptions.some((item) => item.id === membershipId)) return; request.current?.abort(); clearProtectedContext(); setCollection(undefined); setDetail(undefined); setHistory(undefined); setError(undefined); updateParams({ after: null }); setMembershipContext(membershipId); setStatus("Trocando o contexto de acesso. Os dados anteriores foram descartados."); try { const result = await graphql<G.AdminIdentityQuery>(G.AdminIdentityDocument); setIdentityData(result); establishIdentity(result); } catch (failure) { setError(failureText(failure)); } };
  const showHistory = async (item: Row) => { setDetail(item); setHistory(undefined); try { const result = await graphql<G.AdminHistoryQuery>(G.AdminHistoryDocument, { first: 25, after: null }); setHistory(result.auditEvents.nodes); } catch (failure) { setError(failureText(failure)); } };
  const exportCsv = () => { if (!collection) return; const csv = [collection.columns, ...collection.rows.map((x) => collection.columns.map((c) => String(x[c] ?? "").replaceAll('"', '""')))].map((line) => line.map((value) => `"${value}"`).join(",")).join("\n"); const url = URL.createObjectURL(new Blob([csv], { type: "text/csv;charset=utf-8" })); const link = document.createElement("a"); link.href = url; link.download = `${pathname.slice(1)}-filtros.csv`; link.click(); URL.revokeObjectURL(url); setStatus("Exportação CSV preparada com os filtros atuais."); };
  const bootstrap = async () => { try { await graphqlIdentity<G.BootstrapTenantMutation>(G.BootstrapTenantDocument, { input: { name: "Minha operação", businessUnitCode: "MATRIZ", businessUnitName: "Matriz", clientMutationId: "local-bootstrap-admin" } }); await loadIdentity(); } catch (failure) { setError(failureText(failure)); } };
  const permitted = hasAdminAccess(); const promptPermitted = permitted && hasAnalysisPromptAccess() && Boolean(identityData?.me.roles.some((role) => role === "TENANT_ADMIN" || role === "INSPECTION_CONFIG_ADMIN") && identityData.me.productEntitlements.includes("ADMIN")); const activeMembership = identityData?.me.memberships.find((x) => x.id === restoreMembershipContext()?.membershipId); const tenantName = identityData?.tenant?.name ?? "Não selecionado";
  const currentPolicyVersion = collection?.rows.find((row) => row.Recurso === "Política de publicação")?.Versão;
  const primaryAction = () => {
    if (pathname === "/overview") return void load();
    if (pathname === "/audit") return exportCsv();
    setAction(pathname === "/organization" ? "unit" : pathname === "/access" ? "invite" : pathname === "/catalogs" ? "participant" : pathname === "/assets" ? "asset" : "policy");
  };
  if (needsBootstrap) return <main className="admin-denial"><h1>Primeiro acesso</h1><p role="status">{status}</p><button onClick={() => void bootstrap()}>Criar operação local</button></main>;
  if (error && !identityData) return <main className="admin-denial"><h1>Administração</h1><p role="alert">{error}</p><button onClick={() => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", pathname)}>Entrar com conta administrativa</button><a href={process.env.NEXT_PUBLIC_DASHBOARD_URL ?? "http://localhost:3002"}>Ir para o Painel</a></main>;
  return <main className="admin-shell">
    <a className="skip-link" href="#admin-content">Pular para o conteúdo</a>
    <header className="admin-header">
      <strong>Inspection <span>/ Administração</span></strong>
      <div className="admin-header-context"><span>{tenantName}</span><span className="admin-role">Administrador</span><span className="admin-avatar" aria-label={`Contexto ${tenantName}`}>{initials(tenantName)}</span></div>
    </header>
    <div className="admin-layout">
      <nav className="admin-nav" aria-label="Navegação administrativa">
        <p className="admin-nav-label">Administração</p>
        {navItems.filter(([, href]) => (href !== "/prompts" || promptPermitted) && (href !== "/llm-usage" || identityData?.me.canViewLLMCosts)).map(([label, href]) => <Link key={href} href={href} prefetch={false} aria-current={pathname === href ? "page" : undefined}>{label}</Link>)}
        <div className="admin-nav-context">
          <p className="admin-nav-label">Contexto protegido</p>
          <span>{tenantName}</span>
          {membershipOptions.length > 1 && <label>Imobiliária<select aria-label="Imobiliária ativa" value={activeMembership?.id ?? ""} onChange={(event) => void selectMembership(event.target.value)}>{membershipOptions.map((x) => <option key={x.id} value={x.id}>{presentAdminRole(x.role)} · {x.tenantId}</option>)}</select></label>}
          <a className="dashboard-exit" href={process.env.NEXT_PUBLIC_DASHBOARD_URL ?? "http://localhost:3002"}>Abrir Painel</a>
        </div>
      </nav>
      <section id="admin-content" className="admin-content" aria-labelledby="page-title">
        <p className="breadcrumb">Administração / {page.title}</p>
        <div className="page-heading"><div><h1 id="page-title">{section ?? page.title}</h1><p>{page.responsibility}</p></div><button hidden={pathname === "/llm-usage"} disabled={!permitted || loading || pathname === "/prompts" || (pathname === "/audit" && !collection)} onClick={primaryAction}>{loading && pathname === "/overview" ? "Atualizando…" : page.primary}</button></div>
        {pathname === "/llm-usage" ? <LLMUsagePage permitted={Boolean(identityData?.me.canViewLLMCosts)} /> : !permitted || (pathname === "/prompts" && !promptPermitted) ? <div className="admin-state denied" role="alert">Você não tem permissão para acessar este recurso neste escopo.</div> : pathname === "/prompts" ? error ? <div className="admin-state error" role="alert">{error} <button onClick={() => void load()}>Tentar novamente</button></div> : <AnalysisPromptEditor prompt={analysisPrompt} onSaved={() => { setCollection(undefined); void load(); }} /> : <>
          <div className="collection-toolbar"><label>{pathname === "/organization" ? "Buscar unidade" : "Buscar nesta coleção"}<input value={query} onChange={(event) => updateParams({ search: event.target.value || null, after: null })} placeholder={pathname === "/organization" ? "Nome ou código da unidade" : "Nome, identificador ou contexto"} /></label>{query && <button className="secondary" onClick={() => updateParams({ search: null, after: null })}>Limpar busca</button>}</div>
          <p role="status" className="status-line">{loading ? `Carregando ${page.title.toLowerCase()}…` : status}</p>
          {error ? <div className="admin-state error" role="alert">{error} <button onClick={() => void load()}>Tentar novamente</button></div> : <CollectionView data={collection} query={query} compact={pathname === "/organization"} tenantName={tenantName} onHistory={showHistory} onNext={(after) => updateParams({ after })} />}
        </>}
      </section>
    </div>
    {detail && <HistoryPanel detail={detail} history={history} onClose={() => { setDetail(undefined); setHistory(undefined); }} onCorrect={setCorrection} />}
    {correction && <AdminCorrectionForm detail={correction} onClose={() => setCorrection(undefined)} onSuccess={() => { setCorrection(undefined); setDetail(undefined); setCollection(undefined); void load(); }} />}
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
  if (!prompt) return <div className="admin-state loading" role="status">Carregando prompt global…</div>;
  return <form className="prompt-editor" onSubmit={submit} aria-label="Editar prompt global de análise">
    <div className="prompt-editor-meta"><span>Tipo: Imóveis</span><span>Modelo fixo: {prompt.modelAlias}</span><span>Confiança mínima: {prompt.minimumConfidenceBps / 100}%</span><span>Revisão: {prompt.revision}</span><span>Digest: {prompt.canonicalDigest}</span><span>Atualizado: {prompt.updatedAt}</span></div>
    <label>Instrução do sistema<textarea required minLength={1} maxLength={12000} rows={18} value={systemPrompt} onChange={(event) => setSystemPrompt(event.target.value)} /></label>
    <p className="prompt-editor-help">{systemPrompt.length}/12000 caracteres. O schema de saída, o alias do modelo e o limiar de confiança são fixos. Cada alteração gera uma nova revisão e um registro de auditoria sem armazenar o conteúdo anterior. As mudanças afetam somente inspeções criadas depois da gravação.</p>
    <label><input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} /> Confirmo a alteração do prompt global.</label>
    {error && <p className="admin-state error" role="alert">{error}</p>}
    <button disabled={busy || !confirmed || systemPrompt === prompt.systemPrompt}>{busy ? "Salvando…" : "Salvar nova revisão"}</button>
  </form>;
}

function CollectionView({ data, query, compact, tenantName, onHistory, onNext }: { data?: Collection; query: string; compact: boolean; tenantName: string; onHistory: (row: Row) => void; onNext: (cursor: string) => void }) {
  if (!data) return <div className="admin-state loading" role="status">Carregando registros sem alterar o seu contexto…</div>;
  if (!data.rows.length) return <div className="admin-state empty"><h2>{query ? "Nenhum resultado para os filtros atuais" : `Nenhum registro configurado em ${data.title}.`}</h2><p>{query ? "Os filtros foram preservados. Ajuste a busca ou limpe os filtros." : "Use a ação principal para iniciar este recurso."}</p></div>;
  const columns = compact ? ["Unidade", "Código", "Situação"] : data.columns.filter((column) => column !== "ID");
  return <><div className="table-wrap"><table><caption>{data.title}: coleção administrativa</caption><thead><tr>{columns.map((column) => <th scope="col" key={column}>{column}</th>)}<th scope="col">Ação</th></tr></thead><tbody>{data.rows.map((item, index) => <tr key={`${item.ID ?? index}`}>{columns.map((column) => <td key={column} data-label={column}>{column === "Unidade" || column === "Imóvel" ? <strong>{item[column]}</strong> : column === "Situação" ? <span className="admin-status-chip">{formatAdminStatus(item[column])}</span> : item[column]}</td>)}<td data-label="Ação"><button className="link-button" onClick={() => onHistory(item)}>Abrir detalhes →</button></td></tr>)}</tbody></table></div><div className="admin-collection-foot"><span>{compact ? "Nome em destaque. Identificadores no detalhe." : `${data.rows.length} registros · ${data.hasNextPage ? "mais páginas disponíveis" : "fim da consulta"}`}</span><span>Contexto: {tenantName}</span></div>{data.hasNextPage && data.endCursor && <button className="next-page" onClick={() => onNext(data.endCursor!)}>Carregar próxima página</button>}</>;
}

function HistoryPanel({ detail, history, onClose, onCorrect }: { detail: Row; history?: G.AdminHistoryQuery["auditEvents"]["nodes"]; onClose: () => void; onCorrect: (row: Row) => void }) { return <aside className="detail-panel" aria-label="Detalhe e histórico"><button className="secondary" onClick={onClose}>Fechar detalhe</button><h2>Detalhe do recurso</h2><dl>{Object.entries(detail).filter(([key]) => !["inspectionId", "responsibilityVersion", "canCorrectResponsibleEmail"].includes(key)).map(([key, value]) => <div key={key}><dt>{key}</dt><dd>{value}</dd></div>)}</dl>{detail.canCorrectResponsibleEmail === "Sim" && detail.inspectionId && <ButtonLike onClick={() => onCorrect(detail)}>Corrigir e reenviar</ButtonLike>}<h3>Histórico auditável</h3>{history ? history.length ? <ul>{history.map((event) => <li key={event.id}>{event.occurredAt} · {event.action} · {event.outcome}</li>)}</ul> : <p>Nenhum evento encontrado para este recurso.</p> : <p role="status">Carregando histórico…</p>}</aside>; }

function ButtonLike({ onClick, children }: { onClick: () => void; children: ReactNode }) { return <button className="secondary" onClick={onClick}>{children}</button>; }

function AdminCorrectionForm({ detail, onClose, onSuccess }: { detail: Row; onClose: () => void; onSuccess: () => void }) {
  const [email, setEmail] = useState(""); const [confirmation, setConfirmation] = useState(""); const [busy, setBusy] = useState(false); const [error, setError] = useState("");
  const submit = async (event: FormEvent) => { event.preventDefault(); setBusy(true); setError(""); try { const result = await graphql<G.AdminCorrectInspectionResponsibleEmailMutation>(G.AdminCorrectInspectionResponsibleEmailDocument, { input: { inspectionId: String(detail.inspectionId), email, emailConfirmation: confirmation, expectedResponsibilityVersion: Number(detail.responsibilityVersion), clientMutationId: `admin-correct-${Date.now()}` } }); const payload = result.correctInspectionResponsibleEmail; if (payload.userErrors.length) { setError(payload.userErrors[0].message); return; } onSuccess(); } catch (failure) { setError(failureText(failure)); } finally { setBusy(false); } };
  return <div className="modal-backdrop" role="presentation"><form className="admin-form" onSubmit={submit} aria-label="Corrigir e-mail do responsável"><button type="button" className="secondary" onClick={onClose}>Cancelar</button><h2>Corrigir e reenviar</h2><p>O convite atual será invalidado e um novo link será enviado.</p><label>Novo e-mail<input required type="email" value={email} onChange={(event) => setEmail(event.target.value)} /></label><label>Confirme o novo e-mail<input required type="email" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} /></label>{error && <p className="admin-state error" role="alert">{error}</p>}<button disabled={busy}>{busy ? "Reenviando…" : "Corrigir e reenviar"}</button></form></div>;
}

function ActionForm({ action, currentVersion, onClose, onSuccess }: { action: Action; currentVersion?: number; onClose: () => void; onSuccess: () => void }) {
  const [values, setValues] = useState<Record<string, string>>({}); const [busy, setBusy] = useState(false); const [error, setError] = useState<string>(); const [options, setOptions] = useState<AdminFormOptions>(); const [optionsLoading, setOptionsLoading] = useState(false);
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
  const submit = async (event: FormEvent) => { event.preventDefault(); setBusy(true); setError(undefined); const clientMutationId = `admin-${action}-${Date.now()}`; try { let errors: Array<{ message: string }>; switch (action) { case "unit": { if (!currentVersion) throw new Error("A versão atual da imobiliária ainda não foi carregada."); const r = await graphql<G.AdminCreateBusinessUnitMutation>(G.AdminCreateBusinessUnitDocument, { input: { code: values.code, name: values.name, expectedTenantVersion: currentVersion, clientMutationId } }); errors = r.createBusinessUnit.userErrors; break; } case "invite": { const r = await graphql<G.AdminInviteInternalUserMutation>(G.AdminInviteInternalUserDocument, { input: { issuer: values.issuer, subject: values.subject, role: values.role, scopes: [], clientMutationId } }); errors = r.inviteInternalUser.userErrors; break; } case "participant": { const r = await graphql<G.AdminUpsertParticipantMutation>(G.AdminUpsertParticipantDocument, { input: { businessUnitId: values.businessUnitId, name: values.name, segmentRole: values.segmentRole, contacts: [], clientMutationId } }); errors = r.upsertParticipant.userErrors; break; } case "asset": { const r = await graphql<G.AdminRegisterAssetMutation>(G.AdminRegisterAssetDocument, { input: { asset: { businessUnitId: values.businessUnitId, segmentVersionId: values.segmentVersionId, templateId: values.templateId || null, name: values.name, externalKey: values.externalKey, address: values.address, attributes: {}, assignments: [] }, clientMutationId } }); errors = r.registerAsset.userErrors; break; } default: { if (currentVersion === undefined) throw new Error("A versão atual da política ainda não foi carregada."); const r = await graphql<G.AdminConfigurePublicationPolicyMutation>(G.AdminConfigurePublicationPolicyDocument, { input: { mode: values.mode, expectedVersion: currentVersion, clientMutationId } }); errors = r.configurePublicationPolicy.userErrors; } } if (errors.length) throw new Error(errors[0].message); onSuccess(); } catch (failure) { setError(failureText(failure)); } finally { setBusy(false); } };
  const title = action === "unit" ? "Criar unidade" : action === "invite" ? "Convidar usuário" : action === "participant" ? "Criar responsável pela vistoria" : action === "asset" ? "Registrar imóvel" : "Configurar política";
  const roleOptions: Array<[string, string]> = [["TENANT_ADMIN", "Administrador da imobiliária"], ["MANAGER", "Gestor"], ["EMPLOYEE", "Operador"], ["VIEWER", "Visualizador"], ["CUSTOMER_VIEWER", "Visualizador cliente"], ["ORGANIZATION_ADMIN", "Administrador de organização"], ["ACCESS_ADMIN", "Administrador de acessos"], ["PARTICIPATION_ADMIN", "Administrador de participação"], ["INSPECTION_CONFIG_ADMIN", "Administrador de vistorias"], ["GOVERNANCE_ADMIN", "Administrador de governança"], ["AUDITOR", "Auditor"]];
  const toChoice = (value: string, label: string): [string, string] => [value, label];
  const select = (name: string, label: string, choices: Array<[string, string]>, required = true) => <label key={name}>{label}<select required={required} value={values[name] ?? ""} onChange={(event) => setValue(name, event.target.value)}><option value="" disabled={required}>{required ? "Selecione" : "Nenhum"}</option>{choices.map(([value, choiceLabel]) => <option key={value} value={value}>{choiceLabel}</option>)}</select></label>;
  const compatibleTemplates = options?.templates.filter((template) => template.segmentVersionId === values.segmentVersionId) ?? [];
  useEffect(() => { if (action === "asset" && values.templateId && !compatibleTemplates.some((template) => template.id === values.templateId)) setValue("templateId", ""); }, [action, compatibleTemplates, values.templateId]);
  return <div className="modal-backdrop" role="presentation"><form className="admin-form" onSubmit={submit} aria-label="Operação administrativa"><button type="button" className="secondary" onClick={onClose}>Cancelar</button><h2>{title}</h2><p>O contexto atual será auditado. Revise a abrangência antes de salvar.</p>{action === "unit" && <><label>Código da unidade<input required value={values.code ?? ""} onChange={(event) => setValue("code", event.target.value)} /></label><label>Nome da unidade<input required value={values.name ?? ""} onChange={(event) => setValue("name", event.target.value)} /></label></>}{action === "invite" && <><label>Emissor<input required value={values.issuer ?? ""} onChange={(event) => setValue("issuer", event.target.value)} /></label><label>Identificador externo do usuário<input required value={values.subject ?? ""} onChange={(event) => setValue("subject", event.target.value)} /></label>{select("role", "Perfil de acesso", roleOptions)}</>}{action === "participant" && <>{optionsLoading && <p role="status">Carregando unidades…</p>}{options && select("businessUnitId", "Unidade", options.units.map((unit) => toChoice(unit.id, `${unit.name} · ${unit.code}`)))}<label>Nome do responsável<input required value={values.name ?? ""} onChange={(event) => setValue("name", event.target.value)} /></label><label>Função no segmento<input required value={values.segmentRole ?? ""} onChange={(event) => setValue("segmentRole", event.target.value)} /></label></>}{action === "asset" && <>{optionsLoading && <p role="status">Carregando opções de configuração…</p>}{options && <>{select("businessUnitId", "Unidade", options.units.map((unit) => toChoice(unit.id, `${unit.name} · ${unit.code}`)))}{select("segmentVersionId", "Versão do segmento", options.segments.map((segment) => toChoice(segment.activeVersionId!, `${segment.name} · ${segment.key}`)))}{select("templateId", "Modelo de vistoria", compatibleTemplates.map((template) => toChoice(template.id, `${template.name} · ${template.key}`)), false)}</>}<label>Nome do imóvel<input required value={values.name ?? ""} onChange={(event) => setValue("name", event.target.value)} /></label><label>Código do imóvel<input required value={values.externalKey ?? ""} onChange={(event) => setValue("externalKey", event.target.value)} /></label><label>Endereço do imóvel<input required value={values.address ?? ""} onChange={(event) => setValue("address", event.target.value)} /></label></>}{action === "policy" && select("mode", "Modo de publicação", [["MANUAL", "Manual"], ["AUTOMATIC", "Automático"]])}{error && <p className="admin-state error" role="alert">{error}</p>}<button disabled={busy || optionsLoading || ((action === "unit" || action === "policy") && currentVersion === undefined) || ((action === "participant" || action === "asset") && !options)}>{busy ? "Salvando…" : "Salvar operação"}</button></form></div>;
}

function initials(name: string): string {
  const value = name.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part[0]).join("").toUpperCase();
  return value || "OP";
}

function formatAdminStatus(value: string | number | null): string { return presentAdminStatus(value); }
