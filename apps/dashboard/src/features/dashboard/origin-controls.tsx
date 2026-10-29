"use client";

import { useEffect, useState } from "react";
import {
  DashboardOriginVersionsDocument,
  DashboardTemplateVersionDocument,
  DiscardBaselineDraftDocument,
  InvalidateOriginVersionDocument,
  type DashboardOriginVersionsQuery,
} from "@/graphql/generated";
import { graphql } from "@/graphql/client";
import { Dialog } from "@inspection/design-system";
import { ConfirmationDialog } from "./confirmation-dialog";
import { purposeLabel } from "./inventory-presentation";

type OriginVersion = DashboardOriginVersionsQuery["originVersions"]["nodes"][number];

export function originVersionStatus(version: Pick<OriginVersion, "status" | "preparationStatus" | "selectable" | "source" | "draftState">): string {
  if (version.status === "EXPIRED" || version.status === "CLOSED") return "Expirada";
  if (version.status === "INVALIDATED") return "Invalidada";
  if (version.status === "DISCARDED") return "Descartada";
  if (version.status === "SUPERSEDED") return "Versão anterior";
  if (version.preparationStatus === "FAILED" || version.status === "FAILED") return "Preparação com falha";
  if (version.status === "DRAFT" && version.draftState) {
    const prefix = version.source === "INSPECTION_PROMOTION" ? "Rascunho de promoção" : version.source === "CAPTURE" ? "Rascunho de captura" : "Rascunho de inventário";
    const suffix = version.draftState === "PREPARING" ? "em preparação" : version.draftState === "READY" ? "pronto para revisão" : version.draftState === "FAILED" ? "com falha" : "";
    return suffix ? `${prefix} ${suffix}` : prefix;
  }
  if (version.status === "DRAFT" && version.preparationStatus === "READY") return "Origem recebida; pronta para ativação";
  if (version.status === "DRAFT" && version.preparationStatus) return "Origem recebida; preparação pendente";
  if (version.status === "DRAFT" || version.status === "INVITED") return "Convite pendente";
  if (version.status === "ACTIVE" && version.preparationStatus && version.preparationStatus !== "READY") return "Preparação pendente";
  if (version.status === "ACTIVE" && version.selectable) return "Pronta para seleção";
  if (version.status === "ACTIVE") return "Ativa, aguardando preparação";
  return "Situação indisponível";
}

export function selectableOriginVersions(versions: OriginVersion[], mediaKind?: string | null): OriginVersion[] {
  if (!mediaKind) return [];
  return versions.filter((version) => (version.status === "ACTIVE" && version.selectable || version.hasInventory && version.status === "SUPERSEDED") && (version.hasInventory || version.mediaKind === mediaKind));
}

/** activeCompatibleOriginId finds the active version accepted by the existing selector rules. */
export function activeCompatibleOriginId(versions: OriginVersion[], mediaKind?: string | null): string {
  return selectableOriginVersions(versions, mediaKind).find((version) => version.status === "ACTIVE")?.id ?? "";
}

export function useActiveCompatibleOriginId(assetId: string, mediaKind?: string | null) {
  const [originId, setOriginId] = useState("");
  useEffect(() => {
    let current = true;
    setOriginId("");
    if (!assetId || !mediaKind) return () => { current = false; };
    void graphql<DashboardOriginVersionsQuery, { assetId: string }>(DashboardOriginVersionsDocument, { assetId })
      .then((result) => { if (current) setOriginId(activeCompatibleOriginId(result.originVersions.nodes, mediaKind)); })
      .catch(() => { if (current) setOriginId(""); });
    return () => { current = false; };
  }, [assetId, mediaKind]);
  return originId;
}

export function useTemplateMediaKind(templateVersionId?: string | null) {
  const [mediaKind, setMediaKind] = useState<string>();
  useEffect(() => {
    let current = true;
    setMediaKind(undefined);
    if (templateVersionId) {
      void graphql(DashboardTemplateVersionDocument, { id: templateVersionId })
        .then((result) => { if (current) setMediaKind(result.templateVersion?.mediaKind); })
        .catch(() => { if (current) setMediaKind(undefined); });
    }
    return () => { current = false; };
  }, [templateVersionId]);
  return mediaKind;
}

