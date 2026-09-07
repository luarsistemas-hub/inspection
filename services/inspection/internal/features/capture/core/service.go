package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	DB        *gorm.DB
	Now       func() time.Time
	Within    func(context.Context, identity.ID, func(*gorm.DB) error) error
	Finalizer SubmissionFinalizer
}

type SubmissionFinalizer interface {
	Finalize(context.Context, *gorm.DB, database.CaptureDraft, database.SubmissionVersion) error
}

type Bootstrap struct {
	Draft            database.CaptureDraft
	Requirements     []Requirement
	Answers          []database.RequirementAnswer
	ConfirmationOnly bool
}

type MetadataInput struct {
	TenantID, ResponsibilityID, MediaID        identity.ID
	RequirementKey, Description, CaptureSource string
	CapturedAt, WindowStartedAt                time.Time
	Latitude, Longitude, AccuracyMeters        *float64
	DeviceContext                              map[string]any
	Policy                                     GPSPolicy
	ExpectedVersion                            int64
}

func (s Service) Load(ctx context.Context, tenantID, responsibilityID identity.ID) (Bootstrap, error) {
	var result Bootstrap
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&result.Draft).Error; err != nil {
			return apperror.New(apperror.NotFound, "responsibility", "capture responsibility not found")
		}
		if result.Draft.Status == "SUBMITTED" {
			result.ConfirmationOnly = true
			result.Draft.Kind = ""
			result.Draft.TemplateVersionID = identity.ID{}
			result.Draft.ReferencePayload = json.RawMessage(`{}`)
			result.Draft.PolicyPayload = json.RawMessage(`{}`)
			result.Draft.Requirements = json.RawMessage(`[]`)
			return nil
		}
		if err := json.Unmarshal(result.Draft.Requirements, &result.Requirements); err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND draft_id=?", tenantID, result.Draft.ID).Find(&result.Answers).Error
	})
	return result, err
}

