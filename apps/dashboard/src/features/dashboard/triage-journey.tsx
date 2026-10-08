"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import {
  RequestRecaptureDocument, TriageAssigneesDocument, TriageCaseDocument, TriageWorkspaceDocument, UpdateTriageCaseDocument,
  type TriageCaseQuery, type TriageWorkspaceQuery, type TriageWorkspaceQueryVariables,
} from "@/graphql/generated";
import { graphql } from "@/graphql/client";
import type { Capability } from "@/features/dashboard/capabilities";
import { Dialog } from "@inspection/design-system";

type QueueItem = TriageWorkspaceQuery["triageWorkspace"]["nodes"][number];
type CaseDetail = NonNullable<TriageCaseQuery["triageCase"]>;

export function TriageJourney({ capability }: { capability: Capability }) {
  const router = useRouter(); const pathname = usePathname(); const params = useSearchParams();
  const [data, setData] = useState<TriageWorkspaceQuery["triageWorkspace"]>();
  const [detail, setDetail] = useState<CaseDetail>(); const [assignees, setAssignees] = useState<Array<{ id: string; role: string; current: boolean }>>([]);
  const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [notice, setNotice] = useState(""); const [search, setSearch] = useState(params.get("q") ?? "");
  const selected = params.get("case"); const reviewStatus = params.get("status") ?? ""; const classification = params.get("classification") ?? ""; const reason = params.get("reason") ?? ""; const assignee = params.get("assignee") ?? "";
  const canManage = capability.canManageTriage;
  const viewerId = assignees.find((item) => item.current)?.id;
  const variables = useMemo<TriageWorkspaceQueryVariables>(() => ({ first: 25, after: params.get("after"), status: reviewStatus ? reviewStatus as TriageWorkspaceQueryVariables["status"] : null, classification: classification || null, search: params.get("q") || null, assigneeId: assignee === "mine" ? "ME" : assignee === "unassigned" ? "UNASSIGNED" : assignee || null, reason: reason || null }), [params, reviewStatus, classification, assignee, reason]);
  const load = useCallback(async (nextVariables = variables) => {
    setError("");
    try { const result = await graphql(TriageWorkspaceDocument, nextVariables); setData((current) => nextVariables.after && current ? {...result.triageWorkspace,nodes:[...current.nodes,...result.triageWorkspace.nodes.filter((row)=>!current.nodes.some((item)=>item.inspectionId===row.inspectionId))]} : result.triageWorkspace); }
    catch (cause) { setError((cause as Error).message || "Não foi possível carregar a fila."); }
  }, [variables.after, variables.status, variables.classification, variables.search, variables.assigneeId, variables.reason]);
  const loadDetail = useCallback(async (id: string) => {
    setDetail(undefined);
    try { const result = await graphql(TriageCaseDocument, { inspectionId: id }); setDetail(result.triageCase ?? undefined); }
    catch (cause) { setError((cause as Error).message || "Não foi possível carregar os detalhes deste caso."); }
  }, []);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => { if (selected) void graphql(TriageAssigneesDocument, {inspectionId:selected}).then((result) => setAssignees(result.triageAssignees)).catch(() => setAssignees([])); else setAssignees([]); }, [selected]);
  useEffect(() => { if (selected) void loadDetail(selected); else setDetail(undefined); }, [selected, loadDetail]);
  useEffect(() => { const refresh = () => { void load(); if (selected) void loadDetail(selected); }; window.addEventListener("focus", refresh); return () => window.removeEventListener("focus", refresh); }, [load, loadDetail, selected]);

  function updateURL(key: string, value: string) {
    const next = new URLSearchParams(params.toString()); if (value) next.set(key, value); else next.delete(key);
    if (key !== "after" && key !== "case") next.delete("after");
    router.replace(`${pathname}${next.size ? `?${next}` : ""}`, { scroll: false });
  }
  const action = async (kind: string, values: { body?: string; assigneeId?: string; disposition?: string } = {}) => {
    if (!detail) return;
    setBusy(true); setNotice(""); setError("");
    try {
      const result = await graphql(UpdateTriageCaseDocument, { input: { inspectionId: detail.inspectionId, expectedVersion: detail.version, action: kind as never, body: values.body ?? null, assigneeId: values.assigneeId ?? null, disposition: values.disposition as never ?? null, clientMutationId: crypto.randomUUID() } });
      const payload = result.updateTriageCase;
      if (payload.userErrors.length) throw new Error(payload.userErrors.map((item) => item.message).join(" "));
      setNotice(actionMessage(kind)); await load(); await loadDetail(detail.inspectionId);
    } catch (cause) { setError((cause as Error).message || "A ação não foi concluída."); }
    finally { setBusy(false); }
  };
  const refresh = () => { void load(); if (selected) void loadDetail(selected); };
  const openCase = (item: QueueItem) => updateURL("case", item.inspectionId);
  const title = reviewStatus ? statusLabel(reviewStatus) : "Fila de revisão";
  const counts = data?.counts;
  const activeFilterCount = [classification, reviewStatus, reason, assignee].filter(Boolean).length;

  return <div className="triage-page">
    <div className="triage-intro"><div><span className="triage-kicker">CENTRAL DE QUALIDADE</span><h2>{title}</h2><p>Revise achados, confira as evidências e registre o próximo encaminhamento.</p></div><button className="secondary" type="button" onClick={refresh}>Atualizar fila</button></div>
    {notice && <p className="triage-notice" role="status">{notice}</p>}{error && <p className="warning" role="alert">{error} <button className="secondary" type="button" onClick={refresh}>Tentar novamente</button></p>}
    <div className="triage-metrics" aria-label="Resumo da triagem">
      <Metric label="Novos" value={counts?.new ?? "—"} active={reviewStatus === "NEW"} onClick={() => updateURL("status", reviewStatus === "NEW" ? "" : "NEW")} />
      <Metric label="Em revisão" value={counts?.inReview ?? "—"} active={reviewStatus === "IN_REVIEW"} onClick={() => updateURL("status", reviewStatus === "IN_REVIEW" ? "" : "IN_REVIEW")} />
      <Metric label="Aguardando complemento" value={counts?.awaitingEvidence ?? "—"} active={reviewStatus === "AWAITING_EVIDENCE"} onClick={() => updateURL("status", reviewStatus === "AWAITING_EVIDENCE" ? "" : "AWAITING_EVIDENCE")} />
      <Metric label="Críticos em aberto" value={counts?.criticalOpen ?? "—"} active={classification === "CRITICAL"} onClick={() => updateURL("classification", classification === "CRITICAL" ? "" : "CRITICAL")} danger />
    </div>
    <div className="triage-toolbar"><form onSubmit={(event) => { event.preventDefault(); updateURL("q", search.trim()); }}><label>Buscar imóvel ou vistoria<input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Nome, endereço ou código" /></label><button type="submit">Buscar</button></form>
      <details className="triage-advanced-filters"><summary>Filtros{activeFilterCount > 0 ? ` · ${activeFilterCount} ativos` : ""}</summary><div className="triage-advanced-grid">
        <label>Classificação<select value={classification} onChange={(event) => updateURL("classification", event.target.value)}><option value="">Todas</option><option value="CRITICAL">Crítica</option><option value="ATTENTION">Requer atenção</option></select></label>
        <label>Estado da revisão<select value={reviewStatus} onChange={(event) => updateURL("status", event.target.value)}><option value="">Fila ativa</option><option value="NEW">Novo</option><option value="IN_REVIEW">Em revisão</option><option value="AWAITING_EVIDENCE">Aguardando complemento</option><option value="REVIEWED">Revisado</option><option value="ARCHIVED">Arquivado</option></select></label>
        <label>Motivo<select value={reason} onChange={(event) => updateURL("reason", event.target.value)}><option value="">Todos os motivos</option>{Object.entries(reasonLabels).map(([code,label]) => <option value={code} key={code}>{label}</option>)}</select></label>
        <label>Responsável<select value={assignee} onChange={(event) => updateURL("assignee", event.target.value)}><option value="">Todos</option><option value="mine">Meus casos</option><option value="unassigned">Sem responsável</option>{assignees.filter((item) => !item.current).map((item) => <option key={item.id} value={item.id}>{roleLabel(item.role)} · {shortId(item.id)}</option>)}</select></label>
      </div></details>
    </div>
    <div className={`triage-workspace ${selected ? "triage-workspace--detail" : ""}`}>
      <section className="triage-list" aria-label="Casos para revisão"><div className="triage-list-heading"><strong>{data ? `${data.nodes.length} caso(s)` : "Carregando fila…"}</strong><span>Críticos primeiro · mais antigos no topo</span></div>
        {data?.nodes.length ? <ul>{data.nodes.map((item) => <li key={item.inspectionId}><button className={`triage-case-row ${selected === item.inspectionId ? "is-selected" : ""}`} type="button" onClick={() => openCase(item)}><span className={`triage-risk triage-risk--${item.classification.toLowerCase()}`}>{item.classification === "CRITICAL" ? "▲ Crítica" : "● Atenção"}</span><strong>{item.assetName}</strong><span className="triage-address">{item.address}</span><span className="triage-row-meta">{reasonLabels[item.reasonCodes[0]] ?? "Revisão necessária"} · {item.findingCount} achado(s)</span><span className="triage-row-foot"><span>{statusLabel(item.reviewStatus)}</span><span>{item.assigneeId ? `Revisor ${shortId(item.assigneeId)}` : "Sem responsável"}</span><time dateTime={item.createdAt}>{timeAgo(item.createdAt)}</time></span></button></li>)}</ul> : data ? <div className="triage-empty"><span aria-hidden="true">✓</span><strong>Nenhum caso nesta fila</strong><p>Altere os filtros ou atualize para consultar os casos disponíveis.</p></div> : <p role="status">Carregando casos de revisão…</p>}
        {data?.pageInfo.hasNextPage && <button className="secondary triage-more" type="button" onClick={() => updateURL("after", data.pageInfo.endCursor ?? "")}>Carregar mais casos</button>}
      </section>
      {selected && <section className="triage-detail" aria-label="Detalhe do caso">{detail ? <CasePanel detail={detail} busy={busy} canManage={canManage} canReview={capability.canMutate} viewerId={viewerId} assignees={assignees} onAction={action} onRefresh={refresh} onBack={()=>updateURL("case","")} /> : <p role="status">Carregando detalhes, laudo e evidências…</p>}</section>}
    </div>
  </div>;
}

