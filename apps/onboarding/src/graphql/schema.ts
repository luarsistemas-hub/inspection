export type Maybe<T> = T | null;
export type InputMaybe<T> = Maybe<T>;
/** All built-in and custom scalars, mapped to their actual values */
export type Scalars = {
  ID: { input: string; output: string; }
  String: { input: string; output: string; }
  Boolean: { input: boolean; output: boolean; }
  Int: { input: number; output: number; }
  Float: { input: number; output: number; }
  JSON: { input: Record<string, unknown>; output: Record<string, unknown>; }
};

export type AcceptProcessingInput = {
  aiAnalysis: Scalars['Boolean']['input'];
  clientMutationId: Scalars['String']['input'];
  disclosureVersion: Scalars['String']['input'];
  gpsUse: Scalars['Boolean']['input'];
  photoProcessing: Scalars['Boolean']['input'];
  videoProcessing: InputMaybe<Scalars['Boolean']['input']>;
};

export type ActivateBaselineVersionInput = {
  clientMutationId: Scalars['String']['input'];
  counterpartyParticipantIds: Array<Scalars['ID']['input']>;
  expectedOriginVersion: Scalars['Int']['input'];
  originVersionId: Scalars['ID']['input'];
  purpose: Scalars['String']['input'];
};

export type ActivateSegmentDefinitionInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  versionId: Scalars['ID']['input'];
};

export type ActivateTemplateVersionInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  versionId: Scalars['ID']['input'];
};

export type AddExceptionalStageInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  key: Scalars['String']['input'];
  label: Scalars['String']['input'];
  plannedAt: InputMaybe<Scalars['String']['input']>;
  projectId: Scalars['ID']['input'];
  reason: Scalars['String']['input'];
};

export type AddInventoryItemInput = {
  clientMutationId: Scalars['String']['input'];
  color: InputMaybe<Scalars['String']['input']>;
  condition: InputMaybe<Scalars['String']['input']>;
  environmentId: Scalars['ID']['input'];
  evidence: Array<InventoryEvidenceInput>;
  limitation: InputMaybe<Scalars['String']['input']>;
  material: InputMaybe<Scalars['String']['input']>;
  name: Scalars['String']['input'];
  observation: InputMaybe<Scalars['String']['input']>;
  originVersionId: Scalars['ID']['input'];
  quantity: InputMaybe<Scalars['Int']['input']>;
  typeCode: Scalars['String']['input'];
};

export type AnalysisPrompt = {
  __typename?: 'AnalysisPrompt';
  analysisType: AnalysisType;
  canonicalDigest: Scalars['String']['output'];
  minimumConfidenceBps: Scalars['Int']['output'];
  modelAlias: Scalars['String']['output'];
  revision: Scalars['Int']['output'];
  systemPrompt: Scalars['String']['output'];
  task: AnalysisPromptTask;
  updatedAt: Scalars['String']['output'];
};

export type AnalysisPromptPayload = {
  __typename?: 'AnalysisPromptPayload';
  clientMutationId: Scalars['String']['output'];
  prompt: Maybe<AnalysisPrompt>;
  userErrors: Array<UserError>;
};

export type AnalysisPromptTask =
  | 'FINDINGS'
  | 'INVENTORY_CATALOG'
  | 'INVENTORY_COMPARE';

export type AnalysisType =
  | 'REAL_ESTATE';

export type ArchiveAssetInput = {
  assetId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
};

export type ArchiveBusinessUnitInput = {
  businessUnitId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
};

export type Asset = {
  __typename?: 'Asset';
  address: Scalars['String']['output'];
  addressDetails: Maybe<PostalAddress>;
  addressStatus: Scalars['String']['output'];
  assignments: Array<AssetAssignment>;
  attributes: Scalars['JSON']['output'];
  businessUnitId: Scalars['ID']['output'];
  externalKey: Scalars['String']['output'];
  geofenceMeters: Scalars['Int']['output'];
  id: Scalars['ID']['output'];
  latitudeE6: Maybe<Scalars['Int']['output']>;
  longitudeE6: Maybe<Scalars['Int']['output']>;
  name: Scalars['String']['output'];
  policyOverrides: Scalars['JSON']['output'];
  segmentVersionId: Scalars['ID']['output'];
  status: Scalars['String']['output'];
  templateId: Maybe<Scalars['ID']['output']>;
  version: Scalars['Int']['output'];
};

export type AssetAssignment = {
  __typename?: 'AssetAssignment';
  active: Scalars['Boolean']['output'];
  participantId: Scalars['ID']['output'];
  role: Scalars['String']['output'];
};

export type AssetAssignmentInput = {
  participantId: Scalars['ID']['input'];
  role: Scalars['String']['input'];
};

export type AssetConnection = {
  __typename?: 'AssetConnection';
  nodes: Array<Asset>;
  pageInfo: PageInfo;
};

export type AssetInput = {
  address: InputMaybe<Scalars['String']['input']>;
  addressDetails: InputMaybe<PostalAddressInput>;
  assignments: Array<AssetAssignmentInput>;
  attributes: Scalars['JSON']['input'];
  businessUnitId: Scalars['ID']['input'];
  externalKey: Scalars['String']['input'];
  geofenceMeters: InputMaybe<Scalars['Int']['input']>;
  latitudeE6: InputMaybe<Scalars['Int']['input']>;
  longitudeE6: InputMaybe<Scalars['Int']['input']>;
  name: Scalars['String']['input'];
  policyOverrides: InputMaybe<Scalars['JSON']['input']>;
  segmentVersionId: Scalars['ID']['input'];
  templateId: InputMaybe<Scalars['ID']['input']>;
};

export type AssetPayload = {
  __typename?: 'AssetPayload';
  asset: Maybe<Asset>;
  clientMutationId: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type AssignRoleScopesInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: InputMaybe<Scalars['Int']['input']>;
  membershipId: Scalars['ID']['input'];
  role: Scalars['String']['input'];
  scopes: Array<ScopeAssignmentInput>;
};

export type AuditEvent = {
  __typename?: 'AuditEvent';
  action: Scalars['String']['output'];
  correlationId: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  occurredAt: Scalars['String']['output'];
  outcome: Scalars['String']['output'];
  reason: Maybe<Scalars['String']['output']>;
  targetId: Scalars['String']['output'];
  targetType: Scalars['String']['output'];
};

export type AuditEventConnection = {
  __typename?: 'AuditEventConnection';
  nodes: Array<AuditEvent>;
  pageInfo: PageInfo;
};

export type BaselineCounterpartyInvitation = {
  __typename?: 'BaselineCounterpartyInvitation';
  adjustmentVersionId: Maybe<Scalars['ID']['output']>;
  deadlineAt: Scalars['String']['output'];
  deliveryId: Maybe<Scalars['ID']['output']>;
  deliveryStatus: Maybe<Scalars['String']['output']>;
  id: Scalars['ID']['output'];
  participantId: Scalars['ID']['output'];
  resolution: Scalars['String']['output'];
  resolutionReason: Maybe<Scalars['String']['output']>;
  respondedAt: Maybe<Scalars['String']['output']>;
  response: Scalars['String']['output'];
  statement: Maybe<Scalars['String']['output']>;
};

export type BaselineInventory = {
  __typename?: 'BaselineInventory';
  environments: Array<InventoryEnvironment>;
  hasInventory: Scalars['Boolean']['output'];
  version: Maybe<OriginVersion>;
};

export type BaselinePolicy = {
  __typename?: 'BaselinePolicy';
  contestationDeadlineDays: Scalars['Int']['output'];
  version: Scalars['Int']['output'];
};

export type BaselinePolicyPayload = {
  __typename?: 'BaselinePolicyPayload';
  clientMutationId: Scalars['String']['output'];
  policy: Maybe<BaselinePolicy>;
  userErrors: Array<UserError>;
};

export type BeginVideoCaptureInput = {
  acquisitionSource: Scalars['String']['input'];
  clientMutationId: Scalars['String']['input'];
  replacesMediaId: InputMaybe<Scalars['ID']['input']>;
  requirementKey: InputMaybe<Scalars['String']['input']>;
};

export type BulkAcceptInventoryEnvironmentInput = {
  clientMutationId: Scalars['String']['input'];
  environmentId: Scalars['ID']['input'];
  minimumConfidenceBps: InputMaybe<Scalars['Int']['input']>;
  originVersionId: Scalars['ID']['input'];
};