export function FixedOriginSelector({ assetId, mediaKind, value, onChange }: {
  assetId: string;
  mediaKind?: string | null;
  value: string;
  onChange: (value: string) => void;
}) {
  const [versions, setVersions] = useState<OriginVersion[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [status, setStatus] = useState("Carregando origens disponíveis…");
  useEffect(() => {
    let current = true;
    setVersions([]);
    setLoaded(false);
    setStatus("Carregando origens disponíveis…");
    if (!assetId) { setStatus("Selecione um imóvel para consultar as origens."); return () => { current = false; }; }
    void graphql(DashboardOriginVersionsDocument, { assetId })
      .then((result) => {
        if (!current) return;
        setVersions(result.originVersions.nodes);
        setLoaded(true);
        setStatus(result.originVersions.nodes.length ? "Origens atualizadas." : "Nenhuma origem foi convidada para este imóvel.");
      })
      .catch(() => { if (current) setStatus("Não foi possível carregar as origens. Tente novamente."); });
    return () => { current = false; };
  }, [assetId]);
  const choices = selectableOriginVersions(versions, mediaKind);
  // Only drop a preset value once the asset's versions are known; clearing it
  // while they load would discard a preselected or linked origin.
  useEffect(() => { if (loaded && value && !choices.some((origin) => origin.id === value)) onChange(""); }, [choices, loaded, onChange, value]);
  return <div>
    <label>Origem<select aria-label="Origem" required value={value} onChange={(event) => onChange(event.target.value)} disabled={!mediaKind}>
      <option value="">Selecione uma origem de referência</option>
      {choices.map((origin) => <option key={origin.id} value={origin.id}>Versão {origin.versionNumber}{origin.purpose ? ` · ${purposeLabel(origin.purpose)}` : ""}{origin.status === "SUPERSEDED" ? " · anterior" : " · ativa"} · {origin.hasInventory ? "Inventário" : origin.mediaKind === "VIDEO" ? "Vídeo" : "Fotos"}</option>)}
    </select></label>
    <p role="status">{!mediaKind ? "Carregando a modalidade do modelo…" : choices.length ? `${choices.length} origem(ns) pronta(s) para este modelo.` : "Não há origens de referência prontas para seleção."}</p>
    <span className="visually-hidden" aria-live="polite">{status}</span>
  </div>;
}

export function OriginManagement({ assetId, onChanged }: {
  assetId: string;
  onChanged?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [versions, setVersions] = useState<OriginVersion[]>([]);
  const [discarding, setDiscarding] = useState<OriginVersion>();
  const [message, setMessage] = useState("Carregando origens do imóvel…");
  const [busy, setBusy] = useState(false);
  const refresh = async () => {
    try {
      const result = await graphql(DashboardOriginVersionsDocument, { assetId });
      setVersions(result.originVersions.nodes);
      setMessage(result.originVersions.nodes.length ? "Origens atualizadas." : "Ainda não há origens para este imóvel.");
    } catch {
      setMessage("Não foi possível carregar as origens. Tente novamente.");
    }
  };
  useEffect(() => { void refresh(); }, [assetId]);
  const mutate = async (version: OriginVersion) => {
    if (busy) return;
    setBusy(true);
    try {
      const result = await graphql(InvalidateOriginVersionDocument, { input: { versionId: version.id, clientMutationId: mutationId() } });
      const error = result.invalidateOriginVersion.userErrors[0];
      await refresh();
      setMessage(error?.message ?? "Origem invalidada.");
      if (!error) onChanged?.();
    } catch {
      setMessage("Não foi possível concluir a ação de origem. Tente novamente.");
    } finally { setBusy(false); }
  };

  const discard = async () => {
    if (!discarding) return;
    setBusy(true);
    try {
      const result = await graphql(DiscardBaselineDraftDocument, { input: { originVersionId: discarding.id, clientMutationId: mutationId() } });
      const failure = result.discardBaselineDraft.userErrors[0]?.message;
      if (failure) throw new Error(failure);
      await refresh();
      setMessage("Rascunho descartado.");
      onChanged?.();
    } catch (error) {
      throw new Error(error instanceof Error ? error.message : "Não foi possível descartar o rascunho.");
    } finally { setBusy(false); }
  };

  return <>
    <button className="secondary" type="button" onClick={() => setOpen(true)}>Origens do imóvel</button>
    <Dialog isOpen={open} onClose={() => setOpen(false)} title="Origens do imóvel">
    <section className="origin-management" aria-labelledby="origin-management-title">
    <h3 id="origin-management-title">Gerenciar versões de origem</h3>
    <p>Versões de origem e inventário deste imóvel.</p>
    {versions.length > 0 ? <><p role="status" aria-live="polite">{message}</p><ul>{versions.map((version) => <li key={version.id}>
      <strong>Versão {version.versionNumber} · {version.hasInventory ? "Inventário" : version.mediaKind === "VIDEO" ? "Vídeo" : "Fotos"}</strong>
      <span>{originVersionStatus(version)}</span>
      {version.preparationStatus && <span>Preparação: {version.preparationStatus === "READY" ? "pronta" : version.preparationStatus === "FAILED" ? "falhou" : "pendente"}</span>}
      {version.sourceAvailability && <span>Fonte: {availabilityLabel(version.sourceAvailability)}</span>}
      {version.limitedCoverage && <span>Cobertura limitada: esta origem cobre apenas os ambientes selecionados</span>}
      {version.selectable && version.status !== "ACTIVE" ? <span>Não disponível para novas vistorias.</span> : null}
      <div className="actions">
        {version.status === "ACTIVE" && <button type="button" className="secondary" disabled={busy} onClick={() => void mutate(version)}>Invalidar</button>}
        {version.status === "DRAFT" && version.hasInventory && (version.draftState === "READY" || version.draftState === "FAILED") && <button type="button" className="secondary" disabled={busy} onClick={() => setDiscarding(version)}>Descartar rascunho</button>}
      </div>
    </li>)}</ul></> : <p role="status" aria-live="polite">{message === "Ainda não há origens para este imóvel." ? "Ainda não há versões de origem cadastradas para este imóvel." : message}</p>}
  </section>
    </Dialog>
    <ConfirmationDialog isOpen={Boolean(discarding)} onClose={() => setDiscarding(undefined)} title="Descartar rascunho" target={`Versão ${discarding?.versionNumber ?? ""}`} scope="Inventário do imóvel" consequence="O rascunho será descartado. Se pertencer a uma promoção em andamento, a promoção também será cancelada." confirmLabel="Descartar rascunho" onConfirm={discard} />
  </>;
}

function availabilityLabel(value: string): string {
  if (value === "AVAILABLE" || value === "RETAINED") return "disponível";
  if (value === "EXPIRED") return "expirada; evidências retidas podem continuar disponíveis";
  if (value === "DELETING") return "em encerramento";
  return "indisponível";
}

function mutationId() {
  return typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;
}
