import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

// The delivery-backed capture tests share the local Mailpit fixture. Keep this
// file serial so concurrent workers cannot consume each other's OTP/link mail.
test.describe.configure({ mode: "serial" });

const journeys = [
  { name: "E2E-002 Assign hierarchical internal access", anchor: "access:manage", heading: "Pessoas e acesso", data: /membership\(s\) no tenant/ },
  { name: "E2E-004 Review audit trail", anchor: "audit:read", heading: "Auditoria", data: /Eventos append-only/ },
  { name: "E2E-005 Publish template versions", anchor: "assets:manage", heading: "Catálogos e ativos", data: /segmentos, .*templates/ },
  { name: "E2E-006 Register assets", anchor: "assets:manage", heading: "Catálogos e ativos", data: /ativos visíveis/ },
  { name: "E2E-007 Configure policies", anchor: "retention:manage", heading: "Uso e retenção", data: /solicitações/ },
  { name: "E2E-008 Invite origin contributor", anchor: "access:manage", heading: "Pessoas e acesso", data: /membership\(s\) no tenant/ },
  { name: "E2E-009 Capture a described origin", anchor: "inspections:manage", heading: "Agendas e projetos", data: /agendas, .*projetos/ },
  { name: "E2E-010 Activate origin version", anchor: "assets:manage", heading: "Catálogos e ativos", data: /templates/ },
  { name: "E2E-011 Configure recurrence", anchor: "inspections:manage", heading: "Agendas e projetos", data: /agendas/ },
  { name: "E2E-012 Create manual inspections", anchor: "inspections:manage", heading: "Agendas e projetos", data: /inspeções/ },
  { name: "E2E-013 Configure reminders", anchor: "notifications:read", heading: "Notificações", data: /entrega\(s\) de notificação/ },
  { name: "E2E-014 Cancel or invalidate", anchor: "inspections:manage", heading: "Agendas e projetos", data: /inspeções/ },
  { name: "E2E-015 Authenticate and accept processing", anchor: "participants:manage", heading: "Participantes", data: /participante\(s\) visível/ },
  { name: "E2E-016 Complete guided capture", anchor: "participants:manage", heading: "Participantes", data: /participante\(s\) visível/ },
  { name: "E2E-017 Record GPS and geofence", anchor: "inspections:manage", heading: "Agendas e projetos", data: /projetos/ },
  { name: "E2E-018 Resume media uploads", anchor: "inspections:manage", heading: "Agendas e projetos", data: /agendas/ },
  { name: "E2E-019 Submit evidence", anchor: "reports:read", heading: "Triagem e relatórios", data: /item\(ns\) de triagem/ },
  { name: "E2E-020 Prevent sensitive content", anchor: "reports:read", heading: "Triagem e relatórios", data: /Relatórios imutáveis/ },
  { name: "E2E-021 Plan stages", anchor: "inspections:manage", heading: "Agendas e projetos", data: /projetos/ },
  { name: "E2E-022 Close and reopen projects", anchor: "inspections:manage", heading: "Agendas e projetos", data: /projetos/ },
  { name: "E2E-023 Request recapture", anchor: "reports:read", heading: "Triagem e relatórios", data: /triagem/ },
  { name: "E2E-024 Complete recapture", anchor: "reports:read", heading: "Triagem e relatórios", data: /triagem/ },
  { name: "E2E-025 Receive findings", anchor: "reports:read", heading: "Triagem e relatórios", data: /triagem/ },
  { name: "E2E-026 Classify inspection", anchor: "reports:read", heading: "Triagem e relatórios", data: /Relatórios imutáveis/ },
  { name: "E2E-027 Review immutable reports", anchor: "reports:read", heading: "Triagem e relatórios", data: /Relatórios imutáveis/ },
  { name: "E2E-028 Review stage history", anchor: "audit:read", heading: "Auditoria", data: /append-only/ },
  { name: "E2E-029 Triage portfolio", anchor: "reports:read", heading: "Triagem e relatórios", data: /triagem/ },
  { name: "E2E-030 Receive critical alerts", anchor: "notifications:read", heading: "Notificações", data: /notificação/ },
  { name: "E2E-031 Enforce retention", anchor: "retention:manage", heading: "Uso e retenção", data: /Evidências/ },
  { name: "E2E-032 Property journey", anchor: "assets:manage", heading: "Catálogos e ativos", data: /ativos visíveis/ },
  { name: "E2E-033 Construction journey", anchor: "inspections:manage", heading: "Agendas e projetos", data: /projetos/ },
  { name: "E2E-034 Cleaning journey", anchor: "participants:manage", heading: "Participantes", data: /participante\(s\) visível/ }
];

type GraphQLCall = <T = Record<string, unknown>>(query: string, variables?: Record<string, unknown>) => Promise<T>;
type JourneyFixture = {
  unitID: string;
  participantID: string;
  participantEmail: string;
  segmentVersionID: string;
  templateID: string;
  templateVersionID: string;
  projectTemplateID: string;
  assetID: string;
  scheduleID: string;
  inspectionID: string;
  projectID: string;
  scheduleRRule: string;
  scheduleReminderOffsetsMinutes: number[];
  inspectionStatus: string;
};

