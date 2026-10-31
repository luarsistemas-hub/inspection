"use client";

import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Button, Dialog, Field, IconButton, Input, Recovery, Select, Status } from "@inspection/design-system";
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
  cost: "" | "INFORMED" | "MISSING" | "UNCERTAIN" | "ERROR_WITHOUT_COST" | "UNVERIFIED" | "NOT_SENT";
  inspectionId: string;
  withoutInspection: boolean;
  includeTestData: boolean;
};

type Usage = G.AdminLlmUsageQuery["llmUsage"];
type Call = Usage["calls"][number];
type Tenant = G.AdminLlmUsageTenantsQuery["llmUsageTenants"]["nodes"][number];
type TenantAggregate = G.AdminLlmUsageByTenantQuery["llmUsageByTenant"]["nodes"][number];
type InspectionAggregate = G.AdminLlmUsageByInspectionQuery["llmUsageByInspection"]["nodes"][number];

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
  return { from: toLocalInput(start.toISOString()), to: toLocalInput(now.toISOString()), tenantId: "", tenantSearch: "", provider: "", model: "", modelAlias: "", mode: "LIVE", technicalOutcome: "", state: "", cost: "", inspectionId: "", withoutInspection: false, includeTestData: false };
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
    cost: ["INFORMED", "MISSING", "UNCERTAIN", "ERROR_WITHOUT_COST", "UNVERIFIED", "NOT_SENT"].includes(params.get("cost") ?? "") ? params.get("cost") as Filters["cost"] : "",
    inspectionId: params.get("inspectionId") ?? "",
    withoutInspection: params.get("withoutInspection") === "true",
    includeTestData: params.get("includeTestData") === "true",
  };
};
const failureText = (failure: unknown) => failure instanceof Error ? failure.message : (failure as GraphQLFailure).message ?? "Não foi possível carregar o consumo de LLM.";
const formatDate = (value: string) => new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(new Date(value));
const formatNumber = (value: number) => new Intl.NumberFormat().format(value);
/** Keeps unavailable gateway cost distinct from a confirmed zero-cost result. */
export const formatReportedCost = (value: string | number | null | undefined) => {
  if (value == null) return "Não informado";
  const raw = String(value);
  if (!/^(?:\d+)(?:\.\d+)?$/.test(raw)) return "Não informado";
  const [whole, fraction = ""] = raw.split(".");
  const firstNonZero = fraction.search(/[1-9]/);
  const precision = Math.min(12, Math.max(2, firstNonZero >= 0 ? firstNonZero + 3 : 2));
  let decimals = fraction.slice(0, precision).padEnd(2, "0");
  while (decimals.length > 2 && decimals.endsWith("0")) decimals = decimals.slice(0, -1);
  const formattedWhole = new Intl.NumberFormat("pt-BR", { maximumFractionDigits: 0 }).format(BigInt(whole));
  const currency = new Intl.NumberFormat("pt-BR", { style: "currency", currency: "USD" }).formatToParts(0).find((part) => part.type === "currency")?.value ?? "US$";
  return `${currency}\u00a0${formattedWhole},${decimals || "00"}`;
};
const text = (value: string | null | undefined) => value || "—";
export const isConfirmedZeroCost = (value: string | null | undefined) => value != null && /^0+(\.0+)?$/.test(value);
export const costStateLabel = (state: string | null | undefined) => {
  switch (state) {
    case "CONFIRMED": return "Custo confirmado";
    case "UNKNOWN": return "Custo desconhecido";
    case "UNCERTAIN": return "Custo incerto";
    case "ERROR_WITHOUT_COST": return "Erro sem custo informado";
    case "NOT_SENT": return "Não enviada (sem custo)";
    case "UNVERIFIED": return "Origem não verificada";
    default: return "Origem não verificada";
  }
};
/** Summarizes non-zero aggregate call states with Portuguese agreement. */
export const aggregateCostSummary = ({ calls, confirmed, uncertain, unknown, error, unverified }: { calls: number; confirmed: number; uncertain: number; unknown: number; error: number; unverified: number }) => {
  const parts = [`${formatNumber(calls)} ${calls === 1 ? "chamada" : "chamadas"}`];
  const states: Array<[number, string, string]> = [
    [confirmed, "custo confirmado", "custos confirmados"],
    [uncertain, "custo incerto", "custos incertos"],
    [unknown, "custo desconhecido", "custos desconhecidos"],
    [error, "erro sem custo", "erros sem custo"],
    [unverified, "origem não verificada", "origens não verificadas"],
  ];
  for (const [count, singular, plural] of states) {
    if (count > 0) parts.push(`${formatNumber(count)} ${count === 1 ? singular : plural}`);
  }
  return parts.join(" · ");
};

