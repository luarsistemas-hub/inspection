//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	recapturerequest "inspection/services/inspection/internal/features/recapture/request"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

type recaptureNotifications struct {
	mu    sync.Mutex
	sent  int
	fails bool
}

func (n *recaptureNotifications) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fails {
		return notificationcore.NotificationResult{}, apperror.New(apperror.DependencyUnavailable, "delivery", "mail provider unavailable")
	}
	n.sent++
	return notificationcore.NotificationResult{ID: identity.NewID(), State: notificationcore.State("SENT")}, nil
}

type task06Recapture struct {
	fixture       task04PlanFixture
	bus           *mediator.Bus
	notifications *recaptureNotifications
	kitchenKey    string
	currentMedia  identity.ID
}

// newTask06Recapture seals a video plan with a filmed kitchen and an omitted
// bathroom and wires the real recapture request slice.
func newTask06Recapture(t *testing.T) task06Recapture {
	t.Helper()
	fixture := newTask04PlanFixture(t, true, true)
	plan := runTask04Plan(t, fixture)
	requirements := json.RawMessage(`[{"key":"video:main","section":"inspection","label":"Vídeo","evidenceKind":"VIDEO","required":true,"minimumMedia":1,"maximumMedia":1}]`)
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.draftID).Update("requirements", requirements).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.inspectionID).Update("active_video_plan_id", plan.ID).Error
	}); err != nil {
		t.Fatal(err)
	}
	var kitchen database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, fixture.kitchenID).Take(&kitchen).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	notifications := &recaptureNotifications{}
	if err := participantget.Setup(participantget.Dependencies{DB: fixture.h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := recapturerequest.Setup(recapturerequest.Dependencies{DB: fixture.h.DB, Bus: bus, Service: recapturecore.Service{DB: fixture.h.DB}, Notifications: notifications, CaptureBaseURL: "https://capture.example.test", Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	return task06Recapture{fixture: fixture, bus: bus, notifications: notifications, kitchenKey: kitchen.RequirementKey, currentMedia: fixture.currentMediaID()}
}

func (r task06Recapture) context(role string) context.Context {
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: r.fixture.tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: r.fixture.tenantID, Roles: []string{role}}, CorrelationID: "vv-task06-recapture"})
}

func (r task06Recapture) input(key string, items []recapturecore.Item, deadline time.Time) recapturecore.RequestInput {
	return recapturecore.RequestInput{TenantID: r.fixture.tenantID, InspectionID: r.fixture.inspectionID, Items: items, DeadlineAt: deadline, IdempotencyKey: key}
}

func (r task06Recapture) kitchen(reason string) []recapturecore.Item {
	media := r.currentMedia
	return []recapturecore.Item{{RequirementKey: r.kitchenKey, OriginalMediaID: &media, Reason: reason}}
}

func (r task06Recapture) request(ctx context.Context, input recapturecore.RequestInput) (recapturecore.Result, error) {
	raw, err := r.bus.Send(ctx, recapturerequest.Command{Input: input})
	if err != nil {
		return recapturecore.Result{}, err
	}
	return raw.(recapturecore.Result), nil
}

func (r task06Recapture) counts(t *testing.T) (requests, invitations int64) {
	t.Helper()
	if err := r.fixture.h.AdminDB.Model(&database.RecaptureRequest{}).Where("tenant_id=? AND inspection_id=?", r.fixture.tenantID, r.fixture.inspectionID).Count(&requests).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.fixture.h.AdminDB.Model(&database.Invitation{}).Where("tenant_id=?", r.fixture.tenantID).Count(&invitations).Error; err != nil {
		t.Fatal(err)
	}
	return requests, invitations
}

func assertTask06Code(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	if !apperror.Is(err, code) {
		t.Fatalf("recapture request error = %v, want %s", err, code)
	}
}

func TestVV_IT_227EmptyRecaptureItemsAreInvalidInputWithoutInvitation(t *testing.T) {
	r := newTask06Recapture(t)
	_, invitationsBefore := r.counts(t)
	_, err := r.request(r.context(auth.TenantAdmin), r.input("vv-it227", nil, time.Now().UTC().Add(24*time.Hour)))
	assertTask06Code(t, err, apperror.InvalidInput)
	requests, invitations := r.counts(t)
	if requests != 0 || invitations != invitationsBefore || r.notifications.sent != 0 {
		t.Fatalf("empty recapture created requests=%d invitations=%d→%d sent=%d", requests, invitationsBefore, invitations, r.notifications.sent)
	}
}

