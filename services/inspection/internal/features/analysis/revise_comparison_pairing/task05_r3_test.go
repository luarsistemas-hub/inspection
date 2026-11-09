package revise_comparison_pairing

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	processinventoryjob "inspection/services/inspection/internal/features/analysis/process_inventory_job"
	requestinventoryjobs "inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

// chain is a living room with two judged items (Sofá not observed, Mesa
// unchanged with the matcher's pair on current[0]) whose generations are
// processed by the real processor, one after another.
type chain struct {
	*fixture
	t          *testing.T
	sofa, mesa database.InventoryItem
	mesaOrigin identity.ID
	gateway    *inventorytest.Gateway
	processor  *processinventoryjob.Processor
	version    int
	clock      time.Time
}

func newChain(t *testing.T) *chain {
	t.Helper()
	f := newFixture(t)
	c := &chain{fixture: f, t: t, sofa: f.item("Sofá"), version: 1, clock: f.seed.Now}
	var living database.InventoryEnvironment
	if err := f.seed.DB.Where("id=?", c.sofa.EnvironmentID).Take(&living).Error; err != nil {
		t.Fatal(err)
	}
	c.mesa = database.InventoryItem{ID: identity.NewID(), TenantID: f.seed.Tenant, OriginVersionID: f.baseline.Version, EnvironmentID: living.ID, IdentityID: identity.NewID(), TypeCode: "TABLE", Name: "Mesa", NotAssessed: []byte(`[]`), Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "ACCEPTED", Version: 1, CreatedAt: f.seed.Now, UpdatedAt: f.seed.Now}
	c.mesaOrigin = f.seed.Photo(t, "origin")
	good := "GOOD"
	rows := []any{
		&c.mesa,
		&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: f.seed.Tenant, ItemID: c.mesa.ID, Kind: "PHOTO", MediaID: c.mesaOrigin, CreatedAt: f.seed.Now},
		&database.InventoryItemResult{ID: identity.NewID(), TenantID: f.seed.Tenant, JobID: f.job.ID, InspectionID: f.seed.Inspection, ItemIdentityID: &c.sofa.IdentityID, Status: "NOT_OBSERVED", ConditionBefore: &good, ReasonCode: strPtr("NO_CORRESPONDING_VIEW"), CreatedAt: f.seed.Now},
		&database.InventoryItemResult{ID: identity.NewID(), TenantID: f.seed.Tenant, JobID: f.job.ID, InspectionID: f.seed.Inspection, ItemIdentityID: &c.mesa.IdentityID, Status: "PRESENT_UNCHANGED", ConditionBefore: &good, ConditionAfter: &good, CreatedAt: f.seed.Now},
		&database.InventoryEvidencePair{ID: identity.NewID(), TenantID: f.seed.Tenant, JobID: f.job.ID, ItemIdentityID: c.mesa.IdentityID, OriginKind: "PHOTO", OriginMediaID: c.mesaOrigin, CurrentKind: "PHOTO", CurrentMediaID: f.current[0], Decision: "SAME_VIEW", Confirmed: true, Source: "MATCHER", CreatedAt: f.seed.Now},
	}
	for _, row := range rows {
		if err := f.seed.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	// The model confirms whatever it is asked to judge; the matcher keeps the
	// first current photo.
	c.gateway = &inventorytest.Gateway{}
	c.gateway.Respond = func(request llm.StructuredRequest) (string, error) {
		if request.Mode == "INVENTORY_MATCH" {
			var in struct {
				Stage              string           `json:"stage"`
				OriginEvidenceID   string           `json:"originEvidenceId"`
				CurrentEvidenceIDs []string         `json:"currentEvidenceIds"`
				ProposedPairs      []map[string]any `json:"proposedPairs"`
			}
			if err := json.Unmarshal([]byte(request.UserPrompt), &in); err != nil {
				return "", err
			}
			if in.Stage == "verify" {
				raw, _ := json.Marshal(map[string]any{"pairs": in.ProposedPairs})
				return string(raw), nil
			}
			raw, _ := json.Marshal(map[string]any{"pairs": []map[string]any{{"originEvidenceId": in.OriginEvidenceID, "currentEvidenceId": in.CurrentEvidenceIDs[0], "decision": "SAME_VIEW", "reason": "vista"}}})
			return string(raw), nil
		}
		items := []map[string]any{}
		for _, item := range []database.InventoryItem{c.sofa, c.mesa} {
			if strings.Contains(request.UserPrompt, item.IdentityID.String()) {
				items = append(items, map[string]any{"itemIdentityId": item.IdentityID.String(), "status": "PRESENT_UNCHANGED", "conditionAfter": "GOOD", "reasonCode": nil, "description": nil, "outsideTypeCode": nil, "outsideName": nil})
			}
		}
		raw, _ := json.Marshal(map[string]any{"items": items})
		return string(raw), nil
	}
	processor, err := processinventoryjob.Setup(processinventoryjob.Dependencies{DB: f.seed.DB, Store: f.seed.Store.Store(), Gateway: c.gateway, Now: func() time.Time { return c.clock }, Within: f.seed.Within})
	if err != nil {
		t.Fatal(err)
	}
	c.processor = processor
	return c
}

