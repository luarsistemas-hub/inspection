//go:build integration

package configure_policy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	processdeletions "inspection/services/inspection/internal/features/retention/process_deletion_requests"
	purgedata "inspection/services/inspection/internal/features/retention/purge_data"
	scheduleexpiry "inspection/services/inspection/internal/features/retention/schedule_expiry"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// dueRow is one queued retention.purge_due.v1 event of a tenant.
type dueRow struct {
	Body         []byte
	Envelope     events.RawEnvelope
	Class        string
	InspectionID identity.ID
	CreatedAt    time.Time
}

func queuedDue(t *testing.T, h *harness.Harness, tenantID identity.ID) []dueRow {
	t.Helper()
	var rows []database.OutboxIntent
	if err := h.AdminDB.Where("tenant_id=? AND type='retention.purge_due.v1'", tenantID).Order("created_at, id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := make([]dueRow, 0, len(rows))
	for _, row := range rows {
		item := dueRow{Body: row.Payload, CreatedAt: row.CreatedAt}
		if err := json.Unmarshal(row.Payload, &item.Envelope); err != nil {
			t.Fatal(err)
		}
		var payload struct {
			InspectionID identity.ID `json:"inspectionId"`
			Class        string      `json:"class"`
		}
		if err := json.Unmarshal(item.Envelope.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		item.Class, item.InspectionID = payload.Class, payload.InspectionID
		out = append(out, item)
	}
	return out
}

func dueOfClass(rows []dueRow, class string) []dueRow {
	var out []dueRow
	for _, row := range rows {
		if row.Class == class {
			out = append(out, row)
		}
	}
	return out
}

// consume runs one event through the production Consumer (with its inbox) and
// the production purge handler.
func consume(t *testing.T, h *harness.Harness, body []byte) (bool, error) {
	t.Helper()
	consumer := messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: "aibfix-retention", Handle: func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		return purgedata.HandleDueEvent(ctx, tx, h.Store, envelope)
	}}
	return consumer.Process(context.Background(), body, 0)
}

// discovered runs the real platform.discover_scheduler_tenants function, as the
// scheduler does, on the runtime role.
func discovered(t *testing.T, h *harness.Harness, now time.Time) map[identity.ID]bool {
	t.Helper()
	var ids []identity.ID
	if err := h.DB.Raw("SELECT pending.tenant_id FROM platform.discover_scheduler_tenants(?, ?, ?) AS pending(tenant_id)", now, now, now.Add(24*time.Hour)).Scan(&ids).Error; err != nil {
		t.Fatal(err)
	}
	found := map[identity.ID]bool{}
	for _, id := range ids {
		found[id] = true
	}
	return found
}

// attachToProject binds the standalone inspection to the fixture's project and
// stage and sets the project's state and closure.
func attachToProject(t *testing.T, h *harness.Harness, base harness.Task06Fixture, inspectionID identity.ID) {
	t.Helper()
	if err := h.AdminDB.Exec("UPDATE inspections.inspections SET project_id=?, stage_id=? WHERE tenant_id=? AND id=?", base.ProjectID, base.StageID, base.TenantID, inspectionID).Error; err != nil {
		t.Fatal(err)
	}
}

