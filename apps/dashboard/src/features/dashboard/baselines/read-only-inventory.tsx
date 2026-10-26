"use client";

import { conditionLabel, environmentTypeLabel, itemTypeLabel } from "../inventory-presentation";
import { EvidenceStrip } from "./evidence-strip";
import { decisionLabel, type ReviewEnvironment } from "./review-model";

/** ReadOnlyInventory shows an activated or superseded version without review controls. */
export function ReadOnlyInventory({ environments }: { environments: ReviewEnvironment[] }) {
  return <div className="baseline-read-only-inventory">
    {environments.map((environment) => <section className="baseline-environment" key={environment.id} aria-label={environment.name}>
      <header>
        <h3>{environment.name}</h3>
        <span>{environment.expected ? "Ambiente esperado" : "Ambiente adicional"} · {environmentTypeLabel(environment.typeCode)} · {environment.items.length} itens</span>
      </header>
      {environment.items.filter((item) => item.decision !== "REJECTED").map((item) => <article className="baseline-item" key={item.id}>
        <EvidenceStrip item={item} frameLabel="Quadro preservado de" />
        <div className="baseline-item-detail">
          <p>{item.declared ? "Declarado" : decisionLabel(item.decision)}</p>
          <h4>{item.name}</h4>
          <p>{itemTypeLabel(item.typeCode)} · {conditionLabel(item.condition)}</p>
          <p>{itemAttributes(item)}</p>
        </div>
      </article>)}
    </section>)}
  </div>;
}

/** itemAttributes joins the optional descriptive attributes of an item. */
export function itemAttributes(item: { material?: string | null; color?: string | null; quantity?: number | null }) {
  return [item.material, item.color, item.quantity == null ? null : `Quantidade ${item.quantity}`].filter(Boolean).join(" · ") || "Atributos não informados";
}