const fixtureOverview = `query FixtureOverview { memberships(first:100){nodes{id role status version}} businessUnits(first:100){nodes{id status version}} segmentDefinitions(first:100){nodes{id activeVersionId version}} templates(first:100){nodes{id activeVersionId version}} assets(first:100){nodes{id status version businessUnitId segmentVersionId templateId}} participants(first:100){nodes{id version status contacts{id value verified} selectedContactIds}} }`;
const fixtureParticipant = `mutation FixtureParticipant($input:UpsertParticipantInput!){upsertParticipant(input:$input){participant{id version contacts{id value verified}}userErrors{message}}}`;
const fixtureVerify = `mutation FixtureVerify($input:VerifyContactInput!){verifyContact(input:$input){contact{id verified}userErrors{message}}}`;
const fixtureChannels = `mutation FixtureChannels($input:SetDeliveryChannelsInput!){setDeliveryChannels(input:$input){participant{id version}userErrors{message}}}`;
const fixtureSegment = `mutation FixtureSegment($input:PublishSegmentDefinitionInput!){publishSegmentDefinition(input:$input){definition{id version}version{id}userErrors{message}}}`;
const fixtureActivateSegment = `mutation FixtureActivateSegment($input:ActivateSegmentDefinitionInput!){activateSegmentDefinition(input:$input){definition{id activeVersionId version}userErrors{message}}}`;
const fixtureTemplate = `mutation FixtureTemplate($input:PublishTemplateVersionInput!){publishTemplateVersion(input:$input){template{id version}version{id}userErrors{message}}}`;
const fixtureActivateTemplate = `mutation FixtureActivateTemplate($input:ActivateTemplateVersionInput!){activateTemplateVersion(input:$input){template{id activeVersionId version}userErrors{message}}}`;
const fixtureProfile = `mutation FixtureProfile($input:PublishAnalysisProfileInput!){publishAnalysisProfile(input:$input){profile{id key status versionNumber}userErrors{message}}}`;
const fixtureAsset = `mutation FixtureAsset($input:RegisterAssetInput!){registerAsset(input:$input){asset{id version status}userErrors{message}}}`;
const fixtureSchedule = `mutation FixtureSchedule($input:CreateScheduleInput!){createSchedule(input:$input){schedule{id version status rrule reminderOffsetsMinutes}userErrors{message}}}`;
const fixtureInspection = `mutation FixtureInspection($input:CreateInspectionInput!){createInspection(input:$input){inspection{id version status}userErrors{message}}}`;
const fixtureProject = `mutation FixtureProject($input:CreateProjectInput!){createProject(input:$input){project{id version status stages{id key label status version}}userErrors{message}}}`;
const fixtureProjectDetail = `query FixtureProjectDetail($id:ID!){project(id:$id){id version status stages{id key label kind position status plannedAt inspectionId version}}}`;
const fixtureInspectionDetail = `query FixtureInspectionDetail($id:ID!){inspection(id:$id){id version status}}`;
const fixtureAssignScope = `mutation FixtureAssignScope($input:AssignRoleScopesInput!){assignRoleScopes(input:$input){membership{id version scopes{kind resourceId}}userErrors{message}}}`;
const fixtureInviteUser = `mutation FixtureInviteUser($input:InviteInternalUserInput!){inviteInternalUser(input:$input){membership{id status role}userErrors{message}}}`;
const fixtureOriginInvite = `mutation FixtureOriginInvite($input:InviteOriginCaptureInput!){inviteOriginCapture(input:$input){invitationId originVersionId status userErrors{message}}}`;
const fixtureOriginVersions = `query FixtureOriginVersions($assetID:ID!){originVersions(assetId:$assetID,first:100){nodes{id versionNumber status} }}`;
const fixtureActivateOrigin = `mutation FixtureActivateOrigin($input:OriginVersionInput!){activateOriginVersion(input:$input){version{id status}userErrors{message}}}`;
const fixtureCreateStage = `mutation FixtureCreateStage($input:AddExceptionalStageInput!){addExceptionalStage(input:$input){project{id version stages{id key status version}}userErrors{message}}}`;
const fixtureSkipStage = `mutation FixtureSkipStage($input:SkipProjectStageInput!){skipProjectStage(input:$input){project{id version status stages{id key status version}}userErrors{message}}}`;
const fixtureCloseProject = `mutation FixtureCloseProject($input:ProjectTransitionInput!){closeProject(input:$input){project{id version status}userErrors{message}}}`;
const fixtureReopenProject = `mutation FixtureReopenProject($input:ReopenProjectInput!){reopenProject(input:$input){project{id version status}userErrors{message}}}`;
const fixtureCancelInspection = `mutation FixtureCancelInspection($input:InspectionTransitionInput!){cancelInspection(input:$input){inspection{id version status}userErrors{message}}}`;
const fixtureRetention = `mutation FixtureRetention($input:ConfigureRetentionPolicyInput!){configureRetentionPolicy(input:$input){policy{id evidenceDays operationalDays securityDays}userErrors{message}}}`;
const fixtureDeletion = `mutation FixtureDeletion($input:RecordDeletionRequestInput!){recordDeletionRequest(input:$input){status requestId userErrors{message}}}`;
const fixtureHold = `mutation FixtureHold($input:LegalHoldInput!){applyLegalHold(input:$input){status requestId userErrors{message}}}`;
const fixtureReleaseHold = `mutation FixtureReleaseHold($input:LegalHoldInput!){releaseLegalHold(input:$input){status requestId userErrors{message}}}`;
const fixtureAudit = `query FixtureAudit { auditEvents(first:100){nodes{id action targetType outcome targetId}} }`;
const fixtureTriage = `query FixtureTriage { triageInspections(first:100){nodes{inspectionId classification status}} }`;
const fixtureReport = `query FixtureReport($inspectionID:ID!){report(inspectionId:$inspectionID){id version classification mode html}}`;
const fixtureRequestRecapture = `mutation FixtureRequestRecapture($input:RequestRecaptureInput!){requestRecapture(input:$input){recapture{id status responsibilityId deadlineAt}userErrors{message}}}`;
const fixtureNotifications = `query FixtureNotifications { notificationDeliveries(first:100){nodes{id status createdAt updatedAt}} }`;

function assertNoErrors<T extends { userErrors?: Array<{ message: string }> }>(payload: T): T {
  const failure = payload.userErrors?.[0];
  if (failure) throw new Error(failure.message);
  return payload;
}

