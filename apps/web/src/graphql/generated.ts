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

export type AnalysisProfile = {
  __typename?: 'AnalysisProfile';
  canonicalDigest: Scalars['String']['output'];
  definition: Scalars['JSON']['output'];
  id: Scalars['ID']['output'];
  key: Scalars['String']['output'];
  publishedAt: Scalars['String']['output'];
  schemaVersion: Scalars['Int']['output'];
  status: Scalars['String']['output'];
  versionNumber: Scalars['Int']['output'];
};

export type AnalysisProfilePayload = {
  __typename?: 'AnalysisProfilePayload';
  clientMutationId: Scalars['String']['output'];
  profile: Maybe<AnalysisProfile>;
  userErrors: Array<UserError>;
};

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
  address: Scalars['String']['input'];
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

export type CaptureRequirement = {
  __typename?: 'CaptureRequirement';
  captureSourcePolicy: Scalars['String']['output'];
  comparisonTarget: Scalars['String']['output'];
  descriptionRequired: Scalars['Boolean']['output'];
  evidenceKind: Scalars['String']['output'];
  impossibilityAllowed: Scalars['Boolean']['output'];
  instructions: Maybe<Scalars['String']['output']>;
  key: Scalars['String']['output'];
  label: Scalars['String']['output'];
  maximumMedia: Scalars['Int']['output'];
  minimumMedia: Scalars['Int']['output'];
  required: Scalars['Boolean']['output'];
  section: Scalars['String']['output'];
};

export type CompleteMediaUploadInput = {
  clientMutationId: Scalars['String']['input'];
  mediaId: Scalars['ID']['input'];
  parts: Array<CompletedPartInput>;
};

export type CompletedPartInput = {
  etag: Scalars['String']['input'];
  partNumber: Scalars['Int']['input'];
};

export type ConfigureRetentionPolicyInput = {
  clientMutationId: Scalars['String']['input'];
  evidenceDays: Scalars['Int']['input'];
  operationalDays: Scalars['Int']['input'];
  securityDays: InputMaybe<Scalars['Int']['input']>;
};

export type ContactInput = {
  channel: Scalars['String']['input'];
  value: Scalars['String']['input'];
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
  deadlineAt: Scalars['String']['input'];
  dueAt: Scalars['String']['input'];
  participantId: Scalars['ID']['input'];
  reason: Scalars['String']['input'];
  referenceVersionId: InputMaybe<Scalars['ID']['input']>;
  reminderInstants: Array<Scalars['String']['input']>;
  templateId: InputMaybe<Scalars['ID']['input']>;
};

export type CreateMediaUploadInput = {
  clientMutationId: Scalars['String']['input'];
  contentType: Scalars['String']['input'];
  sha256: Scalars['String']['input'];
  sizeBytes: Scalars['Int']['input'];
};

export type CreateProjectInput = {
  assetId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  participantId: Scalars['ID']['input'];
  templateId: InputMaybe<Scalars['ID']['input']>;
};

export type CreateScheduleInput = {
  assetId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  deadlineMinutes: Scalars['Int']['input'];
  participantId: Scalars['ID']['input'];
  referenceVersionId: InputMaybe<Scalars['ID']['input']>;
  reminderOffsetsMinutes: Array<Scalars['Int']['input']>;
  rrule: Scalars['String']['input'];
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

export type DashboardSummary = {
  __typename?: 'DashboardSummary';
  attention: Scalars['Int']['output'];
  critical: Scalars['Int']['output'];
  invalidated: Scalars['Int']['output'];
  normal: Scalars['Int']['output'];
  pending: Scalars['Int']['output'];
  total: Scalars['Int']['output'];
};

export type DeclareCaptureImpossibilityInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: InputMaybe<Scalars['Int']['input']>;
  reason: Scalars['String']['input'];
  requirementKey: Scalars['String']['input'];
};

export type DisableMembershipInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  membershipId: Scalars['ID']['input'];
};

export type ExternalCapture = {
  __typename?: 'ExternalCapture';
  answers: Array<CaptureAnswer>;
  confirmationOnly: Scalars['Boolean']['output'];
  disclosureVersion: Scalars['String']['output'];
  kind: Scalars['String']['output'];
  policy: Scalars['JSON']['output'];
  recaptureRequestId: Maybe<Scalars['ID']['output']>;
  reference: Scalars['JSON']['output'];
  requirements: Array<CaptureRequirement>;
  responsibilityId: Scalars['ID']['output'];
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

export type Inspection = {
  __typename?: 'Inspection';
  analysisProfileVersionId: Scalars['ID']['output'];
  assetId: Scalars['ID']['output'];
  businessUnitId: Scalars['ID']['output'];
  deadlineAt: Scalars['String']['output'];
  dueAt: Scalars['String']['output'];
  evidenceCount: Scalars['Int']['output'];
  id: Scalars['ID']['output'];
  participantId: Scalars['ID']['output'];
  projectId: Maybe<Scalars['ID']['output']>;
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

export type InspectionConnection = {
  __typename?: 'InspectionConnection';
  nodes: Array<Inspection>;
  pageInfo: PageInfo;
};

export type InspectionPayload = {
  __typename?: 'InspectionPayload';
  clientMutationId: Scalars['String']['output'];
  inspection: Maybe<Inspection>;
  userErrors: Array<UserError>;
};

export type InspectionTransitionInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  inspectionId: Scalars['ID']['input'];
};

export type InvalidateInspectionInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  inspectionId: Scalars['ID']['input'];
  reason: Scalars['String']['input'];
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
  issuer: Scalars['String']['input'];
  role: Scalars['String']['input'];
  scopes: Array<ScopeAssignmentInput>;
  subject: Scalars['String']['input'];
};

export type InviteOriginCaptureInput = {
  assetId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  expiresAt: Scalars['String']['input'];
  participantId: Scalars['ID']['input'];
};

export type LegalHoldInput = {
  clientMutationId: Scalars['String']['input'];
  inspectionId: Scalars['ID']['input'];
  reason: Scalars['String']['input'];
};

export type Me = {
  __typename?: 'Me';
  effectiveScopes: Array<Scope>;
  identityId: Scalars['ID']['output'];
  memberships: Array<Membership>;
  roles: Array<Scalars['String']['output']>;
  tenantId: Scalars['ID']['output'];
};

export type Media = {
  __typename?: 'Media';
  captureSource: Maybe<Scalars['String']['output']>;
  description: Maybe<Scalars['String']['output']>;
  flags: Array<Scalars['String']['output']>;
  id: Scalars['ID']['output'];
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
  id: Scalars['ID']['output'];
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

export type Mutation = {
  __typename?: 'Mutation';
  acceptProcessing: CapturePayload;
  activateOriginVersion: OriginVersionPayload;
  activateSegmentDefinition: SegmentDefinitionPayload;
  activateTemplateVersion: TemplatePayload;
  addExceptionalStage: ProjectPayload;
  applyLegalHold: RetentionMutationPayload;
  archiveAsset: AssetPayload;
  archiveBusinessUnit: BusinessUnitPayload;
  assignRoleScopes: MembershipPayload;
  cancelInspection: InspectionPayload;
  cancelSchedule: SchedulePayload;
  closeProject: ProjectPayload;
  completeMediaUpload: MediaPayload;
  configureRetentionPolicy: RetentionPolicyPayload;
  createBusinessUnit: BusinessUnitPayload;
  createInspection: InspectionPayload;
  createMediaUpload: MediaUploadPayload;
  createProject: ProjectPayload;
  createSchedule: SchedulePayload;
  createTenant: CreateTenantPayload;
  declareCaptureImpossibility: CapturePayload;
  declareSensitiveDetectionFalsePositive: MediaPayload;
  disableMembership: MembershipPayload;
  invalidateInspection: InspectionPayload;
  invalidateOriginVersion: OriginVersionPayload;
  inviteInternalUser: MembershipPayload;
  inviteOriginCapture: OriginInvitationPayload;
  presignMediaParts: PresignedPartsPayload;
  publishAnalysisProfile: AnalysisProfilePayload;
  publishSegmentDefinition: SegmentDefinitionPayload;
  publishTemplateVersion: TemplatePayload;
  recordDeletionRequest: RetentionMutationPayload;
  registerAsset: AssetPayload;
  releaseLegalHold: RetentionMutationPayload;
  reopenProject: ProjectPayload;
  requestInvitationOtp: InvitationOtpPayload;
  requestRecapture: RecapturePayload;
  revokeInvitation: InvitationPayload;
  saveCaptureMetadata: MediaPayload;
  setDeliveryChannels: ParticipantPayload;
  skipProjectStage: ProjectPayload;
  startProjectStage: ProjectPayload;
  submitCapture: SubmissionPayload;
  submitRecapture: RecapturePayload;
  updateAsset: AssetPayload;
  updateSchedule: SchedulePayload;
  updateTenant: TenantPayload;
  upsertBusinessUnit: BusinessUnitPayload;
  upsertParticipant: ParticipantPayload;
  verifyContact: ParticipantContactPayload;
  verifyInvitationOtp: ExternalSessionPayload;
};


export type MutationAcceptProcessingArgs = {
  input: AcceptProcessingInput;
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


export type MutationCancelInspectionArgs = {
  input: InspectionTransitionInput;
};


export type MutationCancelScheduleArgs = {
  input: CancelScheduleInput;
};


export type MutationCloseProjectArgs = {
  input: ProjectTransitionInput;
};


export type MutationCompleteMediaUploadArgs = {
  input: CompleteMediaUploadInput;
};


export type MutationConfigureRetentionPolicyArgs = {
  input: ConfigureRetentionPolicyInput;
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


export type MutationDeclareCaptureImpossibilityArgs = {
  input: DeclareCaptureImpossibilityInput;
};


export type MutationDeclareSensitiveDetectionFalsePositiveArgs = {
  input: SensitiveFalsePositiveInput;
};


export type MutationDisableMembershipArgs = {
  input: DisableMembershipInput;
};


export type MutationInvalidateInspectionArgs = {
  input: InvalidateInspectionInput;
};


export type MutationInvalidateOriginVersionArgs = {
  input: OriginVersionInput;
};


export type MutationInviteInternalUserArgs = {
  input: InviteInternalUserInput;
};


export type MutationInviteOriginCaptureArgs = {
  input: InviteOriginCaptureInput;
};


export type MutationPresignMediaPartsArgs = {
  input: PresignMediaPartsInput;
};


export type MutationPublishAnalysisProfileArgs = {
  input: PublishAnalysisProfileInput;
};


export type MutationPublishSegmentDefinitionArgs = {
  input: PublishSegmentDefinitionInput;
};


export type MutationPublishTemplateVersionArgs = {
  input: PublishTemplateVersionInput;
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


export type MutationReopenProjectArgs = {
  input: ReopenProjectInput;
};


export type MutationRequestInvitationOtpArgs = {
  input: RequestInvitationOtpInput;
};


export type MutationRequestRecaptureArgs = {
  input: RequestRecaptureInput;
};


export type MutationRevokeInvitationArgs = {
  input: RevokeInvitationInput;
};


export type MutationSaveCaptureMetadataArgs = {
  input: SaveCaptureMetadataInput;
};


export type MutationSetDeliveryChannelsArgs = {
  input: SetDeliveryChannelsInput;
};


export type MutationSkipProjectStageArgs = {
  input: SkipProjectStageInput;
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


export type MutationUpdateAssetArgs = {
  input: UpdateAssetInput;
};


export type MutationUpdateScheduleArgs = {
  input: UpdateScheduleInput;
};


export type MutationUpdateTenantArgs = {
  input: UpdateTenantInput;
};


export type MutationUpsertBusinessUnitArgs = {
  input: UpsertBusinessUnitInput;
};


export type MutationUpsertParticipantArgs = {
  input: UpsertParticipantInput;
};


export type MutationVerifyContactArgs = {
  input: VerifyContactInput;
};


export type MutationVerifyInvitationOtpArgs = {
  input: VerifyInvitationOtpInput;
};

export type NotificationDelivery = {
  __typename?: 'NotificationDelivery';
  createdAt: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  intentId: Scalars['ID']['output'];
  status: Scalars['String']['output'];
  updatedAt: Scalars['String']['output'];
};

export type NotificationDeliveryConnection = {
  __typename?: 'NotificationDeliveryConnection';
  nodes: Array<NotificationDelivery>;
  pageInfo: PageInfo;
};

export type OriginInvitationPayload = {
  __typename?: 'OriginInvitationPayload';
  clientMutationId: Scalars['String']['output'];
  invitationId: Maybe<Scalars['ID']['output']>;
  originVersionId: Maybe<Scalars['ID']['output']>;
  status: Scalars['String']['output'];
  userErrors: Array<UserError>;
};

export type OriginVersion = {
  __typename?: 'OriginVersion';
  activatedAt: Maybe<Scalars['String']['output']>;
  id: Scalars['ID']['output'];
  originId: Scalars['ID']['output'];
  status: Scalars['String']['output'];
  supersedesId: Maybe<Scalars['ID']['output']>;
  versionNumber: Scalars['Int']['output'];
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
  userErrors: Array<UserError>;
  version: Maybe<OriginVersion>;
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
  participantId: Scalars['ID']['output'];
  reportMode: Scalars['String']['output'];
  stages: Array<ProjectStage>;
  status: Scalars['String']['output'];
  templateId: Scalars['ID']['output'];
  templateVersionId: Scalars['ID']['output'];
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
  inspectionId: Maybe<Scalars['ID']['output']>;
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
  inspectionId: Maybe<Scalars['ID']['output']>;
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

export type PublishAnalysisProfileInput = {
  clientMutationId: Scalars['String']['input'];
  definition: Scalars['JSON']['input'];
  key: Scalars['String']['input'];
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
  asset: Maybe<Asset>;
  assets: AssetConnection;
  auditEvents: AuditEventConnection;
  businessUnits: BusinessUnitConnection;
  dashboardSummary: DashboardSummary;
  externalCapture: ExternalCapture;
  inspection: Maybe<Inspection>;
  inspections: InspectionConnection;
  me: Me;
  memberships: MembershipConnection;
  notificationDeliveries: NotificationDeliveryConnection;
  originVersions: OriginVersionConnection;
  participant: Maybe<Participant>;
  participants: ParticipantConnection;
  project: Maybe<Project>;
  projectTimeline: ProjectTimeline;
  projects: ProjectConnection;
  report: Maybe<Report>;
  reportDownload: Maybe<ReportDownload>;
  retentionPolicies: RetentionPolicyConnection;
  schedules: ScheduleConnection;
  segmentDefinitions: SegmentDefinitionConnection;
  templateVersion: Maybe<TemplateVersion>;
  templates: TemplateConnection;
  tenant: Maybe<Tenant>;
  triageInspections: TriageInspectionConnection;
  usageSummary: UsageSummary;
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


export type QueryBusinessUnitsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QueryDashboardSummaryArgs = {
  businessUnitId: InputMaybe<Scalars['ID']['input']>;
  projectId: InputMaybe<Scalars['ID']['input']>;
};


export type QueryInspectionArgs = {
  id: Scalars['ID']['input'];
};


export type QueryInspectionsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  history?: InputMaybe<Scalars['Boolean']['input']>;
};


export type QueryMembershipsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QueryNotificationDeliveriesArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
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


export type QuerySchedulesArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
};


export type QuerySegmentDefinitionsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  search: InputMaybe<Scalars['String']['input']>;
};


export type QueryTemplateVersionArgs = {
  id: Scalars['ID']['input'];
};


export type QueryTemplatesArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  search: InputMaybe<Scalars['String']['input']>;
};


export type QueryTriageInspectionsArgs = {
  after: InputMaybe<Scalars['String']['input']>;
  classification: InputMaybe<Scalars['String']['input']>;
  first?: InputMaybe<Scalars['Int']['input']>;
  status: InputMaybe<Scalars['String']['input']>;
};


export type QueryUsageSummaryArgs = {
  from: InputMaybe<Scalars['String']['input']>;
  to: InputMaybe<Scalars['String']['input']>;
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

export type RecordDeletionRequestInput = {
  clientMutationId: Scalars['String']['input'];
  inspectionId: Scalars['ID']['input'];
  reason: InputMaybe<Scalars['String']['input']>;
};

export type RegisterAssetInput = {
  asset: AssetInput;
  clientMutationId: Scalars['String']['input'];
};

export type ReopenProjectInput = {
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
  projectId: Scalars['ID']['input'];
  reason: Scalars['String']['input'];
};

export type Report = {
  __typename?: 'Report';
  canonicalJSON: Scalars['JSON']['output'];
  classification: Scalars['String']['output'];
  createdAt: Scalars['String']['output'];
  html: Scalars['String']['output'];
  htmlDigest: Scalars['String']['output'];
  id: Scalars['ID']['output'];
  inspectionId: Scalars['ID']['output'];
  jsonDigest: Scalars['String']['output'];
  mode: Scalars['String']['output'];
  projectId: Maybe<Scalars['ID']['output']>;
  version: Scalars['Int']['output'];
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

export type RequestInvitationOtpInput = {
  clientMutationId: Scalars['String']['input'];
  linkToken: Scalars['String']['input'];
};

export type RequestRecaptureInput = {
  clientMutationId: Scalars['String']['input'];
  deadlineAt: Scalars['String']['input'];
  inspectionId: Scalars['ID']['input'];
  items: Array<RecaptureItemInput>;
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
  referenceVersionId: Maybe<Scalars['ID']['output']>;
  reminderOffsetsMinutes: Array<Scalars['Int']['output']>;
  rrule: Scalars['String']['output'];
  startsAt: Scalars['String']['output'];
  status: Scalars['String']['output'];
  templateId: Scalars['ID']['output'];
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

export type StageTransition = {
  __typename?: 'StageTransition';
  fromState: Maybe<Scalars['String']['output']>;
  id: Scalars['ID']['output'];
  occurredAt: Scalars['String']['output'];
  reason: Maybe<Scalars['String']['output']>;
  stageId: Maybe<Scalars['ID']['output']>;
  toState: Scalars['String']['output'];
};

export type StartProjectStageInput = {
  clientMutationId: Scalars['String']['input'];
  deadlineAt: Scalars['String']['input'];
  dueAt: Scalars['String']['input'];
  expectedProjectVersion: Scalars['Int']['input'];
  expectedStageVersion: Scalars['Int']['input'];
  projectId: Scalars['ID']['input'];
  referenceVersionId: InputMaybe<Scalars['ID']['input']>;
  reminderInstants: Array<Scalars['String']['input']>;
  stageId: Scalars['ID']['input'];
};

export type Submission = {
  __typename?: 'Submission';
  complete: Scalars['Boolean']['output'];
  id: Scalars['ID']['output'];
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

export type TenantPayload = {
  __typename?: 'TenantPayload';
  clientMutationId: Scalars['String']['output'];
  tenant: Maybe<Tenant>;
  userErrors: Array<UserError>;
};

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

export type UpdateAssetInput = {
  asset: AssetInput;
  assetId: Scalars['ID']['input'];
  clientMutationId: Scalars['String']['input'];
  expectedVersion: Scalars['Int']['input'];
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

export type UpsertBusinessUnitInput = {
  businessUnitId: InputMaybe<Scalars['ID']['input']>;
  clientMutationId: Scalars['String']['input'];
  code: Scalars['String']['input'];
  expectedTenantVersion: InputMaybe<Scalars['Int']['input']>;
  expectedVersion: InputMaybe<Scalars['Int']['input']>;
  name: Scalars['String']['input'];
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
  cost: Scalars['Float']['output'];
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
};

export type VerifyContactInput = {
  clientMutationId: Scalars['String']['input'];
  contactId: Scalars['ID']['input'];
  verified: Scalars['Boolean']['input'];
};

export type VerifyInvitationOtpInput = {
  clientMutationId: Scalars['String']['input'];
  code: Scalars['String']['input'];
  linkToken: Scalars['String']['input'];
};
