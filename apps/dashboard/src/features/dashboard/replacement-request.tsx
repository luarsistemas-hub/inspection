"use client";

import { useState } from "react";
import { graphql } from "@/graphql/client";
import { RequestVideoReplacementDocument } from "@/graphql/generated";

const dayMs = 24 * 60 * 60 * 1000;

/** Default replacement deadline: three days from now, formatted for a datetime-local input. */
export function defaultReplacementDeadline(now: Date = new Date()): string {
  const local = new Date(now.getTime() + 3 * dayMs - now.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

/** Validates the manager input and returns the ISO deadline or a participant-safe error message. */
export function parseReplacementInput(deadline: string, reason: string, now: Date = new Date()): { deadlineAt: string } | { error: string } {
  if (reason.trim().length < 4) return { error: "Informe o motivo da nova gravação." };
  const parsed = new Date(deadline);
  if (Number.isNaN(parsed.getTime()) || parsed.getTime() <= now.getTime()) return { error: "Escolha um prazo no futuro." };
  if (parsed.getTime() > now.getTime() + 30 * dayMs) return { error: "O prazo pode ser de no máximo 30 dias." };
  return { deadlineAt: parsed.toISOString() };
}

export function ReplacementRequest({ inspectionId, mediaId, onRequested }: { inspectionId: string; mediaId: string; onRequested?: () => void }) {
  const [open, setOpen] = useState(false);
  const [deadline, setDeadline] = useState(() => defaultReplacementDeadline());
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function submit() {
    const parsed = parseReplacementInput(deadline, reason);
    if ("error" in parsed) { setError(parsed.error); return; }
    setBusy(true); setError(""); setNotice("");
    try {
      const result = await graphql(RequestVideoReplacementDocument, { input: { inspectionId, mediaId, deadlineAt: parsed.deadlineAt, reason: reason.trim(), clientMutationId: crypto.randomUUID() } });
      const errors = result.requestVideoReplacement.userErrors;
      if (errors.length) throw new Error(errors.map((entry) => entry.message).join(" "));
      setNotice("Nova gravação solicitada. O participante receberá um novo convite."); setOpen(false); setReason("");
      onRequested?.();
    } catch (cause) { setError((cause as Error).message || "Não foi possível solicitar a nova gravação."); }
    finally { setBusy(false); }
  }

  return <div className="replacement-request">
    {!open && <button type="button" className="secondary" onClick={() => setOpen(true)}>Pedir nova gravação</button>}
    {open && <form onSubmit={(event) => { event.preventDefault(); void submit(); }}>
      <label>Prazo para enviar<input type="datetime-local" value={deadline} onChange={(event) => setDeadline(event.target.value)} required /></label>
      <label>Motivo da nova gravação<textarea value={reason} rows={2} minLength={4} required onChange={(event) => setReason(event.target.value)} /></label>
      <div><button type="submit" disabled={busy}>{busy ? "Enviando…" : "Enviar pedido"}</button><button type="button" className="secondary" disabled={busy} onClick={() => setOpen(false)}>Cancelar</button></div>
    </form>}
    {error && <p role="alert" className="warning">{error}</p>}{notice && <p role="status">{notice}</p>}
  </div>;
}