func TestVV_IT_228SixthRecaptureCycleIsInvalidStateAndKeepsFindings(t *testing.T) {
	r := newTask06Recapture(t)
	now := time.Now().UTC()
	if err := r.fixture.h.WithinTenant(r.fixture.ctx, r.fixture.tenantID, func(tx *gorm.DB) error {
		for cycle := 0; cycle < recapturecore.MaxRecaptureCycles; cycle++ {
			if err := tx.Create(&database.RecaptureRequest{ID: identity.NewID(), TenantID: r.fixture.tenantID, InspectionID: r.fixture.inspectionID, ResponsibilityID: identity.NewID(), Status: "SUBMITTED", DeadlineAt: now, IdempotencyKey: "vv-it228-prior-" + string(rune('a'+cycle)), PayloadDigest: strings.Repeat("a", 64), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var findingsBefore int64
	if err := r.fixture.h.AdminDB.Model(&database.FindingRecord{}).Where("tenant_id=?", r.fixture.tenantID).Count(&findingsBefore).Error; err != nil {
		t.Fatal(err)
	}
	_, err := r.request(r.context(auth.TenantAdmin), r.input("vv-it228", r.kitchen("Pia sem nitidez"), now.Add(24*time.Hour)))
	assertTask06Code(t, err, apperror.InvalidState)
	var findingsAfter int64
	if err := r.fixture.h.AdminDB.Model(&database.FindingRecord{}).Where("tenant_id=?", r.fixture.tenantID).Count(&findingsAfter).Error; err != nil {
		t.Fatal(err)
	}
	requests, _ := r.counts(t)
	if requests != recapturecore.MaxRecaptureCycles || findingsAfter != findingsBefore {
		t.Fatalf("sixth cycle changed state: requests=%d findings %d→%d", requests, findingsBefore, findingsAfter)
	}
}

func TestVV_IT_229ViewerRecaptureRequestIsForbidden(t *testing.T) {
	r := newTask06Recapture(t)
	_, err := r.request(r.context(auth.Viewer), r.input("vv-it229", r.kitchen("Pia sem nitidez"), time.Now().UTC().Add(24*time.Hour)))
	assertTask06Code(t, err, apperror.Forbidden)
	if requests, _ := r.counts(t); requests != 0 {
		t.Fatalf("viewer created %d recapture requests", requests)
	}
}

func TestVV_IT_230RacingRequestsForSameVideoGapKeepOneActiveCycle(t *testing.T) {
	r := newTask06Recapture(t)
	deadline := time.Now().UTC().Add(24 * time.Hour)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = r.request(r.context(auth.TenantAdmin), r.input("vv-it230-"+string(rune('a'+i)), r.kitchen("Pia sem nitidez"), deadline))
		}(i)
	}
	close(start)
	wg.Wait()
	succeeded := 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case apperror.Is(err, apperror.Conflict):
		default:
			t.Fatalf("racing request error = %v, want success or CONFLICT", err)
		}
	}
	var active int64
	if err := r.fixture.h.AdminDB.Model(&database.RecaptureRequest{}).Where("tenant_id=? AND inspection_id=? AND status IN ?", r.fixture.tenantID, r.fixture.inspectionID, []string{"REQUESTED", "ACCESSED", "RECEIVED"}).Count(&active).Error; err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || active != 1 {
		t.Fatalf("racing requests created succeeded=%d active=%d, want one logical cycle", succeeded, active)
	}
}

func TestVV_IT_231FailedRecaptureMailDoesNotClaimDelivery(t *testing.T) {
	r := newTask06Recapture(t)
	r.notifications.fails = true
	_, err := r.request(r.context(auth.TenantAdmin), r.input("vv-it231", r.kitchen("Pia sem nitidez"), time.Now().UTC().Add(24*time.Hour)))
	if err == nil {
		t.Fatal("failed mail delivery was reported as a successful recapture notification")
	}
	var request database.RecaptureRequest
	if err := r.fixture.h.AdminDB.Where("tenant_id=? AND inspection_id=?", r.fixture.tenantID, r.fixture.inspectionID).Take(&request).Error; err != nil {
		t.Fatal(err)
	}
	if request.Status != "REQUESTED" || r.notifications.sent != 0 {
		t.Fatalf("failed delivery left request=%s sent=%d; want pending REQUESTED and nothing sent", request.Status, r.notifications.sent)
	}
}

func TestVV_IT_232ReplayedRecaptureReturnsSameRequestAndCycleCount(t *testing.T) {
	r := newTask06Recapture(t)
	input := r.input("k16", r.kitchen("Pia sem nitidez"), time.Now().UTC().Add(24*time.Hour))
	first, err := r.request(r.context(auth.TenantAdmin), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.request(r.context(auth.TenantAdmin), input)
	if err != nil {
		t.Fatal(err)
	}
	requests, _ := r.counts(t)
	if first.RequestID != second.RequestID || requests != 1 {
		t.Fatalf("replay k16 first=%s second=%s cycles=%d", first.RequestID, second.RequestID, requests)
	}
}

func TestVV_IT_233RecaptureBeforeInitialSubmissionIsInvalidState(t *testing.T) {
	r := newTask06Recapture(t)
	if err := r.fixture.h.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", r.fixture.tenantID, r.fixture.inspectionID).Update("status", "IN_PROGRESS").Error; err != nil {
		t.Fatal(err)
	}
	_, err := r.request(r.context(auth.TenantAdmin), r.input("vv-it233", r.kitchen("Pia sem nitidez"), time.Now().UTC().Add(24*time.Hour)))
	assertTask06Code(t, err, apperror.InvalidState)
	if requests, _ := r.counts(t); requests != 0 {
		t.Fatalf("request before submission created %d cycles", requests)
	}
}

func TestVV_IT_234PastDeadlineOrClosedInspectionIsRejected(t *testing.T) {
	r := newTask06Recapture(t)
	_, err := r.request(r.context(auth.TenantAdmin), r.input("vv-it234-deadline", r.kitchen("Pia sem nitidez"), time.Now().UTC().Add(-time.Second)))
	assertTask06Code(t, err, apperror.InvalidInput)
	if err := r.fixture.h.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", r.fixture.tenantID, r.fixture.inspectionID).Update("status", "CANCELED").Error; err != nil {
		t.Fatal(err)
	}
	_, err = r.request(r.context(auth.TenantAdmin), r.input("vv-it234-closed", r.kitchen("Pia sem nitidez"), time.Now().UTC().Add(24*time.Hour)))
	assertTask06Code(t, err, apperror.InvalidState)
	if requests, _ := r.counts(t); requests != 0 {
		t.Fatalf("rejected requests created %d cycles", requests)
	}
}
