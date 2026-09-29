"use client";

import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Button, DataTable, Dialog, Field, IconButton, Input, Recovery, Select, Status } from "@inspection/design-system";
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
const formatDate = (value: string) => new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(new Date(value));
const formatNumber = (value: number) => new Intl.NumberFormat().format(value);
/** Keeps unavailable gateway cost distinct from a confirmed zero-cost result. */
export const formatReportedCost = (value: number | null | undefined) => value == null ? "Não informado" : value.toFixed(8);
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
  const activeAdvancedFilterCount = [urlFilters.tenantId, urlFilters.provider, urlFilters.model, urlFilters.modelAlias, urlFilters.mode === "MOCK" ? urlFilters.mode : "", urlFilters.technicalOutcome, urlFilters.state, urlFilters.cost, urlFilters.inspectionId].filter(Boolean).length;

  if (!permitted) return <Recovery kind="denied" title="Acesso restrito">O consumo de LLM está disponível somente para o super admin.</Recovery>;
  return <div className="llm-usage-page">
      <div className="llm-usage-context"><span>{urlFilters.tenantId ? "Visão global · tenant filtrado" : "Visão global · todos os tenants"}</span><span>Fuso: {timezone}</span><span>Custo informado pelo gateway</span>{usage?.coverageStartedAt && <span>Cobertura desde {formatDate(usage.coverageStartedAt)}</span>}{usage && <span>{usage.coverageComplete ? "Cobertura histórica completa" : "Cobertura histórica parcial"}</span>}</div>
    <form className="llm-usage-filters" onSubmit={submit} aria-label="Filtros de consumo de LLM">
      <Field label="Início" required><Input required type="datetime-local" value={draft.from} onChange={(event) => setFilter("from", event.target.value)} /></Field>
      <Field label="Fim" required error={draft.from > draft.to ? "O início deve ocorrer antes do fim." : undefined}><Input required type="datetime-local" value={draft.to} onChange={(event) => setFilter("to", event.target.value)} /></Field>
      <details className="llm-usage-advanced-filters"><summary>Filtros avançados{activeAdvancedFilterCount > 0 ? ` · ${activeAdvancedFilterCount} aplicados` : ""}</summary><div className="llm-usage-advanced-grid">
        <Field label="Tenant"><Input value={draft.tenantSearch || selectedTenantName} onChange={(event) => { setSelectedTenantName(""); setFilter("tenantSearch", event.target.value); setFilter("tenantId", ""); }} placeholder="Nome ou ID" />{tenantLoading && <small>Pesquisando…</small>}{tenantOptions.length > 0 && <span className="llm-tenant-options">{tenantOptions.map((tenant) => <Button type="button" variant="secondary" key={tenant.id} onClick={() => { setSelectedTenantName(tenant.name); setDraft((current) => ({ ...current, tenantId: tenant.id, tenantSearch: tenant.name })); setTenantOptions([]); }}>{tenant.name} · {tenant.id}</Button>)}</span>}</Field>
        <Field label="Provedor"><Input value={draft.provider} onChange={(event) => setFilter("provider", event.target.value)} /></Field>
        <Field label="Modelo"><Input value={draft.model} onChange={(event) => setFilter("model", event.target.value)} /></Field>
        <Field label="Alias do modelo"><Input value={draft.modelAlias} onChange={(event) => setFilter("modelAlias", event.target.value)} /></Field>
        <Field label="Modo"><Select value={draft.mode} onChange={(event) => setFilter("mode", event.target.value as Filters["mode"])}><option value="LIVE">Live</option><option value="MOCK">Mock</option></Select></Field>
        <Field label="Resultado técnico"><Input value={draft.technicalOutcome} onChange={(event) => setFilter("technicalOutcome", event.target.value)} placeholder="success, timeout…" /></Field>
        <Field label="Estado"><Select value={draft.state} onChange={(event) => setFilter("state", event.target.value as Filters["state"])}><option value="">Todos</option><option value="STARTED">Em andamento</option><option value="FINISHED">Finalizada</option></Select></Field>
        <Field label="Custo"><Select value={draft.cost} onChange={(event) => setFilter("cost", event.target.value as Filters["cost"])}><option value="">Todos</option><option value="INFORMED">Informado</option><option value="MISSING">Ausente</option></Select></Field>
        <Field label="Inspection ID"><Input value={draft.inspectionId} onChange={(event) => setFilter("inspectionId", event.target.value)} /></Field>
      </div></details>
      <div className="llm-usage-filter-actions"><Button type="submit" disabled={draft.from > draft.to}>Aplicar</Button><Button type="button" variant="secondary" onClick={clear}>Limpar</Button><Button type="button" variant="secondary" onClick={() => setRefreshKey((value) => value + 1)}>Atualizar</Button></div>
    </form>
    <p role="status" className="status-line">{loading ? "Carregando consumo de LLM…" : error ?? ""}</p>
    {error ? <Recovery kind="error" title="Não foi possível carregar o consumo de LLM" onRetry={() => setRefreshKey((value) => value + 1)}>{error}</Recovery> : usage ? <UsageView usage={usage} loading={loading} onDetail={setDetail} onNext={(after) => updateURL(urlFilters, after)} /> : <Recovery kind="loading" title="Carregando consumo de LLM">A consulta atual será aplicada quando a resposta estiver disponível.</Recovery>}
    {detail && <LLMCallDetail call={detail} onClose={() => setDetail(undefined)} />}
  </div>;
}