export type BulkAcceptInventoryEnvironmentPayload = {
  __typename?: 'BulkAcceptInventoryEnvironmentPayload';
  acceptedCount: Scalars['Int']['output'];
  clientMutationId: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type BusinessUnit = {
  __typename?: 'BusinessUnit';
  code: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  name: Scalars['String']['output'];
  status: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type BusinessUnitConnection = {
  __typename?: 'BusinessUnitConnection';
  nodes: Array<BusinessUnit>;
  pageInfo: PageInfo;
};

export type BusinessUnitPayload = {
  __typename?: 'BusinessUnitPayload';
  businessUnit: Maybe<BusinessUnit>;
  clientMutationId: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type CancelScheduleInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  scheduleId: Scalars['ID']['input'];
};

export type CaptureAnswer = {
  __typename?: 'CaptureAnswer';
  impossibilityReason: Maybe<Scalars['String']['output']>;
  mediaIds: Array<Scalars['ID']['output']>;
  requirementKey: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type CaptureComparisonMode =
  | 'CHECKLIST_ONLY'
  | 'FIXED_ORIGIN';

export type CaptureEnvironment = {
  __typename?: 'CaptureEnvironment';
  addedDuringCapture: Scalars['Boolean']['output'];
  expected: Scalars['Boolean']['output'];
  identityId: Scalars['ID']['output'];
  mediaCount: Scalars['Int']['output'];
  name: Scalars['String']['output'];
  requirementKey: Scalars['String']['output'];
  status: Scalars['String']['output'];
  typeCode: Scalars['String']['output'];
};

export type CaptureEnvironmentPayload = {
  __typename?: 'CaptureEnvironmentPayload';
  clientMutationId: Scalars['String']['output'];
  environment: Maybe<CaptureEnvironment>;
  userErrors: Array<UserError>;
};

export type CaptureGpsInput = {
  accuracyMeters: Scalars['Float']['input'];
  capturedAt: Scalars['String']['input'];
  latitude: Scalars['Float']['input'];
  longitude: Scalars['Float']['input'];
  windowStartedAt: Scalars['String']['input'];
};

export type CapturePayload = {
  __typename?: 'CapturePayload';
  clientMutationId: Scalars['String']['output'];
  status: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type CaptureQualityWarningPayload = {
  __typename?: 'CaptureQualityWarningPayload';
  clientMutationId: Scalars['String']['output'];
  flags: Array<Scalars['String']['output']>;
  userErrors: Array<UserError>;
};

export type CaptureReferenceItem = {
  __typename?: 'CaptureReferenceItem';
  ambiguous: Maybe<Scalars['Boolean']['output']>;
  availability: Scalars['String']['output'];
  component: Maybe<Scalars['String']['output']>;
  description: Scalars['String']['output'];
  environment: Maybe<Scalars['String']['output']>;
  evidenceId: Maybe<Scalars['ID']['output']>;
  gap: Maybe<Scalars['Boolean']['output']>;
  imageUrl: Maybe<Scalars['String']['output']>;
  imageUrlExpiresAt: Maybe<Scalars['String']['output']>;
  mediaId: Scalars['ID']['output'];
  reason: Maybe<Scalars['String']['output']>;
  requirementKey: Scalars['String']['output'];
  sourceMediaId: Maybe<Scalars['ID']['output']>;
  timestampMs: Maybe<Scalars['Int']['output']>;
  view: Maybe<Scalars['String']['output']>;
};

export type CaptureRequirement = {
  __typename?: 'CaptureRequirement';
  addedDuringCapture: Maybe<Scalars['Boolean']['output']>;
  captureSourcePolicy: Scalars['String']['output'];
  comparisonTarget: Scalars['String']['output'];
  descriptionRequired: Scalars['Boolean']['output'];
  environmentIdentityId: Maybe<Scalars['ID']['output']>;
  environmentName: Maybe<Scalars['String']['output']>;
  environmentTypeCode: Maybe<Scalars['String']['output']>;
  evidenceKind: Scalars['String']['output'];
  expected: Maybe<Scalars['Boolean']['output']>;
  impossibilityAllowed: Scalars['Boolean']['output'];
  instructions: Maybe<Scalars['String']['output']>;
  key: Scalars['String']['output'];
  label: Scalars['String']['output'];
  maximumMedia: Scalars['Int']['output'];
  minimumMedia: Scalars['Int']['output'];
  required: Scalars['Boolean']['output'];
  section: Scalars['String']['output'];
};

export type CaptureReviewHandoff = {
  __typename?: 'CaptureReviewHandoff';
  inspectionId: Scalars['ID']['output'];
  originVersionId: Maybe<Scalars['ID']['output']>;
};

export type CloseOriginInvitationInput = {
  clientMutationId: Scalars['String']['input'];
  reason: Scalars['String']['input'];
  versionId: Scalars['ID']['input'];
};

export type CompleteInternalUserActivationInput = {
  clientMutationId: Scalars['String']['input'];
  password: InputMaybe<Scalars['String']['input']>;
};

export type CompleteMediaUploadInput = {
  clientMutationId: Scalars['String']['input'];
  mediaId: Scalars['ID']['input'];
  parts: Array<CompletedPartInput>;
};

export type CompleteOnboardingInput = {
  clientMutationId: Scalars['String']['input'];
};

export type CompletedPartInput = {
  etag: Scalars['String']['input'];
  partNumber: Scalars['Int']['input'];
};

export type ConfigureNotificationPreferencesInput = {
  channels: Array<Scalars['ID']['input']>;
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
};

export type ConfigurePublicationPolicyInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  mode: Scalars['String']['input'];
};

export type ConfigureRetentionPolicyInput = {
  clientMutationId: Scalars['String']['input'];
  evidenceDays: Scalars['Int']['input'];
  operationalDays: Scalars['Int']['input'];
  securityDays: InputMaybe<Scalars['Int']['input']>;
};

export type ConfigureVideoAdmissionInput = {
  clientMutationId: Scalars['String']['input'];
  enabled: Scalars['Boolean']['input'];
  expectedVersion: Scalars['Int']['input'];
  reason: Scalars['String']['input'];
};

export type ConfigureVideoPolicyInput = {
  clientMutationId: Scalars['String']['input'];
  currentRetentionDays: Scalars['Int']['input'];
  expectedVersion: Scalars['Int']['input'];
  maxDurationMinutes: Scalars['Int']['input'];
  originRetentionDays: Scalars['Int']['input'];
  supersededBaselineRetentionDays: InputMaybe<Scalars['Int']['input']>;
};

export type ContactInput = {
  channel: Scalars['String']['input'];
  value: Scalars['String']['input'];
};

export type ContestBaselineInput = {
  clientMutationId: Scalars['String']['input'];
  statement: Scalars['String']['input'];
};

export type ContestationSummary = {
  __typename?: 'ContestationSummary';
  acknowledged: Scalars['Int']['output'];
  contested: Scalars['Int']['output'];
  noResponse: Scalars['Int']['output'];
  open: Scalars['Int']['output'];
  pending: Scalars['Int']['output'];
};

export type CorrectInspectionResponsibleEmailInput = {
  clientMutationId: Scalars['String']['input'];
  email: Scalars['String']['input'];
  emailConfirmation: Scalars['String']['input'];
  expectedResponsibilityVersion: Scalars['Int']['input'];
  inspectionId: Scalars['ID']['input'];
};

export type CorrectOnboardingResponsibleEmailInput = {
  clientMutationId: Scalars['String']['input'];
  email: Scalars['String']['input'];
  emailConfirmation: Scalars['String']['input'];
  expectedResponsibilityVersion: Scalars['Int']['input'];
};

export type CounterpartyBaseline = {
  __typename?: 'CounterpartyBaseline';
  adjustmentStatus: Maybe<Scalars['String']['output']>;
  baseline: CounterpartyBaselineInventory;
  deadlineAt: Scalars['String']['output'];
  newerVersionExists: Scalars['Boolean']['output'];
  readOnly: Scalars['Boolean']['output'];
  resolution: Scalars['String']['output'];
  resolutionReason: Maybe<Scalars['String']['output']>;
  respondedAt: Maybe<Scalars['String']['output']>;
  response: Scalars['String']['output'];
};

export type CounterpartyBaselineEnvironment = {
  __typename?: 'CounterpartyBaselineEnvironment';
  items: Array<CounterpartyBaselineItem>;
  name: Scalars['String']['output'];
  typeCode: Scalars['String']['output'];
};

export type CounterpartyBaselineEvidence = {
  __typename?: 'CounterpartyBaselineEvidence';
  kind: Scalars['String']['output'];
  position: Scalars['Int']['output'];
};

export type CounterpartyBaselineInventory = {
  __typename?: 'CounterpartyBaselineInventory';
  environments: Array<CounterpartyBaselineEnvironment>;
  hasInventory: Scalars['Boolean']['output'];
};

export type CounterpartyBaselineItem = {
  __typename?: 'CounterpartyBaselineItem';
  color: Maybe<Scalars['String']['output']>;
  condition: Scalars['String']['output'];
  evidence: Array<CounterpartyBaselineEvidence>;
  limitation: Maybe<Scalars['String']['output']>;
  material: Maybe<Scalars['String']['output']>;
  name: Scalars['String']['output'];
  observation: Scalars['String']['output'];
  quantity: Maybe<Scalars['Int']['output']>;
  typeCode: Scalars['String']['output'];
};

export type CounterpartyInvitationPayload = {
  __typename?: 'CounterpartyInvitationPayload';
  adjustmentVersionId: Maybe<Scalars['ID']['output']>;
  clientMutationId: Scalars['String']['output'];
  deadlineAt: Scalars['String']['output'];
  invitationId: Scalars['ID']['output'];
  resolution: Scalars['String']['output'];
  response: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type CounterpartyResponseInput = {
  clientMutationId: Scalars['String']['input'];
};

export type CounterpartyResponsePayload = {
  __typename?: 'CounterpartyResponsePayload';
  clientMutationId: Scalars['String']['output'];
  respondedAt: Maybe<Scalars['String']['output']>;
  response: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type CreateBusinessUnitInput = {
  clientMutationId: Scalars['String']['input'];
  code: Scalars['String']['input'];
  expectedTenantVersion: Scalars['Int']['input'];
  name: Scalars['String']['input'];
};

export type CreateInspectionInput = {
  assetId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  comparisonMode: CaptureComparisonMode;
  deadlineAt: Scalars['String']['input'];
  dueAt: Scalars['String']['input'];
  participantId: Scalars['ID']['input'];
  projectId: InputMaybe<Scalars['ID']['input']>;
  reason: Scalars['String']['input'];
  referenceVersionId: InputMaybe<Scalars['ID']['input']>;
  reminderInstants: Array<Scalars['String']['input']>;
  reviewerMembershipId: InputMaybe<Scalars['ID']['input']>;
  stageId: InputMaybe<Scalars['ID']['input']>;
  templateId: InputMaybe<Scalars['ID']['input']>;
};

export type CreateMediaUploadInput = {
  attemptId: InputMaybe<Scalars['ID']['input']>;
  clientMutationId: Scalars['String']['input'];
  contentType: Scalars['String']['input'];
  requirementKey: InputMaybe<Scalars['String']['input']>;
  sha256: Scalars['String']['input'];
  sizeBytes: Scalars['Int']['input'];
};

export type CreateProjectInput = {
  assetId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  name: Scalars['String']['input'];
  orderedStages: InputMaybe<Scalars['Boolean']['input']>;
  stages: Array<PlannedProjectStageInput>;
};

export type CreateScheduleInput = {
  assetId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  comparisonMode: CaptureComparisonMode;
  deadlineMinutes: Scalars['Int']['input'];
  participantId: Scalars['ID']['input'];
  projectId: InputMaybe<Scalars['ID']['input']>;
  referenceVersionId: InputMaybe<Scalars['ID']['input']>;
  reminderOffsetsMinutes: Array<Scalars['Int']['input']>;
  rrule: Scalars['String']['input'];
  stageId: InputMaybe<Scalars['ID']['input']>;
  startsAt: Scalars['String']['input'];
  templateId: Scalars['ID']['input'];
  timezone: Scalars['String']['input'];
};

export type CreateTenantInput = {
  businessUnitCode: Scalars['String']['input'];
  businessUnitName: Scalars['String']['input'];
  clientMutationId: Scalars['String']['input'];
  language: InputMaybe<Scalars['String']['input']>;
  name: Scalars['String']['input'];
  timezone: InputMaybe<Scalars['String']['input']>;
};

export type CreateTenantPayload = {
  __typename?: 'CreateTenantPayload';
  clientMutationId: Scalars['String']['output'];
  tenant: Maybe<Tenant>;
  userErrors: Array<UserError>;
};

export type CustomerEvidenceConnection = {
  __typename?: 'CustomerEvidenceConnection';
  nodes: Array<CustomerEvidenceItem>;
  pageInfo: PageInfo;
};

export type CustomerEvidenceItem = {
  __typename?: 'CustomerEvidenceItem';
  captureSource: Maybe<Scalars['String']['output']>;
  capturedAt: Maybe<Scalars['String']['output']>;
  component: Maybe<Scalars['String']['output']>;
  description: Maybe<Scalars['String']['output']>;
  evidenceKind: Maybe<Scalars['String']['output']>;
  flags: Array<Scalars['String']['output']>;
  frameId: Maybe<Scalars['ID']['output']>;
  id: Scalars['ID']['output'];
  label: Maybe<Scalars['String']['output']>;
  lineageId: Scalars['ID']['output'];
  mediaAvailability: Scalars['String']['output'];
  planId: Maybe<Scalars['ID']['output']>;
  preparationId: Maybe<Scalars['ID']['output']>;
  ptsMicros: Maybe<Scalars['String']['output']>;
  replacedBy: Maybe<Scalars['ID']['output']>;
  requirementKey: Scalars['String']['output'];
  role: Scalars['String']['output'];
  section: Maybe<Scalars['String']['output']>;
  sourceAvailability: Maybe<Scalars['String']['output']>;
  sourceMediaId: Maybe<Scalars['ID']['output']>;
  state: Scalars['String']['output'];
  timestampMs: Maybe<Scalars['Int']['output']>;
  url: Maybe<Scalars['String']['output']>;
};

export type CustomerInventory = {
  __typename?: 'CustomerInventory';
  coverage: Maybe<CustomerInventoryCoverage>;
  informational: Array<CustomerInventoryEntry>;
  problems: Array<CustomerInventoryEntry>;
  purpose: Scalars['String']['output'];
  referenceVersionNumber: Scalars['Int']['output'];
};

export type CustomerInventoryCoverage = {
  __typename?: 'CustomerInventoryCoverage';
  assessedItems: Scalars['Int']['output'];
  environmentsAddedDuringCapture: Array<Scalars['String']['output']>;
  environmentsNotCaptured: Array<Scalars['String']['output']>;
  expectedItems: Scalars['Int']['output'];
  notObserved: Scalars['Int']['output'];
};

export type CustomerInventoryEntry = {
  __typename?: 'CustomerInventoryEntry';
  conditionAfter: Maybe<Scalars['String']['output']>;
  conditionBefore: Maybe<Scalars['String']['output']>;
  environment: Scalars['String']['output'];
  name: Scalars['String']['output'];
  pairs: Array<CustomerInventoryPair>;
  reason: Maybe<Scalars['String']['output']>;
  status: Scalars['String']['output'];
  type: Scalars['String']['output'];
};

export type CustomerInventoryPair = {
  __typename?: 'CustomerInventoryPair';
  currentEvidenceId: Scalars['ID']['output'];
  currentSourceAvailability: Scalars['String']['output'];
  originEvidenceId: Scalars['ID']['output'];
  originSourceAvailability: Scalars['String']['output'];
  timestampMs: Maybe<Scalars['Int']['output']>;
};

export type CustomerPortfolioConnection = {
  __typename?: 'CustomerPortfolioConnection';
  nodes: Array<CustomerPortfolioItem>;
  pageInfo: PageInfo;
};

export type CustomerPortfolioFilter = {
  assetId: InputMaybe<Scalars['ID']['input']>;
  projectId: InputMaybe<Scalars['ID']['input']>;
  search: InputMaybe<Scalars['String']['input']>;
};

export type CustomerPortfolioItem = {
  __typename?: 'CustomerPortfolioItem';
  assetId: Scalars['ID']['output'];
  progress: Scalars['Int']['output'];
  projectId: Maybe<Scalars['ID']['output']>;
  publishedClassification: Maybe<Scalars['String']['output']>;
  status: Scalars['String']['output'];
  updatedAt: Scalars['String']['output'];
};

export type CustomerReport = {
  __typename?: 'CustomerReport';
  advisory: Scalars['String']['output'];
  classification: Scalars['String']['output'];
  context: ReportContext;
  historical: Scalars['Boolean']['output'];
  inspectionId: Scalars['ID']['output'];
  inventory: Maybe<CustomerInventory>;
  requirements: Array<ReportRequirement>;
  snapshotId: Scalars['ID']['output'];
  status: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type CustomerTimelineConnection = {
  __typename?: 'CustomerTimelineConnection';
  nodes: Array<CustomerTimelineEntry>;
  pageInfo: PageInfo;
};

export type CustomerTimelineEntry = {
  __typename?: 'CustomerTimelineEntry';
  historical: Scalars['Boolean']['output'];
  id: Scalars['ID']['output'];
  kind: Scalars['String']['output'];
  occurredAt: Scalars['String']['output'];
  status: Scalars['String']['output'];
};

export type DashboardSummary = {
  __typename?: 'DashboardSummary';
  attention: Scalars['Int']['output'];
  critical: Scalars['Int']['output'];
  invalidated: Scalars['Int']['output'];
  normal: Scalars['Int']['output'];
  pending: Scalars['Int']['output'];
  total: Scalars['Int']['output'];
};

export type DecideInventoryItemsInput = {
  clientMutationId: Scalars['String']['input'];
  decisions: Array<InventoryDecisionInput>;
  originVersionId: Scalars['ID']['input'];
};

export type DeclareCaptureEnvironmentInput = {
  clientMutationId: Scalars['String']['input'];
  name: Scalars['String']['input'];
  typeCode: Scalars['String']['input'];
};

export type DeclareCaptureImpossibilityInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: InputMaybe<Scalars['Int']['input']>;
  reason: InputMaybe<Scalars['String']['input']>;
  reasonCode: InputMaybe<Scalars['String']['input']>;
  reasonText: InputMaybe<Scalars['String']['input']>;
  requirementKey: Scalars['String']['input'];
};

export type DisableMembershipInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  membershipId: Scalars['ID']['input'];
};

export type DiscardBaselineDraftInput = {
  clientMutationId: Scalars['String']['input'];
  originVersionId: Scalars['ID']['input'];
};

export type EvidenceMode =
  | 'ADVANCED'
  | 'SIMPLE';

export type ExternalCapture = {
  __typename?: 'ExternalCapture';
  answers: Array<CaptureAnswer>;
  captureLayout: Maybe<Scalars['String']['output']>;
  confirmationOnly: Scalars['Boolean']['output'];
  disclosureVersion: Scalars['String']['output'];
  environments: Array<CaptureEnvironment>;
  kind: Scalars['String']['output'];
  mediaKind: Scalars['String']['output'];
  policy: Scalars['JSON']['output'];
  recaptureDeadlineAt: Maybe<Scalars['String']['output']>;
  recaptureRequestId: Maybe<Scalars['ID']['output']>;
  reference: Scalars['JSON']['output'];
  referenceItems: Array<CaptureReferenceItem>;
  replacementDeadlineAt: Maybe<Scalars['String']['output']>;
  requirements: Array<CaptureRequirement>;
  responsibilityId: Scalars['ID']['output'];
  reviewHandoff: Maybe<CaptureReviewHandoff>;
  status: Scalars['String']['output'];
  templateVersionId: Scalars['ID']['output'];
};

export type ExternalSessionPayload = {
  __typename?: 'ExternalSessionPayload';
  clientMutationId: Scalars['String']['output'];
  csrfToken: Scalars['String']['output'];
  expiresAt: Scalars['String']['output'];
  status: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type GlobalLlmCallUsage = {
  __typename?: 'GlobalLLMCallUsage';
  attempt: Scalars['Int']['output'];
  cachedInputTokens: Maybe<Scalars['Int']['output']>;
  callId: Scalars['ID']['output'];
  comparisonMode: Scalars['String']['output'];
  correlationId: Scalars['String']['output'];
  durationMs: Maybe<Scalars['Int']['output']>;
  eventId: Scalars['ID']['output'];
  executionId: Scalars['ID']['output'];
  finishedAt: Maybe<Scalars['String']['output']>;
  gatewayRequestId: Maybe<Scalars['String']['output']>;
  httpStatus: Maybe<Scalars['Int']['output']>;
  imageCount: Maybe<Scalars['Int']['output']>;
  inputTokens: Maybe<Scalars['Int']['output']>;
  inspectionId: Scalars['ID']['output'];
  jobId: Scalars['ID']['output'];
  mode: LlmExecutionMode;
  model: Maybe<Scalars['String']['output']>;
  modelAlias: Scalars['String']['output'];
  outputTokens: Maybe<Scalars['Int']['output']>;
  provider: Maybe<Scalars['String']['output']>;
  replayGeneration: Scalars['Int']['output'];
  reportedCost: Maybe<Scalars['Float']['output']>;
  requestBodyBytes: Maybe<Scalars['Int']['output']>;
  startedAt: Scalars['String']['output'];
  state: LlmCallState;
  technicalOutcome: Scalars['String']['output'];
  tenantId: Scalars['ID']['output'];
  tenantName: Scalars['String']['output'];
  transportDelivered: Maybe<Scalars['Boolean']['output']>;
};

export type Inspection = {
  __typename?: 'Inspection';
  analysisPromptSnapshotId: Scalars['ID']['output'];
  assetAddress: Maybe<Scalars['String']['output']>;
  assetExternalKey: Maybe<Scalars['String']['output']>;
  assetId: Scalars['ID']['output'];
  assetName: Maybe<Scalars['String']['output']>;
  attention: Maybe<InspectionAttention>;
  businessUnitId: Scalars['ID']['output'];
  comparisonMode: Maybe<Scalars['String']['output']>;
  deadlineAt: Scalars['String']['output'];
  dueAt: Scalars['String']['output'];
  evidenceCount: Scalars['Int']['output'];
  id: Scalars['ID']['output'];
  mediaKind: Scalars['String']['output'];
  originPromotionFailure: Maybe<Scalars['String']['output']>;
  originPromotionStatus: Maybe<Scalars['String']['output']>;
  participantId: Scalars['ID']['output'];
  participantName: Maybe<Scalars['String']['output']>;
  projectId: Maybe<Scalars['ID']['output']>;
  promotedOriginIsActive: Maybe<Scalars['Boolean']['output']>;
  promotedOriginMediaKind: Maybe<Scalars['String']['output']>;
  promotedOriginVersionId: Maybe<Scalars['ID']['output']>;
  promotedOriginVersionNumber: Maybe<Scalars['Int']['output']>;
  referenceIsActive: Maybe<Scalars['Boolean']['output']>;
  referenceMediaKind: Maybe<Scalars['String']['output']>;
  referenceVersionId: Maybe<Scalars['ID']['output']>;
  referenceVersionNumber: Maybe<Scalars['Int']['output']>;
  referenceVersionStatus: Maybe<Scalars['String']['output']>;
  reminderInstants: Array<Scalars['String']['output']>;
  source: Scalars['String']['output'];
  sourceReason: Maybe<Scalars['String']['output']>;
  stageId: Maybe<Scalars['ID']['output']>;
  stateReason: Maybe<Scalars['String']['output']>;
  status: Scalars['String']['output'];
  templateId: Scalars['ID']['output'];
  templateVersionId: Scalars['ID']['output'];
  version: Scalars['Int']['output'];
};

export type InspectionAttention = {
  __typename?: 'InspectionAttention';
  kind: InspectionAttentionKind;
  mediaId: Maybe<Scalars['ID']['output']>;
  replacementDeadlineAt: Maybe<Scalars['String']['output']>;
  since: Scalars['String']['output'];
};

export type InspectionAttentionKind =
  | 'PREPARATION_FAILED'
  | 'PRIVACY_REVIEW'
  | 'REPLACEMENT_REQUESTED';

export type InspectionConnection = {
  __typename?: 'InspectionConnection';
  nodes: Array<Inspection>;
  pageInfo: PageInfo;
};

export type InspectionLlmUsage = {
  __typename?: 'InspectionLLMUsage';
  attemptedCalls: Scalars['Int']['output'];
  cacheHitCalls: Scalars['Int']['output'];
  cachedInputTokens: Scalars['Int']['output'];
  calls: Array<LlmCallUsage>;
  costComplete: Scalars['Boolean']['output'];
  coverageComplete: Scalars['Boolean']['output'];
  coverageStartedAt: Maybe<Scalars['String']['output']>;
  deliveredCalls: Scalars['Int']['output'];
  incompleteCalls: Scalars['Int']['output'];
  inputTokens: Scalars['Int']['output'];
  inspectionId: Scalars['ID']['output'];
  knownCacheCalls: Scalars['Int']['output'];
  knownReportedCost: Scalars['Float']['output'];
  mode: LlmExecutionMode;
  outputTokens: Scalars['Int']['output'];
  pageInfo: PageInfo;
  unknownCacheCalls: Scalars['Int']['output'];
  unknownCostCalls: Scalars['Int']['output'];
};

export type InspectionPayload = {
  __typename?: 'InspectionPayload';
  clientMutationId: Scalars['String']['output'];
  inspection: Maybe<Inspection>;
  userErrors: Array<UserError>;
};

export type InspectionStatusGroup =
  | 'ATTENTION'
  | 'CLOSED'
  | 'COMPLETED'
  | 'EXECUTION'
  | 'PLANNING';

export type InspectionTransitionInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  inspectionId: Scalars['ID']['input'];
};

export type InternalUserActivation = {
  __typename?: 'InternalUserActivation';
  email: Scalars['String']['output'];
  membershipId: Scalars['ID']['output'];
  name: Scalars['String']['output'];
  newIdentity: Scalars['Boolean']['output'];
  status: Scalars['String']['output'];
};

export type InternalUserActivationPayload = {
  __typename?: 'InternalUserActivationPayload';
  activation: Maybe<InternalUserActivation>;
  clientMutationId: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type InvalidateInspectionInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  inspectionId: Scalars['ID']['input'];
  reason: Scalars['String']['input'];
};

export type InvalidateReportPublicationInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  publicationId: Scalars['ID']['input'];
  reason: Scalars['String']['input'];
};

export type InventoryCounts = {
  __typename?: 'InventoryCounts';
  included: Scalars['Int']['output'];
  notReviewed: Scalars['Int']['output'];
  rejected: Scalars['Int']['output'];
};

export type InventoryDecisionInput = {
  action: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  itemId: Scalars['ID']['input'];
  patch: InputMaybe<InventoryItemPatchInput>;
  reason: InputMaybe<Scalars['String']['input']>;
  reasonText: InputMaybe<Scalars['String']['input']>;
};

export type InventoryDraft = {
  __typename?: 'InventoryDraft';
  environments: Array<InventoryEnvironment>;
  noticeCode: Maybe<Scalars['String']['output']>;
  progress: InventoryProgress;
  readOnly: Scalars['Boolean']['output'];
  version: OriginVersion;
  warnings: Array<Scalars['String']['output']>;
};

export type InventoryEnvironment = {
  __typename?: 'InventoryEnvironment';
  addedDuringCapture: Scalars['Boolean']['output'];
  decision: Scalars['String']['output'];
  expected: Scalars['Boolean']['output'];
  id: Scalars['ID']['output'];
  identityId: Scalars['ID']['output'];
  items: Array<InventoryItem>;
  name: Scalars['String']['output'];
  position: Scalars['Int']['output'];
  requirementKey: Maybe<Scalars['String']['output']>;
  typeCode: Scalars['String']['output'];
};

export type InventoryEnvironmentPayload = {
  __typename?: 'InventoryEnvironmentPayload';
  clientMutationId: Scalars['String']['output'];
  environment: Maybe<InventoryEnvironment>;
  userErrors: Array<UserError>;
};

export type InventoryEvidence = {
  __typename?: 'InventoryEvidence';
  availability: Scalars['String']['output'];
  frameId: Maybe<Scalars['ID']['output']>;
  id: Scalars['ID']['output'];
  kind: Scalars['String']['output'];
  mediaId: Scalars['ID']['output'];
  position: Scalars['Int']['output'];
  region: Maybe<Scalars['JSON']['output']>;
  retentionAvailability: Maybe<Scalars['String']['output']>;
};

export type InventoryEvidenceInput = {
  frameId: InputMaybe<Scalars['ID']['input']>;
  kind: Scalars['String']['input'];
  mediaId: Scalars['ID']['input'];
  region: InputMaybe<Scalars['JSON']['input']>;
};

export type InventoryItem = {
  __typename?: 'InventoryItem';
  color: Maybe<Scalars['String']['output']>;
  condition: Scalars['String']['output'];
  confidenceBps: Maybe<Scalars['Int']['output']>;
  decision: Scalars['String']['output'];
  declared: Scalars['Boolean']['output'];
  environmentId: Scalars['ID']['output'];
  evidence: Array<InventoryEvidence>;
  id: Scalars['ID']['output'];
  identityId: Scalars['ID']['output'];
  limitation: Maybe<Scalars['String']['output']>;
  material: Maybe<Scalars['String']['output']>;
  name: Scalars['String']['output'];
  observation: Scalars['String']['output'];
  origin: Scalars['String']['output'];
  quantity: Maybe<Scalars['Int']['output']>;
  rejectionReason: Maybe<Scalars['String']['output']>;
  rejectionText: Maybe<Scalars['String']['output']>;
  suggested: Maybe<Scalars['JSON']['output']>;
  typeCode: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type InventoryItemPatchInput = {
  color: InputMaybe<Scalars['String']['input']>;
  condition: InputMaybe<Scalars['String']['input']>;
  environmentId: InputMaybe<Scalars['ID']['input']>;
  limitation: InputMaybe<Scalars['String']['input']>;
  material: InputMaybe<Scalars['String']['input']>;
  name: InputMaybe<Scalars['String']['input']>;
  observation: InputMaybe<Scalars['String']['input']>;
  quantity: InputMaybe<Scalars['Int']['input']>;
  typeCode: InputMaybe<Scalars['String']['input']>;
};

export type InventoryItemPayload = {
  __typename?: 'InventoryItemPayload';
  clientMutationId: Scalars['String']['output'];
  item: Maybe<InventoryItem>;
  userErrors: Array<UserError>;
};

export type InventoryItemsPayload = {
  __typename?: 'InventoryItemsPayload';
  clientMutationId: Scalars['String']['output'];
  emptyEnvironments: Array<Scalars['ID']['output']>;
  items: Array<InventoryItem>;
  userErrors: Array<UserError>;
};

export type InventoryProgress = {
  __typename?: 'InventoryProgress';
  decided: Scalars['Int']['output'];
  pending: Scalars['Int']['output'];
};

export type InventorySplitPartInput = {
  condition: InputMaybe<Scalars['String']['input']>;
  evidenceIds: Array<Scalars['ID']['input']>;
  name: Scalars['String']['input'];
  typeCode: Scalars['String']['input'];
};

export type InventoryTrail = {
  __typename?: 'InventoryTrail';
  counterpartyEvents: Array<InventoryTrailEntry>;
  decisions: Array<InventoryTrailEntry>;
  suggestions: Array<InventoryTrailEntry>;
};

export type InventoryTrailEntry = {
  __typename?: 'InventoryTrailEntry';
  action: Scalars['String']['output'];
  actorMembershipId: Maybe<Scalars['ID']['output']>;
  after: Maybe<Scalars['JSON']['output']>;
  before: Maybe<Scalars['JSON']['output']>;
  id: Scalars['ID']['output'];
  occurredAt: Scalars['String']['output'];
  reason: Maybe<Scalars['String']['output']>;
  resolutionReason: Maybe<Scalars['String']['output']>;
  statement: Maybe<Scalars['String']['output']>;
  targetId: Scalars['ID']['output'];
  targetType: Scalars['String']['output'];
};

export type InvitationOtpPayload = {
  __typename?: 'InvitationOtpPayload';
  clientMutationId: Scalars['String']['output'];
  status: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type InvitationPayload = {
  __typename?: 'InvitationPayload';
  clientMutationId: Scalars['String']['output'];
  status: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type InviteInternalUserInput = {
  clientMutationId: Scalars['String']['input'];
  email: Scalars['String']['input'];
  name: Scalars['String']['input'];
  role: Scalars['String']['input'];
  scopes: Array<ScopeAssignmentInput>;
};

export type InviteOriginCaptureInput = {
  assetId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  expiresAt: Scalars['String']['input'];
  participantId: Scalars['ID']['input'];
  reviewerMembershipId: InputMaybe<Scalars['ID']['input']>;
};

export type LlmCallState =
  | 'FINISHED'
  | 'STARTED';

export type LlmCallUsage = {
  __typename?: 'LLMCallUsage';
  attempt: Scalars['Int']['output'];
  cachedInputTokens: Maybe<Scalars['Int']['output']>;
  callId: Scalars['ID']['output'];
  comparisonMode: Scalars['String']['output'];
  correlationId: Scalars['String']['output'];
  durationMs: Maybe<Scalars['Int']['output']>;
  eventId: Scalars['ID']['output'];
  executionId: Scalars['ID']['output'];
  finishedAt: Maybe<Scalars['String']['output']>;
  gatewayRequestId: Maybe<Scalars['String']['output']>;
  httpStatus: Maybe<Scalars['Int']['output']>;
  imageCount: Maybe<Scalars['Int']['output']>;
  inputTokens: Maybe<Scalars['Int']['output']>;
  jobId: Scalars['ID']['output'];
  mode: LlmExecutionMode;
  model: Maybe<Scalars['String']['output']>;
  modelAlias: Scalars['String']['output'];
  outputTokens: Maybe<Scalars['Int']['output']>;
  provider: Maybe<Scalars['String']['output']>;
  replayGeneration: Scalars['Int']['output'];
  reportedCost: Maybe<Scalars['Float']['output']>;
  requestBodyBytes: Maybe<Scalars['Int']['output']>;
  startedAt: Scalars['String']['output'];
  state: LlmCallState;
  technicalOutcome: Scalars['String']['output'];
  transportDelivered: Maybe<Scalars['Boolean']['output']>;
};

export type LlmExecutionMode =
  | 'LIVE'
  | 'MOCK';

export type LlmUsage = {
  __typename?: 'LLMUsage';
  attemptedCalls: Scalars['Int']['output'];
  cacheHitCalls: Scalars['Int']['output'];
  cachedInputTokens: Scalars['Int']['output'];
  calls: Array<GlobalLlmCallUsage>;
  costComplete: Scalars['Boolean']['output'];
  coverageComplete: Scalars['Boolean']['output'];
  coverageStartedAt: Maybe<Scalars['String']['output']>;
  deliveredCalls: Scalars['Int']['output'];
  from: Scalars['String']['output'];
  incompleteCalls: Scalars['Int']['output'];
  inputTokens: Scalars['Int']['output'];
  knownCacheCalls: Scalars['Int']['output'];
  knownReportedCost: Scalars['Float']['output'];
  outputTokens: Scalars['Int']['output'];
  pageInfo: PageInfo;
  to: Scalars['String']['output'];
  unknownCacheCalls: Scalars['Int']['output'];
  unknownCostCalls: Scalars['Int']['output'];
};

export type LlmUsageCostState =
  | 'INFORMED'
  | 'MISSING';

export type LlmUsageFilter = {
  cost: InputMaybe<LlmUsageCostState>;
  from: InputMaybe<Scalars['String']['input']>;
  inspectionId: InputMaybe<Scalars['ID']['input']>;
  mode: InputMaybe<LlmExecutionMode>;
  model: InputMaybe<Scalars['String']['input']>;
  modelAlias: InputMaybe<Scalars['String']['input']>;
  provider: InputMaybe<Scalars['String']['input']>;
  state: InputMaybe<LlmCallState>;
  technicalOutcome: InputMaybe<Scalars['String']['input']>;
  tenantId: InputMaybe<Scalars['ID']['input']>;
  to: InputMaybe<Scalars['String']['input']>;
};

export type LegalHoldInput = {
  clientMutationId: Scalars['String']['input'];
  inspectionId: InputMaybe<Scalars['ID']['input']>;
  originVersionId: InputMaybe<Scalars['ID']['input']>;
  reason: Scalars['String']['input'];
  sourceMediaId: InputMaybe<Scalars['ID']['input']>;
};

export type MarkNotificationReadInput = {
  all: InputMaybe<Scalars['Boolean']['input']>;
  clientMutationId: Scalars['String']['input'];
  kind: InputMaybe<Scalars['String']['input']>;
  notificationId: InputMaybe<Scalars['ID']['input']>;
  projectId: InputMaybe<Scalars['ID']['input']>;
  through: InputMaybe<Scalars['String']['input']>;
};

export type Me = {
  __typename?: 'Me';
  audience: Scalars['String']['output'];
  canViewLLMCosts: Scalars['Boolean']['output'];
  effectiveScopes: Array<Scope>;
  identityId: Scalars['ID']['output'];
  memberships: Array<Membership>;
  product: Scalars['String']['output'];
  productEntitlements: Array<Scalars['String']['output']>;
  roles: Array<Scalars['String']['output']>;
  tenantId: Scalars['ID']['output'];
};

export type Media = {
  __typename?: 'Media';
  captureSource: Maybe<Scalars['String']['output']>;
  description: Maybe<Scalars['String']['output']>;
  flags: Array<Scalars['String']['output']>;
  id: Scalars['ID']['output'];
  mediaKind: Scalars['String']['output'];
  preparationStatus: Maybe<Scalars['String']['output']>;
  replacesMediaId: Maybe<Scalars['ID']['output']>;
  requirementKey: Maybe<Scalars['String']['output']>;
  status: Scalars['String']['output'];
};

export type MediaPayload = {
  __typename?: 'MediaPayload';
  clientMutationId: Scalars['String']['output'];
  media: Maybe<Media>;
  userErrors: Array<UserError>;
};

export type MediaUpload = {
  __typename?: 'MediaUpload';
  expiresAt: Scalars['String']['output'];
  mediaId: Scalars['ID']['output'];
  partSizeBytes: Scalars['Int']['output'];
  uploadId: Scalars['String']['output'];
};

export type MediaUploadPayload = {
  __typename?: 'MediaUploadPayload';
  clientMutationId: Scalars['String']['output'];
  upload: Maybe<MediaUpload>;
  userErrors: Array<UserError>;
};

export type Membership = {
  __typename?: 'Membership';
  email: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  invitationStatus: Scalars['String']['output'];
  name: Scalars['String']['output'];
  role: Scalars['String']['output'];
  scopes: Array<Scope>;
  status: Scalars['String']['output'];
  tenantId: Scalars['ID']['output'];
  version: Scalars['Int']['output'];
};

export type MembershipConnection = {
  __typename?: 'MembershipConnection';
  nodes: Array<Membership>;
  pageInfo: PageInfo;
};

export type MembershipPayload = {
  __typename?: 'MembershipPayload';
  clientMutationId: Scalars['String']['output'];
  membership: Maybe<Membership>;
  userErrors: Array<UserError>;
};

export type MergeInventoryItemsInput = {
  clientMutationId: Scalars['String']['input'];
  itemIds: Array<Scalars['ID']['input']>;
  originVersionId: Scalars['ID']['input'];
  resultEnvironmentId: InputMaybe<Scalars['ID']['input']>;
};

export type Mutation = {
  __typename?: 'Mutation';
  acceptProcessing: CapturePayload;
  acknowledgeBaseline: CounterpartyResponsePayload;
  activateBaselineVersion: OriginVersionPayload;
  activateOriginVersion: OriginVersionPayload;
  activateSegmentDefinition: SegmentDefinitionPayload;
  activateTemplateVersion: TemplatePayload;
  addExceptionalStage: ProjectPayload;
  addInventoryItem: InventoryItemPayload;
  applyLegalHold: RetentionMutationPayload;
  archiveAsset: AssetPayload;
  archiveBusinessUnit: BusinessUnitPayload;
  assignRoleScopes: MembershipPayload;
  beginVideoCapture: VideoAttemptPayload;
  bulkAcceptInventoryEnvironment: BulkAcceptInventoryEnvironmentPayload;
  cancelInspection: InspectionPayload;
  cancelSchedule: SchedulePayload;
  closeOriginInvitation: OriginVersionPayload;
  closeProject: ProjectPayload;
  completeInternalUserActivation: InternalUserActivationPayload;
  completeMediaUpload: MediaPayload;
  completeOnboarding: OnboardingPayload;
  configureMyNotificationPreferences: NotificationPreferencesPayload;
  configurePublicationPolicy: PublicationPolicyPayload;
  configureRetentionPolicy: RetentionPolicyPayload;
  configureVideoAdmission: VideoAdmissionPayload;
  configureVideoPolicy: VideoPolicyPayload;
  contestBaseline: CounterpartyResponsePayload;
  correctInspectionResponsibleEmail: ResponsibleEmailCorrectionPayload;
  correctOnboardingResponsibleEmail: OnboardingPayload;
  createBusinessUnit: BusinessUnitPayload;
  createInspection: InspectionPayload;
  createMediaUpload: MediaUploadPayload;
  createProject: ProjectPayload;
  createSchedule: SchedulePayload;
  createTenant: CreateTenantPayload;
  decideInventoryItems: InventoryItemsPayload;
  declareCaptureEnvironment: CaptureEnvironmentPayload;
  declareCaptureImpossibility: CapturePayload;
  declareSensitiveDetectionFalsePositive: MediaPayload;
  disableMembership: MembershipPayload;
  discardBaselineDraft: OriginVersionPayload;
  invalidateInspection: InspectionPayload;
  invalidateOriginVersion: OriginVersionPayload;
  invalidateReportPublication: ReportPublicationPayload;
  inviteInternalUser: MembershipPayload;
  inviteOriginCapture: OriginInvitationPayload;
  markNotificationRead: RecipientNotificationPayload;
  mergeInventoryItems: InventoryItemPayload;
  planInspection: PlanInspectionPayload;
  presignMediaParts: PresignedPartsPayload;
  promoteInspectionPhotos: OriginPromotionPayload;
  promoteInspectionVideo: OriginVersionPayload;
  publishReport: ReportPublicationPayload;
  publishSegmentDefinition: SegmentDefinitionPayload;
  publishTemplateVersion: TemplatePayload;
  reanalyzeVideoInspection: ReanalyzeVideoInspectionPayload;
  recordCaptureQualityWarning: CaptureQualityWarningPayload;
  recordDeletionRequest: RetentionMutationPayload;
  registerAsset: AssetPayload;
  releaseLegalHold: RetentionMutationPayload;
  removeInventoryEnvironment: InventoryEnvironmentPayload;
  reopenProject: ProjectPayload;
  reprocessVideoPreparation: VideoPreparationPayload;
  requestAdminActivationOtp: OnboardingPayload;
  requestCounterpartyOtp: InvitationOtpPayload;
  requestInternalUserActivationOtp: InternalUserActivationPayload;
  requestInvitationOtp: InvitationOtpPayload;
  requestOnboardingOtp: OnboardingPayload;
  requestOriginCataloging: OriginVersionPayload;
  requestRecapture: RecapturePayload;
  requestVideoReplacement: VideoReplacementPayload;
  resendCounterpartyInvitation: CounterpartyInvitationPayload;
  resendInternalUserInvitation: MembershipPayload;
  resolveBaselineContestation: CounterpartyInvitationPayload;
  retryInventoryDraft: OriginVersionPayload;
  retryVideoPreparation: VideoPreparationPayload;
  revokeInvitation: InvitationPayload;
  saveCaptureMetadata: MediaPayload;
  saveOnboardingStep: OnboardingPayload;
  setAdminInitialPassword: OnboardingPayload;
  setDeliveryChannels: ParticipantPayload;
  skipProjectStage: ProjectPayload;
  splitInventoryItem: InventoryItemsPayload;
  startBaselineDraftFromInspection: OriginVersionPayload;
  startProjectStage: ProjectPayload;
  submitCapture: SubmissionPayload;
  submitRecapture: RecapturePayload;
  updateAnalysisPrompt: AnalysisPromptPayload;
  updateAsset: AssetPayload;
  updateBaselinePolicy: BaselinePolicyPayload;
  updateSchedule: SchedulePayload;
  updateTenant: TenantPayload;
  updateTriageCase: TriageCasePayload;
  upsertBusinessUnit: BusinessUnitPayload;
  upsertInventoryEnvironment: InventoryEnvironmentPayload;
  upsertParticipant: ParticipantPayload;
  verifyAdminActivationOtp: OnboardingPayload;
  verifyContact: ParticipantContactPayload;
  verifyCounterpartyOtp: ExternalSessionPayload;
  verifyInternalUserActivationOtp: InternalUserActivationPayload;
  verifyInvitationOtp: ExternalSessionPayload;
  verifyOnboardingOtp: OnboardingPayload;
};


export type MutationAcceptProcessingArgs = {
  input: AcceptProcessingInput;
};


export type MutationAcknowledgeBaselineArgs = {
  input: CounterpartyResponseInput;
};


export type MutationActivateBaselineVersionArgs = {
  input: ActivateBaselineVersionInput;
};


export type MutationActivateOriginVersionArgs = {
  input: OriginVersionInput;
};


export type MutationActivateSegmentDefinitionArgs = {
  input: ActivateSegmentDefinitionInput;
};


export type MutationActivateTemplateVersionArgs = {
  input: ActivateTemplateVersionInput;
};


export type MutationAddExceptionalStageArgs = {
  input: AddExceptionalStageInput;
};


export type MutationAddInventoryItemArgs = {
  input: AddInventoryItemInput;
};


export type MutationApplyLegalHoldArgs = {
  input: LegalHoldInput;
};


export type MutationArchiveAssetArgs = {
  input: ArchiveAssetInput;
};


export type MutationArchiveBusinessUnitArgs = {
  input: ArchiveBusinessUnitInput;
};


export type MutationAssignRoleScopesArgs = {
  input: AssignRoleScopesInput;
};


export type MutationBeginVideoCaptureArgs = {
  input: BeginVideoCaptureInput;
};


export type MutationBulkAcceptInventoryEnvironmentArgs = {
  input: BulkAcceptInventoryEnvironmentInput;
};


export type MutationCancelInspectionArgs = {
  input: InspectionTransitionInput;
};


export type MutationCancelScheduleArgs = {
  input: CancelScheduleInput;
};


export type MutationCloseOriginInvitationArgs = {
  input: CloseOriginInvitationInput;
};


export type MutationCloseProjectArgs = {
  input: ProjectTransitionInput;
};


export type MutationCompleteInternalUserActivationArgs = {
  input: CompleteInternalUserActivationInput;
};


export type MutationCompleteMediaUploadArgs = {
  input: CompleteMediaUploadInput;
};


export type MutationCompleteOnboardingArgs = {
  input: CompleteOnboardingInput;
};


export type MutationConfigureMyNotificationPreferencesArgs = {
  input: ConfigureNotificationPreferencesInput;
};


export type MutationConfigurePublicationPolicyArgs = {
  input: ConfigurePublicationPolicyInput;
};


export type MutationConfigureRetentionPolicyArgs = {
  input: ConfigureRetentionPolicyInput;
};


export type MutationConfigureVideoAdmissionArgs = {
  input: ConfigureVideoAdmissionInput;
};


export type MutationConfigureVideoPolicyArgs = {
  input: ConfigureVideoPolicyInput;
};


export type MutationContestBaselineArgs = {
  input: ContestBaselineInput;
};


export type MutationCorrectInspectionResponsibleEmailArgs = {
  input: CorrectInspectionResponsibleEmailInput;
};


export type MutationCorrectOnboardingResponsibleEmailArgs = {
  input: CorrectOnboardingResponsibleEmailInput;
};


export type MutationCreateBusinessUnitArgs = {
  input: CreateBusinessUnitInput;
};


export type MutationCreateInspectionArgs = {
  input: CreateInspectionInput;
};


export type MutationCreateMediaUploadArgs = {
  input: CreateMediaUploadInput;
};


export type MutationCreateProjectArgs = {
  input: CreateProjectInput;
};


export type MutationCreateScheduleArgs = {
  input: CreateScheduleInput;
};


export type MutationCreateTenantArgs = {
  input: CreateTenantInput;
};


export type MutationDecideInventoryItemsArgs = {
  input: DecideInventoryItemsInput;
};


export type MutationDeclareCaptureEnvironmentArgs = {
  input: DeclareCaptureEnvironmentInput;
};


export type MutationDeclareCaptureImpossibilityArgs = {
  input: DeclareCaptureImpossibilityInput;
};


export type MutationDeclareSensitiveDetectionFalsePositiveArgs = {
  input: SensitiveFalsePositiveInput;
};


export type MutationDisableMembershipArgs = {
  input: DisableMembershipInput;
};


export type MutationDiscardBaselineDraftArgs = {
  input: DiscardBaselineDraftInput;
};


export type MutationInvalidateInspectionArgs = {
  input: InvalidateInspectionInput;
};


export type MutationInvalidateOriginVersionArgs = {
  input: OriginVersionInput;
};


export type MutationInvalidateReportPublicationArgs = {
  input: InvalidateReportPublicationInput;
};


export type MutationInviteInternalUserArgs = {
  input: InviteInternalUserInput;
};


export type MutationInviteOriginCaptureArgs = {
  input: InviteOriginCaptureInput;
};


export type MutationMarkNotificationReadArgs = {
  input: MarkNotificationReadInput;
};


export type MutationMergeInventoryItemsArgs = {
  input: MergeInventoryItemsInput;
};


export type MutationPlanInspectionArgs = {
  input: PlanInspectionInput;
};


export type MutationPresignMediaPartsArgs = {
  input: PresignMediaPartsInput;
};


export type MutationPromoteInspectionPhotosArgs = {
  input: PromoteInspectionPhotosInput;
};


export type MutationPromoteInspectionVideoArgs = {
  input: PromoteInspectionVideoInput;
};


export type MutationPublishReportArgs = {
  input: PublishReportInput;
};


export type MutationPublishSegmentDefinitionArgs = {
  input: PublishSegmentDefinitionInput;
};


export type MutationPublishTemplateVersionArgs = {
  input: PublishTemplateVersionInput;
};


export type MutationReanalyzeVideoInspectionArgs = {
  input: ReanalyzeVideoInspectionInput;
};


export type MutationRecordCaptureQualityWarningArgs = {
  input: RecordCaptureQualityWarningInput;
};


export type MutationRecordDeletionRequestArgs = {
  input: RecordDeletionRequestInput;
};


export type MutationRegisterAssetArgs = {
  input: RegisterAssetInput;
};


export type MutationReleaseLegalHoldArgs = {
  input: LegalHoldInput;
};


export type MutationRemoveInventoryEnvironmentArgs = {
  input: RemoveInventoryEnvironmentInput;
};


export type MutationReopenProjectArgs = {
  input: ReopenProjectInput;
};


export type MutationReprocessVideoPreparationArgs = {
  input: ReprocessVideoPreparationInput;
};


export type MutationRequestAdminActivationOtpArgs = {
  input: RequestAdminActivationOtpInput;
};


export type MutationRequestCounterpartyOtpArgs = {
  input: RequestCounterpartyOtpInput;
};


export type MutationRequestInternalUserActivationOtpArgs = {
  input: RequestInternalUserActivationOtpInput;
};


export type MutationRequestInvitationOtpArgs = {
  input: RequestInvitationOtpInput;
};


export type MutationRequestOnboardingOtpArgs = {
  input: RequestOnboardingOtpInput;
};


export type MutationRequestOriginCatalogingArgs = {
  input: RequestOriginCatalogingInput;
};


export type MutationRequestRecaptureArgs = {
  input: RequestRecaptureInput;
};


export type MutationRequestVideoReplacementArgs = {
  input: RequestVideoReplacementInput;
};


export type MutationResendCounterpartyInvitationArgs = {
  input: ResendCounterpartyInvitationInput;
};


export type MutationResendInternalUserInvitationArgs = {
  input: ResendInternalUserInvitationInput;
};


export type MutationResolveBaselineContestationArgs = {
  input: ResolveBaselineContestationInput;
};


export type MutationRetryInventoryDraftArgs = {
  input: RetryInventoryDraftInput;
};


export type MutationRetryVideoPreparationArgs = {
  input: RetryVideoPreparationInput;
};


export type MutationRevokeInvitationArgs = {
  input: RevokeInvitationInput;
};


export type MutationSaveCaptureMetadataArgs = {
  input: SaveCaptureMetadataInput;
};


export type MutationSaveOnboardingStepArgs = {
  input: OnboardingStepInput;
};


export type MutationSetAdminInitialPasswordArgs = {
  input: SetAdminInitialPasswordInput;
};


export type MutationSetDeliveryChannelsArgs = {
  input: SetDeliveryChannelsInput;
};


export type MutationSkipProjectStageArgs = {
  input: SkipProjectStageInput;
};


export type MutationSplitInventoryItemArgs = {
  input: SplitInventoryItemInput;
};


export type MutationStartBaselineDraftFromInspectionArgs = {
  input: StartBaselineDraftFromInspectionInput;
};


export type MutationStartProjectStageArgs = {
  input: StartProjectStageInput;
};


export type MutationSubmitCaptureArgs = {
  input: SubmitCaptureInput;
};


export type MutationSubmitRecaptureArgs = {
  input: SubmitRecaptureInput;
};


export type MutationUpdateAnalysisPromptArgs = {
  input: UpdateAnalysisPromptInput;
};


export type MutationUpdateAssetArgs = {
  input: UpdateAssetInput;
};


export type MutationUpdateBaselinePolicyArgs = {
  input: UpdateBaselinePolicyInput;
};


export type MutationUpdateScheduleArgs = {
  input: UpdateScheduleInput;
};


export type MutationUpdateTenantArgs = {
  input: UpdateTenantInput;
};


export type MutationUpdateTriageCaseArgs = {
  input: UpdateTriageCaseInput;
};


export type MutationUpsertBusinessUnitArgs = {
  input: UpsertBusinessUnitInput;
};


export type MutationUpsertInventoryEnvironmentArgs = {
  input: UpsertInventoryEnvironmentInput;
};


export type MutationUpsertParticipantArgs = {
  input: UpsertParticipantInput;
};


export type MutationVerifyAdminActivationOtpArgs = {
  input: VerifyAdminActivationOtpInput;
};


export type MutationVerifyContactArgs = {
  input: VerifyContactInput;
};


export type MutationVerifyCounterpartyOtpArgs = {
  input: VerifyCounterpartyOtpInput;
};


export type MutationVerifyInternalUserActivationOtpArgs = {
  input: VerifyInternalUserActivationOtpInput;
};


export type MutationVerifyInvitationOtpArgs = {
  input: VerifyInvitationOtpInput;
};


export type MutationVerifyOnboardingOtpArgs = {
  input: VerifyOnboardingOtpInput;
};

export type NotificationChannelDelivery = {
  __typename?: 'NotificationChannelDelivery';
  attempts: Scalars['Int']['output'];
  channel: Scalars['String']['output'];
  createdAt: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  lastAttemptAt: Maybe<Scalars['String']['output']>;
  provider: Scalars['String']['output'];
  receiptId: Maybe<Scalars['String']['output']>;
  status: Scalars['String']['output'];
  updatedAt: Scalars['String']['output'];
};

export type NotificationDelivery = {
  __typename?: 'NotificationDelivery';
  aggregateStatus: Scalars['String']['output'];
  canCorrectResponsibleEmail: Scalars['Boolean']['output'];
  channels: Array<NotificationChannelDelivery>;
  createdAt: Scalars['String']['output'];
  failureCode: Maybe<Scalars['String']['output']>;
  id: Scalars['ID']['output'];
  inspectionId: Maybe<Scalars['ID']['output']>;
  intentId: Scalars['ID']['output'];
  invitationId: Maybe<Scalars['ID']['output']>;
  logicalTemplate: Scalars['String']['output'];
  recipientMasked: Maybe<Scalars['String']['output']>;
  responsibilityVersion: Maybe<Scalars['Int']['output']>;
  selectedProvider: Scalars['String']['output'];
  status: Scalars['String']['output'];
  updatedAt: Scalars['String']['output'];
};

export type NotificationDeliveryConnection = {
  __typename?: 'NotificationDeliveryConnection';
  nodes: Array<NotificationDelivery>;
  pageInfo: PageInfo;
};

export type NotificationPreferencesPayload = {
  __typename?: 'NotificationPreferencesPayload';
  clientMutationId: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type OnboardingActivation = {
  __typename?: 'OnboardingActivation';
  activatedAt: Maybe<Scalars['String']['output']>;
  identityId: Scalars['ID']['output'];
  purpose: Scalars['String']['output'];
  status: Scalars['String']['output'];
  tenantId: Scalars['ID']['output'];
};

export type OnboardingAgency = {
  __typename?: 'OnboardingAgency';
  businessUnitCode: Scalars['String']['output'];
  businessUnitId: Scalars['ID']['output'];
  name: Scalars['String']['output'];
  status: Scalars['String']['output'];
  tenantId: Scalars['ID']['output'];
};

export type OnboardingDefinition = {
  __typename?: 'OnboardingDefinition';
  analysisType: AnalysisType;
  originModes: Array<OnboardingOriginMode>;
  purposes: Array<Scalars['String']['output']>;
  schemaVersion: Scalars['Int']['output'];
  segment: Scalars['String']['output'];
  segmentVersion: Scalars['String']['output'];
  steps: Array<OnboardingStep>;
  templates: Array<Scalars['String']['output']>;
  version: Scalars['Int']['output'];
};

export type OnboardingField = {
  __typename?: 'OnboardingField';
  choices: Array<OnboardingOption>;
  key: Scalars['String']['output'];
  label: Scalars['String']['output'];
  /** @deprecated Use choices to obtain user-facing labels */
  options: Array<Scalars['String']['output']>;
  placeholder: Maybe<Scalars['String']['output']>;
  required: Scalars['Boolean']['output'];
  type: Scalars['String']['output'];
};

export type OnboardingOption = {
  __typename?: 'OnboardingOption';
  label: Scalars['String']['output'];
  value: Scalars['String']['output'];
};

export type OnboardingOriginMode = {
  __typename?: 'OnboardingOriginMode';
  key: Scalars['String']['output'];
  label: Scalars['String']['output'];
  required: Scalars['Boolean']['output'];
  templateKey: Scalars['String']['output'];
};

export type OnboardingOwner = {
  __typename?: 'OnboardingOwner';
  email: Scalars['String']['output'];
  name: Scalars['String']['output'];
};

export type OnboardingPayload = {
  __typename?: 'OnboardingPayload';
  activation: Maybe<OnboardingActivation>;
  clientMutationId: Scalars['String']['output'];
  request: Maybe<OnboardingRequest>;
  session: Maybe<OnboardingSession>;
  sessionLocator: Maybe<Scalars['String']['output']>;
  status: Maybe<OnboardingStatus>;
  userErrors: Array<UserError>;
};

export type OnboardingRequest = {
  __typename?: 'OnboardingRequest';
  assetId: Maybe<Scalars['ID']['output']>;
  id: Scalars['ID']['output'];
  originVersionId: Maybe<Scalars['ID']['output']>;
  participantId: Maybe<Scalars['ID']['output']>;
  status: Scalars['String']['output'];
  templateId: Scalars['ID']['output'];
};

export type OnboardingSession = {
  __typename?: 'OnboardingSession';
  completedSteps: Scalars['JSON']['output'];
  currentStep: Scalars['String']['output'];
  definition: Scalars['JSON']['output'];
  existingAgency: Maybe<OnboardingAgency>;
  expiresAt: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  owner: OnboardingOwner;
  state: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type OnboardingStatus = {
  __typename?: 'OnboardingStatus';
  canCorrectResponsibleEmail: Scalars['Boolean']['output'];
  deliveryFailureCode: Maybe<Scalars['String']['output']>;
  deliveryStatus: Scalars['String']['output'];
  inspectionId: Maybe<Scalars['ID']['output']>;
  nextAction: Scalars['String']['output'];
  originStatus: Scalars['String']['output'];
  requestId: Maybe<Scalars['ID']['output']>;
  responsibilityStatus: Maybe<Scalars['String']['output']>;
  responsibilityVersion: Maybe<Scalars['Int']['output']>;
  responsibleEmail: Maybe<Scalars['String']['output']>;
  state: Scalars['String']['output'];
  updatedAt: Maybe<Scalars['String']['output']>;
};

export type OnboardingStep = {
  __typename?: 'OnboardingStep';
  fields: Array<OnboardingField>;
  key: Scalars['String']['output'];
  label: Scalars['String']['output'];
  position: Scalars['Int']['output'];
  required: Scalars['Boolean']['output'];
};

export type OnboardingStepInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  payload: Scalars['JSON']['input'];
  step: Scalars['String']['input'];
};

export type OriginInvitationPayload = {
  __typename?: 'OriginInvitationPayload';
  clientMutationId: Scalars['String']['output'];
  invitationId: Maybe<Scalars['ID']['output']>;
  originVersionId: Maybe<Scalars['ID']['output']>;
  status: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type OriginPromotion = {
  __typename?: 'OriginPromotion';
  eligibleMedia: Array<OriginPromotionMedia>;
  failureReason: Maybe<Scalars['String']['output']>;
  inspectionId: Scalars['ID']['output'];
  originVersionId: Maybe<Scalars['ID']['output']>;
  selectedMediaIds: Array<Scalars['ID']['output']>;
  status: Scalars['String']['output'];
};

export type OriginPromotionMedia = {
  __typename?: 'OriginPromotionMedia';
  description: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  url: Maybe<Scalars['String']['output']>;
};

export type OriginPromotionPayload = {
  __typename?: 'OriginPromotionPayload';
  clientMutationId: Scalars['String']['output'];
  promotion: Maybe<OriginPromotion>;
  userErrors: Array<UserError>;
};

export type OriginVersion = {
  __typename?: 'OriginVersion';
  activatedAt: Maybe<Scalars['String']['output']>;
  activatedBy: Maybe<Scalars['ID']['output']>;
  captureSubmitted: Scalars['Boolean']['output'];
  contestationSummary: Maybe<ContestationSummary>;
  counterpartyInvitations: Array<BaselineCounterpartyInvitation>;
  counts: Maybe<InventoryCounts>;
  draftState: Maybe<Scalars['String']['output']>;
  hasInventory: Scalars['Boolean']['output'];
  id: Scalars['ID']['output'];
  limitedCoverage: Scalars['Boolean']['output'];
  mediaKind: Scalars['String']['output'];
  originId: Scalars['ID']['output'];
  originRevision: Scalars['Int']['output'];
  preparationStatus: Maybe<Scalars['String']['output']>;
  purpose: Maybe<Scalars['String']['output']>;
  scopeManifestDigest: Maybe<Scalars['String']['output']>;
  selectable: Scalars['Boolean']['output'];
  source: Maybe<Scalars['String']['output']>;
  sourceAvailability: Maybe<Scalars['String']['output']>;
  status: Scalars['String']['output'];
  supersedesId: Maybe<Scalars['ID']['output']>;
  versionNumber: Scalars['Int']['output'];
  videoPreparationId: Maybe<Scalars['ID']['output']>;
};

export type OriginVersionConnection = {
  __typename?: 'OriginVersionConnection';
  nodes: Array<OriginVersion>;
  pageInfo: PageInfo;
};

export type OriginVersionInput = {
  clientMutationId: Scalars['String']['input'];
  versionId: Scalars['ID']['input'];
};

export type OriginVersionPayload = {
  __typename?: 'OriginVersionPayload';
  clientMutationId: Scalars['String']['output'];
  /** Set by requestOriginCataloging: true when the legacy video source expired and only retained frames are cataloged. */
  sourcesExpired: Maybe<Scalars['Boolean']['output']>;
  userErrors: Array<UserError>;
  version: Maybe<OriginVersion>;
  warnings: Maybe<Array<Scalars['String']['output']>>;
};

export type PageInfo = {
  __typename?: 'PageInfo';
  endCursor: Maybe<Scalars['String']['output']>;
  hasNextPage: Scalars['Boolean']['output'];
};

export type Participant = {
  __typename?: 'Participant';
  businessUnitId: Scalars['ID']['output'];
  contacts: Array<ParticipantContact>;
  id: Scalars['ID']['output'];
  name: Scalars['String']['output'];
  segmentRole: Scalars['String']['output'];
  selectedContactIds: Array<Scalars['ID']['output']>;
  status: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type ParticipantConnection = {
  __typename?: 'ParticipantConnection';
  nodes: Array<Participant>;
  pageInfo: PageInfo;
};

export type ParticipantContact = {
  __typename?: 'ParticipantContact';
  active: Scalars['Boolean']['output'];
  channel: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  value: Scalars['String']['output'];
  verified: Scalars['Boolean']['output'];
};

export type ParticipantContactPayload = {
  __typename?: 'ParticipantContactPayload';
  clientMutationId: Scalars['String']['output'];
  contact: Maybe<ParticipantContact>;
  userErrors: Array<UserError>;
};

export type ParticipantPayload = {
  __typename?: 'ParticipantPayload';
  clientMutationId: Scalars['String']['output'];
  participant: Maybe<Participant>;
  userErrors: Array<UserError>;
};

export type PlanInspectionInput = {
  assetId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  comparisonMode: CaptureComparisonMode;
  createProject: InputMaybe<Scalars['Boolean']['input']>;
  deadlineAt: InputMaybe<Scalars['String']['input']>;
  deadlineMinutes: InputMaybe<Scalars['Int']['input']>;
  dueAt: InputMaybe<Scalars['String']['input']>;
  orderedStages: InputMaybe<Scalars['Boolean']['input']>;
  participantId: Scalars['ID']['input'];
  projectId: InputMaybe<Scalars['ID']['input']>;
  projectName: InputMaybe<Scalars['String']['input']>;
  reason: InputMaybe<Scalars['String']['input']>;
  referenceVersionId: InputMaybe<Scalars['ID']['input']>;
  reminderInstants: InputMaybe<Array<Scalars['String']['input']>>;
  reminderOffsetsMinutes: InputMaybe<Array<Scalars['Int']['input']>>;
  reviewerMembershipId: InputMaybe<Scalars['ID']['input']>;
  rrule: InputMaybe<Scalars['String']['input']>;
  stageId: InputMaybe<Scalars['ID']['input']>;
  stages: InputMaybe<Array<PlannedProjectStageInput>>;
  startsAt: InputMaybe<Scalars['String']['input']>;
  templateId: Scalars['ID']['input'];
  timezone: InputMaybe<Scalars['String']['input']>;
};

export type PlanInspectionPayload = {
  __typename?: 'PlanInspectionPayload';
  clientMutationId: Scalars['String']['output'];
  inspection: Maybe<Inspection>;
  project: Maybe<Project>;
  schedule: Maybe<Schedule>;
  userErrors: Array<UserError>;
};

export type PlannedProjectStageInput = {
  key: Scalars['String']['input'];
  label: Scalars['String']['input'];
  plannedAt: InputMaybe<Scalars['String']['input']>;
};

export type PostalAddress = {
  __typename?: 'PostalAddress';
  city: Scalars['String']['output'];
  complement: Scalars['String']['output'];
  countryCode: Scalars['String']['output'];
  district: Scalars['String']['output'];
  municipalityCode: Scalars['String']['output'];
  number: Scalars['String']['output'];
  postalCode: Scalars['String']['output'];
  reference: Scalars['String']['output'];
  state: Scalars['String']['output'];
  street: Scalars['String']['output'];
  withoutNumber: Scalars['Boolean']['output'];
};

export type PostalAddressInput = {
  city: Scalars['String']['input'];
  complement: InputMaybe<Scalars['String']['input']>;
  countryCode: InputMaybe<Scalars['String']['input']>;
  district: InputMaybe<Scalars['String']['input']>;
  municipalityCode: InputMaybe<Scalars['String']['input']>;
  number: InputMaybe<Scalars['String']['input']>;
  postalCode: Scalars['String']['input'];
  reference: InputMaybe<Scalars['String']['input']>;
  state: Scalars['String']['input'];
  street: Scalars['String']['input'];
  withoutNumber: Scalars['Boolean']['input'];
};

export type PostalAddressLookup = {
  __typename?: 'PostalAddressLookup';
  city: Maybe<Scalars['String']['output']>;
  district: Maybe<Scalars['String']['output']>;
  found: Scalars['Boolean']['output'];
  municipalityCode: Maybe<Scalars['String']['output']>;
  postalCode: Scalars['String']['output'];
  state: Maybe<Scalars['String']['output']>;
  street: Maybe<Scalars['String']['output']>;
};

export type PresignMediaPartsInput = {
  clientMutationId: Scalars['String']['input'];
  mediaId: Scalars['ID']['input'];
  partNumbers: Array<Scalars['Int']['input']>;
};

export type PresignedPart = {
  __typename?: 'PresignedPart';
  expiresAt: Scalars['String']['output'];
  partNumber: Scalars['Int']['output'];
  url: Scalars['String']['output'];
};

export type PresignedPartsPayload = {
  __typename?: 'PresignedPartsPayload';
  clientMutationId: Scalars['String']['output'];
  parts: Array<PresignedPart>;
  userErrors: Array<UserError>;
};

export type Project = {
  __typename?: 'Project';
  assetId: Scalars['ID']['output'];
  businessUnitId: Scalars['ID']['output'];
  id: Scalars['ID']['output'];
  name: Scalars['String']['output'];
  orderedStages: Scalars['Boolean']['output'];
  stages: Array<ProjectStage>;
  status: Scalars['String']['output'];
  transitions: Array<StageTransition>;
  version: Scalars['Int']['output'];
};

export type ProjectConnection = {
  __typename?: 'ProjectConnection';
  nodes: Array<Project>;
  pageInfo: PageInfo;
};

export type ProjectPayload = {
  __typename?: 'ProjectPayload';
  clientMutationId: Scalars['String']['output'];
  project: Maybe<Project>;
  userErrors: Array<UserError>;
};

export type ProjectStage = {
  __typename?: 'ProjectStage';
  id: Scalars['ID']['output'];
  inspectionIds: Array<Scalars['ID']['output']>;
  key: Scalars['String']['output'];
  kind: Scalars['String']['output'];
  label: Scalars['String']['output'];
  plannedAt: Maybe<Scalars['String']['output']>;
  position: Scalars['Int']['output'];
  reason: Maybe<Scalars['String']['output']>;
  status: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type ProjectTimeline = {
  __typename?: 'ProjectTimeline';
  entries: Array<ProjectTimelineEntry>;
  projectId: Scalars['ID']['output'];
};

export type ProjectTimelineEntry = {
  __typename?: 'ProjectTimelineEntry';
  inspectionIds: Array<Scalars['ID']['output']>;
  label: Scalars['String']['output'];
  occurredAt: Maybe<Scalars['String']['output']>;
  stageId: Scalars['ID']['output'];
  status: Scalars['String']['output'];
};

export type ProjectTransitionInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  projectId: Scalars['ID']['input'];
};

export type PromoteInspectionPhotosInput = {
  clientMutationId: Scalars['String']['input'];
  expectedAssetVersion: Scalars['Int']['input'];
  inspectionId: Scalars['ID']['input'];
  mediaIds: Array<Scalars['ID']['input']>;
};

export type PromoteInspectionVideoInput = {
  clientMutationId: Scalars['String']['input'];
  inspectionId: Scalars['ID']['input'];
};

export type PublicationPolicy = {
  __typename?: 'PublicationPolicy';
  mode: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type PublicationPolicyPayload = {
  __typename?: 'PublicationPolicyPayload';
  clientMutationId: Scalars['String']['output'];
  policy: Maybe<PublicationPolicy>;
  userErrors: Array<UserError>;
};

export type PublishReportInput = {
  clientMutationId: Scalars['String']['input'];
  inspectionId: Scalars['ID']['input'];
  snapshotId: Scalars['ID']['input'];
};

export type PublishSegmentDefinitionInput = {
  clientMutationId: Scalars['String']['input'];
  key: Scalars['String']['input'];
  name: Scalars['String']['input'];
  schema: Scalars['JSON']['input'];
  uiSchema: Scalars['JSON']['input'];
};

export type PublishTemplateVersionInput = {
  clientMutationId: Scalars['String']['input'];
  definition: Scalars['JSON']['input'];
  key: Scalars['String']['input'];
  name: Scalars['String']['input'];
};

export type Query = {
  __typename?: 'Query';
  analysisPrompt: AnalysisPrompt;
  asset: Maybe<Asset>;
  assets: AssetConnection;
  auditEvents: AuditEventConnection;
  baselineInventory: Maybe<BaselineInventory>;
  baselineVersions: OriginVersionConnection;
  businessUnits: BusinessUnitConnection;
  counterpartyBaseline: CounterpartyBaseline;
  customerEvidence: CustomerEvidenceConnection;
  customerPortfolio: CustomerPortfolioConnection;
  customerReport: Maybe<CustomerReport>;
  customerTimeline: CustomerTimelineConnection;
  dashboardSummary: DashboardSummary;
  externalCapture: ExternalCapture;
  inspection: Maybe<Inspection>;
  inspectionLLMUsage: InspectionLlmUsage;
  inspections: InspectionConnection;
  inventoryDraft: Maybe<InventoryDraft>;
  inventoryTrail: InventoryTrail;
  llmUsage: LlmUsage;
  llmUsageTenants: TenantConnection;
  lookupPostalCode: PostalAddressLookup;
  me: Me;
  memberships: MembershipConnection;
  myNotifications: RecipientNotificationConnection;
  notificationDeliveries: NotificationDeliveryConnection;
  onboardingDefinition: OnboardingDefinition;
  onboardingSession: Maybe<OnboardingSession>;
  onboardingStatus: Maybe<OnboardingStatus>;
  originPromotion: Maybe<OriginPromotion>;
  originVersions: OriginVersionConnection;
  participant: Maybe<Participant>;
  participants: ParticipantConnection;
  project: Maybe<Project>;
  projectTimeline: ProjectTimeline;
  projects: ProjectConnection;
  publicationPolicy: PublicationPolicy;
  report: Maybe<Report>;
  reportDownload: Maybe<ReportDownload>;
  reports: ReportConnection;
  retentionPolicies: RetentionPolicyConnection;
  schedules: ScheduleConnection;
  segmentDefinitionVersion: Maybe<SegmentDefinitionVersion>;
  segmentDefinitions: SegmentDefinitionConnection;
  sensitiveContentScreeningBypassed: Scalars['Boolean']['output'];
  sensitiveMediaOriginal: Maybe<SensitiveMediaOriginal>;
  sensitiveMediaReviews: SensitiveMediaReviewConnection;
  templateVersion: Maybe<TemplateVersion>;
  templates: TemplateConnection;
  tenant: Maybe<Tenant>;
  triageAssignees: Array<TriageAssignee>;
  triageCase: Maybe<TriageCase>;
  triageInspections: TriageInspectionConnection;
  triageWorkspace: TriageWorkspace;
  usageSummary: UsageSummary;
  videoAdmission: VideoAdmission;
  videoPolicy: VideoPolicy;
  videoPreparation: VideoPreparationStatus;
  videoPreparationEvidence: VideoPreparationEvidenceConnection;
  videoPreparations: VideoPreparationConnection;
  videoUploadStatus: VideoUploadStatus;
};


export type QueryAnalysisPromptArgs = {
  task?: InputMaybe<AnalysisPromptTask>;
  type: AnalysisType;
};


export type QueryAssetArgs = {
  id: Scalars['ID']['input'];
};


export type QueryAssetsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  businessUnitId: InputMaybe<Scalars['ID']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  search: InputMaybe<Scalars['String']['input']>;
};


export type QueryAuditEventsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QueryBaselineInventoryArgs = {
  assetId: Scalars['ID']['input'];
  versionId: InputMaybe<Scalars['ID']['input']>;
};


export type QueryBaselineVersionsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  assetId: Scalars['ID']['input'];
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QueryBusinessUnitsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QueryCustomerEvidenceArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  inspectionId: Scalars['ID']['input'];
  mode?: InputMaybe<EvidenceMode>;
};


export type QueryCustomerPortfolioArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  filter: InputMaybe<CustomerPortfolioFilter>;
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QueryCustomerReportArgs = {
  inspectionId: Scalars['ID']['input'];
  version: InputMaybe<Scalars['Int']['input']>;
};


export type QueryCustomerTimelineArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  assetId: InputMaybe<Scalars['ID']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  projectId: InputMaybe<Scalars['ID']['input']>;
};


export type QueryDashboardSummaryArgs = {
  businessUnitId: InputMaybe<Scalars['ID']['input']>;
  projectId: InputMaybe<Scalars['ID']['input']>;
};


export type QueryInspectionArgs = {
  id: Scalars['ID']['input'];
};


export type QueryInspectionLlmUsageArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  inspectionId: Scalars['ID']['input'];
  mode?: InputMaybe<LlmExecutionMode>;
};


export type QueryInspectionsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  history?: InputMaybe<Scalars['Boolean']['input']>;
  search: InputMaybe<Scalars['String']['input']>;
  statusGroup: InputMaybe<InspectionStatusGroup>;
};


export type QueryInventoryDraftArgs = {
  originVersionId: Scalars['ID']['input'];
};


export type QueryInventoryTrailArgs = {
  originVersionId: Scalars['ID']['input'];
};


export type QueryLlmUsageArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  filter: InputMaybe<LlmUsageFilter>;
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QueryLlmUsageTenantsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  search: InputMaybe<Scalars['String']['input']>;
};


export type QueryLookupPostalCodeArgs = {
  postalCode: Scalars['String']['input'];
};


export type QueryMembershipsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QueryMyNotificationsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  kind: InputMaybe<Scalars['String']['input']>;
  projectId: InputMaybe<Scalars['ID']['input']>;
  unreadOnly?: InputMaybe<Scalars['Boolean']['input']>;
};


export type QueryNotificationDeliveriesArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QueryOnboardingDefinitionArgs = {
  segment: Scalars['String']['input'];
};


export type QueryOriginPromotionArgs = {
  inspectionId: Scalars['ID']['input'];
};


export type QueryOriginVersionsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  assetId: Scalars['ID']['input'];
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QueryParticipantArgs = {
  id: Scalars['ID']['input'];
};


export type QueryParticipantsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  search: InputMaybe<Scalars['String']['input']>;
};


export type QueryProjectArgs = {
  id: Scalars['ID']['input'];
};


export type QueryProjectTimelineArgs = {
  projectId: Scalars['ID']['input'];
};


export type QueryProjectsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QueryReportArgs = {
  inspectionId: Scalars['ID']['input'];
  version: InputMaybe<Scalars['Int']['input']>;
};


export type QueryReportDownloadArgs = {
  kind?: InputMaybe<Scalars['String']['input']>;
  snapshotId: Scalars['ID']['input'];
};


export type QueryReportsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  classification: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  search: InputMaybe<Scalars['String']['input']>;
};


export type QuerySchedulesArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QuerySegmentDefinitionVersionArgs = {
  id: Scalars['ID']['input'];
};


export type QuerySegmentDefinitionsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  search: InputMaybe<Scalars['String']['input']>;
};


