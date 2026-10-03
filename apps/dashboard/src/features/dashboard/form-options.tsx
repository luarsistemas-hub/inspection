"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Combobox, type ComboboxOption } from "@inspection/design-system";
import { DashboardFormOptionsDocument, type DashboardFormOptionsQuery, type DashboardFormOptionsQueryVariables } from "@/graphql/generated";
import { graphql } from "@/graphql/client";

export type EntityKind = "asset" | "participant" | "template";
export type FormOptionsState = {
  data?: DashboardFormOptionsQuery;
  loading: boolean;
  error?: string;
  searching: Partial<Record<EntityKind, boolean>>;
  search: (kind: EntityKind, value: string) => void;
  loadMore: (kind: EntityKind) => void;
};

const emptyVariables = (kind?: EntityKind, search?: string, after?: string): DashboardFormOptionsQueryVariables => ({
  first: 100,
  assetSearch: kind === "asset" ? search || null : null,
  assetAfter: kind === "asset" ? after ?? null : null,
  participantSearch: kind === "participant" ? search || null : null,
  participantAfter: kind === "participant" ? after ?? null : null,
  templateSearch: kind === "template" ? search || null : null,
  templateAfter: kind === "template" ? after ?? null : null,
});

export function useDashboardFormOptions(enabled: boolean): FormOptionsState {
  const [data, setData] = useState<DashboardFormOptionsQuery>();
  const [loading, setLoading] = useState(enabled);
  const [error, setError] = useState<string>();
  const [searching, setSearching] = useState<Partial<Record<EntityKind, boolean>>>({});
  const searchTerms = useRef<Record<EntityKind, string>>({ asset: "", participant: "", template: "" });
  const requests = useRef<Record<EntityKind, number>>({ asset: 0, participant: 0, template: 0 });

  useEffect(() => {
    if (!enabled) { setData(undefined); setLoading(false); setError(undefined); return; }
    let cancelled = false;
    setLoading(true);
    void graphql<DashboardFormOptionsQuery, DashboardFormOptionsQueryVariables>(DashboardFormOptionsDocument, emptyVariables()).then((result) => {
      if (!cancelled) { setData(result); setError(undefined); }
    }).catch((failure) => {
      if (!cancelled) setError(failure instanceof Error ? failure.message : "Não foi possível carregar as opções do cadastro.");
    }).finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [enabled]);

  const search = useCallback((kind: EntityKind, value: string) => {
    const term = value.trim();
    if (term === searchTerms.current[kind]) return;
    searchTerms.current[kind] = term;
    const request = ++requests.current[kind];
    setSearching((current) => ({ ...current, [kind]: true }));
    void graphql<DashboardFormOptionsQuery, DashboardFormOptionsQueryVariables>(DashboardFormOptionsDocument, emptyVariables(kind, term)).then((result) => {
      if (request !== requests.current[kind]) return;
      setData((current) => current ? { ...current, [kind === "asset" ? "assets" : kind === "participant" ? "participants" : "templates"]: result[kind === "asset" ? "assets" : kind === "participant" ? "participants" : "templates"] } : result);
      setError(undefined);
    }).catch((failure) => {
      if (request === requests.current[kind]) setError(failure instanceof Error ? failure.message : "Não foi possível pesquisar as opções.");
    }).finally(() => {
      if (request === requests.current[kind]) setSearching((current) => ({ ...current, [kind]: false }));
    });
  }, []);

  const loadMore = useCallback((kind: EntityKind) => {
    if (!data) return;
    const field = kind === "asset" ? data.assets : kind === "participant" ? data.participants : data.templates;
    if (!field.pageInfo.hasNextPage || !field.pageInfo.endCursor || searching[kind]) return;
    const request = ++requests.current[kind];
    const term = searchTerms.current[kind];
    setSearching((current) => ({ ...current, [kind]: true }));
    void graphql<DashboardFormOptionsQuery, DashboardFormOptionsQueryVariables>(DashboardFormOptionsDocument, emptyVariables(kind, term, field.pageInfo.endCursor)).then((result) => {
      if (request !== requests.current[kind]) return;
      setData((current) => {
        if (!current) return result;
        const key = kind === "asset" ? "assets" : kind === "participant" ? "participants" : "templates";
        const next = result[key];
        return { ...current, [key]: { ...next, nodes: [...current[key].nodes, ...next.nodes] } };
      });
    }).catch((failure) => {
      if (request === requests.current[kind]) setError(failure instanceof Error ? failure.message : "Não foi possível carregar mais opções.");
    }).finally(() => {
      if (request === requests.current[kind]) setSearching((current) => ({ ...current, [kind]: false }));
    });
  }, [data, searching]);

  return { data, loading, error, searching, search, loadMore };
}

export function EntityRelationshipField({
  kind,
  label,
  value,
  onChange,
  options,
  segmentVersionId,
  required = false,
  disabled = false,
}: {
  kind: EntityKind;
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: FormOptionsState;
  segmentVersionId?: string;
  required?: boolean;
  disabled?: boolean;
}) {
  const connection = kind === "asset" ? options.data?.assets : kind === "participant" ? options.data?.participants : options.data?.templates;
  const choices: ComboboxOption[] = kind === "asset"
    ? (options.data?.assets.nodes ?? []).filter((item) => item.status === "ACTIVE").map((item) => ({ value: item.id, label: item.name, description: item.externalKey }))
    : kind === "participant"
      ? (options.data?.participants.nodes ?? []).filter((item) => item.status === "ACTIVE").map((item) => ({ value: item.id, label: item.name }))
      : (options.data?.templates.nodes ?? []).filter((item) => item.activeVersionId && (!segmentVersionId || item.segmentVersionId === segmentVersionId)).map((item) => ({ value: item.id, label: item.name, description: item.key }));
  return <div className="dashboard-relationship-field"><label>{label}<Combobox value={value} options={choices} onChange={onChange} onInputChange={(term) => options.search(kind, term)} required={required} disabled={disabled} aria-label={label} /></label>{connection?.pageInfo.hasNextPage && <button type="button" className="secondary" disabled={options.searching[kind]} onClick={() => options.loadMore(kind)}>Carregar mais opções</button>}</div>;
}
