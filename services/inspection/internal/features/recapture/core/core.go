// Package core owns selected-item recapture and immutable replacement lineage.
package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// VideoFinalizeResult reports whether prepared visual coverage closed a video recapture.
type VideoFinalizeResult struct {
	Request                 database.RecaptureRequest
	CoveredComponentIDs     []identity.ID
	SupplementPreparationID identity.ID
	Submitted               bool
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
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", in.TenantID, in.InspectionID).First(&inspection).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
		} else if err != nil {
			return err
		}
		if inspection.Status != "SUBMITTED" && inspection.Status != "ANALYZING" && inspection.Status != "RECAPTURE_PENDING" && inspection.Status != "COMPLETED" {
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
		var focused []capturecore.Requirement
		var reference json.RawMessage
		if originalDraft.MediaKind == "VIDEO" {
			focused, reference, err = projectVideoRecapture(ctx, tx, in, originalDraft, originalRequirements)
			if err != nil {
				return err
			}
		} else {
			available := make(map[string]capturecore.Requirement, len(originalRequirements))
			for _, requirement := range originalRequirements {
				available[requirement.Key] = requirement
			}
			focused = make([]capturecore.Requirement, 0, len(in.Items))
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
		}
		var active database.RecaptureRequest
		activeErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND inspection_id=? AND status IN ?", in.TenantID, in.InspectionID, []string{"REQUESTED", "ACCESSED", "RECEIVED"}).First(&active).Error
		if activeErr == nil {
			if !now.Before(active.DeadlineAt) {
				return apperror.New(apperror.InvalidState, "inspectionId", "active recapture has expired and must be finalized")
			}
			if originalDraft.MediaKind == "VIDEO" {
				return apperror.New(apperror.Conflict, "inspectionId", "an active video recapture already exists")
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
		if originalDraft.MediaKind != "VIDEO" {
			reference, err = focusedReference(originalDraft.ReferencePayload, focused)
			if err != nil {
				return err
			}
		}
		draft := database.CaptureDraft{ID: identity.NewID(), TenantID: in.TenantID, ResponsibilityID: responsibility, Kind: "RECAPTURE", MediaKind: originalDraft.MediaKind, TemplateVersionID: originalDraft.TemplateVersionID, ReferencePayload: reference, PolicyPayload: originalDraft.PolicyPayload, Requirements: reqJSON, Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}
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
	if corrected && (request.Status == "RECEIVED" || request.Status == "SUBMITTED") {
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id=?", tenantID, request.ResponsibilityID).Take(&draft).Error; err != nil {
			return database.RecaptureRequest{}, err
		}
		if draft.MediaKind == "VIDEO" {
			return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "requestId", "use FinalizeVideoTx with prepared visual coverage to fulfill a video recapture")
		}
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
		if request.Status == "RECEIVED" && !corrected {
			// A received supplement remains active until its prepared visual scope
			// resolves the selected requirements or the request expires.
		} else {
			return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "requestId", "recapture is terminal")
		}
	}
	if corrected {
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id=?", tenantID, request.ResponsibilityID).Take(&draft).Error; err != nil {
			return database.RecaptureRequest{}, err
		}
		if draft.MediaKind == "VIDEO" {
			return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "requestId", "use FinalizeVideoTx with prepared visual coverage to fulfill a video recapture")
		}
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
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id=?", tenantID, request.ResponsibilityID).Take(&draft).Error; err != nil {
			return database.RecaptureRequest{}, err
		}
		requirementStatus := "EXPIRED"
		if draft.MediaKind == "VIDEO" {
			requirementStatus = "UNRESOLVED"
		}
		if err := tx.Model(&database.RecaptureRequirement{}).Where("tenant_id=? AND request_id=? AND status='OPEN'", tenantID, request.ID).Update("status", requirementStatus).Error; err != nil {
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
	analysisEventType := "analysis.comparison_requested.v1"
	analysisPayload := map[string]any{"inspectionId": request.InspectionID, "recaptureRequestId": request.ID, "corrected": corrected, "requiresAttention": !corrected}
	var inspection database.Inspection
	if err := tx.Where("tenant_id=? AND id=?", tenantID, request.InspectionID).Take(&inspection).Error; err != nil {
		return database.RecaptureRequest{}, err
	}
	var recaptureDraft database.CaptureDraft
	if err := tx.Where("tenant_id=? AND responsibility_id=?", tenantID, request.ResponsibilityID).Take(&recaptureDraft).Error; err != nil {
		return database.RecaptureRequest{}, err
	}
	if inspection.MediaKind == "VIDEO" || recaptureDraft.MediaKind == "VIDEO" {
		analysisEventType = "analysis.comparison_completed.v1"
		analysisPayload["force"] = true
	}
	if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: analysisEventID, Type: analysisEventType, SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: request.InspectionID, CorrelationID: "analysis-recapture-" + request.ID.String(), Payload: analysisPayload}); err != nil {
		return database.RecaptureRequest{}, err
	}
	return request, nil
}