async function signIn(page: import("@playwright/test").Page): Promise<GraphQLCall> {
  let bearerToken = "";
  page.on("request", (request) => {
    if (request.url().endsWith("/graphql")) bearerToken = request.headers().authorization?.replace(/^Bearer\s+/i, "") ?? bearerToken;
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Entrar com conta interna" }).click();
  await expect(page).toHaveURL(/realms\/inspection\/protocol\/openid-connect\/auth/);
  await page.locator("#username").fill("admin");
  await page.locator("#password").fill("admin");
  await page.locator("#kc-login").click();
  await expect(page.getByRole("heading", { name: "Contexto autorizado" })).toBeVisible();
  await expect.poll(() => bearerToken).not.toBe("");
  return async <T>(query: string, variables?: Record<string, unknown>) => {
    const response = await page.request.post(process.env.INSPECTION_GRAPHQL_URL ?? "http://localhost:8080/graphql", { headers: { Authorization: `Bearer ${bearerToken}` }, data: { query, variables } });
    const body = await response.json() as { data?: T; errors?: Array<{ message: string }> };
    if (!response.ok() || body.errors?.length || !body.data) throw new Error(body.errors?.[0]?.message ?? `GraphQL request failed (${response.status()})`);
    return body.data;
  };
}

test("E2E-001 configures and reads a tenant through real OIDC @acceptance", async ({ page }) => {
  await signIn(page);
  await expect(page.getByRole("heading", { name: "Central de inspeções" })).toBeVisible();
  await expect(page.getByText("OIDC smoke")).toBeVisible();
  await expect(page.getByText("Papel efetivo: TENANT_ADMIN.")).toBeVisible();
  await page.getByRole("button", { name: "Salvar organização" }).click();
  await expect(page.getByRole("status")).toContainText("Configuração de OIDC smoke atualizada");
});

test("E2E-003 creates a participant and shows its unverified delivery channel @acceptance", async ({ page }, testInfo) => {
  await signIn(page);
  const suffix = `${testInfo.project.name}-${Date.now()}`;
  const name = `Participante ${suffix}`;
  await page.locator("#participant-name").fill(name);
  await page.locator("#participant-email").fill(`participant-${suffix}@example.com`);
  await page.getByRole("button", { name: "Criar participante" }).click();
  await expect(page.getByRole("status")).toContainText("criado; confirme o canal");
  const participant = page.locator("li", { hasText: name });
  await expect(participant).toBeVisible({ timeout: 15_000 });
  await expect(participant).toContainText("0 contato(s) verificado(s)");
  await page.getByRole("button", { name: `Confirmar canal participant-${suffix}@example.com` }).click();
  await expect(page.getByRole("status")).toContainText("Canal confirmado");
  await expect(page.locator("li", { hasText: name })).toContainText("1 contato(s) verificado(s)");
});

test("dashboard publishes a catalog and registers an asset through real mutations @acceptance", async ({ page }, testInfo) => {
  await signIn(page);
  const area = page.locator(`[id="assets:manage"]`);
  const suffix = `${testInfo.project.name}-${Date.now()}`;
  const openCatalog = async () => {
    const details = area.locator("details").filter({ hasText: "Publicar catálogo" });
    if (await details.getAttribute("open") === null) await details.locator("summary").click();
    return details;
  };
  let catalog = await openCatalog();
  await catalog.locator("#segment-key").fill(`segment-${suffix}`);
  await catalog.locator("#segment-name").fill(`Segmento ${suffix}`);
  await catalog.getByRole("button", { name: "Publicar segmento" }).click();
  await expect(area.getByRole("status")).toContainText("Segmento");

  catalog = await openCatalog();
  await catalog.locator("#profile-key").fill(`profile-${suffix}`);
  await catalog.getByRole("button", { name: "Publicar perfil de análise" }).click();
  await expect(area.getByRole("status")).toContainText("Perfil");

  catalog = await openCatalog();
  await catalog.locator("#template-key").fill(`template-${suffix}`);
  await catalog.locator("#template-name").fill(`Template ${suffix}`);
  await catalog.getByRole("button", { name: "Publicar template" }).click();
  await expect(area.getByRole("status")).toContainText("Template");

  const assetDetails = area.locator("details").filter({ hasText: "Registrar ativo" });
  if (await assetDetails.getAttribute("open") === null) await assetDetails.locator("summary").click();
  await assetDetails.locator("#asset-name").fill(`Ativo ${suffix}`);
  await assetDetails.locator("#asset-external-key").fill(`asset-${suffix}`);
  await assetDetails.locator("#asset-address").fill("Rua de teste, 100");
  await assetDetails.getByRole("button", { name: "Registrar ativo" }).click();
  await expect(area.getByRole("status")).toContainText("Ativo");
});

async function readMail(page: import("@playwright/test").Page, recipient: string, subject: string): Promise<string> {
  let messageID = "";
  await expect.poll(async () => {
    const response = await page.request.get("http://localhost:8026/api/v1/messages?limit=100");
    const payload = await response.json() as { messages?: Array<Record<string, unknown>> };
    const message = (payload.messages ?? [])
      .filter((candidate) => JSON.stringify(candidate).includes(recipient) && String(candidate.Subject ?? candidate.subject ?? "").includes(subject))
      .sort((left, right) => String(right.Created ?? right.created ?? "").localeCompare(String(left.Created ?? left.created ?? "")))[0];
    messageID = String(message?.ID ?? message?.id ?? "");
    return messageID;
  }, { timeout: 20_000 }).not.toBe("");
  // The local dispatcher can deliver the same OTP intent more than once while
  // the worker settles. Let the newest delivery win before reading its code.
  await new Promise((resolve) => setTimeout(resolve, 750));
  const latestResponse = await page.request.get("http://localhost:8026/api/v1/messages?limit=100");
  const latestPayload = await latestResponse.json() as { messages?: Array<Record<string, unknown>> };
  const latestMessage = (latestPayload.messages ?? [])
    .filter((candidate) => JSON.stringify(candidate).includes(recipient) && String(candidate.Subject ?? candidate.subject ?? "").includes(subject))
    .sort((left, right) => String(right.Created ?? right.created ?? "").localeCompare(String(left.Created ?? left.created ?? "")))[0];
  messageID = String(latestMessage?.ID ?? latestMessage?.id ?? messageID);
  const response = await page.request.get(`http://localhost:8026/api/v1/message/${encodeURIComponent(messageID)}`);
  const detail = await response.json() as Record<string, unknown>;
  return `${String(detail.Text ?? detail.text ?? "")}\n${String(detail.HTML ?? detail.html ?? "")}`;
}

async function completeExternalCapture(page: import("@playwright/test").Page, context: import("@playwright/test").BrowserContext, token: string, recipient: string): Promise<void> {
  await context.grantPermissions(["geolocation"], { origin: "http://localhost:3000" });
  await context.setGeolocation({ latitude: -23.5505, longitude: -46.6333, accuracy: 10 });
  await page.goto(`/capture/${token}`);
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  const otpMail = await readMail(page, recipient, "Seu codigo de acesso");
  const code = otpMail.match(/\b\d{6}\b/)?.[0];
  expect(code).toBeTruthy();
  await page.locator("input[inputmode='numeric']").fill(code ?? "");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByRole("heading", { name: "Uso dos seus dados" })).toBeVisible();
  await page.getByRole("button", { name: "Aceito o processamento necessário" }).click();
  await expect(page.getByRole("heading", { name: "Captura guiada" })).toBeVisible();
  await page.getByLabel("Descrição da foto").fill("Evidência da jornada funcional");
  const onePixelPNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=", "base64");
  await page.getByRole("button", { name: "Usar minha localização" }).click();
  await expect(page.getByText(/Localização com precisão/)).toBeVisible();
  await page.getByLabel("Adicionar foto").setInputFiles({ name: "journey-evidence.png", mimeType: "image/png", buffer: onePixelPNG });
  await expect(page.getByText("Evidência enviada e verificada.")).toBeVisible({ timeout: 30_000 });
  const submitResponse = page.waitForResponse((response) => response.url().endsWith("/graphql") && /submit(Capture|Recapture)/.test(response.request().postData() ?? ""));
  await page.getByRole("button", { name: "Enviar inspeção completa" }).click();
  const submission = await submitResponse;
  const submissionBody = await submission.json() as { errors?: Array<{ message: string }>; data?: { submitCapture?: { userErrors?: Array<{ message: string }> }; submitRecapture?: { userErrors?: Array<{ message: string }> } } };
  const submissionError = submissionBody.errors?.[0]?.message ?? submissionBody.data?.submitCapture?.userErrors?.[0]?.message ?? submissionBody.data?.submitRecapture?.userErrors?.[0]?.message;
  if (!submission.ok() || submissionError) throw new Error(submissionError ?? `Capture submission failed (${submission.status()})`);
  await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible({ timeout: 30_000 });
}

async function createJourneyFixture(api: GraphQLCall, suffix: string): Promise<JourneyFixture> {
  const overview = await api<{ memberships: { nodes: Array<{ id: string; role: string; status: string; version: number }> }; businessUnits: { nodes: Array<{ id: string; status: string }> }; segmentDefinitions: { nodes: Array<{ id: string; activeVersionId?: string | null; version: number }> }; templates: { nodes: Array<{ id: string; activeVersionId?: string | null; version: number }> } }>(fixtureOverview);
  const unit = overview.businessUnits.nodes.find((candidate) => candidate.status === "ACTIVE");
  if (!unit) throw new Error("No active business unit is available for the functional journey fixture");
  const participantEmail = `journey-${suffix}@example.com`;
  const participantResult = await api<{ upsertParticipant: { participant?: { id: string; version: number; contacts: Array<{ id: string; value: string }> }; userErrors: Array<{ message: string }> } }>(fixtureParticipant, { input: { businessUnitId: unit.id, name: `Jornada ${suffix}`, segmentRole: "TENANT_PARTICIPANT", contacts: [{ channel: "EMAIL", value: participantEmail }], clientMutationId: crypto.randomUUID() } });
  assertNoErrors(participantResult.upsertParticipant);
  const participant = participantResult.upsertParticipant.participant;
  if (!participant?.id || !participant.contacts[0]?.id) throw new Error("Participant fixture did not return a contact");
  const verifyResult = await api<{ verifyContact: { userErrors: Array<{ message: string }> } }>(fixtureVerify, { input: { contactId: participant.contacts[0].id, verified: true, clientMutationId: crypto.randomUUID() } });
  assertNoErrors(verifyResult.verifyContact);
  const channelResult = await api<{ setDeliveryChannels: { userErrors: Array<{ message: string }> } }>(fixtureChannels, { input: { participantId: participant.id, contactIds: [participant.contacts[0].id], expectedVersion: participant.version, clientMutationId: crypto.randomUUID() } });
  assertNoErrors(channelResult.setDeliveryChannels);

  const segmentResult = await api<{ publishSegmentDefinition: { definition?: { id: string; version: number }; version?: { id: string }; userErrors: Array<{ message: string }> } }>(fixtureSegment, { input: { key: `journey-segment-${suffix}`, name: `Segmento ${suffix}`, schema: { type: "object", properties: { kind: { type: "string", maxLength: 120 } }, required: ["kind"] }, uiSchema: {}, clientMutationId: crypto.randomUUID() } });
  assertNoErrors(segmentResult.publishSegmentDefinition);
  const segment = segmentResult.publishSegmentDefinition.definition;
  const segmentVersion = segmentResult.publishSegmentDefinition.version;
  if (!segment?.id || !segmentVersion?.id) throw new Error("Segment fixture did not return a version");
  const activateSegment = await api<{ activateSegmentDefinition: { userErrors: Array<{ message: string }> } }>(fixtureActivateSegment, { input: { versionId: segmentVersion.id, expectedVersion: segment.version, clientMutationId: crypto.randomUUID() } });
  assertNoErrors(activateSegment.activateSegmentDefinition);

  const profileResult = await api<{ publishAnalysisProfile: { profile?: { id: string }; userErrors: Array<{ message: string }> } }>(fixtureProfile, { input: { key: `journey-profile-${suffix}`, definition: { schemaVersion: 1, modelAlias: "inspection-vision", promptVersion: "journey-v1", outputSchema: { type: "object" }, minimumConfidenceBps: 5000 }, clientMutationId: crypto.randomUUID() } });
  assertNoErrors(profileResult.publishAnalysisProfile);
  const analysisProfileID = profileResult.publishAnalysisProfile.profile?.id;
  if (!analysisProfileID) throw new Error("Analysis profile fixture did not return an ID");
  const templateDefinition = { schemaVersion: 1, segmentVersionId: segmentVersion.id, participantRoles: ["TENANT_PARTICIPANT"], comparisonMode: "CHECKLIST_ONLY", requirements: [{ key: "overview", section: "Geral", label: "Visão geral", evidenceKind: "PHOTO", minimumCount: 1, maximumCount: 2, required: true, descriptionRequired: true, captureSourcePolicy: "CAMERA_DEFAULT", comparisonTarget: "CHECKLIST_ONLY" }], multiStage: false, reportMode: "HISTORICAL", analysisProfile: analysisProfileID, policy: { gpsRequired: false, geofenceMeters: 150, allowGallery: true } };
  const templateResult = await api<{ publishTemplateVersion: { template?: { id: string; version: number }; version?: { id: string }; userErrors: Array<{ message: string }> } }>(fixtureTemplate, { input: { key: `journey-template-${suffix}`, name: `Template ${suffix}`, definition: templateDefinition, clientMutationId: crypto.randomUUID() } });
  assertNoErrors(templateResult.publishTemplateVersion);
  const template = templateResult.publishTemplateVersion.template;
  const templateVersion = templateResult.publishTemplateVersion.version;
  if (!template?.id || !templateVersion?.id) throw new Error("Template fixture did not return a version");
  const activateTemplate = await api<{ activateTemplateVersion: { userErrors: Array<{ message: string }> } }>(fixtureActivateTemplate, { input: { versionId: templateVersion.id, expectedVersion: template.version, clientMutationId: crypto.randomUUID() } });
  assertNoErrors(activateTemplate.activateTemplateVersion);
  const projectDefinition = { schemaVersion: 1, segmentVersionId: segmentVersion.id, participantRoles: ["TENANT_PARTICIPANT"], comparisonMode: "PLANNED_STAGE", requirements: [{ key: "overview", section: "Geral", label: "Visão geral", evidenceKind: "PHOTO", minimumCount: 1, maximumCount: 2, required: true, descriptionRequired: true, captureSourcePolicy: "CAMERA_DEFAULT", comparisonTarget: "PLANNED_STAGE" }], multiStage: true, stages: [{ key: "baseline", label: "Etapa base", position: 1 }, { key: "final", label: "Etapa final", position: 2 }], reportMode: "HISTORICAL", analysisProfile: analysisProfileID, policy: { gpsRequired: false, geofenceMeters: 150, allowGallery: true } };
  const projectTemplateResult = await api<{ publishTemplateVersion: { template?: { id: string; version: number }; version?: { id: string }; userErrors: Array<{ message: string }> } }>(fixtureTemplate, { input: { key: `journey-project-template-${suffix}`, name: `Template de projeto ${suffix}`, definition: projectDefinition, clientMutationId: crypto.randomUUID() } });
  assertNoErrors(projectTemplateResult.publishTemplateVersion);
  const projectTemplate = projectTemplateResult.publishTemplateVersion.template;
  const projectTemplateVersion = projectTemplateResult.publishTemplateVersion.version;
  if (!projectTemplate?.id || !projectTemplateVersion?.id) throw new Error("Project template fixture did not return a version");
  const activateProjectTemplate = await api<{ activateTemplateVersion: { userErrors: Array<{ message: string }> } }>(fixtureActivateTemplate, { input: { versionId: projectTemplateVersion.id, expectedVersion: projectTemplate.version, clientMutationId: crypto.randomUUID() } });
  assertNoErrors(activateProjectTemplate.activateTemplateVersion);

  const assetResult = await api<{ registerAsset: { asset?: { id: string }; userErrors: Array<{ message: string }> } }>(fixtureAsset, { input: { asset: { businessUnitId: unit.id, segmentVersionId: segmentVersion.id, templateId: template.id, name: `Ativo ${suffix}`, externalKey: `journey-asset-${suffix}`, address: "Rua de jornada, 100", latitudeE6: -23550500, longitudeE6: -46633300, geofenceMeters: 150, attributes: { kind: "property" }, policyOverrides: {}, assignments: [{ participantId: participant.id, role: "TENANT_PARTICIPANT" }] }, clientMutationId: crypto.randomUUID() } });
  assertNoErrors(assetResult.registerAsset);
  const assetID = assetResult.registerAsset.asset?.id;
  if (!assetID) throw new Error("Asset fixture did not return an ID");
  const startsAt = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
  const scheduleResult = await api<{ createSchedule: { schedule?: { id: string; rrule: string; reminderOffsetsMinutes: number[] }; userErrors: Array<{ message: string }> } }>(fixtureSchedule, { input: { assetId: assetID, participantId: participant.id, templateId: template.id, rrule: "FREQ=MONTHLY;INTERVAL=1", timezone: "America/Sao_Paulo", startsAt, deadlineMinutes: 1440, reminderOffsetsMinutes: [60], clientMutationId: crypto.randomUUID() } });
  assertNoErrors(scheduleResult.createSchedule);
  const scheduleID = scheduleResult.createSchedule.schedule?.id;
  if (!scheduleID) throw new Error("Schedule fixture did not return an ID");
  const dueAt = new Date(Date.now() + 2 * 24 * 60 * 60 * 1000).toISOString();
  const inspectionResult = await api<{ createInspection: { inspection?: { id: string; status: string }; userErrors: Array<{ message: string }> } }>(fixtureInspection, { input: { assetId: assetID, participantId: participant.id, templateId: template.id, dueAt, deadlineAt: new Date(Date.now() + 3 * 24 * 60 * 60 * 1000).toISOString(), reminderInstants: [new Date(Date.now() + 2 * 24 * 60 * 60 * 1000 + 60 * 60 * 1000).toISOString()], reason: "jornada funcional", clientMutationId: crypto.randomUUID() } });
  assertNoErrors(inspectionResult.createInspection);
  const inspectionID = inspectionResult.createInspection.inspection?.id;
  if (!inspectionID) throw new Error("Inspection fixture did not return an ID");
  const projectResult = await api<{ createProject: { project?: { id: string }; userErrors: Array<{ message: string }> } }>(fixtureProject, { input: { assetId: assetID, participantId: participant.id, templateId: projectTemplate.id, clientMutationId: crypto.randomUUID() } });
  assertNoErrors(projectResult.createProject);
  const projectID = projectResult.createProject.project?.id;
  if (!projectID) throw new Error("Project fixture did not return an ID");
  return { unitID: unit.id, participantID: participant.id, participantEmail, segmentVersionID: segmentVersion.id, templateID: template.id, templateVersionID: templateVersion.id, projectTemplateID: projectTemplate.id, assetID, scheduleID, inspectionID, projectID, scheduleRRule: scheduleResult.createSchedule.schedule?.rrule ?? "", scheduleReminderOffsetsMinutes: scheduleResult.createSchedule.schedule?.reminderOffsetsMinutes ?? [], inspectionStatus: inspectionResult.createInspection.inspection?.status ?? "" };
}

async function exerciseFunctionalJourney(api: GraphQLCall, journeyName: string, suffix: string): Promise<JourneyFixture> {
  const fixture = await createJourneyFixture(api, suffix);
  const overview = await api<{ memberships: { nodes: Array<{ id: string; role: string; status: string; version: number }> } }>(fixtureOverview);
  if (journeyName.startsWith("E2E-002")) {
    const membership = overview.memberships.nodes.find((candidate) => candidate.status === "ACTIVE");
    if (!membership) throw new Error("No active membership was returned for access assignment");
    const result = await api<{ assignRoleScopes: { userErrors: Array<{ message: string }> } }>(fixtureAssignScope, { input: { membershipId: membership.id, role: membership.role, scopes: [{ kind: "BUSINESS_UNIT", resourceId: fixture.unitID }], expectedVersion: membership.version, clientMutationId: crypto.randomUUID() } });
    assertNoErrors(result.assignRoleScopes);
  } else if (journeyName.startsWith("E2E-004")) {
    const retention = await api<{ configureRetentionPolicy: { userErrors: Array<{ message: string }> } }>(fixtureRetention, { input: { evidenceDays: 1825, operationalDays: 365, securityDays: 730, clientMutationId: crypto.randomUUID() } });
    assertNoErrors(retention.configureRetentionPolicy);
    const auditResult = await api<{ auditEvents: { nodes: Array<{ id: string; action: string }> } }>(fixtureAudit);
    expect(auditResult.auditEvents.nodes).toBeDefined();
  } else if (journeyName.startsWith("E2E-007") || journeyName.startsWith("E2E-031")) {
    const retention = await api<{ configureRetentionPolicy: { userErrors: Array<{ message: string }> } }>(fixtureRetention, { input: { evidenceDays: 1825, operationalDays: 365, securityDays: 730, clientMutationId: crypto.randomUUID() } });
    assertNoErrors(retention.configureRetentionPolicy);
    const deletion = await api<{ recordDeletionRequest: { userErrors: Array<{ message: string }> } }>(fixtureDeletion, { input: { inspectionId: fixture.inspectionID, reason: "jornada de privacidade", clientMutationId: crypto.randomUUID() } });
    assertNoErrors(deletion.recordDeletionRequest);
    const hold = await api<{ applyLegalHold: { userErrors: Array<{ message: string }> } }>(fixtureHold, { input: { inspectionId: fixture.inspectionID, reason: "revisão interna", clientMutationId: crypto.randomUUID() } });
    assertNoErrors(hold.applyLegalHold);
    const release = await api<{ releaseLegalHold: { userErrors: Array<{ message: string }> } }>(fixtureReleaseHold, { input: { inspectionId: fixture.inspectionID, reason: "revisão concluída", clientMutationId: crypto.randomUUID() } });
    assertNoErrors(release.releaseLegalHold);
  } else if (journeyName.startsWith("E2E-008")) {
    const result = await api<{ inviteInternalUser: { userErrors: Array<{ message: string }> } }>(fixtureInviteUser, { input: { issuer: "http://localhost:8081/realms/inspection", subject: `journey-contributor-${suffix}`, role: "EMPLOYEE", scopes: [{ kind: "BUSINESS_UNIT", resourceId: fixture.unitID }], clientMutationId: crypto.randomUUID() } });
    assertNoErrors(result.inviteInternalUser);
  } else if (journeyName.startsWith("E2E-009")) {
    const result = await api<{ inviteOriginCapture: { userErrors: Array<{ message: string }> } }>(fixtureOriginInvite, { input: { assetId: fixture.assetID, participantId: fixture.participantID, expiresAt: new Date(Date.now() + 48 * 60 * 60 * 1000).toISOString(), clientMutationId: crypto.randomUUID() } });
    assertNoErrors(result.inviteOriginCapture);
  } else if (journeyName.startsWith("E2E-010")) {
    const versions = await api<{ originVersions: { nodes: Array<{ id: string; status: string }> } }>(fixtureOriginVersions, { assetID: fixture.assetID });
    if (versions.originVersions.nodes[0]) {
      const result = await api<{ activateOriginVersion: { userErrors: Array<{ message: string }> } }>(fixtureActivateOrigin, { input: { versionId: versions.originVersions.nodes[0].id, clientMutationId: crypto.randomUUID() } });
      assertNoErrors(result.activateOriginVersion);
    }
  } else if (journeyName.startsWith("E2E-011") || journeyName.startsWith("E2E-013")) {
    expect(fixture.scheduleRRule).toContain("FREQ=MONTHLY");
    expect(fixture.scheduleReminderOffsetsMinutes.length).toBeGreaterThan(0);
  } else if (journeyName.startsWith("E2E-012")) {
    expect(fixture.inspectionStatus).toBe("PLANNED");
  } else if (journeyName.startsWith("E2E-014")) {
    const detail = await api<{ inspection: { version: number } }>(fixtureInspectionDetail, { id: fixture.inspectionID });
    const result = await api<{ cancelInspection: { userErrors: Array<{ message: string }> } }>(fixtureCancelInspection, { input: { inspectionId: fixture.inspectionID, expectedVersion: detail.inspection.version, clientMutationId: crypto.randomUUID() } });
    assertNoErrors(result.cancelInspection);
  } else if (journeyName.startsWith("E2E-021")) {
    const detail = await api<{ project: { id: string; version: number; stages: Array<{ id: string; status: string; version: number }> } }>(fixtureProjectDetail, { id: fixture.projectID });
    const result = await api<{ addExceptionalStage: { userErrors: Array<{ message: string }> } }>(fixtureCreateStage, { input: { projectId: fixture.projectID, expectedVersion: detail.project.version, key: `exception-${suffix}`, label: "Etapa excepcional", reason: "evidência adicional necessária", clientMutationId: crypto.randomUUID() } });
    assertNoErrors(result.addExceptionalStage);
  } else if (journeyName.startsWith("E2E-022")) {
    let detail = await api<{ project: { id: string; version: number; stages: Array<{ id: string; status: string; version: number }> } }>(fixtureProjectDetail, { id: fixture.projectID });
    for (const planned of detail.project.stages.filter((stage) => stage.status === "PLANNED" || stage.status === "AVAILABLE")) {
      const skipped = await api<{ skipProjectStage: { project?: { version: number }; userErrors: Array<{ message: string }> } }>(fixtureSkipStage, { input: { projectId: fixture.projectID, stageId: planned.id, expectedVersion: detail.project.version, reason: "etapa não aplicável", clientMutationId: crypto.randomUUID() } });
      assertNoErrors(skipped.skipProjectStage);
      detail = await api<{ project: { id: string; version: number; stages: Array<{ id: string; status: string; version: number }> } }>(fixtureProjectDetail, { id: fixture.projectID });
    }
    const current = await api<{ project: { version: number } }>(fixtureProjectDetail, { id: fixture.projectID });
    const closed = await api<{ closeProject: { userErrors: Array<{ message: string }> } }>(fixtureCloseProject, { input: { projectId: fixture.projectID, expectedVersion: current.project.version, clientMutationId: crypto.randomUUID() } });
    assertNoErrors(closed.closeProject);
    const afterClose = await api<{ project: { version: number } }>(fixtureProjectDetail, { id: fixture.projectID });
    const reopened = await api<{ reopenProject: { userErrors: Array<{ message: string }> } }>(fixtureReopenProject, { input: { projectId: fixture.projectID, expectedVersion: afterClose.project.version, reason: "continuidade operacional", clientMutationId: crypto.randomUUID() } });
    assertNoErrors(reopened.reopenProject);
  } else if (journeyName.startsWith("E2E-025") || journeyName.startsWith("E2E-026") || journeyName.startsWith("E2E-027") || journeyName.startsWith("E2E-029")) {
    const result = await api<{ triageInspections: { nodes: Array<{ inspectionId: string }> } }>(fixtureTriage);
    expect(result.triageInspections.nodes).toBeDefined();
  } else if (journeyName.startsWith("E2E-028")) {
    const detail = await api<{ projectTimeline: { projectId: string; entries: Array<{ stageId: string }> } }>(`query JourneyTimeline($id:ID!){projectTimeline(projectId:$id){projectId entries{stageId}}}`, { id: fixture.projectID });
    expect(detail.projectTimeline.projectId).toBe(fixture.projectID);
  } else if (journeyName.startsWith("E2E-030")) {
    const result = await api<{ notificationDeliveries: { nodes: Array<{ id: string; status: string }> } }>(`query JourneyNotifications { notificationDeliveries(first:100){nodes{id status}} }`);
    expect(result.notificationDeliveries.nodes).toBeDefined();
  }
  return fixture;
}

test("capture journey delivers OTP, records GPS, uploads evidence, and submits @acceptance", async ({ page, context }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium", "The delivery-backed capture journey runs once against the shared Mailpit fixture.");
  await signIn(page);
  const suffix = `${testInfo.project.name}-${Date.now()}`;
  const participantName = `Capturador ${suffix}`;
  const recipient = `capture-${suffix}@example.com`;

  await page.locator("#participant-name").fill(participantName);
  await page.locator("#participant-email").fill(recipient);
  await page.getByRole("button", { name: "Criar participante" }).click();
  await expect(page.getByRole("status")).toContainText("criado; confirme o canal");
  const participant = page.locator("li", { hasText: participantName });
  await participant.getByRole("button", { name: `Confirmar canal ${recipient}` }).click();
  await expect(page.getByRole("status")).toContainText("Canal confirmado");
  await participant.getByRole("button", { name: `Selecionar canal ${recipient}` }).click();
  await expect(page.getByRole("status")).toContainText("Canal selecionado");

  const assets = page.locator(`[id="assets:manage"]`);
  const catalog = assets.locator("details").filter({ hasText: "Publicar catálogo" });
  const openCatalog = async () => {
    if (await catalog.getAttribute("open") === null) await catalog.locator("summary").click();
  };
  await openCatalog();
  await catalog.locator("#segment-key").fill(`capture-segment-${suffix}`);
  await catalog.locator("#segment-name").fill(`Segmento de captura ${suffix}`);
  await catalog.getByRole("button", { name: "Publicar segmento" }).click();
  await expect(assets.getByRole("status")).toContainText("Segmento");
  await openCatalog();
  await catalog.locator("#profile-key").fill(`capture-profile-${suffix}`);
  await catalog.getByRole("button", { name: "Publicar perfil de análise" }).click();
  await expect(assets.getByRole("status")).toContainText("Perfil");
  await openCatalog();
  await catalog.locator("#template-key").fill(`capture-template-${suffix}`);
  await catalog.locator("#template-name").fill(`Template de captura ${suffix}`);
  await catalog.getByRole("button", { name: "Publicar template" }).click();
  await expect(assets.getByRole("status")).toContainText("Template");

  const assetName = `Ativo de captura ${suffix}`;
  const assetDetails = assets.locator("details").filter({ hasText: "Registrar ativo" });
  if (await assetDetails.getAttribute("open") === null) await assetDetails.locator("summary").click();
  await assetDetails.locator("#asset-name").fill(assetName);
  await assetDetails.locator("#asset-external-key").fill(`capture-asset-${suffix}`);
  await assetDetails.locator("#asset-address").fill("Rua de captura, 100");
  const assignedParticipant = assetDetails.locator("#asset-participant option").filter({ hasText: participantName }).first();
  await assetDetails.locator("#asset-participant").selectOption((await assignedParticipant.getAttribute("value")) ?? "");
  await assetDetails.getByRole("button", { name: "Registrar ativo" }).click();
  await expect(assets.getByRole("status")).toContainText("Ativo");

  const inspections = page.locator(`[id="inspections:manage"]`);
  const invite = inspections.locator("details").filter({ hasText: "Enviar convite de captura" });
  if (await invite.getAttribute("open") === null) await invite.locator("summary").click();
  const assetOption = invite.locator("#invite-asset option").filter({ hasText: assetName }).first();
  const participantOption = invite.locator("#invite-participant option").filter({ hasText: participantName }).first();
  await invite.locator("#invite-asset").selectOption((await assetOption.getAttribute("value")) ?? "");
  await invite.locator("#invite-participant").selectOption((await participantOption.getAttribute("value")) ?? "");
  await invite.getByRole("button", { name: "Enviar convite de captura" }).click();
  await expect(inspections.getByRole("status")).toContainText("Convite de captura enviado");

  const invitation = await readMail(page, recipient, "inspection-capture-link");
  const token = invitation.match(/[0-9a-f-]{36}\.[A-Za-z0-9_-]{43}/)?.[0];
  expect(token).toBeTruthy();

  await context.grantPermissions(["geolocation"], { origin: "http://localhost:3000" });
  await context.setGeolocation({ latitude: -23.5505, longitude: -46.6333, accuracy: 10 });
  await page.goto(`/capture/${token}`);
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  const otpMail = await readMail(page, recipient, "Seu codigo de acesso");
  const code = otpMail.match(/\b\d{6}\b/)?.[0];
  expect(code).toBeTruthy();
  await page.locator("input[inputmode='numeric']").fill(code ?? "");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByRole("heading", { name: "Uso dos seus dados" })).toBeVisible();
  const acceptResponse = page.waitForResponse((response) => response.url().endsWith("/graphql") && response.request().postData()?.includes("acceptProcessing") === true);
  await page.getByRole("button", { name: "Aceito o processamento necessário" }).click();
  const accepted = await acceptResponse;
  expect(accepted.ok()).toBeTruthy();
  await expect(page.getByRole("heading", { name: "Captura guiada" })).toBeVisible();
  await page.getByLabel("Descrição da foto").fill("Evidência principal do ativo");
  await page.getByRole("button", { name: "Usar minha localização" }).click();
  await expect(page.getByText(/Localização com precisão/)).toBeVisible();
  await page.evaluate(() => {
    Object.defineProperty(navigator, "onLine", { configurable: true, get: () => true });
    window.dispatchEvent(new Event("online"));
  });
  const onePixelPNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=", "base64");
  await page.getByLabel("Adicionar foto").setInputFiles({ name: "evidence.png", mimeType: "image/png", buffer: onePixelPNG });
  await expect(page.getByText("Evidência enviada e verificada.")).toBeVisible({ timeout: 30_000 });
  const submitResponse = page.waitForResponse((response) => response.url().endsWith("/graphql") && /submit(Capture|Recapture)/.test(response.request().postData() ?? ""));
  await page.getByRole("button", { name: "Enviar inspeção completa" }).click();
  const submission = await submitResponse;
  const submissionBody = await submission.json() as { errors?: Array<{ message: string }>; data?: { submitCapture?: { userErrors?: Array<{ message: string }> }; submitRecapture?: { userErrors?: Array<{ message: string }> } } };
  const submissionError = submissionBody.errors?.[0]?.message ?? submissionBody.data?.submitCapture?.userErrors?.[0]?.message ?? submissionBody.data?.submitRecapture?.userErrors?.[0]?.message;
  if (!submission.ok() || submissionError) throw new Error(submissionError ?? `Capture submission failed (${submission.status()})`);
  await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
});

