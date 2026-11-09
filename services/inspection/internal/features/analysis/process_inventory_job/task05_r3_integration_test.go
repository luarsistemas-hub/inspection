//go:build integration

package process_inventory_job

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	revisepairing "inspection/services/inspection/internal/features/analysis/revise_comparison_pairing"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	"inspection/services/inspection/internal/features/origins/resolve_reference"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

// createdMoveOut is a move-out inspection created by the real
// inspections/core.Service.Create, pinned through the real
// origins/resolve_reference query to a baseline whose item photos are also
// registered as origin evidence (as a promoted baseline has them). The
// template's room keys are template keys, not baseline identities, exactly as
// a v3 template scope pins them.
type createdMoveOut struct {
	view inspectioncore.View
	// roomKey is the capture requirement key of each baseline room name.
	roomKey map[string]string
}

func (f *flow) createMoveOutFromBaseline(p pinned, rooms ...catalog.ExpectedRoom) createdMoveOut {
	f.t.Helper()
	return f.createMoveOut(p, catalog.DescriptiveSchemaVersion, rooms...)
}

// createMoveOut creates the move-out with a template of the given schema
// version (v2 templates have no rooms: their scope is "not defined").
func (f *flow) createMoveOut(p pinned, schemaVersion int, rooms ...catalog.ExpectedRoom) createdMoveOut {
	f.t.Helper()
	ctx := f.ctx
	now := f.now()
	// A promoted baseline registers its item photos as origin evidence too.
	f.within(func(tx *gorm.DB) error {
		var evidence []database.InventoryItemEvidence
		if err := tx.Where("tenant_id=? AND item_id IN (SELECT id FROM origins.inventory_items WHERE tenant_id=? AND origin_version_id=?)", f.seed.TenantID, f.seed.TenantID, p.version).Find(&evidence).Error; err != nil {
			return err
		}
		for _, row := range evidence {
			if err := tx.Create(&database.OriginEvidence{ID: identity.NewID(), TenantID: f.seed.TenantID, OriginVersionID: p.version, MediaID: row.MediaID, Category: "LIVING_ROOM", Description: "Foto da referência", AttentionItems: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	document := catalog.TemplateDocument{SchemaVersion: schemaVersion, AssetProfile: "PROPERTY_COMPONENTS", DefaultComparisonMode: catalog.FixedOrigin, AnalysisType: "REAL_ESTATE", ExpectedRooms: rooms}
	if schemaVersion < catalog.DescriptiveSchemaVersion {
		document.AssetProfile = ""
	}
	definition, err := json.Marshal(document)
	if err != nil {
		f.t.Fatal(err)
	}
	templateID, versionID := identity.NewID(), identity.NewID()
	f.within(func(tx *gorm.DB) error {
		if err := tx.Create(&database.Template{ID: templateID, TenantID: f.seed.TenantID, Key: "t05r3-" + templateID.String(), Name: "Vistoria de saída", SegmentVersionID: f.seed.SegmentVersionID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.TemplateVersion{ID: versionID, TenantID: f.seed.TenantID, TemplateID: templateID, VersionNumber: 1, SchemaVersion: schemaVersion, DefinitionJSON: definition, CanonicalDigest: strings.Repeat("0", 64), Status: "ACTIVE", IdempotencyKey: "t05r3-" + versionID.String(), PublishedAt: now, CreatedBy: f.seed.IdentityID}).Error
	})
	bus := mediator.New()
	seed := f.seed
	must := func(err error) {
		if err != nil {
			f.t.Fatal(err)
		}
	}
	must(bus.RegisterQuery(assetget.Query{}, func(_ context.Context, raw any) (any, error) {
		if raw.(assetget.Query).AssetID != seed.AssetID {
			return nil, apperror.New(apperror.NotFound, "", "resource not found")
		}
		return assetcore.View{Asset: database.Asset{ID: seed.AssetID, TenantID: seed.TenantID, BusinessUnitID: seed.BusinessUnitID, TemplateID: &templateID, Status: "ACTIVE", Version: 1}}, nil
	}))
	must(bus.RegisterQuery(participantget.Query{}, func(_ context.Context, raw any) (any, error) {
		return participantcore.ParticipantView{Participant: database.Participant{ID: seed.ParticipantID, TenantID: seed.TenantID, BusinessUnitID: seed.BusinessUnitID, Status: "ACTIVE", Version: 1}, Selected: []identity.ID{identity.NewID()}}, nil
	}))
	must(bus.RegisterQuery(templateresolve.Query{}, func(_ context.Context, raw any) (any, error) {
		return templateresolve.Result{Template: database.Template{ID: templateID, TenantID: seed.TenantID}, Version: database.TemplateVersion{ID: versionID, TenantID: seed.TenantID, TemplateID: templateID, SchemaVersion: schemaVersion, DefinitionJSON: definition}}, nil
	}))
	must(resolve_reference.Setup(resolve_reference.Dependencies{DB: f.h.DB, Bus: bus}))
	service := inspectioncore.Service{DB: f.h.DB, Bus: bus, Now: f.now}
	view, err := service.Create(ctx, inspectioncore.CreateInput{TenantID: seed.TenantID, AssetID: seed.AssetID, ParticipantID: seed.ParticipantID, TemplateID: &templateID, TemplateVersionID: &versionID, ComparisonMode: catalog.FixedOrigin, ReferenceVersionID: &p.version, Source: inspectioncore.SourceScheduled, SourceKey: "t05r3-" + identity.NewID().String(), DueAt: now.Add(time.Hour), DeadlineAt: now.Add(24 * time.Hour)})
	if err != nil {
		f.t.Fatalf("real move-out creation: %v", err)
	}
	var draft database.CaptureDraft
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=?", seed.TenantID, view.Responsibility.ID).Take(&draft).Error
	})
	var requirements []struct {
		Key                   string `json:"key"`
		EnvironmentIdentityID string `json:"environmentIdentityId"`
		EnvironmentName       string `json:"environmentName"`
	}
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		f.t.Fatal(err)
	}
	out := createdMoveOut{view: view, roomKey: map[string]string{}}
	for _, requirement := range requirements {
		out.roomKey[requirement.EnvironmentName] = requirement.Key
	}
	f.seed.InspectionID, f.seed.ResponsibilityID, f.seed.DraftID = view.Inspection.ID, view.Responsibility.ID, draft.ID
	if f.openCapture {
		// The capture stays OPEN: the test submits it with the real capture
		// service (submitCapture). The invitation was opened and the capture
		// started, as the invitation flow records it.
		f.within(func(tx *gorm.DB) error {
			return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", seed.TenantID, view.Inspection.ID).Update("status", "IN_PROGRESS").Error
		})
		return out
	}
	// The participant submitted the capture: the created inspection becomes
	// the flow's inspection (status changes only; every snapshot stays as
	// Create wrote it).
	f.within(func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", seed.TenantID, view.Inspection.ID).Update("status", "SUBMITTED").Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", seed.TenantID, view.Responsibility.ID).Update("status", "SUBMITTED").Error; err != nil {
			return err
		}
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", seed.TenantID, draft.ID).Update("status", "SUBMITTED").Error
	})
	f.seed.InspectionID, f.seed.ResponsibilityID, f.seed.DraftID = view.Inspection.ID, view.Responsibility.ID, draft.ID
	return out
}

// observeRoom answers a created room with one renderable photo and records
// its general view as observed with that photo.
func (f *flow) observeRoom(key string) identity.ID {
	f.t.Helper()
	photo := f.renderablePhoto(key, 140)
	f.answer(key, []identity.ID{photo}, "")
	ids, _ := json.Marshal([]identity.ID{photo})
	now := f.now()
	f.within(func(tx *gorm.DB) error {
		return tx.Create(&database.ComponentAnswer{ID: identity.NewID(), TenantID: f.seed.TenantID, DraftID: f.seed.DraftID, RequirementKey: key, ComponentKey: "geral", ViewKey: "wide", State: "OBSERVED", EvidenceIDs: ids, Version: 1, CreatedAt: now, UpdatedAt: now}).Error
	})
	return photo
}

// TestTask05R3RealMoveOutPublishesV3Comparison drives a move-out created by
// the real Create (real reference payload and template room keys) through
// the real job request, executor, processor, classification and
// classified-event handler: the first emission must publish a valid v3
// comparison whose pairs link the baseline photo to the current photo.
func TestTask05R3RealMoveOutPublishesV3Comparison(t *testing.T) {
	f := newFlow(t, "PHOTO")
	f.openCapture = true
	p := f.pinRenderable(strictLiving(inventorytest.BaselineItem{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD"}))
	created := f.createMoveOutFromBaseline(p, catalog.ExpectedRoom{Key: "sala", TypeCode: "LIVING_ROOM", Name: "Sala", Components: []catalog.ExpectedComponent{{Key: "geral", TypeCode: "OTHER", Label: "Visão geral", Views: []string{"wide"}}}})
	key := created.roomKey["Sala"]
	if key == "" {
		t.Fatalf("created capture has no Sala requirement: %+v", created.roomKey)
	}
	current := f.observeRoom(key)
	f.completeFindingsComparison()
	f.strictScript(firstSameView, func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Sofá"], "PRESENT_UNCHANGED", "conditionAfter", "GOOD"))
	})
	// The real capture submit, its finalizer and the capture.submitted.v1
	// event it recorded request the jobs; the processor's
	// comparison-completed event and the classifier's classified event are
	// delivered from the outbox to the real handlers.
	f.submitCapture(f.seed.ResponsibilityID)
	f.drain()
	row := f.reportThroughOutbox()
	snapshot := decodeSnapshot(t, row)
	if err := reportcore.Validate(snapshot); err != nil {
		t.Fatalf("issued v3 snapshot does not validate: %v", err)
	}
	item := comparisonItem(t, snapshot, "Sofá")
	if item.Outcome != reportcore.ComparisonUnchanged || len(item.Pairs) != 1 || item.Pairs[0].CurrentEvidenceID != current.String() || item.RoomKey != key {
		t.Fatalf("Sofá = %+v; want UNCHANGED paired with current photo %s in room %s", item, current, key)
	}
	// The real reference payload of an inventory baseline lists no media-only
	// origin items: the baseline photo enters the report as the pair's
	// REFERENCE evidence.
	var payload struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(created.view.Reference.Payload, &payload); err != nil || len(payload.Items) != 0 {
		t.Fatalf("reference payload = %s (%v)", created.view.Reference.Payload, err)
	}
	var publications int64
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=? AND status='PUBLISHED'", f.seed.TenantID, row.ID).Count(&publications).Error
	})
	if publications != 1 {
		t.Fatalf("v3 comparison report publications = %d; want the automatic publication", publications)
	}
	// Both PDFs, rendered by the real executor, never print a requirement
	// key or a UUID (item 1, round 4).
	for audience, artifact := range f.renderPDFs(row) {
		reader, err := f.h.Store.Client.Get(f.ctx, f.h.Store.Bucket, artifact.ObjectKey)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		text := pdfText(t, data)
		if !strings.Contains(text, "Sofá") {
			t.Fatalf("%s PDF text extraction found no item: %q", audience, text)
		}
		// The footer prints the public report number (Snapshot.ReportID),
		// the code readers use to verify the report; it is not an internal
		// key and is the only identifier allowed.
		text = strings.ReplaceAll(text, snapshot.ReportID, "<número do laudo>")
		if raw := rawIdentifiers.FindString(text); raw != "" {
			index := strings.Index(text, raw)
			t.Fatalf("%s PDF prints %q in %q", audience, raw, text[max(0, index-40):min(len(text), index+60)])
		}
	}

	// A staff pairing revision of the template-keyed room opens the next
	// generation of that room's job and issues a valid version 2.
	revise, err := revisepairing.Setup(revisepairing.Dependencies{DB: f.h.DB, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	baselinePhoto, err := identity.ParseID(item.Pairs[0].BaselineEvidenceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := revise(f.ctx, revisepairing.Command{TenantID: f.seed.TenantID, ActorID: f.seed.MembershipID, InspectionID: f.seed.InspectionID, ItemIdentityID: p.items["Sofá"].IdentityID, Pairs: []revisepairing.Pair{{BaselineEvidenceID: baselinePhoto, CurrentEvidenceID: current}}, ExpectedVersion: row.VersionNumber, ClientMutationID: "t05r3-revise"}); err != nil {
		t.Fatalf("revision of a template-keyed room: %v", err)
	}
	f.clock.Add(int64(time.Second))
	f.drain()
	_, v2 := f.classifyAndReportV3()
	revised := decodeSnapshot(t, v2)
	if err := reportcore.Validate(revised); err != nil || v2.VersionNumber != row.VersionNumber+1 {
		t.Fatalf("revised version %d: %v", v2.VersionNumber, err)
	}
	if again := comparisonItem(t, revised, "Sofá"); again.Outcome != reportcore.ComparisonUnchanged || !again.PairingRevised || again.RoomKey != key {
		t.Fatalf("revised Sofá = %+v; want UNCHANGED with the staff pair in room %s", again, key)
	}
}
