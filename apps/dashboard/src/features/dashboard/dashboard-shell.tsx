"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { beginPKCE } from "@/auth/pkce";
import { clearSession, getMembershipId, selectMembership, setIdentity, type DashboardIdentity } from "@/auth/session";
import { composeCapabilities, type Capability } from "@/features/dashboard/capabilities";
import { filterInspections, InspectionViewSelector, InspectionViews, useInspectionView, type InspectionActionHandlers, type InspectionColumnKey } from "@/features/dashboard/inspection-views";
import { SchedulesJourney } from "@/features/dashboard/schedules-journey";
import { ReportsJourney } from "@/features/dashboard/reports-journey";
import { TriageJourney } from "@/features/dashboard/triage-journey";
import { useNotifications } from "@/features/notifications/use-notifications";
import { deliveryPresentation } from "@/features/notifications/delivery-status";
import { Combobox, Dialog, type ComboboxOption } from "@inspection/design-system";
import {
  AddExceptionalStageDocument, CancelInspectionDocument, CloseProjectDocument, CreateInspectionDocument, CreateProjectDocument, CustomerEvidenceDocument, CustomerPortfolioDocument, CustomerReportDocument,
  CustomerTimelineDocument, DashboardFormOptionsDocument, DashboardGateDocument, DashboardMembershipsDocument, InvalidateInspectionDocument, OperationalOverviewDocument, OriginPromotionDocument, PromoteInspectionPhotosDocument, ProjectDetailDocument,
  InvalidateReportPublicationDocument, ProjectsDocument, ProjectTimelineDocument, PublishReportDocument, ReopenProjectDocument, ReportDownloadDocument, RequestRecaptureDocument, InspectionsDocument, SkipProjectStageDocument, StartProjectStageDocument,
  type CustomerEvidenceQuery, type CustomerPortfolioQuery, type CustomerReportQuery, type DashboardFormOptionsQuery, type DashboardGateQuery, type DashboardMembershipsQuery, type InspectionsQuery, type OperationalOverviewQuery, type OriginPromotionQuery, type ProjectsQuery, type ReportDownloadQuery,
} from "@/graphql/generated";
import { graphql, type GraphQLFailure } from "@/graphql/client";
import { presentClassification, presentDashboardStatus, presentNotificationChannel, presentProjectStage, presentReportMode } from "./presentation";
import { localDateTimeToInstant } from "./datetime";
import { CustomerReportVisual, ReportVisual } from "./report-visual";

type Page = "Início" | "Agenda de vistorias" | "Vistorias" | "Projetos e etapas" | "Triagem" | "Laudos" | "Portfólio" | "Notificações";

export function DashboardShell({ section }: { section: Page }) {
  const pathname = usePathname();
  const router = useRouter();
  const [identity, setCurrentIdentity] = useState<DashboardIdentity>();
  const [capability, setCapability] = useState<Capability>();
  const [memberships, setMemberships] = useState<DashboardMembershipsQuery["me"]["memberships"]>([]);
  const [message, setMessage] = useState("Verificando acesso…");
  const [reportsRefreshKey, setReportsRefreshKey] = useState(0);

  const loadGate = useCallback(async () => {
    try {
      if (!getMembershipId()) {
        const membershipData = await graphql<DashboardMembershipsQuery, {}>(DashboardMembershipsDocument, {});
        const activeMemberships = membershipData.me.memberships.filter((membership) => membership.status === "ACTIVE");
        setMemberships(activeMemberships);
        if (activeMemberships.length === 1) { selectMembership(activeMemberships[0].id); setMessage("Contexto de acesso selecionado. Carregando dados protegidos…"); void loadGate(); return; }
        setCurrentIdentity(undefined); setCapability(undefined); setMessage("Selecione o contexto de acesso antes de carregar dados protegidos."); return;
      }
      const data = await graphql<DashboardGateQuery, {}>(DashboardGateDocument, {});
      const current = { tenantId: data.me.tenantId, tenantName: data.tenant?.name ?? "Imobiliária", tenantStatus: data.tenant?.status ?? "INACTIVE", entitlements: data.me.productEntitlements, roles: data.me.roles, scopes: data.me.effectiveScopes };
      if (!current.entitlements.includes("DASHBOARD") || !current.roles.length || current.tenantStatus !== "ACTIVE") {
        clearSession(); setCurrentIdentity(undefined); setCapability(undefined); setMessage("Acesso ao Painel não autorizado. Nenhum dado operacional foi carregado."); return;
      }
      setIdentity(current); setCurrentIdentity(current); setCapability(composeCapabilities(current)); setMessage(data.tenant?.name ?? "Contexto operacional ativo");
    } catch (error) {
      setCurrentIdentity(undefined); setCapability(undefined);
      const failure = error as GraphQLFailure;
      setMessage(["FORBIDDEN", "UNAUTHENTICATED", "TENANT_INACTIVE"].includes(failure.code ?? "") ? "Acesso ao Painel não autorizado. Nenhum dado operacional foi carregado." : failure.message);
    }
  }, []);
  useEffect(() => { void loadGate(); }, [loadGate]);
  const notifications = useNotifications(Boolean(identity), capability?.audience === "internal");
  const signIn = () => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", pathname);
  const switchMembership = (membershipId: string) => { selectMembership(membershipId); setCurrentIdentity(undefined); setCapability(undefined); setMessage("Trocando o contexto de acesso. Dados protegidos anteriores foram removidos."); void loadGate(); };

  if (!identity || !capability) return <main className="denial"><h1>Painel</h1><p role="status">{message}</p>{memberships.length > 0 && <MembershipPicker memberships={memberships} selected={getMembershipId()} onChange={switchMembership} />}{memberships.length === 0 && <button onClick={signIn}>Entrar no Painel</button>}</main>;
  return <main><header><strong>Inspection <span>/ Painel</span></strong><div className="dashboard-context">{memberships.length > 1 && <MembershipPicker memberships={memberships} selected={getMembershipId()} onChange={switchMembership} />}<span>{identity.tenantName}</span><span className="dashboard-role">{capability.audience === "customer" ? "Cliente" : "Operação"}</span><span className="dashboard-avatar" aria-label={`Contexto ${identity.tenantName}`}>{tenantInitials(identity.tenantName)}</span></div></header><nav aria-label="Painel"><p className="dashboard-nav-label">Área operacional</p>{capability.links.map(([label, href]) => <Link key={href} aria-current={pathname === href ? "page" : undefined} href={href}>{label}{label === "Notificações" && notifications.unreadCount > 0 ? ` (${notifications.unreadCount})` : ""}</Link>)}<p className="dashboard-nav-label dashboard-nav-label--context">Contexto protegido</p><span className="dashboard-nav-context">{identity.tenantName}</span>{capability.canUseAdmin && <a href={process.env.NEXT_PUBLIC_ADMIN_URL ?? "http://localhost:3000"}>Abrir Administração</a>}</nav><section>{section !== "Vistorias" && <div className={`actions${section === "Laudos" ? " report-page-heading" : ""}`}><h1>{section}</h1><button className="secondary" onClick={() => { void loadGate(); void notifications.refresh(); router.refresh(); if (capability.audience === "internal" && section === "Laudos") setReportsRefreshKey((value) => value + 1); }}>Atualizar</button></div>}{notifications.error && <p className="warning" role="status">Dados já exibidos podem estar desatualizados. {notifications.error}</p>}{capability.audience === "customer" ? <CustomerPortal section={section} /> : <OperationsDashboard section={section} capability={capability} reportsRefreshKey={reportsRefreshKey} notifications={notifications} />}{section === "Notificações" && <NotificationCenter notifications={notifications} />}</section></main>;
}

