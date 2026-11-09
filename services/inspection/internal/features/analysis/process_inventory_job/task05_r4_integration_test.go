//go:build integration

package process_inventory_job

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	analysiscore "inspection/services/inspection/internal/features/analysis/core"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	"inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
	revisepairing "inspection/services/inspection/internal/features/analysis/revise_comparison_pairing"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	finalizesubmission "inspection/services/inspection/internal/features/capture/finalize_submission"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	handleclassified "inspection/services/inspection/internal/features/reports/handle_classified"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"gorm.io/gorm"
)

// rawIdentifiers are internal identifiers a reader must never see.
var rawIdentifiers = regexp.MustCompile(`(?i)env:|origin:|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

var (
	pdfTextBlocks = regexp.MustCompile(`(?s)BT(.*?)ET`)
	pdfShown      = regexp.MustCompile(`(?s)(\((?:\\.|[^\\)])*\)|<[0-9A-Fa-f\s]+>)\s*T(?:j|J)|\[([^\]]*)\]\s*TJ`)
	pdfArrayPart  = regexp.MustCompile(`\((?:\\.|[^\\)])*\)|<[0-9A-Fa-f\s]+>`)
)

// pdfText extracts the shown text of every page (the renderer writes UTF-16
// hex or literal strings).
func pdfText(t *testing.T, data []byte) string {
	t.Helper()
	model.ConfigPath = "disable"
	parsed, err := api.ReadAndValidate(bytes.NewReader(data), model.NewDefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	decode := func(operand string) string {
		var raw []byte
		switch {
		case strings.HasPrefix(operand, "<"):
			raw, _ = hex.DecodeString(strings.Join(strings.Fields(operand[1:len(operand)-1]), ""))
		case strings.HasPrefix(operand, "("):
			raw = unescapePDFLiteral(operand[1 : len(operand)-1])
		}
		if len(raw)%2 != 0 {
			return string(raw)
		}
		units := make([]uint16, 0, len(raw)/2)
		for i := 0; i+1 < len(raw); i += 2 {
			units = append(units, uint16(raw[i])<<8|uint16(raw[i+1]))
		}
		return string(utf16.Decode(units))
	}
	var all strings.Builder
	for page := 1; page <= parsed.PageCount; page++ {
		stream, err := pdfcpu.ExtractPageContent(parsed, page)
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(stream)
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range pdfTextBlocks.FindAllSubmatch(content, -1) {
			for _, shown := range pdfShown.FindAllSubmatch(block[1], -1) {
				if len(shown[1]) > 0 {
					all.WriteString(decode(string(shown[1])))
				} else {
					for _, part := range pdfArrayPart.FindAllString(string(shown[2]), -1) {
						all.WriteString(decode(part))
					}
				}
				all.WriteString(" ")
			}
		}
	}
	return all.String()
}

// unescapePDFLiteral resolves the escapes of a PDF literal string.
func unescapePDFLiteral(literal string) []byte {
	out := make([]byte, 0, len(literal))
	for i := 0; i < len(literal); i++ {
		if literal[i] != '\\' || i+1 == len(literal) {
			out = append(out, literal[i])
			continue
		}
		i++
		switch c := literal[i]; {
		case c == 'n':
			out = append(out, '\n')
		case c == 'r':
			out = append(out, '\r')
		case c == 't':
			out = append(out, '\t')
		case c == 'b':
			out = append(out, '\b')
		case c == 'f':
			out = append(out, '\f')
		case c == '\r' || c == '\n':
		case c >= '0' && c <= '7':
			value := 0
			for digits := 0; digits < 3 && i < len(literal) && literal[i] >= '0' && literal[i] <= '7'; digits++ {
				value = value*8 + int(literal[i]-'0')
				i++
			}
			i--
			out = append(out, byte(value))
		default:
			out = append(out, c)
		}
	}
	return out
}

// submitCapture submits the OPEN capture of one responsibility with the real
// capture service and its production finalizer, after the participant's
// processing consent, and delivers the capture.submitted.v1 event the outbox
// recorded to the real job request handler.
func (f *flow) submitCapture(responsibilityID identity.ID) database.SubmissionVersion {
	f.t.Helper()
	f.within(func(tx *gorm.DB) error {
		return tx.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: f.seed.TenantID, ResponsibilityID: responsibilityID, DisclosureVersion: "t05r4", PhotoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: f.now()}).Error
	})
	finalizer, err := finalizesubmission.Setup(finalizesubmission.Dependencies{Recapture: recapturecore.Service{DB: f.h.DB, Now: f.now}})
	if err != nil {
		f.t.Fatal(err)
	}
	submission, err := (capturecore.Service{DB: f.h.DB, Now: f.now, Finalizer: finalizer}).Submit(f.ctx, f.seed.TenantID, responsibilityID, true)
	if err != nil {
		f.t.Fatalf("real capture submit: %v", err)
	}
	handler, err := request_inventory_jobs.Setup(request_inventory_jobs.Dependencies{Now: f.now})
	if err != nil {
		f.t.Fatal(err)
	}
	f.deliverOutbox("capture.submitted.v1", func(envelope events.RawEnvelope) bool {
		return strings.Contains(string(envelope.Payload), submission.ID.String())
	}, handler)
	return submission
}

// deliverOutbox hands the newest outbox event of a type accepted by match to
// handler inside a tenant transaction, as the worker consumer does.
func (f *flow) deliverOutbox(eventType string, match func(events.RawEnvelope) bool, handler func(context.Context, *gorm.DB, events.RawEnvelope) error) {
	f.t.Helper()
	rows := f.outbox(eventType)
	for i := len(rows) - 1; i >= 0; i-- {
		var envelope events.RawEnvelope
		if err := json.Unmarshal(rows[i].Payload, &envelope); err != nil {
			f.t.Fatal(err)
		}
		if match != nil && !match(envelope) {
			continue
		}
		f.within(func(tx *gorm.DB) error { return handler(f.ctx, tx, envelope) })
		return
	}
	f.t.Fatalf("no %s event in the outbox", eventType)
}

// reportThroughOutbox delivers the comparison-completed event the processor
// recorded to the real classifier, and the classified event it recorded to
// the real classified-event handler; it returns the newest snapshot.
func (f *flow) reportThroughOutbox() database.ReportSnapshot {
	f.t.Helper()
	f.clock.Add(int64(time.Second))
	inspection := f.seed.InspectionID.String()
	forInspection := func(envelope events.RawEnvelope) bool {
		return envelope.AggregateID == f.seed.InspectionID || strings.Contains(string(envelope.Payload), inspection)
	}
	classify, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: f.now})
	if err != nil {
		f.t.Fatal(err)
	}
	f.deliverOutbox("analysis.comparison_completed.v1", forInspection, classify)
	handler, err := handleclassified.Setup(handleclassified.Dependencies{Notifications: silentNotifications{}, CreateSnapshotInTx: func(ctx context.Context, tx *gorm.DB, input generatesnapshot.Input) (generatesnapshot.Result, error) {
		return generatesnapshot.CreateInTx(ctx, tx, input, f.now())
	}, Now: f.now})
	if err != nil {
		f.t.Fatal(err)
	}
	f.deliverOutbox("inspection.classified.v1", forInspection, handler)
	var snapshot database.ReportSnapshot
	if err := f.h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Order("version_number DESC").First(&snapshot).Error; err != nil {
		f.t.Fatal(err)
	}
	return snapshot
}

// answerIn answers a room of one draft with media.
func (f *flow) answerIn(draftID identity.ID, key string, media ...identity.ID) {
	f.t.Helper()
	ids, _ := json.Marshal(media)
	now := f.now()
	f.within(func(tx *gorm.DB) error {
		return tx.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: f.seed.TenantID, DraftID: draftID, RequirementKey: key, MediaIDs: ids, Flags: json.RawMessage(`[]`), Version: 1, CreatedAt: now, UpdatedAt: now}).Error
	})
}

func (f *flow) compareJobs() map[identity.ID]database.InventoryJob {
	f.t.Helper()
	out := map[identity.ID]database.InventoryJob{}
	for _, job := range f.jobs() {
		if job.Kind == "COMPARE" && job.EnvironmentIdentityID != nil {
			if previous, ok := out[*job.EnvironmentIdentityID]; !ok || job.Generation > previous.Generation {
				out[*job.EnvironmentIdentityID] = job
			}
		}
	}
	return out
}

var salaRoom = catalog.ExpectedRoom{Key: "sala", TypeCode: "LIVING_ROOM", Name: "Sala", Components: []catalog.ExpectedComponent{{Key: "geral", TypeCode: "OTHER", Label: "Visão geral", Views: []string{"wide"}}}}

// Item 2 (round 4): with a move-out created by the real Create, two baseline
// rooms named alike stay unmatched instead of both reading one scope room, a
// renamed baseline room is matched by the scope key it was captured with, a
// baseline room without scope room and a scope room without baseline room
// never make a job read an answerless requirement: unmatched items carry
// "ambiente não correspondido", never "não capturado".
func TestTask05R4BaselineRoomsMatchScopeRoomsOneToOne(t *testing.T) {
	f := newFlow(t, "PHOTO")
	f.openCapture = true
	p := f.pin(
		inventorytest.BaselineEnvironment{Name: "Quarto", TypeCode: "BEDROOM", Items: []inventorytest.BaselineItem{{Name: "Cama A", TypeCode: "BED", Condition: "GOOD", Evidence: 1}}},
		inventorytest.BaselineEnvironment{Name: "Quarto", TypeCode: "BEDROOM", Items: []inventorytest.BaselineItem{{Name: "Cama B", TypeCode: "BED", Condition: "GOOD", Evidence: 1}}},
		inventorytest.BaselineEnvironment{Name: "Sala de estar", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}}},
		inventorytest.BaselineEnvironment{Name: "Varanda", TypeCode: "BALCONY", Items: []inventorytest.BaselineItem{{Name: "Vaso", TypeCode: "OTHER", Condition: "GOOD", Evidence: 1}}},
	)
	quartoA, quartoB, salaDeEstar, varanda := p.environments[0], p.environments[1], p.environments[2], p.environments[3]
	// The baseline living room was cataloged from a capture of scope room
	// "sala" (createCatalogJobs records the key it was captured with).
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.InventoryEnvironment{}).Where("tenant_id=? AND id=?", f.seed.TenantID, salaDeEstar.ID).Update("requirement_key", "env:sala").Error
	})
	bedroom := func(key string) catalog.ExpectedRoom {
		return catalog.ExpectedRoom{Key: key, TypeCode: "BEDROOM", Name: "Quarto", Components: []catalog.ExpectedComponent{{Key: "geral", TypeCode: "OTHER", Label: "Visão geral", Views: []string{"wide"}}}}
	}
	kitchen := catalog.ExpectedRoom{Key: "cozinha", TypeCode: "KITCHEN", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "geral", TypeCode: "OTHER", Label: "Visão geral", Views: []string{"wide"}}}}
	f.createMoveOutFromBaseline(p, bedroom("quarto1"), bedroom("quarto2"), salaRoom, kitchen)
	var draft database.CaptureDraft
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", f.seed.TenantID, f.seed.DraftID).Take(&draft).Error
	})
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		t.Fatal(err)
	}
	linked := map[string]string{}
	for _, requirement := range requirements {
		linked[requirement.Key] = requirement.EnvironmentIdentityID
	}
	want := map[string]string{"env:quarto1": "quarto1", "env:quarto2": "quarto2", "env:sala": salaDeEstar.IdentityID.String(), "env:cozinha": "cozinha"}
	for key, identityID := range want {
		if linked[key] != identityID {
			t.Fatalf("created requirements %v; want %v", linked, want)
		}
	}
	f.observeRoom("env:sala")
	f.completeFindingsComparison()
	f.strictScript(firstSameView, func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Sofá"], "PRESENT_UNCHANGED", "conditionAfter", "GOOD"))
	})
	f.submitCapture(f.seed.ResponsibilityID)
	f.drain()
	jobs := f.compareJobs()
	for env, key := range map[database.InventoryEnvironment]string{quartoA: "env:" + quartoA.IdentityID.String(), quartoB: "env:" + quartoB.IdentityID.String(), salaDeEstar: "env:sala", varanda: "env:" + varanda.IdentityID.String()} {
		if job := jobs[env.IdentityID]; job.RequirementKey != key || job.Status != "SUCCEEDED" {
			t.Fatalf("%s job = %+v; want key %s", env.Name, job, key)
		}
	}
	results := f.results()
	for _, name := range []string{"Cama A", "Cama B", "Vaso"} {
		if row := results[p.items[name].IdentityID.String()]; row.Status != "NOT_OBSERVED" || value(row.ReasonCode) != reasonEnvironmentUnmatched {
			t.Fatalf("%s = %+v; want NOT_OBSERVED %s", name, row, reasonEnvironmentUnmatched)
		}
	}
	if row := results[p.items["Sofá"].IdentityID.String()]; row.Status != "PRESENT_UNCHANGED" {
		t.Fatalf("Sofá = %+v; the renamed room was matched by its captured key", row)
	}
	_, row := f.classifyAndReportV3()
	snapshot := decodeSnapshot(t, row)
	if err := reportcore.Validate(snapshot); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Cama A", "Cama B", "Vaso"} {
		item := comparisonItem(t, snapshot, name)
		if item.Outcome != reportcore.ComparisonNotObserved || len(item.Limitations) != 1 || item.Limitations[0] != reportcore.LimitationEnvironmentUnmatched {
			t.Fatalf("%s = %+v; want the unmatched-room limitation", name, item)
		}
	}
	if item := comparisonItem(t, snapshot, "Sofá"); item.Outcome != reportcore.ComparisonUnchanged {
		t.Fatalf("Sofá = %+v", item)
	}
	if strings.Contains(string(row.CanonicalJSON), reportcore.LimitationEnvironmentMissing) {
		t.Fatal("an unmatched baseline room was reported as not captured")
	}
}

// Item 3 (round 4), decision: a new move-out of a v2 template has a scope
// without rooms ("escopo não definido"); the comparison is strict, and
// without room coverage the absence of a movable item is never proven. The
// item stays NOT_OBSERVED with ROOM_COVERAGE_INSUFFICIENT and the report says
// why in Portuguese.
func TestTask05R4NewV2MoveOutKeepsMovableAbsenceUnconfirmed(t *testing.T) {
	f := newFlow(t, "PHOTO")
	f.openCapture = true
	p := f.pin(strictLiving(inventorytest.BaselineItem{Name: "Poltrona", TypeCode: "FURNITURE_OTHER", Condition: "GOOD", Evidence: 1}))
	created := f.createMoveOut(p, catalog.EnvironmentSchemaVersion)
	if created.view.Scope.Profile != "UNDEFINED" {
		t.Fatalf("v2 scope profile = %q; want UNDEFINED", created.view.Scope.Profile)
	}
	key := created.roomKey["Sala"]
	if key != "env:"+p.environments[0].IdentityID.String() {
		t.Fatalf("v2 requirement key = %q", key)
	}
	photo := f.photo(key)
	f.answerIn(f.seed.DraftID, key, photo)
	f.completeFindingsComparison()
	f.strictScript(firstSameView, func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Poltrona"], "MISSING"))
	})
	f.submitCapture(f.seed.ResponsibilityID)
	f.drain()
	if row := f.results()[p.items["Poltrona"].IdentityID.String()]; row.Status != "NOT_OBSERVED" || value(row.ReasonCode) != reasonRoomCoverage {
		t.Fatalf("Poltrona = %+v; want NOT_OBSERVED %s", row, reasonRoomCoverage)
	}
	run, row := f.classifyAndReportV3Any()
	var snapshot reportcore.Snapshot
	if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	var reason string
	if snapshot.Comparison != nil {
		reason = strings.Join(comparisonItem(t, snapshot, "Poltrona").Limitations, " ")
	} else if snapshot.Inventory != nil {
		for _, entry := range snapshot.Inventory.Problems {
			if entry.Name == "Poltrona" {
				reason = entry.Reason
			}
		}
	}
	if run.Classification != analysiscore.ClassificationAttention {
		t.Fatalf("v2 move-out classification = %q; want %q (Requer atenção)", run.Classification, analysiscore.ClassificationAttention)
	}
	if reason != reportcore.LimitationRoomCoverageUndefinedScope || !strings.Contains(reason, "escopo não definido") {
		t.Fatalf("Poltrona limitation = %q; want %q", reason, reportcore.LimitationRoomCoverageUndefinedScope)
	}
	if snapshot.Inventory == nil || len(snapshot.Inventory.Problems) != 1 || snapshot.Inventory.Problems[0].Status != "NOT_OBSERVED" || snapshot.Inventory.Problems[0].Reason != reportcore.LimitationRoomCoverageUndefinedScope {
		t.Fatalf("v3 inventory report = %+v; want NOT_OBSERVED with the undefined-scope coverage limitation", snapshot.Inventory)
	}
	html, err := reportcore.HTML(snapshot)
	if err != nil {
		t.Fatalf("render v2 move-out inventory report: %v", err)
	}
	if !strings.Contains(string(html), "Requer atenção") || strings.Contains(string(html), "Crítica") || !strings.Contains(string(html), reportcore.LimitationRoomCoverageUndefinedScope) {
		t.Fatalf("v2 move-out report classification/coverage = %s; want Requer atenção and the undefined-scope limitation", html)
	}
}

// Round 5: v2 move-outs have no pinned scope rooms, but their participant
// environment requirement still distinguishes an unrecorded environment from
// a baseline environment with no scope match.
func TestTask05R5NewV2MissingEnvironmentIsNotUnmatched(t *testing.T) {
	for _, test := range []struct {
		name       string
		impossible bool
	}{
		{name: "declared impossible", impossible: true},
		{name: "submitted without an answer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFlow(t, "PHOTO")
			f.openCapture = true
			p := f.pin(strictLiving(inventorytest.BaselineItem{Name: "Poltrona", TypeCode: "FURNITURE_OTHER", Condition: "GOOD", Evidence: 1}))
			created := f.createMoveOut(p, catalog.EnvironmentSchemaVersion)
			key := created.roomKey["Sala"]
			if test.impossible {
				capture := capturecore.Service{DB: f.h.DB, Now: f.now}
				if err := capture.DeclareImpossibilityWithCode(f.ctx, f.seed.TenantID, f.seed.ResponsibilityID, key, "LOCKED", "Acesso bloqueado.", 0); err != nil {
					t.Fatalf("declare environment impossibility: %v", err)
				}
			} else {
				// An allowed gap lets the participant submit this v2 draft with
				// the requirement present and unanswered, through the real Submit.
				var draft database.CaptureDraft
				f.within(func(tx *gorm.DB) error {
					if err := tx.Where("tenant_id=? AND responsibility_id=?", f.seed.TenantID, f.seed.ResponsibilityID).Take(&draft).Error; err != nil {
						return err
					}
					var requirements []capturecore.Requirement
					if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
						return err
					}
					for i := range requirements {
						if requirements[i].Key == key {
							requirements[i].AllowConfirmedGap = true
						}
					}
					draft.Requirements, _ = json.Marshal(requirements)
					return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", f.seed.TenantID, draft.ID).Update("requirements", draft.Requirements).Error
				})
			}
			f.completeFindingsComparison()
			f.submitCapture(f.seed.ResponsibilityID)
			f.drain()
			row := f.results()[p.items["Poltrona"].IdentityID.String()]
			if row.Status != "NOT_OBSERVED" || value(row.ReasonCode) != "ENVIRONMENT_NOT_CAPTURED" {
				t.Fatalf("Poltrona = %+v; want NOT_OBSERVED ENVIRONMENT_NOT_CAPTURED", row)
			}
			run, report := f.classifyAndReportV3Any()
			var snapshot reportcore.Snapshot
			if err := json.Unmarshal(report.CanonicalJSON, &snapshot); err != nil {
				t.Fatal(err)
			}
			if run.Classification != analysiscore.ClassificationAttention {
				t.Fatalf("classification = %q; want %q", run.Classification, analysiscore.ClassificationAttention)
			}
			var itemReason string
			for _, entry := range snapshot.Inventory.Problems {
				if entry.Name == "Poltrona" {
					itemReason = entry.Reason
					if entry.Status != "NOT_OBSERVED" {
						t.Fatalf("inventory item status = %q; want NOT_OBSERVED", entry.Status)
					}
				}
			}
			if itemReason != "O ambiente não foi capturado nesta vistoria." {
				t.Fatalf("inventory item limitation = %q; want the Portuguese not-captured explanation; inventory=%+v", itemReason, snapshot.Inventory)
			}
		})
	}
}

// classifyAndReportV3Any is classifyAndReportV3 without requiring a
// comparison section.
func (f *flow) classifyAndReportV3Any() (database.ClassificationRun, database.ReportSnapshot) {
	f.t.Helper()
	f.clock.Add(int64(time.Second))
	classify, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: f.now})
	if err != nil {
		f.t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"inspectionId": f.seed.InspectionID})
	f.within(func(tx *gorm.DB) error {
		return classify(f.ctx, tx, events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", TenantID: f.seed.TenantID, CorrelationID: f.seed.InspectionID.String(), Payload: payload})
	})
	var run database.ClassificationRun
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Order("created_at DESC").First(&run).Error
	})
	handler, err := handleclassified.Setup(handleclassified.Dependencies{Notifications: silentNotifications{}, CreateSnapshotInTx: func(ctx context.Context, tx *gorm.DB, input generatesnapshot.Input) (generatesnapshot.Result, error) {
		return generatesnapshot.CreateInTx(ctx, tx, input, f.now())
	}, Now: f.now})
	if err != nil {
		f.t.Fatal(err)
	}
	raw, _ := json.Marshal(events.InspectionClassifiedPayload{InspectionID: f.seed.InspectionID, ClassificationRunID: run.ID})
	f.within(func(tx *gorm.DB) error {
		return handler(f.ctx, tx, events.RawEnvelope{ID: identity.NewID(), Type: "inspection.classified.v1", SchemaVersion: 1, OccurredAt: f.now(), TenantID: f.seed.TenantID, AggregateID: f.seed.InspectionID, CorrelationID: f.seed.InspectionID.String(), Payload: raw})
	})
	var snapshot database.ReportSnapshot
	if err := f.h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Order("version_number DESC").First(&snapshot).Error; err != nil {
		f.t.Fatal(err)
	}
	return run, snapshot
}

// Item 5 (round 4): a recapture of the room submitted after generation N-1
// changes what the room shows. A later pairing revision of another item must
// judge the whole room again with the new submission instead of carrying
// N-1's "not observed" result.
func TestTask05R4RevisionAfterRecaptureJudgesTheWholeRoomAgain(t *testing.T) {
	f := newFlow(t, "PHOTO")
	f.openCapture = true
	p := f.pinRenderable(inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD"}, {Name: "Mesa", TypeCode: "TABLE", Condition: "GOOD"}}})
	created := f.createMoveOutFromBaseline(p, salaRoom)
	key := created.roomKey["Sala"]
	first := f.observeRoom(key)
	f.completeFindingsComparison()
	// In generation 1 no current view is equivalent to Sofá's baseline view
	// (no confirmed pair): Sofá is "not observed" without any pair, so no
	// replaced-photo rule can reopen it.
	var sofaEvidence database.InventoryItemEvidence
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND item_id=?", f.seed.TenantID, p.items["Sofá"].ID).Take(&sofaEvidence).Error
	})
	f.strictScript(func(in matchRequest) (string, string) {
		if in.OriginEvidenceID == sofaEvidence.MediaID.String() {
			return in.CurrentEvidenceIDs[0], "NOT_COMPARABLE"
		}
		return in.CurrentEvidenceIDs[0], "SAME_VIEW"
	}, func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Sofá"], "NOT_OBSERVED", "reasonCode", "OBSTRUCTED"), claim(p.items["Mesa"], "PRESENT_UNCHANGED", "conditionAfter", "GOOD"))
	})
	f.submitCapture(f.seed.ResponsibilityID)
	f.drain()
	if row := f.results()[p.items["Sofá"].IdentityID.String()]; row.Status != "NOT_OBSERVED" || len(f.confirmedPairs(p.items["Sofá"])) != 0 {
		t.Fatalf("generation 1 Sofá = %+v; want NOT_OBSERVED without a confirmed pair", row)
	}
	_, v1 := f.classifyAndReportV3()
	if job := f.compareJobs()[p.environments[0].IdentityID]; job.CaptureAnswerRef == nil || *job.CaptureAnswerRef == "none" {
		t.Fatalf("generation 1 did not record the answer it read: %+v", job)
	}
	// The room is recaptured and the recapture submitted (real recapture
	// request, capture submit and finalizer).
	f.clock.Add(int64(time.Minute))
	request, err := (recapturecore.Service{DB: f.h.DB, Now: f.now}).Request(f.ctx, recapturecore.RequestInput{TenantID: f.seed.TenantID, InspectionID: f.seed.InspectionID, Items: []recapturecore.Item{{RequirementKey: key, OriginalMediaID: &first, Reason: "Sofá encoberto"}}, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "qa@example.test"}}, DeadlineAt: f.now().Add(48 * time.Hour), IdempotencyKey: "t05r4-recapture-" + f.seed.InspectionID.String()})
	if err != nil {
		t.Fatalf("recapture request: %v", err)
	}
	var recaptureDraft database.CaptureDraft
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=?", f.seed.TenantID, request.ResponsibilityID).Take(&recaptureDraft).Error
	})
	original := f.seed.ResponsibilityID
	f.seed.ResponsibilityID = request.ResponsibilityID
	second := f.renderablePhoto(key, 60)
	f.seed.ResponsibilityID = original
	f.answerIn(recaptureDraft.ID, key, second)
	f.clock.Add(int64(time.Second))
	f.submitCapture(request.ResponsibilityID)
	f.drain()
	// Staff revises the pairing of Mesa with the recaptured view.
	revise, err := revisepairing.Setup(revisepairing.Dependencies{DB: f.h.DB, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	var mesaEvidence database.InventoryItemEvidence
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND item_id=?", f.seed.TenantID, p.items["Mesa"].ID).Take(&mesaEvidence).Error
	})
	if _, err := revise(f.ctx, revisepairing.Command{TenantID: f.seed.TenantID, ActorID: f.seed.MembershipID, InspectionID: f.seed.InspectionID, ItemIdentityID: p.items["Mesa"].IdentityID, Pairs: []revisepairing.Pair{{BaselineEvidenceID: mesaEvidence.MediaID, CurrentEvidenceID: second}}, ExpectedVersion: v1.VersionNumber, ClientMutationID: "t05r4-revise"}); err != nil {
		t.Fatalf("revision: %v", err)
	}
	var judged []string
	f.strictScript(firstSameView, func(request llm.StructuredRequest) string {
		judged = append(judged, request.UserPrompt)
		return compareJSON(t, claim(p.items["Sofá"], "PRESENT_UNCHANGED", "conditionAfter", "GOOD"), claim(p.items["Mesa"], "PRESENT_UNCHANGED", "conditionAfter", "GOOD"))
	})
	f.clock.Add(int64(time.Second))
	f.drain()
	job := f.compareJobs()[p.environments[0].IdentityID]
	if job.Generation != 2 || job.Status != "SUCCEEDED" {
		t.Fatalf("revision job = %+v", job)
	}
	if len(judged) != 1 || !strings.Contains(judged[0], p.items["Sofá"].IdentityID.String()) {
		t.Fatal("the revision did not judge Sofá again after the recapture")
	}
	var rows []database.InventoryItemResult
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND job_id=?", f.seed.TenantID, job.ID).Find(&rows).Error
	})
	for _, row := range rows {
		if row.ItemIdentityID != nil && *row.ItemIdentityID == p.items["Sofá"].IdentityID && row.Status != "PRESENT_UNCHANGED" {
			t.Fatalf("generation 2 Sofá = %+v; the N-1 result of an older submission was carried", row)
		}
	}
	if job.CaptureAnswerRef == nil || *job.CaptureAnswerRef == "none" {
		t.Fatalf("generation 2 answer ref = %v", job.CaptureAnswerRef)
	}
}

// Item 7 (round 4): a strict comparison job that exhausts its attempts keeps
// "no basis" for items the baseline does not show; only items with basis are
// "analysis failed".
func TestTask05R4ExhaustedStrictJobKeepsNoBasis(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pin(strictLiving(inventorytest.BaselineItem{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}, inventorytest.BaselineItem{Name: "Quadro", TypeCode: "OTHER", Condition: "GOOD"}))
	key := inventorytest.EnvironmentKey(p.environments[0])
	f.strictScope(p, map[string]bool{"Sala": true})
	f.answer(key, []identity.ID{f.photo(key)}, "")
	f.gateway.Respond = func(llm.StructuredRequest) (string, error) { return "", errors.New("provider unavailable") }
	f.submitPhotos()
	f.drain()
	job := f.compareJobs()[p.environments[0].IdentityID]
	if job.Status != "FAILED" {
		t.Fatalf("job = %+v; want FAILED", job)
	}
	results := f.results()
	if row := results[p.items["Sofá"].IdentityID.String()]; value(row.ReasonCode) != "ANALYSIS_FAILED" {
		t.Fatalf("Sofá = %+v; want ANALYSIS_FAILED", row)
	}
	if row := results[p.items["Quadro"].IdentityID.String()]; value(row.ReasonCode) != reasonNoBaselineView {
		t.Fatalf("Quadro = %+v; want %s", row, reasonNoBaselineView)
	}
}

// Item 7 (round 4): the pair of an item left "not observed" is not shown, so
// its baseline photo is not left orphaned in the report's evidence.
func TestTask05R4NotObservedPairLeavesNoOrphanEvidence(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pinRenderable(inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD"}, {Name: "Mesa", TypeCode: "TABLE", Condition: "GOOD"}}})
	key := inventorytest.EnvironmentKey(p.environments[0])
	f.strictScope(p, map[string]bool{"Sala": true})
	f.answer(key, []identity.ID{f.renderablePhoto(key, 90)}, "")
	f.completeFindingsComparison()
	f.strictScript(firstSameView, func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Sofá"], "NOT_OBSERVED", "reasonCode", "OBSTRUCTED"), claim(p.items["Mesa"], "PRESENT_UNCHANGED", "conditionAfter", "GOOD"))
	})
	f.submitPhotos()
	f.drain()
	pairs := f.confirmedPairs(p.items["Sofá"])
	if len(pairs) != 1 {
		t.Fatalf("Sofá pairs = %+v; the fixture needs a confirmed pair", pairs)
	}
	_, row := f.classifyAndReportV3()
	snapshot := decodeSnapshot(t, row)
	if err := reportcore.Validate(snapshot); err != nil {
		t.Fatal(err)
	}
	cited := map[string]bool{}
	for _, item := range snapshot.Comparison.Items {
		for _, pair := range item.Pairs {
			cited[pair.BaselineEvidenceID], cited[pair.CurrentEvidenceID] = true, true
		}
	}
	for _, evidence := range snapshot.Evidence {
		if evidence.Role == "REFERENCE" && !cited[evidence.ID] {
			t.Fatalf("orphan reference evidence %+v in the report", evidence)
		}
		if evidence.ID == pairs[0].OriginMediaID.String() {
			t.Fatalf("the baseline photo of a not-observed pair stayed in the report: %+v", evidence)
		}
	}
	if snapshot.Provenance != nil {
		for _, id := range snapshot.Provenance.EvidenceIDs {
			if id == pairs[0].OriginMediaID.String() {
				t.Fatal("provenance cites a removed evidence")
			}
		}
	}
}
