export type PendingPart = { number: number; etag?: string; complete: boolean };
export type CaptureDraft = {
  schemaVersion?: 1;
  id: string;
  responsibilityId?: string;
  blob: Blob;
  sha256: string;
  uploadId?: string;
  mediaId?: string;
  parts: PendingPart[];
  metadata: { requirementKey: string; description: string; source: "camera" | "gallery"; capturedAt: string; gps?: { latitude: number; longitude: number; accuracyMeters: number; capturedAt: string; windowStartedAt: string } };
  answer?: { impossibilityReason?: string; progress: number };
};

const databaseName = "inspection-capture";
const storeName = "drafts";
const databaseVersion = 2;

function open(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(databaseName, databaseVersion);
    request.onupgradeneeded = () => {
      const db = request.result;
      const store = db.objectStoreNames.contains(storeName) ? request.transaction?.objectStore(storeName) : db.createObjectStore(storeName, { keyPath: "id" });
      if (store && !store.indexNames.contains("responsibilityId")) store.createIndex("responsibilityId", "responsibilityId", { unique: false });
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

export async function saveDraft(draft: CaptureDraft): Promise<void> {
  const db = await open();
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(storeName, "readwrite");
    tx.objectStore(storeName).put({ ...draft, schemaVersion: 1 }); tx.oncomplete = () => resolve(); tx.onerror = () => reject(tx.error);
  });
  db.close();
}

export async function loadDrafts(): Promise<CaptureDraft[]> {
  const db = await open();
  const drafts = await new Promise<CaptureDraft[]>((resolve, reject) => {
    const request = db.transaction(storeName, "readonly").objectStore(storeName).getAll();
    request.onsuccess = () => resolve(request.result as CaptureDraft[]); request.onerror = () => reject(request.error);
  });
  db.close(); return drafts;
}

export async function loadDraftsForResponsibility(responsibilityId: string): Promise<CaptureDraft[]> {
  const db = await open();
  const drafts = await new Promise<CaptureDraft[]>((resolve, reject) => {
    const request = db.transaction(storeName, "readonly").objectStore(storeName).index("responsibilityId").getAll(responsibilityId);
    request.onsuccess = () => resolve(request.result as CaptureDraft[]); request.onerror = () => reject(request.error);
  });
  db.close(); return drafts;
}

export async function removeDraft(id: string): Promise<void> {
  const db = await open();
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(storeName, "readwrite"); tx.objectStore(storeName).delete(id); tx.oncomplete = () => resolve(); tx.onerror = () => reject(tx.error);
  });
  db.close();
}

export async function digest(file: Blob): Promise<string> {
  const bytes = await file.arrayBuffer();
  return Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", bytes))).map((value) => value.toString(16).padStart(2, "0")).join("");
}

export function readyForSubmission(drafts: CaptureDraft[], online: boolean): boolean {
  return online && drafts.length > 0 && drafts.every((draft) => draft.mediaId && draft.parts.length > 0 && draft.parts.every((part) => part.complete));
}
