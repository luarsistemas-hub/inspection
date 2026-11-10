import { expect, test, type Page } from "@playwright/test";
import { loginAsMockAdmin } from "./support/auth";

const pageInfo = { endCursor: null, hasNextPage: false };
const unknownOperationsByPage = new WeakMap<Page, string[]>();
test.afterEach(async ({ page }) => {
  const unknownOperations = unknownOperationsByPage.get(page) ?? [];
  expect(unknownOperations, `Operações GraphQL sem mock: ${unknownOperations.join(", ")}`).toEqual([]);
});
const counters = { pendingVoiceItems: 2, conflictItems: 1, staleSuggestions: 1, notesWithoutItem: 1 };
const revision = { id: "revision-current", revisionNumber: 2, text: "cinco pratos", kind: "HUMAN_EDIT", authorId: "reviewer-a", authorName: "Revisora QA", callId: null, createdAt: "2026-10-31T12:01:00Z" };
const originalRevision = { id: "revision-original", revisionNumber: 1, text: "seis pratos", kind: "MODEL", authorId: null, authorName: null, callId: "call-a", createdAt: "2026-10-31T12:00:00Z" };

function makeNote(overrides: Record<string, unknown> = {}) {
  return {
    id: "note-a", inspectionId: "inspection-a", environmentId: "room-a", environmentName: "Cozinha", responsibilityId: "responsibility-a", requirementKey: "kitchen",
    targetKind: "VIDEO_INTERVAL", photoMediaId: null, videoAttemptId: "clip-a", sourceMediaId: "media-a", startMs: 12500, endMs: 18700, durationMs: 6200,
    status: "COMPLETED", anchorStatus: "VALID", anchor: { kind: "VIDEO_INTERVAL", status: "VALID", mediaId: "media-a", mediaStatus: "READY", approximate: true, previewUrl: null, playbackUrl: null, frames: [{ id: "frame-a", timestampMs: 15000, url: "/media/video-evidence/frame-a?inspectionId=inspection-a", status: "AVAILABLE" }], framesTruncated: true, visualCoverage: "FRAMES" },
    highlighted: false, highlightSource: null, version: 2, currentRevisionId: revision.id, textConfirmed: false, noSpeech: false, failureCode: null, audioAvailability: "UNAVAILABLE", audioDeletionStatus: "DELETED", createdAt: "2026-10-31T12:00:00Z", updatedAt: "2026-10-31T12:01:00Z", revisions: [originalRevision, revision],
    ...overrides,
  };
}

