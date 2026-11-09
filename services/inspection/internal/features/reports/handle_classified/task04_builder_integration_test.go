//go:build integration

package handle_classified

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	"inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

func TestUT049UT110UT113BuildDescriptiveReportUsesCatalogFindingVoiceAndHandoverFacts(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, _ notificationrequest.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", true)
		now := time.Now().UTC()
		itemID, originID, versionID := identity.NewID(), identity.NewID(), identity.NewID()
		evidenceRowID, noteID, revisionID := identity.NewID(), identity.NewID(), identity.NewID()
		componentKey := "floor"
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			if err := tx.Create(&database.Origin{ID: originID, TenantID: f.TenantID, AssetID: f.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: f.TenantID, OriginID: originID, VersionNumber: 1, Source: "CAPTURE", SourceInspectionID: &f.InspectionID, HasInventory: true, Status: "DRAFT", IdempotencyKey: "ut049-" + versionID.String(), CreatedAt: now}).Error; err != nil {
				return err
			}
			environmentID := identity.NewID()
			if err := tx.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: f.TenantID, OriginVersionID: versionID, IdentityID: f.EnvironmentIdentityID, TypeCode: "KITCHEN", Name: "Cozinha", Expected: true, Decision: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.InventoryItem{ID: itemID, TenantID: f.TenantID, OriginVersionID: versionID, EnvironmentID: environmentID, IdentityID: identity.NewID(), TypeCode: "FLOOR", Name: "Piso", Description: "Piso visualmente conservado", ComponentKey: &componentKey, Condition: "GOOD", Observation: "OBSERVED", Decision: "PENDING", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.InventoryItemEvidence{ID: evidenceRowID, TenantID: f.TenantID, ItemID: itemID, Kind: "PHOTO", MediaID: f.MediaID, Position: 1, CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,inspection_id,responsibility_id,client_note_id,requirement_key,consent_version,disclosure_version,target_kind,photo_media_id,duration_ms,content_sha256,content_type,size_bytes,status,anchor_status,version,current_revision_id,audio_deletion_status,created_at,updated_at)
				VALUES(?,?,?,?,?,?,?,?, 'PHOTO', ?,500,?,'audio/ogg',1,'COMPLETED','VALID',1,NULL,'PENDING',?,?)`,
				f.TenantID, noteID, f.InspectionID, f.ResponsibilityID, identity.NewID(), "env:new:"+f.EnvironmentIdentityID.String(), "consent-v1", "disclosure-v1", f.MediaID, strings.Repeat("a", 64), now, now).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.TranscriptRevision{ID: revisionID, TenantID: f.TenantID, NoteID: noteID, RevisionNumber: 1, Text: "O piso foi declarado como trincado.", Kind: "PARTICIPANT_DECLARATION", CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Exec("UPDATE transcription.audio_notes SET current_revision_id=? WHERE tenant_id=? AND id=?", revisionID, f.TenantID, noteID).Error; err != nil {
				return err
			}
			visualEvidenceID := evidenceRowID
			sources := []database.InventoryItemAttributeSource{
				{ID: identity.NewID(), TenantID: f.TenantID, ItemID: itemID, OriginVersionID: versionID, AttributeName: "condition", SourceKind: "VOICE", NoteID: &noteID, RevisionID: &revisionID, CreatedAt: now},
				{ID: identity.NewID(), TenantID: f.TenantID, ItemID: itemID, OriginVersionID: versionID, AttributeName: "condition", SourceKind: "VISUAL", VisualEvidenceID: &visualEvidenceID, CreatedAt: now},
			}
			if err := tx.Create(&sources).Error; err != nil {
				return err
			}
			return tx.Create(&database.HandoverRecord{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, RecordKey: "energy-meter", Kind: "METER", Payload: json.RawMessage(`{"meterType":"ENERGY","identifier":"A123","reading":123.4,"unit":"kWh","date":"2026-11-04"}`), MediaIDs: mustJSONIDs(t, f.MediaID), Declarant: "Participante", Version: 1, CreatedAt: now, UpdatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
		evidence := []core.Evidence{{ID: f.MediaID.String(), RequirementKey: "env:new:" + f.EnvironmentIdentityID.String(), Role: "CURRENT"}}
		findings := []core.Finding{{ID: "finding-1", Title: "Trinca visível", Description: "Há uma trinca na imagem.", Severity: "MEDIUM", EvidenceIDs: []string{f.MediaID.String()}}}
		var checklist *core.ChecklistSection
		var handover *core.HandoverSection
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			var err error
			checklist, _, _, handover, _, err = buildDescriptiveReport(ctx, tx, f.TenantID, f.InspectionID, evidence, findings, "prompt", "COMPLETED")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		component := checklist.Rooms[0].Components[0]
		if !component.Conflict || component.Description != "Piso visualmente conservado" || component.Condition != "GOOD" || !strings.Contains(strings.Join(component.Limitations, " "), "fontes visual e por voz") {
			t.Fatalf("builder did not preserve the catalog/finding conflict: %+v", component)
		}
		visualEvidenceLinked := false
		for _, source := range component.AttributeSources {
			if source.Attribute == "condition" && source.Source == "VISUAL" && len(source.EvidenceIDs) == 1 && source.EvidenceIDs[0] == f.MediaID.String() {
				visualEvidenceLinked = true
			}
		}
		if !hasAttributeSource(component.AttributeSources, "condition", "VOICE") || !hasAttributeSource(component.AttributeSources, "condition", "VISUAL") || !visualEvidenceLinked {
			t.Fatalf("builder omitted conflicting voice/visual source summary: %+v", component.AttributeSources)
		}
		if findings[0].Title != "Trinca visível" || findings[0].EvidenceIDs[0] != component.EvidenceIDs[0] {
			t.Fatalf("finding was overwritten or detached from evidence: %+v", findings[0])
		}
		meterFound, pendingFound := false, false
		for _, entry := range handover.Entries {
			if entry.Kind == "METER" {
				meterFound = entry.Status == "DECLARED" && entry.Declarant == "Participante" && strings.Contains(string(entry.Payload), "123.4") && len(entry.EvidenceIDs) == 1
			}
			if entry.Kind == "KEY" && entry.Status == "PENDING" && len(entry.Payload) == 0 {
				pendingFound = true
			}
		}
		if !meterFound || !pendingFound {
			t.Fatalf("builder omitted typed meter declaration or required pending record: %+v", handover)
		}
	})
}

func TestBuildDescriptiveReportSelectsNewestOriginVersionDeterministically(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, _ notificationrequest.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", false)
		now := time.Now().UTC()
		originID, oldVersionID, newestVersionID := identity.NewID(), identity.NewID(), identity.NewID()
		componentKey := "floor"
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			if err := tx.Create(&database.Origin{ID: originID, TenantID: f.TenantID, AssetID: f.AssetID, Version: 2, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			old := database.OriginVersion{ID: oldVersionID, TenantID: f.TenantID, OriginID: originID, VersionNumber: 1, Source: "CAPTURE", SourceInspectionID: &f.InspectionID, HasInventory: true, Status: "SUBMITTED", IdempotencyKey: "task04-origin-old-" + oldVersionID.String(), CreatedAt: now}
			newest := database.OriginVersion{ID: newestVersionID, TenantID: f.TenantID, OriginID: originID, VersionNumber: 2, Source: "CAPTURE", SourceInspectionID: &f.InspectionID, HasInventory: true, Status: "DRAFT", IdempotencyKey: "task04-origin-new-" + newestVersionID.String(), CreatedAt: now.Add(time.Minute)}
			if err := tx.Create(&old).Error; err != nil {
				return err
			}
			if err := tx.Create(&newest).Error; err != nil {
				return err
			}
			for _, candidate := range []struct {
				versionID identity.ID
				name      string
				condition string
			}{{oldVersionID, "Catálogo anterior", "FAIR"}, {newestVersionID, "Catálogo mais recente", "GOOD"}} {
				environmentID := identity.NewID()
				if err := tx.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: f.TenantID, OriginVersionID: candidate.versionID, IdentityID: f.EnvironmentIdentityID, TypeCode: "KITCHEN", Name: "Cozinha", Expected: true, Decision: "PENDING", Version: 1, CreatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.InventoryItem{ID: identity.NewID(), TenantID: f.TenantID, OriginVersionID: candidate.versionID, EnvironmentID: environmentID, IdentityID: identity.NewID(), TypeCode: "FLOOR", Name: "Piso", Description: candidate.name, ComponentKey: &componentKey, Condition: candidate.condition, Observation: "OBSERVED", Decision: "PENDING", CreatedAt: now}).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		evidence := []core.Evidence{{ID: f.MediaID.String(), RequirementKey: "env:new:" + f.EnvironmentIdentityID.String(), Role: "CURRENT", Availability: "AVAILABLE"}}
		var checklist *core.ChecklistSection
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			var err error
			checklist, _, _, _, _, err = buildDescriptiveReport(ctx, tx, f.TenantID, f.InspectionID, evidence, nil, "prompt", "COMPLETED")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if got := checklist.Rooms[0].Components[0].Description; got != "Catálogo mais recente" {
			t.Fatalf("report used nondeterministic origin version; selected description=%q", got)
		}
	})
}

func TestTask04CorruptPinnedScopeIsPermanent(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, _ notificationrequest.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", false)
		if err := h.AdminDB.Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Update("document", json.RawMessage(`{"rooms":"not-a-room-list"}`)).Error; err != nil {
			t.Fatal(err)
		}
		err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			_, _, _, _, _, err := buildDescriptiveReport(ctx, tx, f.TenantID, f.InspectionID, nil, nil, "prompt", "COMPLETED")
			return err
		})
		if !errors.Is(err, messaging.ErrPermanent) {
			t.Fatalf("corrupt pinned scope error=%v, want permanent failure", err)
		}
	})
}

func TestTask04CorruptComponentAndHandoverEvidenceReferencesArePermanent(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, _ notificationrequest.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", true)
		if err := h.AdminDB.Model(&database.ComponentAnswer{}).Where("tenant_id=? AND draft_id=?", f.TenantID, f.DraftID).Update("evidence_ids", json.RawMessage(`{"id":"not-a-list"}`)).Error; err != nil {
			t.Fatal(err)
		}
		if err := h.AdminDB.Create(&database.HandoverRecord{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, RecordKey: "broken-record", Kind: "KEY", Payload: json.RawMessage(`{"quantity":1}`), MediaIDs: json.RawMessage(`["not-a-uuid"]`), Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
		build := func() error {
			return h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
				_, _, _, _, _, err := buildDescriptiveReport(ctx, tx, f.TenantID, f.InspectionID, nil, nil, "prompt", "COMPLETED")
				return err
			})
		}
		err := build()
		if !errors.Is(err, messaging.ErrPermanent) {
			t.Fatalf("corrupt component evidence references error=%v, want permanent failure", err)
		}
		if err := h.AdminDB.Model(&database.ComponentAnswer{}).Where("tenant_id=? AND draft_id=?", f.TenantID, f.DraftID).Update("evidence_ids", json.RawMessage(`[]`)).Error; err != nil {
			t.Fatal(err)
		}
		if err := build(); !errors.Is(err, messaging.ErrPermanent) {
			t.Fatalf("corrupt handover evidence references error=%v, want permanent failure", err)
		}
	})
}
