export type PendingPart = { number: number; etag?: string; complete: boolean };
export type CaptureGPS = { latitude: number; longitude: number; accuracyMeters: number; capturedAt: string; windowStartedAt: string };
export type CaptureDraft = {
  schemaVersion?: 1; id: string; responsibilityId: string; blob: Blob; sha256: string;
  uploadId?: string; mediaId?: string; parts: PendingPart[];
  mediaStatus?: string;
  metadataSaved?: boolean;
  metadata: { requirementKey: string; description: string; source: "camera" | "gallery"; capturedAt: string; gps?: CaptureGPS; deviceContext?: Record<string, unknown> };
};
export type CaptureAnswer = { requirementKey: string; mediaIds: string[] };

const databaseName = "inspection-capture-v3";
const storeName = "drafts";
const quarantineStore = "quarantine";
const databaseVersion = 1;

function open(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(databaseName, databaseVersion);
    request.onupgradeneeded = () => { const db = request.result; const drafts = db.createObjectStore(storeName, { keyPath: "id" }); drafts.createIndex("responsibilityId", "responsibilityId", { unique: false }); db.createObjectStore(quarantineStore, { autoIncrement: true }); };
    request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error);
  });
}

const isNonEmptyString = (value: unknown): value is string => typeof value === "string" && value.length > 0;
const isRecord = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);

function valid(draft: unknown): draft is CaptureDraft {
  if (!isRecord(draft)) return false;
  const value = draft as Partial<CaptureDraft>;
  if (value.schemaVersion !== 1 || !isNonEmptyString(value.id) || !isNonEmptyString(value.responsibilityId) || !value.blob || !isNonEmptyString(value.sha256)) return false;
  if (!Array.isArray(value.parts)) return false;
  const preUpload = value.parts.length === 0 && value.uploadId === undefined && value.mediaId === undefined && value.metadataSaved === false;
  if (value.parts.length === 0 && !preUpload) return false;
  const partNumbers = new Set<number>();
  if (!value.parts.every((part) => {
    if (!isRecord(part) || !Number.isInteger(part.number) || part.number < 1 || typeof part.complete !== "boolean" || (part.etag !== undefined && !isNonEmptyString(part.etag)) || (part.complete && !isNonEmptyString(part.etag))) return false;
    if (partNumbers.has(part.number)) return false;
    partNumbers.add(part.number);
    return true;
  })) return false;
  if (!isRecord(value.metadata) || !isNonEmptyString(value.metadata.requirementKey) || typeof value.metadata.description !== "string" || (value.metadata.source !== "camera" && value.metadata.source !== "gallery") || !isNonEmptyString(value.metadata.capturedAt)) return false;
  if (value.metadata.gps !== undefined && (!isRecord(value.metadata.gps) || typeof value.metadata.gps.latitude !== "number" || !Number.isFinite(value.metadata.gps.latitude) || typeof value.metadata.gps.longitude !== "number" || !Number.isFinite(value.metadata.gps.longitude) || typeof value.metadata.gps.accuracyMeters !== "number" || !Number.isFinite(value.metadata.gps.accuracyMeters) || !isNonEmptyString(value.metadata.gps.capturedAt) || !isNonEmptyString(value.metadata.gps.windowStartedAt))) return false;
  return value.metadata.deviceContext === undefined || isRecord(value.metadata.deviceContext);
}

async function transact<T>(store: string, mode: IDBTransactionMode, action: (objectStore: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const db = await open();
  try { return await new Promise<T>((resolve, reject) => { const request = action(db.transaction(store, mode).objectStore(store)); request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error); }); } finally { db.close(); }
}

async function quarantine(value: unknown): Promise<void> {
  const db = await open();
  try {
    await new Promise<void>((resolve, reject) => {
      const transaction = db.transaction([storeName, quarantineStore], "readwrite");
      transaction.objectStore(quarantineStore).add({ value, quarantinedAt: new Date().toISOString() });
      if (isRecord(value) && typeof value.id === "string") transaction.objectStore(storeName).delete(value.id);
      transaction.oncomplete = () => resolve();
      transaction.onerror = () => reject(transaction.error);
      transaction.onabort = () => reject(transaction.error);
    });
  } finally { db.close(); }
}