function UsageView({ usage, loading, onDetail, onNext }: { usage: Usage; loading: boolean; onDetail: (call: Call) => void; onNext: (after: string) => void }) {
  const cacheHitRate = usage.knownCacheCalls > 0 ? `${((usage.cacheHitCalls / usage.knownCacheCalls) * 100).toFixed(1)}%` : "Não informado";
  return <>
    <div className="llm-usage-summary" aria-label="Resumo do consumo de LLM">
      <Metric label="Tentativas" value={formatNumber(usage.attemptedCalls)} /><Metric label="Entregues" value={formatNumber(usage.deliveredCalls)} /><Metric label="Incompletas" value={formatNumber(usage.incompleteCalls)} /><Metric label="Tokens de entrada" value={formatNumber(usage.inputTokens)} /><Metric label="Tokens em cache" value={usage.knownCacheCalls > 0 ? formatNumber(usage.cachedInputTokens) : "Não informado"} /><Metric label="Chamadas com acerto" value={formatNumber(usage.cacheHitCalls)} /><Metric label="Taxa de acerto (informadas)" value={cacheHitRate} /><Metric label="Cache não informado" value={formatNumber(usage.unknownCacheCalls)} /><Metric label="Tokens de saída" value={formatNumber(usage.outputTokens)} /><Metric label="Custo informado" value={formatReportedCost(usage.knownReportedCost)} /><Metric label="Custo ausente" value={formatNumber(usage.unknownCostCalls)} />
    </div>
    {!usage.calls.length ? <Recovery kind="empty" title="Nenhuma chamada encontrada">Ajuste os filtros ou aumente o período consultado.</Recovery> : <><DataTable density="compact" caption="Chamadas de LLM" mobileLabel="Chamadas de LLM" columns={[{ id: "Data", label: "Data" }, { id: "Tenant", label: "Tenant" }, { id: "Inspeção", label: "Inspeção" }, { id: "Modelo", label: "Modelo" }, { id: "Resultado", label: "Resultado" }, { id: "Tokens", label: "Tokens", align: "end" }, { id: "Custo", label: "Custo", align: "end" }, { id: "Ação", label: "Ação", align: "center", action: true }]}>{usage.calls.map((call) => <tr key={call.callId}><td data-label="Data">{formatDate(call.startedAt)}</td><td data-label="Tenant"><strong>{call.tenantName}</strong><br /><small className="copyable-id" title={call.tenantId}>{call.tenantId.length > 24 ? `${call.tenantId.slice(0, 10)}…${call.tenantId.slice(-6)}` : call.tenantId}</small></td><td data-label="Inspeção" className="copyable-id"><span className="inspection-compact-text" title={call.inspectionId}>{call.inspectionId.slice(0, 12)}…{call.inspectionId.slice(-8)}</span></td><td data-label="Modelo">{text(call.model)}<br /><small>{call.modelAlias}</small></td><td data-label="Resultado"><Status>{call.technicalOutcome}</Status></td><td data-label="Tokens" data-align="end">{formatNumber(call.inputTokens ?? 0)} / {formatNumber(call.outputTokens ?? 0)}<br /><small>Cache: {call.cachedInputTokens == null ? "Não informado" : formatNumber(call.cachedInputTokens)}</small></td><td data-label="Custo" data-align="end">{formatReportedCost(call.reportedCost)}</td><td data-label="Ação" data-align="center" data-action><IconButton label={`Abrir detalhes da chamada de ${call.tenantName}`} tooltip="Abrir detalhes" icon="eye" onPress={() => onDetail(call)} /></td></tr>)}</DataTable><ul className="admin-mobile-collection llm-mobile-collection" aria-label="Chamadas de LLM">{usage.calls.map((call) => <li key={`mobile-${call.callId}`}><article><div className="admin-mobile-row"><div><h2>{call.tenantName}</h2><p>{text(call.model)} · {call.modelAlias}</p></div><IconButton label={`Abrir detalhes da chamada de ${call.tenantName}`} tooltip="Abrir detalhes" icon="eye" onPress={() => onDetail(call)} /></div><dl><div><dt>Resultado</dt><dd><Status>{call.technicalOutcome}</Status></dd></div><div><dt>Data</dt><dd>{formatDate(call.startedAt)}</dd></div><div><dt>Custo</dt><dd>{formatReportedCost(call.reportedCost)}</dd></div></dl></article></li>)}</ul><div className="admin-collection-foot"><span>{usage.calls.length} chamadas · {usage.costComplete ? "custo completo" : "cobertura de custo parcial"}</span><span>{loading ? "Atualizando…" : usage.pageInfo.hasNextPage ? "mais páginas disponíveis" : "fim da consulta"}</span></div>{usage.pageInfo.hasNextPage && usage.pageInfo.endCursor && <Button variant="secondary" onClick={() => onNext(usage.pageInfo.endCursor!)}>Carregar próxima página</Button>}</>}
  </>;
}