function MembershipPicker({ memberships, selected, onChange }: { memberships: DashboardMembershipsQuery["me"]["memberships"]; selected?: string; onChange: (id: string) => void }) {
  return <label className="membership">Contexto de acesso<select aria-label="Contexto de acesso" value={selected ?? ""} onChange={(event) => onChange(event.target.value)}><option value="" disabled>Selecione</option>{memberships.map((membership) => <option key={membership.id} value={membership.id}>{membership.role === "CUSTOMER_VIEWER" ? "Visualizador cliente" : membership.role === "VIEWER" ? "Visualizador" : membership.role === "MANAGER" ? "Gestor" : "Perfil de acesso"} · {membership.tenantId}</option>)}</select></label>;
}

function OperationsDashboard({ section, capability, reportsRefreshKey, notifications }: { section: Page; capability: Capability; reportsRefreshKey: number; notifications: ReturnType<typeof useNotifications> }) {
  const needsFormOptions = capability.canMutate && ["Agenda de vistorias", "Vistorias", "Projetos e etapas"].includes(section);
  const formOptions = useDashboardFormOptions(needsFormOptions);
  if (section === "Agenda de vistorias") return <SchedulesJourney canMutate={capability.canMutate} options={formOptions} />;
  if (section === "Vistorias") return <InspectionsJourney canMutate={capability.canMutate} canPromoteOrigin={capability.canPromoteOrigin} options={formOptions} />;
  if (section === "Projetos e etapas") return <ProjectsJourney canMutate={capability.canMutate} options={formOptions} />;
  if (section === "Laudos") return <ReportsJourney canPublish={capability.canPublish} refreshKey={reportsRefreshKey} />;
  if (section === "Triagem") return <TriageJourney capability={capability} />;
  if (section === "Notificações") return null;
  return <>{section === "Início" && <NotificationSummary notifications={notifications} />}<OverviewJourney /></>;
}

function NotificationSummary({ notifications }: { notifications: ReturnType<typeof useNotifications> }) {
  const items = notifications.items.slice(0, 3);
  if (notifications.unreadCount === 0) return null;
  return <aside className="notification-summary" aria-label="Notificações importantes"><div><strong>{notifications.unreadCount} notificações não lidas</strong><Link href="/notifications">Abrir central</Link></div>{items.map((item) => <Link key={item.id} href={notificationHref(item.action, item.resourceId) ?? "/notifications"} onClick={() => void notifications.markRead(item.id)}><span>{item.title}</span><small>{item.context.assetName ? String(item.context.assetName) : item.body}</small></Link>)}</aside>;
}

type FormOptionsState = { data?: DashboardFormOptionsQuery; loading: boolean; error?: string };
function useDashboardFormOptions(enabled: boolean): FormOptionsState {
  const [state, setState] = useState<FormOptionsState>({ loading: enabled });
  useEffect(() => {
    if (!enabled) { setState({ loading: false }); return; }
    let cancelled = false;
    setState({ loading: true });
    void graphql<DashboardFormOptionsQuery, Record<string, never>>(DashboardFormOptionsDocument, {}).then((data) => { if (!cancelled) setState({ data, loading: false }); }).catch((error) => { if (!cancelled) setState({ loading: false, error: formatFailure(error) }); });
    return () => { cancelled = true; };
  }, [enabled]);
  return state;
}

function FormOptionsNotice({ options }: { options: FormOptionsState }) {
  if (options.loading) return <p role="status">Carregando imóveis, responsáveis pela vistoria e modelos de vistoria…</p>;
  if (options.error) return <p className="warning" role="alert">Não foi possível carregar as opções do cadastro. {options.error}</p>;
  return null;
}

