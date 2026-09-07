"use client";

import { FormEvent, useCallback, useEffect, useState } from "react";
import { beginPKCE } from "@/auth/pkce";
import { getAccessToken } from "@/auth/session";
import { dashboardOperations, graphql } from "@/graphql/client";

const navigation = [
  ["Visão geral", "dashboard:read"], ["Pessoas e acesso", "access:manage"], ["Participantes", "participants:manage"],
  ["Catálogos e ativos", "assets:manage"], ["Agendas e projetos", "inspections:manage"], ["Triagem e relatórios", "reports:read"],
  ["Auditoria", "audit:read"], ["Notificações", "notifications:read"], ["Uso e retenção", "retention:manage"]
];

type Overview = {
  me: { identityId: string; tenantId: string; roles: string[]; memberships: Array<{ id: string; tenantId: string; role: string; status: string; version: number }>; effectiveScopes: Array<{ kind: string; resourceId: string }> };
  tenant: { id: string; name: string; language: string; defaultTimezone: string; status: string; version: number } | null;
  memberships: { nodes: Array<{ id: string; tenantId: string; role: string; status: string; version: number; scopes: Array<{ kind: string; resourceId: string }> }> };
  dashboardSummary: { total: number; normal: number; attention: number; critical: number; pending: number; invalidated: number };
  businessUnits: { nodes: Array<{ id: string; code: string; name: string; status: string; version: number }> };
  participants: { nodes: Array<{ id: string; name: string; segmentRole: string; status: string; businessUnitId: string; version: number; selectedContactIds: string[]; contacts: Array<{ id: string; channel: string; value: string; verified: boolean; active: boolean }> }> };
  segmentDefinitions: { nodes: Array<{ id: string; key: string; name: string; activeVersionId?: string | null; version: number }> };
  templates: { nodes: Array<{ id: string; key: string; name: string; activeVersionId?: string | null; version: number }> };
  assets: { nodes: Array<{ id: string; name: string; externalKey: string; address: string; status: string; businessUnitId: string; version: number; segmentVersionId: string; templateId?: string | null; latitudeE6?: number | null; longitudeE6?: number | null; geofenceMeters: number; assignments: Array<{ participantId: string; role: string; active: boolean }> }> };
  schedules: { nodes: Array<{ id: string; assetId: string; participantId: string; templateId: string; referenceVersionId?: string | null; rrule: string; timezone: string; startsAt: string; status: string; nextDueAt: string; deadlineMinutes: number; reminderOffsetsMinutes: number[]; version: number }> };
  projects: { nodes: Array<{ id: string; assetId: string; participantId: string; templateId?: string | null; templateVersionId: string; status: string; reportMode: string; version: number; stages: Array<{ id: string; key: string; label: string; kind: string; position: number; status: string; plannedAt?: string | null; inspectionId?: string | null; version: number }>; transitions: Array<{ id: string; stageId: string; fromState?: string | null; toState: string; reason: string; occurredAt: string }> }> };
  inspections: { nodes: Array<{ id: string; assetId: string; participantId: string; templateId: string; templateVersionId: string; analysisProfileVersionId: string; projectId?: string | null; status: string; source: string; sourceReason?: string | null; stateReason?: string | null; evidenceCount: number; dueAt: string; deadlineAt: string; reminderInstants: string[]; version: number }> };
  auditEvents: { nodes: Array<{ id: string; action: string; targetType: string; targetId: string; outcome: string; reason?: string | null; correlationId: string; occurredAt: string }> };
  notificationDeliveries: { nodes: Array<{ id: string; intentId: string; status: string; createdAt: string; updatedAt: string }> };
  retentionPolicies: { nodes: Array<{ id: string; evidenceDays: number; operationalDays: number; securityDays: number; version: number; createdAt: string; updatedAt: string }> };
  usageSummary: { from: string; to: string; requests: number; inputTokens: number; outputTokens: number; cost: number };
  triageInspections: { nodes: Array<{ inspectionId: string; projectId?: string | null; assetId: string; classification: string; status: string; updatedAt: string }> };
};

