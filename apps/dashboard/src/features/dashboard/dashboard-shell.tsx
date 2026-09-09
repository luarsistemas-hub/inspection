"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { beginPKCE } from "@/auth/pkce";
import { clearSession, getMembershipId, selectMembership, setIdentity, type DashboardIdentity } from "@/auth/session";
import { composeCapabilities, type Capability } from "@/features/dashboard/capabilities";
import { useNotifications } from "@/features/notifications/use-notifications";
import {
  CancelInspectionDocument, CancelScheduleDocument, CustomerEvidenceDocument, CustomerPortfolioDocument, CustomerReportDocument,
  CustomerTimelineDocument, DashboardGateDocument, DashboardMembershipsDocument, InvalidateInspectionDocument, OperationalOverviewDocument, ProjectDetailDocument,
  ProjectsDocument, ProjectTimelineDocument, ReportDownloadDocument, ReportWorkspaceDocument, SchedulesDocument, InspectionsDocument,
  type CustomerPortfolioQuery, type DashboardGateQuery, type DashboardMembershipsQuery, type InspectionsQuery, type OperationalOverviewQuery, type ProjectsQuery, type ReportWorkspaceQuery, type SchedulesQuery,
} from "@/graphql/generated";
import { graphql, type GraphQLFailure } from "@/graphql/client";

type Page = "Início" | "Agendas" | "Inspeções" | "Projetos e etapas" | "Triagem" | "Relatórios" | "Portfólio" | "Notificações";

export function DashboardShell({ section }: { section: Page }) {
  const pathname = usePathname();
  const router = useRouter();
  const [identity, setCurrentIdentity] = useState<DashboardIdentity>();
  const [capability, setCapability] = useState<Capability>();
  const [memberships, setMemberships] = useState<DashboardMembershipsQuery["memberships"]["nodes"]>([]);
  const [message, setMessage] = useState("Verificando acesso…");

  const loadGate = useCallback(async () => {
    try {
      if (!getMembershipId()) {
        const membershipData = await graphql<DashboardMembershipsQuery, { after: string | null }>(DashboardMembershipsDocument, { after: null });
        const activeMemberships = membershipData.memberships.nodes.filter((membership) => membership.status === "ACTIVE");
        setMemberships(activeMemberships);
        if (activeMemberships.length === 1) { selectMembership(activeMemberships[0].id); setMessage("Contexto de acesso selecionado. Carregando dados protegidos…"); void loadGate(); return; }
        setCurrentIdentity(undefined); setCapability(undefined); setMessage("Selecione o contexto de acesso antes de carregar dados protegidos."); return;
      }
      const data = await graphql<DashboardGateQuery, {}>(DashboardGateDocument, {});
      const current = { tenantId: data.me.tenantId, tenantName: data.tenant?.name ?? "Tenant", tenantStatus: data.tenant?.status ?? "INACTIVE", entitlements: data.me.productEntitlements, roles: data.me.roles, scopes: data.me.effectiveScopes };
      if (!current.entitlements.includes("DASHBOARD") || !current.roles.length || current.tenantStatus !== "ACTIVE") {
        clearSession(); setCurrentIdentity(undefined); setCapability(undefined); setMessage("Acesso ao Dashboard não autorizado. Nenhum dado operacional foi carregado."); return;
      }
      setIdentity(current); setCurrentIdentity(current); setCapability(composeCapabilities(current)); setMessage(data.tenant?.name ?? "Contexto operacional ativo");
    } catch (error) {
      setCurrentIdentity(undefined); setCapability(undefined);
      const failure = error as GraphQLFailure;
      setMessage(["FORBIDDEN", "UNAUTHENTICATED", "TENANT_INACTIVE"].includes(failure.code ?? "") ? "Acesso ao Dashboard não autorizado. Nenhum dado operacional foi carregado." : failure.message);
    }
  }, []);
  useEffect(() => { void loadGate(); }, [loadGate]);
  const notifications = useNotifications(Boolean(identity));
  const signIn = () => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", pathname);
  const switchMembership = (membershipId: string) => { selectMembership(membershipId); setCurrentIdentity(undefined); setCapability(undefined); setMessage("Trocando o contexto de acesso. Dados protegidos anteriores foram removidos."); void loadGate(); };

  if (!identity || !capability) return <main className="denial"><h1>Dashboard</h1><p role="status">{message}</p>{memberships.length > 0 && <MembershipPicker memberships={memberships} selected={getMembershipId()} onChange={switchMembership} />}{memberships.length === 0 && <button onClick={signIn}>Entrar no Dashboard</button>}</main>;
  return <main><header><strong>Inspeção · Dashboard</strong><span>{message}</span><div className="actions">{memberships.length > 1 && <MembershipPicker memberships={memberships} selected={getMembershipId()} onChange={switchMembership} />}{capability.canUseAdmin && <a href={process.env.NEXT_PUBLIC_ADMIN_URL ?? "http://localhost:3000"}>Abrir Administração</a>}<Link href="/notifications">Notificações ({notifications.unreadCount})</Link></div></header><nav aria-label="Dashboard">{capability.links.map(([label, href]) => <Link key={href} aria-current={pathname === href ? "page" : undefined} href={href}>{label}</Link>)}</nav><section><div className="actions"><h1>{section}</h1><button className="secondary" onClick={() => { void loadGate(); void notifications.refresh(); router.refresh(); }}>Atualizar</button></div>{notifications.error && <p className="warning" role="status">Dados já exibidos podem estar desatualizados. {notifications.error}</p>}{capability.audience === "customer" ? <CustomerPortal section={section} /> : <OperationsDashboard section={section} capability={capability} />}{section === "Notificações" && <NotificationCenter notifications={notifications} />}</section></main>;
}