func setProject(t *testing.T, h *harness.Harness, base harness.Task06Fixture, status string, closedAt *time.Time) {
	t.Helper()
	if err := h.AdminDB.Exec("UPDATE projects.projects SET status=?, closed_at=? WHERE tenant_id=? AND id=?", status, closedAt, base.TenantID, base.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
}

func holdInspection(t *testing.T, h *harness.Harness, tenantID, inspectionID identity.ID, active bool) {
	t.Helper()
	id := inspectionID
	var existing database.LegalHold
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&existing).Error; err == nil {
		updates := map[string]any{"active": active, "released_at": nil}
		if !active {
			updates["released_at"] = time.Now().UTC()
		}
		if err := h.AdminDB.Model(&database.LegalHold{}).Where("id=?", existing.ID).Updates(updates).Error; err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := h.AdminDB.Create(&database.LegalHold{ID: identity.NewID(), TenantID: tenantID, InspectionID: &id, Reason: "aibfix hold", Active: active, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
}

// AIBFIX-A1: a tenant with a short retention policy is discovered long before
// the five-year default, applies the same hold/purge/outbox exclusions as
// EmitDue and stops being discovered once its event is queued.
func TestAIBFIX_A1DiscoveryUsesTheEffectivePolicyOfEachTenant(t *testing.T) {
	h := aipHarness(t)
	ctx := context.Background()
	short, plain := closedFixture(t, h), closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	closedAt := now.Add(-400 * day)
	dueItem := newClosedInspection(t, h, short, closedAt)
	heldItem := newClosedInspection(t, h, short, closedAt)
	newClosedInspection(t, h, plain, closedAt)
	holdInspection(t, h, short.TenantID, heldItem.ID, true)
	if _, err := savePolicy(t, h, short.TenantID, 365, 365, 0); err != nil {
		t.Fatal(err)
	}

	found := discovered(t, h, now)
	if !found[short.TenantID] {
		t.Fatal("the tenant with a 365-day policy and a 400-day-old inspection was not discovered")
	}
	if found[plain.TenantID] {
		t.Fatal("a tenant under the five-year default was discovered for a 400-day-old inspection")
	}
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, short.TenantID, now, 10); err != nil || emitted != 1 {
		t.Fatalf("EmitDue emitted=%d err=%v, want 1 (the held inspection is excluded)", emitted, err)
	}
	if discovered(t, h, now)[short.TenantID] {
		t.Fatal("the tenant is still discovered while its only due event is queued and its other inspection is held")
	}
	if processed, err := consume(t, h, dueOfClass(queuedDue(t, h, short.TenantID), "EVIDENCE_REPORTS")[0].Body); err != nil || !processed {
		t.Fatalf("queued event processed=%v err=%v", processed, err)
	}
	wantFullyPurged(t, "short policy purge", presenceOf(t, h, short.TenantID, dueItem))
	wantFullyRetained(t, "held inspection", presenceOf(t, h, short.TenantID, heldItem))
	holdInspection(t, h, short.TenantID, heldItem.ID, false)
	if !discovered(t, h, now.Add(25*time.Hour))[short.TenantID] {
		t.Fatal("the released inspection was not discovered again after the 24h window")
	}
}

// AIBFIX-A2: an inspection of a project enters retention only when the project
// is CLOSED, counting from closed_at; reopening the project recalculates it.
func TestAIBFIX_A2ProjectInspectionFollowsProjectClosure(t *testing.T) {
	h := aipHarness(t)
	ctx := context.Background()
	fixture := closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := now.Add(-6 * 365 * day)
	item := newClosedInspection(t, h, fixture, old)
	attachToProject(t, h, fixture, item.ID)
	// The fixture's own inspection belongs to the same project; keep it open.
	if err := h.AdminDB.Exec("UPDATE inspections.inspections SET status='IN_PROGRESS', deadline_at=? WHERE tenant_id=? AND id=?", now.Add(30*day), fixture.TenantID, fixture.InspectionID).Error; err != nil {
		t.Fatal(err)
	}

	// The project is ACTIVE: the inspection is six years old but has no clock.
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now, 10); err != nil || emitted != 0 {
		t.Fatalf("active project: emitted=%d err=%v, want 0", emitted, err)
	}
	if discovered(t, h, now)[fixture.TenantID] {
		t.Fatal("a tenant whose only old inspection belongs to an active project was discovered")
	}
	if err := handleDue(t, h, fixture.TenantID, item.ID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("active project: err=%v, want not eligible", err)
	}
	wantFullyRetained(t, "active project", presenceOf(t, h, fixture.TenantID, item))

	// A deletion request is blocked with the retention restriction.
	request := database.DeletionRequest{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: &item.ID, Status: "REQUESTED", RequestedAt: now}
	if err := h.AdminDB.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	executor, err := processdeletions.Setup(processdeletions.Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.RunDue(ctx, fixture.TenantID, 10); err != nil {
		t.Fatal(err)
	}
	var stored database.DeletionRequest
	if err := h.AdminDB.Where("id=?", request.ID).Take(&stored).Error; err != nil || stored.Status != "BLOCKED" || stored.Reason != "O prazo de retenção ainda não terminou." {
		t.Fatalf("deletion request of an active project: %+v err=%v", stored, err)
	}

	// Closed four years ago: still inside the five-year period.
	fourYears := now.Add(-4 * 365 * day)
	setProject(t, h, fixture, "CLOSED", &fourYears)
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now, 10); err != nil || emitted != 0 {
		t.Fatalf("project closed four years ago: emitted=%d err=%v, want 0", emitted, err)
	}
	if err := handleDue(t, h, fixture.TenantID, item.ID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("project closed four years ago: err=%v, want not eligible", err)
	}

	// Reopened and closed again just now: the clock restarts from the new closure.
	setProject(t, h, fixture, "ACTIVE", nil)
	justNow := now
	setProject(t, h, fixture, "CLOSED", &justNow)
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now, 10); err != nil || emitted != 0 {
		t.Fatalf("reopened and closed again: emitted=%d err=%v, want 0", emitted, err)
	}
	if err := handleDue(t, h, fixture.TenantID, item.ID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("reopened and closed again: err=%v, want not eligible", err)
	}
	wantFullyRetained(t, "recalculated clock", presenceOf(t, h, fixture.TenantID, item))

	// Closed six years ago: due through the scheduler, the consumer and the deletion request.
	sixYears := now.Add(-6 * 365 * day)
	setProject(t, h, fixture, "CLOSED", &sixYears)
	if !discovered(t, h, now)[fixture.TenantID] {
		t.Fatal("the tenant of a project closed six years ago was not discovered")
	}
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now, 10); err != nil || emitted != 1 {
		t.Fatalf("project closed six years ago: emitted=%d err=%v, want 1", emitted, err)
	}
	if _, err := executor.RunDue(ctx, fixture.TenantID, 10); err != nil {
		t.Fatal(err)
	}
	// The purge removes the inspection together with its deletion request.
	if err := h.AdminDB.Where("id=?", request.ID).Take(&stored).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deletion request after the retention elapsed: %+v err=%v, want it purged with the inspection", stored, err)
	}
	wantFullyPurged(t, "project closed six years ago", presenceOf(t, h, fixture.TenantID, item))
}

