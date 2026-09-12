/** Internal type. DO NOT USE DIRECTLY. */
type Exact<T extends { [key: string]: unknown }> = { [K in keyof T]: T[K] };
/** Internal type. DO NOT USE DIRECTLY. */
export type Incremental<T> = T | { [P in keyof T]?: P extends ' $fragmentName' | '__typename' ? T[P] : never };
import { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';
export type OnboardingStepInput = {
  clientMutationId: string;
  expectedVersion: number;
  payload: Record<string, unknown>;
  step: string;
};

export type RequestOnboardingOtpInput = {
  clientMutationId: string;
  email: string;
  name: string;
};

export type VerifyOnboardingOtpInput = {
  clientMutationId: string;
  code: string;
  sessionLocator: string;
};

export type OnboardingDefinitionQueryVariables = Exact<{
  segment: string;
}>;


export type OnboardingDefinitionQuery = { onboardingDefinition: { schemaVersion: number, version: number, segment: string, segmentVersion: string, purposes: Array<string>, templates: Array<string>, analysisProfile: string, steps: Array<{ key: string, label: string, position: number, required: boolean, fields: Array<{ key: string, label: string, type: string, required: boolean, placeholder: string | null, options: Array<string> }> }>, originModes: Array<{ key: string, label: string, templateKey: string, required: boolean }> } };

export type OnboardingSessionQueryVariables = Exact<{ [key: string]: never; }>;


export type OnboardingSessionQuery = { onboardingSession: { id: string, state: string, currentStep: string, version: number, expiresAt: string, definition: Record<string, unknown> } | null };

export type RequestOnboardingOtpMutationVariables = Exact<{
  input: RequestOnboardingOtpInput;
}>;


export type RequestOnboardingOtpMutation = { requestOnboardingOtp: { sessionLocator: string | null, clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string }> } };

export type VerifyOnboardingOtpMutationVariables = Exact<{
  input: VerifyOnboardingOtpInput;
}>;


export type VerifyOnboardingOtpMutation = { verifyOnboardingOtp: { clientMutationId: string, session: { id: string, state: string, currentStep: string, version: number, expiresAt: string, definition: Record<string, unknown> } | null, userErrors: Array<{ code: string, field: string | null, message: string }> } };

export type SaveOnboardingStepMutationVariables = Exact<{
  input: OnboardingStepInput;
}>;


export type SaveOnboardingStepMutation = { saveOnboardingStep: { clientMutationId: string, session: { id: string, state: string, currentStep: string, version: number, expiresAt: string, definition: Record<string, unknown> } | null, status: { state: string, requestId: string | null, inspectionId: string | null, nextAction: string, originStatus: string, deliveryStatus: string } | null, userErrors: Array<{ code: string, field: string | null, message: string }> } };


export const OnboardingDefinitionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"OnboardingDefinition"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"segment"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"onboardingDefinition"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"segment"},"value":{"kind":"Variable","name":{"kind":"Name","value":"segment"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"schemaVersion"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"segment"}},{"kind":"Field","name":{"kind":"Name","value":"segmentVersion"}},{"kind":"Field","name":{"kind":"Name","value":"steps"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"key"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"position"}},{"kind":"Field","name":{"kind":"Name","value":"required"}},{"kind":"Field","name":{"kind":"Name","value":"fields"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"key"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"required"}},{"kind":"Field","name":{"kind":"Name","value":"placeholder"}},{"kind":"Field","name":{"kind":"Name","value":"options"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"purposes"}},{"kind":"Field","name":{"kind":"Name","value":"originModes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"key"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"templateKey"}},{"kind":"Field","name":{"kind":"Name","value":"required"}}]}},{"kind":"Field","name":{"kind":"Name","value":"templates"}},{"kind":"Field","name":{"kind":"Name","value":"analysisProfile"}}]}}]}}]} as unknown as DocumentNode<OnboardingDefinitionQuery, OnboardingDefinitionQueryVariables>;
export const OnboardingSessionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"OnboardingSession"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"onboardingSession"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"state"}},{"kind":"Field","name":{"kind":"Name","value":"currentStep"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"definition"}}]}}]}}]} as unknown as DocumentNode<OnboardingSessionQuery, OnboardingSessionQueryVariables>;
export const RequestOnboardingOtpDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RequestOnboardingOtp"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"RequestOnboardingOtpInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"requestOnboardingOtp"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"sessionLocator"}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<RequestOnboardingOtpMutation, RequestOnboardingOtpMutationVariables>;
export const VerifyOnboardingOtpDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"VerifyOnboardingOtp"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"VerifyOnboardingOtpInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"verifyOnboardingOtp"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"session"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"state"}},{"kind":"Field","name":{"kind":"Name","value":"currentStep"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"definition"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<VerifyOnboardingOtpMutation, VerifyOnboardingOtpMutationVariables>;
export const SaveOnboardingStepDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SaveOnboardingStep"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"OnboardingStepInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"saveOnboardingStep"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"session"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"state"}},{"kind":"Field","name":{"kind":"Name","value":"currentStep"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"definition"}}]}},{"kind":"Field","name":{"kind":"Name","value":"status"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"state"}},{"kind":"Field","name":{"kind":"Name","value":"requestId"}},{"kind":"Field","name":{"kind":"Name","value":"inspectionId"}},{"kind":"Field","name":{"kind":"Name","value":"nextAction"}},{"kind":"Field","name":{"kind":"Name","value":"originStatus"}},{"kind":"Field","name":{"kind":"Name","value":"deliveryStatus"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<SaveOnboardingStepMutation, SaveOnboardingStepMutationVariables>;