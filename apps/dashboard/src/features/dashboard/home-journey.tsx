"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import {
  InspectionsDocument,
  OperationalOverviewDocument,
  type InspectionsQuery,
  type OperationalOverviewQuery,
} from "@/graphql/generated";
import { graphql } from "@/graphql/client";
import { formatScheduleDateTime } from "./schedules-journey";
import { presentDashboardStatus, presentInspectionSource } from "./presentation";

type Inspection = InspectionsQuery["inspections"]["nodes"][number];

export function HomeJourney({ refreshKey }: { refreshKey: number }) {
  const [overview, setOverview] = useState<OperationalOverviewQuery>();
  const [inspections, setInspections] = useState<Inspection[]>([]);
  const [message, setMessage] = useState("Carregando as vistorias…");
  const request = useRef(0);

  useEffect(() => {
    let active = true;
    const currentRequest = ++request.current;
    setMessage("Atualizando as vistorias…");
    void Promise.all([
      graphql<OperationalOverviewQuery, { after: string | null; classification: string | null; status: string | null }>(
        OperationalOverviewDocument,
        { after: null, classification: null, status: null },
      ),
      graphql<InspectionsQuery, { first: number; after: string | null; history: boolean; search: string | null; statusGroup: null }>(
        InspectionsDocument,
        { first: 100, after: null, history: false, search: null, statusGroup: null },
      ),
    ]).then(([nextOverview, nextInspections]) => {
      if (!active || request.current !== currentRequest) return;
      setOverview(nextOverview);
      setInspections(nextInspections.inspections.nodes.filter(isOpenInspection));
      setMessage("Vistorias atualizadas.");
    }).catch(() => {
      if (!active || request.current !== currentRequest) return;
      setMessage("Não foi possível atualizar as vistorias agora. Tente novamente.");
    });
    return () => { active = false; };
  }, [refreshKey]);

  const summary = overview?.dashboardSummary;
  const needsAttention = inspections.filter((inspection) =>
    inspection.status === "RECAPTURE_PENDING" || isPastDeadline(inspection.deadlineAt),
  ).sort(byDueAt);
  const attentionIds = new Set(needsAttention.map((inspection) => inspection.id));
  const upcoming = inspections.filter((inspection) => !attentionIds.has(inspection.id)).sort(byDueAt).slice(0, 5);

  return <div className="feature home-journey">
    {summary && <section className="home-summary" aria-label="Resumo das vistorias">
      <Link className="home-stat home-stat--attention" href="/triage">
        <span>Vistorias com atenção</span><strong>{summary.attention}</strong>
      </Link>
      <Link className="home-stat home-stat--critical" href="/triage">
        <span>Vistorias críticas</span><strong>{summary.critical}</strong>
      </Link>
      <Link className="home-stat" href="/inspections">
        <span>Vistorias pendentes</span><strong>{summary.pending}</strong>
      </Link>
    </section>}

    <section className="home-section" aria-labelledby="home-attention-title">
      <div className="home-section-heading"><div><h2 id="home-attention-title">Precisam de atenção</h2><p>Vistorias com prazo vencido ou complemento solicitado.</p></div><Link href="/triage">Abrir triagem →</Link></div>
      <ul className="home-inspection-list">
        {needsAttention.slice(0, 4).map((inspection) => <InspectionRow inspection={inspection} key={inspection.id} attention />)}
      </ul>
      {!needsAttention.length && <p className="home-empty">Nenhuma vistoria precisa de atenção agora.</p>}
      {needsAttention.length > 4 && <Link className="home-more" href="/inspections">Ver as outras {needsAttention.length - 4} pendências →</Link>}
    </section>

    <section className="home-section" aria-labelledby="home-upcoming-title">
      <div className="home-section-heading"><div><h2 id="home-upcoming-title">Vistorias a realizar</h2><p>Acompanhe o imóvel, o horário e a situação de cada vistoria.</p></div><Link href="/inspections">Ver todas →</Link></div>
      <ul className="home-inspection-list">
        {upcoming.map((inspection) => <InspectionRow inspection={inspection} key={inspection.id} />)}
      </ul>
      {!upcoming.length && <p className="home-empty">Não há vistorias a realizar neste contexto.</p>}
    </section>

    <p className="home-load-status" role="status" aria-live="polite">{message}</p>
  </div>;
}

function InspectionRow({ inspection, attention = false }: { inspection: Inspection; attention?: boolean }) {
  const title = inspection.assetName?.trim() || "Imóvel indisponível";
  return <li className={`home-inspection-row${attention ? " home-inspection-row--attention" : ""}`}>
    <div className="home-inspection-main">
      <span className="home-inspection-type">{presentInspectionSource(inspection.source)}</span>
      <h3><Link href={`/inspections?inspectionId=${encodeURIComponent(inspection.id)}`}>{title}</Link></h3>
      {inspection.assetAddress && <span>{inspection.assetAddress}</span>}
      <span>Responsável · {inspection.participantName?.trim() || "Indisponível"}</span>
    </div>
    <div className="home-inspection-side">
      <span className={`home-status${attention ? " home-status--attention" : ""}`}>{presentDashboardStatus(inspection.status)}</span>
      <strong>{formatScheduleDateTime(inspection.dueAt)}</strong>
      {attention && <span>{inspection.status === "RECAPTURE_PENDING" ? "Complemento solicitado" : "Prazo vencido"}</span>}
      {inspection.status === "COMPLETED" && <Link href={`/reports?inspectionId=${encodeURIComponent(inspection.id)}`}>Abrir laudo →</Link>}
      {inspection.status !== "COMPLETED" && <Link href={`/inspections?inspectionId=${encodeURIComponent(inspection.id)}`}>Ver vistoria →</Link>}
    </div>
  </li>;
}

function isOpenInspection(inspection: Inspection): boolean {
  return !["COMPLETED", "CANCELED", "INVALIDATED"].includes(inspection.status);
}

function isPastDeadline(value: string): boolean {
  const time = Date.parse(value);
  return Number.isFinite(time) && time < Date.now();
}

function byDueAt(left: Inspection, right: Inspection): number {
  return Date.parse(left.dueAt) - Date.parse(right.dueAt);
}
