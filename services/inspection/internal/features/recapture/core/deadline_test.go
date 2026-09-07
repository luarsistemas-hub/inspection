package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
)

func TestIT239DeadlineIsDurableAndClosesExternalCapture(t *testing.T) {
	f, created := requestedFixture(t, 1)
	var intent database.OutboxIntent
	if err := f.db.Where("tenant_id=? AND type='recapture.deadline_reached.v1'", f.tenantID).First(&intent).Error; err != nil {
		t.Fatal(err)
	}
	if intent.Status != "PENDING" || !intent.NextAttemptAt.Equal(f.now.Add(time.Hour)) {
		t.Fatalf("deadline was not scheduled: %+v", intent)
	}
	var envelope struct {
		Payload struct {
			RequestID identity.ID `json:"requestId"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(intent.Payload, &envelope); err != nil || envelope.Payload.RequestID != created.RequestID {
		t.Fatalf("wrong deadline target: %s %v", intent.Payload, err)
	}
	f.service.Now = func() time.Time { return f.now.Add(time.Hour) }
	for range 2 {
		if _, err := f.service.Finalize(context.Background(), f.tenantID, created.RequestID, false); err != nil {
			t.Fatal(err)
		}
	}
	draft := loadDraft(t, f.db, created.ResponsibilityID)
	if draft.Status != "EXPIRED" {
		t.Fatalf("expired draft is editable: %+v", draft)
	}
	var invitation database.Invitation
	if err := f.db.Where("responsibility_id=?", created.ResponsibilityID).First(&invitation).Error; err != nil || invitation.Status != "REVOKED" || invitation.RevokedAt == nil {
		t.Fatalf("expired invitation remained active: %+v %v", invitation, err)
	}
	var responsibility database.Responsibility
	if err := f.db.First(&responsibility, "id=?", created.ResponsibilityID).Error; err != nil || responsibility.Status != "EXPIRED" {
		t.Fatalf("responsibility not finalized: %+v %v", responsibility, err)
	}
	var count int64
	if err := f.db.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='recapture.completed.v1'", f.tenantID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("expiration replay duplicated completion: %d %v", count, err)
	}
}

func TestIT240FocusedReferenceExcludesUnrequestedOriginPhotos(t *testing.T) {
	wanted, other := identity.NewID(), identity.NewID()
	payload := mustMarshal(map[string]any{"originVersionId": identity.NewID(), "items": []database.OriginEvidence{{MediaID: wanted, Description: "selected"}, {MediaID: other, Description: "private unrelated"}}})
	filtered, err := focusedReference(payload, []capturecore.Requirement{{Key: "origin:" + wanted.String()}})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Items []database.OriginEvidence `json:"items"`
	}
	if err := json.Unmarshal(filtered, &document); err != nil || len(document.Items) != 1 || document.Items[0].MediaID != wanted {
		t.Fatalf("unrequested origin evidence leaked: %s %v", filtered, err)
	}
}