export type QuerySensitiveMediaOriginalArgs = {
  mediaId: Scalars['ID']['input'];
};


export type QuerySensitiveMediaReviewsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QueryTemplateVersionArgs = {
  id: Scalars['ID']['input'];
};


export type QueryTemplatesArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  search: InputMaybe<Scalars['String']['input']>;
};


export type QueryTriageAssigneesArgs = {
  inspectionId: Scalars['ID']['input'];
};


export type QueryTriageCaseArgs = {
  inspectionId: Scalars['ID']['input'];
};


export type QueryTriageInspectionsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  classification: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  status: InputMaybe<Scalars['String']['input']>;
};


export type QueryTriageWorkspaceArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  assigneeId: InputMaybe<Scalars['ID']['input']>;
  classification: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  reason: InputMaybe<Scalars['String']['input']>;
  search: InputMaybe<Scalars['String']['input']>;
  status: InputMaybe<TriageReviewStatus>;
};


export type QueryUsageSummaryArgs = {
  from: InputMaybe<Scalars['String']['input']>;
  to: InputMaybe<Scalars['String']['input']>;
};


export type QueryVideoPreparationArgs = {
  mediaId: Scalars['ID']['input'];
};


export type QueryVideoPreparationEvidenceArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  preparationId: Scalars['ID']['input'];
};