function entityOptions(options: FormOptionsState["data"], kind: "asset" | "participant" | "template", assetId?: string): ComboboxOption[] {
  if (!options) return [];
  if (kind === "asset") return options.assets.nodes.filter((item) => item.status === "ACTIVE").map((item) => ({ value: item.id, label: item.name, description: item.externalKey }));
  if (kind === "participant") {
    const asset = options.assets.nodes.find((item) => item.id === assetId);
    const assigned = new Set(asset?.assignments.filter((assignment) => assignment.active).map((assignment) => assignment.participantId));
    return options.participants.nodes.filter((item) => item.status === "ACTIVE" && (!asset || assigned.has(item.id))).map((item) => ({ value: item.id, label: item.name }));
  }
  const asset = options.assets.nodes.find((item) => item.id === assetId);
  return options.templates.nodes.filter((item) => item.activeVersionId && (!asset || item.segmentVersionId === asset.segmentVersionId)).map((item) => ({ value: item.id, label: item.name, description: item.key }));
}

function RelationshipField({ label, value, onChange, options, required = false, disabled = false }: { label: string; value: string; onChange: (value: string) => void; options: ComboboxOption[]; required?: boolean; disabled?: boolean }) {
  return <label>{label}<Combobox value={value} options={options} onChange={onChange} required={required} disabled={disabled} aria-label={label} /></label>;
}

function OverviewJourney() {
  const [classification, setClassification] = useState(""); const [status, setStatus] = useState(""); const [data, setData] = useState<OperationalOverviewQuery>(); const [message, setMessage] = useState("Carregue o resumo e a fila no contexto autorizado.");
  async function loadOverview(): Promise<OperationalOverviewQuery> { return graphql<OperationalOverviewQuery, { after: string | null; classification: string | null; status: string | null }>(OperationalOverviewDocument, { after: null, classification: classification || null, status: status || null }); }
  const load = async () => { try { const next = await loadOverview(); setData(next); setMessage(next.triageInspections.nodes.length ? "Resumo e fila atualizados com o mesmo filtro." : "Não há itens para os filtros selecionados."); } catch (error) { setMessage(`Dados exibidos podem estar desatualizados. ${(error as Error).message}`); } };
  return <div className="feature"><p>Resumo e triagem usam o mesmo filtro e contexto de servidor. A fila é somente leitura para o perfil Visualizador; a autorização é sempre revalidada no servidor.</p><div className="filters"><label>Classificação<select value={classification} onChange={(event) => setClassification(event.target.value)}><option value="">Todas</option><option value="CRITICAL">Crítica</option><option value="ATTENTION">Atenção</option></select></label><label>Situação<input value={status} onChange={(event) => setStatus(event.target.value)} placeholder="Código da situação, por exemplo PENDING" /></label><button onClick={() => void load()}>Atualizar prioridades</button></div><p role="status">{message}</p>{data && <><div className="cards">{Object.entries(data.dashboardSummary).map(([name, value]) => <article className="card" key={name}><strong>{presentClassification(name) === "Situação não reconhecida" ? "Resumo geral" : presentClassification(name)}</strong><div>{value}</div></article>)}</div><Collection items={data.triageInspections.nodes.map((item) => ({ id: item.inspectionId, title: `${presentClassification(item.classification)} · ${presentDashboardStatus(item.status)}`, detail: `Atualizado em ${item.updatedAt}` }))} /></>}</div>;
}

function InspectionsJourney({ canMutate, canPromoteOrigin, options }: { canMutate: boolean; canPromoteOrigin: boolean; options: FormOptionsState }) {
  const params = useSearchParams(); const [data, setData] = useState<InspectionsQuery>(); const [message, setMessage] = useState("Carregando vistorias…"); const [query, setQuery] = useState(""); const [statusFilter, setStatusFilter] = useState<InspectionColumnKey | "todas">("todas"); const selected = params.get("inspectionId"); const [view, setView] = useInspectionView();
  const load = useCallback(async () => { try { const next = await graphql<InspectionsQuery, { after: string | null; history: boolean }>(InspectionsDocument, { after: null, history: true }); setData(next); setMessage(next.inspections.nodes.length ? "Vistorias atualizadas." : "Nenhuma vistoria encontrada neste escopo."); } catch (error) { setMessage((error as Error).message); } }, []);
  useEffect(() => { void load(); }, [load]);
  const mutate = async (document: typeof CreateInspectionDocument | typeof CancelInspectionDocument | typeof InvalidateInspectionDocument, input: Record<string, unknown>) => { try { const result = await graphql(document as never, { input } as never); const payload = Object.values(result as Record<string, unknown>)[0] as { userErrors?: Array<{ message: string; code: string }> }; setMessage(payload.userErrors?.length ? formatMutationErrors(payload.userErrors) : "Vistoria salva. Atualizando dados…"); if (!payload.userErrors?.length) await load(); } catch (error) { setMessage(formatFailure(error)); } };
  const actions: InspectionActionHandlers | undefined = canMutate ? { onCancel: (item) => void mutate(CancelInspectionDocument, { inspectionId: item.id, expectedVersion: item.version, clientMutationId: mutationId() }), onInvalidate: (item) => { const reason = window.prompt("Motivo da invalidação"); if (reason) void mutate(InvalidateInspectionDocument, { inspectionId: item.id, expectedVersion: item.version, reason, clientMutationId: mutationId() }); }, onRecapture: (item) => void requestRecapture(item.id, item.version, setMessage) } : undefined;
  const inspections = data ? filterInspections(data.inspections.nodes, query, statusFilter) : [];
  return <div className="feature inspection-journey"><div className="inspection-page-heading"><div><p className="inspection-breadcrumb">Operação / Vistorias</p><h1>Vistorias</h1><p>Acompanhe os registros pela lista, pela situação ou pelo prazo.</p></div>{canMutate && <details className="inspection-create-panel"><summary>Nova vistoria</summary><FormOptionsNotice options={options} />{options.data && <InspectionForm options={options.data} onSubmit={(input) => void mutate(CreateInspectionDocument, input)} />}</details>}</div><div className="inspection-view-bar"><div><strong>Visualização</strong><p>A troca preserva os registros e os filtros.</p></div><InspectionViewSelector view={view} onChange={setView} /></div><div className="inspection-toolbar"><label>Buscar vistoria<input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="ID, responsável pela vistoria, origem ou situação" /></label><label className="inspection-status-filter">Situação<select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value as InspectionColumnKey | "todas")}><option value="todas">Todas</option><option value="planejamento">Planejamento</option><option value="execucao">Em execução</option><option value="concluidas">Concluídas</option><option value="encerradas">Encerradas</option></select></label><button className="secondary" onClick={() => void load()}>Carregar vistorias</button></div><p className="inspection-load-status" role="status">{message}</p><div id="inspection-collection" className="inspection-collection" role="region" aria-label={`Vistorias em ${view}`}>{data ? <InspectionViews inspections={inspections} view={view} actions={actions} /> : <p className="inspection-empty-state">Carregando os registros deste contexto…</p>}</div><div className="inspection-foot"><span>{inspections.length} vistoria(ões) exibida(s).</span><span>Contexto: dados da API</span></div>{selected && <InspectionWorkspace id={selected} canMutate={canMutate} canPromoteOrigin={canPromoteOrigin} options={options.data} />}</div>;
}

