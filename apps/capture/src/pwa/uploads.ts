import { captureOperations, graphql } from "@/graphql/client";
import { loadDraft, type CaptureDraft, type PendingPart, saveDraft } from "@/pwa/drafts";
import { throwOnUserErrors } from "@/pwa/mutation-errors";

const defaultPartSize = 5 * 1024 * 1024;
const partsFor = (blob: Blob, partSize: number): PendingPart[] => Array.from({ length: Math.ceil(blob.size / partSize) }, (_, index) => ({ number: index + 1, complete: false }));
const wait = (milliseconds: number) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const lockDatabaseName = "inspection-capture-locks-v1";
const lockStoreName = "locks";
const lockLease = 30_000;

type LockRecord = { name: string; owner: string; expiresAt: number };

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

export function uploadDraft(draft: CaptureDraft): Promise<CaptureDraft> {
  return withDraftLock(draft.id, async () => {
    let current = await loadDraft(draft.id) ?? draft;
    if (!current.mediaId || !current.uploadId) {
      const created = await graphql(captureOperations.createUpload, { input: { contentType: draft.blob.type, sizeBytes: draft.blob.size, sha256: draft.sha256, clientMutationId: draft.id } });
      throwOnUserErrors(created.createMediaUpload);
      if (!created.createMediaUpload.upload) throw new Error("A API não retornou o upload de mídia.");
      current = { ...draft, ...created.createMediaUpload.upload, parts: partsFor(draft.blob, created.createMediaUpload.upload.partSizeBytes || defaultPartSize) };
      await saveDraft(current);
    }
    const mediaId = current.mediaId;
    if (!mediaId) throw new Error("A API não retornou a mídia de upload.");
    const pending = current.parts.filter((part) => !part.complete).map((part) => part.number);
    if (pending.length) {
      const signed = await graphql(captureOperations.parts, { input: { mediaId, partNumbers: pending, clientMutationId: `${draft.id}:parts` } });
      throwOnUserErrors(signed.presignMediaParts);
      const partSize = current.partSizeBytes || defaultPartSize;
      for (const part of signed.presignMediaParts.parts) {
        if (Date.parse(part.expiresAt) <= Date.now()) throw new Error("O acesso temporário desta parte expirou. Retome o envio para solicitar um novo acesso.");
        const response = await fetch(part.url, { method: "PUT", body: current.blob.slice((part.partNumber - 1) * partSize, part.partNumber * partSize), headers: { "Content-Type": current.blob.type } });
        const etag = response.headers.get("etag");
        if (!response.ok || !etag) throw new Error("Não foi possível enviar uma parte da foto.");
        current = { ...current, parts: current.parts.map((saved) => saved.number === part.partNumber ? { ...saved, complete: true, etag } : saved) };
        await saveDraft(current);
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
    let lastError: unknown;
    for (let attempt = 0; attempt < 8; attempt++) {
      try {
        const metadata = await graphql(captureOperations.metadata, { input: { mediaId, requirementKey: current.metadata.requirementKey, description: current.metadata.description, captureSource: current.metadata.source.toUpperCase(), gps: current.metadata.gps, deviceContext: current.metadata.deviceContext, clientMutationId: `${draft.id}:metadata` } });
        throwOnUserErrors(metadata.saveCaptureMetadata);
        current = { ...current, metadataSaved: true, mediaStatus: metadata.saveCaptureMetadata.media?.status ?? current.mediaStatus };
        await saveDraft(current);
        return current;
      } catch (error) {
        lastError = error;
        const message = error instanceof Error ? error.message : String(error);
        if (attempt === 7 || !/pending|verification|screening|processing|network|fetch/i.test(message)) break;
        await wait(250 * (attempt + 1));
      }
    }
    throw lastError instanceof Error ? lastError : new Error("A verificação da foto ainda está em processamento. Tente retomar o envio.");
  });
}
