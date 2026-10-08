"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { beginPKCE, endDashboardSSO } from "@/auth/pkce";
import { clearSession, getIDToken, getMembershipId, getProtectedStateGeneration, selectMembership, setIdentity, type DashboardIdentity } from "@/auth/session";
import { composeCapabilities, type Capability } from "@/features/dashboard/capabilities";
import { formatInspectionDate, InspectionViewSelector, InspectionViews, useInspectionView, type InspectionActionHandlers, type InspectionColumnKey } from "@/features/dashboard/inspection-views";
import { SchedulesJourney } from "@/features/dashboard/schedules-journey";
import { HomeJourney } from "@/features/dashboard/home-journey";
import { FormDialog } from "@/features/dashboard/form-dialog";
import { ReportsJourney } from "@/features/dashboard/reports-journey";
import { FocusDetail } from "@/features/dashboard/focus-detail";
import { TriageJourney } from "@/features/dashboard/triage-journey";
import { ConfirmationDialog } from "@/features/dashboard/confirmation-dialog";
import { useNotifications } from "@/features/notifications/use-notifications";
import { deliveryPresentation } from "@/features/notifications/delivery-status";
import { AccountMenu, AdaptiveNavigation, Dialog, Icon, IconButton, InfoDisclosure, PageHeader, Textarea, ThemeSelector, type AdaptiveNavigationItem } from "@inspection/design-system";
import {
  AddExceptionalStageDocument, CancelInspectionDocument, CloseProjectDocument, CreateProjectDocument, PlanInspectionDocument, CustomerEvidenceDocument, CustomerPortfolioDocument, CustomerReportDocument,
  CustomerTimelineDocument, DashboardGateDocument, DashboardMembershipsDocument, InvalidateInspectionDocument, OriginPromotionDocument, PromoteInspectionPhotosDocument, ProjectDetailDocument,
  InvalidateReportPublicationDocument, ProjectsDocument, ProjectTimelineDocument, PublishReportDocument, ReopenProjectDocument, ReportDownloadDocument, RequestRecaptureDocument, InspectionDetailDocument, InspectionsDocument, SkipProjectStageDocument, StartProjectStageDocument,
  type CustomerEvidenceQuery, type CustomerPortfolioQuery, type CustomerReportQuery, type DashboardGateQuery, type DashboardMembershipsQuery, type InspectionDetailQuery, type InspectionStatusGroup, type InspectionsQuery, type InspectionsQueryVariables, type OriginPromotionQuery, type ProjectsQuery, type ReportDownloadQuery,
} from "@/graphql/generated";
import { EntityRelationshipField, useDashboardFormOptions, type FormOptionsState } from "./form-options";
import { graphql, type GraphQLFailure } from "@/graphql/client";
import { presentClassification, presentDashboardStatus, presentInspectionSource, presentNotificationChannel, presentProjectStage } from "./presentation";
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
  const [operationalRefreshKey, setOperationalRefreshKey] = useState(0);
  const gateRequest = useRef(0);

  const loadGate = useCallback(async () => {
    const request = ++gateRequest.current;
    const generation = getProtectedStateGeneration();
    const requestedMembership = getMembershipId();
    const isCurrent = () => request === gateRequest.current
      && generation === getProtectedStateGeneration()
      && requestedMembership === getMembershipId();
    try {
      if (!requestedMembership) {
        const membershipData = await graphql<DashboardMembershipsQuery, {}>(DashboardMembershipsDocument, {});
        if (!isCurrent()) return;
        const activeMemberships = membershipData.me.memberships.filter((membership) => membership.status === "ACTIVE");
        setMemberships(activeMemberships);
        if (activeMemberships.length === 1) { selectMembership(activeMemberships[0].id); setMessage("Contexto de acesso selecionado. Carregando dados protegidos…"); void loadGate(); return; }
        setCurrentIdentity(undefined); setCapability(undefined); setMessage("Selecione o contexto de acesso antes de carregar dados protegidos."); return;
      }

      // A membership stored in sessionStorage may have been revoked while the tab
      // was closed. Reconcile it against the current membership projection before
      // loading tenant-scoped data, so a stale context cannot become a protected
      // request or silently select a different tenant.
      const membershipData = await graphql<DashboardMembershipsQuery, {}>(DashboardMembershipsDocument, {});
      if (!isCurrent()) return;
      const activeMemberships = membershipData.me.memberships.filter((membership) => membership.status === "ACTIVE");
      setMemberships(activeMemberships);
      if (!activeMemberships.some((membership) => membership.id === requestedMembership)) {
        if (activeMemberships.length === 1) {
          selectMembership(activeMemberships[0].id);
          setMessage("O contexto salvo não está mais disponível. Carregando o contexto autorizado…");
          void loadGate();
          return;
        }
        clearSession();
        setCurrentIdentity(undefined);
        setCapability(undefined);
        setMessage(activeMemberships.length ? "O contexto salvo não está mais disponível. Selecione um contexto autorizado." : "Nenhum dado operacional foi carregado.");
        return;
      }
      const data = await graphql<DashboardGateQuery, {}>(DashboardGateDocument, {});
      if (!isCurrent()) return;
      const current = { tenantId: data.me.tenantId, tenantName: data.tenant?.name ?? "Imobiliária", tenantStatus: data.tenant?.status ?? "INACTIVE", entitlements: data.me.productEntitlements, roles: data.me.roles, scopes: data.me.effectiveScopes };
      if (!current.entitlements.includes("DASHBOARD") || !current.roles.length || current.tenantStatus !== "ACTIVE") {
        clearSession(); setCurrentIdentity(undefined); setCapability(undefined); setMessage("Acesso ao Painel não autorizado. Nenhum dado operacional foi carregado."); return;
      }
      setIdentity(current); setCurrentIdentity(current); setCapability(composeCapabilities(current)); setMessage(data.tenant?.name ?? "Contexto operacional ativo");
    } catch (error) {
      if (!isCurrent()) return;
      setCurrentIdentity(undefined); setCapability(undefined);
      const failure = error as GraphQLFailure;
      setMessage(["FORBIDDEN", "UNAUTHENTICATED", "TENANT_INACTIVE"].includes(failure.code ?? "") || failure.message === "Autenticação necessária"
        ? "Nenhum dado operacional foi carregado."
        : failure.message);
    }
  }, []);
  useEffect(() => { void loadGate(); }, [loadGate]);
  const notifications = useNotifications(Boolean(identity), capability?.audience === "internal");
  const signIn = () => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", pathname);
  const switchMembership = (membershipId: string) => { selectMembership(membershipId); setCurrentIdentity(undefined); setCapability(undefined); setMessage("Trocando o contexto de acesso. Dados protegidos anteriores foram removidos."); void loadGate(); };
  const signOut = () => {
    gateRequest.current += 1;
    const idToken = getIDToken();
    clearSession();
    sessionStorage.removeItem("inspection.dashboard.pkce");
    endDashboardSSO(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", idToken);
  };

  if (!identity || !capability) return <main className="denial"><aside className="dashboard-gate-story"><div className="dashboard-gate-brand"><span className="dashboard-brand-mark" aria-hidden="true">I</span><span>Inspection</span></div><div><h2>Cada vistoria, com clareza.</h2><p>Planeje vistorias e reúna evidências para decidir com segurança.</p></div><small>Painel de operações</small></aside><section className="dashboard-gate-form"><div className="denial-heading"><h1>Acesse sua conta</h1><ThemeSelector /></div><p role="status">{message}</p>{memberships.length > 0 && <MembershipPicker memberships={memberships} selected={getMembershipId()} onChange={switchMembership} />}{memberships.length === 0 && <button onClick={signIn}>Entrar no Painel</button>}<small>Português (Brasil) · Acesso seguro</small></section></main>;
  const primaryHrefs = capability.audience === "customer"
    ? capability.links.map(([, href]) => href)
    : ["/tenants/current", "/schedules", "/inspections"];
  const itemFor = ([label, href]: [string, string]): AdaptiveNavigationItem => ({
    href,
    label,
    icon: href === "/tenants/current" || href === "/portfolio" ? "layout-dashboard" : href === "/schedules" ? "calendar" : href === "/inspections" ? "clipboard-check" : href === "/projects" ? "building-2" : href === "/triage" ? "search" : href === "/reports" ? "history" : "users",
    badge: href === "/notifications" && notifications.unreadCount > 0 ? notifications.unreadCount : undefined,
  });
  const primaryItems = capability.links.filter(([, href]) => primaryHrefs.includes(href)).map(itemFor);
  const secondaryItems = capability.links.filter(([, href]) => !primaryHrefs.includes(href)).map(itemFor);
  if (capability.canUseAdmin) secondaryItems.push({ href: process.env.NEXT_PUBLIC_ADMIN_URL ?? "http://localhost:3000", label: "Abrir Administração", icon: "arrow-up-right" });
  const activeHref = pathname === "/" ? "/tenants/current" : pathname;
  const pageDescription = section === "Início" ? "Acompanhe as vistorias em andamento e os imóveis que exigem atenção." : section === "Laudos" ? "Consulte os laudos gerados das suas vistorias." : undefined;
  return <main className="dashboard-shell"><header><strong className="dashboard-brand"><span className="dashboard-brand-mark" aria-hidden="true">I</span><span className="dashboard-brand-desktop">Painel <small>/ {identity.tenantName}</small></span><span className="dashboard-brand-mobile">Inspection</span></strong><div className="dashboard-context"><ThemeSelector /><AccountMenu name={identity.tenantName} description={capability.audience === "customer" ? "Cliente" : "Operação"} onSignOut={signOut}>{memberships.length > 1 ? <MembershipPicker memberships={memberships} selected={getMembershipId()} onChange={switchMembership} /> : null}</AccountMenu></div></header><AdaptiveNavigation label="Painel" activeHref={activeHref} primaryItems={primaryItems} secondaryItems={secondaryItems} moreBadge={notifications.unreadCount > 0 ? notifications.unreadCount : undefined} renderLink={(item, className, onNavigate) => <Link key={item.href} aria-current={activeHref === item.href ? "page" : undefined} className={className} href={item.href} title={item.label} onClick={onNavigate}><span className="inspection-adaptive-navigation__icon"><Icon name={item.icon} size={22} />{item.badge ? <span className="inspection-adaptive-navigation__badge" aria-hidden="true">{item.badge}</span> : null}</span><span>{item.label}</span></Link>} /><section>{section !== "Vistorias" && <PageHeader title={section} description={pageDescription} descriptionMode="disclosure" emphasis="plain" breadcrumbs={<><Link href="/tenants/current">Painel</Link><span aria-hidden="true">/</span><span>{section}</span></>} actions={section === "Início" ? <Link className="dashboard-primary-action" href="/schedules">Ver agenda</Link> : ["Agenda de vistorias", "Projetos e etapas", "Triagem", "Notificações"].includes(section) ? null : <button className="secondary" onClick={() => { void loadGate(); void notifications.refresh(); router.refresh(); if (capability.audience === "internal" && section === "Laudos") setReportsRefreshKey((value) => value + 1); }}>Atualizar</button>} />}{notifications.error && <p className="warning" role="status">Dados já exibidos podem estar desatualizados. {notifications.error}</p>}{capability.audience === "customer" ? <CustomerPortal section={section} /> : <OperationsDashboard section={section} capability={capability} reportsRefreshKey={reportsRefreshKey} operationalRefreshKey={operationalRefreshKey} />}{section === "Notificações" && <NotificationCenter notifications={notifications} />}</section></main>;
}

