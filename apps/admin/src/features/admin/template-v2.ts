export type TemplateV2CaptureFields = {
  photoMinimum: number;
  photoMaximum: number;
  videoMaximumClips: number;
  suggestedEnvironmentTypes: string;
};

export function buildEnvironmentCaptureV2(fields: TemplateV2CaptureFields) {
  return {
    captureLayout: "ENVIRONMENTS",
    environmentCapture: {
      photo: { minimum: fields.photoMinimum, maximum: fields.photoMaximum },
      video: { maximumClips: fields.videoMaximumClips },
    },
    suggestedEnvironmentTypes: fields.suggestedEnvironmentTypes.split(",").map((value) => value.trim()).filter(Boolean),
  };
}

export function templateSchemaNotice(schemaVersion: number, versionNumber: number): string {
  return schemaVersion === 1
    ? `Versão ${versionNumber} · v1 sem inventário. Esta versão é somente leitura; crie um novo modelo v2 para usar a captura por ambiente.`
    : `Versão ativa ${versionNumber} · v2 com captura por ambiente.`;
}