function Metric({ label, value }: { label: string; value: string }) { return <div className="llm-usage-metric"><span>{label}</span><strong>{value}</strong></div>; }

function LLMCallDetail({ call, onClose }: { call: Call; onClose: () => void }) {
  const values: Array<[string, string]> = [["Call ID", call.callId], ["Tenant", `${call.tenantName} · ${call.tenantId}`], ["Inspection ID", call.inspectionId], ["Job ID", call.jobId], ["Event ID", call.eventId], ["Execution ID", call.executionId], ["Correlation ID", call.correlationId], ["Gateway request ID", text(call.gatewayRequestId)], ["Provedor", text(call.provider)], ["Modelo", text(call.model)], ["Alias", call.modelAlias], ["Modo", call.mode], ["Comparação", call.comparisonMode], ["Estado", call.state], ["Resultado técnico", call.technicalOutcome], ["HTTP", call.httpStatus == null ? "—" : String(call.httpStatus)], ["Tokens", `${call.inputTokens ?? "—"} entrada / ${call.outputTokens ?? "—"} saída`], ["Tokens de entrada em cache", call.cachedInputTokens == null ? "Não informado" : formatNumber(call.cachedInputTokens)], ["Imagens", call.imageCount == null ? "Não informado" : formatNumber(call.imageCount)], ["Corpo enviado", call.requestBodyBytes == null ? "Não informado" : `${formatNumber(call.requestBodyBytes)} bytes`], ["Custo informado", formatReportedCost(call.reportedCost)], ["Duração", call.durationMs == null ? "—" : `${call.durationMs} ms`], ["Início", formatDate(call.startedAt)], ["Fim", call.finishedAt ? formatDate(call.finishedAt) : "—"]];
  return <Dialog isOpen onClose={onClose} title={`Detalhes da chamada de ${call.tenantName}`} size="wide"><dl>{values.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl><Button variant="secondary" onClick={onClose}>Fechar detalhe</Button></Dialog>;
}