export type QueryVideoPreparationsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  status: InputMaybe<Array<Scalars['String']['input']>>;
};


export type QueryVideoUploadStatusArgs = {
  mediaId: Scalars['ID']['input'];
};

export type ReanalyzeVideoInspectionInput = {
  clientMutationId: Scalars['String']['input'];
  expectedPlanId: Scalars['ID']['input'];
  inspectionId: Scalars['ID']['input'];
};

export type ReanalyzeVideoInspectionPayload = {
  __typename?: 'ReanalyzeVideoInspectionPayload';
  clientMutationId: Scalars['String']['output'];
  generation: Maybe<Scalars['Int']['output']>;
  planId: Maybe<Scalars['ID']['output']>;
  userErrors: Array<UserError>;
};

export type Recapture = {
  __typename?: 'Recapture';
  deadlineAt: Maybe<Scalars['String']['output']>;
  id: Scalars['ID']['output'];
  responsibilityId: Scalars['ID']['output'];
  status: Scalars['String']['output'];
};

export type RecaptureItemInput = {
  originalMediaId: InputMaybe<Scalars['ID']['input']>;
  reason: Scalars['String']['input'];
  requirementKey: Scalars['String']['input'];
};

export type RecapturePayload = {
  __typename?: 'RecapturePayload';
  clientMutationId: Scalars['String']['output'];
  recapture: Maybe<Recapture>;
  userErrors: Array<UserError>;
};