export default function DashboardPage() {
  const signedIn = Boolean(getAccessToken());
  const [overview, setOverview] = useState<Overview>();
  const [loading, setLoading] = useState(signedIn);
  const [error, setError] = useState<string>();
  const [unitCode, setUnitCode] = useState("");
  const [unitName, setUnitName] = useState("");
  const [unitNotice, setUnitNotice] = useState<string>();
  const [editingUnitId, setEditingUnitId] = useState<string>();
  const [editingUnitCode, setEditingUnitCode] = useState("");
  const [editingUnitName, setEditingUnitName] = useState("");
  const [tenantName, setTenantName] = useState("");
  const [tenantTimezone, setTenantTimezone] = useState("");
  const [tenantNotice, setTenantNotice] = useState<string>();
  const [participantName, setParticipantName] = useState("");
  const [participantEmail, setParticipantEmail] = useState("");
  const [participantUnit, setParticipantUnit] = useState("");
  const [participantNotice, setParticipantNotice] = useState<string>();
  const [accessIssuer, setAccessIssuer] = useState("");
  const [accessSubject, setAccessSubject] = useState("");
  const [accessRole, setAccessRole] = useState("VIEWER");
  const [accessMembership, setAccessMembership] = useState("");
  const [accessScopeKind, setAccessScopeKind] = useState("BUSINESS_UNIT");
  const [accessScopeResource, setAccessScopeResource] = useState("");
  const [accessNotice, setAccessNotice] = useState<string>();
  const [segmentKey, setSegmentKey] = useState("");
  const [segmentName, setSegmentName] = useState("");
  const [activeSegmentVersionId, setActiveSegmentVersionId] = useState<string>();
  const [templateKey, setTemplateKey] = useState("");
  const [templateName, setTemplateName] = useState("");
  const [activeTemplateId, setActiveTemplateId] = useState<string>();
  const [profileKey, setProfileKey] = useState("");
  const [assetName, setAssetName] = useState("");
  const [assetExternalKey, setAssetExternalKey] = useState("");
  const [assetAddress, setAssetAddress] = useState("");
  const [assetParticipantId, setAssetParticipantId] = useState("");
  const [retentionEvidenceDays, setRetentionEvidenceDays] = useState("1825");
  const [retentionOperationalDays, setRetentionOperationalDays] = useState("365");
  const [retentionSecurityDays, setRetentionSecurityDays] = useState("730");
  const [operationNotice, setOperationNotice] = useState<string>();
  const [inviteAssetId, setInviteAssetId] = useState("");
  const [inviteParticipantId, setInviteParticipantId] = useState("");
  const [inviteExpiresAt, setInviteExpiresAt] = useState(() => new Date(Date.now() + 48 * 60 * 60 * 1000).toISOString().slice(0, 16));
  const [scheduleAssetId, setScheduleAssetId] = useState("");
  const [scheduleParticipantId, setScheduleParticipantId] = useState("");
  const [scheduleStartsAt, setScheduleStartsAt] = useState(() => new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString().slice(0, 16));
  const [scheduleRRule, setScheduleRRule] = useState("FREQ=MONTHLY;INTERVAL=1");
  const [inspectionAssetId, setInspectionAssetId] = useState("");
  const [inspectionParticipantId, setInspectionParticipantId] = useState("");
  const [inspectionDueAt, setInspectionDueAt] = useState(() => new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString().slice(0, 16));
  const [projectAssetId, setProjectAssetId] = useState("");
  const [projectParticipantId, setProjectParticipantId] = useState("");
  const [stageProjectId, setStageProjectId] = useState("");
  const [stageKey, setStageKey] = useState("");
  const [stageLabel, setStageLabel] = useState("");
  const [stageReason, setStageReason] = useState("");
  const [reportInspectionId, setReportInspectionId] = useState("");
  const [reportPreview, setReportPreview] = useState<{ id: string; version: number; classification: string; mode: string; html: string }>();
  const [reportDownload, setReportDownload] = useState<{ url: string; kind: string; status: string }>();
  const [recaptureInspectionId, setRecaptureInspectionId] = useState("");
  const [recaptureRequirementKey, setRecaptureRequirementKey] = useState("overview");
  const [recaptureReason, setRecaptureReason] = useState("");
  const [retentionInspectionId, setRetentionInspectionId] = useState("");
  const [retentionReason, setRetentionReason] = useState("");
  const canManageAccess = Boolean(overview?.me.roles.some((role) => role === "TENANT_ADMIN" || role === "MANAGER"));
  const isTenantAdmin = Boolean(overview?.me.roles.includes("TENANT_ADMIN"));
  const login = () => void beginPKCE(
    process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth",
    process.env.NEXT_PUBLIC_OIDC_CLIENT_ID ?? "inspection-web", `${location.origin}/auth/callback`
  );
  const load = useCallback(async () => {
    if (!getAccessToken()) return;
    setLoading(true);
    setError(undefined);
    try {
      const result = await graphql<Overview>("dashboard", dashboardOperations.overview, { first: 100 });
      setOverview(result);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Não foi possível carregar os dados autorizados.");
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => { void load(); }, [load]);
  const createBusinessUnit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!overview?.tenant || !unitCode.trim() || !unitName.trim()) return;
    setUnitNotice(undefined);
    try {
      const result = await graphql<{ upsertBusinessUnit: { userErrors: Array<{ message: string }>; businessUnit?: { name: string } } }>("dashboard", dashboardOperations.upsertBusinessUnit, {
        input: { code: unitCode.trim(), name: unitName.trim(), expectedTenantVersion: overview.tenant.version, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.upsertBusinessUnit.userErrors[0];
      if (failure) throw new Error(failure.message);
      setUnitCode(""); setUnitName("");
      setUnitNotice(`Unidade ${result.upsertBusinessUnit.businessUnit?.name ?? ""} criada.`);
      await load();
    } catch (reason) {
      setUnitNotice(reason instanceof Error ? reason.message : "Não foi possível criar a unidade.");
    }
  };
  const archiveBusinessUnit = async (unit: Overview["businessUnits"]["nodes"][number]) => {
    setUnitNotice(undefined);
    try {
      const result = await graphql<{ archiveBusinessUnit: { userErrors: Array<{ message: string }>; businessUnit?: { name: string } } }>("dashboard", dashboardOperations.archiveBusinessUnit, {
        input: { businessUnitId: unit.id, expectedVersion: unit.version, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.archiveBusinessUnit.userErrors[0];
      if (failure) throw new Error(failure.message);
      setUnitNotice(`Unidade ${result.archiveBusinessUnit.businessUnit?.name ?? unit.name} arquivada.`);
      await load();
    } catch (reason) {
      setUnitNotice(reason instanceof Error ? reason.message : "Não foi possível arquivar a unidade.");
    }
  };
  const saveBusinessUnit = async (event: FormEvent<HTMLFormElement>, unit: Overview["businessUnits"]["nodes"][number]) => {
    event.preventDefault();
    if (!editingUnitCode.trim() || !editingUnitName.trim() || !overview?.tenant) return;
    setUnitNotice(undefined);
    try {
      const result = await graphql<{ upsertBusinessUnit: { userErrors: Array<{ message: string }>; businessUnit?: { name: string } } }>("dashboard", dashboardOperations.upsertBusinessUnit, {
        input: { businessUnitId: unit.id, code: editingUnitCode.trim(), name: editingUnitName.trim(), expectedVersion: unit.version, expectedTenantVersion: overview.tenant.version, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.upsertBusinessUnit.userErrors[0];
      if (failure) throw new Error(failure.message);
      setEditingUnitId(undefined); setUnitNotice(`Unidade ${result.upsertBusinessUnit.businessUnit?.name ?? editingUnitName} atualizada.`);
      await load();
    } catch (reason) {
      setUnitNotice(reason instanceof Error ? reason.message : "Não foi possível atualizar a unidade.");
    }
  };
  const inviteInternalUser = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!accessIssuer.trim() || !accessSubject.trim()) return;
    setAccessNotice(undefined);
    try {
      const result = await graphql<{ inviteInternalUser: { userErrors: Array<{ message: string }>; membership?: { id: string; role: string } } }>("dashboard", dashboardOperations.inviteInternalUser, {
        input: { issuer: accessIssuer.trim(), subject: accessSubject.trim(), role: accessRole, scopes: [], clientMutationId: crypto.randomUUID() }
      });
      const failure = result.inviteInternalUser.userErrors[0];
      if (failure) throw new Error(failure.message);
      setAccessIssuer(""); setAccessSubject("");
      setAccessNotice(`Membro ${result.inviteInternalUser.membership?.role ?? accessRole} convidado.`);
      await load();
    } catch (reason) {
      setAccessNotice(reason instanceof Error ? reason.message : "Não foi possível convidar o usuário.");
    }
  };
  const assignRoleScope = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!accessMembership || !accessScopeResource.trim()) return;
    setAccessNotice(undefined);
    try {
      const membership = overview?.memberships.nodes.find((item) => item.id === accessMembership);
      const result = await graphql<{ assignRoleScopes: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.assignRoleScopes, {
        input: { membershipId: accessMembership, role: membership?.role ?? accessRole, scopes: [{ kind: accessScopeKind, resourceId: accessScopeResource.trim() }], expectedVersion: membership?.version ?? 0, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.assignRoleScopes.userErrors[0];
      if (failure) throw new Error(failure.message);
      setAccessScopeResource(""); setAccessNotice("Papel e escopo atualizados.");
      await load();
    } catch (reason) {
      setAccessNotice(reason instanceof Error ? reason.message : "Não foi possível atualizar o escopo.");
    }
  };
  const disableMembership = async (membership: Overview["memberships"]["nodes"][number]) => {
    setAccessNotice(undefined);
    try {
      const result = await graphql<{ disableMembership: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.disableMembership, {
        input: { membershipId: membership.id, expectedVersion: membership.version, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.disableMembership.userErrors[0];
      if (failure) throw new Error(failure.message);
      setAccessNotice("Membership desativada.");
      await load();
    } catch (reason) {
      setAccessNotice(reason instanceof Error ? reason.message : "Não foi possível desativar a membership.");
    }
  };
  const publishSegment = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setOperationNotice(undefined);
    try {
      const result = await graphql<{ publishSegmentDefinition: { userErrors: Array<{ message: string }>; definition?: { id: string; key: string; name: string; activeVersionId?: string | null; version: number }; version?: { id: string } } }>("dashboard", dashboardOperations.publishSegmentDefinition, {
        input: { key: segmentKey.trim(), name: segmentName.trim(), schema: { type: "object", properties: { kind: { type: "string", maxLength: 120 } }, required: ["kind"] }, uiSchema: {}, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.publishSegmentDefinition.userErrors[0];
      if (failure) throw new Error(failure.message);
      if (result.publishSegmentDefinition.definition && result.publishSegmentDefinition.version) {
        const activation = await graphql<{ activateSegmentDefinition: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.activateSegmentDefinition, {
          input: { versionId: result.publishSegmentDefinition.version.id, expectedVersion: result.publishSegmentDefinition.definition.version, clientMutationId: crypto.randomUUID() }
        });
        const activationFailure = activation.activateSegmentDefinition.userErrors[0];
        if (activationFailure) throw new Error(activationFailure.message);
        setActiveSegmentVersionId(result.publishSegmentDefinition.version.id);
      }
      setSegmentKey(""); setSegmentName("");
      setOperationNotice(`Segmento ${result.publishSegmentDefinition.definition?.name ?? "publicado"}.`);
      const created = result.publishSegmentDefinition.definition;
      const versionID = result.publishSegmentDefinition.version?.id;
      if (created && versionID) {
        setOverview((current) => current ? {
          ...current,
          segmentDefinitions: {
            ...current.segmentDefinitions,
            nodes: [...current.segmentDefinitions.nodes.filter((definition) => definition.id !== created.id), { ...created, activeVersionId: versionID }]
          }
        } : current);
      }
    } catch (reason) {
      setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível publicar o segmento.");
    }
  };
  const publishTemplate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setOperationNotice(undefined);
    try {
      const segmentVersionId = activeSegmentVersionId ?? overview?.segmentDefinitions.nodes[0]?.activeVersionId;
      if (!segmentVersionId) throw new Error("Publique um segmento antes do template.");
      const result = await graphql<{ publishTemplateVersion: { userErrors: Array<{ message: string }>; template?: { id: string; key: string; name: string; activeVersionId?: string | null; version: number }; version?: { id: string } } }>("dashboard", dashboardOperations.publishTemplateVersion, {
        input: { key: templateKey.trim(), name: templateName.trim(), definition: { schemaVersion: 1, segmentVersionId, participantRoles: ["TENANT_PARTICIPANT"], comparisonMode: "FIXED_ORIGIN", requirements: [{ key: "overview", section: "Geral", label: "Visão geral", evidenceKind: "PHOTO", minimumCount: 1, maximumCount: 2, required: true, descriptionRequired: true, captureSourcePolicy: "CAMERA_DEFAULT", comparisonTarget: "FIXED_ORIGIN" }], multiStage: false, reportMode: "HISTORICAL", analysisProfile: "default", policy: { gpsRequired: false, geofenceMeters: 150, allowGallery: true } }, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.publishTemplateVersion.userErrors[0];
      if (failure) throw new Error(failure.message);
      if (result.publishTemplateVersion.template?.id && result.publishTemplateVersion.version?.id) {
        const activation = await graphql<{ activateTemplateVersion: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.activateTemplateVersion, {
          input: { versionId: result.publishTemplateVersion.version.id, expectedVersion: result.publishTemplateVersion.template.version, clientMutationId: crypto.randomUUID() }
        });
        const activationFailure = activation.activateTemplateVersion.userErrors[0];
        if (activationFailure) throw new Error(activationFailure.message);
        setActiveTemplateId(result.publishTemplateVersion.template.id);
      }
      setTemplateKey(""); setTemplateName("");
      setOperationNotice(`Template ${result.publishTemplateVersion.template?.name ?? "publicado"}.`);
      const created = result.publishTemplateVersion.template;
      if (created) {
        setOverview((current) => current ? {
          ...current,
          templates: {
            ...current.templates,
            nodes: [...current.templates.nodes.filter((template) => template.id !== created.id), { ...created, activeVersionId: result.publishTemplateVersion.version?.id ?? null }]
          }
        } : current);
      }
    } catch (reason) {
      setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível publicar o template.");
    }
  };
  const publishProfile = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setOperationNotice(undefined);
    try {
      const result = await graphql<{ publishAnalysisProfile: { userErrors: Array<{ message: string }>; profile?: { key: string } } }>("dashboard", dashboardOperations.publishAnalysisProfile, {
        input: { key: profileKey.trim(), definition: { schemaVersion: 1, modelAlias: "inspection-vision", promptVersion: "analysis-v1", outputSchema: { type: "object" }, minimumConfidenceBps: 5000 }, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.publishAnalysisProfile.userErrors[0];
      if (failure) throw new Error(failure.message);
      setProfileKey("");
      setOperationNotice(`Perfil ${result.publishAnalysisProfile.profile?.key ?? "publicado"}.`);
    } catch (reason) {
      setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível publicar o perfil.");
    }
  };
  const registerAsset = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const unit = overview?.businessUnits.nodes.find((item) => item.status !== "ARCHIVED");
    const segmentVersionId = activeSegmentVersionId ?? overview?.segmentDefinitions.nodes[0]?.activeVersionId;
    if (!unit || !segmentVersionId || !assetName.trim() || !assetExternalKey.trim() || !assetAddress.trim()) {
      setOperationNotice("Cadastre uma unidade e um segmento ativo antes de registrar o ativo.");
      return;
    }
    const templateId = activeTemplateId ?? overview?.templates.nodes[0]?.id ?? null;
    setOperationNotice(undefined);
    try {
      const result = await graphql<{ registerAsset: { userErrors: Array<{ message: string }>; asset?: Overview["assets"]["nodes"][number] } }>("dashboard", dashboardOperations.registerAsset, {
        input: { asset: { businessUnitId: unit.id, segmentVersionId, templateId, name: assetName.trim(), externalKey: assetExternalKey.trim(), address: assetAddress.trim(), geofenceMeters: 150, attributes: { kind: "property" }, policyOverrides: {}, assignments: assetParticipantId ? [{ participantId: assetParticipantId, role: "TENANT_PARTICIPANT" }] : [] }, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.registerAsset.userErrors[0];
      if (failure) throw new Error(failure.message);
      setAssetName(""); setAssetExternalKey(""); setAssetAddress(""); setAssetParticipantId("");
      setOperationNotice(`Ativo ${result.registerAsset.asset?.name ?? "registrado"}.`);
      const created = result.registerAsset.asset;
      if (created) {
        setOverview((current) => current ? {
          ...current,
          assets: {
            ...current.assets,
            nodes: [...current.assets.nodes.filter((asset) => asset.id !== created.id), created]
          }
        } : current);
      }
    } catch (reason) {
      setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível registrar o ativo.");
    }
  };
  const archiveAsset = async (asset: Overview["assets"]["nodes"][number]) => {
    setOperationNotice(undefined);
    try {
      const result = await graphql<{ archiveAsset: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.archiveAsset, {
        input: { assetId: asset.id, expectedVersion: asset.version, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.archiveAsset.userErrors[0];
      if (failure) throw new Error(failure.message);
      setOperationNotice(`Ativo ${asset.name} arquivado.`);
      await load();
    } catch (reason) {
      setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível arquivar o ativo.");
    }
  };
  const configureRetention = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setOperationNotice(undefined);
    try {
      const result = await graphql<{ configureRetentionPolicy: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.configureRetentionPolicy, {
        input: { evidenceDays: Number(retentionEvidenceDays), operationalDays: Number(retentionOperationalDays), securityDays: Number(retentionSecurityDays), clientMutationId: crypto.randomUUID() }
      });
      const failure = result.configureRetentionPolicy.userErrors[0];
      if (failure) throw new Error(failure.message);
      setOperationNotice("Política de retenção atualizada.");
      await load();
    } catch (reason) {
      setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível atualizar a retenção.");
    }
  };
  const updateTenant = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const name = tenantName.trim() || overview?.tenant?.name || "";
    const timezone = tenantTimezone.trim() || overview?.tenant?.defaultTimezone || "";
    if (!overview?.tenant || !name || !timezone) return;
    setTenantNotice(undefined);
    try {
      const result = await graphql<{ updateTenant: { userErrors: Array<{ message: string }>; tenant?: { name: string } } }>("dashboard", dashboardOperations.updateTenant, {
        input: { name, language: overview.tenant.language, timezone, expectedVersion: overview.tenant.version, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.updateTenant.userErrors[0];
      if (failure) throw new Error(failure.message);
      setTenantNotice(`Configuração de ${result.updateTenant.tenant?.name ?? "tenant"} atualizada.`);
      await load();
    } catch (reason) {
      setTenantNotice(reason instanceof Error ? reason.message : "Não foi possível atualizar o tenant.");
    }
  };
  const createParticipant = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const businessUnitId = participantUnit || overview?.businessUnits.nodes[0]?.id;
    if (!businessUnitId || !participantName.trim() || !participantEmail.trim()) return;
    setParticipantNotice(undefined);
    try {
      const result = await graphql<{ upsertParticipant: { userErrors: Array<{ message: string }>; participant?: Overview["participants"]["nodes"][number] } }>("dashboard", dashboardOperations.createParticipant, {
        input: { businessUnitId, name: participantName.trim(), segmentRole: "TENANT_PARTICIPANT", contacts: [{ channel: "EMAIL", value: participantEmail.trim() }], clientMutationId: crypto.randomUUID() }
      });
      const failure = result.upsertParticipant.userErrors[0];
      if (failure) throw new Error(failure.message);
      setParticipantName(""); setParticipantEmail("");
      setParticipantNotice(`Participante ${result.upsertParticipant.participant?.name ?? ""} criado; confirme o canal antes de convidar.`);
      const created = result.upsertParticipant.participant;
      if (created) {
        setOverview((current) => current ? {
          ...current,
          participants: {
            ...current.participants,
            nodes: [...current.participants.nodes.filter((participant) => participant.id !== created.id), created]
          }
        } : current);
      }
    } catch (reason) {
      setParticipantNotice(reason instanceof Error ? reason.message : "Não foi possível criar o participante.");
    }
  };
  const verifyContact = async (contactId: string) => {
    setParticipantNotice(undefined);
    try {
      const result = await graphql<{ verifyContact: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.verifyContact, {
        input: { contactId, verified: true, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.verifyContact.userErrors[0];
      if (failure) throw new Error(failure.message);
      setParticipantNotice("Canal confirmado e disponível para convite.");
      setOverview((current) => current ? {
        ...current,
        participants: {
          ...current.participants,
          nodes: current.participants.nodes.map((participant) => ({
            ...participant,
            contacts: participant.contacts.map((contact) => contact.id === contactId ? { ...contact, verified: true } : contact)
          }))
        }
      } : current);
    } catch (reason) {
      setParticipantNotice(reason instanceof Error ? reason.message : "Não foi possível confirmar o canal.");
    }
  };
  const selectDeliveryChannel = async (participant: Overview["participants"]["nodes"][number], contactId: string) => {
    setParticipantNotice(undefined);
    try {
      const result = await graphql<{ setDeliveryChannels: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.setDeliveryChannels, {
        input: { participantId: participant.id, contactIds: [contactId], expectedVersion: participant.version, clientMutationId: crypto.randomUUID() }
      });
      const failure = result.setDeliveryChannels.userErrors[0];
      if (failure) throw new Error(failure.message);
      setParticipantNotice("Canal selecionado para convites.");
      setOverview((current) => current ? {
        ...current,
        participants: {
          ...current.participants,
          nodes: current.participants.nodes.map((item) => item.id === participant.id ? { ...item, version: item.version + 1, selectedContactIds: [contactId] } : item)
        }
      } : current);
    } catch (reason) {
      setParticipantNotice(reason instanceof Error ? reason.message : "Não foi possível selecionar o canal.");
    }
  };
  const inviteOriginCapture = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!inviteAssetId || !inviteParticipantId) return;
    setOperationNotice(undefined);
    try {
      const result = await graphql<{ inviteOriginCapture: { userErrors: Array<{ message: string }>; status: string } }>("dashboard", dashboardOperations.inviteOriginCapture, {
        input: { assetId: inviteAssetId, participantId: inviteParticipantId, expiresAt: new Date(inviteExpiresAt).toISOString(), clientMutationId: crypto.randomUUID() }
      });
      const failure = result.inviteOriginCapture.userErrors[0];
      if (failure) throw new Error(failure.message);
      setOperationNotice("Convite de captura enviado pelo canal selecionado.");
    } catch (reason) {
      setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível enviar o convite de captura.");
    }
  };
  const createSchedule = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const templateId = activeTemplateId ?? overview?.templates.nodes[0]?.id;
    if (!scheduleAssetId || !scheduleParticipantId || !templateId) return;
    try {
      const result = await graphql<{ createSchedule: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.createSchedule, {
        input: { assetId: scheduleAssetId, participantId: scheduleParticipantId, templateId, rrule: scheduleRRule, timezone: "America/Sao_Paulo", startsAt: new Date(scheduleStartsAt).toISOString(), deadlineMinutes: 1440, reminderOffsetsMinutes: [1440], clientMutationId: crypto.randomUUID() }
      });
      const failure = result.createSchedule.userErrors[0];
      if (failure) throw new Error(failure.message);
      setOperationNotice("Agenda criada.");
      await load();
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível criar a agenda."); }
  };
  const cancelSchedule = async (schedule: Overview["schedules"]["nodes"][number]) => {
    try {
      const result = await graphql<{ cancelSchedule: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.cancelSchedule, { input: { scheduleId: schedule.id, expectedVersion: schedule.version, clientMutationId: crypto.randomUUID() } });
      const failure = result.cancelSchedule.userErrors[0];
      if (failure) throw new Error(failure.message);
      setOperationNotice("Agenda cancelada.");
      await load();
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível cancelar a agenda."); }
  };
  const cancelInspection = async (inspection: Overview["inspections"]["nodes"][number]) => {
    try {
      const result = await graphql<{ cancelInspection: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.cancelInspection, { input: { inspectionId: inspection.id, expectedVersion: inspection.version, clientMutationId: crypto.randomUUID() } });
      const failure = result.cancelInspection.userErrors[0];
      if (failure) throw new Error(failure.message);
      setOperationNotice("Inspeção cancelada.");
      await load();
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível cancelar a inspeção."); }
  };
  const createInspection = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const templateId = activeTemplateId ?? overview?.templates.nodes[0]?.id;
    if (!inspectionAssetId || !inspectionParticipantId || !templateId) return;
    try {
      const dueAt = new Date(inspectionDueAt);
      const result = await graphql<{ createInspection: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.createInspection, { input: { assetId: inspectionAssetId, participantId: inspectionParticipantId, templateId, dueAt: dueAt.toISOString(), deadlineAt: new Date(dueAt.getTime() + 24 * 60 * 60 * 1000).toISOString(), reminderInstants: [], reason: "inspeção manual", clientMutationId: crypto.randomUUID() } });
      const failure = result.createInspection.userErrors[0];
      if (failure) throw new Error(failure.message);
      setOperationNotice("Inspeção manual criada.");
      await load();
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível criar a inspeção."); }
  };
  const invalidateInspection = async (inspection: Overview["inspections"]["nodes"][number]) => {
    try {
      const result = await graphql<{ invalidateInspection: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.invalidateInspection, { input: { inspectionId: inspection.id, expectedVersion: inspection.version, reason: "invalidada pela operação", clientMutationId: crypto.randomUUID() } });
      const failure = result.invalidateInspection.userErrors[0];
      if (failure) throw new Error(failure.message);
      setOperationNotice("Inspeção invalidada.");
      await load();
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível invalidar a inspeção."); }
  };
  const createProject = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!projectAssetId || !projectParticipantId) return;
    const templateId = activeTemplateId ?? overview?.templates.nodes[0]?.id;
    try {
      const result = await graphql<{ createProject: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.createProject, { input: { assetId: projectAssetId, participantId: projectParticipantId, templateId, clientMutationId: crypto.randomUUID() } });
      const failure = result.createProject.userErrors[0];
      if (failure) throw new Error(failure.message);
      setOperationNotice("Projeto criado.");
      await load();
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível criar o projeto."); }
  };
  const addExceptionalStage = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const project = overview?.projects.nodes.find((item) => item.id === stageProjectId) ?? overview?.projects.nodes[0];
    if (!project || !stageKey.trim() || !stageLabel.trim() || !stageReason.trim()) return;
    try {
      const result = await graphql<{ addExceptionalStage: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.addExceptionalStage, { input: { projectId: project.id, expectedVersion: project.version, key: stageKey.trim(), label: stageLabel.trim(), reason: stageReason.trim(), clientMutationId: crypto.randomUUID() } });
      const failure = result.addExceptionalStage.userErrors[0];
      if (failure) throw new Error(failure.message);
      setStageKey(""); setStageLabel(""); setStageReason("");
      setOperationNotice("Etapa excepcional adicionada.");
      await load();
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível adicionar a etapa."); }
  };
  const startProjectStage = async (project: Overview["projects"]["nodes"][number], stage: Overview["projects"]["nodes"][number]["stages"][number]) => {
    try {
      const dueAt = new Date(Date.now() + 24 * 60 * 60 * 1000);
      const result = await graphql<{ startProjectStage: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.startProjectStage, { input: { projectId: project.id, stageId: stage.id, expectedProjectVersion: project.version, expectedStageVersion: stage.version, dueAt: dueAt.toISOString(), deadlineAt: new Date(dueAt.getTime() + 24 * 60 * 60 * 1000).toISOString(), reminderInstants: [], clientMutationId: crypto.randomUUID() } });
      const failure = result.startProjectStage.userErrors[0];
      if (failure) throw new Error(failure.message);
      setOperationNotice(`Etapa ${stage.label} iniciada.`);
      await load();
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível iniciar a etapa."); }
  };
  const skipProjectStage = async (project: Overview["projects"]["nodes"][number], stage: Overview["projects"]["nodes"][number]["stages"][number]) => {
    try {
      const result = await graphql<{ skipProjectStage: { userErrors: Array<{ message: string }> } }>("dashboard", dashboardOperations.skipProjectStage, { input: { projectId: project.id, stageId: stage.id, expectedVersion: project.version, reason: "etapa não aplicável nesta execução", clientMutationId: crypto.randomUUID() } });
      const failure = result.skipProjectStage.userErrors[0];
      if (failure) throw new Error(failure.message);
      setOperationNotice(`Etapa ${stage.label} marcada como ignorada.`);
      await load();
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível ignorar a etapa."); }
  };
  const reviewReport = async (inspectionId: string) => {
    setReportPreview(undefined);
    setReportDownload(undefined);
    try {
      const result = await graphql<{ report: { id: string; version: number; classification: string; mode: string; html: string } | null }>("dashboard", dashboardOperations.report, { inspectionId });
      if (!result.report) throw new Error("O relatório ainda não está disponível.");
      setReportPreview(result.report);
      setOperationNotice(`Relatório ${result.report.classification} carregado; versão imutável ${result.report.version}.`);
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível carregar o relatório."); }
  };
  const downloadReport = async () => {
    if (!reportPreview) return;
    try {
      const result = await graphql<{ reportDownload: { url: string; kind: string; status: string } | null }>("dashboard", dashboardOperations.reportDownload, { snapshotId: reportPreview.id, kind: "PDF" });
      if (!result.reportDownload?.url) throw new Error("O PDF ainda não está disponível.");
      setReportDownload(result.reportDownload);
      setOperationNotice("Download autorizado por cinco minutos; o endereço não é persistido pelo navegador.");
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível autorizar o download."); }
  };
  const requestRecapture = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!recaptureInspectionId || !recaptureRequirementKey.trim() || !recaptureReason.trim()) return;
    try {
      const result = await graphql<{ requestRecapture: { userErrors: Array<{ message: string }>; recapture?: { status: string } } }>("dashboard", dashboardOperations.requestRecapture, { input: { inspectionId: recaptureInspectionId, items: [{ requirementKey: recaptureRequirementKey.trim(), reason: recaptureReason.trim() }], deadlineAt: new Date(Date.now() + 48 * 60 * 60 * 1000).toISOString(), clientMutationId: crypto.randomUUID() } });
      const failure = result.requestRecapture.userErrors[0];
      if (failure) throw new Error(failure.message);
      setRecaptureReason("");
      setOperationNotice(`Recaptura solicitada (${result.requestRecapture.recapture?.status ?? "PENDING"}).`);
      await load();
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível solicitar a recaptura."); }
  };
  const retentionAction = async (inspection: Overview["inspections"]["nodes"][number], action: "deletion" | "hold" | "release") => {
    try {
      const operation = action === "deletion" ? dashboardOperations.recordDeletionRequest : action === "hold" ? dashboardOperations.applyLegalHold : dashboardOperations.releaseLegalHold;
      const input = action === "deletion" ? { inspectionId: inspection.id, reason: retentionReason.trim() || "solicitação operacional", clientMutationId: crypto.randomUUID() } : { inspectionId: inspection.id, reason: retentionReason.trim() || (action === "hold" ? "retenção legal" : "fim da retenção legal"), clientMutationId: crypto.randomUUID() };
      const field = action === "deletion" ? "recordDeletionRequest" : action === "hold" ? "applyLegalHold" : "releaseLegalHold";
      const result = await graphql<Record<string, { userErrors: Array<{ message: string }>; status?: string }>>("dashboard", operation, { input });
      const failure = result[field].userErrors[0];
      if (failure) throw new Error(failure.message);
      setRetentionReason("");
      setOperationNotice(action === "deletion" ? "Solicitação de exclusão registrada sob a política ativa." : action === "hold" ? "Legal hold aplicado." : "Legal hold liberado.");
      await load();
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível alterar a retenção."); }
  };
  const transitionProject = async (project: Overview["projects"]["nodes"][number], action: "close" | "reopen") => {
    try {
      const operation = action === "close" ? dashboardOperations.closeProject : dashboardOperations.reopenProject;
      const input = action === "close" ? { projectId: project.id, expectedVersion: project.version, clientMutationId: crypto.randomUUID() } : { projectId: project.id, expectedVersion: project.version, reason: "reabertura operacional", clientMutationId: crypto.randomUUID() };
      const result = await graphql<Record<string, { userErrors: Array<{ message: string }> }>>("dashboard", operation, { input });
      const failure = result[action === "close" ? "closeProject" : "reopenProject"].userErrors[0];
      if (failure) throw new Error(failure.message);
      setOperationNotice(action === "close" ? "Projeto encerrado." : "Projeto reaberto.");
      await load();
    } catch (reason) { setOperationNotice(reason instanceof Error ? reason.message : "Não foi possível alterar o projeto."); }
  };
  return <main>
    <header><h1>Central de inspeções</h1><p>Visão interna — informações e relatórios são restritos à sua função e escopo.</p></header>
    {!signedIn && <section className="card warning" aria-live="polite"><p className="status">É necessário entrar novamente após recarregar a página.</p><button onClick={login}>Entrar com conta interna</button></section>}
    {signedIn && <section className="card" aria-live="polite" aria-busy={loading}>
      <h2>Contexto autorizado</h2>
      {loading && <p>Carregando dados autorizados…</p>}
      {error && <><p role="alert">{error}</p><button onClick={() => void load()}>Tentar novamente</button></>}
      {overview && <>
        <p><strong>{overview.tenant?.name ?? "Tenant indisponível"}</strong> · {overview.tenant?.language} · {overview.tenant?.defaultTimezone}</p>
        <p>Papel efetivo: {overview.me.roles.join(", ") || "sem papel"}. Escopos: {overview.me.effectiveScopes.length}.</p>
        <dl className="metrics" aria-label="Resumo operacional">
          <div><dt>Total</dt><dd>{overview.dashboardSummary.total}</dd></div>
          <div><dt>Pendentes</dt><dd>{overview.dashboardSummary.pending}</dd></div>
          <div><dt>Atenção</dt><dd>{overview.dashboardSummary.attention}</dd></div>
          <div><dt>Críticos</dt><dd>{overview.dashboardSummary.critical}</dd></div>
        </dl>
        <p>{overview.triageInspections.nodes.length ? `${overview.triageInspections.nodes.length} inspeções requerem triagem.` : "Nenhuma inspeção pendente de triagem."}</p>
      </>}
    </section>}
    {signedIn && overview?.tenant && <section className="card" id="units:manage" aria-labelledby="units-heading">
      <h2 id="units-heading">Unidades e acesso</h2>
      <p>Unidades configuradas:</p><ul>{overview.businessUnits.nodes.map((unit) => <li key={unit.id} data-resource-id={unit.id}>{editingUnitId === unit.id ? <form onSubmit={(event) => void saveBusinessUnit(event, unit)}><label htmlFor={`edit-unit-code-${unit.id}`}>Código<input id={`edit-unit-code-${unit.id}`} value={editingUnitCode} onChange={(event) => setEditingUnitCode(event.target.value)} required /></label><label htmlFor={`edit-unit-name-${unit.id}`}>Nome<input id={`edit-unit-name-${unit.id}`} value={editingUnitName} onChange={(event) => setEditingUnitName(event.target.value)} required /></label><button type="submit">Salvar unidade</button><button type="button" onClick={() => setEditingUnitId(undefined)}>Cancelar</button></form> : <>{unit.name} ({unit.code}) · {unit.status}{unit.status !== "ARCHIVED" && <><button type="button" onClick={() => { setEditingUnitId(unit.id); setEditingUnitCode(unit.code); setEditingUnitName(unit.name); }} aria-label={`Editar unidade ${unit.name}`}>Editar</button><button type="button" onClick={() => void archiveBusinessUnit(unit)} aria-label={`Arquivar unidade ${unit.name}`}>Arquivar</button></>}</>}</li>)}</ul>
      {overview.me.roles.includes("TENANT_ADMIN") && <><form onSubmit={(event) => void updateTenant(event)}>
        <label htmlFor="tenant-name">Nome da organização<input id="tenant-name" defaultValue={overview.tenant.name} onChange={(event) => setTenantName(event.target.value)} required maxLength={200} /></label>
        <label htmlFor="tenant-timezone">Fuso horário<input id="tenant-timezone" defaultValue={overview.tenant.defaultTimezone} onChange={(event) => setTenantTimezone(event.target.value)} required /></label>
        <button disabled={loading} type="submit">Salvar organização</button>
      </form>{tenantNotice && <p className="status" role="status">{tenantNotice}</p>}<form onSubmit={(event) => void createBusinessUnit(event)}>
        <label htmlFor="unit-code">Código da unidade<input id="unit-code" value={unitCode} onChange={(event) => setUnitCode(event.target.value)} required maxLength={80} /></label>
        <label htmlFor="unit-name">Nome da unidade<input id="unit-name" value={unitName} onChange={(event) => setUnitName(event.target.value)} required maxLength={200} /></label>
        <button disabled={loading} type="submit">Criar unidade</button>
      </form></>}
      {unitNotice && <p className="status" role="status">{unitNotice}</p>}
    </section>}
    <nav aria-label="Navegação interna">
      {navigation.map(([label, scope]) => <a href={`#${scope}`} key={scope}>{label}</a>)}
    </nav>
    {signedIn && overview && <section className="grid" aria-label="Áreas administrativas">
      {canManageAccess && <article className="card" id="access:manage">
        <h2>Pessoas e acesso</h2>
        <p>{overview.memberships.nodes.length} membership(s) no tenant.</p>
        <ul>{overview.memberships.nodes.map((membership) => <li key={membership.id}>{membership.role} · {membership.status} · {membership.scopes.length} escopo(s){isTenantAdmin && membership.status === "ACTIVE" && <button type="button" onClick={() => void disableMembership(membership)} aria-label={`Desativar membership ${membership.id}`}>Desativar</button>}</li>)}</ul>
        {isTenantAdmin && <form onSubmit={(event) => void inviteInternalUser(event)}>
          <label htmlFor="access-issuer">Issuer OIDC<input id="access-issuer" value={accessIssuer} onChange={(event) => setAccessIssuer(event.target.value)} required /></label>
          <label htmlFor="access-subject">Subject OIDC<input id="access-subject" value={accessSubject} onChange={(event) => setAccessSubject(event.target.value)} required /></label>
          <label htmlFor="access-role">Papel<select id="access-role" value={accessRole} onChange={(event) => setAccessRole(event.target.value)}><option>TENANT_ADMIN</option><option>MANAGER</option><option>EMPLOYEE</option><option>VIEWER</option></select></label>
          <button type="submit">Convidar usuário interno</button>
        </form>}
        <form onSubmit={(event) => void assignRoleScope(event)}>
          <label htmlFor="access-membership">Membership<select id="access-membership" value={accessMembership} onChange={(event) => setAccessMembership(event.target.value)}><option value="">Selecione</option>{overview.memberships.nodes.map((membership) => <option key={membership.id} value={membership.id}>{membership.id} · {membership.role}</option>)}</select></label>
          <label htmlFor="access-scope-kind">Tipo de escopo<select id="access-scope-kind" value={accessScopeKind} onChange={(event) => setAccessScopeKind(event.target.value)}><option>BUSINESS_UNIT</option><option>ASSET</option><option>INSPECTION</option></select></label>
          <label htmlFor="access-scope-resource">ID do recurso<input id="access-scope-resource" value={accessScopeResource} onChange={(event) => setAccessScopeResource(event.target.value)} required /></label>
          <button type="submit">Adicionar escopo</button>
        </form>
        {accessNotice && <p className="status" role="status">{accessNotice}</p>}
      </article>}
      <article className="card" id="participants:manage"><h2>Participantes</h2><p>{overview.participants.nodes.length} participante(s) visível(is).</p><ul>{overview.participants.nodes.map((participant) => <li key={participant.id}>{participant.name} · {participant.segmentRole} · {participant.contacts.filter((contact) => contact.verified).length} contato(s) verificado(s)<ul>{participant.contacts.map((contact) => <li key={contact.id}>{contact.channel}: {contact.value} · {contact.verified ? "confirmado" : "pendente"}{!contact.verified && (overview.me.roles.includes("TENANT_ADMIN") || overview.me.roles.includes("MANAGER")) && <button type="button" onClick={() => void verifyContact(contact.id)} aria-label={`Confirmar canal ${contact.value}`}>Confirmar canal</button>}{contact.verified && (overview.me.roles.includes("TENANT_ADMIN") || overview.me.roles.includes("MANAGER")) && <button type="button" onClick={() => void selectDeliveryChannel(participant, contact.id)} aria-label={`Selecionar canal ${contact.value}`}>Usar para convites</button>}</li>)}</ul></li>)}</ul>
        {(overview.me.roles.includes("TENANT_ADMIN") || overview.me.roles.includes("MANAGER")) && <form onSubmit={(event) => void createParticipant(event)}>
          <label htmlFor="participant-name">Nome do participante<input id="participant-name" value={participantName} onChange={(event) => setParticipantName(event.target.value)} required maxLength={200} /></label>
          <label htmlFor="participant-email">E-mail de contato<input id="participant-email" type="email" value={participantEmail} onChange={(event) => setParticipantEmail(event.target.value)} required maxLength={320} /></label>
          <label htmlFor="participant-unit">Unidade<select id="participant-unit" value={participantUnit || overview.businessUnits.nodes[0]?.id || ""} onChange={(event) => setParticipantUnit(event.target.value)}>{overview.businessUnits.nodes.map((unit) => <option key={unit.id} value={unit.id}>{unit.name}</option>)}</select></label>
          <button disabled={loading || overview.businessUnits.nodes.length === 0} type="submit">Criar participante</button>
        </form>}
        {participantNotice && <p className="status" role="status">{participantNotice}</p>}
      </article>
      <article className="card" id="assets:manage">
        <h2>Catálogos e ativos</h2>
        <p>{overview.segmentDefinitions.nodes.length} segmentos, {overview.templates.nodes.length} templates e {overview.assets.nodes.length} ativos visíveis.</p>
        <ul>{overview.assets.nodes.map((asset) => <li key={asset.id}>{asset.name} · {asset.status} · {asset.address}{asset.status !== "ARCHIVED" && <button type="button" onClick={() => void archiveAsset(asset)} aria-label={`Arquivar ativo ${asset.name}`}>Arquivar</button>}</li>)}</ul>
        {isTenantAdmin && <details><summary>Publicar catálogo</summary>
          <form onSubmit={(event) => void publishSegment(event)}>
            <label htmlFor="segment-key">Chave do segmento<input id="segment-key" value={segmentKey} onChange={(event) => setSegmentKey(event.target.value)} required /></label>
            <label htmlFor="segment-name">Nome do segmento<input id="segment-name" value={segmentName} onChange={(event) => setSegmentName(event.target.value)} required /></label>
            <button type="submit">Publicar segmento</button>
          </form>
          <form onSubmit={(event) => void publishTemplate(event)}>
            <label htmlFor="template-key">Chave do template<input id="template-key" value={templateKey} onChange={(event) => setTemplateKey(event.target.value)} required /></label>
            <label htmlFor="template-name">Nome do template<input id="template-name" value={templateName} onChange={(event) => setTemplateName(event.target.value)} required /></label>
            <button type="submit">Publicar template</button>
          </form>
          <form onSubmit={(event) => void publishProfile(event)}>
            <label htmlFor="profile-key">Chave do perfil de análise<input id="profile-key" value={profileKey} onChange={(event) => setProfileKey(event.target.value)} required /></label>
            <button type="submit">Publicar perfil de análise</button>
          </form>
        </details>}
        {(isTenantAdmin || overview.me.roles.includes("MANAGER")) && <details><summary>Registrar ativo</summary>
          <form onSubmit={(event) => void registerAsset(event)}>
            <label htmlFor="asset-name">Nome do ativo<input id="asset-name" value={assetName} onChange={(event) => setAssetName(event.target.value)} required /></label>
            <label htmlFor="asset-external-key">Identificador externo<input id="asset-external-key" value={assetExternalKey} onChange={(event) => setAssetExternalKey(event.target.value)} required /></label>
            <label htmlFor="asset-address">Endereço<input id="asset-address" value={assetAddress} onChange={(event) => setAssetAddress(event.target.value)} required /></label>
            <label htmlFor="asset-participant">Participante atribuído<select id="asset-participant" value={assetParticipantId} onChange={(event) => setAssetParticipantId(event.target.value)}><option value="">Nenhum</option>{overview.participants.nodes.filter((participant) => participant.status === "ACTIVE").map((participant) => <option key={participant.id} value={participant.id}>{participant.name}</option>)}</select></label>
            <button type="submit">Registrar ativo</button>
          </form>
        </details>}
        {operationNotice && <p className="status" role="status">{operationNotice}</p>}
      </article>
      <article className="card" id="inspections:manage">
        <h2>Agendas e projetos</h2>
        <p>{overview.schedules.nodes.length} agendas, {overview.projects.nodes.length} projetos e {overview.inspections.nodes.length} inspeções.</p>
        <ul>{overview.schedules.nodes.map((schedule) => <li key={schedule.id}>{schedule.status} · próxima data {new Date(schedule.nextDueAt).toLocaleString("pt-BR")}{schedule.status === "ACTIVE" && <button type="button" onClick={() => void cancelSchedule(schedule)} aria-label={`Cancelar agenda ${schedule.id}`}>Cancelar</button>}</li>)}</ul>
        <ul>{overview.projects.nodes.map((project) => <li key={project.id}>Projeto {project.status} · {project.reportMode}{project.status !== "CLOSED" && <button type="button" onClick={() => void transitionProject(project, "close")} aria-label={`Encerrar projeto ${project.id}`}>Encerrar</button>}{project.status === "CLOSED" && <button type="button" onClick={() => void transitionProject(project, "reopen")} aria-label={`Reabrir projeto ${project.id}`}>Reabrir</button>}<ul>{project.stages.map((stage) => <li key={stage.id}>{stage.label} · {stage.status}{stage.status === "PLANNED" && <><button type="button" onClick={() => void startProjectStage(project, stage)} aria-label={`Iniciar etapa ${stage.label}`}>Iniciar</button><button type="button" onClick={() => void skipProjectStage(project, stage)} aria-label={`Ignorar etapa ${stage.label}`}>Ignorar</button></>}</li>)}</ul></li>)}</ul>
        <ul>{overview.inspections.nodes.map((inspection) => <li key={inspection.id}>Inspeção {inspection.status} · {new Date(inspection.dueAt).toLocaleString("pt-BR")}{inspection.status !== "INVALIDATED" && inspection.status !== "CANCELLED" && <><button type="button" onClick={() => void cancelInspection(inspection)} aria-label={`Cancelar inspeção ${inspection.id}`}>Cancelar</button><button type="button" onClick={() => void invalidateInspection(inspection)} aria-label={`Invalidar inspeção ${inspection.id}`}>Invalidar</button></>}</li>)}</ul>
        {(isTenantAdmin || overview.me.roles.includes("MANAGER") || overview.me.roles.includes("EMPLOYEE")) && <details><summary>Enviar convite de captura</summary>
          <form onSubmit={(event) => void inviteOriginCapture(event)}>
            <label htmlFor="invite-asset">Ativo<select id="invite-asset" value={inviteAssetId} onChange={(event) => setInviteAssetId(event.target.value)} required><option value="">Selecione</option>{overview.assets.nodes.filter((asset) => asset.status === "ACTIVE").map((asset) => <option key={asset.id} value={asset.id}>{asset.name} · {asset.externalKey}</option>)}</select></label>
            <label htmlFor="invite-participant">Participante<select id="invite-participant" value={inviteParticipantId} onChange={(event) => setInviteParticipantId(event.target.value)} required><option value="">Selecione</option>{overview.participants.nodes.filter((participant) => participant.status === "ACTIVE").map((participant) => <option key={participant.id} value={participant.id}>{participant.name}</option>)}</select></label>
            <label htmlFor="invite-expires">Expira em<input id="invite-expires" type="datetime-local" value={inviteExpiresAt} onChange={(event) => setInviteExpiresAt(event.target.value)} required /></label>
            <button type="submit">Enviar convite de captura</button>
          </form>
        </details>}
        {(isTenantAdmin || overview.me.roles.includes("MANAGER")) && <details><summary>Planejar operação</summary>
          <form onSubmit={(event) => void createSchedule(event)}>
            <label htmlFor="schedule-asset">Ativo<select id="schedule-asset" value={scheduleAssetId} onChange={(event) => setScheduleAssetId(event.target.value)} required><option value="">Selecione</option>{overview.assets.nodes.filter((asset) => asset.status === "ACTIVE").map((asset) => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label>
            <label htmlFor="schedule-participant">Participante<select id="schedule-participant" value={scheduleParticipantId} onChange={(event) => setScheduleParticipantId(event.target.value)} required><option value="">Selecione</option>{overview.participants.nodes.filter((participant) => participant.status === "ACTIVE").map((participant) => <option key={participant.id} value={participant.id}>{participant.name}</option>)}</select></label>
            <label htmlFor="schedule-starts">Início<input id="schedule-starts" type="datetime-local" value={scheduleStartsAt} onChange={(event) => setScheduleStartsAt(event.target.value)} required /></label>
            <label htmlFor="schedule-rrule">Recorrência RRULE<input id="schedule-rrule" value={scheduleRRule} onChange={(event) => setScheduleRRule(event.target.value)} required /></label>
            <button type="submit">Criar agenda</button>
          </form>
          <form onSubmit={(event) => void createInspection(event)}>
            <label htmlFor="inspection-asset">Ativo da inspeção<select id="inspection-asset" value={inspectionAssetId} onChange={(event) => setInspectionAssetId(event.target.value)} required><option value="">Selecione</option>{overview.assets.nodes.filter((asset) => asset.status === "ACTIVE").map((asset) => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label>
            <label htmlFor="inspection-participant">Participante da inspeção<select id="inspection-participant" value={inspectionParticipantId} onChange={(event) => setInspectionParticipantId(event.target.value)} required><option value="">Selecione</option>{overview.participants.nodes.filter((participant) => participant.status === "ACTIVE").map((participant) => <option key={participant.id} value={participant.id}>{participant.name}</option>)}</select></label>
            <label htmlFor="inspection-due">Vencimento<input id="inspection-due" type="datetime-local" value={inspectionDueAt} onChange={(event) => setInspectionDueAt(event.target.value)} required /></label>
            <button type="submit">Criar inspeção manual</button>
          </form>
          <form onSubmit={(event) => void createProject(event)}>
            <label htmlFor="project-asset">Ativo do projeto<select id="project-asset" value={projectAssetId} onChange={(event) => setProjectAssetId(event.target.value)} required><option value="">Selecione</option>{overview.assets.nodes.filter((asset) => asset.status === "ACTIVE").map((asset) => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label>
            <label htmlFor="project-participant">Participante do projeto<select id="project-participant" value={projectParticipantId} onChange={(event) => setProjectParticipantId(event.target.value)} required><option value="">Selecione</option>{overview.participants.nodes.filter((participant) => participant.status === "ACTIVE").map((participant) => <option key={participant.id} value={participant.id}>{participant.name}</option>)}</select></label>
            <button type="submit">Criar projeto</button>
          </form>
          <form onSubmit={(event) => void addExceptionalStage(event)}>
            <label htmlFor="stage-project">Projeto<select id="stage-project" value={stageProjectId} onChange={(event) => setStageProjectId(event.target.value)} required><option value="">Selecione</option>{overview.projects.nodes.filter((project) => project.status !== "CLOSED").map((project) => <option key={project.id} value={project.id}>{project.id}</option>)}</select></label>
            <label htmlFor="stage-key">Chave da etapa<input id="stage-key" value={stageKey} onChange={(event) => setStageKey(event.target.value)} required /></label>
            <label htmlFor="stage-label">Nome da etapa<input id="stage-label" value={stageLabel} onChange={(event) => setStageLabel(event.target.value)} required /></label>
            <label htmlFor="stage-reason">Motivo<input id="stage-reason" value={stageReason} onChange={(event) => setStageReason(event.target.value)} required /></label>
            <button type="submit">Adicionar etapa excepcional</button>
          </form>
        </details>}
        {operationNotice && <p className="status" role="status">{operationNotice}</p>}
      </article>
      <article className="card" id="reports:read"><h2>Triagem e relatórios</h2><p>{overview.triageInspections.nodes.length} item(ns) de triagem. Relatórios imutáveis permanecem restritos à função autorizada.</p><ul>{overview.triageInspections.nodes.map((item) => <li key={item.inspectionId}>{item.classification} · {item.status} · <button type="button" onClick={() => void reviewReport(item.inspectionId)} aria-label={`Abrir relatório ${item.inspectionId}`}>Abrir relatório</button></li>)}</ul>{overview.inspections.nodes.length > 0 && <form onSubmit={(event) => { event.preventDefault(); if (reportInspectionId) void reviewReport(reportInspectionId); }}><label htmlFor="report-inspection">Inspeção<select id="report-inspection" value={reportInspectionId} onChange={(event) => setReportInspectionId(event.target.value)} required><option value="">Selecione</option>{overview.inspections.nodes.map((inspection) => <option key={inspection.id} value={inspection.id}>{inspection.id} · {inspection.status}</option>)}</select></label><button type="submit">Consultar relatório</button></form>}{reportPreview && <div className="notice" role="status"><p>Relatório {reportPreview.classification} · versão {reportPreview.version} · modo {reportPreview.mode}.</p><p>Conteúdo imutável disponível para revisão interna; o HTML não é injetado na página.</p><button type="button" onClick={() => void downloadReport()}>Autorizar download PDF</button>{reportDownload && <a href={reportDownload.url} target="_blank" rel="noreferrer" download>Baixar relatório PDF</a>}</div>}<form onSubmit={(event) => void requestRecapture(event)}><label htmlFor="recapture-inspection">Solicitar recaptura para<select id="recapture-inspection" value={recaptureInspectionId} onChange={(event) => setRecaptureInspectionId(event.target.value)} required><option value="">Selecione</option>{overview.inspections.nodes.map((inspection) => <option key={inspection.id} value={inspection.id}>{inspection.id} · {inspection.status}</option>)}</select></label><label htmlFor="recapture-requirement">Requisito<input id="recapture-requirement" value={recaptureRequirementKey} onChange={(event) => setRecaptureRequirementKey(event.target.value)} required /></label><label htmlFor="recapture-reason">Motivo<input id="recapture-reason" value={recaptureReason} onChange={(event) => setRecaptureReason(event.target.value)} required /></label><button type="submit">Solicitar recaptura</button></form></article>
      <article className="card" id="audit:read"><h2>Auditoria</h2><p>Eventos append-only disponíveis no seu escopo.</p><ul>{overview.auditEvents.nodes.map((event) => <li key={event.id}>{event.action} em {event.targetType} · {event.outcome} · {event.reason ?? "sem motivo"}</li>)}</ul></article>
      <article className="card" id="notifications:read"><h2>Notificações</h2><p>{overview.notificationDeliveries.nodes.length} entrega(s) de notificação no período consultado.</p><ul>{overview.notificationDeliveries.nodes.map((delivery) => <li key={delivery.id}>{delivery.status} · criada {new Date(delivery.createdAt).toLocaleString("pt-BR")} · atualizada {new Date(delivery.updatedAt).toLocaleString("pt-BR")}</li>)}</ul></article>
      <article className="card" id="retention:manage">
        <h2>Uso e retenção</h2>
        <p>{overview.usageSummary.requests} solicitações; custo estimado: {overview.usageSummary.cost.toLocaleString("pt-BR", { style: "currency", currency: "USD" })}.</p>
        <ul>{overview.retentionPolicies.nodes.map((policy) => <li key={policy.id}>Evidências: {policy.evidenceDays} dias · operação: {policy.operationalDays} dias · segurança: {policy.securityDays} dias</li>)}</ul>
        {isTenantAdmin && <form onSubmit={(event) => void configureRetention(event)}>
          <label htmlFor="retention-evidence">Retenção de evidências (dias)<input id="retention-evidence" type="number" min="1" value={retentionEvidenceDays} onChange={(event) => setRetentionEvidenceDays(event.target.value)} required /></label>
          <label htmlFor="retention-operational">Retenção operacional (dias)<input id="retention-operational" type="number" min="1" value={retentionOperationalDays} onChange={(event) => setRetentionOperationalDays(event.target.value)} required /></label>
          <label htmlFor="retention-security">Retenção de segurança (dias)<input id="retention-security" type="number" min="1" value={retentionSecurityDays} onChange={(event) => setRetentionSecurityDays(event.target.value)} required /></label>
          <button type="submit">Salvar retenção</button>
        </form>}
        <form onSubmit={(event) => { event.preventDefault(); const inspection = overview.inspections.nodes.find((item) => item.id === retentionInspectionId); if (inspection) void retentionAction(inspection, "deletion"); }}>
          <label htmlFor="retention-inspection">Inspeção para privacidade<select id="retention-inspection" value={retentionInspectionId} onChange={(event) => setRetentionInspectionId(event.target.value)} required><option value="">Selecione</option>{overview.inspections.nodes.map((inspection) => <option key={inspection.id} value={inspection.id}>{inspection.id} · {inspection.status}</option>)}</select></label>
          <label htmlFor="retention-reason">Motivo de retenção/privacidade<input id="retention-reason" value={retentionReason} onChange={(event) => setRetentionReason(event.target.value)} required /></label>
          <button type="submit">Registrar pedido de exclusão</button>
        </form>
        {overview.inspections.nodes.map((inspection) => <div key={`hold-${inspection.id}`}><button type="button" onClick={() => void retentionAction(inspection, "hold")} aria-label={`Aplicar legal hold ${inspection.id}`}>Aplicar legal hold</button><button type="button" onClick={() => void retentionAction(inspection, "release")} aria-label={`Liberar legal hold ${inspection.id}`}>Liberar legal hold</button></div>)}
      </article>
    </section>}
    <section className="card"><h2>Triagem responsável</h2><p>Relatórios e classificações são apoio interno; não atribuem culpa, custo ou consequência automática.</p><p>Downloads de relatório, evidências e notificações ficam restritos ao escopo interno autorizado.</p></section>
  </main>;
}