async function installDashboardMocks(page: Page, options: { anchorStatus?: string; mediaStatus?: string; highlight?: boolean } = {}) {
  const unknownOperations: string[] = [];
  unknownOperationsByPage.set(page, unknownOperations);
  const regenerations: Array<Record<string, unknown>> = [];
  const state = { regenerations, note: makeNote({ anchorStatus: options.anchorStatus ?? "VALID", anchor: { ...makeNote().anchor, mediaStatus: options.mediaStatus ?? "READY" }, highlighted: options.highlight ?? false }), lateNote: makeNote({ id: "note-late", videoAttemptId: "clip-late", revisions: [{ ...originalRevision, id: "revision-late", text: "Uma nota tardia" }], currentRevisionId: "revision-late" }), revised: false, confirmed: false, highlightMutation: false };
  await page.route("**/graphql", async (route) => {
    const request = route.request().postDataJSON() as { query: string; variables?: Record<string, unknown> };
    const operation = request.query.match(/(?:query|mutation)\s+(\w+)/)?.[1] ?? "unknown";
    let data: Record<string, unknown> = {};
    if (operation === "DashboardGate") data = { me: { tenantId: "tenant-a", productEntitlements: ["DASHBOARD"], roles: ["MANAGER"], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-a" }] }, tenant: { id: "tenant-a", name: "Operação QA", status: "ACTIVE" } };
    if (operation === "DashboardMemberships") data = { me: { memberships: [{ id: "membership-a", tenantId: "tenant-a", name: "Revisora QA", email: "reviewer@example.test", role: "MANAGER", status: "ACTIVE" }] } };
    if (operation === "MyNotifications") data = { myNotifications: { nodes: [], unreadCount: 0, pageInfo } };
    if (operation === "Inspections") data = { inspections: { nodes: [], pageInfo } };
    if (operation === "InspectionDetail") data = { inspection: { id: "inspection-a", assetId: "asset-a", participantId: "participant-a", projectId: null, stageId: null, source: "MANUAL", sourceReason: null, stateReason: null, status: "COMPLETED", mediaKind: "VIDEO", comparisonMode: "CHECKLIST_ONLY", referenceVersionId: null, referenceVersionNumber: null, referenceMediaKind: null, referenceVersionStatus: null, referenceIsActive: null, evidenceCount: 1, dueAt: "2026-10-31T12:00:00Z", deadlineAt: "2026-10-31T13:00:00Z", reminderInstants: [], version: 1, assetName: "Apartamento QA", assetAddress: "Rua de teste", assetExternalKey: "QA-1", participantName: "Participante QA", attention: null } };
    if (operation === "InspectionCoverage") data = { inspectionCoverage: { expected: 0, fullyAssessed: 0, partiallyAssessed: 0, notObserved: 0, notApplicable: 0, impossible: 0, unknownScope: true, pendingRecords: [], suggestions: [] } };
    if (operation === "DashboardAudioNotes") data = { audioNotes: { nodes: [state.note, state.lateNote], pageInfo } };
    if (operation === "DashboardAudioNote") data = { audioNote: request.variables?.id === "note-late" ? state.lateNote : state.note };
    if (operation === "DashboardFormOptions") data = { assets: { nodes: [], pageInfo }, participants: { nodes: [], pageInfo }, templates: { nodes: [], pageInfo } };
    if (operation === "DashboardReviewers") data = { memberships: { nodes: [], pageInfo } };
    if (operation === "Projects") data = { projects: { nodes: [], pageInfo } };
    if (operation === "DashboardOriginVersions") data = { originVersions: { nodes: [], pageInfo } };
    if (operation === "DashboardTemplateVersion") data = { templateVersion: null };
    if (operation === "InspectionOriginPromotion") data = { inspectionOriginPromotion: { inspectionId: "inspection-a", eligible: false, impediments: [], activeOrigin: null, conflictingDraft: null, automaticCatalogSkipped: false, confirmationDigest: null, currentPromotion: null, generatedVersionActive: false } };
    if (operation === "ReviseAudioNote") {
      const input = request.variables?.input as { noteId: string; text: string };
      const target = input.noteId === "note-late" ? state.lateNote : state.note;
      target.revisions.push({ id: "revision-edited", revisionNumber: 3, text: input.text, kind: "HUMAN_EDIT", authorId: "reviewer-a", authorName: "Revisora QA", callId: null, createdAt: "2026-10-31T12:02:00Z" });
      target.currentRevisionId = "revision-edited"; target.version += 1; state.revised = true;
      data = { reviseAudioNote: { note: target, userErrors: [], clientMutationId: "edit-a" } };
    }
    if (operation === "ConfirmAudioNoteText") { state.note.textConfirmed = true; state.note.version += 1; state.confirmed = true; data = { confirmAudioNoteText: { note: state.note, userErrors: [], clientMutationId: "confirm-a" } }; }
    if (operation === "SetAudioNoteHighlight") { state.note.highlighted = false; state.note.version += 1; state.highlightMutation = true; data = { setAudioNoteHighlight: { note: state.note, userErrors: [], clientMutationId: "highlight-a" } }; }
    if (operation === "DashboardInventoryDraft") data = { inventoryDraft: { assetId: "asset-a", sourceInspectionId: "inspection-a", version: { id: "draft-a", originRevision: 1, versionNumber: 1, status: "DRAFT", draftState: "READY", hasInventory: true, purpose: null, counts: { included: 0, rejected: 0, notReviewed: 1 } }, progress: { decided: 0, pending: 1 }, readOnly: false, noticeCode: null, warnings: [], voiceSummary: counters, environments: [{ id: "room-a", identityId: "room-a", typeCode: "KITCHEN", name: "Cozinha", requirementKey: "kitchen", expected: true, addedDuringCapture: false, decision: "PENDING", position: 0, voiceSummary: counters, items: [{ id: "item-a", identityId: "item-a", environmentId: "room-a", typeCode: "PLATE", name: "Pratos", material: null, color: null, quantity: 5, condition: "GOOD", observation: "OBSERVED", limitation: null, confidenceBps: 7000, origin: "AI", declared: true, decision: "PENDING", rejectionReason: null, version: 1, voiceConflicts: [{ attributeName: "quantidade", visualValue: "4", voiceValue: "5" }], evidence: [], attributeSources: [{ id: "source-a", attributeName: "quantidade", sourceKind: "VOICE", noteId: "note-a", revisionId: "revision-original", currentRevisionId: "revision-current", stale: true, visualEvidenceId: null, reviewedBy: null, createdAt: "2026-10-31T12:00:00Z" }] }] }] } };
    if (operation === "DashboardCounterpartyOptions") data = { participants: { nodes: [], pageInfo } };
    if (operation === "DashboardBaselineAsset") data = { asset: { id: "asset-a", name: "Apartamento QA", externalKey: "QA-1", businessUnitId: "unit-a" } };
    if (operation === "RegenerateInventorySuggestions") regenerations.push(request.variables?.input as Record<string, unknown>);
    if (operation === "RegenerateInventorySuggestions") data = { regenerateInventorySuggestions: { draft: null, userErrors: [], clientMutationId: "regen-a" } };
    const knownOperations = new Set(["DashboardGate", "DashboardMemberships", "MyNotifications", "Inspections", "InspectionDetail", "InspectionCoverage", "DashboardAudioNotes", "DashboardAudioNote", "DashboardFormOptions", "DashboardReviewers", "Projects", "DashboardOriginVersions", "DashboardTemplateVersion", "InspectionOriginPromotion", "ReviseAudioNote", "ConfirmAudioNoteText", "SetAudioNoteHighlight", "DashboardInventoryDraft", "DashboardCounterpartyOptions", "DashboardBaselineAsset", "RegenerateInventorySuggestions"]);
    if (!knownOperations.has(operation)) {
      unknownOperations.push(operation);
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ errors: [{ message: `Operação GraphQL sem mock: ${operation}` }] }) });
      return;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data }) });
  });
  return state;
}

