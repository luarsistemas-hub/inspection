package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

type respondFixture struct {
	db        *gorm.DB
	service   Service
	clock     *time.Time
	tenantID  identity.ID
	versionID identity.ID
}

func newRespondFixture(t *testing.T) *respondFixture {
	t.Helper()
	db := inviteTestDB(t)
	clock := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	f := &respondFixture{db: db, clock: &clock, tenantID: identity.NewID(), versionID: identity.NewID()}
	if err := db.Exec(`INSERT INTO origins.origin_versions(id,tenant_id,status,has_inventory) VALUES(?,?,?,?)`, f.versionID, f.tenantID, "ACTIVE", true).Error; err != nil {
		t.Fatal(err)
	}
	within := func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }
	f.service = Service{DB: db, Clock: func() time.Time { return *f.clock }, Within: within, Inventory: inventorycore.Service{DB: db, Within: within}}
	return f
}

func (f *respondFixture) pending(t *testing.T, deadline time.Time) SessionOwner {
	t.Helper()
	row := seedResponseInvitation(t, f.db, f.tenantID, f.versionID, identity.NewID(), deadline)
	return SessionOwner{TenantID: f.tenantID, InvitationID: row.ID}
}

func (f *respondFixture) load(t *testing.T, owner SessionOwner) database.CounterpartyInvitation {
	t.Helper()
	var row database.CounterpartyInvitation
	if err := f.db.Where("id=?", owner.InvitationID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func (f *respondFixture) responseEvents(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := f.db.Table("messaging.outbox").Where("tenant_id=? AND type='counterparty.responded.v1'", f.tenantID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestUnit_UT287_AcknowledgeIsIdempotent(t *testing.T) {
	f := newRespondFixture(t)
	owner := f.pending(t, f.clock.Add(time.Hour))
	first, err := f.service.Respond(context.Background(), owner, "ACKNOWLEDGED", "", "ack-1")
	if err != nil || first.Response != "ACKNOWLEDGED" || first.RespondedAt == nil || !first.RespondedAt.Equal(*f.clock) || first.ResponseMutationID == nil || *first.ResponseMutationID != "ack-1" {
		t.Fatalf("UT-287 acknowledgment=%+v err=%v", first, err)
	}
	*f.clock = f.clock.Add(time.Minute)
	second, err := f.service.Respond(context.Background(), owner, "ACKNOWLEDGED", "", "ack-2")
	if err != nil || second.Response != "ACKNOWLEDGED" || !second.RespondedAt.Equal(*first.RespondedAt) || *second.ResponseMutationID != "ack-1" || !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("UT-287 second acknowledgment changed state: %+v err=%v", second, err)
	}
	if stored := f.load(t, owner); !stored.RespondedAt.Equal(*first.RespondedAt) || *stored.ResponseMutationID != "ack-1" {
		t.Fatalf("UT-287 stored acknowledgment changed: %+v", stored)
	}
	if got := f.responseEvents(t); got != 1 {
		t.Fatalf("UT-287 response events=%d, want 1", got)
	}
}

func TestUnit_UT288_OneResponsePerCounterparty(t *testing.T) {
	f := newRespondFixture(t)
	contested := f.pending(t, f.clock.Add(time.Hour))
	if _, err := f.service.Respond(context.Background(), contested, "CONTESTED", "A cozinha tem infiltração.", "contest"); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Respond(context.Background(), contested, "ACKNOWLEDGED", "", "ack-after-contest")
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("UT-288 acknowledge after CONTESTED error=%v, want InvalidState kind", err)
	}
	assertAppError(t, err, apperror.AlreadyResponded, "response")
	if stored := f.load(t, contested); stored.Response != "CONTESTED" {
		t.Fatalf("UT-288 response changed to %s", stored.Response)
	}
	acknowledged := f.pending(t, f.clock.Add(time.Hour))
	if _, err := f.service.Respond(context.Background(), acknowledged, "ACKNOWLEDGED", "", "ack"); err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Respond(context.Background(), acknowledged, "CONTESTED", "mudei de ideia", "contest-after-ack")
	assertAppError(t, err, apperror.AlreadyResponded, "response")
	if got := f.responseEvents(t); got != 2 {
		t.Fatalf("UT-288 response events=%d, want 2", got)
	}
}

func TestUnit_UT289_ContestRejectsBlankStatement(t *testing.T) {
	f := newRespondFixture(t)
	owner := f.pending(t, f.clock.Add(time.Hour))
	for _, statement := range []string{"", "   ", " \t\n "} {
		_, err := f.service.Respond(context.Background(), owner, "CONTESTED", statement, "blank")
		assertAppError(t, err, apperror.InvalidInput, "statement")
	}
	if stored := f.load(t, owner); stored.Response != "PENDING" || stored.Statement != nil || f.responseEvents(t) != 0 {
		t.Fatalf("UT-289 blank statement changed invitation: %+v", stored)
	}
}

func TestUnit_UT290_ContestStatementLengthBoundary(t *testing.T) {
	f := newRespondFixture(t)
	tooLong := f.pending(t, f.clock.Add(time.Hour))
	_, err := f.service.Respond(context.Background(), tooLong, "CONTESTED", strings.Repeat("é", 4001), "too-long")
	assertAppError(t, err, apperror.InvalidInput, "statement")
	if stored := f.load(t, tooLong); stored.Response != "PENDING" {
		t.Fatalf("UT-290 4001 characters changed invitation to %s", stored.Response)
	}
	exact := f.pending(t, f.clock.Add(time.Hour))
	statement := strings.Repeat("é", 4000)
	contested, err := f.service.Respond(context.Background(), exact, "CONTESTED", "  "+statement+"  ", "exact")
	if err != nil || contested.Statement == nil || *contested.Statement != statement {
		t.Fatalf("UT-290 4000 characters result=%+v err=%v", contested, err)
	}
}

func TestUnit_UT291_DeadlineBoundaryInsideRespond(t *testing.T) {
	f := newRespondFixture(t)
	deadline := *f.clock
	for _, response := range []string{"ACKNOWLEDGED", "CONTESTED"} {
		closed := f.pending(t, deadline)
		_, err := f.service.Respond(context.Background(), closed, response, "fora do prazo", "at-deadline")
		if !apperror.Is(err, apperror.InvalidState) {
			t.Fatalf("UT-291 %s at deadline error=%v, want InvalidState kind", response, err)
		}
		assertAppError(t, err, apperror.DeadlinePassed, "deadlineAt")
		if stored := f.load(t, closed); stored.Response != "PENDING" {
			t.Fatalf("UT-291 rejected response changed state to %s", stored.Response)
		}
	}
	open := f.pending(t, deadline.Add(time.Millisecond))
	if result, err := f.service.Respond(context.Background(), open, "ACKNOWLEDGED", "", "before-deadline"); err != nil || result.Response != "ACKNOWLEDGED" {
		t.Fatalf("UT-291 deadline-1ms result=%+v err=%v", result, err)
	}
}

func TestUnit_UT292_ContestReplayIsIdempotentOnlyForIdenticalMutation(t *testing.T) {
	f := newRespondFixture(t)
	owner := f.pending(t, f.clock.Add(time.Hour))
	first, err := f.service.Respond(context.Background(), owner, "CONTESTED", "Piso riscado na sala.", "contest-1")
	if err != nil {
		t.Fatal(err)
	}
	*f.clock = f.clock.Add(time.Minute)
	replay, err := f.service.Respond(context.Background(), owner, "CONTESTED", "Piso riscado na sala.", "contest-1")
	if err != nil {
		t.Fatalf("UT-292 identical replay error=%v", err)
	}
	if replay.Response != first.Response || replay.Resolution != first.Resolution || *replay.Statement != *first.Statement || !replay.RespondedAt.Equal(*first.RespondedAt) || !replay.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("UT-292 replay result=%+v, want %+v", replay, first)
	}
	if got := f.responseEvents(t); got != 1 {
		t.Fatalf("UT-292 response events=%d, want exactly one", got)
	}
	_, err = f.service.Respond(context.Background(), owner, "CONTESTED", "Outra declaração.", "contest-1")
	assertAppError(t, err, apperror.AlreadyResponded, "response")
	_, err = f.service.Respond(context.Background(), owner, "CONTESTED", "Outra declaração.", "contest-2")
	assertAppError(t, err, apperror.AlreadyResponded, "response")
	if stored := f.load(t, owner); *stored.Statement != "Piso riscado na sala." || f.responseEvents(t) != 1 {
		t.Fatalf("UT-292 second contestation changed state: %+v", stored)
	}
}

func TestUnit_UT293_ContestKeepsVersionActiveWithOpenResolution(t *testing.T) {
	f := newRespondFixture(t)
	owner := f.pending(t, f.clock.Add(time.Hour))
	contested, err := f.service.Respond(context.Background(), owner, "CONTESTED", "Faltou registrar o armário.", "contest")
	if err != nil || contested.Response != "CONTESTED" || contested.Resolution != "OPEN" || contested.RespondedAt == nil {
		t.Fatalf("UT-293 contest result=%+v err=%v", contested, err)
	}
	var version database.OriginVersion
	if err := f.db.Where("id=?", f.versionID).First(&version).Error; err != nil || version.Status != "ACTIVE" {
		t.Fatalf("UT-293 version status=%q err=%v, want ACTIVE", version.Status, err)
	}
	if got := f.responseEvents(t); got != 1 {
		t.Fatalf("UT-293 response events=%d, want 1", got)
	}
}

func TestUnit_UT294_NoResponseLateResponseAndResendReturnDeadlinePassed(t *testing.T) {
	f := newRespondFixture(t)
	owner := f.pending(t, f.clock.Add(-time.Second))
	if err := f.db.Model(&database.CounterpartyInvitation{}).Where("id=?", owner.InvitationID).Update("response", "NO_RESPONSE").Error; err != nil {
		t.Fatal(err)
	}
	for _, response := range []string{"ACKNOWLEDGED", "CONTESTED"} {
		_, err := f.service.Respond(context.Background(), owner, response, "tarde", "late")
		assertAppError(t, err, apperror.DeadlinePassed, "deadlineAt")
	}
	_, err := f.service.Resend(context.Background(), f.tenantID, owner.InvitationID)
	assertAppError(t, err, apperror.DeadlinePassed, "deadlineAt")
}

func TestUnit_UT297_ResolveDeclineRequiresReason(t *testing.T) {
	f := newRespondFixture(t)
	owner := f.pending(t, f.clock.Add(time.Hour))
	if _, err := f.service.Respond(context.Background(), owner, "CONTESTED", "Discordo.", "contest"); err != nil {
		t.Fatal(err)
	}
	actorID := identity.NewID()
	for _, reason := range []string{"", "   "} {
		_, err := f.service.Resolve(context.Background(), f.tenantID, owner.InvitationID, actorID, "DECLINE", reason)
		assertAppError(t, err, apperror.InvalidInput, "reason")
	}
	if stored := f.load(t, owner); stored.Resolution != "OPEN" {
		t.Fatalf("UT-297 rejected decline changed resolution to %s", stored.Resolution)
	}
	resolved, err := f.service.Resolve(context.Background(), f.tenantID, owner.InvitationID, actorID, "DECLINE", "  fora do escopo acordado ")
	if err != nil || resolved.Resolution != "DECLINED" || resolved.ResolutionReason == nil || *resolved.ResolutionReason != "fora do escopo acordado" || resolved.ResolvedBy == nil || *resolved.ResolvedBy != actorID || resolved.ResolvedAt == nil || resolved.AdjustmentVersionID != nil {
		t.Fatalf("UT-297 decline result=%+v err=%v", resolved, err)
	}
	if stored := f.load(t, owner); stored.Resolution != "DECLINED" || stored.Response != "CONTESTED" {
		t.Fatalf("UT-297 stored resolution=%+v", stored)
	}
}

func TestUnit_UT299_SecondResolveIsAlreadyResolved(t *testing.T) {
	f := newRespondFixture(t)
	owner := f.pending(t, f.clock.Add(time.Hour))
	if _, err := f.service.Respond(context.Background(), owner, "CONTESTED", "Discordo.", "contest"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Resolve(context.Background(), f.tenantID, owner.InvitationID, identity.NewID(), "DECLINE", "sem fundamento"); err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []string{"ACCEPT", "DECLINE"} {
		_, err := f.service.Resolve(context.Background(), f.tenantID, owner.InvitationID, identity.NewID(), outcome, "outro motivo")
		if !apperror.Is(err, apperror.Conflict) {
			t.Fatalf("UT-299 second %s error=%v, want Conflict kind", outcome, err)
		}
		assertAppError(t, err, apperror.AlreadyResolved, "counterpartyInvitationId")
	}
	if stored := f.load(t, owner); stored.Resolution != "DECLINED" || *stored.ResolutionReason != "sem fundamento" || stored.AdjustmentVersionID != nil {
		t.Fatalf("UT-299 first resolution changed: %+v", stored)
	}
}

func TestUnit_ResolveRejectsUncontestedInvitationAndInvalidOutcome(t *testing.T) {
	f := newRespondFixture(t)
	owner := f.pending(t, f.clock.Add(time.Hour))
	_, err := f.service.Resolve(context.Background(), f.tenantID, owner.InvitationID, identity.NewID(), "DECLINE", "motivo")
	assertAppError(t, err, apperror.InvalidState, "counterpartyInvitationId")
	_, err = f.service.Resolve(context.Background(), f.tenantID, owner.InvitationID, identity.NewID(), "MAYBE", "motivo")
	assertAppError(t, err, apperror.InvalidInput, "outcome")
	_, err = f.service.Resolve(context.Background(), f.tenantID, owner.InvitationID, identity.NewID(), "DECLINE", strings.Repeat("a", 501))
	assertAppError(t, err, apperror.InvalidInput, "reason")
	_, err = f.service.Resolve(context.Background(), f.tenantID, identity.NewID(), identity.NewID(), "DECLINE", "motivo")
	assertAppError(t, err, apperror.NotFound, "counterpartyInvitationId")
}

func TestUnit_UT301_DeadlineSweepOnlyMarksSilence(t *testing.T) {
	f := newRespondFixture(t)
	now := *f.clock
	expired := f.pending(t, now)
	future := f.pending(t, now.Add(time.Millisecond))
	acknowledged := f.pending(t, now.Add(-time.Second))
	contested := f.pending(t, now.Add(-time.Second))
	for owner, response := range map[SessionOwner]string{acknowledged: "ACKNOWLEDGED", contested: "CONTESTED"} {
		if err := f.db.Model(&database.CounterpartyInvitation{}).Where("id=?", owner.InvitationID).Update("response", response).Error; err != nil {
			t.Fatal(err)
		}
	}
	updated, err := f.service.Sweep(context.Background(), f.tenantID, now, 10)
	if err != nil || updated != 1 {
		t.Fatalf("UT-301 sweep updated=%d err=%v, want one", updated, err)
	}
	want := map[SessionOwner]string{expired: "NO_RESPONSE", future: "PENDING", acknowledged: "ACKNOWLEDGED", contested: "CONTESTED"}
	for owner, response := range want {
		if stored := f.load(t, owner); stored.Response != response || (owner == expired && stored.RespondedAt != nil) {
			t.Fatalf("UT-301 invitation response=%s respondedAt=%v, want %s", stored.Response, stored.RespondedAt, response)
		}
	}
	if again, err := f.service.Sweep(context.Background(), f.tenantID, now, 10); err != nil || again != 0 {
		t.Fatalf("UT-301 second sweep updated=%d err=%v, want 0", again, err)
	}
	var acknowledgedCount int64
	if err := f.db.Model(&database.CounterpartyInvitation{}).Where("tenant_id=? AND response='ACKNOWLEDGED'", f.tenantID).Count(&acknowledgedCount).Error; err != nil || acknowledgedCount != 1 {
		t.Fatalf("UT-301 sweep produced acknowledgments: %d err=%v", acknowledgedCount, err)
	}
}

func TestUnit_UT322_ClientMutationIDBounded(t *testing.T) {
	f := newRespondFixture(t)
	owner := f.pending(t, f.clock.Add(time.Hour))
	_, err := f.service.Respond(context.Background(), owner, "ACKNOWLEDGED", "", strings.Repeat("a", 201))
	assertAppError(t, err, apperror.InvalidInput, "clientMutationId")
}

func seedResponseInvitation(t *testing.T, db *gorm.DB, tenantID, versionID, participantID identity.ID, deadline time.Time) database.CounterpartyInvitation {
	t.Helper()
	id := identity.NewID()
	row := database.CounterpartyInvitation{ID: id, TenantID: tenantID, OriginVersionID: versionID, ParticipantID: participantID, TokenHash: []byte(id.String()), Channels: []byte(`[]`), DeadlineAt: deadline, Response: "PENDING", Resolution: "OPEN", DeliveryGeneration: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}