function MembershipPicker({ memberships, selected, onChange }: { memberships: DashboardMembershipsQuery["me"]["memberships"]; selected?: string; onChange: (id: string) => void }) {
  return <label className="membership">Contexto de acesso<select aria-label="Contexto de acesso" value={selected ?? ""} onChange={(event) => onChange(event.target.value)}><option value="" disabled>Selecione</option>{memberships.map((membership) => <option key={membership.id} value={membership.id}>{membership.role === "CUSTOMER_VIEWER" ? "Visualizador cliente" : membership.role === "VIEWER" ? "Visualizador" : membership.role === "MANAGER" ? "Gestor" : "Perfil de acesso"} · {membership.tenantId}</option>)}</select></label>;
}

function OperationsDashboard({ section, capability, reportsRefreshKey, operationalRefreshKey }: { section: Page; capability: Capability; reportsRefreshKey: number; operationalRefreshKey: number }) {
  const needsFormOptions = section === "Agenda de vistorias" || capability.canMutate && ["Vistorias", "Projetos e etapas"].includes(section);
  const formOptions = useDashboardFormOptions(needsFormOptions);
  if (section === "Agenda de vistorias") return <SchedulesJourney canMutate={capability.canMutate} options={formOptions} refreshKey={operationalRefreshKey} />;
  if (section === "Vistorias") return <InspectionsJourney canMutate={capability.canMutate} canPromoteOrigin={capability.canPromoteOrigin} options={formOptions} />;
  if (section === "Projetos e etapas") return <ProjectsJourney canMutate={capability.canMutate} options={formOptions} />;
  if (section === "Laudos") return <ReportsJourney canPublish={capability.canPublish} refreshKey={reportsRefreshKey} />;
  if (section === "Triagem") return <TriageJourney capability={capability} />;
  if (section === "Notificações") return null;
  return <HomeJourney refreshKey={operationalRefreshKey} />;
}