export async function hasDraftCapacity(bytes: number): Promise<boolean> {
  const estimate = await navigator.storage?.estimate?.();
  return !estimate?.quota || !estimate.usage || estimate.usage + bytes < estimate.quota * 0.9;
}

export async function saveDraft(draft: CaptureDraft): Promise<void> {
  if (!await hasDraftCapacity(draft.blob.size)) throw new Error("O armazenamento deste dispositivo está quase cheio. Libere espaço antes de salvar outra foto.");
  await transact(storeName, "readwrite", (store) => store.put({ ...draft, schemaVersion: 1 }));
}

export async function persistDraftMediaStatus(draft: CaptureDraft, mediaStatus: string): Promise<CaptureDraft> {
  const updated = { ...draft, mediaStatus };
  await saveDraft(updated);
  return updated;
}

export async function loadDraft(id: string): Promise<CaptureDraft | undefined> {
  const value = await transact<unknown>(storeName, "readonly", (store) => store.get(id));
  if (valid(value)) return value;
  if (value !== undefined) await quarantine(value);
  return undefined;
}

export async function loadDraftsForResponsibility(responsibilityId: string): Promise<CaptureDraft[]> {
  const db = await open();
  try {
    const values = await new Promise<unknown[]>((resolve, reject) => { const request = db.transaction(storeName, "readonly").objectStore(storeName).index("responsibilityId").getAll(responsibilityId); request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error); });
    const validDrafts: CaptureDraft[] = [];
    for (const value of values) { if (valid(value)) validDrafts.push(value); else await quarantine(value); }
    return validDrafts;
  } finally { db.close(); }
}

export const removeDraft = (id: string): Promise<unknown> => transact(storeName, "readwrite", (store) => store.delete(id));
export async function removeDraftsForResponsibility(responsibilityId: string): Promise<void> { await Promise.all((await loadDraftsForResponsibility(responsibilityId)).map((draft) => removeDraft(draft.id))); }
export async function digest(file: Blob): Promise<string> { return Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", await file.arrayBuffer()))).map((byte) => byte.toString(16).padStart(2, "0")).join(""); }
export const mediaCountForRequirement = (requirementKey: string, answers: CaptureAnswer[], drafts: CaptureDraft[]): number => {
  const blockedStatuses = new Set(["SCREENED", "REJECTED", "PURGED", "ABORTED"]);
  const blockedMediaIds = new Set(drafts.filter((draft) => blockedStatuses.has(draft.mediaStatus ?? "") && draft.mediaId).map((draft) => draft.mediaId));
  const mediaIds = new Set(answers.filter((answer) => answer.requirementKey === requirementKey).flatMap((answer) => answer.mediaIds).filter((mediaId) => Boolean(mediaId) && !blockedMediaIds.has(mediaId)));
  let count = mediaIds.size;
  for (const draft of drafts) {
    if (blockedStatuses.has(draft.mediaStatus ?? "")) continue;
    if (draft.metadata.requirementKey !== requirementKey || (draft.mediaId && mediaIds.has(draft.mediaId))) continue;
    if (draft.mediaId) mediaIds.add(draft.mediaId);
    count += 1;
  }
  return count;
};
export const readyForSubmission = (drafts: CaptureDraft[], online: boolean, allRequirementsSatisfied?: boolean, confirmIncomplete = false): boolean => {
  if (!online) return false;
  const mediaReady = drafts.length > 0 && drafts.every((draft) => !["SCREENED", "REJECTED", "PURGED", "ABORTED"].includes(draft.mediaStatus ?? "") && !!draft.mediaId && draft.metadataSaved !== false && draft.parts.length > 0 && draft.parts.every((part) => part.complete));
  if (allRequirementsSatisfied === undefined) return mediaReady;
  return (confirmIncomplete || allRequirementsSatisfied) && (drafts.length === 0 || mediaReady);
};