// AIBFIX-M1: a purge_due event that did not purge (legal hold) is queued again
// on the next day with a fresh identity the consumer inbox accepts, so the
// inspection is never orphaned.
func TestAIBFIX_M1HeldInspectionIsReemittedTheNextDayAndPurged(t *testing.T) {
	h := aipHarness(t)
	ctx := context.Background()
	fixture := closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	item := newClosedInspection(t, h, fixture, now.Add(-6*365*day))

	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now, 10); err != nil || emitted != 1 {
		t.Fatalf("first pass emitted=%d err=%v, want 1", emitted, err)
	}
	first := dueOfClass(queuedDue(t, h, fixture.TenantID), "EVIDENCE_REPORTS")[0]
	if first.Envelope.ID != scheduleexpiry.EventID(fixture.TenantID, item.ID, now) {
		t.Fatalf("event id %s is not the daily bucket of the inspection", first.Envelope.ID)
	}
	holdInspection(t, h, fixture.TenantID, item.ID, true)
	if processed, err := consume(t, h, first.Body); err != nil || !processed {
		t.Fatalf("event under a legal hold processed=%v err=%v, want consumed without error", processed, err)
	}
	wantFullyRetained(t, "held", presenceOf(t, h, fixture.TenantID, item))
	holdInspection(t, h, fixture.TenantID, item.ID, false)

	// The same day nothing is queued again and the old event is a duplicate.
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now.Add(time.Hour), 10); err != nil || emitted != 0 {
		t.Fatalf("pass inside the 24h window emitted=%d err=%v, want 0", emitted, err)
	}
	if processed, err := consume(t, h, first.Body); err != nil || processed {
		t.Fatalf("replay of the consumed event processed=%v err=%v, want duplicate", processed, err)
	}
	wantFullyRetained(t, "released but not yet re-emitted", presenceOf(t, h, fixture.TenantID, item))

	// The next day the inspection is queued again under a new identity and purges.
	next := now.Add(25 * time.Hour)
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, next, 10); err != nil || emitted != 1 {
		t.Fatalf("next-day pass emitted=%d err=%v, want 1", emitted, err)
	}
	second := dueOfClass(queuedDue(t, h, fixture.TenantID), "EVIDENCE_REPORTS")
	if len(second) != 2 || second[1].Envelope.ID == first.Envelope.ID || second[1].Envelope.ID != scheduleexpiry.EventID(fixture.TenantID, item.ID, next) {
		t.Fatalf("next-day event did not get a fresh daily identity: %+v", second)
	}
	if processed, err := consume(t, h, second[1].Body); err != nil || !processed {
		t.Fatalf("next-day event processed=%v err=%v", processed, err)
	}
	wantFullyPurged(t, "re-emitted", presenceOf(t, h, fixture.TenantID, item))
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, next.Add(48*time.Hour), 10); err != nil || emitted != 0 {
		t.Fatalf("pass after the purge emitted=%d err=%v, want 0", emitted, err)
	}
}

