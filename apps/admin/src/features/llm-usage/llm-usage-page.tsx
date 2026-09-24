"use client";

import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { graphql, type GraphQLFailure } from "@/graphql/client";
import * as G from "@/graphql/generated";

type Filters = {
  from: string;
  to: string;
  tenantId: string;
  tenantSearch: string;
  provider: string;
  model: string;
  modelAlias: string;
  mode: "LIVE" | "MOCK";
  technicalOutcome: string;
  state: "" | "STARTED" | "FINISHED";
  cost: "" | "INFORMED" | "MISSING";
  inspectionId: string;
};

type Usage = G.AdminLlmUsageQuery["llmUsage"];
type Call = Usage["calls"][number];
type Tenant = G.AdminLlmUsageTenantsQuery["llmUsageTenants"]["nodes"][number];

const pad = (value: number) => String(value).padStart(2, "0");
const toLocalInput = (value: string) => {
  const date = new Date(value);
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
};
const toUTC = (value: string) => new Date(value).toISOString();
const readURLDate = (value: string | null, fallback: string) => {
  if (!value) return fallback;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? fallback : toLocalInput(date.toISOString());
};
const defaultFilters = (): Filters => {
  const now = new Date();
  const start = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);
  return { from: toLocalInput(start.toISOString()), to: toLocalInput(now.toISOString()), tenantId: "", tenantSearch: "", provider: "", model: "", modelAlias: "", mode: "LIVE", technicalOutcome: "", state: "", cost: "", inspectionId: "" };
};
const readFilters = (params: URLSearchParams): Filters => {
  const defaults = defaultFilters();
  return {
    ...defaults,
    from: readURLDate(params.get("from"), defaults.from),
    to: readURLDate(params.get("to"), defaults.to),
    tenantId: params.get("tenantId") ?? "",
    tenantSearch: params.get("tenantSearch") ?? "",
    provider: params.get("provider") ?? "",
    model: params.get("model") ?? "",
    modelAlias: params.get("modelAlias") ?? "",
    mode: params.get("mode") === "MOCK" ? "MOCK" : "LIVE",
    technicalOutcome: params.get("outcome") ?? "",
    state: params.get("state") === "STARTED" || params.get("state") === "FINISHED" ? params.get("state") as Filters["state"] : "",
    cost: params.get("cost") === "INFORMED" || params.get("cost") === "MISSING" ? params.get("cost") as Filters["cost"] : "",
    inspectionId: params.get("inspectionId") ?? "",
  };
};
const failureText = (failure: unknown) => failure instanceof Error ? failure.message : (failure as GraphQLFailure).message ?? "Não foi possível carregar o consumo de LLM.";
const formatDate = (value: string) => new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "medium" }).format(new Date(value));
const formatNumber = (value: number) => new Intl.NumberFormat().format(value);
const formatCost = (value: number | null | undefined) => value == null ? "Não informado" : value.toFixed(8);
const text = (value: string | null | undefined) => value || "—";