function MembershipPicker({ memberships, selected, onChange }: { memberships: DashboardMembershipsQuery["memberships"]["nodes"]; selected?: string; onChange: (id: string) => void }) {
  return <label className="membership">Contexto de acesso<select aria-label="Contexto de acesso" value={selected ?? ""} onChange={(event) => onChange(event.target.value)}><option value="" disabled>Selecione</option>{memberships.map((membership) => <option key={membership.id} value={membership.id}>{membership.role} · {membership.tenantId}</option>)}</select></label>;
}

function OperationsDashboard({ section, capability }: { section: Page; capability: Capability }) {
  if (section === "Agendas") return <SchedulesJourney canMutate={capability.canMutate} />;
  if (section === "Inspeções") return <InspectionsJourney canMutate={capability.canMutate} />;
  if (section === "Projetos e etapas") return <ProjectsJourney canMutate={capability.canMutate} />;
  if (section === "Relatórios") return <ReportsJourney canPublish={capability.canPublish} />;
  if (section === "Notificações") return null;
  return <OverviewJourney />;
}

function OverviewJourney() {
  const [classification, setClassification] = useState(""); const [status, setStatus] = useState(""); const [data, setData] = useState<OperationalOverviewQuery>(); const [message, setMessage] = useState("Carregue o resumo e a fila no contexto autorizado.");
  async function loadOverview(): Promise<OperationalOverviewQuery> { return graphql<OperationalOverviewQuery, { after: string | null; classification: string | null; status: string | null }>(OperationalOverviewDocument, { after: null, classification: classification || null, status: status || null }); }
  const load = async () => { try { const next = await loadOverview(); setData(next); setMessage(next.triageInspections.nodes.length ? "Resumo e fila atualizados com o mesmo filtro." : "Não há itens para os filtros selecionados."); } catch (error) { setMessage(`Dados exibidos podem estar desatualizados. ${(error as Error).message}`); } };
  return <div className="feature"><p>Resumo e triagem usam o mesmo filtro e contexto de servidor. A fila é somente leitura para VIEWER; a autorização é sempre revalidada no servidor.</p><div className="filters"><label>Classificação<select value={classification} onChange={(event) => setClassification(event.target.value)}><option value="">Todas</option><option value="CRITICAL">Crítica</option><option value="ATTENTION">Atenção</option></select></label><label>Status<input value={status} onChange={(event) => setStatus(event.target.value)} placeholder="Ex.: PENDING" /></label><button onClick={() => void load()}>Atualizar prioridades</button></div><p role="status">{message}</p>{data && <><div className="cards">{Object.entries(data.dashboardSummary).map(([name, value]) => <article className="card" key={name}><strong>{name}</strong><div>{value}</div></article>)}</div><Collection items={data.triageInspections.nodes.map((item) => ({ id: item.inspectionId, title: `${item.classification} · ${item.status}`, detail: `Atualizado em ${item.updatedAt}` }))} /></>}</div>;
}

function SchedulesJourney({ canMutate }: { canMutate: boolean }) {
  const [data, setData] = useState<SchedulesQuery>(); const [message, setMessage] = useState("Agendas futuras preservam inspeções históricas após cancelamento.");
  const load = async () => { try { const next = await graphql<SchedulesQuery, { after: string | null }>(SchedulesDocument, { after: null }); setData(next); setMessage(next.schedules.nodes.length ? "Agendas atualizadas." : "Nenhuma agenda encontrada. Crie uma agenda após configurar dependências elegíveis."); } catch (error) { setMessage((error as Error).message); } };
  return <div className="feature"><p>Criação e atualização mostram recorrência, prazo, lembretes, próxima ocorrência e versão. Conflitos preservam os valores enviados para recuperação.</p><button onClick={() => void load()}>Carregar agendas</button><p role="status">{message}</p>{data && <Collection items={data.schedules.nodes.map((item) => ({ id: item.id, title: `${item.status} · próxima: ${item.nextDueAt}`, detail: `${item.rrule} · ${item.timezone} · v${item.version}` }))} />}{canMutate && <p className="warning">Use a confirmação de cancelamento no detalhe para interromper gerações futuras sem apagar inspeções anteriores.</p>}</div>;
}

