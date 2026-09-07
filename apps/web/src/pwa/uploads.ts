import { captureOperations, graphql } from "@/graphql/client";
import { CaptureDraft, PendingPart, saveDraft } from "@/pwa/drafts";

const partSize = 5 * 1024 * 1024;

type UserError = { code: string; message: string };
type GPS = { latitude: number; longitude: number; accuracyMeters: number; capturedAt: string; windowStartedAt: string };

function failOnUserErrors(value: { userErrors: UserError[] }): void {
  const failure = value.userErrors[0];
  if (failure) throw new Error(failure.message);
}

function numbersFor(file: Blob): number[] {
  return Array.from({ length: Math.ceil(file.size / partSize) }, (_, index) => index + 1);
}

export async function uploadDraft(draft: CaptureDraft, gps?: GPS): Promise<CaptureDraft> {
  let resumable = draft;
  if (!resumable.mediaId || !resumable.uploadId) {
    const create = await graphql<{ createMediaUpload: { upload?: { mediaId: string; uploadId: string }; userErrors: UserError[] } }>(
      "capture", captureOperations.createUpload, { contentType: draft.blob.type, size: draft.blob.size, hash: draft.sha256, id: draft.id }
    );
    failOnUserErrors(create.createMediaUpload);
    const upload = create.createMediaUpload.upload;
    if (!upload) throw new Error("A API não retornou o upload de mídia.");
    resumable = { ...draft, mediaId: upload.mediaId, uploadId: upload.uploadId, parts: numbersFor(draft.blob).map((number) => ({ number, complete: false })) };
    await saveDraft(resumable);
  }
  const mediaId = resumable.mediaId;
  if (!mediaId) throw new Error("Mídia pendente não possui identificador.");
  const pending = resumable.parts.filter((part) => !part.complete).map((part) => part.number);
  let signedParts: Array<{ partNumber: number; url: string }> = [];
  if (pending.length > 0) {
    const signed = await graphql<{ presignMediaParts: { parts: Array<{ partNumber: number; url: string }>; userErrors: UserError[] } }>(
      "capture", captureOperations.parts, { mediaId, parts: pending, id: `${draft.id}:presign` }
    );
    failOnUserErrors(signed.presignMediaParts);
    signedParts = signed.presignMediaParts.parts;
  }

  for (const part of signedParts) {
    const body = draft.blob.slice((part.partNumber - 1) * partSize, part.partNumber * partSize);
    const response = await fetch(part.url, { method: "PUT", body, headers: { "Content-Type": draft.blob.type } });
    if (!response.ok) throw new Error("Não foi possível enviar uma parte da foto.");
    const etag = response.headers.get("etag");
    if (!etag) throw new Error("O armazenamento não confirmou a parte enviada.");
    const parts: PendingPart[] = resumable.parts.map((saved) => saved.number === part.partNumber ? { ...saved, etag, complete: true } : saved);
    resumable = { ...resumable, parts };
    await saveDraft(resumable);
  }

  const complete = await graphql<{ completeMediaUpload: { media?: { id: string }; userErrors: UserError[] } }>(
    "capture", captureOperations.completeUpload, { mediaId, parts: resumable.parts.map((part) => ({ partNumber: part.number, etag: part.etag })), id: `${draft.id}:complete` }
  );
  failOnUserErrors(complete.completeMediaUpload);
  const metadataVariables = {
    mediaId, key: draft.metadata.requirementKey, description: draft.metadata.description, source: draft.metadata.source === "camera" ? "CAMERA" : "GALLERY",
    gps: gps ?? draft.metadata.gps, deviceContext: { userAgent: navigator.userAgent, language: navigator.language }, id: `${draft.id}:metadata`
  };
  for (let attempt = 0; ; attempt += 1) {
    try {
      const metadata = await graphql<{ saveCaptureMetadata: { userErrors: UserError[] } }>("capture", captureOperations.metadata, metadataVariables);
      failOnUserErrors(metadata.saveCaptureMetadata);
      break;
    } catch (error) {
      const message = error instanceof Error ? error.message : typeof error === "object" && error && "message" in error ? String(error.message) : "";
      if (!message.includes("verification and screening are pending") || attempt >= 8) throw error;
      await new Promise((resolve) => setTimeout(resolve, Math.min(250 * 2 ** attempt, 2_000)));
    }
  }
  return resumable;
}
