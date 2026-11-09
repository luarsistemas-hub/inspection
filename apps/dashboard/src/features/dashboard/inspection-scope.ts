export function isScopeVersionConflict(code?: string | null): boolean {
  return code === "CONFLICT";
}

export async function confirmScopeAndRefresh<T>(expectedVersion: number, confirm: (version: number) => Promise<{ error?: string; code?: string | null; value?: T }>, reload: () => Promise<void>): Promise<{ value?: T; error?: string; conflict: boolean }> {
  const result = await confirm(expectedVersion);
  if (!result.error) return { value: result.value, conflict: false };
  const conflict = isScopeVersionConflict(result.code);
  if (conflict) await reload();
  return { error: result.error, conflict };
}

export function scopeSelectionKey(roomKey: string, componentKey: string): string {
  return `${roomKey}:${componentKey}`;
}

export function scopeSelectionInput(keys: string[]): Array<{ roomKey: string; componentKey: string }> {
  return keys.map((identity) => {
    const separator = identity.indexOf(":");
    return { roomKey: identity.slice(0, separator), componentKey: identity.slice(separator + 1) };
  }).filter((item) => item.roomKey && item.componentKey);
}

export function friendlyScopeView(view: string): string {
  if (view.toLowerCase() === "general" || view.toLowerCase() === "geral") return "vista geral";
  return view.replace(/[_-]+/g, " ");
}

export function friendlyPendingRecord(key: string): string {
  const normalized = key.toLowerCase();
  if (normalized.includes("key") || normalized.includes("chave")) return "Chaves";
  if (normalized.includes("control")) return "Controles";
  if (normalized.includes("meter") || normalized.includes("medidor")) return "Medidores";
  if (normalized.includes("note") || normalized.includes("observ")) return "Observações";
  return key.replace(/[_-]+/g, " ");
}
