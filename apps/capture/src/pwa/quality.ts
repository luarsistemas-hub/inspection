export type CaptureQualityCode = "DARK" | "BLURRY" | "SHORT" | "TOO_FEW_PHOTOS";
export type LumaAnalysis = { meanLuma: number; laplacianVariance: number };
export type QualityWarningState = Record<string, CaptureQualityCode[]>;
export type PersistedQualityState = { warnings: QualityWarningState; dismissed: Record<string, CaptureQualityCode[]> };
const qualityStorageKey = (responsibilityId: string) => `inspection-capture-quality-v1:${responsibilityId}`;

export function loadQualityState(responsibilityId: string): PersistedQualityState {
  try {
    const value = JSON.parse(localStorage.getItem(qualityStorageKey(responsibilityId)) ?? "null") as Partial<PersistedQualityState> | null;
    return { warnings: value?.warnings ?? {}, dismissed: value?.dismissed ?? {} };
  } catch { return { warnings: {}, dismissed: {} }; }
}

export function saveQualityState(responsibilityId: string, state: PersistedQualityState): void {
  try { localStorage.setItem(qualityStorageKey(responsibilityId), JSON.stringify(state)); } catch { /* Warnings remain available for this session. */ }
}

export function setQualityWarnings(state: QualityWarningState, requirementKey: string, warnings: CaptureQualityCode[]): QualityWarningState {
  return { ...state, [requirementKey]: [...new Set(warnings)] };
}

export function dismissQualityWarnings(state: Record<string, CaptureQualityCode[]>, requirementKey: string, warnings: CaptureQualityCode[]): Record<string, CaptureQualityCode[]> {
  return { ...state, [requirementKey]: [...new Set([...(state[requirementKey] ?? []), ...warnings])] };
}

export function submitFailOpen<T>(warnings: Array<{ requirementKey: string; code: CaptureQualityCode }>, report: (warning: { requirementKey: string; code: CaptureQualityCode }) => Promise<unknown>, submit: () => Promise<T>): Promise<T> {
  for (const warning of warnings) void report(warning).catch(() => {});
  return submit();
}

/** Computes grayscale luma and the variance of its 4-neighbour Laplacian. */
export function analyzeLumaPixels(pixels: ArrayLike<number>, width: number, height: number): LumaAnalysis {
  if (!Number.isInteger(width) || !Number.isInteger(height) || width < 3 || height < 3 || pixels.length < width * height * 4) throw new Error("Imagem indisponível para análise.");
  const luma = new Float64Array(width * height);
  let sum = 0;
  for (let index = 0; index < width * height; index += 1) {
    const offset = index * 4;
    const value = 0.2126 * pixels[offset] + 0.7152 * pixels[offset + 1] + 0.0722 * pixels[offset + 2];
    luma[index] = value;
    sum += value;
  }
  let laplacianSum = 0;
  let laplacianSquareSum = 0;
  let count = 0;
  for (let y = 1; y < height - 1; y += 1) for (let x = 1; x < width - 1; x += 1) {
    const index = y * width + x;
    const value = luma[index - width] + luma[index - 1] + luma[index + 1] + luma[index + width] - 4 * luma[index];
    laplacianSum += value;
    laplacianSquareSum += value * value;
    count += 1;
  }
  const mean = laplacianSum / count;
  return { meanLuma: sum / luma.length, laplacianVariance: Math.max(0, laplacianSquareSum / count - mean * mean) };
}

export async function analyzeCapturePhoto(file: Blob): Promise<LumaAnalysis> {
  if (typeof createImageBitmap !== "function") throw new Error("Análise de imagem indisponível.");
  const bitmap = await createImageBitmap(file);
  try {
    const scale = Math.min(1, 320 / Math.max(bitmap.width, bitmap.height));
    const width = Math.max(3, Math.round(bitmap.width * scale));
    const height = Math.max(3, Math.round(bitmap.height * scale));
    const canvas = document.createElement("canvas");
    canvas.width = width; canvas.height = height;
    const context = canvas.getContext("2d", { willReadFrequently: true });
    if (!context) throw new Error("Análise de imagem indisponível.");
    context.drawImage(bitmap, 0, 0, width, height);
    return analyzeLumaPixels(context.getImageData(0, 0, width, height).data, width, height);
  } finally { bitmap.close(); }
}

export function photoQualityWarnings(analysis: LumaAnalysis, photoCount: number, minimumPhotos: number): CaptureQualityCode[] {
  const warnings: CaptureQualityCode[] = [];
  if (analysis.meanLuma < 40) warnings.push("DARK");
  if (analysis.laplacianVariance < 50) warnings.push("BLURRY");
  if (photoCount < minimumPhotos) warnings.push("TOO_FEW_PHOTOS");
  return warnings;
}

export const clipQualityWarnings = (durationSeconds: number): CaptureQualityCode[] => durationSeconds < 2 ? ["SHORT"] : [];
export const clipIsTooShort = (durationSeconds: number): boolean => durationSeconds < 1;

export async function failOpenQuality<T>(analyze: () => Promise<T>): Promise<T | undefined> {
  try { return await analyze(); } catch { return undefined; }
}