// issue closes the pending reissue request and records the next report
// version, as the classified-event handler does.
func (c *chain) issue() {
	c.t.Helper()
	c.version++
	db := c.seed.DB
	if err := db.Model(&database.AuditEvent{}).Where("action='report.reissue_requested'").Update("outcome", "DONE").Error; err != nil {
		c.t.Fatal(err)
	}
	if err := db.Create(&database.ReportSnapshot{ID: identity.NewID(), TenantID: c.seed.Tenant, InspectionID: c.seed.Inspection, Mode: "HISTORICAL", Classification: "NORMAL", JSONDigest: "d", HTMLDigest: "h", SchemaVersion: 3, VersionNumber: c.version, PDFRenderVersion: 1, CanonicalJSON: json.RawMessage(`{}`), HTML: json.RawMessage(`""`), CreatedAt: c.clock}).Error; err != nil {
		c.t.Fatal(err)
	}
}

func (c *chain) generation(n int) database.InventoryJob {
	c.t.Helper()
	var job database.InventoryJob
	if err := c.seed.DB.Where("requirement_key=? AND generation=?", c.job.RequirementKey, n).Take(&job).Error; err != nil {
		c.t.Fatalf("generation %d: %v", n, err)
	}
	return job
}

// revise records a staff pairing of item and returns the opened generation.
func (c *chain) revise(item database.InventoryItem, origin, current identity.ID) database.InventoryJob {
	c.t.Helper()
	command := c.command("Sofá", Pair{BaselineEvidenceID: origin, CurrentEvidenceID: current})
	command.ItemIdentityID, command.ExpectedVersion = item.IdentityID, c.version
	if _, err := c.fixture.revise(context.Background(), command); err != nil {
		c.t.Fatal(err)
	}
	return c.latest()
}

func (c *chain) latest() database.InventoryJob {
	c.t.Helper()
	var job database.InventoryJob
	if err := c.seed.DB.Where("requirement_key=?", c.job.RequirementKey).Order("generation DESC").Take(&job).Error; err != nil {
		c.t.Fatal(err)
	}
	return job
}

// succeed processes a generation and marks it SUCCEEDED.
func (c *chain) succeed(job database.InventoryJob) {
	c.t.Helper()
	c.clock = c.clock.Add(time.Minute)
	if err := c.processor.Process(context.Background(), job, func(write func(*gorm.DB) error) error { return c.seed.DB.Transaction(write) }); err != nil {
		c.t.Fatal(err)
	}
	if err := c.seed.DB.Model(&database.InventoryJob{}).Where("id=?", job.ID).Update("status", "SUCCEEDED").Error; err != nil {
		c.t.Fatal(err)
	}
	c.issue()
}

// fail exhausts a generation's attempts.
func (c *chain) fail(job database.InventoryJob) {
	c.t.Helper()
	c.clock = c.clock.Add(time.Minute)
	var answer database.RequirementAnswer
	answerRef := "none"
	if err := capturecore.LatestSubmittedAnswer(context.Background(), c.seed.DB, c.seed.Tenant, c.seed.Inspection, job.RequirementKey, &answer); err == nil {
		answerRef = fmt.Sprintf("%s:%d", answer.ID, answer.Version)
	} else if err != gorm.ErrRecordNotFound {
		c.t.Fatal(err)
	}
	if err := c.seed.DB.Model(&database.InventoryJob{}).Where("id=?", job.ID).Update("capture_answer_ref", answerRef).Error; err != nil {
		c.t.Fatal(err)
	}
	if err := c.seed.DB.Model(&database.InventoryJob{}).Where("id=?", job.ID).Update("status", "FAILED").Error; err != nil {
		c.t.Fatal(err)
	}
	if err := c.processor.Failed(context.Background(), c.seed.DB, job, "provider unavailable"); err != nil {
		c.t.Fatal(err)
	}
	c.issue()
}