function FormOptionsNotice({ options }: { options: FormOptionsState }) {
  if (options.loading) return <p role="status">Carregando imóveis, responsáveis pela vistoria e modelos de vistoria…</p>;
  if (options.error) return <p className="warning" role="alert">Não foi possível carregar as opções do cadastro. {options.error}</p>;
  return null;
}

function InspectionsJourney({ canMutate, canPromoteOrigin, options }: { canMutate: boolean; canPromoteOrigin: boolean; options: FormOptionsState }) {
  const params = useSearchParams(); const router = useRouter();
  const [data, setData] = useState<InspectionsQuery>(); const [message, setMessage] = useState("Carregando vistorias…");
  const [query, setQuery] = useState(""); const [statusFilter, setStatusFilter] = useState<InspectionColumnKey | "todas">("todas");
  const [focusedInspectionId, setFocusedInspectionId] = useState<string>();
  const selected = params.get("inspectionId"); const [view, setView] = useInspectionView();
  const [createOpen, setCreateOpen] = useState(false); const [creating, setCreating] = useState(false); const [createError, setCreateError] = useState(""); const createOpenerRef = useRef<HTMLButtonElement>(null);
  const [invalidating, setInvalidating] = useState<InspectionsQuery["inspections"]["nodes"][number]>(); const [recapturing, setRecapturing] = useState<InspectionsQuery["inspections"]["nodes"][number]>(); const [canceling, setCanceling] = useState<InspectionsQuery["inspections"]["nodes"][number]>();
  const busyRef = useRef(false); const listRequest = useRef(0); const debounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const returnToCollection = () => { const next = new URLSearchParams(params.toString()); next.delete("inspectionId"); const search = next.toString(); router.replace(search ? `/inspections?${search}` : "/inspections", { scroll: false }); };
  const openInspection = (inspection: InspectionsQuery["inspections"]["nodes"][number]) => { const next = new URLSearchParams(params.toString()); next.set("inspectionId", inspection.id); router.replace(`/inspections?${next}`, { scroll: false }); };
  const statusGroup: InspectionStatusGroup | null = ({ planejamento: "PLANNING", execucao: "EXECUTION", concluidas: "COMPLETED", encerradas: "CLOSED" } as const)[statusFilter as Exclude<typeof statusFilter, "todas">] ?? null;
  const load = useCallback(async (input: { after?: string | null; append?: boolean; immediate?: boolean; search?: string; group?: InspectionStatusGroup | null } = {}) => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    const request = ++listRequest.current;
    const variables: InspectionsQueryVariables = { first: 25, after: input.after ?? null, history: true, search: (input.search ?? query).trim() || null, statusGroup: input.group === undefined ? statusGroup : input.group };
    setMessage(input.after ? "Carregando mais vistorias…" : "Buscando vistorias…");
    try {
      const next = await graphql<InspectionsQuery, InspectionsQueryVariables>(InspectionsDocument, variables);
      if (request !== listRequest.current) return;
      setData((current) => input.append && current ? { inspections: { nodes: [...current.inspections.nodes, ...next.inspections.nodes], pageInfo: next.inspections.pageInfo } } : next);
      setMessage(next.inspections.nodes.length ? "Vistorias atualizadas." : "Nenhuma vistoria encontrada neste escopo.");
    } catch (error) {
      if (request === listRequest.current) setMessage((error as Error).message);
    }
  }, [query, statusGroup]);
  const initialLoad = useRef(false);
  useEffect(() => { if (initialLoad.current) return; initialLoad.current = true; void load({ search: "", group: null }); }, [load]);
  useEffect(() => {
    if (!data) return;
    debounceRef.current = setTimeout(() => void load({ search: query, group: statusGroup }), 250);
    return () => { if (debounceRef.current) clearTimeout(debounceRef.current); };
  }, [query, statusGroup, load]);
  const submitSearch = (event: React.FormEvent<HTMLFormElement>) => { event.preventDefault(); void load({ search: query, group: statusGroup, immediate: true }); };
  const mutate = async (document: typeof PlanInspectionDocument | typeof CancelInspectionDocument | typeof InvalidateInspectionDocument, input: Record<string, unknown>): Promise<string | undefined> => { try { const result = await graphql(document as never, { input } as never); const payload = Object.values(result as Record<string, unknown>)[0] as { userErrors?: Array<{ message: string; code: string }> }; if (payload.userErrors?.length) { const error = formatMutationErrors(payload.userErrors); setMessage(error); return error; } setMessage("Vistoria salva. Atualizando dados…"); await load(); return undefined; } catch (error) { const failure = formatFailure(error); setMessage(failure); return failure; } };
  const createInspection = async (input: Record<string, unknown>) => { if (busyRef.current) return; busyRef.current = true; setCreating(true); setCreateError(""); const error = await mutate(PlanInspectionDocument, input); busyRef.current = false; setCreating(false); if (error) setCreateError(error); else setCreateOpen(false); };
  const actions: InspectionActionHandlers | undefined = canMutate ? { onCancel: setCanceling, onInvalidate: setInvalidating, onRecapture: setRecapturing } : undefined;
  const inspections = data?.inspections.nodes ?? [];
  const focusedInspection = inspections.find((inspection) => inspection.id === focusedInspectionId) ?? inspections[0];
  return <div className="feature inspection-journey"><div className="inspection-page-heading"><div><p className="inspection-breadcrumb">Operação / Vistorias</p><InfoDisclosure label="vistorias" heading={<h1>Vistorias</h1>}>Acompanhe os registros pela lista, pela situação ou pelo prazo.</InfoDisclosure></div>{canMutate && <button ref={createOpenerRef} onClick={() => { setCreateError(""); setCreateOpen(true); }}>Nova vistoria</button>}</div>
    <div className="inspection-view-bar"><InfoDisclosure label="visualização da lista" heading={<strong>Visualização</strong>}>A troca preserva os registros e os filtros.</InfoDisclosure><InspectionViewSelector view={view} onChange={setView} /></div>
    <div className={view === "quadro" ? "inspection-focus-workspace inspection-focus-workspace--board" : "inspection-focus-workspace"}><div className="inspection-focus-list"><form className="inspection-toolbar" role="search" aria-label="Buscar vistorias" onSubmit={submitSearch}>
      <label htmlFor="inspection-search">Buscar vistoria<input id="inspection-search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Imóvel, responsável, endereço, código ou ID" /></label>
      <label className="inspection-status-filter" htmlFor="inspection-status">Situação<select id="inspection-status" value={statusFilter} onChange={(event) => setStatusFilter(event.target.value as InspectionColumnKey | "todas")}><option value="todas">Todas</option><option value="planejamento">Planejamento</option><option value="execucao">Em execução</option><option value="concluidas">Concluídas</option><option value="encerradas">Encerradas</option></select></label>
      <IconButton label="Buscar vistorias" tooltip="Buscar vistorias" icon="search" onPress={() => void load({ search: query, group: statusGroup, immediate: true })} />
    </form>
    <p className="inspection-load-status" role="status">{message}</p>
    <div id="inspection-collection" className="inspection-collection" role="region" aria-label={`Vistorias em ${view}`}>{data ? <InspectionViews inspections={inspections} view={view} actions={actions} onOpen={openInspection} focusedId={focusedInspection?.id} onFocus={setFocusedInspectionId} /> : <p className="inspection-empty-state">Carregando os registros deste contexto…</p>}</div>
    <div className="inspection-foot"><span>{inspections.length} vistoria(ões) exibida(s).</span><span>Contexto: dados da API</span></div>
    {data?.inspections.pageInfo.hasNextPage && <button className="secondary inspection-load-more" type="button" onClick={() => void load({ after: data.inspections.pageInfo.endCursor, append: true })}>Carregar mais</button>}</div>{focusedInspection && view !== "quadro" && <FocusDetail title={focusedInspection.assetName?.trim() || "Imóvel indisponível"} eyebrow={view === "agenda" ? "Prazo da vistoria" : "Vistoria selecionada"} details={[["Responsável", focusedInspection.participantName?.trim() || "Indisponível"], ["Situação", presentDashboardStatus(focusedInspection.status)], ["Evidências", String(focusedInspection.evidenceCount)], ["Vencimento", formatInspectionDate(focusedInspection.dueAt)], ["Prazo final", formatInspectionDate(focusedInspection.deadlineAt)]]} href={`/inspections?inspectionId=${encodeURIComponent(focusedInspection.id)}`} actionLabel="Abrir vistoria" />}</div>
    {selected && <Dialog isOpen onClose={returnToCollection} title="Detalhe da vistoria" size="default"><InspectionWorkspace id={selected} canPromoteOrigin={canPromoteOrigin} options={options} /></Dialog>}
    {canMutate && <FormDialog isOpen={createOpen} onClose={() => setCreateOpen(false)} restoreFocusRef={createOpenerRef} title="Nova vistoria" busy={creating} error={createError}><FormOptionsNotice options={options} />{options.data && <InspectionForm options={options} onSubmit={(input) => void createInspection(input)} busy={creating} />}</FormDialog>}
    <ConfirmationDialog isOpen={Boolean(canceling)} onClose={() => setCanceling(undefined)} title="Cancelar vistoria" target="Vistoria selecionada" scope="Contexto operacional atual" consequence="A vistoria será cancelada e não poderá continuar o fluxo atual." confirmLabel="Cancelar vistoria" onConfirm={async () => { if (!canceling) return; const failure = await mutate(CancelInspectionDocument, { inspectionId: canceling.id, expectedVersion: canceling.version, clientMutationId: mutationId() }); if (failure) throw new Error(failure); }} />
    <ConfirmationDialog isOpen={Boolean(invalidating)} onClose={() => setInvalidating(undefined)} title="Invalidar vistoria" target="Vistoria selecionada" scope="Contexto operacional atual" consequence="A vistoria será invalidada e exigirá revisão antes de qualquer novo uso." confirmLabel="Invalidar vistoria" reasonLabel="Motivo da invalidação" onConfirm={async (reason) => { if (!invalidating) return; const failure = await mutate(InvalidateInspectionDocument, { inspectionId: invalidating.id, expectedVersion: invalidating.version, reason, clientMutationId: mutationId() }); if (failure) throw new Error(failure); }} />
    <RecaptureRequestDialog inspection={recapturing} onClose={() => setRecapturing(undefined)} onSubmit={async (input) => { try { const result = await graphql(RequestRecaptureDocument, { input: input as never }); setMessage(result.requestRecapture.userErrors.length ? formatMutationErrors(result.requestRecapture.userErrors) : "Complemento solicitado; o responsável pela vistoria verá o novo requisito após a atualização."); } catch (error) { setMessage(formatFailure(error)); throw error; } }} />
  </div>;
}