async function openInspectionNotes(page: Page) {
  await loginAsMockAdmin(page, "/inspections?inspectionId=inspection-a");
  await expect(page.getByRole("heading", { name: "Anotações de voz" })).toBeVisible();
}

test("IT-126..IT-138 E2E-010 text correction, confirmation and separate item decision", async ({ page }) => {
  const state = await installDashboardMocks(page);
  await openInspectionNotes(page);
  const review = page.getByRole("region", { name: "Revisão da anotação de voz" });
  const originalText = review.getByRole("region", { name: "Texto original" });
  const currentText = review.getByRole("region", { name: "Revisão atual" });
  await expect(originalText).toContainText("seis pratos");
  await expect(originalText).toContainText("Transcrição automática");
  await expect(originalText).toContainText("31/10/2026");
  await expect(currentText).toContainText("cinco pratos");
  await expect(currentText).toContainText("Revisora QA");
  await expect(currentText).toContainText("31/10/2026");
  await page.getByLabel("Corrigir texto da anotação").fill("cinco pratos revisados");
  await page.getByRole("button", { name: "Salvar correção" }).click();
  await expect(review.getByRole("region", { name: "Revisão atual" })).toContainText("cinco pratos revisados");
  await page.getByLabel("Corrigir texto da anotação").fill("cinco pratos revisados");
  await page.getByRole("button", { name: "Confirmar texto" }).click();
  await expect(review.getByText("Texto confirmado · somente leitura", { exact: true })).toBeVisible();
  expect(state.revised).toBe(true);
  expect(state.confirmed).toBe(true);
  await expect(page.getByRole("button", { name: /Aceitar item/ })).toHaveCount(0);
});

