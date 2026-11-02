/** Where a note is anchored. A photo note keeps its local photo draft ID until the photo has a server media ID. */
export type AudioTarget = { kind: "PHOTO"; photoDraftId?: string; mediaId?: string } | { kind: "VIDEO"; attemptId: string; startMs: number; endMs: number; timeBasis: "CAPTURE_CLOCK" };

/** A locally retained, deliberately recorded voice note. */
export type AudioDraft = {
  id: string;
  responsibilityId: string;
  requirementKey: string;
  disclosureVersion: string;
  consentVersion: string;
  target: AudioTarget;
  durationMs: number;
  contentType: string;
  sizeBytes: number;
  sha256: string;
  state: "PENDING_UPLOAD" | "RECEIVED" | "FAILED" | "UNSENDABLE";
  audio?: Blob;
  noteId?: string;
  statusMessage?: string;
  /** Stable server rejection code that made the note unsendable. */
  failureCode?: string;
  createdAt: string;
};

const databaseName = "inspection-audio-notes-v1";
const storeName = "audioDrafts";
const channelName = "inspection.audio-notes";
/** Window event dispatched after any local note change so open lists can reload. */
export const audioDraftsChangedEvent = "inspection-audio-drafts-changed";

const channel = typeof window !== "undefined" && typeof BroadcastChannel !== "undefined" ? new BroadcastChannel(channelName) : undefined;
channel?.addEventListener("message", () => { if (typeof window !== "undefined") window.dispatchEvent(new Event(audioDraftsChangedEvent)); });

function notifyChanged(): void {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new Event(audioDraftsChangedEvent));
  try { channel?.postMessage("changed"); } catch { /* another tab will reload on its next poll */ }
}

function transactionDone(tx: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onabort = () => reject(tx.error ?? new Error("O armazenamento local foi interrompido."));
    tx.onerror = () => reject(tx.error ?? new Error("Não foi possível salvar a anotação neste dispositivo."));
  });
}

function openDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(databaseName, 1);
    request.onupgradeneeded = () => request.result.createObjectStore(storeName, { keyPath: "id" }).createIndex("responsibilityId", "responsibilityId");
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error("Não foi possível abrir as anotações salvas neste dispositivo."));
  });
}

async function blobBytes(blob: Blob): Promise<ArrayBuffer> {
  if (typeof blob.arrayBuffer === "function") return blob.arrayBuffer();
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as ArrayBuffer);
    reader.onerror = () => reject(reader.error ?? new Error("Não foi possível ler o áudio salvo."));
    reader.readAsArrayBuffer(blob);
  });
}

function validDraft(value: unknown): value is AudioDraft {
  if (!value || typeof value !== "object") return false;
  const draft = value as AudioDraft;
  const anchor = draft.target?.kind === "PHOTO"
    ? Boolean(draft.target.mediaId || draft.target.photoDraftId)
    : draft.target?.kind === "VIDEO" && Number.isFinite(draft.target.startMs) && Number.isFinite(draft.target.endMs) && draft.target.startMs >= 0 && draft.target.endMs > draft.target.startMs && draft.target.timeBasis === "CAPTURE_CLOCK";
  return Boolean(draft.id && draft.responsibilityId && draft.requirementKey && draft.disclosureVersion && draft.consentVersion && anchor && Number.isFinite(draft.durationMs) && draft.durationMs >= 500 && draft.durationMs <= 60_000 && draft.sizeBytes > 0 && draft.sizeBytes <= 5 * 1024 * 1024 && draft.sha256 && ["PENDING_UPLOAD", "RECEIVED", "FAILED", "UNSENDABLE"].includes(draft.state));
}

/** Saves audio bytes and their original capture anchor under the invitation responsibility. */
export async function saveAudioDraft(draft: AudioDraft): Promise<void> {
  if (!validDraft(draft) || !draft.audio || draft.audio.size !== draft.sizeBytes) throw new Error("A anotação não pôde ser salva com segurança neste dispositivo.");
  const bytes = await blobBytes(draft.audio);
  const db = await openDatabase();
  try {
    const tx = db.transaction(storeName, "readwrite");
    tx.objectStore(storeName).put({ ...draft, audio: undefined, audioBytes: bytes, audioType: draft.audio.type });
    await transactionDone(tx);
  } finally { db.close(); }
  notifyChanged();
}

