"use client";

import type * as G from "@/graphql/generated";
import { conditionLabel, environmentTypeLabel, itemTypeLabel, purposeLabel } from "../inventory-presentation";
import { EvidenceStrip } from "./evidence-strip";
import { itemAttributes } from "./read-only-inventory";

type BaselineInventory = NonNullable<G.DashboardBaselineInventoryQuery["baselineInventory"]>;
const includedDecisions = ["ACCEPTED", "EDITED", "ADDED"];

/** PublishedInventory shows an activated inventory without review controls or history. */
export function PublishedInventory({ inventory, isCustomer }: { inventory: BaselineInventory; isCustomer: boolean }) {
  const version = inventory.version;
  if (!version) return null;
  const activatedAt = version.activatedAt ? new Intl.DateTimeFormat("pt-BR", { dateStyle: "long" }).format(new Date(version.activatedAt)) : null;
  return <section className="baseline-customer-view" aria-labelledby="published-inventory-heading">
    <h3 id="published-inventory-heading">Versão {version.versionNumber} · {purposeLabel(version.purpose)}</h3>
    <p>{activatedAt ? `Ativada em ${activatedAt}.` : "Data de ativação não informada."}</p>
    <p>{isCustomer ? "Inventário ativo do imóvel. As sugestões rejeitadas e o histórico de revisão não são exibidos." : "Inventário em modo somente leitura."}</p>
    {/* Customer projections omit internal ids, so positions key the lists. */}
    {inventory.environments.map((environment, environmentIndex) => {
      const items = isCustomer ? environment.items : environment.items.filter((item) => includedDecisions.includes(item.decision));
      return <section className="baseline-environment" key={environment.id || `environment-${environmentIndex}`} aria-label={environment.name}>
        <header><h4>{environment.name}</h4><span>{environmentTypeLabel(environment.typeCode)} · {items.length} itens</span></header>
        {items.map((item, itemIndex) => <article className="baseline-item" key={item.id || `item-${itemIndex}`}>
          <EvidenceStrip item={item} frameLabel="Quadro preservado de" />
          <div className="baseline-item-detail">
            <h5>{item.name}</h5>
            <p>{itemTypeLabel(item.typeCode)} · {conditionLabel(item.condition)}{item.declared ? " · Declarado" : ""}</p>
            <p>{itemAttributes(item)}</p>
          </div>
        </article>)}
      </section>;
    })}
  </section>;
}