func (c *chain) result(job database.InventoryJob, item database.InventoryItem) string {
	c.t.Helper()
	var row database.InventoryItemResult
	if err := c.seed.DB.Where("job_id=? AND item_identity_id=?", job.ID, item.IdentityID).Take(&row).Error; err != nil {
		c.t.Fatalf("%s has no result in generation %d: %v", item.Name, job.Generation, err)
	}
	return row.Status + ":" + value(row.ReasonCode)
}

// pairs lists source/current-photo of the confirmed pairs of item in job.
func (c *chain) pairs(job database.InventoryJob, item database.InventoryItem) []string {
	c.t.Helper()
	var rows []database.InventoryEvidencePair
	if err := c.seed.DB.Where("job_id=? AND item_identity_id=? AND confirmed", job.ID, item.IdentityID).Order("created_at, id").Find(&rows).Error; err != nil {
		c.t.Fatal(err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Source+"/"+row.CurrentMediaID.String())
	}
	return out
}

func (c *chain) judged(call int, item database.InventoryItem) bool {
	calls := c.gateway.Calls("INVENTORY_COMPARE")
	return call < len(calls) && strings.Contains(calls[call].UserPrompt, item.IdentityID.String())
}

func samePairs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Items 2 and 3 (review round 3): four chained generations of one room.
// Generation 2 revises Sofá (succeeds); generation 3 revises Mesa and fails;
// generation 4 revises Sofá again. Generation 3 keeps Sofá's result WITH its
// staff pair; generation 4 copies exactly generation 3 and judges Mesa again
// with its staff pair (decided in the failed generation), never pairing
// generation 2's Mesa result with a photo nobody evaluated. A later complete
// reissue applies the newest staff decision of every item.
func TestTask05R3ChainedRevisionsCarryGenerationNMinus1WithStaffPairs(t *testing.T) {
	c := newChain(t)
	sofaOrigin := c.origin(c.sofa)
	staffSofa1, staffSofa2, staffMesa := "STAFF/"+c.current[1].String(), "STAFF/"+c.current[0].String(), "STAFF/"+c.current[1].String()

	g2 := c.revise(c.sofa, sofaOrigin, c.current[1])
	c.succeed(g2)
	if c.result(g2, c.sofa) != "PRESENT_UNCHANGED:" || !samePairs(c.pairs(g2, c.sofa), staffSofa1) {
		t.Fatalf("generation 2 Sofá = %s %v; want judged with its staff pair", c.result(g2, c.sofa), c.pairs(g2, c.sofa))
	}
	if c.result(g2, c.mesa) != "PRESENT_UNCHANGED:" || !samePairs(c.pairs(g2, c.mesa), "MATCHER/"+c.current[0].String()) {
		t.Fatalf("generation 2 Mesa = %s %v; want carried with its matcher pair", c.result(g2, c.mesa), c.pairs(g2, c.mesa))
	}

	g3 := c.revise(c.mesa, c.mesaOrigin, c.current[1])
	c.fail(g3)
	if got := c.result(g3, c.sofa); got != "PRESENT_UNCHANGED:" || !samePairs(c.pairs(g3, c.sofa), staffSofa1) {
		t.Fatalf("generation 3 (failed revision of Mesa) Sofá = %s %v; want generation 2's result with its staff pair", got, c.pairs(g3, c.sofa))
	}
	if got := c.result(g3, c.mesa); got != "NOT_OBSERVED:ANALYSIS_FAILED" {
		t.Fatalf("generation 3 Mesa = %s; want the failed judgment", got)
	}

	calls := len(c.gateway.Calls("INVENTORY_COMPARE"))
	g4 := c.revise(c.sofa, sofaOrigin, c.current[0])
	c.succeed(g4)
	if !c.judged(calls, c.sofa) || !c.judged(calls, c.mesa) {
		t.Fatalf("generation 4 must judge Sofá (revised) and Mesa (its staff decision failed in generation 3)")
	}
	if got := c.result(g4, c.mesa); got != "PRESENT_UNCHANGED:" || !samePairs(c.pairs(g4, c.mesa), staffMesa) {
		t.Fatalf("generation 4 Mesa = %s %v; want judged with its own staff pair only", got, c.pairs(g4, c.mesa))
	}
	if got := c.result(g4, c.sofa); got != "PRESENT_UNCHANGED:" || !samePairs(c.pairs(g4, c.sofa), staffSofa2) {
		t.Fatalf("generation 4 Sofá = %s %v; want the newest staff pair", got, c.pairs(g4, c.sofa))
	}

	// A complete reissue judges the whole room with every newest decision.
	if _, err := requestinventoryjobs.Reanalyze(context.Background(), c.seed.DB, c.seed.Tenant, c.seed.Inspection, c.clock); err != nil {
		t.Fatal(err)
	}
	g5 := c.generation(5)
	calls = len(c.gateway.Calls("INVENTORY_COMPARE"))
	c.succeed(g5)
	if !c.judged(calls, c.sofa) || !c.judged(calls, c.mesa) {
		t.Fatal("the complete reissue must judge every item")
	}
	if !samePairs(c.pairs(g5, c.sofa), staffSofa2) || !samePairs(c.pairs(g5, c.mesa), staffMesa) {
		t.Fatalf("complete reissue pairs Sofá=%v Mesa=%v; want the newest staff decision per item", c.pairs(g5, c.sofa), c.pairs(g5, c.mesa))
	}
}

