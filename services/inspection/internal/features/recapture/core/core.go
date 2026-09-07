// Package core owns selected-item recapture and immutable replacement lineage.
package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxRecaptureCycles = 5

type Item struct {
	RequirementKey  string
	OriginalMediaID *identity.ID
	Reason          string
}
type RequestInput struct {
	TenantID, InspectionID identity.ID
	Items                  []Item
	Delivery               []invitationcore.DeliveryIntent
	DeadlineAt             time.Time
	IdempotencyKey         string
}
type Result struct {
	RequestID, ResponsibilityID identity.ID
	LinkToken                   string
	Status                      string
}
type Service struct {
	DB     *gorm.DB
	Now    func() time.Time
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

func (s Service) Request(ctx context.Context, in RequestInput) (Result, error) {
	if in.TenantID == (identity.ID{}) || in.InspectionID == (identity.ID{}) || len(in.Items) == 0 || len(in.Items) > capturecore.MaxActivePhotos || len(in.Delivery) == 0 || strings.TrimSpace(in.IdempotencyKey) == "" || !in.DeadlineAt.After(s.now()) {
		return Result{}, apperror.New(apperror.InvalidInput, "input", "selected items, channels, deadline, and idempotency key are required")
	}
	seen := map[string]bool{}
	for index := range in.Items {
		in.Items[index].RequirementKey = strings.TrimSpace(in.Items[index].RequirementKey)
		in.Items[index].Reason = strings.TrimSpace(in.Items[index].Reason)
		item := in.Items[index]
		if item.RequirementKey == "" || item.Reason == "" || seen[item.RequirementKey] {
			return Result{}, apperror.New(apperror.InvalidInput, "items", "each selected requirement needs one reason")
		}
		seen[item.RequirementKey] = true
	}
	canonical, _ := json.Marshal(struct {
		InspectionID identity.ID
		Items        []Item
		DeadlineAt   time.Time
	}{in.InspectionID, in.Items, in.DeadlineAt.UTC()})
	digestBytes := sha256.Sum256(canonical)
	payloadDigest := hex.EncodeToString(digestBytes[:])
	token, err := security.NewScopedToken(in.TenantID)
	if err != nil {
		return Result{}, err
	}
	hash := security.HashToken(token)
	deliveries, _ := json.Marshal(in.Delivery)
	now := s.now()
	out := Result{LinkToken: token, Status: "REQUESTED"}
	err = s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var existing database.RecaptureRequest
		if err := tx.Where("tenant_id=? AND idempotency_key=?", in.TenantID, in.IdempotencyKey).First(&existing).Error; err == nil {
			if existing.PayloadDigest != "" && existing.PayloadDigest != payloadDigest {
				return apperror.New(apperror.Conflict, "idempotencyKey", "idempotency key was already used with different input")
			}
			out.RequestID, out.ResponsibilityID, out.Status = existing.ID, existing.ResponsibilityID, existing.Status
			out.LinkToken = ""
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var inspection database.Inspection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", in.TenantID, in.InspectionID).First(&inspection).Error; err != nil {
			return apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
		}
		if inspection.Status != "SUBMITTED" && inspection.Status != "ANALYZING" && inspection.Status != "RECAPTURE_PENDING" {
			return apperror.New(apperror.InvalidState, "inspectionId", "recapture requires submitted evidence")
		}
		var originalDraft database.CaptureDraft
		responsibilityIDs := tx.Model(&database.Responsibility{}).Select("id").Where("tenant_id=? AND inspection_id=?", in.TenantID, in.InspectionID)
		if err := tx.Where("tenant_id=? AND kind='INSPECTION' AND responsibility_id IN (?)", in.TenantID, responsibilityIDs).First(&originalDraft).Error; err != nil {
			return apperror.New(apperror.InvalidState, "inspectionId", "inspection evidence responsibility was not found")
		}
		var originalRequirements []capturecore.Requirement
		if err := json.Unmarshal(originalDraft.Requirements, &originalRequirements); err != nil {
			return apperror.Wrap(apperror.InvalidState, err)
		}
		available := make(map[string]capturecore.Requirement, len(originalRequirements))
		for _, requirement := range originalRequirements {
			available[requirement.Key] = requirement
		}
		focused := make([]capturecore.Requirement, 0, len(in.Items))
		for _, item := range in.Items {
			requirement, ok := available[item.RequirementKey]
			if !ok {
				return apperror.New(apperror.InvalidInput, "items", "selected requirement was not found")
			}
			requirement.Required = true
			requirement.ImpossibilityAllowed = false
			requirement.MinimumMedia = 1
			requirement.Instructions = strings.TrimSpace(requirement.Instructions + "\nRecapture: " + item.Reason)
			if requirement.MaximumMedia < 1 {
				requirement.MaximumMedia = 1
			}
			focused = append(focused, requirement)
			if item.OriginalMediaID != nil {
				var count int64
				if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND id=? AND responsibility_id=? AND requirement_key=? AND status='READY'", in.TenantID, *item.OriginalMediaID, originalDraft.ResponsibilityID, item.RequirementKey).Count(&count).Error; err != nil || count != 1 {
					return apperror.New(apperror.InvalidInput, "items", "selected original evidence was not found")
				}
			}
		}
		var active database.RecaptureRequest
		activeErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND inspection_id=? AND status IN ?", in.TenantID, in.InspectionID, []string{"REQUESTED", "ACCESSED"}).First(&active).Error
		if activeErr == nil {
			if !now.Before(active.DeadlineAt) {
				return apperror.New(apperror.InvalidState, "inspectionId", "active recapture has expired and must be finalized")
			}
			if err := mergeActiveRequest(tx, active, in.Items, focused, originalDraft.ReferencePayload, now); err != nil {
				return err
			}
			out.RequestID, out.ResponsibilityID, out.Status, out.LinkToken = active.ID, active.ResponsibilityID, active.Status, ""
			return nil
		}
		if activeErr != gorm.ErrRecordNotFound {
			return activeErr
		}
		var cycles int64
		if err := tx.Model(&database.RecaptureRequest{}).Where("tenant_id=? AND inspection_id=?", in.TenantID, in.InspectionID).Count(&cycles).Error; err != nil {
			return err
		}
		if cycles >= MaxRecaptureCycles {
			return apperror.New(apperror.InvalidState, "items", "recapture cycle limit reached")
		}
		reqJSON, _ := json.Marshal(focused)
		responsibility := identity.NewID()
		request := database.RecaptureRequest{ID: identity.NewID(), TenantID: in.TenantID, InspectionID: in.InspectionID, ResponsibilityID: responsibility, Status: "REQUESTED", DeadlineAt: in.DeadlineAt, IdempotencyKey: in.IdempotencyKey, PayloadDigest: payloadDigest, CreatedAt: now, UpdatedAt: now}
		reference, err := focusedReference(originalDraft.ReferencePayload, focused)
		if err != nil {
			return err
		}
		draft := database.CaptureDraft{ID: identity.NewID(), TenantID: in.TenantID, ResponsibilityID: responsibility, Kind: "RECAPTURE", TemplateVersionID: originalDraft.TemplateVersionID, ReferencePayload: reference, PolicyPayload: originalDraft.PolicyPayload, Requirements: reqJSON, Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}
		responsibilityRow := database.Responsibility{ID: responsibility, TenantID: in.TenantID, InspectionID: in.InspectionID, ParticipantID: inspection.ParticipantID, Status: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}
		invite := database.Invitation{ID: identity.NewID(), TenantID: in.TenantID, ResponsibilityID: responsibility, TokenHash: hash[:], DeliveryIntents: deliveries, Status: "ACTIVE", ExpiresAt: in.DeadlineAt, CreatedAt: now}
		for _, row := range []any{&request, &responsibilityRow, &draft, &invite} {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		for _, item := range in.Items {
			row := database.RecaptureRequirement{ID: identity.NewID(), TenantID: in.TenantID, RequestID: request.ID, RequirementKey: item.RequirementKey, OriginalMediaID: item.OriginalMediaID, Reason: strings.TrimSpace(item.Reason), Status: "OPEN", CreatedAt: now}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		if inspection.Status != "RECAPTURE_PENDING" {
			if err := tx.Model(&inspection).Updates(map[string]any{"status": "RECAPTURE_PENDING", "version": inspection.Version + 1, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		eventID := identity.NewID()
		if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "recapture.requested.v1", SchemaVersion: 1, OccurredAt: now, TenantID: in.TenantID, AggregateID: request.ID, CorrelationID: "recapture-" + request.ID.String(), Payload: map[string]any{"requestId": request.ID, "inspectionId": request.InspectionID, "responsibilityId": responsibility}}); err != nil {
			return err
		}
		deadlineID := identity.NewID()
		if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: deadlineID, Type: "recapture.deadline_reached.v1", SchemaVersion: 1, OccurredAt: now, TenantID: in.TenantID, AggregateID: request.ID, CorrelationID: "recapture-deadline-" + request.ID.String(), Payload: map[string]any{"requestId": request.ID, "deadlineAt": request.DeadlineAt}}); err != nil {
			return err
		}
		if err := tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND id=?", in.TenantID, deadlineID).Update("next_attempt_at", request.DeadlineAt).Error; err != nil {
			return err
		}
		out.RequestID, out.ResponsibilityID = request.ID, responsibility
		return nil
	})
	return out, err
}