function InspectionsJourney({ canMutate }: { canMutate: boolean }) {
  const params = useSearchParams(); const [data, setData] = useState<InspectionsQuery>(); const [message, setMessage] = useState("Coleção, histórico e detalhe conservam cursores no servidor."); const selected = params.get("inspectionId");
  const load = async () => { try { const next = await graphql<InspectionsQuery, { after: string | null; history: boolean }>(InspectionsDocument, { after: null, history: true }); setData(next); setMessage(next.inspections.nodes.length ? "Inspeções atualizadas." : "Nenhuma inspeção encontrada neste escopo."); } catch (error) { setMessage((error as Error).message); } };
  return <div className="feature"><button onClick={() => void load()}>Carregar inspeções</button><p role="status">{message}</p>{data && <Collection items={data.inspections.nodes.map((item) => ({ id: item.id, title: `${item.status} · ${item.source}`, detail: `${item.evidenceCount} evidência(s) · prazo ${item.deadlineAt} · v${item.version}` }))} />}{selected && <InspectionWorkspace id={selected} canMutate={canMutate} />}</div>;
}

function InspectionWorkspace({ id, canMutate }: { id: string; canMutate: boolean }) {
  const [message, setMessage] = useState("Carregando estado atual da inspeção…"); const [detail, setDetail] = useState<InspectionsQuery["inspections"]["nodes"][number]>();
  async function loadDetail(): Promise<InspectionsQuery> { return graphql<InspectionsQuery, { after: string | null; history: boolean }>(InspectionsDocument, { after: null, history: true }); }
  useEffect(() => { void loadDetail().then((data) => { const inspection = data.inspections.nodes.find((item) => item.id === id); setDetail(inspection); setMessage(inspection ? "Estado atual carregado." : "A inspeção não está disponível neste contexto."); }).catch((error) => setMessage((error as Error).message)); }, [id]);
  return <article className="card"><h2>Detalhe da inspeção</h2><p role="status">{message}</p>{detail && <dl><dt>Status</dt><dd>{detail.status}</dd><dt>Evidências</dt><dd>{detail.evidenceCount}</dd><dt>Motivo</dt><dd>{detail.stateReason ?? "—"}</dd><dt>Versão</dt><dd>{detail.version}</dd></dl>}{canMutate && <p>Cancelamento e invalidação exigem versão atual, identidade de mutação e, na invalidação, motivo. Após interrupção, recarregue este detalhe para confirmar o resultado.</p>}</article>;
}

function ProjectsJourney({ canMutate }: { canMutate: boolean }) {
  const [data, setData] = useState<ProjectsQuery>(); const [message, setMessage] = useState("Projetos preservam estágios e histórico ordenado.");
  const load = async () => { try { const next = await graphql<ProjectsQuery, { after: string | null }>(ProjectsDocument, { after: null }); setData(next); setMessage(next.projects.nodes.length ? "Projetos atualizados." : "Não há projetos neste escopo."); } catch (error) { setMessage((error as Error).message); } };
  return <div className="feature"><p>Crie projetos com ativo, participante e template. Etapas excepcionais, início, salto, encerramento e reabertura usam versões esperadas e razões quando exigidas.</p><button onClick={() => void load()}>Carregar projetos</button><p role="status">{message}</p>{data && <Collection items={data.projects.nodes.map((item) => ({ id: item.id, title: `${item.status} · ${item.stages.length} etapa(s)`, detail: `${item.reportMode} · v${item.version}` }))} />}{canMutate && <p className="warning">Transições fora de ordem e versões desatualizadas são exibidas como conflito recuperável, sem sobrescrever o estado mais recente.</p>}</div>;
}