function Metric({ label, value, active, danger, onClick }: { label: string; value: number | string; active: boolean; danger?: boolean; onClick: () => void }) {
  return <button type="button" className={`triage-metric ${active ? "is-active" : ""} ${danger ? "is-danger" : ""}`} onClick={onClick}><span>{label}</span><strong>{value}</strong></button>;
}

function CasePanel({ detail, busy, canManage, canReview, viewerId, assignees, onAction, onRefresh, onBack }: { detail: CaseDetail; busy: boolean; canManage: boolean; canReview: boolean; viewerId?: string; assignees: Array<{ id: string; role: string; current: boolean }>; onAction: (kind: string, values?: { body?: string; assigneeId?: string; disposition?: string }) => Promise<void>; onRefresh: () => void; onBack: () => void }) {
  const [tab, setTab] = useState("Resumo"); const report = detail.report;
  const ownedByMe = detail.assigneeId === viewerId; const canAct = canReview && (ownedByMe || !detail.assigneeId || canManage);
  const [note, setNote] = useState(""); const [decision, setDecision] = useState(""); const [disposition, setDisposition] = useState("NO_ACTION"); const [assigneeId, setAssigneeId] = useState("");
  const [recaptureOpen, setRecaptureOpen] = useState(false); const [deadline, setDeadline] = useState(() => localDateTimeInput(Date.now()+86_400_000));
  const [recaptureError, setRecaptureError] = useState("");
  const [selectedRequirements, setSelectedRequirements] = useState<Record<string,string>>({});
  const findings = report?.findings ?? []; const availableRequirements = report?.requirements ?? [];
  const toggleRequirement = (key: string) => setSelectedRequirements((state) => { const next = {...state}; if (key in next) delete next[key]; else next[key] = ""; return next; });
  async function submitCompletion(event: FormEvent) { event.preventDefault(); if (!decision.trim()) return; await onAction("COMPLETE", {body:decision,disposition}); setDecision(""); }
  async function submitNote(event: FormEvent) { event.preventDefault(); if (!note.trim()) return; await onAction("NOTE", {body:note}); setNote(""); }
  async function submitRecapture(event: FormEvent) {
    event.preventDefault(); const items = Object.entries(selectedRequirements).filter(([,reason]) => reason.trim()).map(([requirementKey,reason]) => ({requirementKey,reason,originalMediaId:null}));
    if (!items.length) return;
    try {
      const result = await graphql(RequestRecaptureDocument, {input:{inspectionId:detail.inspectionId,deadlineAt:new Date(deadline).toISOString(),items,clientMutationId:crypto.randomUUID()}});
      if (result.requestRecapture.userErrors.length) throw new Error(result.requestRecapture.userErrors.map((item) => item.message).join(" "));
      setRecaptureOpen(false); setSelectedRequirements({}); onRefresh();
    } catch (error) { setRecaptureError((error as Error).message || "Não foi possível solicitar o complemento."); }
  }
  return <div className="triage-case-detail"><header><div><button type="button" className="triage-back" onClick={onBack}>← Voltar à fila</button><span className={`triage-risk triage-risk--${detail.classification.toLowerCase()}`}>{detail.classification === "CRITICAL" ? "▲ Crítica" : "● Requer atenção"}</span><h3>{detail.assetName}</h3><p>{detail.address}</p></div><Link href={`/inspections?inspectionId=${encodeURIComponent(detail.inspectionId)}`}>Abrir vistoria ↗</Link></header>
    <div className="triage-case-chips"><span>{statusLabel(detail.reviewStatus)}</span><span>Vistoria · {statusLabel(detail.status)}</span><span>Laudo v{detail.reportVersion || "—"}</span><span>{detail.assigneeId ? `Revisor ${shortId(detail.assigneeId)}` : "Sem responsável"}</span></div>
    <nav className="triage-tabs" aria-label="Detalhes do caso">{["Resumo","Evidências","Histórico"].map((label) => <button type="button" className={tab===label?"is-active":""} aria-current={tab===label?"page":undefined} key={label} onClick={()=>setTab(label)}>{label}{label==="Evidências"?` (${report?.evidence.length??0})`:label==="Histórico"?` (${detail.events.length})`:""}</button>)}</nav>
    <div className="triage-tab-content">
      {tab === "Resumo" && <><div className="triage-reason-box"><strong>Por que este caso precisa de revisão?</strong><p>{detail.reasonCodes.map((code) => reasonLabels[code] ?? humanize(code)).join(" · ") || "A classificação requer uma avaliação da equipe."}</p></div><h4>Achados do laudo</h4>{findings.length ? findings.map((finding) => <article className="triage-finding" key={finding.id ?? finding.title}><span className={`triage-severity triage-severity--${finding.severity.toLowerCase()}`}>{finding.severity === "CRITICAL" ? "▲ Crítico" : finding.severity === "NONE" ? "Sem alteração" : "● Atenção"}</span><h5>{finding.title}</h5><p>{finding.description}</p><p className="triage-recommendation"><strong>Recomendação:</strong> {finding.recommendedAction}</p><small>Confiança da análise · {Math.round(finding.confidence*100)}%</small></article>) : <p>O laudo não contém achados detalhados para esta classificação.</p>}{report?.advisory && <p className="triage-advisory">{report.advisory}</p>}{report && <Link href={`/reports?inspectionId=${encodeURIComponent(detail.inspectionId)}`}>Consultar laudo completo v{report.version} →</Link>}</>}
      {tab === "Evidências" && <><p className="triage-muted">Fotos carregadas para este caso. Selecione uma imagem para ampliar.</p>{report?.evidence.length ? <div className="triage-evidence-grid">{report.evidence.map((item) => <figure key={item.id}><a href={item.url ?? undefined} target="_blank" rel="noreferrer" aria-label={`Ampliar ${item.description ?? item.requirementKey}`}>{item.url ? <img src={item.url} alt={item.description ?? item.requirementKey} loading="lazy" /> : <span className="triage-image-missing">Imagem indisponível</span>}</a><figcaption><strong>{item.description ?? item.requirementKey}</strong><small>{item.role} · {item.capturedAt ? new Date(item.capturedAt).toLocaleString("pt-BR") : "Data não informada"}</small>{item.flags.length > 0 && <small>Alertas: {item.flags.map((flag)=>humanize(flag)).join(", ")}</small>}</figcaption></figure>)}</div> : <p>Nenhuma evidência visual disponível neste laudo.</p>}</>}
      {tab === "Histórico" && <ol className="triage-history">{detail.events.length ? detail.events.map((event)=><li key={event.id}><span>{eventLabel(event.kind)}</span><time dateTime={event.createdAt}>{new Date(event.createdAt).toLocaleString("pt-BR")}</time>{event.body && <p>{event.body}</p>}<small>Responsável · {shortId(event.actorId)}</small></li>) : <li>O caso foi criado a partir da classificação e do laudo mais recentes.</li>}</ol>}
    </div>
    {canReview && <div className="triage-actions"><div className="triage-action-primary">
      {detail.reviewStatus === "NEW" && canAct && (!detail.assigneeId || detail.assigneeId === viewerId) && <button disabled={busy} onClick={()=>void onAction("TAKE")}>Assumir revisão</button>}
      {detail.reviewStatus === "IN_REVIEW" && canAct && <><button disabled={busy} onClick={()=>setRecaptureOpen(true)}>Solicitar complemento</button><button className="secondary" disabled={busy} onClick={()=>document.getElementById("triage-complete-form")?.scrollIntoView({behavior:"smooth",block:"center"})}>Concluir revisão</button></>}
      {detail.reviewStatus === "AWAITING_EVIDENCE" && <p role="status">Aguardando novas evidências. O caso retorna à revisão depois da reanálise.</p>}
      {detail.reviewStatus === "REVIEWED" && canManage && <button className="secondary" disabled={busy} onClick={()=>void onAction("REOPEN")}>Reabrir caso</button>}
    </div>
    {canManage && detail.reviewStatus !== "REVIEWED" && detail.reviewStatus !== "ARCHIVED" && <div className="triage-assign"><label>Atribuir responsável<select value={assigneeId || detail.assigneeId || ""} onChange={(event)=>setAssigneeId(event.target.value)}><option value="">Selecionar pessoa</option>{assignees.map((person)=><option key={person.id} value={person.id}>{person.current?"Você":roleLabel(person.role)} · {shortId(person.id)}</option>)}</select></label><button className="secondary" disabled={busy||!assigneeId} onClick={()=>void onAction("ASSIGN",{assigneeId})}>Atribuir</button></div>}
    {canAct && detail.reviewStatus === "IN_REVIEW" && <><form className="triage-note-form" onSubmit={(event)=>void submitNote(event)}><label>Adicionar nota<input value={note} onChange={(event)=>setNote(event.target.value)} placeholder="Registre contexto para a equipe" /></label><button className="secondary" disabled={busy||!note.trim()}>Adicionar nota</button></form><form id="triage-complete-form" className="triage-complete-form" onSubmit={(event)=>void submitCompletion(event)}><h4>Encaminhar e concluir revisão</h4><label>Encaminhamento<select value={disposition} onChange={(event)=>setDisposition(event.target.value)}><option value="NO_ACTION">Sem ação adicional</option><option value="REFERRED">Encaminhado para providência</option><option value="EXTERNAL_FOLLOWUP">Acompanhamento externo</option></select></label><label>Justificativa<textarea value={decision} onChange={(event)=>setDecision(event.target.value)} required minLength={4} rows={3} placeholder="Explique a decisão e o próximo passo" /></label><button disabled={busy||!decision.trim()}>Registrar decisão</button></form></>}
    </div>}
    <Dialog isOpen={recaptureOpen} onClose={()=>setRecaptureOpen(false)} title="Solicitar complemento"><div className="triage-recapture-dialog"><p>Selecione os requisitos e informe o que precisa ser capturado novamente.</p>{recaptureError && <p className="warning" role="alert">{recaptureError}</p>}<form onSubmit={(event)=>void submitRecapture(event)}><label>Prazo para envio<input type="datetime-local" value={deadline} min={localDateTimeInput(Date.now())} onChange={(event)=>setDeadline(event.target.value)} required /></label>{availableRequirements.length ? availableRequirements.map((requirement)=><fieldset key={requirement.key}><label><input type="checkbox" checked={requirement.key in selectedRequirements} onChange={()=>toggleRequirement(requirement.key)} />{requirement.label}</label>{requirement.key in selectedRequirements && <label>Motivo<textarea value={selectedRequirements[requirement.key]} onChange={(event)=>setSelectedRequirements((state)=>({...state,[requirement.key]:event.target.value}))} required rows={2} /></label>}</fieldset>) : <p>O laudo não informou requisitos. Consulte o laudo completo antes de solicitar novas fotos.</p>}<div className="triage-dialog-actions"><button type="button" className="secondary" onClick={()=>setRecaptureOpen(false)}>Cancelar</button><button disabled={busy||!Object.values(selectedRequirements).some((value)=>value.trim())}>Confirmar solicitação</button></div></form></div></Dialog>
    <button className="secondary triage-detail-refresh" onClick={onRefresh}>Atualizar dados</button>
  </div>;
}

