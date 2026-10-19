"use client";

import { useEffect, useState } from "react";
import {
  ActivateOriginVersionDocument,
  CloseOriginInvitationDocument,
  DashboardOriginVersionsDocument,
  DashboardTemplateVersionDocument,
  InvalidateOriginVersionDocument,
  InviteOriginCaptureDocument,
  type DashboardOriginVersionsQuery,
} from "@/graphql/generated";
import { graphql } from "@/graphql/client";
import { Dialog } from "@inspection/design-system";

type OriginVersion = DashboardOriginVersionsQuery["originVersions"]["nodes"][number];

export function canActivateOriginVersion(version: Pick<OriginVersion, "status" | "mediaKind" | "captureSubmitted" | "preparationStatus">): boolean {
  if (version.status !== "DRAFT" || !version.captureSubmitted) return false;
  return version.mediaKind === "PHOTO" || (version.mediaKind === "VIDEO" && version.preparationStatus === "READY");
}

export function originVersionStatus(status: string, preparationStatus?: string | null, selectable?: boolean): string {
  if (status === "EXPIRED" || status === "CLOSED") return "Expirada";
  if (status === "INVALIDATED") return "Invalidada";
  if (preparationStatus === "FAILED" || status === "FAILED") return "Preparação com falha";
  if (status === "DRAFT" && preparationStatus === "READY") return "Origem recebida; pronta para ativação";
  if (status === "DRAFT" && preparationStatus) return "Origem recebida; preparação pendente";
  if (status === "DRAFT" || status === "INVITED") return "Convite pendente";
  if (preparationStatus && preparationStatus !== "READY") return "Preparação pendente";
  if (selectable) return "Pronta para seleção";
  return status === "ACTIVE" ? "Ativa, aguardando preparação" : "Aguardando envio";
}

export function selectableOriginVersions(versions: OriginVersion[], mediaKind?: string | null): OriginVersion[] {
  if (!mediaKind) return [];
  return versions.filter((version) => version.selectable && version.mediaKind === mediaKind && version.status === "ACTIVE");
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
  const [status, setStatus] = useState("Carregando origens disponíveis…");
  useEffect(() => {
    let current = true;
    setVersions([]);
    setStatus("Carregando origens disponíveis…");
    if (!assetId) { setStatus("Selecione um imóvel para consultar as origens."); return () => { current = false; }; }
    void graphql(DashboardOriginVersionsDocument, { assetId })
      .then((result) => {
        if (!current) return;
        setVersions(result.originVersions.nodes);
        setStatus(result.originVersions.nodes.length ? "Origens atualizadas." : "Nenhuma origem foi convidada para este imóvel.");
      })
      .catch(() => { if (current) setStatus("Não foi possível carregar as origens. Tente novamente."); });
    return () => { current = false; };
  }, [assetId]);
  const choices = selectableOriginVersions(versions, mediaKind);
  useEffect(() => { if (value && !choices.some((origin) => origin.id === value)) onChange(""); }, [choices, onChange, value]);
  const label = mediaKind === "VIDEO" ? "vídeo de referência" : mediaKind === "PHOTO" ? "fotos de referência" : "origem de referência";
  return <div>
    <label>Origem<select aria-label="Origem" required value={value} onChange={(event) => onChange(event.target.value)} disabled={!mediaKind}>
      <option value="">Selecione {label}</option>
      {choices.map((origin) => <option key={origin.id} value={origin.id}>Versão {origin.versionNumber} · {origin.mediaKind === "VIDEO" ? "Vídeo" : "Fotos"}</option>)}
    </select></label>
    <p role="status">{!mediaKind ? "Carregando a modalidade do modelo…" : choices.length ? `${choices.length} origem(ns) pronta(s) para este modelo.` : `Não há ${label} prontas para seleção.`}</p>
    <span className="visually-hidden" aria-live="polite">{status}</span>
  </div>;
}