test("IT-139..IT-152 E2E-011 anchor status, approximate interval and retained context", async ({ page }) => {
  await installDashboardMocks(page, { mediaStatus: "UNAVAILABLE" });
  await openInspectionNotes(page);
  const anchor = page.getByRole("region", { name: "Âncora da anotação" });
  await expect(anchor.getByText("Intervalo aproximado 00:12–00:18", { exact: true })).toBeVisible();
  await expect(anchor.getByText("Vídeo original indisponível", { exact: true })).toBeVisible();
  await expect(anchor.getByRole("heading", { name: "Contexto visual" })).toBeVisible();
  await expect(anchor.getByText("Mostrando alguns quadros; outros não foram revisados", { exact: true })).toBeVisible();
  const noteButtons = page.getByRole("navigation", { name: "Navegação das anotações de voz" }).getByRole("button", { name: /Nota \d+, Intervalo aproximado/ });
  await expect(noteButtons).toHaveCount(2);
  const firstNoteLabel = await noteButtons.nth(0).getAttribute("aria-label");
  const secondNoteLabel = await noteButtons.nth(1).getAttribute("aria-label");
  expect(firstNoteLabel).not.toBe(secondNoteLabel);
  await expect(page.locator("a[href*='X-Amz'], img[src*='X-Amz'], video[src*='X-Amz']")).toHaveCount(0);
});

test("IT-170/IT-175/IT-176/IT-178/IT-206 counters, conflicts, stale citation and explicit late-note regeneration", async ({ page }) => {
  const state = await installDashboardMocks(page);
  await loginAsMockAdmin(page, "/baselines/review/draft-a");
  const voiceSummary = page.getByRole("region", { name: "Resumo das anotações de voz" });
  await expect(voiceSummary.getByText("Itens de voz pendentes", { exact: true })).toBeVisible();
  await expect(voiceSummary.getByText("Conflitos voz × imagem", { exact: true })).toBeVisible();
  await expect(page.getByText("Imagem: 4")).toBeVisible();
  await expect(page.getByText("Voz: 5")).toBeVisible();
  await page.locator("details.baseline-voice-source > summary").click();
  await expect(page.locator("details.baseline-voice-source").getByText(/Baseado em revisão anterior/)).toBeVisible();
  await page.locator("details.baseline-room-actions > summary").click();
  const lateNotes = page.getByRole("group", { name: "Notas sem item em Cozinha" });
  await expect(lateNotes).toBeVisible();
  const regenerate = lateNotes.getByRole("button", { name: "Regenerar sugestões selecionadas" });
  // Regeneration is explicit: nothing is sent until the operator selects a late note.
  await expect(regenerate).toBeDisabled();
  await lateNotes.getByRole("checkbox", { name: "Uma nota tardia" }).check();
  await regenerate.click();
  await expect.poll(() => state.regenerations.length).toBe(1);
  expect(JSON.stringify(state.regenerations[0])).toContain("revision-late");
});

test("E2E-009 operator removes participant highlight without changing inventory", async ({ page }) => {
  const state = await installDashboardMocks(page, { highlight: true });
  await openInspectionNotes(page);
  await expect(page.getByText("Destaque do participante")).toBeVisible();
  await page.getByRole("region", { name: "Revisão da anotação de voz" }).getByRole("button", { name: "Remover destaque", exact: true }).click();
  await expect(page.getByRole("alertdialog")).toContainText("não altera nenhum item do inventário");
  await page.getByRole("alertdialog").getByRole("button", { name: "Remover destaque" }).click();
  await expect(page.getByText("Destaque do participante")).toHaveCount(0);
  expect(state.highlightMutation).toBe(true);
});