// AIBFIX-M2: the operational class nulls the capture location of submitted
// evidence after OperationalDays, keeps the evidence, audits once and is
// idempotent; a held or recent inspection keeps its location.
func TestAIBFIX_M2LocationIsRemovedAfterTheOperationalPeriod(t *testing.T) {
	h := aipHarness(t)
	ctx := context.Background()
	fixture := closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	lat, lon, accuracy, distance := int32(-23550520), int32(-46633308), 12000, int64(35000)
	withLocation := func(responsibilityID identity.ID, suffix string) identity.ID {
		t.Helper()
		id := identity.NewID()
		err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Create(&database.MediaObject{ID: id, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, ObjectKey: "tenant/" + fixture.TenantID.String() + "/media/" + id.String(), ContentType: "image/png", SHA256: "aibfix-" + id.String(), SizeBytes: 1, Status: "READY", MediaKind: "PHOTO", IdempotencyKey: "aibfix-" + suffix + id.String(), RequirementKey: "aibfix", Description: "evidência preservada", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", LatitudeE6: &lat, LongitudeE6: &lon, AccuracyMM: &accuracy, DistanceMM: &distance, CreatedAt: now}).Error
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	// The fixture inspection has a SUBMITTED capture draft, so its evidence is
	// protected by the submitted-evidence immutability trigger.
	setClosure(t, h, fixture.TenantID, fixture.InspectionID, now.Add(-2*365*day), "COMPLETED")
	submittedMedia := withLocation(fixture.ResponsibilityID, "submitted-")
	plainItem := newClosedInspection(t, h, fixture, now.Add(-2*365*day))
	plainMedia := withLocation(plainItem.ResponsibilityID, "plain-")
	heldItem := newClosedInspection(t, h, fixture, now.Add(-2*365*day))
	heldMedia := withLocation(heldItem.ResponsibilityID, "held-")
	recentItem := newClosedInspection(t, h, fixture, now.Add(-30*day))
	recentMedia := withLocation(recentItem.ResponsibilityID, "recent-")
	holdInspection(t, h, fixture.TenantID, heldItem.ID, true)

	location := func(id identity.ID) (present bool, description string) {
		t.Helper()
		var row database.MediaObject
		if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, id).Take(&row).Error; err != nil {
			t.Fatalf("evidence %s is gone: %v", id, err)
		}
		return row.LatitudeE6 != nil || row.LongitudeE6 != nil || row.AccuracyMM != nil || row.DistanceMM != nil, row.Description
	}

	// Under the default policy (one year) only the two-year-old, unheld inspections are due.
	if !discovered(t, h, now)[fixture.TenantID] {
		t.Fatal("the tenant with expired location was not discovered")
	}
	emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now, 10)
	if err != nil || emitted != 2 {
		t.Fatalf("EmitDue emitted=%d err=%v, want 2 operational events (evidence is not due yet)", emitted, err)
	}
	rows := queuedDue(t, h, fixture.TenantID)
	if len(dueOfClass(rows, "EVIDENCE_REPORTS")) != 0 || len(dueOfClass(rows, "OPERATIONAL_LOCATION")) != 2 {
		t.Fatalf("queued events = %+v, want only operational ones", rows)
	}
	for _, row := range dueOfClass(rows, "OPERATIONAL_LOCATION") {
		if processed, err := consume(t, h, row.Body); err != nil || !processed {
			t.Fatalf("operational event processed=%v err=%v", processed, err)
		}
	}
	for label, id := range map[string]identity.ID{"submitted": submittedMedia, "plain": plainMedia} {
		if present, description := location(id); present || description != "evidência preservada" {
			t.Fatalf("%s evidence: location present=%v description=%q, want location removed and evidence kept", label, present, description)
		}
	}
	for label, id := range map[string]identity.ID{"held": heldMedia, "recent": recentMedia} {
		if present, _ := location(id); !present {
			t.Fatalf("%s evidence lost its location before its clock elapsed", label)
		}
	}
	wantFullyRetained(t, "operational purge keeps the evidence", presenceOf(t, h, fixture.TenantID, plainItem))

	var audits []database.AuditEvent
	if err := h.AdminDB.Where("tenant_id=? AND action='retention.location_purged'", fixture.TenantID).Find(&audits).Error; err != nil || len(audits) != 2 {
		t.Fatalf("location audit events=%d err=%v, want one per inspection", len(audits), err)
	}
	for _, audit := range audits {
		if !strings.Contains(audit.Reason, "class=OPERATIONAL_LOCATION") || !strings.Contains(audit.Reason, "items=") || strings.Contains(audit.Reason, "-23") {
			t.Fatalf("location audit reason %q", audit.Reason)
		}
	}

	// Idempotent: nothing is queued again, a direct replay changes nothing.
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now.Add(48*time.Hour), 10); err != nil || emitted != 0 {
		t.Fatalf("pass after the location purge emitted=%d err=%v, want 0", emitted, err)
	}
	envelope := dueEnvelopeOfClass(t, fixture.TenantID, plainItem.ID, "OPERATIONAL_LOCATION")
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return purgedata.HandleDueEvent(ctx, tx, h.Store, envelope)
	}); err != nil {
		t.Fatalf("replay of the location purge: %v", err)
	}
	if err := h.AdminDB.Where("tenant_id=? AND action='retention.location_purged'", fixture.TenantID).Find(&audits).Error; err != nil || len(audits) != 2 {
		t.Fatalf("replay wrote a second audit event: %d", len(audits))
	}

	// Only the location may change: the retention flag does not open the evidence.
	err = h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.retention_location', 'on', true)").Error; err != nil {
			return err
		}
		return tx.Exec("UPDATE media.media_objects SET description='alterada' WHERE tenant_id=? AND id=?", fixture.TenantID, submittedMedia).Error
	})
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("a non-location change under the retention flag: err=%v, want the immutability error", err)
	}

	// A policy extension keeps the location of an inspection that is not yet due.
	if _, err := savePolicy(t, h, fixture.TenantID, 1825, 5*365, 0); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return purgedata.HandleDueEvent(ctx, tx, h.Store, dueEnvelopeOfClass(t, fixture.TenantID, recentItem.ID, "OPERATIONAL_LOCATION"))
	}); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("early location event for a recent inspection: err=%v, want not eligible", err)
	}
}

