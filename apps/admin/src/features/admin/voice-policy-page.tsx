"use client";

import { FormEvent, useCallback, useEffect, useState } from "react";
import { Alert, Button, Checkbox, Field, Input, Recovery } from "@inspection/design-system";
import { AdminLocalError, graphQLFailureText, graphql, throwAdminUserErrors } from "@/graphql/client";
import * as G from "@/graphql/generated";
import { hasCompleteVoicePolicyLimits, voicePolicyMissingLimits, type VoicePolicyLimits } from "./voice-policy-validation";

const emptyLimits: VoicePolicyLimits = { tenantNoteLimit: "", tenantAudioLimitMs: "", inspectionNoteLimit: "", inspectionAudioLimitMs: "" };
const toLimit = (value: number | null | undefined): number | "" => value && value > 0 ? value : "";
const failureText = graphQLFailureText;

/** Tenant administrator controls for voice-note admission ceilings. */
export function VoicePolicyPage() {
  const [policy, setPolicy] = useState<G.AdminVoicePolicyQuery["voicePolicy"]>();
  const [limits, setLimits] = useState<VoicePolicyLimits>(emptyLimits);
  const [enabled, setEnabled] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("Carregando limites atuais…");

  const load = useCallback(async () => {
    setError("");
    try {
      const result = await graphql<G.AdminVoicePolicyQuery>(G.AdminVoicePolicyDocument);
      const current = result.voicePolicy;
      setPolicy(current);
      setEnabled(current?.enabled ?? false);
      setLimits(current ? {
        tenantNoteLimit: toLimit(current.tenantNoteLimit),
        tenantAudioLimitMs: toLimit(current.tenantAudioLimitMs),
        inspectionNoteLimit: toLimit(current.inspectionNoteLimit),
        inspectionAudioLimitMs: toLimit(current.inspectionAudioLimitMs),
      } : emptyLimits);
      setStatus(current?.enabled ? "Anotações por voz habilitadas com os limites efetivos abaixo." : "Anotações por voz desativadas.");
    } catch (failure) {
      setStatus("Não foi possível carregar os limites atuais.");
      setError(failureText(failure));
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const complete = hasCompleteVoicePolicyLimits(limits);
  const missing = voicePolicyMissingLimits(limits);
  const update = (key: keyof VoicePolicyLimits, value: string) => {
    const parsed = value === "" ? "" : Number(value);
    setLimits((current) => ({ ...current, [key]: parsed }));
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (!policy || busy || !complete) return;
    setBusy(true);
    setError("");
    try {
      const result = await graphql<G.AdminConfigureVoicePolicyMutation>(G.AdminConfigureVoicePolicyDocument, {
        input: {
          enabled,
          expectedVersion: policy.version,
          tenantNoteLimit: limits.tenantNoteLimit as number,
          tenantAudioLimitMs: limits.tenantAudioLimitMs as number,
          inspectionNoteLimit: limits.inspectionNoteLimit as number,
          inspectionAudioLimitMs: limits.inspectionAudioLimitMs as number,
          clientMutationId: `voice-policy-${crypto.randomUUID()}`,
        },
      });
      const payload = result.configureVoicePolicy;
      if (payload.userErrors.length) throwAdminUserErrors(payload.userErrors);
      if (!payload.policy) throw new AdminLocalError("Não foi possível salvar a política de voz.");
      setPolicy(payload.policy);
      setStatus(payload.policy.enabled ? "Anotações por voz habilitadas. Os limites efetivos já estão disponíveis para novas capturas." : "Anotações por voz desativadas. As anotações já aceitas mantêm o processamento e os limites originais.");
    } catch (failure) {
      setError(failureText(failure));
      setStatus("As alterações não foram confirmadas. Os limites carregados anteriormente continuam valendo.");
    } finally {
      setBusy(false);
    }
  };

  if (policy === undefined && !error) return <Recovery kind="loading" title="Carregando política de voz">Os limites atuais serão exibidos quando a consulta terminar.</Recovery>;

  return <section className="admin-form" aria-labelledby="voice-policy-heading">
    <h2 id="voice-policy-heading">Disponibilidade e limites</h2>
    <p role="status">{status}</p>
    <p>Informe limites positivos para a imobiliária e para cada vistoria. Os novos limites se aplicam às novas anotações; trabalhos já aceitos continuam válidos.</p>
    <form onSubmit={save}>
      <Field label="Máximo de anotações por imobiliária" required error={limits.tenantNoteLimit !== "" && !Number.isInteger(limits.tenantNoteLimit) ? "Informe um número inteiro positivo." : undefined}>
        <Input aria-label="Máximo de anotações por imobiliária" type="number" min={1} step={1} required value={limits.tenantNoteLimit} onChange={(event) => update("tenantNoteLimit", event.target.value)} />
      </Field>
      <Field label="Áudio total máximo por imobiliária (minutos)" required error={limits.tenantAudioLimitMs !== "" && (!Number.isInteger(limits.tenantAudioLimitMs) || limits.tenantAudioLimitMs <= 0) ? "Informe uma duração positiva em minutos." : undefined}>
        <Input aria-label="Áudio total máximo por imobiliária (minutos)" type="number" min={1} step={1} required value={limits.tenantAudioLimitMs === "" ? "" : limits.tenantAudioLimitMs / 60000} onChange={(event) => update("tenantAudioLimitMs", event.target.value === "" ? "" : String(Number(event.target.value) * 60000))} />
      </Field>
      <Field label="Máximo de anotações por vistoria" required error={limits.inspectionNoteLimit !== "" && !Number.isInteger(limits.inspectionNoteLimit) ? "Informe um número inteiro positivo." : undefined}>
        <Input aria-label="Máximo de anotações por vistoria" type="number" min={1} step={1} required value={limits.inspectionNoteLimit} onChange={(event) => update("inspectionNoteLimit", event.target.value)} />
      </Field>
      <Field label="Áudio total máximo por vistoria (minutos)" required error={limits.inspectionAudioLimitMs !== "" && (!Number.isInteger(limits.inspectionAudioLimitMs) || limits.inspectionAudioLimitMs <= 0) ? "Informe uma duração positiva em minutos." : undefined}>
        <Input aria-label="Áudio total máximo por vistoria (minutos)" type="number" min={1} step={1} required value={limits.inspectionAudioLimitMs === "" ? "" : limits.inspectionAudioLimitMs / 60000} onChange={(event) => update("inspectionAudioLimitMs", event.target.value === "" ? "" : String(Number(event.target.value) * 60000))} />
      </Field>
      <Checkbox label="Habilitar novas anotações por voz" checked={enabled} disabled={!complete} onChange={(event) => setEnabled(event.target.checked)} />
      {!complete && <p role="note">Para habilitar, preencha os quatro limites com números inteiros positivos: {missing.join(", ")}.</p>}
      {error && <Alert tone="danger">{error}</Alert>}
      <Button type="submit" disabled={busy || !policy || !complete} isPending={busy} pendingLabel="Salvando política…">Salvar política de voz</Button>
    </form>
    <aside className="admin-focus-detail" aria-label="Limites efetivos">
      <span className="admin-focus-eyebrow">Limites efetivos</span>
      <dl>
        <div><dt>Imobiliária</dt><dd>{limits.tenantNoteLimit || "Não configurado"} anotações · {limits.tenantAudioLimitMs === "" ? "Não configurado" : `${limits.tenantAudioLimitMs / 60000} min`} de áudio</dd></div>
        <div><dt>Por vistoria</dt><dd>{limits.inspectionNoteLimit || "Não configurado"} anotações · {limits.inspectionAudioLimitMs === "" ? "Não configurado" : `${limits.inspectionAudioLimitMs / 60000} min`} de áudio</dd></div>
        <div><dt>Estado</dt><dd>{enabled ? "Habilitada" : "Desabilitada"}</dd></div>
        <div><dt>Versão da política</dt><dd>{policy?.version ?? "—"}</dd></div>
      </dl>
    </aside>
  </section>;
}
