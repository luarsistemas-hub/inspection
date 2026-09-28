export type PendingPart = { number: number; etag?: string; complete: boolean };
export type CaptureGPS = { latitude: number; longitude: number; accuracyMeters: number; capturedAt: string; windowStartedAt: string };
export type CaptureDraft = {
  schemaVersion?: 1; id: string; responsibilityId: string; blob: Blob; sha256: string;
  sourceSha256?: string; imageProfile?: string;
  uploadId?: string; mediaId?: string; parts: PendingPart[];
  expiresAt?: string; partSizeBytes?: number;
  mediaStatus?: string;
  replacesMediaId?: string;
  replacesDraftId?: string;
  metadataSaved?: boolean;
  metadata: { requirementKey: string; description: string; source: "camera" | "gallery"; capturedAt: string; gps?: CaptureGPS; deviceContext?: Record<string, unknown> };
};
export type CaptureAnswer = { requirementKey: string; mediaIds: string[] };

const databaseName = "inspection-capture-v3";
const storeName = "drafts";
const quarantineStore = "quarantine";
const databaseVersion = 1;
type StoredCaptureDraft = Omit<CaptureDraft, "blob"> & { blob: Blob | ArrayBuffer; blobType?: string };
const isArrayBuffer = (value: unknown): value is ArrayBuffer => Object.prototype.toString.call(value) === "[object ArrayBuffer]";
const isBlob = (value: unknown): value is Blob => Object.prototype.toString.call(value) === "[object Blob]";

function open(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(databaseName, databaseVersion);
    request.onupgradeneeded = () => { const db = request.result; const drafts = db.createObjectStore(storeName, { keyPath: "id" }); drafts.createIndex("responsibilityId", "responsibilityId", { unique: false }); db.createObjectStore(quarantineStore, { autoIncrement: true }); };
    request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error);
  });
}

const isNonEmptyString = (value: unknown): value is string => typeof value === "string" && value.length > 0;
const isRecord = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);

function valid(draft: unknown): draft is StoredCaptureDraft {
  if (!isRecord(draft)) return false;
  const value = draft as Partial<StoredCaptureDraft>;
  const storedBlob = isBlob(value.blob) || isArrayBuffer(value.blob);
  if (value.schemaVersion !== 1 || !isNonEmptyString(value.id) || !isNonEmptyString(value.responsibilityId) || !storedBlob || !isNonEmptyString(value.sha256)) return false;
  if (isArrayBuffer(value.blob) && typeof value.blobType !== "string") return false;
  if (value.sourceSha256 !== undefined && !/^[a-f\d]{64}$/i.test(value.sourceSha256)) return false;
  if (value.imageProfile !== undefined && (!isNonEmptyString(value.imageProfile) || value.imageProfile.length > 80)) return false;
  if (!Array.isArray(value.parts)) return false;
  const preUpload = value.parts.length === 0 && value.uploadId === undefined && value.mediaId === undefined && value.metadataSaved === false;
  if (value.parts.length === 0 && !preUpload) return false;
  if (value.expiresAt !== undefined && !isNonEmptyString(value.expiresAt)) return false;
  if (value.replacesMediaId !== undefined && !isNonEmptyString(value.replacesMediaId)) return false;
  if (value.replacesDraftId !== undefined && !isNonEmptyString(value.replacesDraftId)) return false;
  if (value.partSizeBytes !== undefined && (!Number.isInteger(value.partSizeBytes) || value.partSizeBytes < 1)) return false;
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

function restoreBlob(draft: StoredCaptureDraft): CaptureDraft {
  if (isArrayBuffer(draft.blob)) {
    const blobType = draft.blobType ?? "";
    return { ...draft, blob: new Blob([draft.blob], { type: blobType }) };
  }
  return { ...draft, blob: draft.blob as Blob };
}

function readBlob(blob: Blob): Promise<ArrayBuffer> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => reader.result instanceof ArrayBuffer ? resolve(reader.result) : reject(new Error("Não foi possível ler a foto salva."));
    reader.onerror = () => reject(reader.error ?? new Error("Não foi possível ler a foto salva."));
    reader.readAsArrayBuffer(blob);
  });
}

