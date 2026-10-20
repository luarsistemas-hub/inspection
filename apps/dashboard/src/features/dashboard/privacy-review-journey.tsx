"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { Dialog } from "@inspection/design-system";
import { graphql } from "@/graphql/client";
import { ReplacementRequest } from "./replacement-request";
import { DeclareSensitiveDetectionFalsePositiveDocument, SensitiveMediaOriginalDocument, SensitiveMediaReviewsDocument, type SensitiveMediaReviewsQuery } from "@/graphql/generated";

type Review = SensitiveMediaReviewsQuery["sensitiveMediaReviews"]["nodes"][number];

export function PrivacyReviewJourney() {
  const params = useSearchParams();
  const [rows, setRows] = useState<Review[]>([]);
  const [after, setAfter] = useState<string | null>(null);
  const [hasNext, setHasNext] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [reason, setReason] = useState<Record<string, string>>({});
  const [original, setOriginal] = useState<{ url: string; kind: string; expiresAt: string } | null>(null);
  const [selected, setSelected] = useState("");
  const autoOpened = useRef("");

  const load = useCallback(async (cursor: string | null = null) => {
    setError("");
    try {
      const result = await graphql(SensitiveMediaReviewsDocument, { first: 25, after: cursor });
      setRows((current) => cursor ? [...current, ...result.sensitiveMediaReviews.nodes] : result.sensitiveMediaReviews.nodes);
      setAfter(result.sensitiveMediaReviews.pageInfo.endCursor ?? null);
      setHasNext(result.sensitiveMediaReviews.pageInfo.hasNextPage);
    } catch (cause) { setError((cause as Error).message || "Não foi possível carregar a fila."); }
  }, []);
  useEffect(() => { void load(); }, [load]);

  async function view(item: Review) {
    setSelected(item.mediaId); setOriginal(null); setError("");
    try {
      const result = await graphql(SensitiveMediaOriginalDocument, { mediaId: item.mediaId });
      const value = result.sensitiveMediaOriginal;
      if (!value) throw new Error("Original indisponível. Solicite uma nova gravação.");
      setOriginal({ url: value.url, kind: value.mediaKind, expiresAt: value.expiresAt });
    } catch (cause) { setError((cause as Error).message || "Original indisponível. Solicite uma nova gravação."); }
  }
  useEffect(() => {
    const mediaId = params.get("mediaId");
    const target = mediaId && rows.find((item) => item.mediaId === mediaId);
    if (target && autoOpened.current !== target.mediaId) {
      autoOpened.current = target.mediaId;
      void view(target);
    }
  }, [params, rows]);
  async function resolve(item: Review) {
    const why = (reason[item.mediaId] ?? "").trim(); if (!why) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const result = await graphql(DeclareSensitiveDetectionFalsePositiveDocument, { input: { mediaId: item.mediaId, reason: why, clientMutationId: crypto.randomUUID() } });
      const errors = result.declareSensitiveDetectionFalsePositive.userErrors;
      if (errors.length) throw new Error(errors.map((entry) => entry.message).join(" "));
      setNotice("Mídia liberada e análise retomada."); setReason((current) => ({ ...current, [item.mediaId]: "" })); await load();
    } catch (cause) { setError((cause as Error).message || "Não foi possível registrar a decisão."); }
    finally { setBusy(false); }
  }

  return <main className="privacy-review-page">
    <header><div><h2>Revisão de privacidade</h2><p>Confira mídias bloqueadas e registre uma decisão para retomar a análise.</p></div><button className="secondary" type="button" onClick={() => void load()}>Atualizar fila</button></header>
    {error && <p role="alert" className="warning">{error}</p>}{notice && <p role="status">{notice}</p>}
    {rows.length ? <ul className="privacy-review-list">{rows.map((item) => <li key={item.mediaId}>
      <div className="privacy-review-meta"><strong>{item.assetName}</strong><span>{item.participantName} · {item.mediaKind === "VIDEO" ? "Vídeo" : "Foto"} · {item.requirementKey}</span><time dateTime={item.blockedAt}>{new Date(item.blockedAt).toLocaleString("pt-BR")}</time><Link href={`/inspections?inspectionId=${encodeURIComponent(item.inspectionId)}`}>Abrir vistoria ↗</Link></div>
      {!item.sourceAvailable && <p role="status">Original indisponível. Solicite uma nova gravação.</p>}
      <div className="privacy-review-actions"><button type="button" className="secondary" disabled={!item.sourceAvailable} onClick={() => void view(item)}>Ver original</button><label>Motivo da liberação<textarea value={reason[item.mediaId] ?? ""} required minLength={4} rows={2} onChange={(event) => setReason((current) => ({ ...current, [item.mediaId]: event.target.value }))} /></label><button type="button" disabled={busy || !item.sourceAvailable || (reason[item.mediaId] ?? "").trim().length < 4} onClick={() => void resolve(item)}>Liberar mídia (falso positivo)</button></div>
      {item.mediaKind === "VIDEO" && item.submitted && <ReplacementRequest inspectionId={item.inspectionId} mediaId={item.mediaId} onRequested={() => void load()} />}
    </li>)}</ul> : <p role="status">{error ? "A fila não está disponível." : "Nenhuma mídia bloqueada aguarda revisão."}</p>}
    {hasNext && after && <button className="secondary" type="button" onClick={() => void load(after)}>Carregar mais</button>}
    <Dialog isOpen={Boolean(selected)} onClose={() => { setSelected(""); setOriginal(null); }} title="Original bloqueado"><div className="privacy-original-viewer">{original ? <>{original.kind === "PHOTO" ? <img src={original.url} alt="Original em revisão" referrerPolicy="no-referrer" /> : <video src={original.url} controls autoPlay playsInline controlsList="nodownload" /> }<small>Link temporário válido até {new Date(original.expiresAt).toLocaleTimeString("pt-BR")}. Ele não será salvo.</small></> : <p role="status">Carregando original…</p>}</div></Dialog>
  </main>;
}