export type RecipientNotification = {
  __typename?: 'RecipientNotification';
  action: Scalars['String']['output'];
  body: Scalars['String']['output'];
  context: Scalars['JSON']['output'];
  createdAt: Scalars['String']['output'];
  dueAt: Maybe<Scalars['String']['output']>;
  id: Scalars['ID']['output'];
  kind: Scalars['String']['output'];
  priority: Scalars['String']['output'];
  readAt: Maybe<Scalars['String']['output']>;
  resourceId: Maybe<Scalars['ID']['output']>;
  resourceKind: Scalars['String']['output'];
  title: Scalars['String']['output'];
};

export type RecipientNotificationConnection = {
  __typename?: 'RecipientNotificationConnection';
  nodes: Array<RecipientNotification>;
  pageInfo: PageInfo;
  unreadCount: Scalars['Int']['output'];
};

export type RecipientNotificationPayload = {
  __typename?: 'RecipientNotificationPayload';
  clientMutationId: Scalars['String']['output'];
  markedCount: Scalars['Int']['output'];
  notification: Maybe<RecipientNotification>;
  unreadCount: Scalars['Int']['output'];
  userErrors: Array<UserError>;
};

export type RecordCaptureQualityWarningInput = {
  clientMutationId: Scalars['String']['input'];
  code: Scalars['String']['input'];
  ignored: Scalars['Boolean']['input'];
  requirementKey: Scalars['String']['input'];
};