func mergeActiveRequest(tx *gorm.DB, request database.RecaptureRequest, items []Item, focused []capturecore.Requirement, originalReference json.RawMessage, now time.Time) error {
	var rows []database.RecaptureRequirement
	if err := tx.Where("tenant_id=? AND request_id=?", request.TenantID, request.ID).Find(&rows).Error; err != nil {
		return err
	}
	byKey := make(map[string]database.RecaptureRequirement, len(rows))
	for _, row := range rows {
		byKey[row.RequirementKey] = row
	}
	var draft database.CaptureDraft
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", request.TenantID, request.ResponsibilityID).First(&draft).Error; err != nil {
		return err
	}
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		return apperror.Wrap(apperror.InvalidState, err)
	}
	requirementsChanged := false
	for index, item := range items {
		if row, ok := byKey[item.RequirementKey]; ok {
			if row.OriginalMediaID != nil && item.OriginalMediaID != nil && *row.OriginalMediaID != *item.OriginalMediaID {
				return apperror.New(apperror.Conflict, "items", "active recapture targets different original evidence")
			}
			if !containsReason(row.Reason, item.Reason) {
				if err := tx.Model(&row).Update("reason", row.Reason+"\n"+item.Reason).Error; err != nil {
					return err
				}
				for index := range requirements {
					if requirements[index].Key == item.RequirementKey {
						requirements[index].Instructions += "\nRecapture: " + item.Reason
						requirementsChanged = true
					}
				}
			}
			continue
		}
		row := database.RecaptureRequirement{ID: identity.NewID(), TenantID: request.TenantID, RequestID: request.ID, RequirementKey: item.RequirementKey, OriginalMediaID: item.OriginalMediaID, Reason: item.Reason, Status: "OPEN", CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		requirements = append(requirements, focused[index])
		requirementsChanged = true
	}
	if !requirementsChanged {
		return nil
	}
	encoded, _ := json.Marshal(requirements)
	reference, err := focusedReference(originalReference, requirements)
	if err != nil {
		return err
	}
	return tx.Model(&draft).Updates(map[string]any{"requirements": encoded, "reference_payload": reference, "version": draft.Version + 1, "updated_at": now}).Error
}

