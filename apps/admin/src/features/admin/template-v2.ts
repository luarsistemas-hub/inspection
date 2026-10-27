export type TemplateV2CaptureFields = {
  photoMinimum: number;
  photoMaximum: number;
  videoMaximumClips: number;
  suggestedEnvironmentTypes: string[] | string;
};

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
    : `Versão ativa ${versionNumber} · v2 com captura por ambiente.`;
}
