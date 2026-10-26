"use client";

import { useEffect, useState } from "react";
import { graphql } from "@/graphql/client";
import * as G from "@/graphql/generated";
import { failureText } from "./review-model";
import { formatTrailTime, sortTrail, trailActionLabel, trailAuthor, trailDetails, type InventoryTrail, type TrailEntry } from "./trail-model";

/** InventoryTrailView is the read-only audit trail of one inventory version. */
export function InventoryTrailView({ versionId }: { versionId: string }) {
  const [trail, setTrail] = useState<InventoryTrail>();
  const [error, setError] = useState("");
  const [names, setNames] = useState(new Map<string, string>());
  useEffect(() => {
    setTrail(undefined);
    setError("");
    void graphql<G.DashboardInventoryTrailQuery, G.DashboardInventoryTrailQueryVariables>(G.DashboardInventoryTrailDocument, { originVersionId: versionId })
      .then((result) => setTrail(result.inventoryTrail))
      .catch((failure) => setError(failureText(failure)));
    // Member names are best effort: an auditor may not list memberships.
    void graphql<G.DashboardReviewersQuery, G.DashboardReviewersQueryVariables>(G.DashboardReviewersDocument, { first: 100, after: null })
      .then((result) => setNames(new Map(result.memberships.nodes.map((member) => [member.id, member.name]))))
      .catch(() => setNames(new Map()));
  }, [versionId]);
  return <section className="baseline-trail" aria-labelledby="baseline-trail-title">
    <h2 id="baseline-trail-title">Trilha de auditoria</h2>
    <p>Consulta somente leitura das sugestões, decisões, ativação e manifestações desta versão.</p>
    {error && <p role="alert">{error}</p>}
    {!trail && !error && <p role="status">Carregando trilha…</p>}
    {trail && <>
      <TrailSection title="Sugestões da análise" entries={trail.suggestions} names={names} empty="Nenhuma sugestão automática registrada." />
      <TrailSection title="Decisões da equipe" entries={trail.decisions} names={names} empty="Nenhuma decisão registrada." />
      <TrailSection title="Contraparte" entries={trail.counterpartyEvents} names={names} empty="Nenhum convite à contraparte." />
    </>}
  </section>;
}

function TrailSection({ title, entries, names, empty }: { title: string; entries: TrailEntry[]; names: Map<string, string>; empty: string }) {
  return <section aria-label={title}>
    <h3>{title}</h3>
    {entries.length === 0 ? <p>{empty}</p> : <ol className="baseline-trail-list">
      {sortTrail(entries).map((entry) => <li key={entry.id}>
        <p><strong>{trailActionLabel(entry)}</strong> · {trailAuthor(entry, names)} · <time dateTime={entry.occurredAt}>{formatTrailTime(entry.occurredAt)}</time></p>
        {trailDetails(entry).map((detail) => <p key={detail}>{detail}</p>)}
      </li>)}
    </ol>}
  </section>;
}