function ReportsJourney({ canPublish }: { canPublish: boolean }) {
  const params = useSearchParams(); const inspectionId = params.get("inspectionId"); const [message, setMessage] = useState("Selecione uma inspeção para abrir o relatório versionado."); const [data, setData] = useState<ReportWorkspaceQuery | null>();
  async function load(): Promise<ReportWorkspaceQuery | null> { return inspectionId ? graphql<ReportWorkspaceQuery, { inspectionId: string; version: number | null }>(ReportWorkspaceDocument, { inspectionId, version: null }) : null; }
  const open = async () => { try { const next = await load(); setData(next); setMessage(next?.report ? "Versão legível, conteúdo canônico e renderização carregados." : "Relatório ainda não está pronto ou não está disponível."); } catch (error) { setMessage((error as Error).message); } };
  return <div className="feature"><p>Downloads são somente PDF, com status observável, digest e expiração. Publicação e invalidação são versões separadas e a invalidação remove o acesso do cliente imediatamente.</p><button disabled={!inspectionId} onClick={() => void open()}>Abrir relatório</button><p role="status">{message}</p>{data?.report && <article className="card"><strong>{data.report.classification} · versão {data.report.version}</strong><p>Digest canônico: {data.report.jsonDigest}</p><p>Digest renderizado: {data.report.htmlDigest}</p><details><summary>Conteúdo canônico</summary><pre>{JSON.stringify(data.report.canonicalJSON, null, 2)}</pre></details></article>}{canPublish && <p className="warning">Antes de publicar ou invalidar, a confirmação informa alvo, versão, efeito no cliente e razão. Após erro ou interrupção, recarregue o estado publicado.</p>}</div>;
}

function CustomerPortal({ section }: { section: Page }) {
  const params = useSearchParams(); const [message, setMessage] = useState("Esta experiência usa somente projeções de cliente aprovadas pelo servidor."); const [portfolio, setPortfolio] = useState<CustomerPortfolioQuery>();
  async function loadPortfolio(): Promise<CustomerPortfolioQuery> { return graphql<CustomerPortfolioQuery, { after: string | null }>(CustomerPortfolioDocument, { after: null }); }
  const load = async () => { try { const data = await loadPortfolio(); setPortfolio(data); setMessage(data.customerPortfolio.nodes.length ? "Portfólio autorizado atualizado." : "Ainda não há itens publicados para sua organização."); } catch { setMessage("Este conteúdo não está disponível no momento."); } };
  if (section === "Notificações") return null;
  if (section === "Relatórios") return <CustomerReportJourney inspectionId={params.get("inspectionId")} />;
  return <div className="feature"><p>Ativos, projetos e linha do tempo não são derivados de objetos internos no navegador. Estados indisponíveis não revelam recursos não autorizados.</p><button onClick={() => void load()}>Carregar portfólio</button><p role="status">{message}</p>{portfolio && <Collection items={portfolio.customerPortfolio.nodes.map((item) => ({ id: `${item.assetId}:${item.projectId ?? ""}`, title: `${item.status} · ${item.progress}%`, detail: item.publishedClassification ?? "Resultado ainda não publicado" }))} />}</div>;
}

function CustomerReportJourney({ inspectionId }: { inspectionId: string | null }) {
  const [message, setMessage] = useState("Selecione uma inspeção publicada para consultar resultado e evidências compartilhadas.");
  const load = async () => { if (!inspectionId) return; try { const [report, evidence] = await Promise.all([graphql(CustomerReportDocument, { inspectionId, version: null }), graphql(CustomerEvidenceDocument, { inspectionId, mode: "SIMPLE", after: null })]); setMessage(report.customerReport ? `${report.customerReport.classification}. ${evidence.customerEvidence.nodes.length} evidência(s) explicitamente compartilhada(s).` : "Este resultado não está disponível no momento."); } catch { setMessage("Este resultado não está disponível no momento."); } };
  return <div className="feature"><button disabled={!inspectionId} onClick={() => void load()}>Abrir resultado publicado</button><p role="status">{message}</p><p className="warning">URLs de mídia têm acesso temporário e são reautorizadas. Conteúdo invalidado, substituído ou indisponível não reaparece por uma versão anterior.</p></div>;
}

function NotificationCenter({ notifications }: { notifications: ReturnType<typeof useNotifications> }) {
  const [unreadOnly, setUnreadOnly] = useState(false);
  const visible = notifications.items.filter((item) => !unreadOnly || !item.readAt);
  return <div className="feature"><label><input type="checkbox" checked={unreadOnly} onChange={(event) => setUnreadOnly(event.target.checked)} /> Mostrar apenas não lidas</label>{visible.length ? <ul>{visible.map((notice) => <li key={notice.id}><strong>{notice.title}</strong><p>{notice.body}</p>{notice.readAt ? "Lida" : <button onClick={() => void notifications.markRead(notice.id)}>Marcar como lida</button>}</li>)}</ul> : <p role="status">Não há notificações neste filtro.</p>}<p>Links são autorizados novamente ao abrir. Preferências externas aceitam somente destinos verificados; avisos obrigatórios no produto permanecem ativos.</p></div>;
}

function Collection({ items }: { items: Array<{ id: string; title: string; detail: string }> }) { return items.length ? <ul className="collection">{items.map((item) => <li key={item.id}><strong>{item.title}</strong><span>{item.detail}</span></li>)}</ul> : <p role="status">Nenhum resultado encontrado.</p>; }