function InspectionForm({ onSubmit, options, busy }: { onSubmit: (input: Record<string, unknown>) => void; options: FormOptionsState; busy: boolean }) {
  const [assetId, setAssetId] = useState(""); const [participantId, setParticipantId] = useState(""); const [templateId, setTemplateId] = useState(""); const [dueAt, setDueAt] = useState(""); const [deadlineAt, setDeadlineAt] = useState("");
  const [comparisonMode, setComparisonMode] = useState<"CHECKLIST_ONLY" | "FIXED_ORIGIN">("CHECKLIST_ONLY"); const [recurring, setRecurring] = useState(false); const [frequency, setFrequency] = useState("MONTHLY"); const [timezone] = useState("America/Sao_Paulo"); const [deadlineMinutes, setDeadlineMinutes] = useState("60");
  const [projectLink, setProjectLink] = useState("none"); const [projectName, setProjectName] = useState(""); const [stageLabel, setStageLabel] = useState(""); const [orderedStages, setOrderedStages] = useState(false);
  const [projects, setProjects] = useState<ProjectsQuery["projects"]["nodes"]>([]); const [selectedProjectId, setSelectedProjectId] = useState(""); const [selectedStageId, setSelectedStageId] = useState("");
  useEffect(() => { void graphql<ProjectsQuery, { after: string | null }>(ProjectsDocument, { after: null }).then((data) => setProjects(data.projects.nodes.filter((project) => project.status === "ACTIVE"))).catch(() => setProjects([])); }, []);
  const asset = options.data?.assets.nodes.find((item) => item.id === assetId);
  const selectedProject = projects.find((project) => project.id === selectedProjectId);
  return <form onSubmit={(event) => { event.preventDefault(); const common: Record<string, unknown> = { assetId, participantId, templateId, comparisonMode, clientMutationId: mutationId() }; if (projectLink === "new") { common.createProject = true; common.projectName = projectName; common.orderedStages = orderedStages; common.stages = stageLabel.trim() ? [{ key: "stage-1", label: stageLabel.trim(), plannedAt: null }] : []; } else if (projectLink === "existing") { common.projectId = selectedProjectId; common.stageId = selectedStageId || null; } if (recurring) { onSubmit({ ...common, startsAt: localDateTimeToInstant(dueAt), timezone, rrule: `FREQ=${frequency}`, deadlineMinutes: Number(deadlineMinutes), reminderOffsetsMinutes: [], reminderInstants: [] }); } else { onSubmit({ ...common, dueAt: localDateTimeToInstant(dueAt), deadlineAt: localDateTimeToInstant(deadlineAt), reason: "Criada pelo Painel", reminderInstants: [] }); } }}>
    <EntityRelationshipField kind="asset" label="Imóvel" value={assetId} onChange={(value) => { setAssetId(value); setParticipantId(""); setTemplateId(""); }} options={options} required />
    <EntityRelationshipField kind="participant" label="Responsável pela vistoria" value={participantId} onChange={setParticipantId} options={options} required disabled={!assetId} />
    <EntityRelationshipField kind="template" label="Modelo de vistoria" value={templateId} onChange={setTemplateId} options={options} segmentVersionId={asset?.segmentVersionId} required disabled={!assetId} />
    <label>Comparação<select value={comparisonMode} onChange={(event) => setComparisonMode(event.target.value as typeof comparisonMode)}><option value="CHECKLIST_ONLY">Somente checklist</option><option value="FIXED_ORIGIN">Comparar com fotos de referência do imóvel</option></select></label>
    <label><input type="checkbox" checked={recurring} onChange={(event) => setRecurring(event.target.checked)} /> Repetir vistoria</label>
    {recurring ? <><Field label="Primeira vistoria" value={dueAt} onChange={setDueAt} type="datetime-local" required /><label>Frequência<select value={frequency} onChange={(event) => setFrequency(event.target.value)}><option value="WEEKLY">Semanal</option><option value="MONTHLY">Mensal</option><option value="YEARLY">Anual</option></select></label><Field label="Prazo em minutos" value={deadlineMinutes} onChange={setDeadlineMinutes} type="number" required /></> : <><Field label="Vencimento" value={dueAt} onChange={setDueAt} type="datetime-local" required /><Field label="Prazo final" value={deadlineAt} onChange={setDeadlineAt} type="datetime-local" required /></>}
    <label>Projeto<select value={projectLink} onChange={(event) => { setProjectLink(event.target.value); setSelectedProjectId(""); setSelectedStageId(""); }}><option value="none">Sem projeto</option><option value="existing">Adicionar a um projeto existente</option><option value="new">Criar projeto junto</option></select></label>
    {projectLink === "existing" && <><label>Projeto<select required value={selectedProjectId} onChange={(event) => { setSelectedProjectId(event.target.value); setSelectedStageId(""); }}><option value="">Selecione</option>{projects.filter((project) => project.assetId === assetId).map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}</select></label>{selectedProject?.stages.length ? <label>Etapa (opcional)<select value={selectedStageId} onChange={(event) => setSelectedStageId(event.target.value)}><option value="">Sem etapa</option>{selectedProject.stages.filter((stage) => !["COMPLETED", "SKIPPED", "CANCELED", "INVALIDATED"].includes(stage.status)).map((stage) => <option key={stage.id} value={stage.id}>{stage.label}</option>)}</select></label> : null}</>}
    {projectLink === "new" && <><Field label="Nome do projeto" value={projectName} onChange={setProjectName} required /><Field label="Primeira etapa (opcional)" value={stageLabel} onChange={setStageLabel} /><label><input type="checkbox" checked={orderedStages} onChange={(event) => setOrderedStages(event.target.checked)} /> Exigir decisão sobre etapas anteriores</label></>}
    <button type="submit" disabled={busy}>Planejar vistoria</button>
  </form>;
}