export type RecordDeletionRequestInput = {
  clientMutationId: Scalars['String']['input'];
  inspectionId: InputMaybe<Scalars['ID']['input']>;
  originVersionId: InputMaybe<Scalars['ID']['input']>;
  reason: InputMaybe<Scalars['String']['input']>;
  sourceMediaId: InputMaybe<Scalars['ID']['input']>;
};

export type RegisterAssetInput = {
  asset: AssetInput;
  clientMutationId: Scalars['String']['input'];
};

export type RemoveInventoryEnvironmentInput = {
  clientMutationId: Scalars['String']['input'];
  confirmItemExclusion: Scalars['Boolean']['input'];
  environmentId: Scalars['ID']['input'];
  originVersionId: Scalars['ID']['input'];
};

export type ReopenProjectInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  projectId: Scalars['ID']['input'];
  reason: Scalars['String']['input'];
};

export type Report = {
  __typename?: 'Report';
  advisory: Scalars['String']['output'];
  canonicalJSON: Scalars['JSON']['output'];
  classification: Scalars['String']['output'];
  context: ReportContext;
  createdAt: Scalars['String']['output'];
  evidence: ReportEvidenceConnection;
  findings: Array<ReportFinding>;
  html: Scalars['String']['output'];
  htmlDigest: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  inspectionId: Scalars['ID']['output'];
  inventory: Maybe<CustomerInventory>;
  jsonDigest: Scalars['String']['output'];
  mode: Scalars['String']['output'];
  pdfStatus: Scalars['String']['output'];
  projectId: Maybe<Scalars['ID']['output']>;
  requirements: Array<ReportRequirement>;
  timeline: Array<ReportTimelineEntry>;
  version: Scalars['Int']['output'];
};