function InspectionForm({ onSubmit, options }: { onSubmit: (input: Record<string, unknown>) => void; options: DashboardFormOptionsQuery }) { const [assetId, setAssetId] = useState(""); const [participantId, setParticipantId] = useState(""); const [templateId, setTemplateId] = useState(""); const [dueAt, setDueAt] = useState(""); const [deadlineAt, setDeadlineAt] = useState(""); const participants = entityOptions(options, "participant", assetId); const templates = entityOptions(options, "template", assetId); useEffect(() => { if (participantId && !participants.some((option) => option.value === participantId)) setParticipantId(""); if (templateId && !templates.some((option) => option.value === templateId)) setTemplateId(""); }, [assetId, participantId, participants, templateId, templates]); return <form onSubmit={(event) => { event.preventDefault(); onSubmit({ assetId, participantId, dueAt: localDateTimeToInstant(dueAt), deadlineAt: localDateTimeToInstant(deadlineAt), reason: "Criada pelo Dashboard", templateId: templateId || null, referenceVersionId: null, reminderInstants: [], clientMutationId: mutationId() }); }}><h2>Nova vistoria</h2><RelationshipField label="Imóvel" value={assetId} onChange={(value) => { setAssetId(value); setParticipantId(""); setTemplateId(""); }} options={entityOptions(options, "asset")} required /><RelationshipField label="Responsável pela vistoria" value={participantId} onChange={setParticipantId} options={participants} required disabled={!assetId} /><RelationshipField label="Modelo de vistoria (opcional)" value={templateId} onChange={setTemplateId} options={templates} disabled={!assetId} /><Field label="Vencimento" value={dueAt} onChange={setDueAt} type="datetime-local" required /><Field label="Prazo final" value={deadlineAt} onChange={setDeadlineAt} type="datetime-local" required /><button type="submit">Criar vistoria</button></form>; }

function InspectionWorkspace({ id, canMutate, canPromoteOrigin, options }: { id: string; canMutate: boolean; canPromoteOrigin: boolean; options?: DashboardFormOptionsQuery }) {
  const [message, setMessage] = useState("Carregando estado atual da vistoria…"); const [detail, setDetail] = useState<InspectionsQuery["inspections"]["nodes"][number]>();
  async function loadDetail(): Promise<InspectionsQuery> { return graphql<InspectionsQuery, { after: string | null; history: boolean }>(InspectionsDocument, { after: null, history: true }); }
  useEffect(() => { void loadDetail().then((data) => { const inspection = data.inspections.nodes.find((item) => item.id === id); setDetail(inspection); setMessage(inspection ? "Estado atual carregado." : "A vistoria não está disponível neste contexto."); }).catch((error) => setMessage((error as Error).message)); }, [id]);
  return <article className="card"><h2>Detalhe da vistoria</h2><p role="status">{message}</p>{detail && <dl><dt>Situação</dt><dd>{presentDashboardStatus(detail.status)}</dd><dt>Evidências</dt><dd>{detail.evidenceCount}</dd><dt>Motivo</dt><dd>{detail.stateReason ?? "—"}</dd><dt>Versão</dt><dd>{detail.version}</dd></dl>}{canPromoteOrigin && detail?.status === "COMPLETED" && <OriginPromotionAction inspection={detail} options={options} />}{canMutate && <p>Cancelamento e invalidação exigem versão atual, identidade de mutação e, na invalidação, motivo. Após interrupção, recarregue este detalhe para confirmar o resultado.</p>}</article>;
}