function InspectionWorkspace({ id, canPromoteOrigin, options }: { id: string; canPromoteOrigin: boolean; options: FormOptionsState }) {
  const [message, setMessage] = useState("Carregando detalhes da vistoria…"); const [detail, setDetail] = useState<InspectionDetailQuery["inspection"]>(); const detailRequest = useRef(0);
  useEffect(() => { const request = ++detailRequest.current; setDetail(undefined); setMessage("Carregando detalhes da vistoria…"); void graphql<InspectionDetailQuery, { id: string }>(InspectionDetailDocument, { id }).then((data) => { if (request !== detailRequest.current) return; setDetail(data.inspection); setMessage(data.inspection ? "" : "Esta vistoria não está disponível neste contexto."); }).catch((error) => { if (request === detailRequest.current) setMessage((error as Error).message); }); }, [id]);
  if (!detail) return <section className="inspection-workspace"><p role="status">{message}</p></section>;
  const assetName = detail.assetName?.trim() || "Imóvel indisponível";
  const participantName = detail.participantName?.trim() || "Responsável indisponível";
  return <section className="inspection-workspace" aria-labelledby="inspection-detail-title">
    <header className="inspection-detail-heading"><div><span className="inspection-record-kicker">{presentInspectionSource(detail.source)}</span><h2 id="inspection-detail-title">{assetName}</h2></div><span className="inspection-status">{presentDashboardStatus(detail.status)}</span></header>
    <dl className="inspection-detail-grid">
      <div><dt>Responsável</dt><dd>{participantName}</dd></div>
      <div><dt>Endereço</dt><dd>{detail.assetAddress?.trim() || "Endereço indisponível"}</dd></div>
      {detail.assetExternalKey && <div><dt>Código do imóvel</dt><dd>{detail.assetExternalKey}</dd></div>}
      <div><dt>Vencimento</dt><dd>{formatInspectionDate(detail.dueAt)}</dd></div>
      <div><dt>Prazo final</dt><dd>{formatInspectionDate(detail.deadlineAt)}</dd></div>
      <div><dt>Evidências</dt><dd>{detail.evidenceCount}</dd></div>
      {detail.stateReason && <div className="inspection-detail-reason"><dt>Motivo</dt><dd>{detail.stateReason}</dd></div>}
    </dl>
    {detail.status === "COMPLETED" && <Link className="inspection-report-cta" href={`/reports?inspectionId=${encodeURIComponent(detail.id)}`}>Abrir laudo <span aria-hidden="true">→</span></Link>}
    <details className="inspection-technical-details"><summary>Dados do registro</summary><dl><div><dt>ID</dt><dd>{detail.id}</dd></div><div><dt>Versão</dt><dd>{detail.version}</dd></div></dl></details>
    {canPromoteOrigin && detail.status === "COMPLETED" && <details className="inspection-technical-details"><summary>Fotos de referência do imóvel</summary><OriginPromotionAction inspection={detail} options={options} /></details>}
  </section>;
}

