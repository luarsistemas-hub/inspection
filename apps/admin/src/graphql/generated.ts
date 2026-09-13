/** Internal type. DO NOT USE DIRECTLY. */
type Exact<T extends { [key: string]: unknown }> = { [K in keyof T]: T[K] };
/** Internal type. DO NOT USE DIRECTLY. */
export type Incremental<T> = T | { [P in keyof T]?: P extends ' $fragmentName' | '__typename' ? T[P] : never };
import type * as Schema from "./schema";
import type * as Types from "./generated";
import { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';
export type AssetAssignmentInput = {
  participantId: string | number;
  role: string;
};

export type AssetInput = {
  address: string;
  assignments: Array<AssetAssignmentInput>;
  attributes: Record<string, unknown>;
  businessUnitId: string | number;
  externalKey: string;
  geofenceMeters: number | null | undefined;
  latitudeE6: number | null | undefined;
  longitudeE6: number | null | undefined;
  name: string;
  policyOverrides: Record<string, unknown> | null | undefined;
  segmentVersionId: string | number;
  templateId: string | number | null | undefined;
};

export type ConfigurePublicationPolicyInput = {
  clientMutationId: string;
  expectedVersion: number;
  mode: string;
};

export type ContactInput = {
  channel: string;
  value: string;
};

export type CreateBusinessUnitInput = {
  clientMutationId: string;
  code: string;
  expectedTenantVersion: number;
  name: string;
};

export type CreateTenantInput = {
  businessUnitCode: string;
  businessUnitName: string;
  clientMutationId: string;
  language: string | null | undefined;
  name: string;
  timezone: string | null | undefined;
};

export type InviteInternalUserInput = {
  clientMutationId: string;
  issuer: string;
  role: string;
  scopes: Array<ScopeAssignmentInput>;
  subject: string;
};

export type RegisterAssetInput = {
  asset: AssetInput;
  clientMutationId: string;
};

export type ScopeAssignmentInput = {
  kind: string;
  resourceId: string | number;
};

export type UpsertParticipantInput = {
  businessUnitId: string | number;
  clientMutationId: string;
  contacts: Array<ContactInput>;
  expectedVersion: number | null | undefined;
  name: string;
  participantId: string | number | null | undefined;
  segmentRole: string;
};

export type AdminIdentityQueryVariables = Exact<{ [key: string]: never; }>;


export type AdminIdentityQuery = { me: { identityId: string, tenantId: string, roles: Array<string>, productEntitlements: Array<string>, memberships: Array<{ id: string, tenantId: string, role: string, status: string, scopes: Array<{ kind: string, resourceId: string }> }>, effectiveScopes: Array<{ kind: string, resourceId: string }> }, tenant: { id: string, name: string, language: string, defaultTimezone: string, status: string, version: number } | null };

export type AdminGateQueryVariables = Exact<{ [key: string]: never; }>;


export type AdminGateQuery = { me: { tenantId: string, roles: Array<string>, productEntitlements: Array<string> }, tenant: { id: string, name: string, status: string } | null };

export type BootstrapTenantMutationVariables = Exact<{
  input: Schema.CreateTenantInput;
}>;


export type BootstrapTenantMutation = { createTenant: { clientMutationId: string, tenant: { id: string, name: string, status: string } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type AdminOrganizationQueryVariables = Exact<{
  first: number;
  after: string | null | undefined;
}>;


export type AdminOrganizationQuery = { businessUnits: { nodes: Array<{ id: string, code: string, name: string, status: string, version: number }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type AdminAccessQueryVariables = Exact<{
  first: number;
  after: string | null | undefined;
}>;


export type AdminAccessQuery = { memberships: { nodes: Array<{ id: string, role: string, status: string, version: number, scopes: Array<{ kind: string, resourceId: string }> }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type AdminCatalogsQueryVariables = Exact<{
  first: number;
  after: string | null | undefined;
  search: string | null | undefined;
}>;


export type AdminCatalogsQuery = { participants: { nodes: Array<{ id: string, name: string, status: string, version: number, contacts: Array<{ id: string, channel: string, value: string, verified: boolean, active: boolean }> }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } }, segmentDefinitions: { nodes: Array<{ id: string, key: string, name: string, activeVersionId: string | null, version: number }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } }, templates: { nodes: Array<{ id: string, key: string, name: string, segmentVersionId: string, activeVersionId: string | null, version: number }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type AdminAssetsQueryVariables = Exact<{
  first: number;
  after: string | null | undefined;
  search: string | null | undefined;
}>;


export type AdminAssetsQuery = { assets: { nodes: Array<{ id: string, name: string, externalKey: string, address: string, status: string, businessUnitId: string, version: number }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type AdminGovernanceQueryVariables = Exact<{
  first: number;
  after: string | null | undefined;
}>;


export type AdminGovernanceQuery = { publicationPolicy: { mode: string, version: number }, retentionPolicies: { nodes: Array<{ id: string, evidenceDays: number, operationalDays: number, securityDays: number, version: number }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } }, notificationDeliveries: { nodes: Array<{ id: string, status: string, createdAt: string, updatedAt: string }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type AdminAuditQueryVariables = Exact<{
  first: number;
  after: string | null | undefined;
}>;


export type AdminAuditQuery = { auditEvents: { nodes: Array<{ id: string, action: string, targetType: string, targetId: string, outcome: string, reason: string | null, correlationId: string, occurredAt: string }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } }, usageSummary: { from: string, to: string, requests: number, inputTokens: number, outputTokens: number, cost: number } };

export type AdminHistoryQueryVariables = Exact<{
  first: number;
  after: string | null | undefined;
}>;


export type AdminHistoryQuery = { auditEvents: { nodes: Array<{ id: string, action: string, targetType: string, targetId: string, outcome: string, reason: string | null, correlationId: string, occurredAt: string }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type AdminCreateBusinessUnitMutationVariables = Exact<{
  input: Schema.CreateBusinessUnitInput;
}>;


export type AdminCreateBusinessUnitMutation = { createBusinessUnit: { clientMutationId: string, businessUnit: { id: string, code: string, name: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type AdminInviteInternalUserMutationVariables = Exact<{
  input: Schema.InviteInternalUserInput;
}>;


export type AdminInviteInternalUserMutation = { inviteInternalUser: { clientMutationId: string, membership: { id: string, role: string, status: string, version: number, scopes: Array<{ kind: string, resourceId: string }> } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type AdminUpsertParticipantMutationVariables = Exact<{
  input: Schema.UpsertParticipantInput;
}>;


export type AdminUpsertParticipantMutation = { upsertParticipant: { clientMutationId: string, participant: { id: string, name: string, status: string, version: number, contacts: Array<{ id: string, channel: string, value: string, verified: boolean, active: boolean }> } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type AdminRegisterAssetMutationVariables = Exact<{
  input: Schema.RegisterAssetInput;
}>;


export type AdminRegisterAssetMutation = { registerAsset: { clientMutationId: string, asset: { id: string, name: string, externalKey: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type AdminConfigurePublicationPolicyMutationVariables = Exact<{
  input: Schema.ConfigurePublicationPolicyInput;
}>;


export type AdminConfigurePublicationPolicyMutation = { configurePublicationPolicy: { clientMutationId: string, policy: { mode: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };


export const AdminIdentityDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminIdentity"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"me"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"identityId"}},{"kind":"Field","name":{"kind":"Name","value":"tenantId"}},{"kind":"Field","name":{"kind":"Name","value":"roles"}},{"kind":"Field","name":{"kind":"Name","value":"productEntitlements"}},{"kind":"Field","name":{"kind":"Name","value":"memberships"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"tenantId"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"scopes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"resourceId"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"effectiveScopes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"resourceId"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"tenant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"language"}},{"kind":"Field","name":{"kind":"Name","value":"defaultTimezone"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}}]}}]} as unknown as DocumentNode<Types.AdminIdentityQuery, Types.AdminIdentityQueryVariables>;
export const AdminGateDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminGate"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"me"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"tenantId"}},{"kind":"Field","name":{"kind":"Name","value":"roles"}},{"kind":"Field","name":{"kind":"Name","value":"productEntitlements"}}]}},{"kind":"Field","name":{"kind":"Name","value":"tenant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}}]}}]} as unknown as DocumentNode<Types.AdminGateQuery, Types.AdminGateQueryVariables>;
export const BootstrapTenantDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"BootstrapTenant"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CreateTenantInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createTenant"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"tenant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<Types.BootstrapTenantMutation, Types.BootstrapTenantMutationVariables>;
export const AdminOrganizationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminOrganization"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"businessUnits"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<Types.AdminOrganizationQuery, Types.AdminOrganizationQueryVariables>;
export const AdminAccessDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminAccess"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"memberships"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"scopes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"resourceId"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<Types.AdminAccessQuery, Types.AdminAccessQueryVariables>;
export const AdminCatalogsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminCatalogs"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"search"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"participants"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}},{"kind":"Argument","name":{"kind":"Name","value":"search"},"value":{"kind":"Variable","name":{"kind":"Name","value":"search"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"contacts"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"value"}},{"kind":"Field","name":{"kind":"Name","value":"verified"}},{"kind":"Field","name":{"kind":"Name","value":"active"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"segmentDefinitions"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}},{"kind":"Argument","name":{"kind":"Name","value":"search"},"value":{"kind":"Variable","name":{"kind":"Name","value":"search"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"key"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"activeVersionId"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"templates"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}},{"kind":"Argument","name":{"kind":"Name","value":"search"},"value":{"kind":"Variable","name":{"kind":"Name","value":"search"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"key"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"segmentVersionId"}},{"kind":"Field","name":{"kind":"Name","value":"activeVersionId"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<Types.AdminCatalogsQuery, Types.AdminCatalogsQueryVariables>;
export const AdminAssetsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminAssets"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"search"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"assets"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}},{"kind":"Argument","name":{"kind":"Name","value":"search"},"value":{"kind":"Variable","name":{"kind":"Name","value":"search"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"externalKey"}},{"kind":"Field","name":{"kind":"Name","value":"address"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"businessUnitId"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<Types.AdminAssetsQuery, Types.AdminAssetsQueryVariables>;
export const AdminGovernanceDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminGovernance"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"publicationPolicy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"retentionPolicies"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"evidenceDays"}},{"kind":"Field","name":{"kind":"Name","value":"operationalDays"}},{"kind":"Field","name":{"kind":"Name","value":"securityDays"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"notificationDeliveries"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<Types.AdminGovernanceQuery, Types.AdminGovernanceQueryVariables>;
export const AdminAuditDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminAudit"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"auditEvents"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"action"}},{"kind":"Field","name":{"kind":"Name","value":"targetType"}},{"kind":"Field","name":{"kind":"Name","value":"targetId"}},{"kind":"Field","name":{"kind":"Name","value":"outcome"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}},{"kind":"Field","name":{"kind":"Name","value":"occurredAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"usageSummary"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"from"}},{"kind":"Field","name":{"kind":"Name","value":"to"}},{"kind":"Field","name":{"kind":"Name","value":"requests"}},{"kind":"Field","name":{"kind":"Name","value":"inputTokens"}},{"kind":"Field","name":{"kind":"Name","value":"outputTokens"}},{"kind":"Field","name":{"kind":"Name","value":"cost"}}]}}]}}]} as unknown as DocumentNode<Types.AdminAuditQuery, Types.AdminAuditQueryVariables>;
export const AdminHistoryDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminHistory"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"auditEvents"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"action"}},{"kind":"Field","name":{"kind":"Name","value":"targetType"}},{"kind":"Field","name":{"kind":"Name","value":"targetId"}},{"kind":"Field","name":{"kind":"Name","value":"outcome"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}},{"kind":"Field","name":{"kind":"Name","value":"occurredAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<Types.AdminHistoryQuery, Types.AdminHistoryQueryVariables>;
export const AdminCreateBusinessUnitDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminCreateBusinessUnit"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CreateBusinessUnitInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createBusinessUnit"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"businessUnit"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<Types.AdminCreateBusinessUnitMutation, Types.AdminCreateBusinessUnitMutationVariables>;
export const AdminInviteInternalUserDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminInviteInternalUser"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"InviteInternalUserInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"inviteInternalUser"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"membership"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"scopes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"resourceId"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<Types.AdminInviteInternalUserMutation, Types.AdminInviteInternalUserMutationVariables>;
export const AdminUpsertParticipantDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminUpsertParticipant"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UpsertParticipantInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"upsertParticipant"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"participant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"contacts"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"value"}},{"kind":"Field","name":{"kind":"Name","value":"verified"}},{"kind":"Field","name":{"kind":"Name","value":"active"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<Types.AdminUpsertParticipantMutation, Types.AdminUpsertParticipantMutationVariables>;
export const AdminRegisterAssetDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminRegisterAsset"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"RegisterAssetInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"registerAsset"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"asset"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"externalKey"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<Types.AdminRegisterAssetMutation, Types.AdminRegisterAssetMutationVariables>;
export const AdminConfigurePublicationPolicyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminConfigurePublicationPolicy"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ConfigurePublicationPolicyInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"configurePublicationPolicy"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"policy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<Types.AdminConfigurePublicationPolicyMutation, Types.AdminConfigurePublicationPolicyMutationVariables>;