export function LLMUsagePage({ permitted }: { permitted: boolean }) {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const urlFilters = useMemo(() => readFilters(params), [params]);
  const [draft, setDraft] = useState<Filters>(urlFilters);
  const [usage, setUsage] = useState<Usage>();
  const [tenantOptions, setTenantOptions] = useState<Tenant[]>([]);
  const [tenantAggregates, setTenantAggregates] = useState<TenantAggregate[]>([]);
  const [inspectionAggregates, setInspectionAggregates] = useState<InspectionAggregate[]>([]);
  const [tenantAggregateNext, setTenantAggregateNext] = useState<string>();
  const [inspectionAggregateNext, setInspectionAggregateNext] = useState<string>();
  const [selectedTenantName, setSelectedTenantName] = useState("");
  const [detail, setDetail] = useState<Call>();
  const [loading, setLoading] = useState(false);
  const [tenantAggregateLoading, setTenantAggregateLoading] = useState(false);
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
    if (!urlFilters.inspectionId && !urlFilters.withoutInspection) { setUsage(undefined); setLoading(false); return; }
    request.current?.abort();
    const controller = new AbortController(); request.current = controller;
    setLoading(true); setError(undefined);
    const filter = {
      from: toUTC(urlFilters.from), to: toUTC(urlFilters.to), tenantId: urlFilters.tenantId || undefined, inspectionId: urlFilters.inspectionId || undefined, withoutInspection: urlFilters.withoutInspection,
      provider: urlFilters.provider || undefined, model: urlFilters.model || undefined, modelAlias: urlFilters.modelAlias || undefined,
      mode: urlFilters.mode, technicalOutcome: urlFilters.technicalOutcome || undefined, state: urlFilters.state || undefined, cost: urlFilters.cost || undefined, includeTestData: urlFilters.includeTestData,
    };
    void graphql<G.AdminLlmUsageQuery>(G.AdminLlmUsageDocument, { filter, first: 25, after: params.get("after") }, controller.signal).then((result) => { if (!controller.signal.aborted) setUsage(result.llmUsage); }).catch((failure) => { if (!controller.signal.aborted) setError(failureText(failure)); }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [permitted, refreshKey, urlFilters, params]);

  useEffect(() => {
    if (!permitted) return;
    const controller = new AbortController();
    setError(undefined);
    setTenantAggregateLoading(true);
    const filter = { from: toUTC(urlFilters.from), to: toUTC(urlFilters.to), provider: urlFilters.provider || undefined, model: urlFilters.model || undefined, modelAlias: urlFilters.modelAlias || undefined, mode: urlFilters.mode, technicalOutcome: urlFilters.technicalOutcome || undefined, state: urlFilters.state || undefined, cost: urlFilters.cost || undefined, includeTestData: urlFilters.includeTestData };
    if (!urlFilters.tenantId) {
      void graphql<G.AdminLlmUsageByTenantQuery>(G.AdminLlmUsageByTenantDocument, { filter, first: 25, after: params.get("after") }, controller.signal).then((result) => { if (controller.signal.aborted) return; setTenantAggregates((current) => params.get("after") ? [...current, ...result.llmUsageByTenant.nodes] : result.llmUsageByTenant.nodes); setTenantAggregateNext(result.llmUsageByTenant.pageInfo.hasNextPage ? result.llmUsageByTenant.pageInfo.endCursor ?? undefined : undefined); }).catch((failure) => { if (!controller.signal.aborted) setError(failureText(failure)); }).finally(() => { if (!controller.signal.aborted) setTenantAggregateLoading(false); });
      setInspectionAggregates([]); setInspectionAggregateNext(undefined);
    } else if (!urlFilters.inspectionId && !urlFilters.withoutInspection) {
      void graphql<G.AdminLlmUsageByInspectionQuery>(G.AdminLlmUsageByInspectionDocument, { tenantId: urlFilters.tenantId, filter, first: 25, after: params.get("after") }, controller.signal).then((result) => { if (controller.signal.aborted) return; setInspectionAggregates((current) => params.get("after") ? [...current, ...result.llmUsageByInspection.nodes] : result.llmUsageByInspection.nodes); setInspectionAggregateNext(result.llmUsageByInspection.pageInfo.hasNextPage ? result.llmUsageByInspection.pageInfo.endCursor ?? undefined : undefined); }).catch((failure) => { if (!controller.signal.aborted) setError(failureText(failure)); }).finally(() => { if (!controller.signal.aborted) setTenantAggregateLoading(false); });
      setTenantAggregates([]); setTenantAggregateNext(undefined);
    } else { setTenantAggregateLoading(false); }
    return () => controller.abort();
  }, [permitted, refreshKey, urlFilters, params]);

  const updateURL = (next: Filters, after?: string | null) => {
    const query = new URLSearchParams();
    query.set("from", toUTC(next.from)); query.set("to", toUTC(next.to)); query.set("mode", next.mode);
    const values: Array<[string, string]> = [["tenantId", next.tenantId], ["tenantSearch", next.tenantSearch], ["provider", next.provider], ["model", next.model], ["modelAlias", next.modelAlias], ["outcome", next.technicalOutcome], ["state", next.state], ["cost", next.cost], ["inspectionId", next.inspectionId], ["withoutInspection", next.withoutInspection ? "true" : ""], ["includeTestData", next.includeTestData ? "true" : ""]];
    values.forEach(([key, value]) => { if (value) query.set(key, value); });
    if (after) query.set("after", after);
    router.replace(`${pathname}?${query.toString()}`);
  };
  const submit = (event: FormEvent) => { event.preventDefault(); updateURL(draft); };
  const clear = () => { const next = defaultFilters(); setSelectedTenantName(""); updateURL(next); };
  const setFilter = <K extends keyof Filters>(key: K, value: Filters[K]) => setDraft((current) => ({ ...current, [key]: value }));
  const timezone = typeof Intl !== "undefined" ? Intl.DateTimeFormat().resolvedOptions().timeZone : "UTC";
  const activeAdvancedFilterCount = [urlFilters.tenantId, urlFilters.provider, urlFilters.model, urlFilters.modelAlias, urlFilters.mode === "MOCK" ? urlFilters.mode : "", urlFilters.technicalOutcome, urlFilters.state, urlFilters.cost, urlFilters.inspectionId, urlFilters.withoutInspection, urlFilters.includeTestData].filter(Boolean).length;

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
        <Field label="Estado do custo"><Select value={draft.cost} onChange={(event) => setFilter("cost", event.target.value as Filters["cost"])}><option value="">Todos</option><option value="INFORMED">Confirmado pelo gateway</option><option value="MISSING">Desconhecido</option><option value="UNCERTAIN">Incerto</option><option value="ERROR_WITHOUT_COST">Erro sem custo informado</option><option value="UNVERIFIED">Origem não verificada</option><option value="NOT_SENT">Não enviada (sem custo)</option></Select></Field>
        <Field label="Inspection ID"><Input value={draft.inspectionId} onChange={(event) => setFilter("inspectionId", event.target.value)} /></Field>
        <label className="llm-include-test-data"><input type="checkbox" checked={draft.includeTestData} onChange={(event) => setFilter("includeTestData", event.target.checked)} /> Incluir dados de teste</label>
      </div></details>
      <div className="llm-usage-filter-actions"><Button type="submit" disabled={draft.from > draft.to}>Aplicar</Button><Button type="button" variant="secondary" onClick={clear}>Limpar</Button><Button type="button" variant="secondary" onClick={() => setRefreshKey((value) => value + 1)}>Atualizar</Button></div>
    </form>
    <p role="status" className="status-line">{loading ? "Carregando consumo de LLM…" : error ?? ""}</p>
    {urlFilters.tenantId && <nav aria-label="Navegação do consumo de LLM" className="llm-usage-filter-actions"><Button type="button" variant="secondary" onClick={() => updateURL({ ...urlFilters, tenantId: "", inspectionId: "", withoutInspection: false })}>Todos os tenants</Button>{(urlFilters.inspectionId || urlFilters.withoutInspection) && <Button type="button" variant="secondary" onClick={() => updateURL({ ...urlFilters, inspectionId: "", withoutInspection: false })}>Vistorias do tenant</Button>}</nav>}
    {error ? <Recovery kind="error" title="Não foi possível carregar o consumo de LLM" onRetry={() => setRefreshKey((value) => value + 1)}>{error}</Recovery> : !urlFilters.tenantId ? <AggregateList label="Tenants" loading={tenantAggregateLoading} items={tenantAggregates.map((item) => ({ id: item.tenantId, label: item.tenantName, calls: item.attemptedCalls, amount: item.knownReportedCostDecimal, confirmed: item.confirmedCostCalls, uncertain: item.uncertainCostCalls, unknown: item.unknownCostCalls, unverified: item.unverifiedCostCalls, error: item.errorWithoutCostCalls, costComplete: item.costComplete, coverage: item.coverageComplete ? "Cobertura histórica completa" : "Cobertura histórica parcial" }))} onSelect={(id) => updateURL({ ...urlFilters, tenantId: id, inspectionId: "", withoutInspection: false })} onNext={tenantAggregateNext ? () => updateURL(urlFilters, tenantAggregateNext) : undefined} /> : !urlFilters.inspectionId && !urlFilters.withoutInspection ? <AggregateList label="Vistorias" loading={tenantAggregateLoading} items={inspectionAggregates.map((item) => ({ id: item.inspectionId ?? "sem-vistoria", label: item.inspectionLabel, calls: item.attemptedCalls, amount: item.knownReportedCostDecimal, confirmed: item.confirmedCostCalls, uncertain: item.uncertainCostCalls, unknown: item.unknownCostCalls, unverified: item.unverifiedCostCalls, error: item.errorWithoutCostCalls, costComplete: item.costComplete, coverage: item.coverageComplete ? "Cobertura histórica completa" : "Cobertura histórica parcial" }))} onSelect={(id) => updateURL({ ...urlFilters, inspectionId: id === "sem-vistoria" ? "" : id, withoutInspection: id === "sem-vistoria" })} onNext={inspectionAggregateNext ? () => updateURL(urlFilters, inspectionAggregateNext) : undefined} /> : usage ? <UsageView usage={usage} loading={loading} onDetail={setDetail} onNext={(after) => updateURL(urlFilters, after)} /> : <Recovery kind="loading" title="Carregando consumo de LLM">A consulta atual será aplicada quando a resposta estiver disponível.</Recovery>}
    {detail && <LLMCallDetail call={detail} onClose={() => setDetail(undefined)} />}
  </div>;
}