function OriginPromotionAction({ inspection, options }: { inspection: InspectionsQuery["inspections"]["nodes"][number]; options: FormOptionsState }) {
  const [promotion, setPromotion] = useState<OriginPromotionQuery["originPromotion"]>(); const [selected, setSelected] = useState<string[]>([]); const [message, setMessage] = useState("Verificando se esta vistoria pode criar a referência do imóvel…"); const [confirmOpen, setConfirmOpen] = useState(false); const [promoting, setPromoting] = useState(false);
  const load = useCallback(async () => { try { const data = await graphql<OriginPromotionQuery, { inspectionId: string }>(OriginPromotionDocument, { inspectionId: inspection.id }); setPromotion(data.originPromotion); setMessage(data.originPromotion ? "Selecione as fotos que formarão a referência permanente do imóvel." : "Esta vistoria não pode criar uma referência."); } catch { setPromotion(undefined); setMessage("Esta vistoria não pode criar uma referência."); } }, [inspection.id]);
  useEffect(() => { void load(); }, [load]);
  if (!promotion) return null;
  const assetVersion = options.data?.assets.nodes.find((asset) => asset.id === inspection.assetId)?.version;
  const toggle = (id: string) => setSelected((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id]);
  const submit = async () => { if (!assetVersion || selected.length === 0 || promoting) return; setPromoting(true); try { const data = await graphql(PromoteInspectionPhotosDocument, { input: { inspectionId: inspection.id, mediaIds: selected, expectedAssetVersion: assetVersion, clientMutationId: mutationId() } }); const next = data.promoteInspectionPhotos.promotion; setPromotion(next); setMessage(next?.status === "ACTIVE" ? "Referência ativa para as próximas vistorias." : "Referência solicitada. A cópia e ativação continuam em segundo plano."); } catch (error) { const failure = formatFailure(error); setMessage(failure); throw new Error(failure); } finally { setPromoting(false); } };
  return <section className="origin-promotion"><h3>Fotos de referência</h3><p role="status">{message}</p>{promotion.status === "ACTIVE" ? <p>A referência está ativa para as próximas vistorias.</p> : <>{promotion.failureReason && <p className="warning">{promotion.failureReason}</p>}<div>{promotion.eligibleMedia.map((media) => <label key={media.id}>{media.url && <img src={media.url} alt={media.description} loading="lazy" />}<input type="checkbox" checked={selected.includes(media.id)} onChange={() => toggle(media.id)} /> {media.description}</label>)}</div><button disabled={!assetVersion || selected.length === 0 || promoting} onClick={() => setConfirmOpen(true)}>Usar fotos como referência</button></>}<ConfirmationDialog isOpen={confirmOpen} onClose={() => setConfirmOpen(false)} title="Usar fotos como referência" target={`${selected.length} foto(s) selecionada(s)`} scope="Imóvel desta vistoria" consequence="As fotos serão usadas como referência independente nas próximas vistorias." confirmLabel="Usar como referência" onConfirm={submit} /></section>;
}