// FinalizeVideoTx closes a received video recapture using components verified by a prepared supplement.
func (s Service) FinalizeVideoTx(tx *gorm.DB, tenantID, requestID identity.ID, coveredComponentIDs []identity.ID) (VideoFinalizeResult, error) {
	var result VideoFinalizeResult
	var request database.RecaptureRequest
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, requestID).First(&request).Error; err != nil {
		return result, apperror.New(apperror.NotFound, "requestId", "recapture not found")
	}
	result.Request = request
	if request.Status != "RECEIVED" && request.Status != "SUBMITTED" {
		return result, apperror.New(apperror.InvalidState, "requestId", "video recapture is not ready for visual completion")
	}
	var draft database.CaptureDraft
	if err := tx.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE'", tenantID, request.ResponsibilityID).Take(&draft).Error; err != nil {
		return result, err
	}
	if draft.MediaKind != "VIDEO" {
		return result, apperror.New(apperror.InvalidState, "requestId", "video recapture is required")
	}
	var reference struct {
		VideoRecapture videoRecaptureProjection `json:"videoRecapture"`
	}
	if err := json.Unmarshal(draft.ReferencePayload, &reference); err != nil || len(reference.VideoRecapture.Selected) == 0 {
		return result, apperror.New(apperror.InvalidState, "requestId", "video recapture selection is invalid")
	}
	selected := make(map[identity.ID]string, len(reference.VideoRecapture.Selected))
	for _, item := range reference.VideoRecapture.Selected {
		if item.ComponentID == (identity.ID{}) || item.RequirementKey != "video:"+item.ComponentID.String() {
			return result, apperror.New(apperror.InvalidState, "requestId", "video recapture selection is invalid")
		}
		selected[item.ComponentID] = item.RequirementKey
	}
	seen := make(map[identity.ID]bool, len(coveredComponentIDs))
	for _, componentID := range coveredComponentIDs {
		if componentID == (identity.ID{}) || seen[componentID] {
			return result, apperror.New(apperror.InvalidInput, "coveredComponentIds", "covered components must be unique selected components")
		}
		if _, ok := selected[componentID]; !ok {
			return result, apperror.New(apperror.InvalidInput, "coveredComponentIds", "covered component was not selected for this recapture")
		}
		seen[componentID] = true
	}
	result.CoveredComponentIDs = append([]identity.ID(nil), coveredComponentIDs...)
	if len(coveredComponentIDs) == 0 {
		return result, nil
	}
	var supplement database.MediaObject
	if err := tx.Where("tenant_id=? AND responsibility_id=? AND requirement_key='video:supplement' AND media_kind='VIDEO' AND status='READY'", tenantID, request.ResponsibilityID).Order("created_at desc").Take(&supplement).Error; err != nil {
		return VideoFinalizeResult{}, apperror.New(apperror.InvalidState, "requestId", "ready prepared video supplement is required")
	}
	var submissions []database.SubmissionVersion
	if err := tx.Where("tenant_id=? AND draft_id=? AND responsibility_id=?", tenantID, draft.ID, request.ResponsibilityID).Order("version_number desc").Find(&submissions).Error; err != nil {
		return VideoFinalizeResult{}, err
	}
	receivedBeforeDeadline := false
	for _, submission := range submissions {
		var receipt struct {
			MediaKind string      `json:"mediaKind"`
			MediaID   identity.ID `json:"mediaId"`
		}
		if err := json.Unmarshal(submission.Payload, &receipt); err == nil && receipt.MediaKind == "VIDEO" && receipt.MediaID == supplement.ID && submission.SubmittedAt.Before(request.DeadlineAt) {
			receivedBeforeDeadline = true
			break
		}
	}
	if !receivedBeforeDeadline {
		return VideoFinalizeResult{}, apperror.New(apperror.InvalidState, "requestId", "video supplement was not received before the recapture deadline")
	}
	var preparation database.VideoPreparationJob
	if err := tx.Where("tenant_id=? AND media_id=? AND status='READY'", tenantID, supplement.ID).Order("generation desc").Take(&preparation).Error; err != nil {
		return VideoFinalizeResult{}, apperror.New(apperror.InvalidState, "requestId", "ready prepared video supplement is required")
	}
	result.SupplementPreparationID = preparation.ID
	var requirements []database.RecaptureRequirement
	if err := tx.Where("tenant_id=? AND request_id=?", tenantID, request.ID).Find(&requirements).Error; err != nil {
		return VideoFinalizeResult{}, err
	}
	rowsByKey := make(map[string]database.RecaptureRequirement, len(requirements))
	for _, requirement := range requirements {
		rowsByKey[requirement.RequirementKey] = requirement
	}
	if request.Status == "SUBMITTED" {
		for _, requirement := range requirements {
			componentID := componentIDForRequirement(selected, requirement.RequirementKey)
			requested := componentID != (identity.ID{}) && seen[componentID]
			if (requirement.Status == "CORRECTED") != requested {
				return VideoFinalizeResult{}, apperror.New(apperror.InvalidState, "requestId", "video recapture was already finalized with different coverage")
			}
		}
		result.Request = request
		result.Submitted = true
		return result, nil
	}
	for componentID := range seen {
		key := selected[componentID]
		row, ok := rowsByKey[key]
		if !ok {
			return VideoFinalizeResult{}, apperror.New(apperror.InvalidState, "requestId", "selected video requirement is unavailable")
		}
		if row.Status != "CORRECTED" {
			if err := tx.Model(&row).Update("status", "CORRECTED").Error; err != nil {
				return VideoFinalizeResult{}, err
			}
		}
	}
	var cycles int64
	if err := tx.Model(&database.RecaptureRequest{}).Where("tenant_id=? AND inspection_id=?", tenantID, request.InspectionID).Count(&cycles).Error; err != nil {
		return VideoFinalizeResult{}, err
	}
	if cycles >= MaxRecaptureCycles {
		if err := tx.Model(&database.RecaptureRequirement{}).Where("tenant_id=? AND request_id=? AND status='OPEN'", tenantID, request.ID).Update("status", "UNRESOLVED").Error; err != nil {
			return VideoFinalizeResult{}, err
		}
	}
	request.Status = "SUBMITTED"
	request.UpdatedAt = s.now()
	if err := tx.Save(&request).Error; err != nil {
		return VideoFinalizeResult{}, err
	}
	if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", tenantID, request.ResponsibilityID).Updates(map[string]any{"status": "SUBMITTED", "version": gorm.Expr("version + 1"), "updated_at": request.UpdatedAt}).Error; err != nil {
		return VideoFinalizeResult{}, err
	}
	if err := tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND responsibility_id=? AND revoked_at IS NULL", tenantID, request.ResponsibilityID).Update("revoked_at", request.UpdatedAt).Error; err != nil {
		return VideoFinalizeResult{}, err
	}
	if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", tenantID, request.ResponsibilityID).Updates(map[string]any{"status": "REVOKED", "revoked_at": request.UpdatedAt}).Error; err != nil {
		return VideoFinalizeResult{}, err
	}
	transition := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=? AND status='RECAPTURE_PENDING'", tenantID, request.InspectionID).Updates(map[string]any{"status": "ANALYZING", "version": gorm.Expr("version + 1"), "updated_at": request.UpdatedAt})
	if transition.Error != nil {
		return VideoFinalizeResult{}, transition.Error
	}
	if transition.RowsAffected != 1 {
		return VideoFinalizeResult{}, apperror.New(apperror.InvalidState, "inspection", "inspection no longer accepts recapture")
	}
	eventID := identity.NewID()
	if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "recapture.completed.v1", SchemaVersion: 1, OccurredAt: request.UpdatedAt, TenantID: tenantID, AggregateID: request.ID, CorrelationID: "recapture-complete-" + request.ID.String(), Payload: map[string]any{"requestId": request.ID, "inspectionId": request.InspectionID, "corrected": true, "expired": false, "coveredComponentIds": coveredComponentIDs, "supplementPreparationId": preparation.ID}}); err != nil {
		return VideoFinalizeResult{}, err
	}
	result.Request = request
	result.Submitted = true
	return result, nil
}