/** Reads one note only when it belongs to the supplied responsibility. */
export async function loadAudioDraft(id: string, responsibilityId: string): Promise<AudioDraft | undefined> {
  const db = await openDatabase();
  try {
    const record = await new Promise<Record<string, unknown> | undefined>((resolve, reject) => {
      const request = db.transaction(storeName).objectStore(storeName).get(id);
      request.onsuccess = () => resolve(request.result as Record<string, unknown> | undefined);
      request.onerror = () => reject(request.error);
    });
    if (!record || record.responsibilityId !== responsibilityId) return undefined;
    return restoreRecord(record);
  } finally { db.close(); }
}

/** Lists only notes scoped to the original capture responsibility. */
export async function listAudioDrafts(responsibilityId: string): Promise<AudioDraft[]> {
  const db = await openDatabase();
  try {
    const records = await new Promise<Array<Record<string, unknown>>>((resolve, reject) => {
      const request = db.transaction(storeName).objectStore(storeName).index("responsibilityId").getAll(responsibilityId);
      request.onsuccess = () => resolve(request.result as Array<Record<string, unknown>>);
      request.onerror = () => reject(request.error);
    });
    const drafts: AudioDraft[] = [];
    for (const record of records) {
      const draft = await loadRecord(record);
      if (draft) drafts.push(draft);
    }
    return drafts.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
  } finally { db.close(); }
}

async function loadRecord(record: Record<string, unknown>): Promise<AudioDraft | undefined> {
  return restoreRecord(record);
}

/** Rebuilds a draft; a note whose local bytes are missing or corrupt stays visible as unsendable instead of vanishing. */
function restoreRecord(record: Record<string, unknown>): AudioDraft | undefined {
  const bytes = record.audioBytes;
  const audio = Object.prototype.toString.call(bytes) === "[object ArrayBuffer]" ? new Blob([bytes as ArrayBuffer], { type: String(record.audioType ?? record.contentType ?? "") }) : undefined;
  const draft = { ...record, audio } as AudioDraft;
  if (!validDraft(draft)) return undefined;
  if (draft.state === "RECEIVED") return { ...draft, audio: undefined };
  if (draft.state === "UNSENDABLE") return draft;
  if (audio && audio.size === draft.sizeBytes) return draft;
  return { ...draft, audio: undefined, state: "UNSENDABLE", failureCode: draft.failureCode ?? "LOCAL_AUDIO_UNAVAILABLE", statusMessage: "O áudio salvo neste dispositivo está incompleto. Descarte esta anotação e grave novamente." };
}

/** Persists status changes without replacing the audio bytes; a durable receipt drops them. */
export async function updateAudioDraft(draft: AudioDraft): Promise<void> {
  const db = await openDatabase();
  try {
    const tx = db.transaction(storeName, "readwrite");
    const store = tx.objectStore(storeName);
    const request = store.get(draft.id);
    request.onsuccess = () => {
      const current = request.result as Record<string, unknown> | undefined;
      if (!current || current.responsibilityId !== draft.responsibilityId) return;
      const next: Record<string, unknown> = { ...current, ...draft, audio: undefined, audioType: current.audioType };
      if (draft.state === "RECEIVED") delete next.audioBytes; else next.audioBytes = current.audioBytes;
      store.put(next);
    };
    await transactionDone(tx);
  } finally { db.close(); }
  notifyChanged();
}

/** Removes bytes only after the server confirms durable receipt. */
export async function removeAudioBytes(id: string, responsibilityId: string): Promise<void> {
  const draft = await loadAudioDraft(id, responsibilityId);
  if (!draft) return;
  await updateAudioDraft({ ...draft, audio: undefined, state: "RECEIVED" });
}

/** Deletes one note and its bytes, only within its original responsibility. */
export async function deleteAudioDraft(id: string, responsibilityId: string): Promise<void> {
  const db = await openDatabase();
  try {
    const tx = db.transaction(storeName, "readwrite");
    const store = tx.objectStore(storeName);
    const request = store.get(id);
    request.onsuccess = () => {
      const current = request.result as Record<string, unknown> | undefined;
      if (current && current.responsibilityId === responsibilityId) store.delete(id);
    };
    await transactionDone(tx);
  } finally { db.close(); }
  notifyChanged();
}

/** Clears every local note once its responsibility is closed so no audio outlives the capture. */
export async function removeAudioDraftsForResponsibility(responsibilityId: string): Promise<void> {
  const db = await openDatabase();
  try {
    const tx = db.transaction(storeName, "readwrite");
    const store = tx.objectStore(storeName);
    const request = store.index("responsibilityId").getAllKeys(responsibilityId);
    request.onsuccess = () => { for (const key of request.result) store.delete(key); };
    await transactionDone(tx);
  } finally { db.close(); }
  notifyChanged();
}