function OriginPromotionAction({ inspection, options }: { inspection: InspectionsQuery["inspections"]["nodes"][number]; options?: DashboardFormOptionsQuery }) {
  const [promotion, setPromotion] = useState<OriginPromotionQuery["originPromotion"]>(); const [selected, setSelected] = useState<string[]>([]); const [message, setMessage] = useState("Verificando se esta vistoria pode criar a referência do imóvel…");
  const load = useCallback(async () => { try { const data = await graphql<OriginPromotionQuery, { inspectionId: string }>(OriginPromotionDocument, { inspectionId: inspection.id }); setPromotion(data.originPromotion); setMessage(data.originPromotion ? "Selecione as fotos que formarão a referência permanente do imóvel." : "Esta vistoria não pode criar uma referência."); } catch { setPromotion(undefined); setMessage("Esta vistoria não pode criar uma referência."); } }, [inspection.id]);
  useEffect(() => { void load(); }, [load]);
  if (!promotion) return null;
  const assetVersion = options?.assets.nodes.find((asset) => asset.id === inspection.assetId)?.version;
  const toggle = (id: string) => setSelected((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id]);
  const submit = async () => { if (!assetVersion || selected.length === 0 || !window.confirm("Usar as fotos selecionadas como referência independente para próximas vistorias?")) return; try { const data = await graphql(PromoteInspectionPhotosDocument, { input: { inspectionId: inspection.id, mediaIds: selected, expectedAssetVersion: assetVersion, clientMutationId: mutationId() } }); const next = data.promoteInspectionPhotos.promotion; setPromotion(next); setMessage(next?.status === "ACTIVE" ? "Referência ativa para as próximas vistorias." : "Referência solicitada. A cópia e ativação continuam em segundo plano."); } catch (error) { setMessage(formatFailure(error)); } };
  return <section className="origin-promotion"><h3>Fotos de referência</h3><p role="status">{message}</p>{promotion.status === "ACTIVE" ? <p>A referência está ativa para as próximas vistorias.</p> : <>{promotion.failureReason && <p className="warning">{promotion.failureReason}</p>}<div>{promotion.eligibleMedia.map((media) => <label key={media.id}>{media.url && <img src={media.url} alt={media.description} loading="lazy" />}<input type="checkbox" checked={selected.includes(media.id)} onChange={() => toggle(media.id)} /> {media.description}</label>)}</div><button disabled={!assetVersion || selected.length === 0} onClick={() => void submit()}>Usar fotos como referência</button></>}</section>;
}

function ProjectsJourney({ canMutate, options }: { canMutate: boolean; options: FormOptionsState }) {
  const [data, setData] = useState<ProjectsQuery>(); const [message, setMessage] = useState("Projetos preservam estágios e histórico ordenado.");
  const load = async () => { try { const next = await graphql<ProjectsQuery, { after: string | null }>(ProjectsDocument, { after: null }); setData(next); setMessage(next.projects.nodes.length ? "Projetos atualizados." : "Não há projetos neste escopo."); } catch (error) { setMessage((error as Error).message); } };
  const mutate = async (document: typeof CreateProjectDocument | typeof StartProjectStageDocument | typeof SkipProjectStageDocument | typeof CloseProjectDocument | typeof ReopenProjectDocument | typeof AddExceptionalStageDocument, input: Record<string, unknown>) => { try { const result = await graphql(document as never, { input } as never); const payload = Object.values(result as Record<string, unknown>)[0] as { userErrors?: Array<{ message: string; code: string }> }; setMessage(payload.userErrors?.length ? formatMutationErrors(payload.userErrors) : "Projeto salvo. Atualizando dados…"); if (!payload.userErrors?.length) await load(); } catch (error) { setMessage(formatFailure(error)); } };
  return <div className="feature"><p>Crie projetos com imóvel, responsável pela vistoria e modelo de vistoria. Etapas excepcionais, início, salto, encerramento e reabertura usam versões esperadas e razões quando exigidas.</p>{canMutate && <><FormOptionsNotice options={options} />{options.data && <ProjectForm options={options.data} onSubmit={(input) => void mutate(CreateProjectDocument, input)} />}</>}<button onClick={() => void load()}>Carregar projetos</button><p role="status">{message}</p>{data && <ul className="collection">{data.projects.nodes.map((item) => <li key={item.id}><strong>{presentDashboardStatus(item.status)} · {item.stages.length} etapa(s)</strong><span>{presentReportMode(item.reportMode)} · v{item.version}</span>{canMutate && <div className="actions"><button onClick={() => void mutate(CloseProjectDocument, { projectId: item.id, expectedVersion: item.version, clientMutationId: mutationId() })}>Encerrar</button><button className="secondary" onClick={() => { const reason = window.prompt("Motivo da reabertura"); if (reason) void mutate(ReopenProjectDocument, { projectId: item.id, expectedVersion: item.version, reason, clientMutationId: mutationId() }); }}>Reabrir</button>{item.stages.map((stage) => <span key={stage.id} className="actions"><button onClick={() => void mutate(StartProjectStageDocument, { projectId: item.id, stageId: stage.id, expectedProjectVersion: item.version, expectedStageVersion: stage.version, dueAt: stage.plannedAt ?? new Date().toISOString(), deadlineAt: stage.plannedAt ?? new Date().toISOString(), reminderInstants: [], referenceVersionId: null, clientMutationId: mutationId() })}>Iniciar {presentProjectStage(stage.label, stage.key)}</button><button className="secondary" onClick={() => { const reason = window.prompt("Motivo do salto"); if (reason) void mutate(SkipProjectStageDocument, { projectId: item.id, stageId: stage.id, expectedVersion: item.version, reason, clientMutationId: mutationId() }); }}>Pular</button></span>)}</div>}</li>)}</ul>}</div>;
}

function ProjectForm({ onSubmit, options }: { onSubmit: (input: Record<string, unknown>) => void; options: DashboardFormOptionsQuery }) { const [assetId, setAssetId] = useState(""); const [participantId, setParticipantId] = useState(""); const [templateId, setTemplateId] = useState(""); const participants = entityOptions(options, "participant", assetId); const templates = entityOptions(options, "template", assetId); useEffect(() => { if (participantId && !participants.some((option) => option.value === participantId)) setParticipantId(""); if (templateId && !templates.some((option) => option.value === templateId)) setTemplateId(""); }, [assetId, participantId, participants, templateId, templates]); return <form onSubmit={(event) => { event.preventDefault(); onSubmit({ assetId, participantId, templateId: templateId || null, clientMutationId: mutationId() }); }}><h2>Novo projeto</h2><RelationshipField label="Imóvel" value={assetId} onChange={(value) => { setAssetId(value); setParticipantId(""); setTemplateId(""); }} options={entityOptions(options, "asset")} required /><RelationshipField label="Responsável pela vistoria" value={participantId} onChange={setParticipantId} options={participants} required disabled={!assetId} /><RelationshipField label="Modelo de vistoria (opcional)" value={templateId} onChange={setTemplateId} options={templates} disabled={!assetId} /><button type="submit">Criar projeto</button></form>; }

