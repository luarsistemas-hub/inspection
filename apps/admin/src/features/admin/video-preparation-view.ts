export function preparationFrameMetrics(metrics: { framesSampled: number; framesRetained: number; framesRepresentative: number }): string {
  return `Quadros: ${metrics.framesSampled} amostrados · ${metrics.framesRetained} mantidos · ${metrics.framesRepresentative} enviados à análise`;
}
