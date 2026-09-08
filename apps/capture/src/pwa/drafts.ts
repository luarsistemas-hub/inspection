export type PendingPart = { number: number; etag?: string; complete: boolean };
export type CaptureDraft = {
  schemaVersion?: 1; id: string; responsibilityId: string; blob: Blob; sha256: string;
  uploadId?: string; mediaId?: string; parts: PendingPart[];
  metadata: { requirementKey: string; description: string; source: "camera" | "gallery"; capturedAt: string };
};

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

function valid(draft: unknown): draft is CaptureDraft {
  if (!draft || typeof draft !== "object") return false;
  const value = draft as Partial<CaptureDraft>;
  return value.schemaVersion === 1 && typeof value.id === "string" && typeof value.responsibilityId === "string" && !!value.blob && Array.isArray(value.parts) && typeof value.sha256 === "string" && !!value.metadata;
}

async function transact<T>(store: string, mode: IDBTransactionMode, action: (objectStore: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const db = await open();
  try { return await new Promise<T>((resolve, reject) => { const request = action(db.transaction(store, mode).objectStore(store)); request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error); }); } finally { db.close(); }
}

export async function hasDraftCapacity(bytes: number): Promise<boolean> {
  const estimate = await navigator.storage?.estimate?.();
  return !estimate?.quota || !estimate.usage || estimate.usage + bytes < estimate.quota * 0.9;
}

export async function saveDraft(draft: CaptureDraft): Promise<void> {
  if (!await hasDraftCapacity(draft.blob.size)) throw new Error("O armazenamento deste dispositivo está quase cheio. Libere espaço antes de salvar outra foto.");
  await transact(storeName, "readwrite", (store) => store.put({ ...draft, schemaVersion: 1 }));
}

export async function loadDraftsForResponsibility(responsibilityId: string): Promise<CaptureDraft[]> {
  const db = await open();
  try {
    const values = await new Promise<unknown[]>((resolve, reject) => { const request = db.transaction(storeName, "readonly").objectStore(storeName).index("responsibilityId").getAll(responsibilityId); request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error); });
    const validDrafts: CaptureDraft[] = [];
    for (const value of values) { if (valid(value)) validDrafts.push(value); else { await transact(quarantineStore, "readwrite", (store) => store.add({ value, quarantinedAt: new Date().toISOString() })); } }
    return validDrafts;
  } finally { db.close(); }
}

export const removeDraft = (id: string): Promise<unknown> => transact(storeName, "readwrite", (store) => store.delete(id));
export async function removeDraftsForResponsibility(responsibilityId: string): Promise<void> { await Promise.all((await loadDraftsForResponsibility(responsibilityId)).map((draft) => removeDraft(draft.id))); }
export async function digest(file: Blob): Promise<string> { return Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", await file.arrayBuffer()))).map((byte) => byte.toString(16).padStart(2, "0")).join(""); }
export const readyForSubmission = (drafts: CaptureDraft[], online: boolean): boolean => online && drafts.length > 0 && drafts.every((draft) => !!draft.mediaId && draft.parts.length > 0 && draft.parts.every((part) => part.complete));