function UsageView({ usage, loading, onDetail, onNext }: { usage: Usage; loading: boolean; onDetail: (call: Call) => void; onNext: (after: string) => void }) {
  const cacheHitRate = usage.knownCacheCalls > 0 ? `${((usage.cacheHitCalls / usage.knownCacheCalls) * 100).toFixed(1)}%` : "Não informado";
  const [focusedCallId, setFocusedCallId] = useState<string>();
  const focusedCall = usage.calls.find((call) => call.callId === focusedCallId) ?? usage.calls[0];
  return <>
    <div className="llm-usage-metrics" aria-label="Resumo do consumo de LLM"><Metric label="Chamadas entregues" value={formatNumber(usage.deliveredCalls)} /><Metric label="Tokens de entrada" value={formatNumber(usage.inputTokens)} /><Metric label="Custo informado" value={formatReportedCost(usage.knownReportedCostDecimal || usage.knownReportedCost)} /></div>
    <details className="llm-usage-more"><summary>Métricas detalhadas</summary><div className="llm-usage-more-grid"><Metric label="Tentativas" value={formatNumber(usage.attemptedCalls)} /><Metric label="Incompletas" value={formatNumber(usage.incompleteCalls)} /><Metric label="Tokens em cache" value={usage.knownCacheCalls > 0 ? formatNumber(usage.cachedInputTokens) : "Não informado"} /><Metric label="Chamadas com acerto" value={formatNumber(usage.cacheHitCalls)} /><Metric label="Taxa de acerto (informadas)" value={cacheHitRate} /><Metric label="Cache não informado" value={formatNumber(usage.unknownCacheCalls)} /><Metric label="Tokens de saída" value={formatNumber(usage.outputTokens)} /><Metric label="Custo desconhecido" value={formatNumber(usage.unknownCostCalls)} /><Metric label="Custo incerto" value={formatNumber(usage.uncertainCostCalls)} /><Metric label="Erro sem custo informado" value={formatNumber(usage.errorWithoutCostCalls)} /><Metric label="Origem não verificada" value={formatNumber(usage.unverifiedCostCalls)} /></div></details>
    {!usage.calls.length ? <Recovery kind="empty" title="Nenhuma chamada encontrada">Ajuste os filtros ou aumente o período consultado.</Recovery> : <><div className="llm-focus-workspace"><div className="llm-focus-list"><ol className="admin-focus-rows llm-focus-rows" aria-label="Chamadas de LLM">{usage.calls.map((call) => <li key={call.callId}><div className="admin-focus-row" data-focused={focusedCall?.callId === call.callId}><div className="admin-focus-row-main"><button className="admin-row-focus llm-row-focus" type="button" aria-pressed={focusedCall?.callId === call.callId} onClick={() => setFocusedCallId(call.callId)}>{call.tenantName}</button><span>{text(call.modelAlias || call.model)} · {formatDate(call.startedAt)}</span></div><div className="admin-focus-row-side"><Status>{costStateLabel(call.costState)}</Status><IconButton label={`Abrir detalhes da chamada de ${call.tenantName}`} tooltip="Abrir detalhes" icon="eye" onPress={() => onDetail(call)} /></div></div>{call.mode === "LIVE" && call.provider !== "qa" && call.costState === "CONFIRMED" && isConfirmedZeroCost(call.reportedCostDecimal) && (call.inputTokens ?? 0) > 0 && <p role="note">Modelo com tokens e custo zero informado pelo gateway; confirme a precificação.</p>}</li>)}</ol><div className="admin-collection-foot"><span>{usage.calls.length} chamadas · {usage.costComplete ? "custo completo" : "cobertura de custo parcial"}</span><span>{loading ? "Atualizando…" : usage.pageInfo.hasNextPage ? "mais páginas disponíveis" : "fim da consulta"}</span></div>{usage.pageInfo.hasNextPage && usage.pageInfo.endCursor && <Button variant="secondary" onClick={() => onNext(usage.pageInfo.endCursor!)}>Carregar próxima página</Button>}</div>{focusedCall && <aside className="admin-focus-detail llm-focus-detail"><span className="admin-focus-eyebrow">Chamada selecionada</span><h2>{focusedCall.modelAlias}</h2><dl><div><dt>Tenant</dt><dd>{focusedCall.tenantName}</dd></div><div><dt>Resultado</dt><dd>{focusedCall.technicalOutcome}</dd></div><div><dt>Entrada / saída</dt><dd>{formatNumber(focusedCall.inputTokens ?? 0)} / {formatNumber(focusedCall.outputTokens ?? 0)} tokens</dd></div><div><dt>Cache</dt><dd>{focusedCall.cachedInputTokens == null ? "Não informado" : formatNumber(focusedCall.cachedInputTokens)}</dd></div><div><dt>Custo</dt><dd>{formatReportedCost(focusedCall.reportedCost)}</dd></div></dl><Button variant="secondary" onClick={() => onDetail(focusedCall)}>Ver detalhes <span aria-hidden="true">→</span></Button></aside>}</div></>}
  </>;
}