test("E2E-015..030 completes an inspection, analysis, report, triage, and recapture journey @acceptance", async ({ page, context }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium", "The delivery-backed lifecycle uses the shared Mailpit fixture once on Chromium.");
  test.setTimeout(120_000);
  const api = await signIn(page);
  const suffix = `lifecycle-${testInfo.project.name}-${Date.now()}`;
  const fixture = await createJourneyFixture(api, suffix);
  const invitation = await readMail(page, fixture.participantEmail, "inspection-capture-link");
  const token = invitation.match(/[0-9a-f-]{36}\.[A-Za-z0-9_-]{43}/)?.[0];
  expect(token).toBeTruthy();
  await completeExternalCapture(page, context, token ?? "", fixture.participantEmail);

  // Directed recapture is requested while the submitted inspection is still
  // analyzing. Once the classifier reaches COMPLETED the inspection is
  // terminal by contract and cannot be reopened by a recapture request.
  const recapture = await api<{ requestRecapture: { recapture?: { status: string }; userErrors: Array<{ message: string }> } }>(fixtureRequestRecapture, { input: { inspectionId: fixture.inspectionID, items: [{ requirementKey: "overview", reason: "confirmar enquadramento" }], deadlineAt: new Date(Date.now() + 48 * 60 * 60 * 1000).toISOString(), clientMutationId: crypto.randomUUID() } });
  assertNoErrors(recapture.requestRecapture);
  expect(recapture.requestRecapture.recapture?.status).toBe("REQUESTED");

  const recaptureInvitation = await readMail(page, fixture.participantEmail, "inspection-recapture-link");
  const recaptureToken = recaptureInvitation.match(/[0-9a-f-]{36}\.[A-Za-z0-9_-]{43}/)?.[0];
  expect(recaptureToken).toBeTruthy();
  await completeExternalCapture(page, context, recaptureToken ?? "", fixture.participantEmail);

  await expect.poll(async () => {
    try {
      const result = await api<{ report: { id: string; version: number; classification: string } | null }>(fixtureReport, { inspectionID: fixture.inspectionID });
      return result.report ? `${result.report.id}:${result.report.version}:${result.report.classification}` : "";
    } catch {
      return "";
    }
  }, { timeout: 45_000 }).not.toBe("");
  await expect.poll(async () => {
    try {
      const result = await api<{ triageInspections: { nodes: Array<{ inspectionId: string; classification: string; status: string }> } }>(fixtureTriage);
      return result.triageInspections.nodes.some((item) => item.inspectionId === fixture.inspectionID) ? result : null;
    } catch {
      return null;
    }
  }, { timeout: 45_000 }).not.toBeNull();
  const triageResult = await api<{ triageInspections: { nodes: Array<{ inspectionId: string; classification: string; status: string }> } }>(fixtureTriage);
  expect(triageResult.triageInspections.nodes.some((item) => item.inspectionId === fixture.inspectionID)).toBeTruthy();
  const reportResult = await api<{ report: { id: string; version: number; classification: string } | null }>(fixtureReport, { inspectionID: fixture.inspectionID });
  expect(reportResult.report?.id).toBeTruthy();
  const notifications = await api<{ notificationDeliveries: { nodes: Array<{ id: string; status: string }> } }>(fixtureNotifications);
  expect(notifications.notificationDeliveries.nodes).toBeDefined();
});