func focusedReference(payload json.RawMessage, requirements []capturecore.Requirement) (json.RawMessage, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, err
	}
	if raw, ok := document["items"]; ok {
		var items []database.OriginEvidence
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		selected := make(map[string]bool, len(requirements))
		for _, requirement := range requirements {
			selected[requirement.Key] = true
		}
		filtered := make([]database.OriginEvidence, 0, len(requirements))
		for _, item := range items {
			if selected["origin:"+item.MediaID.String()] {
				filtered = append(filtered, item)
			}
		}
		document["items"], _ = json.Marshal(filtered)
	}
	return json.Marshal(document)
}

func containsReason(joined, reason string) bool {
	for _, existing := range strings.Split(joined, "\n") {
		if existing == reason {
			return true
		}
	}
	return false
}

func (s Service) Finalize(ctx context.Context, tenantID, requestID identity.ID, corrected bool) (Result, error) {
	var request database.RecaptureRequest
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var err error
		request, err = s.FinalizeTx(tx, tenantID, requestID, corrected)
		return err
	})
	return Result{RequestID: request.ID, ResponsibilityID: request.ResponsibilityID, Status: request.Status}, err
}

func (s Service) FinalizeTx(tx *gorm.DB, tenantID, requestID identity.ID, corrected bool) (database.RecaptureRequest, error) {
	var request database.RecaptureRequest
	now := s.now()
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, requestID).First(&request).Error; err != nil {
		return database.RecaptureRequest{}, apperror.New(apperror.NotFound, "requestId", "recapture not found")
	}
	target := "SUBMITTED"
	if !corrected {
		target = "EXPIRED"
	}
	if request.Status == target {
		return request, nil
	}
	if corrected && !now.Before(request.DeadlineAt) {
		return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "requestId", "recapture deadline has expired")
	}
	if !corrected {
		if now.Before(request.DeadlineAt) {
			return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "requestId", "recapture deadline has not expired")
		}
		target = "EXPIRED"
	}
	if request.Status != "REQUESTED" && request.Status != "ACCESSED" {
		return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "requestId", "recapture is terminal")
	}
	if corrected {
		var requirements []database.RecaptureRequirement
		if err := tx.Where("tenant_id=? AND request_id=?", tenantID, request.ID).Find(&requirements).Error; err != nil {
			return database.RecaptureRequest{}, err
		}
		for _, requirement := range requirements {
			var replacement database.MediaObject
			if err := tx.Where("tenant_id=? AND responsibility_id=? AND requirement_key=? AND status='READY'", tenantID, request.ResponsibilityID, requirement.RequirementKey).Order("created_at desc").First(&replacement).Error; err != nil {
				return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, requirement.RequirementKey, "ready replacement evidence is required")
			}
			if requirement.OriginalMediaID != nil {
				replacement.ReplacesMediaID = requirement.OriginalMediaID
				if err := tx.Model(&replacement).Update("replaces_media_id", requirement.OriginalMediaID).Error; err != nil {
					return database.RecaptureRequest{}, err
				}
			}
			if err := tx.Model(&requirement).Update("status", "CORRECTED").Error; err != nil {
				return database.RecaptureRequest{}, err
			}
		}
	}
	if !corrected {
		if err := tx.Model(&database.RecaptureRequirement{}).Where("tenant_id=? AND request_id=? AND status='OPEN'", tenantID, request.ID).Update("status", "EXPIRED").Error; err != nil {
			return database.RecaptureRequest{}, err
		}
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", tenantID, request.ResponsibilityID).Updates(map[string]any{"status": "EXPIRED", "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
			return database.RecaptureRequest{}, err
		}
	}
	request.Status = target
	request.UpdatedAt = now
	if err := tx.Save(&request).Error; err != nil {
		return database.RecaptureRequest{}, err
	}
	if err := tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND responsibility_id=? AND revoked_at IS NULL", tenantID, request.ResponsibilityID).Update("revoked_at", now).Error; err != nil {
		return database.RecaptureRequest{}, err
	}
	if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", tenantID, request.ResponsibilityID).Updates(map[string]any{"status": "REVOKED", "revoked_at": now}).Error; err != nil {
		return database.RecaptureRequest{}, err
	}
	if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", tenantID, request.ResponsibilityID).Updates(map[string]any{"status": target, "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
		return database.RecaptureRequest{}, err
	}
	transition := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=? AND status='RECAPTURE_PENDING'", tenantID, request.InspectionID).Updates(map[string]any{"status": "ANALYZING", "version": gorm.Expr("version + 1"), "updated_at": now})
	if transition.Error != nil {
		return database.RecaptureRequest{}, transition.Error
	}
	if transition.RowsAffected != 1 {
		return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "inspection", "inspection no longer accepts recapture")
	}
	eventID := identity.NewID()
	if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "recapture.completed.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: request.ID, CorrelationID: "recapture-complete-" + request.ID.String(), Payload: map[string]any{"requestId": request.ID, "inspectionId": request.InspectionID, "corrected": corrected, "expired": !corrected}}); err != nil {
		return database.RecaptureRequest{}, err
	}
	analysisEventID := identity.NewID()
	if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: analysisEventID, Type: "analysis.comparison_requested.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: request.InspectionID, CorrelationID: "analysis-recapture-" + request.ID.String(), Payload: map[string]any{"inspectionId": request.InspectionID, "recaptureRequestId": request.ID, "corrected": corrected, "requiresAttention": !corrected}}); err != nil {
		return database.RecaptureRequest{}, err
	}
	return request, nil
}
func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Service) within(ctx context.Context, t identity.ID, fn func(*gorm.DB) error) error {
	if s.Within != nil {
		return s.Within(ctx, t, fn)
	}
	return (tenanttx.Runner{DB: s.DB}).Within(ctx, t, fn)
}