// Item 3 (review round 3): after a recapture, a revision never keeps a result
// supported by a replaced photo. When the reissue of the recaptured room
// failed, the revision copies that failure (generation N-1), not the older
// successful generation; when it did not fail, the items paired with a
// replaced photo are judged again.
func TestTask05R3RevisionAfterRecaptureNeverKeepsReplacedPhotos(t *testing.T) {
	for _, failedReissue := range []bool{true, false} {
		c := newChain(t)
		// The participant resubmits the room with a new photo.
		var base database.CaptureDraft
		if err := c.seed.DB.Where("id=?", c.seed.CaptureDraft).Take(&base).Error; err != nil {
			t.Fatal(err)
		}
		at := c.seed.Now.Add(time.Hour)
		base.ID, base.CreatedAt, base.UpdatedAt = identity.NewID(), at, at
		if err := c.seed.DB.Create(&base).Error; err != nil {
			t.Fatal(err)
		}
		recaptured := c.seed.Photo(t, c.job.RequirementKey)
		ids, _ := json.Marshal([]identity.ID{recaptured})
		if err := c.seed.DB.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: c.seed.Tenant, DraftID: base.ID, RequirementKey: c.job.RequirementKey, MediaIDs: ids, Flags: json.RawMessage(`[]`), Version: 1, CreatedAt: at, UpdatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
		if failedReissue {
			if _, err := requestinventoryjobs.Reanalyze(context.Background(), c.seed.DB, c.seed.Tenant, c.seed.Inspection, c.clock); err != nil {
				t.Fatal(err)
			}
			c.fail(c.latest())
		}
		calls := len(c.gateway.Calls("INVENTORY_COMPARE"))
		next := c.revise(c.sofa, c.origin(c.sofa), recaptured)
		c.succeed(next)
		mesa := c.result(next, c.mesa)
		for _, pair := range c.pairs(next, c.mesa) {
			if strings.HasSuffix(pair, c.current[0].String()) {
				t.Fatalf("failedReissue=%v: Mesa keeps a pair on the replaced photo: %v", failedReissue, c.pairs(next, c.mesa))
			}
		}
		if failedReissue {
			if mesa != "NOT_OBSERVED:ANALYSIS_FAILED" || c.judged(calls, c.mesa) {
				t.Fatalf("after a failed reissue Mesa = %s (judged=%v); want the failure of generation N-1 carried", mesa, c.judged(calls, c.mesa))
			}
		} else if !c.judged(calls, c.mesa) || mesa != "PRESENT_UNCHANGED:" || !samePairs(c.pairs(next, c.mesa), "MATCHER/"+recaptured.String()) {
			t.Fatalf("Mesa = %s pairs %v judged=%v; want judged again on the new submission", mesa, c.pairs(next, c.mesa), c.judged(calls, c.mesa))
		}
	}
}
