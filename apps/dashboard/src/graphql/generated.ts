/** Internal type. DO NOT USE DIRECTLY. */
type Exact<T extends { [key: string]: unknown }> = { [K in keyof T]: T[K] };
/** Internal type. DO NOT USE DIRECTLY. */
export type Incremental<T> = T | { [P in keyof T]?: P extends ' $fragmentName' | '__typename' ? T[P] : never };
import { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';
export type AddExceptionalStageInput = {
  clientMutationId: string;
  expectedVersion: number;
  key: string;
  label: string;
  plannedAt: string | null | undefined;
  projectId: string | number;
  reason: string;
};

export type CancelScheduleInput = {
  clientMutationId: string;
  expectedVersion: number;
  scheduleId: string | number;
};

export type ConfigureNotificationPreferencesInput = {
  channels: Array<string | number>;
  clientMutationId: string;
  expectedVersion: number;
};

export type CreateInspectionInput = {
  assetId: string | number;
  clientMutationId: string;
  deadlineAt: string;
  dueAt: string;
  participantId: string | number;
  reason: string;
  referenceVersionId: string | number | null | undefined;
  reminderInstants: Array<string>;
  templateId: string | number | null | undefined;
};

export type CreateProjectInput = {
  assetId: string | number;
  clientMutationId: string;
  participantId: string | number;
  templateId: string | number | null | undefined;
};

export type CreateScheduleInput = {
  assetId: string | number;
  clientMutationId: string;
  deadlineMinutes: number;
  participantId: string | number;
  referenceVersionId: string | number | null | undefined;
  reminderOffsetsMinutes: Array<number>;
  rrule: string;
  startsAt: string;
  templateId: string | number;
  timezone: string;
};

export type EvidenceMode =
  | 'ADVANCED'
  | 'SIMPLE';

export type InspectionTransitionInput = {
  clientMutationId: string;
  expectedVersion: number;
  inspectionId: string | number;
};

export type InvalidateInspectionInput = {
  clientMutationId: string;
  expectedVersion: number;
  inspectionId: string | number;
  reason: string;
};

export type InvalidateReportPublicationInput = {
  clientMutationId: string;
  expectedVersion: number;
  publicationId: string | number;
  reason: string;
};

export type MarkNotificationReadInput = {
  clientMutationId: string;
  notificationId: string | number;
};

export type ProjectTransitionInput = {
  clientMutationId: string;
  expectedVersion: number;
  projectId: string | number;
};

export type PublishReportInput = {
  clientMutationId: string;
  inspectionId: string | number;
  snapshotId: string | number;
};

export type RecaptureItemInput = {
  originalMediaId: string | number | null | undefined;
  reason: string;
  requirementKey: string;
};

export type ReopenProjectInput = {
  clientMutationId: string;
  expectedVersion: number;
  projectId: string | number;
  reason: string;
};

export type RequestRecaptureInput = {
  clientMutationId: string;
  deadlineAt: string;
  inspectionId: string | number;
  items: Array<RecaptureItemInput>;
};

export type SkipProjectStageInput = {
  clientMutationId: string;
  expectedVersion: number;
  projectId: string | number;
  reason: string;
  stageId: string | number;
};

export type StartProjectStageInput = {
  clientMutationId: string;
  deadlineAt: string;
  dueAt: string;
  expectedProjectVersion: number;
  expectedStageVersion: number;
  projectId: string | number;
  referenceVersionId: string | number | null | undefined;
  reminderInstants: Array<string>;
  stageId: string | number;
};

export type UpdateScheduleInput = {
  clientMutationId: string;
  deadlineMinutes: number;
  expectedVersion: number;
  reminderOffsetsMinutes: Array<number>;
  rrule: string;
  scheduleId: string | number;
  startsAt: string;
  timezone: string;
};

export type DashboardGateQueryVariables = Exact<{ [key: string]: never; }>;


export type DashboardGateQuery = { me: { tenantId: string, productEntitlements: Array<string>, roles: Array<string>, effectiveScopes: Array<{ kind: string, resourceId: string }> }, tenant: { id: string, name: string, status: string } | null };

export type DashboardMembershipsQueryVariables = Exact<{ [key: string]: never; }>;


export type DashboardMembershipsQuery = { me: { memberships: Array<{ id: string, tenantId: string, role: string, status: string }> } };

export type DashboardFormOptionsQueryVariables = Exact<{ [key: string]: never; }>;


export type DashboardFormOptionsQuery = { assets: { nodes: Array<{ id: string, name: string, externalKey: string, businessUnitId: string, segmentVersionId: string, templateId: string | null, status: string, assignments: Array<{ participantId: string, role: string, active: boolean }> }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } }, participants: { nodes: Array<{ id: string, businessUnitId: string, name: string, status: string }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } }, templates: { nodes: Array<{ id: string, key: string, name: string, segmentVersionId: string, activeVersionId: string | null, version: number }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type OperationalOverviewQueryVariables = Exact<{
  after: string | null | undefined;
  classification: string | null | undefined;
  status: string | null | undefined;
}>;


export type OperationalOverviewQuery = { dashboardSummary: { total: number, normal: number, attention: number, critical: number, pending: number, invalidated: number }, triageInspections: { nodes: Array<{ inspectionId: string, projectId: string | null, assetId: string | null, classification: string, status: string, updatedAt: string }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type SchedulesQueryVariables = Exact<{
  after: string | null | undefined;
}>;


export type SchedulesQuery = { schedules: { nodes: Array<{ id: string, assetId: string, participantId: string, templateId: string, rrule: string, timezone: string, startsAt: string, nextDueAt: string, deadlineMinutes: number, reminderOffsetsMinutes: Array<number>, status: string, version: number }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type InspectionsQueryVariables = Exact<{
  after: string | null | undefined;
  history: boolean;
}>;


export type InspectionsQuery = { inspections: { nodes: Array<{ id: string, assetId: string, participantId: string, projectId: string | null, stageId: string | null, source: string, sourceReason: string | null, stateReason: string | null, status: string, evidenceCount: number, dueAt: string, deadlineAt: string, reminderInstants: Array<string>, version: number }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type InspectionDetailQueryVariables = Exact<{
  id: string | number;
}>;


export type InspectionDetailQuery = { inspection: { id: string, assetId: string, participantId: string, projectId: string | null, stageId: string | null, source: string, sourceReason: string | null, stateReason: string | null, status: string, evidenceCount: number, dueAt: string, deadlineAt: string, reminderInstants: Array<string>, version: number } | null };

export type ProjectsQueryVariables = Exact<{
  after: string | null | undefined;
}>;


export type ProjectsQuery = { projects: { nodes: Array<{ id: string, assetId: string, participantId: string, templateId: string, reportMode: string, status: string, version: number, stages: Array<{ id: string, key: string, label: string, kind: string, position: number, status: string, plannedAt: string | null, reason: string | null, inspectionId: string | null, version: number }> }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type ProjectDetailQueryVariables = Exact<{
  id: string | number;
}>;


export type ProjectDetailQuery = { project: { id: string, assetId: string, participantId: string, templateId: string, reportMode: string, status: string, version: number, stages: Array<{ id: string, key: string, label: string, kind: string, position: number, status: string, plannedAt: string | null, reason: string | null, inspectionId: string | null, version: number }>, transitions: Array<{ id: string, stageId: string | null, fromState: string | null, toState: string, reason: string | null, occurredAt: string }> } | null };

export type ProjectTimelineQueryVariables = Exact<{
  projectId: string | number;
}>;


export type ProjectTimelineQuery = { projectTimeline: { projectId: string, entries: Array<{ stageId: string, label: string, status: string, inspectionId: string | null, occurredAt: string | null }> } };

export type ReportWorkspaceQueryVariables = Exact<{
  inspectionId: string | number;
  version: number | null | undefined;
}>;


export type ReportWorkspaceQuery = { report: { id: string, inspectionId: string, projectId: string | null, version: number, mode: string, classification: string, jsonDigest: string, htmlDigest: string, canonicalJSON: Record<string, unknown>, html: string, createdAt: string } | null };

export type ReportDownloadQueryVariables = Exact<{
  snapshotId: string | number;
}>;


export type ReportDownloadQuery = { reportDownload: { snapshotId: string, kind: string, objectKey: string, url: string, status: string, sha256: string | null } | null };

export type CustomerPortfolioQueryVariables = Exact<{
  after: string | null | undefined;
}>;


export type CustomerPortfolioQuery = { customerPortfolio: { nodes: Array<{ assetId: string, projectId: string | null, publishedClassification: string | null, status: string, progress: number, updatedAt: string }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type CustomerTimelineQueryVariables = Exact<{
  assetId: string | number | null | undefined;
  projectId: string | number | null | undefined;
  after: string | null | undefined;
}>;


export type CustomerTimelineQuery = { customerTimeline: { nodes: Array<{ id: string, kind: string, status: string, occurredAt: string, historical: boolean }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type CustomerReportQueryVariables = Exact<{
  inspectionId: string | number;
  version: number | null | undefined;
}>;


export type CustomerReportQuery = { customerReport: { inspectionId: string, snapshotId: string, version: number, classification: string, advisory: string, status: string, historical: boolean } | null };

export type CustomerEvidenceQueryVariables = Exact<{
  inspectionId: string | number;
  mode: EvidenceMode;
  after: string | null | undefined;
}>;


export type CustomerEvidenceQuery = { customerEvidence: { nodes: Array<{ id: string, requirementKey: string, description: string | null, captureSource: string | null, state: string, lineageId: string, replacedBy: string | null, mediaAvailability: string, url: string | null }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type MyNotificationsQueryVariables = Exact<{
  after: string | null | undefined;
  unreadOnly: boolean;
}>;


export type MyNotificationsQuery = { myNotifications: { unreadCount: number, nodes: Array<{ id: string, kind: string, title: string, body: string, resourceKind: string, resourceId: string | null, createdAt: string, readAt: string | null }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type NotificationDeliveriesQueryVariables = Exact<{
  after: string | null | undefined;
}>;


export type NotificationDeliveriesQuery = { notificationDeliveries: { nodes: Array<{ id: string, intentId: string, status: string, aggregateStatus: string, selectedProvider: string, createdAt: string, updatedAt: string, channels: Array<{ id: string, channel: string, status: string, provider: string, receiptId: string | null, attempts: number, lastAttemptAt: string | null, createdAt: string, updatedAt: string }> }>, pageInfo: { endCursor: string | null, hasNextPage: boolean } } };

export type CreateScheduleMutationVariables = Exact<{
  input: CreateScheduleInput;
}>;


export type CreateScheduleMutation = { createSchedule: { clientMutationId: string, schedule: { id: string, status: string, nextDueAt: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type UpdateScheduleMutationVariables = Exact<{
  input: UpdateScheduleInput;
}>;


export type UpdateScheduleMutation = { updateSchedule: { clientMutationId: string, schedule: { id: string, status: string, nextDueAt: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type CancelScheduleMutationVariables = Exact<{
  input: CancelScheduleInput;
}>;


export type CancelScheduleMutation = { cancelSchedule: { clientMutationId: string, schedule: { id: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type CreateInspectionMutationVariables = Exact<{
  input: CreateInspectionInput;
}>;


export type CreateInspectionMutation = { createInspection: { clientMutationId: string, inspection: { id: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type CancelInspectionMutationVariables = Exact<{
  input: InspectionTransitionInput;
}>;


export type CancelInspectionMutation = { cancelInspection: { clientMutationId: string, inspection: { id: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type InvalidateInspectionMutationVariables = Exact<{
  input: InvalidateInspectionInput;
}>;


export type InvalidateInspectionMutation = { invalidateInspection: { clientMutationId: string, inspection: { id: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type CreateProjectMutationVariables = Exact<{
  input: CreateProjectInput;
}>;


export type CreateProjectMutation = { createProject: { clientMutationId: string, project: { id: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type AddExceptionalStageMutationVariables = Exact<{
  input: AddExceptionalStageInput;
}>;


export type AddExceptionalStageMutation = { addExceptionalStage: { clientMutationId: string, project: { id: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type StartProjectStageMutationVariables = Exact<{
  input: StartProjectStageInput;
}>;


export type StartProjectStageMutation = { startProjectStage: { clientMutationId: string, project: { id: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type SkipProjectStageMutationVariables = Exact<{
  input: SkipProjectStageInput;
}>;


export type SkipProjectStageMutation = { skipProjectStage: { clientMutationId: string, project: { id: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type CloseProjectMutationVariables = Exact<{
  input: ProjectTransitionInput;
}>;


export type CloseProjectMutation = { closeProject: { clientMutationId: string, project: { id: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type ReopenProjectMutationVariables = Exact<{
  input: ReopenProjectInput;
}>;


export type ReopenProjectMutation = { reopenProject: { clientMutationId: string, project: { id: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type PublishReportMutationVariables = Exact<{
  input: PublishReportInput;
}>;


export type PublishReportMutation = { publishReport: { clientMutationId: string, publication: { id: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type InvalidateReportPublicationMutationVariables = Exact<{
  input: InvalidateReportPublicationInput;
}>;


export type InvalidateReportPublicationMutation = { invalidateReportPublication: { clientMutationId: string, publication: { id: string, status: string, version: number } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type RequestRecaptureMutationVariables = Exact<{
  input: RequestRecaptureInput;
}>;


export type RequestRecaptureMutation = { requestRecapture: { clientMutationId: string, recapture: { id: string, status: string, deadlineAt: string | null } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type MarkNotificationReadMutationVariables = Exact<{
  input: MarkNotificationReadInput;
}>;


export type MarkNotificationReadMutation = { markNotificationRead: { clientMutationId: string, notification: { id: string, readAt: string | null } | null, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };

export type ConfigureNotificationPreferencesMutationVariables = Exact<{
  input: ConfigureNotificationPreferencesInput;
}>;


export type ConfigureNotificationPreferencesMutation = { configureMyNotificationPreferences: { clientMutationId: string, userErrors: Array<{ code: string, field: string | null, message: string, correlationId: string }> } };


export const DashboardGateDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DashboardGate"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"me"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"tenantId"}},{"kind":"Field","name":{"kind":"Name","value":"productEntitlements"}},{"kind":"Field","name":{"kind":"Name","value":"roles"}},{"kind":"Field","name":{"kind":"Name","value":"effectiveScopes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"resourceId"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"tenant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}}]}}]} as unknown as DocumentNode<DashboardGateQuery, DashboardGateQueryVariables>;
export const DashboardMembershipsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DashboardMemberships"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"me"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"memberships"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"tenantId"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}}]}}]}}]} as unknown as DocumentNode<DashboardMembershipsQuery, DashboardMembershipsQueryVariables>;
export const DashboardFormOptionsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DashboardFormOptions"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"assets"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"100"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"externalKey"}},{"kind":"Field","name":{"kind":"Name","value":"businessUnitId"}},{"kind":"Field","name":{"kind":"Name","value":"segmentVersionId"}},{"kind":"Field","name":{"kind":"Name","value":"templateId"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"assignments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"participantId"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"active"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"participants"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"100"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"businessUnitId"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"templates"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"100"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"key"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"segmentVersionId"}},{"kind":"Field","name":{"kind":"Name","value":"activeVersionId"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<DashboardFormOptionsQuery, DashboardFormOptionsQueryVariables>;
export const OperationalOverviewDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"OperationalOverview"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"classification"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"status"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"dashboardSummary"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"total"}},{"kind":"Field","name":{"kind":"Name","value":"normal"}},{"kind":"Field","name":{"kind":"Name","value":"attention"}},{"kind":"Field","name":{"kind":"Name","value":"critical"}},{"kind":"Field","name":{"kind":"Name","value":"pending"}},{"kind":"Field","name":{"kind":"Name","value":"invalidated"}}]}},{"kind":"Field","name":{"kind":"Name","value":"triageInspections"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"25"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}},{"kind":"Argument","name":{"kind":"Name","value":"classification"},"value":{"kind":"Variable","name":{"kind":"Name","value":"classification"}}},{"kind":"Argument","name":{"kind":"Name","value":"status"},"value":{"kind":"Variable","name":{"kind":"Name","value":"status"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"inspectionId"}},{"kind":"Field","name":{"kind":"Name","value":"projectId"}},{"kind":"Field","name":{"kind":"Name","value":"assetId"}},{"kind":"Field","name":{"kind":"Name","value":"classification"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<OperationalOverviewQuery, OperationalOverviewQueryVariables>;
export const SchedulesDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"Schedules"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"schedules"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"25"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"assetId"}},{"kind":"Field","name":{"kind":"Name","value":"participantId"}},{"kind":"Field","name":{"kind":"Name","value":"templateId"}},{"kind":"Field","name":{"kind":"Name","value":"rrule"}},{"kind":"Field","name":{"kind":"Name","value":"timezone"}},{"kind":"Field","name":{"kind":"Name","value":"startsAt"}},{"kind":"Field","name":{"kind":"Name","value":"nextDueAt"}},{"kind":"Field","name":{"kind":"Name","value":"deadlineMinutes"}},{"kind":"Field","name":{"kind":"Name","value":"reminderOffsetsMinutes"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<SchedulesQuery, SchedulesQueryVariables>;
export const InspectionsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"Inspections"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"history"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Boolean"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"inspections"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"25"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}},{"kind":"Argument","name":{"kind":"Name","value":"history"},"value":{"kind":"Variable","name":{"kind":"Name","value":"history"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"assetId"}},{"kind":"Field","name":{"kind":"Name","value":"participantId"}},{"kind":"Field","name":{"kind":"Name","value":"projectId"}},{"kind":"Field","name":{"kind":"Name","value":"stageId"}},{"kind":"Field","name":{"kind":"Name","value":"source"}},{"kind":"Field","name":{"kind":"Name","value":"sourceReason"}},{"kind":"Field","name":{"kind":"Name","value":"stateReason"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"evidenceCount"}},{"kind":"Field","name":{"kind":"Name","value":"dueAt"}},{"kind":"Field","name":{"kind":"Name","value":"deadlineAt"}},{"kind":"Field","name":{"kind":"Name","value":"reminderInstants"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<InspectionsQuery, InspectionsQueryVariables>;
export const InspectionDetailDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"InspectionDetail"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"inspection"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"assetId"}},{"kind":"Field","name":{"kind":"Name","value":"participantId"}},{"kind":"Field","name":{"kind":"Name","value":"projectId"}},{"kind":"Field","name":{"kind":"Name","value":"stageId"}},{"kind":"Field","name":{"kind":"Name","value":"source"}},{"kind":"Field","name":{"kind":"Name","value":"sourceReason"}},{"kind":"Field","name":{"kind":"Name","value":"stateReason"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"evidenceCount"}},{"kind":"Field","name":{"kind":"Name","value":"dueAt"}},{"kind":"Field","name":{"kind":"Name","value":"deadlineAt"}},{"kind":"Field","name":{"kind":"Name","value":"reminderInstants"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}}]}}]} as unknown as DocumentNode<InspectionDetailQuery, InspectionDetailQueryVariables>;
export const ProjectsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"Projects"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"projects"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"25"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"assetId"}},{"kind":"Field","name":{"kind":"Name","value":"participantId"}},{"kind":"Field","name":{"kind":"Name","value":"templateId"}},{"kind":"Field","name":{"kind":"Name","value":"reportMode"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"stages"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"key"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"position"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"plannedAt"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"inspectionId"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<ProjectsQuery, ProjectsQueryVariables>;
export const ProjectDetailDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ProjectDetail"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"project"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"assetId"}},{"kind":"Field","name":{"kind":"Name","value":"participantId"}},{"kind":"Field","name":{"kind":"Name","value":"templateId"}},{"kind":"Field","name":{"kind":"Name","value":"reportMode"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"stages"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"key"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"position"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"plannedAt"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"inspectionId"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"transitions"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"stageId"}},{"kind":"Field","name":{"kind":"Name","value":"fromState"}},{"kind":"Field","name":{"kind":"Name","value":"toState"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"occurredAt"}}]}}]}}]}}]} as unknown as DocumentNode<ProjectDetailQuery, ProjectDetailQueryVariables>;
export const ProjectTimelineDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ProjectTimeline"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"projectId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"projectTimeline"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"projectId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"projectId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"projectId"}},{"kind":"Field","name":{"kind":"Name","value":"entries"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"stageId"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"inspectionId"}},{"kind":"Field","name":{"kind":"Name","value":"occurredAt"}}]}}]}}]}}]} as unknown as DocumentNode<ProjectTimelineQuery, ProjectTimelineQueryVariables>;
export const ReportWorkspaceDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ReportWorkspace"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"inspectionId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"version"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"report"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"inspectionId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"inspectionId"}}},{"kind":"Argument","name":{"kind":"Name","value":"version"},"value":{"kind":"Variable","name":{"kind":"Name","value":"version"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"inspectionId"}},{"kind":"Field","name":{"kind":"Name","value":"projectId"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"classification"}},{"kind":"Field","name":{"kind":"Name","value":"jsonDigest"}},{"kind":"Field","name":{"kind":"Name","value":"htmlDigest"}},{"kind":"Field","name":{"kind":"Name","value":"canonicalJSON"}},{"kind":"Field","name":{"kind":"Name","value":"html"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}}]}}]}}]} as unknown as DocumentNode<ReportWorkspaceQuery, ReportWorkspaceQueryVariables>;
export const ReportDownloadDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ReportDownload"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"snapshotId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"reportDownload"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"snapshotId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"snapshotId"}}},{"kind":"Argument","name":{"kind":"Name","value":"kind"},"value":{"kind":"StringValue","value":"PDF","block":false}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"snapshotId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"objectKey"}},{"kind":"Field","name":{"kind":"Name","value":"url"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"sha256"}}]}}]}}]} as unknown as DocumentNode<ReportDownloadQuery, ReportDownloadQueryVariables>;
export const CustomerPortfolioDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"CustomerPortfolio"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"customerPortfolio"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"25"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"assetId"}},{"kind":"Field","name":{"kind":"Name","value":"projectId"}},{"kind":"Field","name":{"kind":"Name","value":"publishedClassification"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"progress"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<CustomerPortfolioQuery, CustomerPortfolioQueryVariables>;
export const CustomerTimelineDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"CustomerTimeline"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"assetId"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"projectId"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"customerTimeline"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"assetId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"assetId"}}},{"kind":"Argument","name":{"kind":"Name","value":"projectId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"projectId"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"25"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"occurredAt"}},{"kind":"Field","name":{"kind":"Name","value":"historical"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<CustomerTimelineQuery, CustomerTimelineQueryVariables>;
export const CustomerReportDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"CustomerReport"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"inspectionId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"version"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"customerReport"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"inspectionId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"inspectionId"}}},{"kind":"Argument","name":{"kind":"Name","value":"version"},"value":{"kind":"Variable","name":{"kind":"Name","value":"version"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"inspectionId"}},{"kind":"Field","name":{"kind":"Name","value":"snapshotId"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"classification"}},{"kind":"Field","name":{"kind":"Name","value":"advisory"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"historical"}}]}}]}}]} as unknown as DocumentNode<CustomerReportQuery, CustomerReportQueryVariables>;
export const CustomerEvidenceDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"CustomerEvidence"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"inspectionId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"mode"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"EvidenceMode"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"customerEvidence"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"inspectionId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"inspectionId"}}},{"kind":"Argument","name":{"kind":"Name","value":"mode"},"value":{"kind":"Variable","name":{"kind":"Name","value":"mode"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"25"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"requirementKey"}},{"kind":"Field","name":{"kind":"Name","value":"description"}},{"kind":"Field","name":{"kind":"Name","value":"captureSource"}},{"kind":"Field","name":{"kind":"Name","value":"state"}},{"kind":"Field","name":{"kind":"Name","value":"lineageId"}},{"kind":"Field","name":{"kind":"Name","value":"replacedBy"}},{"kind":"Field","name":{"kind":"Name","value":"mediaAvailability"}},{"kind":"Field","name":{"kind":"Name","value":"url"}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<CustomerEvidenceQuery, CustomerEvidenceQueryVariables>;
export const MyNotificationsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"MyNotifications"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"unreadOnly"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Boolean"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"myNotifications"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"25"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}},{"kind":"Argument","name":{"kind":"Name","value":"unreadOnly"},"value":{"kind":"Variable","name":{"kind":"Name","value":"unreadOnly"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"title"}},{"kind":"Field","name":{"kind":"Name","value":"body"}},{"kind":"Field","name":{"kind":"Name","value":"resourceKind"}},{"kind":"Field","name":{"kind":"Name","value":"resourceId"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"readAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"unreadCount"}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<MyNotificationsQuery, MyNotificationsQueryVariables>;
export const NotificationDeliveriesDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"NotificationDeliveries"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"notificationDeliveries"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"25"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"intentId"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"aggregateStatus"}},{"kind":"Field","name":{"kind":"Name","value":"selectedProvider"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"channels"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"provider"}},{"kind":"Field","name":{"kind":"Name","value":"receiptId"}},{"kind":"Field","name":{"kind":"Name","value":"attempts"}},{"kind":"Field","name":{"kind":"Name","value":"lastAttemptAt"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endCursor"}},{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<NotificationDeliveriesQuery, NotificationDeliveriesQueryVariables>;
export const CreateScheduleDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CreateSchedule"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CreateScheduleInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createSchedule"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"schedule"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"nextDueAt"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<CreateScheduleMutation, CreateScheduleMutationVariables>;
export const UpdateScheduleDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"UpdateSchedule"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UpdateScheduleInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"updateSchedule"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"schedule"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"nextDueAt"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<UpdateScheduleMutation, UpdateScheduleMutationVariables>;
export const CancelScheduleDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CancelSchedule"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CancelScheduleInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cancelSchedule"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"schedule"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<CancelScheduleMutation, CancelScheduleMutationVariables>;
export const CreateInspectionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CreateInspection"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CreateInspectionInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createInspection"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"inspection"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<CreateInspectionMutation, CreateInspectionMutationVariables>;
export const CancelInspectionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CancelInspection"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"InspectionTransitionInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cancelInspection"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"inspection"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<CancelInspectionMutation, CancelInspectionMutationVariables>;
export const InvalidateInspectionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"InvalidateInspection"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"InvalidateInspectionInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"invalidateInspection"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"inspection"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<InvalidateInspectionMutation, InvalidateInspectionMutationVariables>;
export const CreateProjectDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CreateProject"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CreateProjectInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createProject"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"project"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<CreateProjectMutation, CreateProjectMutationVariables>;
export const AddExceptionalStageDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AddExceptionalStage"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"AddExceptionalStageInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"addExceptionalStage"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"project"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<AddExceptionalStageMutation, AddExceptionalStageMutationVariables>;
export const StartProjectStageDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"StartProjectStage"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"StartProjectStageInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"startProjectStage"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"project"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<StartProjectStageMutation, StartProjectStageMutationVariables>;
export const SkipProjectStageDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SkipProjectStage"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"SkipProjectStageInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"skipProjectStage"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"project"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<SkipProjectStageMutation, SkipProjectStageMutationVariables>;
export const CloseProjectDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CloseProject"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ProjectTransitionInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"closeProject"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"project"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<CloseProjectMutation, CloseProjectMutationVariables>;
export const ReopenProjectDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ReopenProject"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ReopenProjectInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"reopenProject"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"project"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<ReopenProjectMutation, ReopenProjectMutationVariables>;
export const PublishReportDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"PublishReport"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"PublishReportInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"publishReport"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"publication"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<PublishReportMutation, PublishReportMutationVariables>;
export const InvalidateReportPublicationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"InvalidateReportPublication"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"InvalidateReportPublicationInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"invalidateReportPublication"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"publication"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<InvalidateReportPublicationMutation, InvalidateReportPublicationMutationVariables>;
export const RequestRecaptureDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RequestRecapture"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"RequestRecaptureInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"requestRecapture"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"recapture"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"deadlineAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<RequestRecaptureMutation, RequestRecaptureMutationVariables>;
export const MarkNotificationReadDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"MarkNotificationRead"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"MarkNotificationReadInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"markNotificationRead"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"notification"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"readAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<MarkNotificationReadMutation, MarkNotificationReadMutationVariables>;
export const ConfigureNotificationPreferencesDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ConfigureNotificationPreferences"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ConfigureNotificationPreferencesInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"configureMyNotificationPreferences"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userErrors"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"field"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"correlationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clientMutationId"}}]}}]}}]} as unknown as DocumentNode<ConfigureNotificationPreferencesMutation, ConfigureNotificationPreferencesMutationVariables>;