func dueEnvelopeOfClass(t *testing.T, tenantID, inspectionID identity.ID, class string) events.RawEnvelope {
	t.Helper()
	envelope := dueEnvelope(t, tenantID, inspectionID)
	payload, err := json.Marshal(map[string]any{"inspectionId": inspectionID, "class": class})
	if err != nil {
		t.Fatal(err)
	}
	envelope.Payload = payload
	return envelope
}

// AIBFIX-M2: the security class removes expired invitation, OTP and session
// secrets after SecurityDays and leaves unexpired and in-grace secrets alone.
func TestAIBFIX_M2ExpiredSecretsAreRemovedAfterTheSecurityGracePeriod(t *testing.T) {
	h := aipHarness(t)
	ctx := context.Background()
	fixture := closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	item := newClosedInspection(t, h, fixture, now)
	type secrets struct{ Invitation, OTP, Session identity.ID }
	seed := func(expires time.Time) secrets {
		t.Helper()
		s := secrets{identity.NewID(), identity.NewID(), identity.NewID()}
		err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Create(&database.Invitation{ID: s.Invitation, TenantID: fixture.TenantID, ResponsibilityID: item.ResponsibilityID, TokenHash: append([]byte("tokenhash-"), []byte(s.Invitation.String()[:22])...), PreviousTokenHash: []byte("previous-" + s.Invitation.String()[:23]), DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: expires, CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.OTPChallenge{ID: s.OTP, TenantID: fixture.TenantID, InvitationID: s.Invitation, CodeHMAC: []byte("0123456789abcdef0123456789abcdef"), SendCount: 1, LastSentAt: now, ExpiresAt: expires, CreatedAt: now}).Error; err != nil {
				return err
			}
			return tx.Create(&database.ExternalSession{ID: s.Session, TenantID: fixture.TenantID, InvitationID: &s.Invitation, ResponsibilityID: &item.ResponsibilityID, Kind: "CAPTURE", SessionDigest: []byte("session-" + s.Session.String()[:24]), CSRFDigest: []byte("csrf-digest-0123456789abcdef0123"), ExpiresAt: expires, CreatedAt: now}).Error
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	longExpired := seed(now.Add(-40 * day))
	inGrace := seed(now.Add(-10 * day))
	active := seed(now.Add(5 * day))
	if _, err := savePolicy(t, h, fixture.TenantID, 1825, 365, 30); err != nil {
		t.Fatal(err)
	}
	exists := func(model any, id identity.ID) bool {
		t.Helper()
		var count int64
		if err := h.AdminDB.Model(model).Where("tenant_id=? AND id=?", fixture.TenantID, id).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		return count == 1
	}
	invitation := func(id identity.ID) (hash []byte, previous []byte, purgedAt *time.Time) {
		t.Helper()
		var row struct {
			TokenHash, PreviousTokenHash []byte
			SecretsPurgedAt              *time.Time
		}
		if err := h.AdminDB.Raw("SELECT token_hash, previous_token_hash, secrets_purged_at FROM invitations.invitations WHERE tenant_id=? AND id=?", fixture.TenantID, id).Scan(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row.TokenHash, row.PreviousTokenHash, row.SecretsPurgedAt
	}
	originalHash, _, _ := invitation(longExpired.Invitation)

	if !discovered(t, h, now)[fixture.TenantID] {
		t.Fatal("the tenant with expired secrets was not discovered")
	}
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now, 10); err != nil || emitted != 1 {
		t.Fatalf("EmitDue emitted=%d err=%v, want one security event", emitted, err)
	}
	security := dueOfClass(queuedDue(t, h, fixture.TenantID), "SECURITY_SECRETS")
	if len(security) != 1 || security[0].Envelope.ID != scheduleexpiry.SecurityEventID(fixture.TenantID, now) {
		t.Fatalf("security events = %+v", security)
	}
	if processed, err := consume(t, h, security[0].Body); err != nil || !processed {
		t.Fatalf("security event processed=%v err=%v", processed, err)
	}
	if exists(&database.OTPChallenge{}, longExpired.OTP) || exists(&database.ExternalSession{}, longExpired.Session) {
		t.Fatal("the OTP challenge or the session expired beyond the grace period survived")
	}
	hash, previous, purgedAt := invitation(longExpired.Invitation)
	if purgedAt == nil || previous != nil || string(hash) == string(originalHash) || len(hash) != 32 {
		t.Fatalf("invitation secrets were not scrubbed: hash=%x previous=%v purgedAt=%v", hash, previous, purgedAt)
	}
	for label, s := range map[string]secrets{"in grace": inGrace, "active": active} {
		if !exists(&database.OTPChallenge{}, s.OTP) || !exists(&database.ExternalSession{}, s.Session) {
			t.Fatalf("%s: unexpired or in-grace OTP/session was removed", label)
		}
		if _, previous, purgedAt := invitation(s.Invitation); purgedAt != nil || previous == nil {
			t.Fatalf("%s: invitation secrets were scrubbed early", label)
		}
	}
	var audits []database.AuditEvent
	if err := h.AdminDB.Where("tenant_id=? AND action='retention.secrets_purged'", fixture.TenantID).Find(&audits).Error; err != nil || len(audits) != 1 || !strings.Contains(audits[0].Reason, "otp_challenges=1 sessions=1 invitations=1") {
		t.Fatalf("security audit = %+v err=%v", audits, err)
	}

	// Idempotent: a replay of the same purge and the same day change nothing.
	if processed, err := consume(t, h, security[0].Body); err != nil || processed {
		t.Fatalf("replay processed=%v err=%v, want duplicate", processed, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return purgedata.HandleDueEvent(ctx, tx, h.Store, dueEnvelopeOfClass(t, fixture.TenantID, identity.ID{}, "SECURITY_SECRETS"))
	}); err != nil {
		t.Fatalf("direct replay: %v", err)
	}
	if err := h.AdminDB.Where("tenant_id=? AND action='retention.secrets_purged'", fixture.TenantID).Find(&audits).Error; err != nil || len(audits) != 1 {
		t.Fatalf("replay wrote another audit event: %d", len(audits))
	}

	// Shortening the grace period brings the in-grace secrets due on the next day.
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now.Add(25*time.Hour), 10); err != nil || emitted != 0 {
		t.Fatalf("next day under a 30-day grace emitted=%d err=%v, want 0", emitted, err)
	}
	if _, err := savePolicy(t, h, fixture.TenantID, 1825, 365, 5); err != nil {
		t.Fatal(err)
	}
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now.Add(25*time.Hour), 10); err != nil || emitted != 1 {
		t.Fatalf("next day under a 5-day grace emitted=%d err=%v, want 1", emitted, err)
	}
	security = dueOfClass(queuedDue(t, h, fixture.TenantID), "SECURITY_SECRETS")
	if len(security) != 2 {
		t.Fatalf("security events = %d, want 2", len(security))
	}
	if processed, err := consume(t, h, security[1].Body); err != nil || !processed {
		t.Fatalf("second security event processed=%v err=%v", processed, err)
	}
	if exists(&database.OTPChallenge{}, inGrace.OTP) || exists(&database.ExternalSession{}, inGrace.Session) {
		t.Fatal("the secrets that left the shortened grace period survived")
	}
	if !exists(&database.OTPChallenge{}, active.OTP) || !exists(&database.ExternalSession{}, active.Session) {
		t.Fatal("unexpired secrets were removed")
	}
}