export type ReportEvidenceArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
};

export type ReportAssetContext = {
  __typename?: 'ReportAssetContext';
  address: Scalars['String']['output'];
  externalKey: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  name: Scalars['String']['output'];
};

export type ReportConnection = {
  __typename?: 'ReportConnection';
  nodes: Array<ReportSummary>;
  pageInfo: PageInfo;
};

export type ReportContext = {
  __typename?: 'ReportContext';
  asset: ReportAssetContext;
  inspection: ReportInspectionContext;
  participant: ReportParticipantContext;
  template: ReportTemplateContext;
};

export type ReportDownload = {
  __typename?: 'ReportDownload';
  kind: Scalars['String']['output'];
  objectKey: Scalars['String']['output'];
  sha256: Maybe<Scalars['String']['output']>;
  snapshotId: Scalars['ID']['output'];
  status: Scalars['String']['output'];
  url: Scalars['String']['output'];
};

export type ReportEvidence = {
  __typename?: 'ReportEvidence';
  availability: Scalars['String']['output'];
  captureSource: Maybe<Scalars['String']['output']>;
  capturedAt: Maybe<Scalars['String']['output']>;
  component: Maybe<Scalars['String']['output']>;
  description: Maybe<Scalars['String']['output']>;
  displayDigest: Maybe<Scalars['String']['output']>;
  evidenceKind: Maybe<Scalars['String']['output']>;
  flags: Array<Scalars['String']['output']>;
  frameId: Maybe<Scalars['ID']['output']>;
  id: Scalars['ID']['output'];
  label: Maybe<Scalars['String']['output']>;
  planId: Maybe<Scalars['ID']['output']>;
  preparationId: Maybe<Scalars['ID']['output']>;
  ptsMicros: Maybe<Scalars['String']['output']>;
  requirementKey: Scalars['String']['output'];
  role: Scalars['String']['output'];
  sourceAvailability: Maybe<Scalars['String']['output']>;
  sourceMediaId: Maybe<Scalars['ID']['output']>;
  timestampMs: Maybe<Scalars['Int']['output']>;
  url: Maybe<Scalars['String']['output']>;
};

export type ReportEvidenceConnection = {
  __typename?: 'ReportEvidenceConnection';
  nodes: Array<ReportEvidence>;
  pageInfo: PageInfo;
};

export type ReportFinding = {
  __typename?: 'ReportFinding';
  category: Scalars['String']['output'];
  confidence: Scalars['Float']['output'];
  description: Scalars['String']['output'];
  evidenceIds: Array<Scalars['ID']['output']>;
  id: Maybe<Scalars['ID']['output']>;
  quality: Scalars['String']['output'];
  recommendedAction: Scalars['String']['output'];
  severity: Scalars['String']['output'];
  title: Scalars['String']['output'];
};

export type ReportInspectionContext = {
  __typename?: 'ReportInspectionContext';
  dueAt: Maybe<Scalars['String']['output']>;
  generatedAt: Scalars['String']['output'];
  projectId: Maybe<Scalars['ID']['output']>;
  stageId: Maybe<Scalars['ID']['output']>;
  stageLabel: Maybe<Scalars['String']['output']>;
  submittedAt: Maybe<Scalars['String']['output']>;
};

export type ReportParticipantContext = {
  __typename?: 'ReportParticipantContext';
  id: Scalars['ID']['output'];
  name: Scalars['String']['output'];
};