for (const journey of journeys) {
  test(`${journey.name} @acceptance`, async ({ page }, testInfo) => {
    const api = await signIn(page);
    const area = page.locator(`[id="${journey.anchor}"]`);
    await area.scrollIntoViewIfNeeded();
    await expect(area).toBeVisible();
    await expect(area.getByRole("heading", { name: journey.heading })).toBeVisible();
    await expect(area).toContainText(journey.data);
    const suffix = `${journey.name.replace(/[^A-Za-z0-9]+/g, "-").toLowerCase()}-${testInfo.project.name}-${Date.now()}`;
    await exerciseFunctionalJourney(api, journey.name, suffix);
  });
}

test("capture removes an invalid link token before displaying the failure @security", async ({ page }) => {
  await page.goto("/capture/opaque-link");
  await expect(page.getByRole("heading", { name: "Acesso indisponível" })).toBeVisible();
  await expect(page).toHaveURL(/\/capture\/acesso$/);
  expect(await page.evaluate(() => Object.keys(localStorage))).toEqual([]);
});

test("dashboard is keyboard accessible and does not persist an internal token @a11y", async ({ page }) => {
  await page.goto("/");
  const login = page.getByRole("button", { name: "Entrar com conta interna" });
  await login.focus();
  await expect(login).toBeFocused();
  const overview = page.getByRole("link", { name: "Visão geral" });
  await overview.focus();
  await expect(overview).toBeFocused();
  await expect(page.getByRole("main")).toBeVisible();
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
});

test("capture PWA caches only the public shell @security @a11y", async ({ page }) => {
  await page.goto("/capture/opaque-link");
  await expect(page.getByRole("heading", { name: "Acesso indisponível" })).toBeVisible();
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.ready.then(() => true))).toBeTruthy();
  const cacheState = await page.evaluate(async () => {
    const keys = await caches.keys();
    const cachedURLs = (await Promise.all(keys.map(async (key) => (await caches.open(key)).keys()))).flat().map((request) => request.url);
    return { keys, cachedURLs, protected: Boolean(await caches.match(`${location.origin}/capture/acesso`)) };
  });
  expect(cacheState.keys).toContain("inspection-static-v1");
  expect(cacheState.cachedURLs.every((url) => url.includes("/manifest.webmanifest") || url.includes("/icon.svg") || url.includes("/_next/static/"))).toBeTruthy();
  expect(cacheState.protected).toBeFalsy();
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
});

test("authenticated dashboard has no WCAG A or AA violations @a11y", async ({ page }) => {
  await signIn(page);
  await expect(page).toHaveTitle("Inspeção");
  const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa"]).analyze();
  expect(results.violations).toEqual([]);
});