function CustomerPortal({ section }: { section: Page }) {
  const params = useSearchParams(); const [message, setMessage] = useState("Esta experiência usa somente projeções de cliente aprovadas pelo servidor."); const [portfolio, setPortfolio] = useState<CustomerPortfolioQuery>();
  async function loadPortfolio(): Promise<CustomerPortfolioQuery> { return graphql<CustomerPortfolioQuery, { after: string | null }>(CustomerPortfolioDocument, { after: null }); }
  const load = async () => { try { const data = await loadPortfolio(); setPortfolio(data); setMessage(data.customerPortfolio.nodes.length ? "Portfólio autorizado atualizado." : "Ainda não há itens publicados para sua organização."); } catch { setMessage("Este conteúdo não está disponível no momento."); } };
  if (section === "Notificações") return null;
  if (section === "Laudos") return <CustomerReportJourney inspectionId={params.get("inspectionId")} />;
  return <div className="feature"><p>Imóveis, projetos e linha do tempo não são derivados de objetos internos no navegador. Estados indisponíveis não revelam recursos não autorizados.</p><button onClick={() => void load()}>Carregar portfólio</button><p role="status">{message}</p>{portfolio && <Collection items={portfolio.customerPortfolio.nodes.map((item) => ({ id: `${item.assetId}:${item.projectId ?? ""}`, title: `${presentDashboardStatus(item.status)} · ${item.progress}%`, detail: presentClassification(item.publishedClassification)  }))} />}</div>;
}

function CustomerReportJourney({ inspectionId }: { inspectionId: string | null }) {
  const [message, setMessage] = useState("Selecione uma vistoria publicada para consultar resultado e evidências compartilhadas.");
  const [report, setReport] = useState<CustomerReportQuery["customerReport"]>(); const [evidence, setEvidence] = useState<CustomerEvidenceQuery["customerEvidence"]>();
  const [download, setDownload] = useState<ReportDownloadQuery["reportDownload"]>();
  const load = async () => { if (!inspectionId) return; try { const [nextReport, nextEvidence] = await Promise.all([graphql<CustomerReportQuery, { inspectionId: string; version: number | null }>(CustomerReportDocument, { inspectionId, version: null }), graphql<CustomerEvidenceQuery, { inspectionId: string; mode: "SIMPLE"; after: string | null }>(CustomerEvidenceDocument, { inspectionId, mode: "SIMPLE", after: null })]); setReport(nextReport.customerReport); setEvidence(nextEvidence.customerEvidence); setMessage(nextReport.customerReport ? `${presentClassification(nextReport.customerReport.classification)}. ${nextEvidence.customerEvidence.nodes.length} evidência(s) disponíveis.` : "Este resultado não está disponível no momento."); } catch { setMessage("Este resultado não está disponível no momento."); } };
  const prepareDownload = async () => { if (!report) return; try { const next = await graphql<ReportDownloadQuery, { snapshotId: string }>(ReportDownloadDocument, { snapshotId: report.snapshotId }); setDownload(next.reportDownload); } catch { setMessage("O PDF publicado não está disponível neste momento."); } };
  return <div className="feature"><button disabled={!inspectionId} onClick={() => void load()}>Abrir resultado publicado</button><p role="status">{message}</p>{report && evidence && <><CustomerReportVisual report={report} evidence={evidence.nodes} /><div className="report-visual-actions"><button onClick={() => void prepareDownload()}>Baixar PDF</button>{download?.url && <a href={download.url} target="_blank" rel="noreferrer">Abrir PDF</a>}</div></>}<p className="warning">URLs de mídia têm acesso temporário e são reautorizadas a cada abertura.</p></div>;
}