function AggregateList({ label, items, loading, onSelect, onNext }: { label: string; items: Array<{ id: string; label: string; calls: number; amount: string; confirmed: number; uncertain: number; unknown: number; error: number; unverified: number; costComplete: boolean; coverage: string }>; loading: boolean; onSelect: (id: string) => void; onNext?: () => void }) {
  if (loading && !items.length) return <Recovery kind="loading" title={`Carregando consumo por ${label.toLocaleLowerCase("pt-BR")}`}>A consulta atual está em andamento.</Recovery>;
  if (!items.length) return <Recovery kind="empty" title={`Nenhum ${label.toLocaleLowerCase("pt-BR")} encontrado`}>Ajuste os filtros ou aumente o período consultado.</Recovery>;
  return <section aria-label={`Consumo por ${label.toLocaleLowerCase("pt-BR")}`} className="llm-usage-aggregates"><ol className="admin-focus-rows llm-focus-rows">{items.map((item) => <li key={item.id}><div className="admin-focus-row"><div className="admin-focus-row-main"><button className="admin-row-focus llm-row-focus" type="button" aria-label={`Abrir consumo de ${item.label}`} onClick={() => onSelect(item.id)}>{item.label}</button><span className="llm-aggregate-summary">{aggregateCostSummary(item)}</span><span className="llm-aggregate-status">{item.costComplete ? "Custo completo" : "Custo parcial"} · {item.coverage}</span></div><div className="admin-focus-row-side"><strong>{formatReportedCost(item.amount)}</strong></div></div></li>)}</ol>{loading && <p role="status">Atualizando agregados…</p>}{onNext && <Button variant="secondary" onClick={onNext}>Carregar próxima página</Button>}</section>;
}

