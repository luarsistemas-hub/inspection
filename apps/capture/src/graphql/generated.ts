/** Internal type. DO NOT USE DIRECTLY. */
type Exact<T extends { [key: string]: unknown }> = { [K in keyof T]: T[K] };
/** Internal type. DO NOT USE DIRECTLY. */
export type Incremental<T> = T | { [P in keyof T]?: P extends ' $fragmentName' | '__typename' ? T[P] : never };
import { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';
export type AcceptProcessingInput = {
  aiAnalysis: boolean;
  clientMutationId: string;
  disclosureVersion: string;
  gpsUse: boolean;
  photoProcessing: boolean;
  videoProcessing: boolean | null | undefined;
};

export type BeginVideoCaptureInput = {
  acquisitionSource: string;
  clientMutationId: string;
  replacesMediaId: string | number | null | undefined;
  requirementKey: string | null | undefined;
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

export type ContestBaselineInput = {
  clientMutationId: string;
  statement: string;
};

export type CounterpartyResponseInput = {
  clientMutationId: string;
};

export type CreateMediaUploadInput = {
  attemptId: string | number | null | undefined;
  clientMutationId: string;
  contentType: string;
  requirementKey: string | null | undefined;
  sha256: string;
  sizeBytes: number;
};

export type DeclareCaptureEnvironmentInput = {
  clientMutationId: string;
  name: string;
  typeCode: string;
};

export type DeclareCaptureImpossibilityInput = {
  clientMutationId: string;
  expectedVersion: number | null | undefined;
  reason: string | null | undefined;
  reasonCode: string | null | undefined;
  reasonText: string | null | undefined;
  requirementKey: string;
};

export type PresignMediaPartsInput = {
  clientMutationId: string;
  mediaId: string | number;
  partNumbers: Array<number>;
};

export type RecordCaptureQualityWarningInput = {
  clientMutationId: string;
  code: string;
  ignored: boolean;
  requirementKey: string;
};

export type RequestCounterpartyOtpInput = {
  clientMutationId: string;
  token: string;
};

export type RequestInvitationOtpInput = {
  clientMutationId: string;
  linkToken: string;
};

export type RevokeInvitationInput = {
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
  replacesMediaId: string | number | null | undefined;
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

export type VerifyCounterpartyOtpInput = {
  clientMutationId: string;
  code: string;
  token: string;
};

export type VerifyInvitationOtpInput = {
  clientMutationId: string;
  code: string;
  linkToken: string;
};

export type ExternalCaptureBootstrapQueryVariables = Exact<{ [key: string]: never; }>;


export type ExternalCaptureBootstrapQuery = { externalCapture: { responsibilityId: string, recaptureRequestId: string | null, recaptureDeadlineAt: string | null, replacementDeadlineAt: string | null, status: string, confirmationOnly: boolean, kind: string, mediaKind: string, disclosureVersion: string, captureLayout: string | null, reference: Record<string, unknown>, policy: Record<string, unknown>, environments: Array<{ requirementKey: string, identityId: string, typeCode: string, name: string, expected: boolean, addedDuringCapture: boolean, mediaCount: number, status: string }>, reviewHandoff: { inspectionId: string, originVersionId: string | null } | null, referenceItems: Array<{ mediaId: string, evidenceId: string | null, sourceMediaId: string | null, timestampMs: number | null, environment: string | null, component: string | null, view: string | null, ambiguous: boolean | null, gap: boolean | null, requirementKey: string, description: string, reason: string | null, availability: string, imageUrl: string | null, imageUrlExpiresAt: string | null }>, requirements: Array<{ key: string, section: string, label: string, instructions: string | null, required: boolean, minimumMedia: number, maximumMedia: number, descriptionRequired: boolean, captureSourcePolicy: string, comparisonTarget: string, impossibilityAllowed: boolean, environmentIdentityId: string | null, environmentTypeCode: string | null, environmentName: string | null, expected: boolean | null, addedDuringCapture: boolean | null }>, answers: Array<{ requirementKey: string, mediaIds: Array<string>, impossibilityReason: string | null, version: number }> } };

export type RequestInvitationOtpMutationVariables = Exact<{
  input: RequestInvitationOtpInput;
}>;


export type RequestInvitationOtpMutation = { requestInvitationOtp: { status: string, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type VerifyInvitationOtpMutationVariables = Exact<{
  input: VerifyInvitationOtpInput;
}>;


export type VerifyInvitationOtpMutation = { verifyInvitationOtp: { status: string, csrfToken: string, expiresAt: string, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type AcceptProcessingMutationVariables = Exact<{
  input: AcceptProcessingInput;
}>;


export type AcceptProcessingMutation = { acceptProcessing: { status: string, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type RevokeInvitationMutationVariables = Exact<{
  input: RevokeInvitationInput;
}>;


export type RevokeInvitationMutation = { revokeInvitation: { status: string, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type CreateMediaUploadMutationVariables = Exact<{
  input: CreateMediaUploadInput;
}>;


export type CreateMediaUploadMutation = { createMediaUpload: { clientMutationId: string, upload: { mediaId: string, uploadId: string, expiresAt: string, partSizeBytes: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type BeginVideoCaptureMutationVariables = Exact<{
  input: BeginVideoCaptureInput;
}>;


export type BeginVideoCaptureMutation = { beginVideoCapture: { clientMutationId: string, attempt: { id: string, kind: string, acquisitionSource: string, maxDurationSeconds: number, maxBytes: number, policyVersion: number, admittedAt: string, expiresAt: string, status: string } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type VideoUploadStatusQueryVariables = Exact<{
  mediaId: string | number;
}>;


export type VideoUploadStatusQuery = { videoUploadStatus: { mediaId: string, status: string, expiresAt: string, receivedParts: Array<number>, missingParts: Array<number>, confirmedParts: Array<{ partNumber: number, etag: string, sizeBytes: number }> } };

export type VideoPreparationQueryVariables = Exact<{
  mediaId: string | number;
}>;


export type VideoPreparationQuery = { videoPreparation: { mediaId: string, stage: string, errorCode: string, retryable: boolean, processedFrames: number, sourceAvailable: boolean } };

export type PresignMediaPartsMutationVariables = Exact<{
  input: PresignMediaPartsInput;
}>;


export type PresignMediaPartsMutation = { presignMediaParts: { clientMutationId: string, parts: Array<{ partNumber: number, url: string, expiresAt: string }>, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type CompleteMediaUploadMutationVariables = Exact<{
  input: CompleteMediaUploadInput;
}>;


export type CompleteMediaUploadMutation = { completeMediaUpload: { clientMutationId: string, media: { id: string, status: string } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type SaveCaptureMetadataMutationVariables = Exact<{
  input: SaveCaptureMetadataInput;
}>;


export type SaveCaptureMetadataMutation = { saveCaptureMetadata: { clientMutationId: string, media: { id: string, status: string, replacesMediaId: string | null } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type DeclareCaptureImpossibilityMutationVariables = Exact<{
  input: DeclareCaptureImpossibilityInput;
}>;


export type DeclareCaptureImpossibilityMutation = { declareCaptureImpossibility: { status: string, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type DeclareCaptureEnvironmentMutationVariables = Exact<{
  input: DeclareCaptureEnvironmentInput;
}>;


export type DeclareCaptureEnvironmentMutation = { declareCaptureEnvironment: { clientMutationId: string, environment: { requirementKey: string, identityId: string, typeCode: string, name: string, expected: boolean, addedDuringCapture: boolean, mediaCount: number, status: string } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type RecordCaptureQualityWarningMutationVariables = Exact<{
  input: RecordCaptureQualityWarningInput;
}>;


export type RecordCaptureQualityWarningMutation = { recordCaptureQualityWarning: { flags: Array<string>, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type SubmitCaptureMutationVariables = Exact<{
  input: SubmitCaptureInput;
}>;


export type SubmitCaptureMutation = { submitCapture: { clientMutationId: string, submission: { id: string, complete: boolean, requiresAttention: boolean, submittedAt: string } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type SubmitRecaptureMutationVariables = Exact<{
  input: SubmitRecaptureInput;
}>;


export type SubmitRecaptureMutation = { submitRecapture: { clientMutationId: string, recapture: { id: string, status: string } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type CounterpartyBaselineQueryVariables = Exact<{ [key: string]: never; }>;


export type CounterpartyBaselineQuery = { counterpartyBaseline: { deadlineAt: string, response: string, respondedAt: string | null, resolution: string, resolutionReason: string | null, adjustmentStatus: string | null, newerVersionExists: boolean, readOnly: boolean, baseline: { hasInventory: boolean, environments: Array<{ typeCode: string, name: string, items: Array<{ typeCode: string, name: string, material: string | null, color: string | null, quantity: number | null, condition: string, observation: string, limitation: string | null, evidence: Array<{ kind: string, position: number }> }> }> } } };

export type RequestCounterpartyOtpMutationVariables = Exact<{
  input: RequestCounterpartyOtpInput;
}>;


export type RequestCounterpartyOtpMutation = { requestCounterpartyOtp: { status: string, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type VerifyCounterpartyOtpMutationVariables = Exact<{
  input: VerifyCounterpartyOtpInput;
}>;


export type VerifyCounterpartyOtpMutation = { verifyCounterpartyOtp: { status: string, csrfToken: string, expiresAt: string, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type AcknowledgeBaselineMutationVariables = Exact<{
  input: CounterpartyResponseInput;
}>;


export type AcknowledgeBaselineMutation = { acknowledgeBaseline: { response: string, respondedAt: string | null, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type ContestBaselineMutationVariables = Exact<{
  input: ContestBaselineInput;
}>;


export type ContestBaselineMutation = { contestBaseline: { response: string, respondedAt: string | null, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };


export const ExternalCaptureBootstrapDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ExternalCaptureBootstrap"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"externalCapture"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"responsibilityId"}},{"kind":"Field","name":{"kind":"Name","value":"recaptureRequestId"}},{"kind":"Field","name":{"kind":"Name","value":"recaptureDeadlineAt"}},{"kind":"Field","name":{"kind":"Name","value":"replacementDeadlineAt"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"confirmationOnly"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"mediaKind"}},{"kind":"Field","name":{"kind":"Name","value":"disclosureVersion"}},{"kind":"Field","name":{"kind":"Name","value":"captureLayout"}},{"kind":"Field","name":{"kind":"Name","value":"reference"}},{"kind":"Field","name":{"kind":"Name","value":"policy"}},{"kind":"Field","name":{"kind":"Name","value":"environments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"requirementKey"}},{"kind":"Field","name":{"kind":"Name","value":"identityId"}},{"kind":"Field","name":{"kind":"Name","value":"typeCode"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"expected"}},{"kind":"Field","name":{"kind":"Name","value":"addedDuringCapture"}},{"kind":"Field","name":{"kind":"Name","value":"mediaCount"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}},{"kind":"Field","name":{"kind":"Name","value":"reviewHandoff"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"inspectionId"}},{"kind":"Field","name":{"kind":"Name","value":"originVersionId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"referenceItems"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mediaId"}},{"kind":"Field","name":{"kind":"Name","value":"evidenceId"}},{"kind":"Field","name":{"kind":"Name","value":"sourceMediaId"}},{"kind":"Field","name":{"kind":"Name","value":"timestampMs"}},{"kind":"Field","name":{"kind":"Name","value":"environment"}},{"kind":"Field","name":{"kind":"Name","value":"component"}},{"kind":"Field","name":{"kind":"Name","value":"view"}},{"kind":"Field","name":{"kind":"Name","value":"ambiguous"}},{"kind":"Field","name":{"kind":"Name","value":"gap"}},{"kind":"Field","name":{"kind":"Name","value":"requirementKey"}},{"kind":"Field","name":{"kind":"Name","value":"description"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"imageUrl"}},{"kind":"Field","name":{"kind":"Name","value":"imageUrlExpiresAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"requirements"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"key"}},{"kind":"Field","name":{"kind":"Name","value":"section"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"instructions"}},{"kind":"Field","name":{"kind":"Name","value":"required"}},{"kind":"Field","name":{"kind":"Name","value":"minimumMedia"}},{"kind":"Field","name":{"kind":"Name","value":"maximumMedia"}},{"kind":"Field","name":{"kind":"Name","value":"descriptionRequired"}},{"kind":"Field","name":{"kind":"Name","value":"captureSourcePolicy"}},{"kind":"Field","name":{"kind":"Name","value":"comparisonTarget"}},{"kind":"Field","name":{"kind":"Name","value":"impossibilityAllowed"}},{"kind":"Field","name":{"kind":"Name","value":"environmentIdentityId"}},{"kind":"Field","name":{"kind":"Name","value":"environmentTypeCode"}},{"kind":"Field","name":{"kind":"Name","value":"environmentName"}},{"kind":"Field","name":{"kind":"Name","value":"expected"}},{"kind":"Field","name":{"kind":"Name","value":"addedDuringCapture"}}]}},{"kind":"Field","name":{"kind":"Name","value":"answers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"requirementKey"}},{"kind":"Field","name":{"kind":"Name","value":"mediaIds"}},{"kind":"Field","name":{"kind":"Name","value":"impossibilityReason"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}}]}}]}}]} as unknown as DocumentNode<ExternalCaptureBootstrapQuery, ExternalCaptureBootstrapQueryVariables>;
export const RequestInvitationOtpDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RequestInvitationOtp"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"RequestInvitationOtpInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"requestInvitationOtp"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<RequestInvitationOtpMutation, RequestInvitationOtpMutationVariables>;
export const VerifyInvitationOtpDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"VerifyInvitationOtp"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"VerifyInvitationOtpInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"verifyInvitationOtp"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"csrfToken"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<VerifyInvitationOtpMutation, VerifyInvitationOtpMutationVariables>;
export const AcceptProcessingDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AcceptProcessing"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"AcceptProcessingInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"acceptProcessing"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<AcceptProcessingMutation, AcceptProcessingMutationVariables>;
export const RevokeInvitationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RevokeInvitation"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"RevokeInvitationInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"revokeInvitation"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<RevokeInvitationMutation, RevokeInvitationMutationVariables>;
export const CreateMediaUploadDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CreateMediaUpload"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CreateMediaUploadInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createMediaUpload"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"upload"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mediaId"}},{"kind":"Field","name":{"kind":"Name","value":"uploadId"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"partSizeBytes"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<CreateMediaUploadMutation, CreateMediaUploadMutationVariables>;
export const BeginVideoCaptureDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"BeginVideoCapture"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"BeginVideoCaptureInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"beginVideoCapture"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"attempt"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"acquisitionSource"}},{"kind":"Field","name":{"kind":"Name","value":"maxDurationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"maxBytes"}},{"kind":"Field","name":{"kind":"Name","value":"policyVersion"}},{"kind":"Field","name":{"kind":"Name","value":"admittedAt"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<BeginVideoCaptureMutation, BeginVideoCaptureMutationVariables>;
export const VideoUploadStatusDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"VideoUploadStatus"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"mediaId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"videoUploadStatus"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"mediaId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"mediaId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mediaId"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"receivedParts"}},{"kind":"Field","name":{"kind":"Name","value":"confirmedParts"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"partNumber"}},{"kind":"Field","name":{"kind":"Name","value":"etag"}},{"kind":"Field","name":{"kind":"Name","value":"sizeBytes"}}]}},{"kind":"Field","name":{"kind":"Name","value":"missingParts"}}]}}]}}]} as unknown as DocumentNode<VideoUploadStatusQuery, VideoUploadStatusQueryVariables>;
export const VideoPreparationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"VideoPreparation"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"mediaId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"videoPreparation"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"mediaId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"mediaId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mediaId"}},{"kind":"Field","name":{"kind":"Name","value":"stage"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"retryable"}},{"kind":"Field","name":{"kind":"Name","value":"processedFrames"}},{"kind":"Field","name":{"kind":"Name","value":"sourceAvailable"}}]}}]}}]} as unknown as DocumentNode<VideoPreparationQuery, VideoPreparationQueryVariables>;
export const PresignMediaPartsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"PresignMediaParts"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"PresignMediaPartsInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"presignMediaParts"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"parts"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"partNumber"}},{"kind":"Field","name":{"kind":"Name","value":"url"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<PresignMediaPartsMutation, PresignMediaPartsMutationVariables>;
export const CompleteMediaUploadDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CompleteMediaUpload"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CompleteMediaUploadInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"completeMediaUpload"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"media"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<CompleteMediaUploadMutation, CompleteMediaUploadMutationVariables>;
export const SaveCaptureMetadataDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SaveCaptureMetadata"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"SaveCaptureMetadataInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"saveCaptureMetadata"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"media"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"replacesMediaId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<SaveCaptureMetadataMutation, SaveCaptureMetadataMutationVariables>;
export const DeclareCaptureImpossibilityDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"DeclareCaptureImpossibility"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"DeclareCaptureImpossibilityInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"declareCaptureImpossibility"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<DeclareCaptureImpossibilityMutation, DeclareCaptureImpossibilityMutationVariables>;
export const DeclareCaptureEnvironmentDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"DeclareCaptureEnvironment"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"DeclareCaptureEnvironmentInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"declareCaptureEnvironment"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"environment"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"requirementKey"}},{"kind":"Field","name":{"kind":"Name","value":"identityId"}},{"kind":"Field","name":{"kind":"Name","value":"typeCode"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"expected"}},{"kind":"Field","name":{"kind":"Name","value":"addedDuringCapture"}},{"kind":"Field","name":{"kind":"Name","value":"mediaCount"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<DeclareCaptureEnvironmentMutation, DeclareCaptureEnvironmentMutationVariables>;
export const RecordCaptureQualityWarningDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RecordCaptureQualityWarning"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"RecordCaptureQualityWarningInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"recordCaptureQualityWarning"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"flags"}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<RecordCaptureQualityWarningMutation, RecordCaptureQualityWarningMutationVariables>;
export const SubmitCaptureDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SubmitCapture"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"SubmitCaptureInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"submitCapture"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"submission"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"complete"}},{"kind":"Field","name":{"kind":"Name","value":"requiresAttention"}},{"kind":"Field","name":{"kind":"Name","value":"submittedAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<SubmitCaptureMutation, SubmitCaptureMutationVariables>;
export const SubmitRecaptureDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SubmitRecapture"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"SubmitRecaptureInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"submitRecapture"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"recapture"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<SubmitRecaptureMutation, SubmitRecaptureMutationVariables>;
export const CounterpartyBaselineDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"CounterpartyBaseline"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"counterpartyBaseline"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"deadlineAt"}},{"kind":"Field","name":{"kind":"Name","value":"response"}},{"kind":"Field","name":{"kind":"Name","value":"respondedAt"}},{"kind":"Field","name":{"kind":"Name","value":"resolution"}},{"kind":"Field","name":{"kind":"Name","value":"resolutionReason"}},{"kind":"Field","name":{"kind":"Name","value":"adjustmentStatus"}},{"kind":"Field","name":{"kind":"Name","value":"newerVersionExists"}},{"kind":"Field","name":{"kind":"Name","value":"readOnly"}},{"kind":"Field","name":{"kind":"Name","value":"baseline"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasInventory"}},{"kind":"Field","name":{"kind":"Name","value":"environments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"typeCode"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"typeCode"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"material"}},{"kind":"Field","name":{"kind":"Name","value":"color"}},{"kind":"Field","name":{"kind":"Name","value":"quantity"}},{"kind":"Field","name":{"kind":"Name","value":"condition"}},{"kind":"Field","name":{"kind":"Name","value":"observation"}},{"kind":"Field","name":{"kind":"Name","value":"limitation"}},{"kind":"Field","name":{"kind":"Name","value":"evidence"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"position"}}]}}]}}]}}]}}]}}]}}]} as unknown as DocumentNode<CounterpartyBaselineQuery, CounterpartyBaselineQueryVariables>;
export const RequestCounterpartyOtpDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RequestCounterpartyOtp"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"RequestCounterpartyOtpInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"requestCounterpartyOtp"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<RequestCounterpartyOtpMutation, RequestCounterpartyOtpMutationVariables>;
export const VerifyCounterpartyOtpDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"VerifyCounterpartyOtp"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"VerifyCounterpartyOtpInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"verifyCounterpartyOtp"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"csrfToken"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<VerifyCounterpartyOtpMutation, VerifyCounterpartyOtpMutationVariables>;
export const AcknowledgeBaselineDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AcknowledgeBaseline"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CounterpartyResponseInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"acknowledgeBaseline"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"response"}},{"kind":"Field","name":{"kind":"Name","value":"respondedAt"}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<AcknowledgeBaselineMutation, AcknowledgeBaselineMutationVariables>;
export const ContestBaselineDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ContestBaseline"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ContestBaselineInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"contestBaseline"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"response"}},{"kind":"Field","name":{"kind":"Name","value":"respondedAt"}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<ContestBaselineMutation, ContestBaselineMutationVariables>;