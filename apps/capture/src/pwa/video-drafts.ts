export type VideoDraftState = "INCOMPLETE" | "UNSAVED" | "SAVED" | "CONFIRMED" | "RECEIVED";
export type VideoPart = { number: number; etag?: string };
export type VideoDraft = {
  id: string;
  responsibilityId: string;
  kind: string;
  source: "CAMERA" | "FILE";
  contentType: string;
  maxDurationSeconds: number;
  maxBytes: number;
  expiresAt: string;
  state: VideoDraftState;
  chunkCount: number;
  sizeBytes: number;
  sha256?: string;
  mediaId?: string;
  replacesMediaId?: string;
  uploadId?: string;
  partSizeBytes?: number;
  parts: VideoPart[];
  metadataSaved?: boolean;
  preparationStatus?: string;
};

export const videoDatabaseName = "inspection-video-capture-v1";
const draftsStore = "videoDrafts";
const chunksStore = "videoChunks";

export function openVideoDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(videoDatabaseName, 1);
    request.onupgradeneeded = () => {
      const db = request.result;
      db.createObjectStore(draftsStore, { keyPath: "id" }).createIndex("responsibilityId", "responsibilityId");
      db.createObjectStore(chunksStore, { keyPath: ["attemptId", "sequence"] });
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

function transactionDone(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve();
    transaction.onabort = () => reject(transaction.error ?? new Error("O armazenamento local foi interrompido."));
    transaction.onerror = () => reject(transaction.error ?? new Error("Não foi possível salvar o vídeo."));
  });
}

export async function saveVideoDraft(draft: VideoDraft): Promise<void> {
  const db = await openVideoDatabase();
  try {
    const tx = db.transaction(draftsStore, "readwrite");
    tx.objectStore(draftsStore).put(draft);
    await transactionDone(tx);
  } finally { db.close(); }
}

export async function loadVideoDraft(id: string): Promise<VideoDraft | undefined> {
  const db = await openVideoDatabase();
  try {
    return await new Promise((resolve, reject) => {
      const request = db.transaction(draftsStore).objectStore(draftsStore).get(id);
      request.onsuccess = () => resolve(request.result as VideoDraft | undefined);
      request.onerror = () => reject(request.error);
    });
  } finally { db.close(); }
}

export async function listVideoDrafts(responsibilityId: string): Promise<VideoDraft[]> {
  const db = await openVideoDatabase();
  try {
    return await new Promise((resolve, reject) => {
      const request = db.transaction(draftsStore).objectStore(draftsStore).index("responsibilityId").getAll(responsibilityId);
      request.onsuccess = () => resolve(request.result as VideoDraft[]);
      request.onerror = () => reject(request.error);
    });
  } finally { db.close(); }
}

export async function appendVideoChunk(attemptId: string, sequence: number, blob: Blob): Promise<void> {
  if (!Number.isSafeInteger(sequence) || sequence < 0 || blob.size === 0) throw new Error("Parte de vídeo inválida.");
  const data = await blob.arrayBuffer();
  const db = await openVideoDatabase();
  try {
    const tx = db.transaction(chunksStore, "readwrite");
    tx.objectStore(chunksStore).add({ attemptId, sequence, data, type: blob.type });
    await transactionDone(tx);
  } finally { db.close(); }
}

export async function readVideoChunk(attemptId: string, sequence: number): Promise<Blob | undefined> {
  const db = await openVideoDatabase();
  try {
    return await new Promise((resolve, reject) => {
      const request = db.transaction(chunksStore).objectStore(chunksStore).get([attemptId, sequence]);
      request.onsuccess = () => {
        const record = request.result as { blob?: Blob; data?: ArrayBuffer; type?: string } | undefined;
        if (!record) { resolve(undefined); return; }
        if (record.blob !== undefined) { resolve(record.blob); return; }
        if (Object.prototype.toString.call(record.data) === "[object ArrayBuffer]") {
          resolve(new Blob([record.data as ArrayBuffer], { type: record.type ?? "" }));
          return;
        }
        resolve(undefined);
      };
      request.onerror = () => reject(request.error);
    });
  } finally { db.close(); }
}

export async function videoChunks(attemptId: string, count: number): Promise<Blob[]> {
  const blobs: Blob[] = [];
  for (let sequence = 0; sequence < count; sequence += 1) {
    const blob = await readVideoChunk(attemptId, sequence);
    if (!blob) throw new Error(`Parte ${sequence} ausente; esta gravação está incompleta.`);
    blobs.push(blob);
  }
  return blobs;
}

export async function finalizeVideoDraft(draft: VideoDraft, digest: (attemptId: string, chunkCount: number) => Promise<string>): Promise<VideoDraft> {
  if (draft.chunkCount < 1 || draft.sizeBytes < 1) throw new Error("A gravação não contém vídeo utilizável.");
  const blobs = await videoChunks(draft.id, draft.chunkCount);
  if (blobs.reduce((sum, blob) => sum + blob.size, 0) !== draft.sizeBytes) throw new Error("As partes do vídeo estão incompletas.");
  await saveVideoDraft({ ...draft, state: "UNSAVED" });
  const sha256 = await digest(draft.id, draft.chunkCount);
  const finalized: VideoDraft = { ...draft, sha256, state: "SAVED" };
  await saveVideoDraft(finalized);
  return finalized;
}

export async function discardVideoDraft(draft: VideoDraft): Promise<void> {
  const db = await openVideoDatabase();
  try {
    const tx = db.transaction([draftsStore, chunksStore], "readwrite");
    tx.objectStore(draftsStore).delete(draft.id);
    const request = tx.objectStore(chunksStore).openCursor(IDBKeyRange.bound([draft.id, 0], [draft.id, Number.MAX_SAFE_INTEGER]));
    request.onsuccess = () => { const cursor = request.result; if (cursor) { cursor.delete(); cursor.continue(); } };
    await transactionDone(tx);
  } finally { db.close(); }
}