function ProjectsJourney({ canMutate, options }: { canMutate: boolean; options: FormOptionsState }) {
  const [data, setData] = useState<ProjectsQuery>(); const [message, setMessage] = useState("Projetos preservam estágios e histórico ordenado."); const [createOpen, setCreateOpen] = useState(false); const [creating, setCreating] = useState(false); const [createError, setCreateError] = useState(""); const [reasonAction, setReasonAction] = useState<{ kind: "reopen" | "skip"; projectId: string; stageId?: string; version: number }>(); const busyRef = useRef(false);
  const load = async () => { try { const next = await graphql<ProjectsQuery, { after: string | null }>(ProjectsDocument, { after: null }); setData(next); setMessage(next.projects.nodes.length ? "Projetos atualizados." : "Não há projetos neste escopo."); } catch (error) { setMessage((error as Error).message); } };
  const mutate = async (document: typeof CreateProjectDocument | typeof StartProjectStageDocument | typeof SkipProjectStageDocument | typeof CloseProjectDocument | typeof ReopenProjectDocument | typeof AddExceptionalStageDocument, input: Record<string, unknown>): Promise<string | undefined> => { try { const result = await graphql(document as never, { input } as never); const payload = Object.values(result as Record<string, unknown>)[0] as { userErrors?: Array<{ message: string; code: string }> }; if (payload.userErrors?.length) { const failure = formatMutationErrors(payload.userErrors); setMessage(failure); return failure; } setMessage("Projeto salvo. Atualizando dados…"); await load(); return undefined; } catch (error) { const failure = formatFailure(error); setMessage(failure); return failure; } };
  const createProject = async (input: Record<string, unknown>) => { if (busyRef.current) return; busyRef.current = true; setCreating(true); setCreateError(""); const error = await mutate(CreateProjectDocument, input); busyRef.current = false; setCreating(false); if (error) setCreateError(error); else setCreateOpen(false); };
  return <div className="feature projects-focus">{!data?.projects.nodes.length && <div className="projects-focus-empty"><Icon name="building-2" size={28} /><h2>Seus projetos, em um só lugar</h2><p>Consulte os projetos para acompanhar as etapas e vistorias de cada imóvel.</p><div className="projects-focus-actions"><button type="button" onClick={() => void load()}>Carregar projetos</button>{canMutate && <button type="button" className="secondary" onClick={() => { setCreateError(""); setCreateOpen(true); }}>Novo projeto</button>}</div></div>}{data && data.projects.nodes.length > 0 && <div className="projects-focus-actions projects-focus-actions--loaded"><button type="button" className="secondary" onClick={() => void load()}>Atualizar projetos</button>{canMutate && <button type="button" onClick={() => { setCreateError(""); setCreateOpen(true); }}>Novo projeto</button>}</div>}<p role="status">{message}</p><div className="projects-focus-steps" aria-label="Fluxo dos projetos"><span>Projeto</span><span>Etapas</span><span>Vistorias</span><span>Laudos</span></div>{data && <ul className="collection">{data.projects.nodes.map((item) => <li key={item.id}><strong>{item.name} · {presentDashboardStatus(item.status)} · {item.stages.length} etapa(s)</strong><span>{item.orderedStages ? "Etapas ordenadas" : "Etapas independentes"} · v{item.version}</span>{canMutate && <div className="actions"><button onClick={() => void mutate(CloseProjectDocument, { projectId: item.id, expectedVersion: item.version, clientMutationId: mutationId() })}>Encerrar</button><button className="secondary" onClick={() => setReasonAction({ kind: "reopen", projectId: item.id, version: item.version })}>Reabrir</button>{item.stages.map((stage) => <span key={stage.id} className="actions"><button onClick={() => void mutate(StartProjectStageDocument, { projectId: item.id, stageId: stage.id, expectedProjectVersion: item.version, expectedStageVersion: stage.version, clientMutationId: mutationId() })}>Ativar {presentProjectStage(stage.label, stage.key)}</button><button className="secondary" onClick={() => setReasonAction({ kind: "skip", projectId: item.id, stageId: stage.id, version: item.version })}>Pular</button></span>)}</div>}</li>)}</ul>}{canMutate && <FormDialog isOpen={createOpen} onClose={() => setCreateOpen(false)} title="Novo projeto" busy={creating} error={createError}><FormOptionsNotice options={options} />{options.data && <ProjectForm options={options} onSubmit={(input) => void createProject(input)} busy={creating} />}</FormDialog>}<ConfirmationDialog isOpen={Boolean(reasonAction)} onClose={() => setReasonAction(undefined)} title={reasonAction?.kind === "skip" ? "Pular etapa" : "Reabrir projeto"} target="Projeto selecionado" scope="Contexto operacional atual" consequence={reasonAction?.kind === "skip" ? "A etapa será marcada como pulada." : "O projeto voltará ao fluxo de trabalho."} confirmLabel={reasonAction?.kind === "skip" ? "Pular etapa" : "Reabrir projeto"} reasonLabel={reasonAction?.kind === "skip" ? "Motivo do salto" : "Motivo da reabertura"} onConfirm={async (reason) => { if (!reasonAction) return; const failure = await mutate(reasonAction.kind === "skip" ? SkipProjectStageDocument : ReopenProjectDocument, reasonAction.kind === "skip" ? { projectId: reasonAction.projectId, stageId: reasonAction.stageId, expectedVersion: reasonAction.version, reason, clientMutationId: mutationId() } : { projectId: reasonAction.projectId, expectedVersion: reasonAction.version, reason, clientMutationId: mutationId() }); if (failure) throw new Error(failure); }} /></div>;
}

function ProjectForm({ onSubmit, options, busy }: { onSubmit: (input: Record<string, unknown>) => void; options: FormOptionsState; busy: boolean }) {
  const [assetId, setAssetId] = useState(""); const [name, setName] = useState(""); const [orderedStages, setOrderedStages] = useState(false);
  return <form onSubmit={(event) => { event.preventDefault(); onSubmit({ assetId, name, orderedStages, stages: [], clientMutationId: mutationId() }); }}>
    <EntityRelationshipField kind="asset" label="Imóvel" value={assetId} onChange={setAssetId} options={options} required />
    <Field label="Nome do projeto" value={name} onChange={setName} required />
    <label><input type="checkbox" checked={orderedStages} onChange={(event) => setOrderedStages(event.target.checked)} /> Etapas ordenadas</label>
    <button type="submit" disabled={busy}>Criar projeto</button>
  </form>;
}

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
  const requestRef = useRef(0);
  const load = async () => { if (!inspectionId) return; const request = ++requestRef.current; setReport(undefined); setEvidence(undefined); setDownload(undefined); try { const [nextReport, nextEvidence] = await Promise.all([graphql<CustomerReportQuery, { inspectionId: string; version: number | null }>(CustomerReportDocument, { inspectionId, version: null }), graphql<CustomerEvidenceQuery, { inspectionId: string; mode: "SIMPLE"; after: string | null }>(CustomerEvidenceDocument, { inspectionId, mode: "SIMPLE", after: null })]); if (request !== requestRef.current) return; setReport(nextReport.customerReport); setEvidence(nextEvidence.customerEvidence); setMessage(nextReport.customerReport ? `${presentClassification(nextReport.customerReport.classification)}. ${nextEvidence.customerEvidence.nodes.length} evidência(s) disponíveis.` : "Este resultado não está disponível no momento."); } catch { if (request === requestRef.current) setMessage("Este resultado não está disponível no momento."); } };
  const prepareDownload = async () => { if (!report) return; const request = requestRef.current; const snapshotId = report.snapshotId; try { const next = await graphql<ReportDownloadQuery, { snapshotId: string }>(ReportDownloadDocument, { snapshotId }); if (request !== requestRef.current) return; setDownload(next.reportDownload); } catch { if (request === requestRef.current) setMessage("O PDF publicado não está disponível neste momento."); } };
  return <div className="feature"><button disabled={!inspectionId} onClick={() => void load()}>Abrir resultado publicado</button><p role="status">{message}</p>{report && evidence && <><CustomerReportVisual report={report} evidence={evidence.nodes} /><div className="report-visual-actions"><button onClick={() => void prepareDownload()}>Baixar PDF</button>{download?.url && <a href={download.url} target="_blank" rel="noreferrer">Abrir PDF</a>}</div></>}<p className="warning">URLs de mídia têm acesso temporário e são reautorizadas a cada abertura.</p></div>;
}