// Saving the values of the latest policy version again changes nothing, and
// duplicate scheduler replicas queue each event once without failing.
func TestAIBFIX_PolicyNoOpAndDuplicateReplicas(t *testing.T) {
	h := aipHarness(t)
	ctx := context.Background()
	fixture := closedFixture(t, h)
	first, err := savePolicy(t, h, fixture.TenantID, 700, 300, 10)
	if err != nil || first.Version != 1 {
		t.Fatalf("first save version=%d err=%v", first.Version, err)
	}
	again, err := savePolicy(t, h, fixture.TenantID, 700, 300, 10)
	if err != nil || again.ID != first.ID || again.Version != 1 || countPolicies(t, h, fixture.TenantID) != 1 {
		t.Fatalf("identical save: id=%s version=%d rows=%d err=%v", again.ID, again.Version, countPolicies(t, h, fixture.TenantID), err)
	}
	var audits int64
	if err := h.AdminDB.Model(&database.AuditEvent{}).Where("tenant_id=? AND action='retention.policy_configured'", fixture.TenantID).Count(&audits).Error; err != nil || audits != 1 {
		t.Fatalf("policy audit events=%d err=%v, want 1", audits, err)
	}
	if next, err := savePolicy(t, h, fixture.TenantID, 700, 300, 11); err != nil || next.Version != 2 {
		t.Fatalf("changed save version=%d err=%v", next.Version, err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	item := newClosedInspection(t, h, fixture, now.Add(-2*365*day))
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, emitErr := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now, 10)
			errs <- emitErr
		}()
	}
	wg.Wait()
	close(errs)
	for emitErr := range errs {
		if emitErr != nil {
			t.Fatalf("a duplicate replica failed: %v", emitErr)
		}
	}
	rows := dueOfClass(queuedDue(t, h, fixture.TenantID), "EVIDENCE_REPORTS")
	if len(rows) != 1 || rows[0].InspectionID != item.ID {
		t.Fatalf("evidence events after six concurrent passes = %d, want exactly one", len(rows))
	}
	var indexes int64
	if err := h.AdminDB.Raw("SELECT count(*) FROM pg_indexes WHERE schemaname='messaging' AND tablename='outbox' AND indexname='idx_outbox_retention_purge_due' AND indexdef LIKE '%retention.purge_due.v1%'").Scan(&indexes).Error; err != nil || indexes != 1 {
		t.Fatalf("partial outbox index present=%d err=%v", indexes, err)
	}
}
