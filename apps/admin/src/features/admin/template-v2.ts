export type TemplateV2CaptureFields = {
  photoMinimum: number;
  photoMaximum: number;
  videoMaximumClips: number;
  suggestedEnvironmentTypes: string[] | string;
};

export type ExpectedComponentDraft = { key: string; typeCode: string; label: string; views: string[] };
export type ExpectedRoomDraft = { key: string; typeCode: string; name: string; components: ExpectedComponentDraft[] };

const scopeKey = /^[a-z][a-z0-9_-]{0,63}$/;

export function normalizeTemplateScope(rooms: ExpectedRoomDraft[] | null | undefined): ExpectedRoomDraft[] {
  return (rooms ?? []).map((room) => ({
    ...room,
    components: (room.components ?? []).map((component) => ({ ...component, views: (component.views ?? []).filter((view): view is string => typeof view === "string") })),
  }));
}

export function validateTemplateScope(rooms: ExpectedRoomDraft[]): string | undefined {
  const roomKeys = new Set<string>();
  for (const room of rooms) {
    if (!scopeKey.test(room.key.trim())) return `A chave do ambiente “${room.key || room.name}” é inválida. Use letras minúsculas, números, hífen ou sublinhado.`;
    if (!room.name.trim()) return `Informe o nome do ambiente “${room.key}”.`;
    if (roomKeys.has(room.key.trim())) return `A chave do ambiente “${room.key.trim()}” está duplicada.`;
    roomKeys.add(room.key.trim());
    const componentKeys = new Set<string>();
    for (const component of room.components) {
      if (!scopeKey.test(component.key.trim())) return `A chave do componente “${component.key || component.label}” é inválida. Use letras minúsculas, números, hífen ou sublinhado.`;
      if (!component.label.trim()) return `Informe o nome do componente “${component.key}”.`;
      if (componentKeys.has(component.key.trim())) return `A chave de componente “${component.key.trim()}” está duplicada.`;
      componentKeys.add(component.key.trim());
      const views = new Set<string>();
      for (const view of component.views) {
        if (!scopeKey.test(view.trim())) return `A chave da vista “${view || component.label}” é inválida. Use letras minúsculas, números, hífen ou sublinhado.`;
        if (views.has(view.trim())) return `A vista “${view.trim()}” está duplicada neste componente.`;
        views.add(view.trim());
      }
    }
  }
  return undefined;
}

export function buildTemplateScopeV3(assetProfile: string, expectedRooms: ExpectedRoomDraft[], handoverRecords: Array<{ key: string; kind: string; required: boolean }>) {
  return { schemaVersion: 3, assetProfile, expectedRooms, handoverRecords };
}

export const suggestedEnvironmentOptions = [
  { code: "LIVING_ROOM", label: "Sala de estar" },
  { code: "DINING_ROOM", label: "Sala de jantar" },
  { code: "KITCHEN", label: "Cozinha" },
  { code: "BEDROOM", label: "Quarto" },
  { code: "SUITE", label: "Suíte" },
  { code: "BATHROOM", label: "Banheiro" },
  { code: "TOILET", label: "Lavabo" },
  { code: "LAUNDRY", label: "Lavanderia" },
  { code: "SERVICE_AREA", label: "Área de serviço" },
  { code: "BALCONY", label: "Varanda" },
  { code: "GARAGE", label: "Garagem" },
  { code: "HALLWAY", label: "Corredor" },
  { code: "ENTRANCE", label: "Entrada" },
  { code: "OFFICE", label: "Escritório" },
  { code: "STORAGE", label: "Depósito" },
  { code: "EXTERNAL_AREA", label: "Área externa" },
  { code: "POOL_AREA", label: "Área da piscina" },
  { code: "GOURMET_AREA", label: "Área gourmet" },
  { code: "OTHER", label: "Outro" },
] as const;

export const defaultSuggestedEnvironmentTypes = ["LIVING_ROOM", "KITCHEN", "BEDROOM", "BATHROOM", "OTHER"];

export const componentTypeOptions = [
  ["FLOOR", "Piso"], ["WALL", "Parede"], ["CEILING", "Teto"], ["DOOR", "Porta"], ["WINDOW", "Janela"],
  ["LIGHTING", "Iluminação"], ["SOCKET_SWITCH", "Tomadas e interruptores"], ["CABINET", "Armários"], ["COUNTERTOP", "Bancada"],
  ["SINK", "Pia"], ["FAUCET", "Torneira"], ["TOILET_FIXTURE", "Louça sanitária"], ["SHOWER_BOX", "Box do banheiro"],
  ["BATHTUB", "Banheira"], ["MIRROR", "Espelho"], ["STOVE_COOKTOP", "Fogão ou cooktop"], ["OVEN", "Forno"],
  ["REFRIGERATOR", "Geladeira"], ["AIR_CONDITIONER", "Ar-condicionado"], ["WATER_HEATER", "Aquecedor de água"],
  ["WARDROBE", "Guarda-roupa"], ["BED", "Cama"], ["SOFA", "Sofá"], ["TABLE", "Mesa"], ["CHAIR", "Cadeira"],
  ["CURTAIN_BLIND", "Cortina ou persiana"], ["APPLIANCE_OTHER", "Outro eletrodoméstico"], ["FURNITURE_OTHER", "Outro móvel"],
  ["FIXTURE_OTHER", "Outro componente fixo"], ["OTHER", "Outro"],
] as const;

export function buildEnvironmentCaptureV2(fields: TemplateV2CaptureFields) {
  return {
    captureLayout: "ENVIRONMENTS",
    environmentCapture: {
      photo: { minimum: fields.photoMinimum, maximum: fields.photoMaximum },
      video: { maximumClips: fields.videoMaximumClips },
    },
    suggestedEnvironmentTypes: typeof fields.suggestedEnvironmentTypes === "string"
      ? fields.suggestedEnvironmentTypes.split(",").map((value) => value.trim()).filter(Boolean)
      : fields.suggestedEnvironmentTypes,
  };
}

export function templateSchemaNotice(schemaVersion: number, versionNumber: number): string {
  return schemaVersion === 1
    ? `Versão ${versionNumber} · v1 sem inventário. Esta versão é somente leitura; crie um novo modelo v2 para usar a captura por ambiente.`
    : schemaVersion === 2 ? `Versão ativa ${versionNumber} · v2 com captura por ambiente.` : `Versão ativa ${versionNumber} · v3 com escopo descritivo.`;
}