func componentIDForRequirement(selected map[identity.ID]string, key string) identity.ID {
	for componentID, requirementKey := range selected {
		if requirementKey == key {
			return componentID
		}
	}
	return identity.ID{}
}

// ReceiveVideoTx records a supplement receipt without treating the upload as
// visual fulfillment. A later prepared plan must resolve selected components
// before the request can transition to SUBMITTED.
func (s Service) ReceiveVideoTx(tx *gorm.DB, tenantID, requestID, responsibilityID, submissionID identity.ID) (database.RecaptureRequest, error) {
	var request database.RecaptureRequest
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND responsibility_id=?", tenantID, requestID, responsibilityID).First(&request).Error; err != nil {
		return database.RecaptureRequest{}, apperror.New(apperror.NotFound, "requestId", "recapture not found")
	}
	now := s.now()
	if !now.Before(request.DeadlineAt) {
		return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "requestId", "recapture deadline has expired")
	}
	if request.Status == "RECEIVED" {
		return request, nil
	}
	if request.Status != "REQUESTED" && request.Status != "ACCESSED" {
		return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "requestId", "recapture is not accepting a supplement")
	}
	var draft database.CaptureDraft
	if err := tx.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE' AND media_kind='VIDEO' AND status='SUBMITTED'", tenantID, responsibilityID).Take(&draft).Error; err != nil {
		return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "requestId", "submitted video supplement is required")
	}
	var submission database.SubmissionVersion
	if err := tx.Where("tenant_id=? AND id=? AND draft_id=? AND responsibility_id=?", tenantID, submissionID, draft.ID, responsibilityID).Take(&submission).Error; err != nil {
		return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "requestId", "supplement submission was not found")
	}
	var receipt struct {
		MediaKind string      `json:"mediaKind"`
		MediaID   identity.ID `json:"mediaId"`
	}
	if err := json.Unmarshal(submission.Payload, &receipt); err != nil || receipt.MediaKind != "VIDEO" || receipt.MediaID == (identity.ID{}) {
		return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "requestId", "video supplement receipt is invalid")
	}
	var media database.MediaObject
	if err := tx.Where("tenant_id=? AND id=? AND responsibility_id=? AND requirement_key='video:supplement' AND media_kind='VIDEO' AND status IN ?", tenantID, receipt.MediaID, responsibilityID, []string{"RECEIVED", "VERIFIED", "SCREENED", "READY"}).Take(&media).Error; err != nil {
		return database.RecaptureRequest{}, apperror.New(apperror.InvalidState, "requestId", "confirmed video supplement receipt is required")
	}
	request.Status = "RECEIVED"
	request.UpdatedAt = now
	if err := tx.Save(&request).Error; err != nil {
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