func (s Service) SaveMetadata(ctx context.Context, in MetadataInput) (database.MediaObject, error) {
	if strings.TrimSpace(in.RequirementKey) == "" {
		return database.MediaObject{}, apperror.New(apperror.InvalidInput, "requirementKey", "requirement is required")
	}
	if in.CaptureSource != "CAMERA" && in.CaptureSource != "GALLERY" {
		return database.MediaObject{}, apperror.New(apperror.InvalidInput, "captureSource", "capture source must be CAMERA or GALLERY")
	}
	capturedAtProvided := !in.CapturedAt.IsZero()
	if !capturedAtProvided {
		in.CapturedAt = s.now()
	}
	var reading *GPSReading
	if in.Latitude != nil && in.Longitude != nil && in.AccuracyMeters != nil {
		reading = &GPSReading{Latitude: *in.Latitude, Longitude: *in.Longitude, AccuracyMeters: *in.AccuracyMeters, CapturedAt: in.CapturedAt}
	}
	device, _ := json.Marshal(in.DeviceContext)
	var out database.MediaObject
	err := s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", in.TenantID, in.ResponsibilityID).First(&draft).Error; err != nil || draft.Status != "OPEN" {
			return apperror.New(apperror.InvalidState, "responsibility", "capture is unavailable")
		}
		var snapshot struct {
			Required       bool    `json:"gpsRequired"`
			AllowGallery   bool    `json:"allowGallery"`
			GeofenceMeters float64 `json:"geofenceMeters"`
			Latitude       *int32  `json:"assetLatitudeE6"`
			Longitude      *int32  `json:"assetLongitudeE6"`
		}
		if err := json.Unmarshal(draft.PolicyPayload, &snapshot); err != nil {
			return err
		}
		policy := GPSPolicy{Required: snapshot.Required, AllowGallery: snapshot.AllowGallery, GeofenceMeters: snapshot.GeofenceMeters}
		if snapshot.Latitude != nil && snapshot.Longitude != nil {
			lat, lon := float64(*snapshot.Latitude)/1e6, float64(*snapshot.Longitude)/1e6
			policy.AssetLatitude, policy.AssetLongitude = &lat, &lon
		}
		decision, err := EvaluateGPS(policy, reading, in.WindowStartedAt)
		if err != nil {
			return err
		}
		if decision.Blocked {
			return apperror.New(apperror.InvalidState, "gps", "required GPS must be accurate within the guided window")
		}
		if in.CaptureSource == "GALLERY" {
			decision.Flags = append(decision.Flags, "GALLERY_SOURCE")
		}
		flags, _ := json.Marshal(decision.Flags)
		var requirements []Requirement
		if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
			return err
		}
		var selected Requirement
		owned := false
		for _, r := range requirements {
			if r.Key == in.RequirementKey {
				owned = true
				selected = r
			}
		}
		if !owned && (draft.Kind == "RECAPTURE" || !strings.HasPrefix(in.RequirementKey, "extra:")) {
			return apperror.New(apperror.Forbidden, "requirementKey", "requirement is outside this responsibility")
		}
		if err := ValidateDescription(in.Description, selected.DescriptionRequired || strings.HasPrefix(in.RequirementKey, "extra:")); err != nil {
			return err
		}
		if in.CaptureSource == "GALLERY" && (!policy.AllowGallery || selected.CaptureSourcePolicy == "CAMERA_ONLY") {
			return apperror.New(apperror.InvalidInput, "captureSource", "gallery capture is not allowed")
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND responsibility_id=?", in.TenantID, in.MediaID, in.ResponsibilityID).First(&out).Error; err != nil {
			return apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		if out.Status != "READY" && out.Status != "SCREENED" {
			return apperror.New(apperror.InvalidState, "mediaId", "media verification and screening are pending")
		}
		if out.RequirementKey != "" {
			if metadataMatches(out, in, reading, device, capturedAtProvided) {
				return nil
			}
			return apperror.New(apperror.Conflict, "mediaId", "capture metadata was already saved")
		}
		if selected.MaximumMedia > 0 {
			var count int64
			if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND requirement_key=? AND status NOT IN ?", in.TenantID, in.ResponsibilityID, in.RequirementKey, []string{"ABORTED", "PURGED"}).Count(&count).Error; err != nil {
				return err
			}
			if count >= int64(selected.MaximumMedia) {
				return apperror.New(apperror.InvalidState, "requirementKey", "requirement media limit reached")
			}
		}
		var existingFlags []string
		_ = json.Unmarshal(out.Flags, &existingFlags)
		out.RequirementKey, out.Description, out.CaptureSource, out.CapturedAt, out.DeviceContext = in.RequirementKey, strings.TrimSpace(in.Description), in.CaptureSource, &in.CapturedAt, device
		out.Flags = mergeFlags(existingFlags, decision.Flags)
		if reading != nil {
			lat, lon := int32(reading.Latitude*1e6), int32(reading.Longitude*1e6)
			accuracy := int(reading.AccuracyMeters * 1000)
			distance := int64(decision.DistanceMeters * 1000)
			out.LatitudeE6, out.LongitudeE6, out.AccuracyMM, out.DistanceMM = &lat, &lon, &accuracy, &distance
		}
		if err := tx.Save(&out).Error; err != nil {
			return err
		}
		var answer database.RequirementAnswer
		find := tx.Where("tenant_id=? AND draft_id=? AND requirement_key=?", in.TenantID, draft.ID, in.RequirementKey).First(&answer)
		if find.Error != nil && find.Error != gorm.ErrRecordNotFound {
			return find.Error
		}
		var mediaIDs []identity.ID
		if find.Error == nil {
			_ = json.Unmarshal(answer.MediaIDs, &mediaIDs)
		}
		for _, id := range mediaIDs {
			if id == in.MediaID {
				return nil
			}
		}
		mediaIDs = append(mediaIDs, in.MediaID)
		encoded, _ := json.Marshal(mediaIDs)
		if find.Error == gorm.ErrRecordNotFound {
			answer = database.RequirementAnswer{ID: identity.NewID(), TenantID: in.TenantID, DraftID: draft.ID, RequirementKey: in.RequirementKey, MediaIDs: encoded, Flags: flags, Version: 1, CreatedAt: s.now(), UpdatedAt: s.now()}
			return tx.Create(&answer).Error
		}
		if in.ExpectedVersion > 0 && answer.Version != in.ExpectedVersion {
			return apperror.New(apperror.Conflict, "version", "stale requirement answer")
		}
		return tx.Model(&answer).Updates(map[string]any{"media_ids": encoded, "flags": flags, "version": answer.Version + 1, "updated_at": s.now()}).Error
	})
	return out, err
}