export type ReportPublication = {
  __typename?: 'ReportPublication';
  id: Scalars['ID']['output'];
  inspectionId: Scalars['ID']['output'];
  invalidatedAt: Maybe<Scalars['String']['output']>;
  publishedAt: Maybe<Scalars['String']['output']>;
  snapshotId: Scalars['ID']['output'];
  status: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type ReportPublicationPayload = {
  __typename?: 'ReportPublicationPayload';
  clientMutationId: Scalars['String']['output'];
  publication: Maybe<ReportPublication>;
  userErrors: Array<UserError>;
};

export type ReportRequirement = {
  __typename?: 'ReportRequirement';
  analysisMode: Scalars['String']['output'];
  analysisStatus: Scalars['String']['output'];
  coverage: Maybe<Scalars['String']['output']>;
  impossibilityReason: Maybe<Scalars['String']['output']>;
  instructions: Maybe<Scalars['String']['output']>;
  key: Scalars['String']['output'];
  label: Scalars['String']['output'];
  noRelevantChange: Maybe<Scalars['Boolean']['output']>;
  section: Scalars['String']['output'];
};

export type ReportSummary = {
  __typename?: 'ReportSummary';
  assetAddress: Scalars['String']['output'];
  assetExternalKey: Scalars['String']['output'];
  assetName: Scalars['String']['output'];
  classification: Scalars['String']['output'];
  generatedAt: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  inspectionId: Scalars['ID']['output'];
  participantName: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type ReportTemplateContext = {
  __typename?: 'ReportTemplateContext';
  id: Scalars['ID']['output'];
  name: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type ReportTimelineEntry = {
  __typename?: 'ReportTimelineEntry';
  classification: Maybe<Scalars['String']['output']>;
  position: Scalars['Int']['output'];
  stageId: Scalars['ID']['output'];
  status: Scalars['String']['output'];
};

export type ReprocessVideoPreparationInput = {
  clientMutationId: Scalars['String']['input'];
  expectedPreparationVersion: Scalars['Int']['input'];
  mediaId: Scalars['ID']['input'];
  profileDigest: Scalars['String']['input'];
};

export type RequestAdminActivationOtpInput = {
  activationToken: InputMaybe<Scalars['String']['input']>;
  clientMutationId: Scalars['String']['input'];
};

export type RequestCounterpartyOtpInput = {
  clientMutationId: Scalars['String']['input'];
  token: Scalars['String']['input'];
};

export type RequestInternalUserActivationOtpInput = {
  clientMutationId: Scalars['String']['input'];
  invitationToken: InputMaybe<Scalars['String']['input']>;
};

export type RequestInvitationOtpInput = {
  clientMutationId: Scalars['String']['input'];
  linkToken: Scalars['String']['input'];
};

export type RequestOnboardingOtpInput = {
  clientMutationId: Scalars['String']['input'];
  email: Scalars['String']['input'];
  name: Scalars['String']['input'];
  turnstileToken: InputMaybe<Scalars['String']['input']>;
};

export type RequestOriginCatalogingInput = {
  assetId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
};

export type RequestRecaptureInput = {
  clientMutationId: Scalars['String']['input'];
  deadlineAt: Scalars['String']['input'];
  inspectionId: Scalars['ID']['input'];
  items: Array<RecaptureItemInput>;
};

export type RequestVideoReplacementInput = {
  clientMutationId: Scalars['String']['input'];
  deadlineAt: Scalars['String']['input'];
  inspectionId: Scalars['ID']['input'];
  mediaId: Scalars['ID']['input'];
  reason: Scalars['String']['input'];
};

export type ResendCounterpartyInvitationInput = {
  clientMutationId: Scalars['String']['input'];
  counterpartyInvitationId: Scalars['ID']['input'];
};

export type ResendInternalUserInvitationInput = {
  clientMutationId: Scalars['String']['input'];
  membershipId: Scalars['ID']['input'];
};

export type ResolveBaselineContestationInput = {
  clientMutationId: Scalars['String']['input'];
  counterpartyInvitationId: Scalars['ID']['input'];
  outcome: Scalars['String']['input'];
  reason: InputMaybe<Scalars['String']['input']>;
};

export type ResponsibleEmailCorrection = {
  __typename?: 'ResponsibleEmailCorrection';
  canCorrectResponsibleEmail: Scalars['Boolean']['output'];
  deliveryId: Maybe<Scalars['ID']['output']>;
  deliveryStatus: Scalars['String']['output'];
  inspectionId: Scalars['ID']['output'];
  invitationId: Scalars['ID']['output'];
  recipientMasked: Scalars['String']['output'];
  responsibilityId: Scalars['ID']['output'];
  responsibilityStatus: Scalars['String']['output'];
  responsibilityVersion: Scalars['Int']['output'];
};

export type ResponsibleEmailCorrectionPayload = {
  __typename?: 'ResponsibleEmailCorrectionPayload';
  clientMutationId: Scalars['String']['output'];
  correction: Maybe<ResponsibleEmailCorrection>;
  userErrors: Array<UserError>;
};

export type RetentionMutationPayload = {
  __typename?: 'RetentionMutationPayload';
  clientMutationId: Scalars['String']['output'];
  requestId: Maybe<Scalars['ID']['output']>;
  status: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type RetentionPolicy = {
  __typename?: 'RetentionPolicy';
  createdAt: Scalars['String']['output'];
  evidenceDays: Scalars['Int']['output'];
  id: Scalars['ID']['output'];
  operationalDays: Scalars['Int']['output'];
  securityDays: Scalars['Int']['output'];
  updatedAt: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type RetentionPolicyConnection = {
  __typename?: 'RetentionPolicyConnection';
  nodes: Array<RetentionPolicy>;
  pageInfo: PageInfo;
};

export type RetentionPolicyPayload = {
  __typename?: 'RetentionPolicyPayload';
  clientMutationId: Scalars['String']['output'];
  policy: Maybe<RetentionPolicy>;
  userErrors: Array<UserError>;
};

export type RetryInventoryDraftInput = {
  clientMutationId: Scalars['String']['input'];
  originVersionId: Scalars['ID']['input'];
};

export type RetryVideoPreparationInput = {
  clientMutationId: Scalars['String']['input'];
  expectedPreparationVersion: Scalars['Int']['input'];
  mediaId: Scalars['ID']['input'];
};

export type RevokeInvitationInput = {
  clientMutationId: Scalars['String']['input'];
  linkToken: Scalars['String']['input'];
};

export type SaveCaptureMetadataInput = {
  captureSource: Scalars['String']['input'];
  clientMutationId: Scalars['String']['input'];
  description: Scalars['String']['input'];
  deviceContext: InputMaybe<Scalars['JSON']['input']>;
  gps: InputMaybe<CaptureGpsInput>;
  mediaId: Scalars['ID']['input'];
  replacesMediaId: InputMaybe<Scalars['ID']['input']>;
  requirementKey: Scalars['String']['input'];
};

export type Schedule = {
  __typename?: 'Schedule';
  assetId: Scalars['ID']['output'];
  businessUnitId: Scalars['ID']['output'];
  deadlineMinutes: Scalars['Int']['output'];
  id: Scalars['ID']['output'];
  nextDueAt: Scalars['String']['output'];
  participantId: Scalars['ID']['output'];
  projectId: Maybe<Scalars['ID']['output']>;
  referenceVersionId: Maybe<Scalars['ID']['output']>;
  reminderOffsetsMinutes: Array<Scalars['Int']['output']>;
  rrule: Scalars['String']['output'];
  stageId: Maybe<Scalars['ID']['output']>;
  startsAt: Scalars['String']['output'];
  status: Scalars['String']['output'];
  templateId: Scalars['ID']['output'];
  templateVersionId: Scalars['ID']['output'];
  timezone: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type ScheduleConnection = {
  __typename?: 'ScheduleConnection';
  nodes: Array<Schedule>;
  pageInfo: PageInfo;
};

export type SchedulePayload = {
  __typename?: 'SchedulePayload';
  clientMutationId: Scalars['String']['output'];
  schedule: Maybe<Schedule>;
  userErrors: Array<UserError>;
};

export type Scope = {
  __typename?: 'Scope';
  kind: Scalars['String']['output'];
  resourceId: Scalars['ID']['output'];
};

export type ScopeAssignmentInput = {
  kind: Scalars['String']['input'];
  resourceId: Scalars['ID']['input'];
};

export type SegmentDefinition = {
  __typename?: 'SegmentDefinition';
  activeVersionId: Maybe<Scalars['ID']['output']>;
  id: Scalars['ID']['output'];
  key: Scalars['String']['output'];
  name: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type SegmentDefinitionConnection = {
  __typename?: 'SegmentDefinitionConnection';
  nodes: Array<SegmentDefinition>;
  pageInfo: PageInfo;
};

export type SegmentDefinitionPayload = {
  __typename?: 'SegmentDefinitionPayload';
  clientMutationId: Scalars['String']['output'];
  definition: Maybe<SegmentDefinition>;
  userErrors: Array<UserError>;
  version: Maybe<SegmentDefinitionVersion>;
};

export type SegmentDefinitionVersion = {
  __typename?: 'SegmentDefinitionVersion';
  canonicalDigest: Scalars['String']['output'];
  definitionId: Scalars['ID']['output'];
  id: Scalars['ID']['output'];
  publishedAt: Scalars['String']['output'];
  schema: Scalars['JSON']['output'];
  schemaVersion: Scalars['Int']['output'];
  status: Scalars['String']['output'];
  uiSchema: Scalars['JSON']['output'];
  versionNumber: Scalars['Int']['output'];
};

export type SensitiveFalsePositiveInput = {
  clientMutationId: Scalars['String']['input'];
  mediaId: Scalars['ID']['input'];
  reason: Scalars['String']['input'];
};

export type SensitiveMediaKind =
  | 'PHOTO'
  | 'VIDEO';

export type SensitiveMediaOriginal = {
  __typename?: 'SensitiveMediaOriginal';
  expiresAt: Scalars['String']['output'];
  mediaId: Scalars['ID']['output'];
  mediaKind: SensitiveMediaKind;
  url: Scalars['String']['output'];
};

export type SensitiveMediaReview = {
  __typename?: 'SensitiveMediaReview';
  assetName: Scalars['String']['output'];
  blockedAt: Scalars['String']['output'];
  inspectionId: Scalars['ID']['output'];
  mediaId: Scalars['ID']['output'];
  mediaKind: SensitiveMediaKind;
  participantName: Scalars['String']['output'];
  requirementKey: Scalars['String']['output'];
  sourceAvailable: Scalars['Boolean']['output'];
  submitted: Scalars['Boolean']['output'];
};

export type SensitiveMediaReviewConnection = {
  __typename?: 'SensitiveMediaReviewConnection';
  nodes: Array<SensitiveMediaReview>;
  pageInfo: PageInfo;
};

export type SetAdminInitialPasswordInput = {
  clientMutationId: Scalars['String']['input'];
  password: Scalars['String']['input'];
};

export type SetDeliveryChannelsInput = {
  clientMutationId: Scalars['String']['input'];
  contactIds: Array<Scalars['ID']['input']>;
  expectedVersion: Scalars['Int']['input'];
  participantId: Scalars['ID']['input'];
};

export type SkipProjectStageInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  projectId: Scalars['ID']['input'];
  reason: Scalars['String']['input'];
  stageId: Scalars['ID']['input'];
};

export type SplitInventoryItemInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  itemId: Scalars['ID']['input'];
  originVersionId: Scalars['ID']['input'];
  parts: Array<InventorySplitPartInput>;
};

export type StageTransition = {
  __typename?: 'StageTransition';
  fromState: Maybe<Scalars['String']['output']>;
  id: Scalars['ID']['output'];
  occurredAt: Scalars['String']['output'];
  reason: Maybe<Scalars['String']['output']>;
  stageId: Maybe<Scalars['ID']['output']>;
  toState: Scalars['String']['output'];
};

export type StartBaselineDraftFromInspectionInput = {
  clientMutationId: Scalars['String']['input'];
  inspectionId: Scalars['ID']['input'];
};

export type StartProjectStageInput = {
  clientMutationId: Scalars['String']['input'];
  expectedProjectVersion: Scalars['Int']['input'];
  expectedStageVersion: Scalars['Int']['input'];
  projectId: Scalars['ID']['input'];
  stageId: Scalars['ID']['input'];
};

export type Submission = {
  __typename?: 'Submission';
  complete: Scalars['Boolean']['output'];
  id: Scalars['ID']['output'];
  preparationPending: Scalars['Boolean']['output'];
  requiresAttention: Scalars['Boolean']['output'];
  submittedAt: Scalars['String']['output'];
};

export type SubmissionPayload = {
  __typename?: 'SubmissionPayload';
  clientMutationId: Scalars['String']['output'];
  submission: Maybe<Submission>;
  userErrors: Array<UserError>;
};

export type SubmitCaptureInput = {
  clientMutationId: Scalars['String']['input'];
  confirmIncomplete: Scalars['Boolean']['input'];
};

export type SubmitRecaptureInput = {
  clientMutationId: Scalars['String']['input'];
  confirmIncomplete: Scalars['Boolean']['input'];
  requestId: Scalars['ID']['input'];
};

export type Template = {
  __typename?: 'Template';
  activeVersionId: Maybe<Scalars['ID']['output']>;
  id: Scalars['ID']['output'];
  key: Scalars['String']['output'];
  name: Scalars['String']['output'];
  segmentVersionId: Scalars['ID']['output'];
  version: Scalars['Int']['output'];
};

export type TemplateConnection = {
  __typename?: 'TemplateConnection';
  nodes: Array<Template>;
  pageInfo: PageInfo;
};

export type TemplatePayload = {
  __typename?: 'TemplatePayload';
  clientMutationId: Scalars['String']['output'];
  template: Maybe<Template>;
  userErrors: Array<UserError>;
  version: Maybe<TemplateVersion>;
};

export type TemplateVersion = {
  __typename?: 'TemplateVersion';
  canonicalDigest: Scalars['String']['output'];
  definition: Scalars['JSON']['output'];
  id: Scalars['ID']['output'];
  isVideo: Scalars['Boolean']['output'];
  mediaKind: Scalars['String']['output'];
  publishedAt: Scalars['String']['output'];
  schemaVersion: Scalars['Int']['output'];
  status: Scalars['String']['output'];
  templateId: Scalars['ID']['output'];
  versionNumber: Scalars['Int']['output'];
};

export type Tenant = {
  __typename?: 'Tenant';
  defaultTimezone: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  language: Scalars['String']['output'];
  name: Scalars['String']['output'];
  status: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type TenantConnection = {
  __typename?: 'TenantConnection';
  nodes: Array<Tenant>;
  pageInfo: PageInfo;
};

export type TenantPayload = {
  __typename?: 'TenantPayload';
  clientMutationId: Scalars['String']['output'];
  tenant: Maybe<Tenant>;
  userErrors: Array<UserError>;
};

export type TriageAssignee = {
  __typename?: 'TriageAssignee';
  current: Scalars['Boolean']['output'];
  id: Scalars['ID']['output'];
  role: Scalars['String']['output'];
};

export type TriageCase = {
  __typename?: 'TriageCase';
  address: Scalars['String']['output'];
  assetId: Scalars['ID']['output'];
  assetName: Scalars['String']['output'];
  assigneeId: Maybe<Scalars['ID']['output']>;
  classification: Scalars['String']['output'];
  createdAt: Scalars['String']['output'];
  events: Array<TriageCaseEvent>;
  inspectionId: Scalars['ID']['output'];
  reasonCodes: Array<Scalars['String']['output']>;
  report: Maybe<Report>;
  reportVersion: Scalars['Int']['output'];
  reviewStatus: TriageReviewStatus;
  status: Scalars['String']['output'];
  updatedAt: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type TriageCaseAction =
  | 'ASSIGN'
  | 'COMPLETE'
  | 'NOTE'
  | 'REOPEN'
  | 'TAKE'
  | 'WAIT_FOR_EVIDENCE';

export type TriageCaseEvent = {
  __typename?: 'TriageCaseEvent';
  actorId: Scalars['ID']['output'];
  body: Scalars['String']['output'];
  createdAt: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  kind: Scalars['String']['output'];
};

export type TriageCasePayload = {
  __typename?: 'TriageCasePayload';
  clientMutationId: Scalars['String']['output'];
  triageCase: TriageCase;
  userErrors: Array<UserError>;
};

export type TriageCounts = {
  __typename?: 'TriageCounts';
  awaitingEvidence: Scalars['Int']['output'];
  criticalOpen: Scalars['Int']['output'];
  inReview: Scalars['Int']['output'];
  new: Scalars['Int']['output'];
};

export type TriageDisposition =
  | 'EXTERNAL_FOLLOWUP'
  | 'NO_ACTION'
  | 'REFERRED';

export type TriageInspection = {
  __typename?: 'TriageInspection';
  assetId: Maybe<Scalars['ID']['output']>;
  classification: Scalars['String']['output'];
  inspectionId: Scalars['ID']['output'];
  projectId: Maybe<Scalars['ID']['output']>;
  status: Scalars['String']['output'];
  updatedAt: Scalars['String']['output'];
};

export type TriageInspectionConnection = {
  __typename?: 'TriageInspectionConnection';
  nodes: Array<TriageInspection>;
  pageInfo: PageInfo;
};

export type TriageQueueItem = {
  __typename?: 'TriageQueueItem';
  address: Scalars['String']['output'];
  assetId: Scalars['ID']['output'];
  assetName: Scalars['String']['output'];
  assigneeId: Maybe<Scalars['ID']['output']>;
  classification: Scalars['String']['output'];
  createdAt: Scalars['String']['output'];
  findingCount: Scalars['Int']['output'];
  inspectionId: Scalars['ID']['output'];
  reasonCodes: Array<Scalars['String']['output']>;
  reportVersion: Scalars['Int']['output'];
  reviewStatus: TriageReviewStatus;
  status: Scalars['String']['output'];
  updatedAt: Scalars['String']['output'];
  version: Scalars['Int']['output'];
};

export type TriageReviewStatus =
  | 'ARCHIVED'
  | 'AWAITING_EVIDENCE'
  | 'IN_REVIEW'
  | 'NEW'
  | 'REVIEWED';

export type TriageWorkspace = {
  __typename?: 'TriageWorkspace';
  counts: TriageCounts;
  nodes: Array<TriageQueueItem>;
  pageInfo: PageInfo;
};

export type UpdateAnalysisPromptInput = {
  analysisType: AnalysisType;
  clientMutationId: Scalars['String']['input'];
  expectedRevision: Scalars['Int']['input'];
  systemPrompt: Scalars['String']['input'];
  task: InputMaybe<AnalysisPromptTask>;
};

export type UpdateAssetInput = {
  asset: AssetInput;
  assetId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
};

export type UpdateBaselinePolicyInput = {
  clientMutationId: Scalars['String']['input'];
  contestationDeadlineDays: Scalars['Int']['input'];
};

export type UpdateScheduleInput = {
  clientMutationId: Scalars['String']['input'];
  deadlineMinutes: Scalars['Int']['input'];
  expectedVersion: Scalars['Int']['input'];
  reminderOffsetsMinutes: Array<Scalars['Int']['input']>;
  rrule: Scalars['String']['input'];
  scheduleId: Scalars['ID']['input'];
  startsAt: Scalars['String']['input'];
  timezone: Scalars['String']['input'];
};

export type UpdateTenantInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  language: Scalars['String']['input'];
  name: Scalars['String']['input'];
  timezone: Scalars['String']['input'];
};

export type UpdateTriageCaseInput = {
  action: TriageCaseAction;
  assigneeId: InputMaybe<Scalars['ID']['input']>;
  body: InputMaybe<Scalars['String']['input']>;
  clientMutationId: Scalars['String']['input'];
  disposition: InputMaybe<TriageDisposition>;
  expectedVersion: Scalars['Int']['input'];
  inspectionId: Scalars['ID']['input'];
};

export type UpsertBusinessUnitInput = {
  businessUnitId: InputMaybe<Scalars['ID']['input']>;
  clientMutationId: Scalars['String']['input'];
  code: Scalars['String']['input'];
  expectedTenantVersion: InputMaybe<Scalars['Int']['input']>;
  expectedVersion: InputMaybe<Scalars['Int']['input']>;
  name: Scalars['String']['input'];
};

export type UpsertInventoryEnvironmentInput = {
  clientMutationId: Scalars['String']['input'];
  environmentId: InputMaybe<Scalars['ID']['input']>;
  expected: Scalars['Boolean']['input'];
  name: Scalars['String']['input'];
  originVersionId: Scalars['ID']['input'];
  typeCode: Scalars['String']['input'];
};

export type UpsertParticipantInput = {
  businessUnitId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  contacts: Array<ContactInput>;
  expectedVersion: InputMaybe<Scalars['Int']['input']>;
  name: Scalars['String']['input'];
  participantId: InputMaybe<Scalars['ID']['input']>;
  segmentRole: Scalars['String']['input'];
};

export type UsageSummary = {
  __typename?: 'UsageSummary';
  cost: Maybe<Scalars['Float']['output']>;
  from: Scalars['String']['output'];
  inputTokens: Scalars['Int']['output'];
  outputTokens: Scalars['Int']['output'];
  requests: Scalars['Int']['output'];
  to: Scalars['String']['output'];
};

export type UserError = {
  __typename?: 'UserError';
  code: Scalars['String']['output'];
  correlationId: Scalars['String']['output'];
  field: Maybe<Scalars['String']['output']>;
  message: Scalars['String']['output'];
  pendingRequirementKeys: Maybe<Array<Scalars['String']['output']>>;
};

export type VerifyAdminActivationOtpInput = {
  clientMutationId: Scalars['String']['input'];
  code: Scalars['String']['input'];
};

export type VerifyContactInput = {
  clientMutationId: Scalars['String']['input'];
  contactId: Scalars['ID']['input'];
  verified: Scalars['Boolean']['input'];
};

export type VerifyCounterpartyOtpInput = {
  clientMutationId: Scalars['String']['input'];
  code: Scalars['String']['input'];
  token: Scalars['String']['input'];
};

export type VerifyInternalUserActivationOtpInput = {
  clientMutationId: Scalars['String']['input'];
  code: Scalars['String']['input'];
};

export type VerifyInvitationOtpInput = {
  clientMutationId: Scalars['String']['input'];
  code: Scalars['String']['input'];
  linkToken: Scalars['String']['input'];
};

export type VerifyOnboardingOtpInput = {
  clientMutationId: Scalars['String']['input'];
  code: Scalars['String']['input'];
  sessionLocator: Scalars['String']['input'];
};

export type VideoAdmission = {
  __typename?: 'VideoAdmission';
  changedAt: Scalars['String']['output'];
  enabled: Scalars['Boolean']['output'];
  reason: Maybe<Scalars['String']['output']>;
  version: Scalars['Int']['output'];
};

export type VideoAdmissionPayload = {
  __typename?: 'VideoAdmissionPayload';
  admission: Maybe<VideoAdmission>;
  clientMutationId: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type VideoAttempt = {
  __typename?: 'VideoAttempt';
  acquisitionSource: Scalars['String']['output'];
  admittedAt: Scalars['String']['output'];
  expiresAt: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  kind: Scalars['String']['output'];
  maxBytes: Scalars['Int']['output'];
  maxDurationSeconds: Scalars['Int']['output'];
  policyVersion: Scalars['Int']['output'];
  status: Scalars['String']['output'];
};

export type VideoAttemptPayload = {
  __typename?: 'VideoAttemptPayload';
  attempt: Maybe<VideoAttempt>;
  clientMutationId: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type VideoPolicy = {
  __typename?: 'VideoPolicy';
  currentRetentionDays: Scalars['Int']['output'];
  maxDurationMinutes: Scalars['Int']['output'];
  originRetentionDays: Scalars['Int']['output'];
  supersededBaselineRetentionDays: Scalars['Int']['output'];
  version: Scalars['Int']['output'];
};

export type VideoPolicyPayload = {
  __typename?: 'VideoPolicyPayload';
  clientMutationId: Scalars['String']['output'];
  policy: Maybe<VideoPolicy>;
  userErrors: Array<UserError>;
};

export type VideoPreparationConnection = {
  __typename?: 'VideoPreparationConnection';
  nodes: Array<VideoPreparationStatus>;
  pageInfo: PageInfo;
};

export type VideoPreparationEvidenceConnection = {
  __typename?: 'VideoPreparationEvidenceConnection';
  nodes: Array<VideoPreparationEvidenceItem>;
  pageInfo: PageInfo;
};

export type VideoPreparationEvidenceItem = {
  __typename?: 'VideoPreparationEvidenceItem';
  component: Maybe<Scalars['String']['output']>;
  environment: Maybe<Scalars['String']['output']>;
  frameId: Maybe<Scalars['ID']['output']>;
  id: Scalars['ID']['output'];
  kind: Scalars['String']['output'];
  label: Maybe<Scalars['String']['output']>;
  preparationId: Maybe<Scalars['ID']['output']>;
  ptsMicros: Maybe<Scalars['String']['output']>;
  role: Maybe<Scalars['String']['output']>;
  sourceMediaId: Maybe<Scalars['ID']['output']>;
  timestampMs: Maybe<Scalars['Int']['output']>;
  view: Maybe<Scalars['String']['output']>;
};

export type VideoPreparationPayload = {
  __typename?: 'VideoPreparationPayload';
  clientMutationId: Scalars['String']['output'];
  preparation: Maybe<VideoPreparationStatus>;
  userErrors: Array<UserError>;
};

export type VideoPreparationStatus = {
  __typename?: 'VideoPreparationStatus';
  attempts: Scalars['Int']['output'];
  errorCode: Scalars['String']['output'];
  firstFrameReadyAt: Maybe<Scalars['String']['output']>;
  manifestDigest: Scalars['String']['output'];
  mediaId: Scalars['ID']['output'];
  preparationId: Scalars['ID']['output'];
  preparationVersion: Scalars['Int']['output'];
  processedFrames: Scalars['Int']['output'];
  retryable: Scalars['Boolean']['output'];
  sourceAvailable: Scalars['Boolean']['output'];
  sourceExpiryAt: Maybe<Scalars['String']['output']>;
  stage: Scalars['String']['output'];
  status: Scalars['String']['output'];
};

export type VideoReceivedPart = {
  __typename?: 'VideoReceivedPart';
  etag: Scalars['String']['output'];
  partNumber: Scalars['Int']['output'];
  sizeBytes: Scalars['Int']['output'];
};

export type VideoReplacement = {
  __typename?: 'VideoReplacement';
  deadlineAt: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  responsibilityId: Scalars['ID']['output'];
  status: Scalars['String']['output'];
};

export type VideoReplacementPayload = {
  __typename?: 'VideoReplacementPayload';
  clientMutationId: Scalars['String']['output'];
  replacement: Maybe<VideoReplacement>;
  userErrors: Array<UserError>;
};

export type VideoUploadStatus = {
  __typename?: 'VideoUploadStatus';
  confirmedParts: Array<VideoReceivedPart>;
  expiresAt: Scalars['String']['output'];
  mediaId: Scalars['ID']['output'];
  missingParts: Array<Scalars['Int']['output']>;
  receivedParts: Array<Scalars['Int']['output']>;
  status: Scalars['String']['output'];
};