export function OriginManagement({ assetId, templateVersionId, participants }: {
  assetId: string;
  templateVersionId?: string | null;
  participants: Array<{ id: string; name: string }>;
}) {
  const [open, setOpen] = useState(false);
  const [versions, setVersions] = useState<OriginVersion[]>([]);
  const [participantId, setParticipantId] = useState("");
  const [message, setMessage] = useState("Carregando origens do imóvel…");
  const [busy, setBusy] = useState(false);
  const mediaKind = useTemplateMediaKind(templateVersionId);
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

  const mutate = async (action: "invite" | "activate" | "invalidate" | "close", version?: OriginVersion) => {
    if (busy) return;
    setBusy(true);
    let feedback = "";
    try {
      if (action === "invite") {
        const expiresAt = new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString();
        const result = await graphql(InviteOriginCaptureDocument, { input: { assetId, participantId, expiresAt, clientMutationId: mutationId() } });
        const payload = result.inviteOriginCapture;
        feedback = payload.userErrors[0]?.message ?? (payload.originVersionId ? `Convite de ${mediaKind === "VIDEO" ? "vídeo" : "fotos"} enviado; a origem aparecerá após o envio.` : "Não foi possível criar o convite.");
      } else if (version) {
        const input = { versionId: version.id, clientMutationId: mutationId() };
        const payload = action === "activate"
          ? (await graphql(ActivateOriginVersionDocument, { input })).activateOriginVersion
          : action === "invalidate"
            ? (await graphql(InvalidateOriginVersionDocument, { input })).invalidateOriginVersion
            : (await graphql(CloseOriginInvitationDocument, { input: { ...input, reason: "Convite fechado pelo Painel" } })).closeOriginInvitation;
        feedback = payload.userErrors[0]?.message ?? (action === "activate" ? "Origem ativada para novas vistorias." : action === "invalidate" ? "Origem invalidada." : "Convite fechado.");
      }
      await refresh();
      if (feedback) setMessage(feedback);
    } catch {
      setMessage("Não foi possível concluir a ação de origem. Tente novamente.");
    } finally { setBusy(false); }
  };

  return <>
    <button className="secondary" type="button" onClick={() => setOpen(true)}>Origens do imóvel</button>
    <Dialog isOpen={open} onClose={() => setOpen(false)} title="Origens do imóvel">
    <section className="origin-management" aria-labelledby="origin-management-title">
    <h3 id="origin-management-title">Gerenciar versões de origem</h3>
    <p>Imóvel desta vistoria · Convite para {mediaKind === "VIDEO" ? "vídeo" : mediaKind === "PHOTO" ? "fotos" : "carregamento da modalidade"} conforme o modelo ativo.</p>
    <div className="origin-invite-controls">
      <label>Responsável<select aria-label="Responsável pela origem" value={participantId} onChange={(event) => setParticipantId(event.target.value)}>
        <option value="">Selecione</option>{participants.filter((participant) => participant.id).map((participant) => <option key={participant.id} value={participant.id}>{participant.name}</option>)}
      </select></label>
      <button type="button" disabled={busy || !participantId || !mediaKind} onClick={() => void mutate("invite")}>Convidar origem{mediaKind ? ` (${mediaKind === "VIDEO" ? "vídeo" : "fotos"})` : ""}</button>
    </div>
    {versions.length > 0 ? <><p role="status" aria-live="polite">{message}</p><ul>{versions.map((version) => <li key={version.id}>
      <strong>Versão {version.versionNumber} · {version.mediaKind === "VIDEO" ? "Vídeo" : "Fotos"}</strong>
      <span>{originVersionStatus(version.status, version.preparationStatus, version.selectable)}</span>
      {version.preparationStatus && <span>Preparação: {version.preparationStatus === "READY" ? "pronta" : version.preparationStatus === "FAILED" ? "falhou" : "pendente"}</span>}
      {version.sourceAvailability && <span>Fonte: {availabilityLabel(version.sourceAvailability)}</span>}
      {version.limitedCoverage && <span>Cobertura limitada: esta origem cobre apenas os ambientes selecionados</span>}
      {version.selectable && version.status !== "ACTIVE" ? <span>Não disponível para novas vistorias.</span> : null}
      <div className="actions">
        {canActivateOriginVersion(version) && <button type="button" disabled={busy} onClick={() => void mutate("activate", version)}>Ativar</button>}
        {version.status === "ACTIVE" && <button type="button" className="secondary" disabled={busy} onClick={() => void mutate("invalidate", version)}>Invalidar</button>}
        {version.status === "DRAFT" && !version.preparationStatus && <button type="button" className="secondary" disabled={busy} onClick={() => void mutate("close", version)}>Fechar convite</button>}
      </div>
    </li>)}</ul></> : <p role="status" aria-live="polite">{message === "Ainda não há origens para este imóvel." ? "Ainda não há versões de origem cadastradas para este imóvel." : message}</p>}
  </section>
    </Dialog>
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