async function transact<T>(store: string, mode: IDBTransactionMode, action: (objectStore: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const db = await open();
  try { return await new Promise<T>((resolve, reject) => {
    const transaction = db.transaction(store, mode);
    const request = action(transaction.objectStore(store));
    request.onerror = () => reject(request.error);
    transaction.oncomplete = () => resolve(request.result);
    transaction.onerror = () => reject(transaction.error ?? request.error);
    transaction.onabort = () => reject(transaction.error ?? request.error);
  }); } finally { db.close(); }
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
  if (!await hasDraftCapacity(draft.blob.size)) throw new DOMException("O armazenamento deste dispositivo está quase cheio. Libere espaço antes de salvar outra foto.", "QuotaExceededError");
  const { blob, ...metadata } = draft;
  const storedBytes = await readBlob(blob);
  await transact(storeName, "readwrite", (store) => store.put({ ...metadata, blob: storedBytes, blobType: blob.type, schemaVersion: 1 }));
}

export async function persistDraftMediaStatus(draft: CaptureDraft, mediaStatus: string): Promise<CaptureDraft> {
  const updated = { ...draft, mediaStatus };
  const db = await open();
  try {
    await new Promise<void>((resolve, reject) => {
      const transaction = db.transaction(storeName, "readwrite");
      const store = transaction.objectStore(storeName);
      const request = store.get(draft.id);
      request.onsuccess = () => {
        if (!valid(request.result)) {
          transaction.abort();
          return;
        }
        store.put({ ...request.result, mediaStatus });
      };
      transaction.oncomplete = () => resolve();
      transaction.onerror = () => reject(transaction.error ?? request.error);
      transaction.onabort = () => reject(transaction.error ?? request.error ?? new Error("Não foi possível atualizar o status da foto salva."));
    });
  } finally { db.close(); }
  return updated;
}

export async function loadDraft(id: string): Promise<CaptureDraft | undefined> {
  const value = await transact<unknown>(storeName, "readonly", (store) => store.get(id));
  if (valid(value)) return restoreBlob(value);
  if (value !== undefined) await quarantine(value);
  return undefined;
}

export async function loadDraftsForResponsibility(responsibilityId: string): Promise<CaptureDraft[]> {
  const db = await open();
  try {
    const values = await new Promise<unknown[]>((resolve, reject) => { const request = db.transaction(storeName, "readonly").objectStore(storeName).index("responsibilityId").getAll(responsibilityId); request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error); });
    const validDrafts: CaptureDraft[] = [];
    for (const value of values) { if (valid(value)) validDrafts.push(restoreBlob(value)); else await quarantine(value); }
    return validDrafts;
  } finally { db.close(); }
}

export const removeDraft = (id: string): Promise<unknown> => transact(storeName, "readwrite", (store) => store.delete(id));
export async function removeDraftsForResponsibility(responsibilityId: string): Promise<void> { await Promise.all((await loadDraftsForResponsibility(responsibilityId)).map((draft) => removeDraft(draft.id))); }
export async function digest(file: Blob): Promise<string> { return Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", await file.arrayBuffer()))).map((byte) => byte.toString(16).padStart(2, "0")).join(""); }
export const isReadyCaptureDraft = (draft: CaptureDraft): boolean => draft.mediaStatus === "READY" && Boolean(draft.mediaId) && draft.metadataSaved === true && draft.parts.length > 0 && draft.parts.every((part) => part.complete);
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
export const hasDuplicateDraft = (requirementKey: string, sha256: string, drafts: CaptureDraft[]): boolean => drafts.some((draft) =>
  draft.metadata.requirementKey === requirementKey && (draft.sha256 === sha256 || draft.sourceSha256 === sha256) && !["ABORTED", "PURGED"].includes(draft.mediaStatus ?? "")
);
export const readyForSubmission = (drafts: CaptureDraft[], online: boolean, allRequirementsSatisfied?: boolean, confirmIncomplete = false): boolean => {
  if (!online) return false;
  const mediaReady = drafts.length > 0 && drafts.every(isReadyCaptureDraft);
  if (allRequirementsSatisfied === undefined) return mediaReady;
  return (confirmIncomplete || allRequirementsSatisfied) && (drafts.length === 0 || mediaReady);
};
