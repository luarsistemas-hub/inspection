/** Internal type. DO NOT USE DIRECTLY. */
type Exact<T extends { [key: string]: unknown }> = { [K in keyof T]: T[K] };
/** Internal type. DO NOT USE DIRECTLY. */
export type Incremental<T> = T | { [P in keyof T]?: P extends ' $fragmentName' | '__typename' ? T[P] : never };
import type * as Schema from "./schema";
export type AcceptProcessingInput = {
  aiAnalysis: boolean;
  clientMutationId: string;
  disclosureVersion: string;
  gpsUse: boolean;
  photoProcessing: boolean;
};

export type CaptureGpsInput = {
  accuracyMeters: number;
  capturedAt: string;
  latitude: number;
  longitude: number;
  windowStartedAt: string;
};

export type CompleteMediaUploadInput = {
  clientMutationId: string;
  mediaId: string | number;
  parts: Array<CompletedPartInput>;
};

export type CompletedPartInput = {
  etag: string;
  partNumber: number;
};

export type CreateMediaUploadInput = {
  clientMutationId: string;
  contentType: string;
  sha256: string;
  sizeBytes: number;
};

export type DeclareCaptureImpossibilityInput = {
  clientMutationId: string;
  expectedVersion: number | null | undefined;
  reason: string;
  requirementKey: string;
};

export type PresignMediaPartsInput = {
  clientMutationId: string;
  mediaId: string | number;
  partNumbers: Array<number>;
};

export type RequestInvitationOtpInput = {
  clientMutationId: string;
  linkToken: string;
};

export type SaveCaptureMetadataInput = {
  captureSource: string;
  clientMutationId: string;
  description: string;
  deviceContext: Record<string, unknown> | null | undefined;
  gps: CaptureGpsInput | null | undefined;
  mediaId: string | number;
  requirementKey: string;
};

export type SubmitCaptureInput = {
  clientMutationId: string;
  confirmIncomplete: boolean;
};

export type SubmitRecaptureInput = {
  clientMutationId: string;
  confirmIncomplete: boolean;
  requestId: string | number;
};

export type VerifyInvitationOtpInput = {
  clientMutationId: string;
  code: string;
  linkToken: string;
};

export type ExternalCaptureBootstrapQueryVariables = Exact<{ [key: string]: never; }>;


export type ExternalCaptureBootstrapQuery = { externalCapture: { responsibilityId: string, recaptureRequestId: string | null, status: string, confirmationOnly: boolean, kind: string, disclosureVersion: string, reference: Record<string, unknown>, policy: Record<string, unknown>, requirements: Array<{ key: string, section: string, label: string, instructions: string | null, required: boolean, minimumMedia: number, maximumMedia: number, descriptionRequired: boolean, impossibilityAllowed: boolean }>, answers: Array<{ requirementKey: string, mediaIds: Array<string>, impossibilityReason: string | null, version: number }> } };

export type RequestInvitationOtpMutationVariables = Exact<{
  input: Schema.RequestInvitationOtpInput;
}>;


export type RequestInvitationOtpMutation = { requestInvitationOtp: { status: string, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type VerifyInvitationOtpMutationVariables = Exact<{
  input: Schema.VerifyInvitationOtpInput;
}>;


export type VerifyInvitationOtpMutation = { verifyInvitationOtp: { status: string, csrfToken: string, expiresAt: string, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type AcceptProcessingMutationVariables = Exact<{
  input: Schema.AcceptProcessingInput;
}>;


export type AcceptProcessingMutation = { acceptProcessing: { status: string, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type CreateMediaUploadMutationVariables = Exact<{
  input: Schema.CreateMediaUploadInput;
}>;


export type CreateMediaUploadMutation = { createMediaUpload: { clientMutationId: string, upload: { mediaId: string, uploadId: string, expiresAt: string, partSizeBytes: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type PresignMediaPartsMutationVariables = Exact<{
  input: Schema.PresignMediaPartsInput;
}>;


export type PresignMediaPartsMutation = { presignMediaParts: { clientMutationId: string, parts: Array<{ partNumber: number, url: string, expiresAt: string }>, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type CompleteMediaUploadMutationVariables = Exact<{
  input: Schema.CompleteMediaUploadInput;
}>;


export type CompleteMediaUploadMutation = { completeMediaUpload: { clientMutationId: string, media: { id: string, status: string } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type SaveCaptureMetadataMutationVariables = Exact<{
  input: Schema.SaveCaptureMetadataInput;
}>;


export type SaveCaptureMetadataMutation = { saveCaptureMetadata: { clientMutationId: string, media: { id: string, status: string } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type DeclareCaptureImpossibilityMutationVariables = Exact<{
  input: Schema.DeclareCaptureImpossibilityInput;
}>;


export type DeclareCaptureImpossibilityMutation = { declareCaptureImpossibility: { status: string, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type SubmitCaptureMutationVariables = Exact<{
  input: Schema.SubmitCaptureInput;
}>;


export type SubmitCaptureMutation = { submitCapture: { clientMutationId: string, submission: { id: string, complete: boolean, requiresAttention: boolean, submittedAt: string } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type SubmitRecaptureMutationVariables = Exact<{
  input: Schema.SubmitRecaptureInput;
}>;


export type SubmitRecaptureMutation = { submitRecapture: { clientMutationId: string, recapture: { id: string, status: string } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };
