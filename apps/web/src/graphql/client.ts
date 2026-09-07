import { getAccessToken } from "@/auth/session";
import { getCaptureCsrfToken } from "@/auth/capture-session";

export type Zone = "dashboard" | "capture";
export type GraphQLFailure = { message: string; code?: string };
type GraphQLResponse<T> = { data?: T; errors?: Array<{ message: string; extensions?: { code?: string } }> };

const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";

export async function graphql<T>(zone: Zone, query: string, variables?: Record<string, unknown>): Promise<T> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (zone === "dashboard") {
    const token = getAccessToken();
    if (!token) throw { message: "Autenticação necessária", code: "UNAUTHENTICATED" } satisfies GraphQLFailure;
    headers.Authorization = `Bearer ${token}`;
  }
  if (zone === "capture") {
    const csrf = getCaptureCsrfToken();
    if (csrf) headers["X-CSRF-Token"] = csrf;
  }
  const response = await fetch(endpoint, {
    method: "POST", credentials: zone === "capture" ? "include" : "omit", headers,
    body: JSON.stringify({ query, variables })
  });
  const body = await response.json() as GraphQLResponse<T>;
  if (!response.ok || body.errors?.length) {
    throw { message: body.errors?.[0]?.message ?? "Não foi possível concluir a solicitação", code: body.errors?.[0]?.extensions?.code } satisfies GraphQLFailure;
  }
  if (!body.data) throw { message: "Resposta vazia da API" } satisfies GraphQLFailure;
  return body.data;
}

export const captureOperations = {
  exchange: `mutation Exchange($linkToken:String!,$id:String!){ requestInvitationOtp(input:{linkToken:$linkToken,clientMutationId:$id}){status userErrors{message code}} }`,
  verifyOTP: `mutation Verify($linkToken:String!,$code:String!,$id:String!){ verifyInvitationOtp(input:{linkToken:$linkToken,code:$code,clientMutationId:$id}){status csrfToken expiresAt userErrors{message code}} }`,
  bootstrap: `query Capture { externalCapture { responsibilityId recaptureRequestId status confirmationOnly kind disclosureVersion reference policy requirements { key section label instructions evidenceKind required minimumMedia maximumMedia descriptionRequired captureSourcePolicy comparisonTarget impossibilityAllowed } answers { requirementKey mediaIds impossibilityReason version } } }`,
  accept: `mutation Accept($disclosureVersion:String!,$photoProcessing:Boolean!,$aiAnalysis:Boolean!,$gpsUse:Boolean!,$id:String!){ acceptProcessing(input:{disclosureVersion:$disclosureVersion,photoProcessing:$photoProcessing,aiAnalysis:$aiAnalysis,gpsUse:$gpsUse,clientMutationId:$id}){status userErrors{message code}} }`,
  createUpload: `mutation CreateUpload($contentType:String!,$size:Int!,$hash:String!,$id:String!){ createMediaUpload(input:{contentType:$contentType,sizeBytes:$size,sha256:$hash,clientMutationId:$id}){upload{mediaId uploadId expiresAt partSizeBytes} userErrors{message code}} }`,
  parts: `mutation Parts($mediaId:ID!,$parts:[Int!]!,$id:String!){ presignMediaParts(input:{mediaId:$mediaId,partNumbers:$parts,clientMutationId:$id}){parts{partNumber url expiresAt} userErrors{message code}} }`,
  completeUpload: `mutation Complete($mediaId:ID!,$parts:[CompletedPartInput!]!,$id:String!){ completeMediaUpload(input:{mediaId:$mediaId,parts:$parts,clientMutationId:$id}){media{id status flags} userErrors{message code}} }`,
  metadata: `mutation Metadata($mediaId:ID!,$key:String!,$description:String!,$source:String!,$gps:CaptureGPSInput,$deviceContext:JSON,$id:String!){ saveCaptureMetadata(input:{mediaId:$mediaId,requirementKey:$key,description:$description,captureSource:$source,gps:$gps,deviceContext:$deviceContext,clientMutationId:$id}){media{id status flags} userErrors{message code}} }`,
  impossibility: `mutation Impossible($key:String!,$reason:String!,$id:String!){ declareCaptureImpossibility(input:{requirementKey:$key,reason:$reason,clientMutationId:$id}){status userErrors{message code}} }`,
  submit: `mutation Submit($confirmIncomplete:Boolean!,$id:String!){ submitCapture(input:{confirmIncomplete:$confirmIncomplete,clientMutationId:$id}){submission { id complete requiresAttention submittedAt } userErrors { message code } } }`,
  submitRecapture: `mutation SubmitRecapture($requestId:ID!,$confirmIncomplete:Boolean!,$id:String!){ submitRecapture(input:{requestId:$requestId,confirmIncomplete:$confirmIncomplete,clientMutationId:$id}){recapture{id responsibilityId status} userErrors { message code } } }`
};

export const dashboardOperations = {
  overview: `query DashboardOverview($first:Int!){ me { identityId tenantId roles memberships { id tenantId role status version scopes { kind resourceId } } effectiveScopes { kind resourceId } } tenant { id name language defaultTimezone status version } memberships(first:$first) { nodes { id tenantId role status version scopes { kind resourceId } } pageInfo { endCursor hasNextPage } } businessUnits(first:$first) { nodes { id code name status version } pageInfo { endCursor hasNextPage } } participants(first:$first) { nodes { id name segmentRole status businessUnitId version contacts { id channel value verified active } selectedContactIds } } segmentDefinitions(first:$first) { nodes { id key name activeVersionId version } } templates(first:$first) { nodes { id key name activeVersionId version } } assets(first:$first) { nodes { id name externalKey address status businessUnitId version segmentVersionId templateId latitudeE6 longitudeE6 geofenceMeters assignments { participantId role active } } } schedules(first:$first) { nodes { id assetId participantId templateId referenceVersionId rrule timezone startsAt status nextDueAt deadlineMinutes reminderOffsetsMinutes version } } projects(first:$first) { nodes { id assetId participantId templateId templateVersionId status reportMode version stages { id key label kind position status plannedAt inspectionId version } transitions { id stageId fromState toState reason occurredAt } } } inspections(first:$first) { nodes { id assetId participantId templateId templateVersionId analysisProfileVersionId projectId source sourceReason stateReason status evidenceCount dueAt deadlineAt reminderInstants version } } auditEvents(first:$first) { nodes { id action targetType targetId outcome reason correlationId occurredAt } } notificationDeliveries(first:$first) { nodes { id intentId status createdAt updatedAt } } retentionPolicies { nodes { id evidenceDays operationalDays securityDays version createdAt updatedAt } } usageSummary { from to requests inputTokens outputTokens cost } dashboardSummary { total normal attention critical pending invalidated } triageInspections(first:$first) { nodes { inspectionId projectId assetId classification status updatedAt } pageInfo { endCursor hasNextPage } } }`,
  updateTenant: `mutation UpdateTenant($input:UpdateTenantInput!){ updateTenant(input:$input) { tenant { id name language defaultTimezone status version } userErrors { field message code } clientMutationId } }`,
  createBusinessUnit: `mutation CreateBusinessUnit($input:CreateBusinessUnitInput!){ createBusinessUnit(input:$input) { businessUnit { id code name status version } userErrors { field message code } clientMutationId } }`,
  createParticipant: `mutation CreateParticipant($input:UpsertParticipantInput!){ upsertParticipant(input:$input) { participant { id businessUnitId name segmentRole status version contacts { id channel value verified active } selectedContactIds } userErrors { field message code } clientMutationId } }`,
  verifyContact: `mutation VerifyContact($input:VerifyContactInput!){ verifyContact(input:$input) { contact { id verified active } userErrors { field message code } clientMutationId } }`,
  upsertBusinessUnit: `mutation UpsertBusinessUnit($input:UpsertBusinessUnitInput!){ upsertBusinessUnit(input:$input) { businessUnit { id code name status version } userErrors { field message code } clientMutationId } }`,
  archiveBusinessUnit: `mutation ArchiveBusinessUnit($input:ArchiveBusinessUnitInput!){ archiveBusinessUnit(input:$input) { businessUnit { id code name status version } userErrors { field message code } clientMutationId } }`,
  inviteInternalUser: `mutation InviteInternalUser($input:InviteInternalUserInput!){ inviteInternalUser(input:$input) { membership { id tenantId role status version scopes { kind resourceId } } userErrors { field message code } clientMutationId } }`,
  assignRoleScopes: `mutation AssignRoleScopes($input:AssignRoleScopesInput!){ assignRoleScopes(input:$input) { membership { id tenantId role status version scopes { kind resourceId } } userErrors { field message code } clientMutationId } }`,
  disableMembership: `mutation DisableMembership($input:DisableMembershipInput!){ disableMembership(input:$input) { membership { id tenantId role status version scopes { kind resourceId } } userErrors { field message code } clientMutationId } }`,
  publishSegmentDefinition: `mutation PublishSegment($input:PublishSegmentDefinitionInput!){ publishSegmentDefinition(input:$input){ definition { id key name activeVersionId version } version { id } userErrors { field message code } clientMutationId } }`,
  activateSegmentDefinition: `mutation ActivateSegment($input:ActivateSegmentDefinitionInput!){ activateSegmentDefinition(input:$input){ definition { id key name activeVersionId version } version { id } userErrors { field message code } clientMutationId } }`,
  publishTemplateVersion: `mutation PublishTemplate($input:PublishTemplateVersionInput!){ publishTemplateVersion(input:$input){ template { id key name activeVersionId version } version { id versionNumber status } userErrors { field message code } clientMutationId } }`,
  activateTemplateVersion: `mutation ActivateTemplate($input:ActivateTemplateVersionInput!){ activateTemplateVersion(input:$input){ template { id key name activeVersionId version } version { id versionNumber status } userErrors { field message code } clientMutationId } }`,
  publishAnalysisProfile: `mutation PublishProfile($input:PublishAnalysisProfileInput!){ publishAnalysisProfile(input:$input){ profile { id key status versionNumber } userErrors { field message code } clientMutationId } }`,
  registerAsset: `mutation RegisterAsset($input:RegisterAssetInput!){ registerAsset(input:$input){ asset { id name externalKey address status businessUnitId version segmentVersionId templateId latitudeE6 longitudeE6 geofenceMeters assignments { participantId role active } } userErrors { field message code } clientMutationId } }`,
  archiveAsset: `mutation ArchiveAsset($input:ArchiveAssetInput!){ archiveAsset(input:$input){ asset { id name status version } userErrors { field message code } clientMutationId } }`,
  createSchedule: `mutation CreateSchedule($input:CreateScheduleInput!){ createSchedule(input:$input){ schedule { id assetId participantId status nextDueAt version } userErrors { field message code } clientMutationId } }`,
  updateSchedule: `mutation UpdateSchedule($input:UpdateScheduleInput!){ updateSchedule(input:$input){ schedule { id assetId participantId status nextDueAt version rrule deadlineMinutes reminderOffsetsMinutes } userErrors { field message code } clientMutationId } }`,
  cancelSchedule: `mutation CancelSchedule($input:CancelScheduleInput!){ cancelSchedule(input:$input){ schedule { id status version } userErrors { field message code } clientMutationId } }`,
  createInspection: `mutation CreateInspection($input:CreateInspectionInput!){ createInspection(input:$input){ inspection { id assetId participantId status dueAt version } userErrors { field message code } clientMutationId } }`,
  cancelInspection: `mutation CancelInspection($input:InspectionTransitionInput!){ cancelInspection(input:$input){ inspection { id status version } userErrors { field message code } clientMutationId } }`,
  invalidateInspection: `mutation InvalidateInspection($input:InvalidateInspectionInput!){ invalidateInspection(input:$input){ inspection { id status version } userErrors { field message code } clientMutationId } }`,
  createProject: `mutation CreateProject($input:CreateProjectInput!){ createProject(input:$input){ project { id assetId status reportMode version } userErrors { field message code } clientMutationId } }`,
  addExceptionalStage: `mutation AddExceptionalStage($input:AddExceptionalStageInput!){ addExceptionalStage(input:$input){ project { id status version stages { id key label kind position status plannedAt inspectionId version } } userErrors { field message code } clientMutationId } }`,
  startProjectStage: `mutation StartProjectStage($input:StartProjectStageInput!){ startProjectStage(input:$input){ project { id status version stages { id key label kind position status plannedAt inspectionId version } } userErrors { field message code } clientMutationId } }`,
  skipProjectStage: `mutation SkipProjectStage($input:SkipProjectStageInput!){ skipProjectStage(input:$input){ project { id status version stages { id key label kind position status plannedAt inspectionId version } } userErrors { field message code } clientMutationId } }`,
  closeProject: `mutation CloseProject($input:ProjectTransitionInput!){ closeProject(input:$input){ project { id status version } userErrors { field message code } clientMutationId } }`,
  reopenProject: `mutation ReopenProject($input:ReopenProjectInput!){ reopenProject(input:$input){ project { id status version } userErrors { field message code } clientMutationId } }`,
  report: `query Report($inspectionId:ID!,$version:Int){ report(inspectionId:$inspectionId,version:$version){ id inspectionId projectId version mode classification jsonDigest htmlDigest canonicalJSON html createdAt } }`,
  reportDownload: `query ReportDownload($snapshotId:ID!,$kind:String!){ reportDownload(snapshotId:$snapshotId,kind:$kind){ snapshotId kind objectKey url status sha256 } }`,
  projectTimeline: `query ProjectTimeline($projectId:ID!){ projectTimeline(projectId:$projectId){ projectId entries { stageId label status inspectionId occurredAt } } }`,
  requestRecapture: `mutation RequestRecapture($input:RequestRecaptureInput!){ requestRecapture(input:$input){ recapture { id responsibilityId status deadlineAt } userErrors { field message code } clientMutationId } }`,
  configureRetentionPolicy: `mutation ConfigureRetention($input:ConfigureRetentionPolicyInput!){ configureRetentionPolicy(input:$input){ policy { id evidenceDays operationalDays securityDays version } userErrors { field message code } clientMutationId } }`,
  recordDeletionRequest: `mutation RecordDeletion($input:RecordDeletionRequestInput!){ recordDeletionRequest(input:$input){ status requestId userErrors { field message code } clientMutationId } }`,
  applyLegalHold: `mutation ApplyLegalHold($input:LegalHoldInput!){ applyLegalHold(input:$input){ status requestId userErrors { field message code } clientMutationId } }`
  ,releaseLegalHold: `mutation ReleaseLegalHold($input:LegalHoldInput!){ releaseLegalHold(input:$input){ status requestId userErrors { field message code } clientMutationId } }`
  ,setDeliveryChannels: `mutation SetDeliveryChannels($input:SetDeliveryChannelsInput!){ setDeliveryChannels(input:$input){ participant { id version } userErrors { field message code } clientMutationId } }`
  ,inviteOriginCapture: `mutation InviteOriginCapture($input:InviteOriginCaptureInput!){ inviteOriginCapture(input:$input){ invitationId originVersionId status userErrors { field message code } clientMutationId } }`,
  activateOriginVersion: `mutation ActivateOriginVersion($input:OriginVersionInput!){ activateOriginVersion(input:$input){ version { id originId versionNumber status activatedAt } userErrors { field message code } clientMutationId } }`,
  invalidateOriginVersion: `mutation InvalidateOriginVersion($input:OriginVersionInput!){ invalidateOriginVersion(input:$input){ version { id originId versionNumber status activatedAt } userErrors { field message code } clientMutationId } }`
};