func metadataMatches(media database.MediaObject, in MetadataInput, reading *GPSReading, device json.RawMessage, capturedAtProvided bool) bool {
	if media.RequirementKey != in.RequirementKey || media.Description != strings.TrimSpace(in.Description) || media.CaptureSource != in.CaptureSource {
		return false
	}
	if capturedAtProvided && (media.CapturedAt == nil || !media.CapturedAt.Equal(in.CapturedAt)) {
		return false
	}
	if in.DeviceContext != nil {
		var existing, requested any
		if json.Unmarshal(media.DeviceContext, &existing) != nil || json.Unmarshal(device, &requested) != nil || !reflect.DeepEqual(existing, requested) {
			return false
		}
	}
	if reading == nil {
		return true
	}
	lat, lon := int32(reading.Latitude*1e6), int32(reading.Longitude*1e6)
	accuracy := int(reading.AccuracyMeters * 1000)
	return media.LatitudeE6 != nil && media.LongitudeE6 != nil && media.AccuracyMM != nil && *media.LatitudeE6 == lat && *media.LongitudeE6 == lon && *media.AccuracyMM == accuracy
}

func (s Service) DeclareImpossibility(ctx context.Context, tenantID, responsibilityID identity.ID, requirementKey, reason string, expectedVersion int64) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return apperror.New(apperror.InvalidInput, "reason", "impossibility reason is required")
	}
	if err := ValidateDescription(reason, true); err != nil {
		return err
	}
	return s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", tenantID, responsibilityID).First(&draft).Error; err != nil {
			return apperror.New(apperror.InvalidState, "responsibility", "capture is unavailable")
		}
		var requirements []Requirement
		_ = json.Unmarshal(draft.Requirements, &requirements)
		allowed := false
		for _, r := range requirements {
			if r.Key == requirementKey && r.ImpossibilityAllowed {
				allowed = true
			}
		}
		if !allowed {
			return apperror.New(apperror.InvalidInput, "requirementKey", "impossibility is not allowed")
		}
		var answer database.RequirementAnswer
		err := tx.Where("tenant_id=? AND draft_id=? AND requirement_key=?", tenantID, draft.ID, requirementKey).First(&answer).Error
		if err == gorm.ErrRecordNotFound {
			return tx.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: tenantID, DraftID: draft.ID, RequirementKey: requirementKey, MediaIDs: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), ImpossibilityReason: reason, Version: 1, CreatedAt: s.now(), UpdatedAt: s.now()}).Error
		}
		if err != nil {
			return err
		}
		if expectedVersion > 0 && answer.Version != expectedVersion {
			return apperror.New(apperror.Conflict, "version", "stale requirement answer")
		}
		if answer.ImpossibilityReason == reason {
			return nil
		}
		return tx.Model(&answer).Updates(map[string]any{"impossibility_reason": reason, "version": answer.Version + 1, "updated_at": s.now()}).Error
	})
}

