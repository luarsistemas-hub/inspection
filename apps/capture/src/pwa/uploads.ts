import { captureOperations, graphql } from "@/graphql/client";
import { loadDraft, type CaptureDraft, type PendingPart, saveDraft } from "@/pwa/drafts";
import { throwOnUserErrors } from "@/pwa/mutation-errors";

const defaultPartSize = 5 * 1024 * 1024;
const partsFor = (blob: Blob, partSize: number): PendingPart[] => Array.from({ length: Math.ceil(blob.size / partSize) }, (_, index) => ({ number: index + 1, complete: false }));
const wait = (milliseconds: number) => new Promise((resolve) => setTimeout(resolve, milliseconds));
// The worker verifies and screens the object asynchronously after completion.
// Camera captures tend to expose this race more often because the client starts
// saving metadata immediately after the multipart upload completes.
const metadataRetryAttempts = 30;
const metadataRetryDelay = 1000;
const metadataReadyStatuses = new Set(["READY", "SCREENED"]);
const lockDatabaseName = "inspection-capture-locks-v1";
const lockStoreName = "locks";
const lockLease = 30_000;

type LockRecord = { name: string; owner: string; expiresAt: number };

export type UploadProgressPhase = "preparing" | "uploading" | "verifying" | "saving" | "complete" | "error";
export type UploadProgress = {
  phase: UploadProgressPhase;
  percent: number;
  completedParts: number;
  totalParts: number;
};
export type UploadProgressCallback = (progress: UploadProgress) => void;
export type UploadDraftOptions = { wait?: (milliseconds: number) => Promise<unknown> };

const reportProgress = (onProgress: UploadProgressCallback | undefined, progress: UploadProgress): void => {
  onProgress?.(progress);
};

const errorCode = (error: unknown): string | undefined => {
  if (typeof error !== "object" || error === null || !("code" in error)) return undefined;
  const code = (error as { code?: unknown }).code;
  return typeof code === "string" ? code : undefined;
};

const errorMessage = (error: unknown): string => error instanceof Error ? error.message : typeof error === "object" && error !== null && "message" in error ? String((error as { message?: unknown }).message ?? "") : String(error);

const isRetryableMediaProcessingError = (error: unknown): boolean => {
  const message = errorMessage(error);
  const code = errorCode(error);
  const pendingMedia = /media verification and screening are pending|media processing pending/i.test(message);
  return (pendingMedia && (!code || code === "INVALID_STATE")) || /network|fetch/i.test(message);
};

function openLockDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(lockDatabaseName, 1);
    request.onupgradeneeded = () => request.result.createObjectStore(lockStoreName, { keyPath: "name" });
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

async function updateLock(record: LockRecord, replaceExpired: boolean): Promise<boolean> {
  const db = await openLockDatabase();
  try {
    return await new Promise<boolean>((resolve, reject) => {
      const transaction = db.transaction(lockStoreName, "readwrite");
      const store = transaction.objectStore(lockStoreName);
      const request = store.get(record.name);
      let acquired = false;
      request.onsuccess = () => {
        const current = request.result as LockRecord | undefined;
        if ((replaceExpired && (!current || current.expiresAt <= Date.now())) || (!replaceExpired && current?.owner === record.owner)) {
          store.put(record);
          acquired = true;
        }
      };
      request.onerror = () => reject(request.error);
      transaction.oncomplete = () => resolve(acquired);
      transaction.onerror = () => reject(transaction.error);
      transaction.onabort = () => reject(transaction.error);
    });
  } finally {
    db.close();
  }
}

async function releaseLock(name: string, owner: string): Promise<void> {
  const db = await openLockDatabase();
  try {
    await new Promise<void>((resolve, reject) => {
      const transaction = db.transaction(lockStoreName, "readwrite");
      const store = transaction.objectStore(lockStoreName);
      const request = store.get(name);
      request.onsuccess = () => { if ((request.result as LockRecord | undefined)?.owner === owner) store.delete(name); };
      request.onerror = () => reject(request.error);
      transaction.oncomplete = () => resolve();
      transaction.onerror = () => reject(transaction.error);
      transaction.onabort = () => reject(transaction.error);
    });
  } finally {
    db.close();
  }
}

async function withIndexedDBLock<T>(name: string, action: () => Promise<T>): Promise<T> {
  const owner = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  while (!await updateLock({ name, owner, expiresAt: Date.now() + lockLease }, true)) await wait(50);
  const heartbeat = setInterval(() => {
    void updateLock({ name, owner, expiresAt: Date.now() + lockLease }, false);
  }, lockLease / 3);
  try { return await action(); } finally { clearInterval(heartbeat); await releaseLock(name, owner); }
}

async function withDraftLock<T>(draftId: string, action: () => Promise<T>): Promise<T> {
  const name = `capture-upload:${draftId}`;
  if (navigator.locks) return navigator.locks.request(name, { mode: "exclusive" }, action);
  return withIndexedDBLock(name, action);
}

