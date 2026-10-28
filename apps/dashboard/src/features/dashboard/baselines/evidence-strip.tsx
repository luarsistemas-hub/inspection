"use client";

import { EvidenceImage } from "../report-visual";
import { formatEvidenceTime, inventoryAvailabilityLabel } from "./review-model";

/** StripEvidence is the evidence shape shared by drafts and published inventories. */
export type StripEvidence = { id: string; kind: string; url?: string | null; timestampMs?: number | null; availability: string; retentionAvailability?: string | null };

/** EvidenceStrip renders an item's evidence with its availability caption. */
export function EvidenceStrip({ item, frameLabel }: { item: { name: string; evidence: StripEvidence[] }; frameLabel: string }) {
  if (!item.evidence.length) return <div className="baseline-item-evidence"><span>Evidência indisponível</span></div>;
  return <div className="baseline-item-evidence">
    {/* Customer projections omit evidence ids, so the position keys the figure. */}
    {item.evidence.map((evidence, index) => <figure key={evidence.id || `evidence-${index}`}>
      {evidence.url
        ? <EvidenceImage url={evidence.url} alt={evidence.kind === "VIDEO_FRAME" ? `${frameLabel} ${item.name}` : `Evidência de ${item.name}`} />
        : <span>Evidência indisponível</span>}
      <figcaption>
        {inventoryAvailabilityLabel(evidence.retentionAvailability ?? evidence.availability)}
        {evidence.timestampMs != null ? ` · ${formatEvidenceTime(evidence.timestampMs)}` : ""}
      </figcaption>
    </figure>)}
  </div>;
}