func (s Service) Submit(ctx context.Context, tenantID, responsibilityID identity.ID, confirmIncomplete bool) (database.SubmissionVersion, error) {
	var result database.SubmissionVersion
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&draft).Error; err != nil {
			return apperror.New(apperror.NotFound, "responsibility", "capture not found")
		}
		if draft.Status == "SUBMITTED" {
			return tx.Where("tenant_id=? AND draft_id=?", tenantID, draft.ID).Order("version_number desc").First(&result).Error
		}
		if draft.Status != "OPEN" {
			return apperror.New(apperror.InvalidState, "responsibility", "capture is unavailable")
		}
		var acceptance int64
		if err := tx.Model(&database.ProcessingAcceptance{}).Where("tenant_id=? AND responsibility_id=? AND photo_processing AND ai_analysis AND gps_use", tenantID, responsibilityID).Count(&acceptance).Error; err != nil || acceptance != 1 {
			return apperror.New(apperror.InvalidState, "consent", "processing acceptance is required")
		}
		var requirements []Requirement
		if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
			return err
		}
		byKey := make(map[string]Requirement, len(requirements))
		for _, requirement := range requirements {
			byKey[requirement.Key] = requirement
		}
		var media []database.MediaObject
		if err := tx.Where("tenant_id=? AND responsibility_id=? AND status NOT IN ?", tenantID, responsibilityID, []string{"ABORTED", "PURGED"}).Find(&media).Error; err != nil {
			return err
		}
		if len(media) > MaxActivePhotos {
			return apperror.New(apperror.InvalidState, "media", "capture media limit exceeded")
		}
		ready := make(map[identity.ID]database.MediaObject, len(media))
		for _, item := range media {
			if item.Status != "READY" {
				return apperror.New(apperror.InvalidState, item.ID.String(), "media verification and screening must complete before submission")
			}
			requirement, owned := byKey[item.RequirementKey]
			extra := strings.HasPrefix(item.RequirementKey, "extra:") && draft.Kind != "RECAPTURE"
			if !owned && !extra {
				return apperror.New(apperror.InvalidInput, item.ID.String(), "media must belong to an applicable requirement")
			}
			if err := ValidateDescription(item.Description, requirement.DescriptionRequired || extra); err != nil {
				return apperror.New(apperror.InvalidInput, item.ID.String(), "media description is invalid")
			}
			ready[item.ID] = item
		}
		var rows []database.RequirementAnswer
		if err := tx.Where("tenant_id=? AND draft_id=?", tenantID, draft.ID).Find(&rows).Error; err != nil {
			return err
		}
		answers := make([]Answer, 0, len(rows))
		associated := make(map[identity.ID]bool, len(ready))
		for _, row := range rows {
			var ids []identity.ID
			if err := json.Unmarshal(row.MediaIDs, &ids); err != nil {
				return apperror.New(apperror.InvalidInput, row.RequirementKey, "invalid answer media")
			}
			for _, id := range ids {
				item, ok := ready[id]
				if !ok || associated[id] || item.RequirementKey != row.RequirementKey {
					return apperror.New(apperror.InvalidInput, row.RequirementKey, "answer contains unavailable or repeated media")
				}
				associated[id] = true
			}
			answers = append(answers, Answer{RequirementKey: row.RequirementKey, ReadyMedia: len(ids), Impossibility: row.ImpossibilityReason})
		}
		if len(associated) != len(ready) {
			return apperror.New(apperror.InvalidState, "media", "media metadata must be saved before submission")
		}
		complete, err := Evaluate(requirements, answers, confirmIncomplete)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"requirements": requirements, "answers": rows, "complete": complete.Complete, "requiresAttention": complete.RequiresAttention})
		digest := sha256.Sum256(payload)
		now := s.now()
		result = database.SubmissionVersion{ID: identity.NewID(), TenantID: tenantID, DraftID: draft.ID, ResponsibilityID: responsibilityID, VersionNumber: 1, Complete: complete.Complete, RequiresAttention: complete.RequiresAttention, Payload: payload, PayloadDigest: hex.EncodeToString(digest[:]), SubmittedAt: now}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if err := tx.Model(&draft).Updates(map[string]any{"status": "SUBMITTED", "version": draft.Version + 1, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND responsibility_id=? AND revoked_at IS NULL", tenantID, responsibilityID).Update("revoked_at", now).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", tenantID, responsibilityID).Updates(map[string]any{"status": "COMPLETED", "revoked_at": now}).Error; err != nil {
			return err
		}
		eventID := identity.NewID()
		if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "capture.submitted.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: responsibilityID, CorrelationID: "capture-submit-" + result.ID.String(), Payload: map[string]any{"draftId": draft.ID, "kind": draft.Kind, "responsibilityId": responsibilityID, "submissionId": result.ID, "complete": result.Complete, "requiresAttention": result.RequiresAttention}}); err != nil {
			return err
		}
		if s.Finalizer != nil {
			return s.Finalizer.Finalize(ctx, tx, draft, result)
		}
		return nil
	})
	return result, err
}

func mergeFlags(existing, added []string) json.RawMessage {
	seen := make(map[string]bool, len(existing)+len(added))
	result := make([]string, 0, len(existing)+len(added))
	for _, values := range [][]string{existing, added} {
		for _, value := range values {
			if value != "" && !seen[value] {
				seen[value] = true
				result = append(result, value)
			}
		}
	}
	encoded, _ := json.Marshal(result)
	return encoded
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Service) within(ctx context.Context, tenant identity.ID, fn func(*gorm.DB) error) error {
	if s.Within != nil {
		return s.Within(ctx, tenant, fn)
	}
	return (tenanttx.Runner{DB: s.DB}).Within(ctx, tenant, fn)
}