function Metric({ label, value }: { label: string; value: string }) { return <div className="llm-usage-metric"><span>{label}</span><strong>{value}</strong></div>; }

function LLMCallDetail({ call, onClose }: { call: Call; onClose: () => void }) {
  const values: Array<[string, string]> = [["Call ID", call.callId], ["Tenant", `${call.tenantName} · ${call.tenantId}`], ["Inspection ID", call.inspectionId ?? "Sem vistoria"], ["Job ID", call.jobId], ["Event ID", call.eventId], ["Execution ID", call.executionId], ["Correlation ID", call.correlationId], ["Gateway request ID", text(call.gatewayRequestId)], ["ID da chamada no LiteLLM", text(call.gatewayCallId)], ["Provedor", text(call.provider)], ["Modelo", text(call.model)], ["Alias", call.modelAlias], ["Modo", call.mode], ["Comparação", call.comparisonMode], ["Estado", call.state], ["Resultado técnico", call.technicalOutcome], ["HTTP", call.httpStatus == null ? "—" : String(call.httpStatus)], ["Tokens", `${call.inputTokens ?? "—"} entrada / ${call.outputTokens ?? "—"} saída`], ["Tokens de entrada em cache", call.cachedInputTokens == null ? "Não informado" : formatNumber(call.cachedInputTokens)], ["Imagens", call.imageCount == null ? "Não informado" : formatNumber(call.imageCount)], ["Corpo enviado", call.requestBodyBytes == null ? "Não informado" : `${formatNumber(call.requestBodyBytes)} bytes`], ["Custo informado", formatReportedCost(call.reportedCostDecimal ?? call.reportedCost)], ["Origem do custo", costStateLabel(call.costState)], ["Duração", call.durationMs == null ? "—" : `${call.durationMs} ms`], ["Início", formatDate(call.startedAt)], ["Fim", call.finishedAt ? formatDate(call.finishedAt) : "—"]];
  return <Dialog isOpen onClose={onClose} title={`Detalhes da chamada de ${call.tenantName}`} size="wide"><dl>{values.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl><Button variant="secondary" onClick={onClose}>Fechar detalhe</Button></Dialog>;
}