function NotificationCenter({ notifications }: { notifications: ReturnType<typeof useNotifications> }) {
  const [deliveriesOpen, setDeliveriesOpen] = useState(false);
  const showingAll = notifications.filter === "all";
  const kinds = [...new Set(notifications.items.map((item) => item.kind))];
  const projects = [...new Set(notifications.items.map((item) => String(item.context.projectId ?? "")).filter(Boolean))];
  const groups = groupNotificationsByDay(notifications.items);
  const openDeliveries = () => {
    setDeliveriesOpen(true);
    void notifications.loadDeliveries();
  };

  return <div className="feature notifications-page">
    <div className="notification-toolbar">
      <div className="notification-tabs" role="group" aria-label="Filtro de notificações">
        <button type="button" aria-pressed={!showingAll} onClick={() => notifications.changeFilter("unread")}>Não lidas ({notifications.unreadCount})</button>
        <button type="button" aria-pressed={showingAll} onClick={() => notifications.changeFilter("all")}>Todas</button>
      </div>
      <label>Tipo<select aria-label="Filtrar por tipo" value={notifications.kindFilter} onChange={(event) => notifications.changeKindFilter(event.target.value)}><option value="">Todos os tipos</option>{kinds.map((kind) => <option key={kind} value={kind}>{notificationKindLabel(kind)}</option>)}</select></label>
      {projects.length > 0 && <label>Projeto<select aria-label="Filtrar por projeto" value={notifications.projectFilter} onChange={(event) => notifications.changeProjectFilter(event.target.value)}><option value="">Todos os projetos</option>{projects.map((project) => <option key={project} value={project}>{project.slice(0, 8)}</option>)}</select></label>}
      {notifications.unreadCount > 0 && <button className="secondary" disabled={notifications.readingAll} onClick={() => void notifications.markAllRead()}>{notifications.readingAll ? "Marcando…" : "Marcar todas como lidas"}</button>}
      {notifications.deliveryVisibility && <button className="secondary notification-delivery-button" onClick={openDeliveries}>Acompanhar envios</button>}
    </div>
    {notifications.items.length ? <div aria-label="Lista de notificações">{groups.map(([label, items]) => <section className="notification-day-group" key={label}><h2>{label}</h2><ul className="notifications-list">{groupNotificationItems(items).map((group) => <NotificationCard key={group[0].id} notices={group} notifications={notifications} openDeliveries={openDeliveries} />)}</ul></section>)}</div> : <p className="notification-empty" role="status">{notifications.loadingNotifications ? "Carregando notificações…" : showingAll ? "Não há notificações." : "Não há notificações não lidas."}</p>}
    {notifications.hasMoreNotifications && <button className="secondary notification-load-more" disabled={notifications.loadingMoreNotifications} onClick={() => void notifications.loadMoreNotifications()}>{notifications.loadingMoreNotifications ? "Carregando…" : "Carregar mais"}</button>}
    <Dialog isOpen={deliveriesOpen} onClose={() => setDeliveriesOpen(false)} title="Estado das entregas">
      <div className="notification-deliveries">
        <p>Solicitada, em processamento, aceita, enviada e entregue são etapas diferentes. O status de aceite não confirma recebimento.</p>
        {notifications.loadingDeliveries ? <p role="status">Carregando entregas…</p> : notifications.deliveries.length ? <ul data-testid="notification-delivery-list">{notifications.deliveries.map((delivery) => { const presentation = deliveryPresentation(delivery.aggregateStatus, delivery.channels); return <li key={delivery.id} data-testid={`notification-delivery-${delivery.id}`}><strong data-testid={`notification-delivery-state-${delivery.id}`}>{presentation.label}</strong><p>{presentation.description}</p>{presentation.partial && <p data-testid={`notification-delivery-partial-${delivery.id}`}><strong>Entrega parcial:</strong> nem todos os canais foram confirmados.</p>}{presentation.retryExhausted && <p><strong>Tentativas esgotadas:</strong> a próxima ação exige revisão operacional.</p>}{presentation.needsReview && <p><strong>Revisão necessária:</strong> não presuma entrega nem reenvie automaticamente.</p>}<ul aria-label={`Canais da entrega ${delivery.id}`}>{delivery.channels.map((channel) => { const channelState = deliveryPresentation(channel.status, [channel]); return <li key={channel.id} data-testid={`notification-channel-${channel.id}`}><strong>{presentNotificationChannel(channel.channel)}: {channelState.label}</strong><span> · {channel.attempts} tentativa(s)</span>{channel.lastAttemptAt && <span> · última tentativa em {new Date(channel.lastAttemptAt).toLocaleString("pt-BR")}</span>}</li>; })}</ul></li>; })}</ul> : <p role="status">Não há entregas operacionais neste contexto.</p>}
        {notifications.hasMoreDeliveries && <button className="secondary" onClick={() => void notifications.loadMoreDeliveries()}>Carregar mais entregas</button>}
        <button className="secondary" onClick={() => setDeliveriesOpen(false)}>Fechar</button>
      </div>
    </Dialog>
  </div>;
}

function NotificationCard({ notices, notifications, openDeliveries }: { notices: ReturnType<typeof useNotifications>["items"]; notifications: ReturnType<typeof useNotifications>; openDeliveries: () => void }) {
  const notice = notices[0];
  const hasUnread = notices.some((item) => !item.readAt);
  const markGroupRead = () => { for (const item of notices) if (!item.readAt) void notifications.markRead(item.id); };
  const href = notificationHref(notice.action, notice.resourceId);
  return <li className="notification-card notification-card--list" data-read={hasUnread ? "false" : "true"} data-priority={notice.priority.toLowerCase()}>
    <div className="notification-card-heading"><h3>{notice.title}</h3><span className={`notification-state${hasUnread ? "" : " notification-state--read"}`}>{hasUnread ? "Não lida" : "Lida"}</span></div>
    {typeof notice.context.assetName === "string" && <strong>{notice.context.assetName}</strong>}{typeof notice.context.address === "string" && <span className="notification-address">{notice.context.address}</span>}
    <p>{notice.body}</p>{notice.dueAt && <p><strong>Prazo:</strong> {formatNotificationDate(notice.dueAt)}</p>}
    <time dateTime={notice.createdAt} title={formatNotificationDate(notice.createdAt)}>{formatNotificationRelativeDate(notice.createdAt)}</time>
    {notices.length > 1 && <details className="notification-history"><summary>{notices.length - 1} atualização(ões) anterior(es)</summary><ul>{notices.slice(1).map((item) => <li key={item.id}><strong>{item.title}</strong><p>{item.body}</p><time dateTime={item.createdAt} title={formatNotificationDate(item.createdAt)}>{formatNotificationRelativeDate(item.createdAt)}</time><span>{item.readAt ? "Lida" : "Não lida"}</span></li>)}</ul></details>}
    <div className="notification-card-actions">{href ? <Link className="secondary" href={href} onClick={markGroupRead}>{notificationActionLabel(notice.action)}</Link> : notice.action === "OPEN_DELIVERY" ? <button className="secondary" onClick={() => { markGroupRead(); openDeliveries(); }}>{notificationActionLabel(notice.action)}</button> : null}
    {hasUnread && <button className="notification-mark-read" type="button" aria-label="Marcar como lida" title="Marcar como lida" onClick={markGroupRead}><svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><path d="m3 8 3.2 3.2L13 4.5" /></svg></button>}</div>
  </li>;
}