export function uploadDraft(draft: CaptureDraft, onProgress?: UploadProgressCallback, options: UploadDraftOptions = {}): Promise<CaptureDraft> {
  return withDraftLock(draft.id, async () => {
    const retryWait = options.wait ?? wait;
    let current = await loadDraft(draft.id) ?? draft;
    const totalParts = current.parts.length;
    reportProgress(onProgress, { phase: "preparing", percent: 5, completedParts: current.parts.filter((part) => part.complete).length, totalParts });
    if (!current.mediaId || !current.uploadId) {
      const created = await graphql(captureOperations.createUpload, { input: { contentType: draft.blob.type, sizeBytes: draft.blob.size, sha256: draft.sha256, clientMutationId: draft.id } });
      throwOnUserErrors(created.createMediaUpload);
      if (!created.createMediaUpload.upload) throw new Error("A API não retornou o upload de mídia.");
      current = { ...draft, ...created.createMediaUpload.upload, parts: partsFor(draft.blob, created.createMediaUpload.upload.partSizeBytes || defaultPartSize) };
      await saveDraft(current);
    }
    const mediaId = current.mediaId;
    if (!mediaId) throw new Error("A API não retornou a mídia de upload.");
    const currentTotalParts = current.parts.length;
    const pending = current.parts.filter((part) => !part.complete).map((part) => part.number);
    reportProgress(onProgress, { phase: "uploading", percent: 10 + (currentTotalParts > 0 ? (currentTotalParts - pending.length) / currentTotalParts * 60 : 60), completedParts: currentTotalParts - pending.length, totalParts: currentTotalParts });
    if (pending.length) {
      const signed = await graphql(captureOperations.parts, { input: { mediaId, partNumbers: pending, clientMutationId: `${draft.id}:parts` } });
      throwOnUserErrors(signed.presignMediaParts);
      const partSize = current.partSizeBytes || defaultPartSize;
      for (const [index, part] of signed.presignMediaParts.parts.entries()) {
        if (Date.parse(part.expiresAt) <= Date.now()) throw new Error("O acesso temporário desta parte expirou. Retome o envio para solicitar um novo acesso.");
        const response = await fetch(part.url, { method: "PUT", body: current.blob.slice((part.partNumber - 1) * partSize, part.partNumber * partSize), headers: { "Content-Type": current.blob.type } });
        const etag = response.headers.get("etag");
        if (!response.ok || !etag) throw new Error("Não foi possível enviar uma parte da foto.");
        current = { ...current, parts: current.parts.map((saved) => saved.number === part.partNumber ? { ...saved, complete: true, etag } : saved) };
        await saveDraft(current);
        reportProgress(onProgress, { phase: "uploading", percent: 10 + (current.parts.filter((saved) => saved.complete).length / currentTotalParts) * 60, completedParts: current.parts.filter((saved) => saved.complete).length, totalParts: currentTotalParts });
        if (index === signed.presignMediaParts.parts.length - 1) reportProgress(onProgress, { phase: "verifying", percent: 75, completedParts: currentTotalParts, totalParts: currentTotalParts });
      }
    }
    const completedParts = current.parts.map((part) => {
      if (!part.complete || !part.etag) throw new Error("O envio da foto não foi concluído.");
      return { partNumber: part.number, etag: part.etag };
    });
    const completed = await graphql(captureOperations.completeUpload, { input: { mediaId, parts: completedParts, clientMutationId: `${draft.id}:complete` } });
    throwOnUserErrors(completed.completeMediaUpload);
    current = { ...current, mediaStatus: completed.completeMediaUpload.media?.status ?? current.mediaStatus };
    await saveDraft(current);
    reportProgress(onProgress, { phase: "verifying", percent: 75, completedParts: currentTotalParts, totalParts: currentTotalParts });
    if (current.mediaStatus && !metadataReadyStatuses.has(current.mediaStatus)) await retryWait(metadataRetryDelay);
    let lastError: unknown;
    for (let attempt = 0; attempt < metadataRetryAttempts; attempt++) {
      try {
        reportProgress(onProgress, { phase: "saving", percent: Math.min(95, 80 + attempt), completedParts: currentTotalParts, totalParts: currentTotalParts });
        const metadata = await graphql(captureOperations.metadata, { input: { mediaId, requirementKey: current.metadata.requirementKey, description: current.metadata.description, captureSource: current.metadata.source.toUpperCase(), gps: current.metadata.gps, deviceContext: current.metadata.deviceContext, clientMutationId: `${draft.id}:metadata` } });
        throwOnUserErrors(metadata.saveCaptureMetadata);
        current = { ...current, metadataSaved: true, mediaStatus: metadata.saveCaptureMetadata.media?.status ?? current.mediaStatus };
        await saveDraft(current);
        reportProgress(onProgress, { phase: "complete", percent: 100, completedParts: currentTotalParts, totalParts: currentTotalParts });
        return current;
      } catch (error) {
        lastError = error;
        if (attempt === metadataRetryAttempts - 1 || !isRetryableMediaProcessingError(error)) {
          reportProgress(onProgress, { phase: "error", percent: Math.min(95, 80 + attempt), completedParts: currentTotalParts, totalParts: currentTotalParts });
          break;
        }
        reportProgress(onProgress, { phase: "verifying", percent: Math.min(94, 80 + attempt), completedParts: currentTotalParts, totalParts: currentTotalParts });
        await retryWait(metadataRetryDelay);
      }
    }
    if (lastError !== undefined) throw lastError;
    throw new Error("A verificação da foto ainda está em processamento. Tente retomar o envio.");
  });
}