const reasonLabels: Record<string,string> = { CRITICAL_FINDING:"Achado crítico", OBSERVED_CHANGE:"Alteração observada", INCONCLUSIVE:"Análise inconclusiva", ANALYSIS_FAILED:"Falha técnica na análise", MISSING_EVIDENCE:"Evidência ausente", EVIDENCE_FLAG:"Evidência com alerta", PENDING_COMPARISON:"Análise pendente", UNCORRECTED_RECAPTURE:"Complemento não corrigido", SKIPPED_STAGE:"Etapa pulada", NO_COMPARISONS:"Sem comparações disponíveis" };
function statusLabel(value:string) { return ({NEW:"Novo",IN_REVIEW:"Em revisão",AWAITING_EVIDENCE:"Aguardando complemento",REVIEWED:"Revisado",ARCHIVED:"Arquivado",COMPLETED:"Concluída",RECAPTURE_PENDING:"Complemento pendente",ANALYZING:"Em análise",SUBMITTED:"Enviada",CANCELED:"Cancelada",INVALIDATED:"Invalidada"} as Record<string,string>)[value] ?? humanize(value); }
function localDateTimeInput(timestamp:number) { const date=new Date(timestamp); date.setMinutes(date.getMinutes()-date.getTimezoneOffset()); return date.toISOString().slice(0,16); }
function roleLabel(value:string) { return ({TENANT_ADMIN:"Administradora",MANAGER:"Gestor",EMPLOYEE:"Colaborador"} as Record<string,string>)[value] ?? "Equipe"; }
function humanize(value:string) { return value.toLowerCase().split("_").map((word)=>word.charAt(0).toUpperCase()+word.slice(1)).join(" "); }
function shortId(value:string) { return value.slice(0,8); }
function timeAgo(value:string) { const minutes=Math.max(0,Math.floor((Date.now()-new Date(value).getTime())/60000)); if(minutes<60)return `${minutes} min`; const hours=Math.floor(minutes/60); if(hours<24)return `${hours} h`; return `${Math.floor(hours/24)} d`; }
function eventLabel(value:string) { return ({TAKE:"Assumiu a revisão",ASSIGN:"Alterou responsável",NOTE:"Adicionou nota",COMPLETE:"Concluiu a revisão",REOPEN:"Reabriu o caso",WAIT_FOR_EVIDENCE:"Solicitou complemento"} as Record<string,string>)[value] ?? humanize(value); }
function actionMessage(value:string) { return ({TAKE:"Você assumiu a revisão.",ASSIGN:"Responsável atualizado.",NOTE:"Nota adicionada ao histórico.",COMPLETE:"Encaminhamento registrado e revisão concluída.",REOPEN:"Caso reaberto para revisão.",WAIT_FOR_EVIDENCE:"Complemento solicitado; o caso aguardará as novas evidências."} as Record<string,string>)[value] ?? "Caso atualizado."; }