function NotificationCenter({ notifications }: { notifications: ReturnType<typeof useNotifications> }) {
  const [deliveriesOpen, setDeliveriesOpen] = useState(false);
  const [focusedNoticeId, setFocusedNoticeId] = useState<string>();
  const showingAll = notifications.filter === "all";
  const kinds = [...new Set(notifications.items.map((item) => item.kind))];
  const projects = [...new Set(notifications.items.map((item) => String(item.context.projectId ?? "")).filter(Boolean))];
  const groups = groupNotificationsByDay(notifications.items);
  const focusedNotice = notifications.items.find((item) => item.id === focusedNoticeId) ?? notifications.items[0];
  const openDeliveries = () => {
    setDeliveriesOpen(true);
    void notifications.loadDeliveries();
  };

  return <div className="feature notifications-page"><div className="notification-focus-workspace"><div className="notification-focus-list">
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
    {notifications.items.length ? <div aria-label="Lista de notificações">{groups.map(([label, items]) => <section className="notification-day-group" key={label}><h2>{label}</h2><ul className="notifications-list">{groupNotificationItems(items).map((group) => <NotificationCard key={group[0].id} notices={group} notifications={notifications} openDeliveries={openDeliveries} focused={group.some((item) => item.id === focusedNotice?.id)} onFocus={setFocusedNoticeId} />)}</ul></section>)}</div> : <p className="notification-empty" role="status">{notifications.loadingNotifications ? "Carregando notificações…" : showingAll ? "Não há notificações." : "Não há notificações não lidas."}</p>}
    {notifications.hasMoreNotifications && <button className="secondary notification-load-more" disabled={notifications.loadingMoreNotifications} onClick={() => void notifications.loadMoreNotifications()}>{notifications.loadingMoreNotifications ? "Carregando…" : "Carregar mais"}</button>}</div>{focusedNotice && <FocusDetail title={focusedNotice.title} eyebrow="Notificação selecionada" details={[["Imóvel", String(focusedNotice.context.assetName ?? "Indisponível")], ["Situação", focusedNotice.readAt ? "Lida" : "Não lida"], ["Recebida", formatNotificationDate(focusedNotice.createdAt)], ...(focusedNotice.dueAt ? [["Prazo", formatNotificationDate(focusedNotice.dueAt)] as [string, string]] : [])]} href={notificationHref(focusedNotice.action, focusedNotice.resourceId)} actionLabel={notificationActionLabel(focusedNotice.action)} onAction={() => { const group = groupNotificationItems(notifications.items).find((items) => items.some((item) => item.id === focusedNotice.id)); for (const item of group ?? [focusedNotice]) if (!item.readAt) void notifications.markRead(item.id); }} />}</div>
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

function NotificationCard({ notices, notifications, openDeliveries, focused, onFocus }: { notices: ReturnType<typeof useNotifications>["items"]; notifications: ReturnType<typeof useNotifications>; openDeliveries: () => void; focused: boolean; onFocus: (id: string) => void }) {
  const notice = notices[0];
  const hasUnread = notices.some((item) => !item.readAt);
  const markGroupRead = () => { for (const item of notices) if (!item.readAt) void notifications.markRead(item.id); };
  const href = notificationHref(notice.action, notice.resourceId);
  return <li className="notification-card notification-card--list" data-focused={focused} data-read={hasUnread ? "false" : "true"} data-priority={notice.priority.toLowerCase()}>
    <div className="notification-card-heading"><h3><button className="notification-focus-select" type="button" aria-pressed={focused} onClick={() => onFocus(notice.id)}>{notice.title}</button><span className="notification-focus-mobile-title">{notice.title}</span></h3><span className={`notification-state${hasUnread ? "" : " notification-state--read"}`}>{hasUnread ? "Não lida" : "Lida"}</span></div>
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

function RecaptureRequestDialog({ inspection, onClose, onSubmit }: { inspection?: InspectionsQuery["inspections"]["nodes"][number]; onClose: () => void; onSubmit: (input: Record<string, unknown>) => Promise<void> | void }) {
  const [reason, setReason] = useState("");
  const [deadlineAt, setDeadlineAt] = useState("");
  const [requirementKey, setRequirementKey] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (inspection) { setReason(""); setDeadlineAt(""); setRequirementKey(""); setError(""); setBusy(false); } }, [inspection]);
  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!inspection) return;
    if (!reason.trim() || !deadlineAt || !requirementKey.trim()) { setError("Preencha o motivo, o prazo e o requisito antes de solicitar o complemento."); return; }
    setBusy(true);
    try {
      await onSubmit({ inspectionId: inspection.id, deadlineAt: new Date(deadlineAt).toISOString(), items: [{ requirementKey: requirementKey.trim(), reason: reason.trim(), originalMediaId: null }], clientMutationId: mutationId() });
      onClose();
    } catch (failure) { setError(formatFailure(failure)); } finally { setBusy(false); }
  };
  return <Dialog isOpen={Boolean(inspection)} isDismissable={!busy} onClose={() => { if (!busy) onClose(); }} title="Solicitar complemento">
    <form className="recapture-request-form" onSubmit={(event) => void submit(event)}>
      <p>Informe o requisito, o motivo e o prazo que serão enviados ao responsável pela vistoria.</p>
      {error && <p className="warning" role="alert">{error}</p>}
      <label>Requisito do complemento<input value={requirementKey} onChange={(event) => setRequirementKey(event.target.value)} required /></label>
      <label>Prazo final<input type="datetime-local" value={deadlineAt} onChange={(event) => setDeadlineAt(event.target.value)} required /></label>
      <label>Motivo do complemento<Textarea value={reason} onChange={(event) => setReason(event.target.value)} required rows={3} /></label>
      <div className="actions"><button type="button" className="secondary" disabled={busy} onClick={onClose}>Cancelar</button><button type="submit" disabled={busy}>Solicitar complemento</button></div>
    </form>
  </Dialog>;
}

function Collection({ items }: { items: Array<{ id: string; href?: string; title: string; detail: string }> }) { return items.length ? <ul className="collection">{items.map((item) => <li key={item.id}>{item.href ? <Link href={item.href}><strong>{item.title}</strong></Link> : <strong>{item.title}</strong>}<span>{item.detail}</span></li>)}</ul> : <p role="status">Nenhum resultado encontrado.</p>; }

function tenantInitials(name: string): string {
  const initials = name.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part[0]).join("").toUpperCase();
  return initials || "OP";
}
