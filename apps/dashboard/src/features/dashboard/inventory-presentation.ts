const purposes: Record<string, string> = {
  MOVE_IN: "Entrada de locação",
  CONSTRUCTION_HANDOVER: "Entrega de obra",
  POST_RENOVATION: "Pós-reforma",
  INVENTORY_UPDATE: "Atualização do inventário",
  CONTESTATION_ADJUSTMENT: "Ajuste de contestação",
};

const conditions: Record<string, string> = {
  NEW: "Novo — Sem sinais de uso",
  GOOD: "Bom — Uso leve, sem danos visíveis",
  FAIR: "Regular — Desgaste ou danos pequenos visíveis que não comprometem o uso",
  POOR: "Ruim — Danos visíveis relevantes ou comprometimento aparente do uso",
  NOT_ASSESSABLE: "Não avaliável — Evidência insuficiente para avaliar",
};

const itemTypes: Record<string, string> = {
  FLOOR: "Piso", WALL: "Parede", CEILING: "Teto", DOOR: "Porta", WINDOW: "Janela",
  LIGHTING: "Iluminação", SOCKET_SWITCH: "Tomada ou interruptor", CABINET: "Armário",
  COUNTERTOP: "Bancada", SINK: "Pia", FAUCET: "Torneira", TOILET_FIXTURE: "Louça sanitária",
  SHOWER_BOX: "Box", BATHTUB: "Banheira", MIRROR: "Espelho", STOVE_COOKTOP: "Fogão ou cooktop",
  OVEN: "Forno", REFRIGERATOR: "Geladeira", AIR_CONDITIONER: "Ar-condicionado",
  WATER_HEATER: "Aquecedor de água", WARDROBE: "Guarda-roupa", BED: "Cama", SOFA: "Sofá",
  TABLE: "Mesa", CHAIR: "Cadeira", CURTAIN_BLIND: "Cortina ou persiana",
  APPLIANCE_OTHER: "Outro eletrodoméstico", FURNITURE_OTHER: "Outro móvel",
  FIXTURE_OTHER: "Outro acabamento ou instalação", OTHER: "Outro",
};

const environmentTypes: Record<string, string> = {
  LIVING_ROOM: "Sala de estar", DINING_ROOM: "Sala de jantar", KITCHEN: "Cozinha",
  BEDROOM: "Quarto", SUITE: "Suíte", BATHROOM: "Banheiro", TOILET: "Lavabo",
  LAUNDRY: "Lavanderia", SERVICE_AREA: "Área de serviço", BALCONY: "Varanda", GARAGE: "Garagem",
  HALLWAY: "Corredor", ENTRANCE: "Entrada", OFFICE: "Escritório", STORAGE: "Depósito",
  EXTERNAL_AREA: "Área externa", POOL_AREA: "Área da piscina", GOURMET_AREA: "Área gourmet", OTHER: "Outro",
};

export const purposeOptions = Object.entries(purposes);

export function purposeLabel(value?: string | null) {
  return value ? purposes[value] ?? value : "Sem propósito";
}

export function conditionLabel(value?: string | null) {
  return value ? conditions[value] ?? "Condição sem definição disponível." : "—";
}

export function itemTypeLabel(value: string) { return itemTypes[value] ?? "Outro"; }
export function environmentTypeLabel(value: string) { return environmentTypes[value] ?? "Outro"; }
export const conditionOptions = Object.entries(conditions);
export const itemTypeOptions = Object.entries(itemTypes);
export const environmentTypeOptions = Object.entries(environmentTypes);

export function buildCoverageSummary(coverage?: { expectedItems: number; assessedItems: number; environmentsAddedDuringCapture: string[] } | null) {
  if (!coverage) return null;
  return { label: `${coverage.assessedItems} de ${coverage.expectedItems} itens avaliados`, addedEnvironments: coverage.environmentsAddedDuringCapture.slice() };
}

export function baselineVersionPresentation(version: { versionNumber: number; purpose?: string | null; activatedAt?: string | null; activatedBy?: string | null; status: string; contestationSummary?: { open?: number | null; acknowledged?: number | null; contested?: number | null; noResponse?: number | null } | null }, authorName?: string) {
  const summary = version.contestationSummary;
  return {
    title: `Versão ${version.versionNumber}`,
    purpose: purposeLabel(version.purpose),
    activatedAt: version.activatedAt ? new Intl.DateTimeFormat("pt-BR", { dateStyle: "medium" }).format(new Date(version.activatedAt)) : "Ainda não ativada",
    author: version.activatedBy ? `Autor ${authorName ?? "membro da equipe"}` : "Autor não informado",
    status: versionStatusLabel(version.status),
    contestation: `${summary?.open ?? 0} abertas · ${summary?.acknowledged ?? 0} aceitas · ${summary?.contested ?? 0} contestadas · ${summary?.noResponse ?? 0} sem resposta no prazo`,
  };
}

/** versionStatusLabel names the lifecycle state of a baseline version. */
export function versionStatusLabel(status: string) {
  return ({ DRAFT: "Rascunho", ACTIVE: "Ativa", SUPERSEDED: "Substituída", DISCARDED: "Descartada", INVALIDATED: "Invalidada" } as Record<string, string>)[status] ?? "Situação não informada";
}

/** versionSourceLabel names how a baseline version was produced. */
export function versionSourceLabel(source?: string | null) {
  return ({ CAPTURE: "captura de origem", LEGACY_ORIGIN: "catalogação de origem existente", COMPARATIVE_INSPECTION: "vistoria comparativa", CONTESTATION: "ajuste por contestação" } as Record<string, string>)[source ?? ""] ?? "origem não informada";
}

/** counterpartyResponseLabel names a counterparty answer to a baseline version. */
export function counterpartyResponseLabel(response: string) {
  return ({ PENDING: "Aguardando resposta", ACKNOWLEDGED: "Ciente", CONTESTED: "Contestou", NO_RESPONSE: "Sem resposta no prazo" } as Record<string, string>)[response] ?? "Resposta não informada";
}