function groupNotificationItems(items: ReturnType<typeof useNotifications>["items"]) {
  const groups = new Map<string, typeof items>();
  for (const item of items) {
    const key = item.resourceId ? `${item.kind}:${item.resourceKind}:${item.resourceId}` : item.id;
    groups.set(key, [...(groups.get(key) ?? []), item]);
  }
  return [...groups.values()];
}

function groupNotificationsByDay(items: ReturnType<typeof useNotifications>["items"]) {
  const today = new Date();
  const dateKey = (date: Date) => `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
  const yesterday = new Date(today); yesterday.setDate(yesterday.getDate() - 1);
  const groups = new Map<string, typeof items>();
  for (const item of items) {
    const date = new Date(item.createdAt);
    const key = dateKey(date) === dateKey(today) ? "Hoje" : dateKey(date) === dateKey(yesterday) ? "Ontem" : "Anteriores";
    groups.set(key, [...(groups.get(key) ?? []), item]);
  }
  return ["Hoje", "Ontem", "Anteriores"].filter((key) => groups.has(key)).map((key) => [key, groups.get(key)!] as const);
}

function notificationKindLabel(kind: string) { return ({ INSPECTION_INVITED: "Vistoria disponível", INSPECTION_CREATED: "Nova vistoria", INSPECTION_OVERDUE: "Prazo vencido", INSPECTION_DEADLINE: "Prazo próximo", TRIAGE_ASSIGNED: "Triagem atribuída", RECAPTURE_REQUESTED: "Complemento solicitado", RECAPTURE_COMPLETED: "Complemento concluído", RECAPTURE_EXPIRED: "Complemento vencido", REPORT_READY: "Laudo disponível", REPORT_PUBLISHED: "Laudo publicado", RESPONSIBLE_EMAIL_DELIVERY: "Falha no envio", SCHEDULE_CREATED: "Agenda criada", SCHEDULE_CHANGED: "Agenda alterada", SCHEDULE_CANCELED: "Agenda cancelada" } as Record<string, string>)[kind] ?? kind.replaceAll("_", " ").toLowerCase() }
function notificationActionLabel(action: string) { return ({ OPEN_INSPECTION: "Ver vistoria", OPEN_TRIAGE: "Abrir triagem", OPEN_SCHEDULE: "Ver vistoria", OPEN_REPORT: "Abrir laudo", OPEN_DELIVERY: "Acompanhar envios" } as Record<string, string>)[action] ?? "Abrir" }
function notificationHref(action: string, resourceId: string | null) {
  if (!resourceId) return null;
  if (action === "OPEN_TRIAGE") return `/triage?case=${encodeURIComponent(resourceId)}`;
  if (action === "OPEN_SCHEDULE") return `/schedules?scheduleId=${encodeURIComponent(resourceId)}`;
  if (action === "OPEN_REPORT") return `/reports?inspectionId=${encodeURIComponent(resourceId)}`;
  if (action === "OPEN_INSPECTION") return `/inspections?inspectionId=${encodeURIComponent(resourceId)}`;
  return null;
}

function formatNotificationDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Data indisponível" : date.toLocaleString("pt-BR");
}

function formatNotificationRelativeDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Data indisponível";
  const minutes = Math.round((date.getTime() - Date.now()) / 60_000);
  const relative = new Intl.RelativeTimeFormat("pt-BR", { numeric: "auto" });
  if (Math.abs(minutes) < 60) return relative.format(minutes, "minute");
  const hours = Math.round(minutes / 60);
  if (Math.abs(hours) < 24) return relative.format(hours, "hour");
  const days = Math.round(hours / 24);
  return relative.format(days, "day");
}

function Field({ label, value, onChange, type = "text", required = false }: { label: string; value: string; onChange: (value: string) => void; type?: string; required?: boolean }) { return <label>{label}<input type={type} value={value} required={required} onChange={(event) => onChange(event.target.value)} /></label>; }
function mutationId() { return globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random()}`; }
function formatMutationErrors(errors: Array<{ message: string; code: string }>) { const conflict = errors.find((error) => error.code === "CONFLICT" || error.code === "VERSION_CONFLICT"); return conflict ? `Conflito de versão: ${conflict.message} Recarregue os dados e tente novamente.` : errors.map((error) => error.message).join(" "); }
function formatFailure(error: unknown) { const failure = error as GraphQLFailure; return failure.code === "CONFLICT" || failure.code === "VERSION_CONFLICT" ? `Conflito de versão: ${failure.message} Recarregue os dados e tente novamente.` : failure.message ?? "Não foi possível concluir a operação."; }
async function requestRecapture(inspectionId: string, _version: number, setMessage: (message: string) => void) { const reason = window.prompt("Motivo do complemento"); if (!reason) return; const deadlineAt = window.prompt("Prazo final (ISO 8601)", new Date(Date.now() + 86_400_000).toISOString()); if (!deadlineAt) return; const requirementKey = window.prompt("Requisito do complemento"); if (!requirementKey) return; try { const result = await graphql(RequestRecaptureDocument, { input: { inspectionId, deadlineAt, items: [{ requirementKey, reason, originalMediaId: null }], clientMutationId: mutationId() } }); const errors = result.requestRecapture.userErrors; setMessage(errors.length ? formatMutationErrors(errors) : "Complemento solicitado; o responsável pela vistoria verá o novo requisito após a atualização."); } catch (error) { setMessage(formatFailure(error)); } }

function Collection({ items }: { items: Array<{ id: string; title: string; detail: string }> }) { return items.length ? <ul className="collection">{items.map((item) => <li key={item.id}><strong>{item.title}</strong><span>{item.detail}</span></li>)}</ul> : <p role="status">Nenhum resultado encontrado.</p>; }

function tenantInitials(name: string): string {
  const initials = name.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part[0]).join("").toUpperCase();
  return initials || "OP";
}