export function LLMUsagePage({ permitted }: { permitted: boolean }) {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const urlFilters = useMemo(() => readFilters(params), [params]);
  const [draft, setDraft] = useState<Filters>(urlFilters);
  const [usage, setUsage] = useState<Usage>();
  const [tenantOptions, setTenantOptions] = useState<Tenant[]>([]);
  const [selectedTenantName, setSelectedTenantName] = useState("");
  const [detail, setDetail] = useState<Call>();
  const [loading, setLoading] = useState(false);
  const [tenantLoading, setTenantLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [refreshKey, setRefreshKey] = useState(0);
  const request = useRef<AbortController | undefined>(undefined);

  useEffect(() => { setDraft(urlFilters); }, [urlFilters]);
  useEffect(() => {
    if (!permitted || !urlFilters.tenantId) return;
    if (selectedTenantName) return;
    void graphql<G.AdminLlmUsageTenantsQuery>(G.AdminLlmUsageTenantsDocument, { search: urlFilters.tenantId, first: 25, after: null }).then((result) => {
      const match = result.llmUsageTenants.nodes.find((tenant) => tenant.id === urlFilters.tenantId);
      if (match) setSelectedTenantName(match.name);
    }).catch(() => undefined);
  }, [permitted, selectedTenantName, urlFilters.tenantId]);
  useEffect(() => {
    if (!permitted || !draft.tenantSearch.trim()) { setTenantOptions([]); return; }
    const timer = window.setTimeout(() => {
      setTenantLoading(true);
      void graphql<G.AdminLlmUsageTenantsQuery>(G.AdminLlmUsageTenantsDocument, { search: draft.tenantSearch.trim(), first: 25, after: null }).then((result) => setTenantOptions(result.llmUsageTenants.nodes)).catch(() => setTenantOptions([])).finally(() => setTenantLoading(false));
    }, 250);
    return () => window.clearTimeout(timer);
  }, [draft.tenantSearch, permitted]);
  useEffect(() => {
    if (!permitted) return;
    request.current?.abort();
    const controller = new AbortController(); request.current = controller;
    setLoading(true); setError(undefined);
    const filter = {
      from: toUTC(urlFilters.from), to: toUTC(urlFilters.to), tenantId: urlFilters.tenantId || undefined, inspectionId: urlFilters.inspectionId || undefined,
      provider: urlFilters.provider || undefined, model: urlFilters.model || undefined, modelAlias: urlFilters.modelAlias || undefined,
      mode: urlFilters.mode, technicalOutcome: urlFilters.technicalOutcome || undefined, state: urlFilters.state || undefined, cost: urlFilters.cost || undefined,
    };
    void graphql<G.AdminLlmUsageQuery>(G.AdminLlmUsageDocument, { filter, first: 25, after: params.get("after") }).then((result) => { if (!controller.signal.aborted) setUsage(result.llmUsage); }).catch((failure) => { if (!controller.signal.aborted) setError(failureText(failure)); }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [permitted, refreshKey, urlFilters, params]);

  const updateURL = (next: Filters, after?: string | null) => {
    const query = new URLSearchParams();
    query.set("from", toUTC(next.from)); query.set("to", toUTC(next.to)); query.set("mode", next.mode);
    const values: Array<[string, string]> = [["tenantId", next.tenantId], ["tenantSearch", next.tenantSearch], ["provider", next.provider], ["model", next.model], ["modelAlias", next.modelAlias], ["outcome", next.technicalOutcome], ["state", next.state], ["cost", next.cost], ["inspectionId", next.inspectionId]];
    values.forEach(([key, value]) => { if (value) query.set(key, value); });
    if (after) query.set("after", after);
    router.replace(`${pathname}?${query.toString()}`);
  };
  const submit = (event: FormEvent) => { event.preventDefault(); updateURL(draft); };
  const clear = () => { const next = defaultFilters(); setSelectedTenantName(""); updateURL(next); };
  const setFilter = <K extends keyof Filters>(key: K, value: Filters[K]) => setDraft((current) => ({ ...current, [key]: value }));
  const timezone = typeof Intl !== "undefined" ? Intl.DateTimeFormat().resolvedOptions().timeZone : "UTC";

  if (!permitted) return <div className="admin-state denied" role="alert"><h2>Acesso restrito</h2><p>O consumo de LLM está disponível somente para o super admin.</p></div>;
  return <div className="llm-usage-page">
      <div className="llm-usage-context"><span>Visão global · todos os tenants</span><span>Fuso: {timezone}</span><span>Custo informado pelo gateway</span>{usage?.coverageStartedAt && <span>Cobertura desde {formatDate(usage.coverageStartedAt)}</span>}{usage && <span>{usage.coverageComplete ? "Cobertura histórica completa" : "Cobertura histórica parcial"}</span>}</div>
    <form className="llm-usage-filters" onSubmit={submit} aria-label="Filtros de consumo de LLM">
      <label>Início<input required type="datetime-local" value={draft.from} onChange={(event) => setFilter("from", event.target.value)} /></label>
      <label>Fim<input required type="datetime-local" value={draft.to} onChange={(event) => setFilter("to", event.target.value)} /></label>
      <label>Tenant<input value={draft.tenantSearch || selectedTenantName} onChange={(event) => { setSelectedTenantName(""); setFilter("tenantSearch", event.target.value); setFilter("tenantId", ""); }} placeholder="Nome ou ID" />{tenantLoading && <small>Pesquisando…</small>}{tenantOptions.length > 0 && <span className="llm-tenant-options">{tenantOptions.map((tenant) => <button type="button" className="link-button" key={tenant.id} onClick={() => { setSelectedTenantName(tenant.name); setDraft((current) => ({ ...current, tenantId: tenant.id, tenantSearch: tenant.name })); setTenantOptions([]); }}>{tenant.name} · {tenant.id}</button>)}</span>}</label>
      <label>Provedor<input value={draft.provider} onChange={(event) => setFilter("provider", event.target.value)} /></label>
      <label>Modelo<input value={draft.model} onChange={(event) => setFilter("model", event.target.value)} /></label>
      <label>Alias do modelo<input value={draft.modelAlias} onChange={(event) => setFilter("modelAlias", event.target.value)} /></label>
      <label>Modo<select value={draft.mode} onChange={(event) => setFilter("mode", event.target.value as Filters["mode"])}><option value="LIVE">Live</option><option value="MOCK">Mock</option></select></label>
      <label>Resultado técnico<input value={draft.technicalOutcome} onChange={(event) => setFilter("technicalOutcome", event.target.value)} placeholder="success, timeout…" /></label>
      <label>Estado<select value={draft.state} onChange={(event) => setFilter("state", event.target.value as Filters["state"])}><option value="">Todos</option><option value="STARTED">Em andamento</option><option value="FINISHED">Finalizada</option></select></label>
      <label>Custo<select value={draft.cost} onChange={(event) => setFilter("cost", event.target.value as Filters["cost"])}><option value="">Todos</option><option value="INFORMED">Informado</option><option value="MISSING">Ausente</option></select></label>
      <label>Inspection ID<input value={draft.inspectionId} onChange={(event) => setFilter("inspectionId", event.target.value)} /></label>
      <div className="llm-usage-filter-actions"><button type="submit">Aplicar</button><button type="button" className="secondary" onClick={clear}>Limpar</button><button type="button" className="secondary" onClick={() => setRefreshKey((value) => value + 1)}>Atualizar</button></div>
    </form>
    <p role="status" className="status-line">{loading ? "Carregando consumo de LLM…" : error ?? ""}</p>
    {error ? <div className="admin-state error" role="alert">{error}<button onClick={() => setRefreshKey((value) => value + 1)}>Tentar novamente</button></div> : usage ? <UsageView usage={usage} loading={loading} onDetail={setDetail} onNext={(after) => updateURL(urlFilters, after)} /> : <div className="admin-state loading" role="status">Carregando consumo de LLM…</div>}
    {detail && <LLMCallDetail call={detail} onClose={() => setDetail(undefined)} />}
  </div>;
}

function UsageView({ usage, loading, onDetail, onNext }: { usage: Usage; loading: boolean; onDetail: (call: Call) => void; onNext: (after: string) => void }) {
  return <>
    <div className="llm-usage-summary" aria-label="Resumo do consumo de LLM">
      <Metric label="Tentativas" value={formatNumber(usage.attemptedCalls)} /><Metric label="Entregues" value={formatNumber(usage.deliveredCalls)} /><Metric label="Incompletas" value={formatNumber(usage.incompleteCalls)} /><Metric label="Tokens de entrada" value={formatNumber(usage.inputTokens)} /><Metric label="Tokens de saída" value={formatNumber(usage.outputTokens)} /><Metric label="Custo informado" value={formatCost(usage.knownReportedCost)} /><Metric label="Custo ausente" value={formatNumber(usage.unknownCostCalls)} />
    </div>
    {!usage.calls.length ? <div className="admin-state empty"><h2>Nenhuma chamada encontrada</h2><p>Ajuste os filtros ou aumente o período consultado.</p></div> : <><div className="table-wrap"><table><caption>Chamadas de LLM</caption><thead><tr>{["Data", "Tenant", "Inspeção", "Modelo", "Resultado", "Tokens", "Custo", "Ação"].map((column) => <th scope="col" key={column}>{column}</th>)}</tr></thead><tbody>{usage.calls.map((call) => <tr key={call.callId}><td data-label="Data">{formatDate(call.startedAt)}</td><td data-label="Tenant"><strong>{call.tenantName}</strong><br /><small className="copyable-id">{call.tenantId}</small></td><td data-label="Inspeção" className="copyable-id">{call.inspectionId}</td><td data-label="Modelo">{text(call.model)}<br /><small>{call.modelAlias}</small></td><td data-label="Resultado"><span className="admin-status-chip">{call.technicalOutcome}</span></td><td data-label="Tokens">{formatNumber(call.inputTokens ?? 0)} / {formatNumber(call.outputTokens ?? 0)}</td><td data-label="Custo">{formatCost(call.reportedCost)}</td><td data-label="Ação"><button className="link-button" onClick={() => onDetail(call)}>Abrir detalhes →</button></td></tr>)}</tbody></table></div><div className="admin-collection-foot"><span>{usage.calls.length} chamadas · {usage.costComplete ? "custo completo" : "cobertura de custo parcial"}</span><span>{loading ? "Atualizando…" : usage.pageInfo.hasNextPage ? "mais páginas disponíveis" : "fim da consulta"}</span></div>{usage.pageInfo.hasNextPage && usage.pageInfo.endCursor && <button className="next-page" onClick={() => onNext(usage.pageInfo.endCursor!)}>Carregar próxima página</button>}</>}
  </>;
}

function Metric({ label, value }: { label: string; value: string }) { return <div className="llm-usage-metric"><span>{label}</span><strong>{value}</strong></div>; }

function LLMCallDetail({ call, onClose }: { call: Call; onClose: () => void }) {
  const values: Array<[string, string]> = [["Call ID", call.callId], ["Tenant", `${call.tenantName} · ${call.tenantId}`], ["Inspection ID", call.inspectionId], ["Job ID", call.jobId], ["Event ID", call.eventId], ["Execution ID", call.executionId], ["Correlation ID", call.correlationId], ["Gateway request ID", text(call.gatewayRequestId)], ["Provedor", text(call.provider)], ["Modelo", text(call.model)], ["Alias", call.modelAlias], ["Modo", call.mode], ["Comparação", call.comparisonMode], ["Estado", call.state], ["Resultado técnico", call.technicalOutcome], ["HTTP", call.httpStatus == null ? "—" : String(call.httpStatus)], ["Tokens", `${call.inputTokens ?? "—"} entrada / ${call.outputTokens ?? "—"} saída`], ["Custo informado", formatCost(call.reportedCost)], ["Duração", call.durationMs == null ? "—" : `${call.durationMs} ms`], ["Início", formatDate(call.startedAt)], ["Fim", call.finishedAt ? formatDate(call.finishedAt) : "—"]];
  return <aside className="detail-panel" aria-label="Detalhes da chamada de LLM"><button className="secondary" onClick={onClose}>Fechar detalhe</button><h2>Detalhes da chamada</h2><dl>{values.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl></aside>;
}
