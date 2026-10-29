package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	inventoryvocabulary "inspection/services/inspection/internal/features/inventory/vocabulary"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/tenanttx"
	"inspection/services/inspection/internal/platform/video"

	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	DB                 *gorm.DB
	Now                func() time.Time
	Within             func(context.Context, identity.ID, func(*gorm.DB) error) error
	Finalizer          SubmissionFinalizer
	DisableRequiredGPS bool
}

type SubmissionFinalizer interface {
	Finalize(context.Context, *gorm.DB, database.CaptureDraft, database.SubmissionVersion) error
}

// decodeRequirements treats the v1 template-definition payload as an empty
// legacy requirement list and decodes v2 capture requirement arrays.
func decodeRequirements(raw json.RawMessage) ([]Requirement, error) {
	requirements, err := decodeAllRequirements(raw)
	if err != nil {
		return nil, err
	}
	return ActiveRequirements(requirements), nil
}

// ActiveRequirements drops environments removed during capture. Removed
// entries stay persisted only so a replayed declaration can be refused.
func ActiveRequirements(requirements []Requirement) []Requirement {
	return slices.DeleteFunc(requirements, func(requirement Requirement) bool { return requirement.Deleted })
}

func decodeAllRequirements(raw json.RawMessage) ([]Requirement, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, nil
	}
	var requirements []Requirement
	if err := json.Unmarshal(trimmed, &requirements); err != nil {
		return nil, err
	}
	return requirements, nil
}

type Bootstrap struct {
	Draft            database.CaptureDraft
	SchemaVersion    int
	Requirements     []Requirement
	Answers          []database.RequirementAnswer
	ConfirmationOnly bool
}

type MetadataInput struct {
	TenantID, ResponsibilityID, MediaID        identity.ID
	ReplacesMediaID                            *identity.ID
	RequirementKey, Description, CaptureSource string
	CapturedAt, WindowStartedAt                time.Time
	Latitude, Longitude, AccuracyMeters        *float64
	DeviceContext                              map[string]any
	Policy                                     GPSPolicy
	ExpectedVersion                            int64
}

// DeclareEnvironmentInput names one additional environment in a v2 capture.
type DeclareEnvironmentInput struct {
	TenantID, ResponsibilityID identity.ID
	TypeCode, Name             string
	ClientMutationID           string
}

// ManageEnvironmentInput renames or removes an environment added during capture.
type ManageEnvironmentInput struct {
	TenantID, ResponsibilityID identity.ID
	RequirementKey, Name       string
	Remove                     bool
}

// RecordQualityWarningInput records a non-blocking client-side quality result.
type RecordQualityWarningInput struct {
	TenantID, ResponsibilityID identity.ID
	RequirementKey, Code       string
	Ignored                    bool
}

func (s Service) Load(ctx context.Context, tenantID, responsibilityID identity.ID) (Bootstrap, error) {
	var result Bootstrap
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&result.Draft).Error; err != nil {
			return apperror.New(apperror.NotFound, "responsibility", "capture responsibility not found")
		}
		if result.Draft.Kind == "INSPECTION" && result.Draft.TemplateVersionID != (identity.ID{}) {
			var responsibility database.Responsibility
			if err := tx.Where("tenant_id=? AND id=?", tenantID, responsibilityID).Take(&responsibility).Error; err != nil {
				return err
			}
			var inspection database.Inspection
			if err := tx.Where("tenant_id=? AND id=?", tenantID, responsibility.InspectionID).Take(&inspection).Error; err != nil {
				return err
			}
			if responsibility.Status == "CANCELED" || responsibility.Status == "REVOKED" || inspection.Status == "CANCELED" || inspection.Status == "INVALIDATED" {
				return apperror.New(apperror.InvalidState, "responsibility", "capture is unavailable")
			}
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
		requirements, err := decodeRequirements(result.Draft.Requirements)
		if err != nil {
			return err
		}
		result.Requirements = requirements
		if result.Draft.TemplateVersionID != (identity.ID{}) {
			var templateVersion database.TemplateVersion
			if err := tx.Where("tenant_id=? AND id=?", tenantID, result.Draft.TemplateVersionID).Take(&templateVersion).Error; err != nil {
				return err
			}
			result.SchemaVersion = templateVersion.SchemaVersion
		}
		if s.DisableRequiredGPS {
			policy, err := withoutRequiredGPS(result.Draft.PolicyPayload)
			if err != nil {
				return err
			}
			result.Draft.PolicyPayload = policy
		}
		return tx.Where("tenant_id=? AND draft_id=?", tenantID, result.Draft.ID).Find(&result.Answers).Error
	})
	return result, err
}

// DeclareEnvironment adds an environment requirement to an open v2 capture.
func (s Service) DeclareEnvironment(ctx context.Context, in DeclareEnvironmentInput) (Requirement, error) {
	in.TypeCode = strings.ToUpper(strings.TrimSpace(in.TypeCode))
	in.Name = strings.Join(strings.Fields(strings.TrimSpace(in.Name)), " ")
	if err := inventoryvocabulary.ValidateEnvironment(in.TypeCode, in.Name); err != nil {
		return Requirement{}, apperror.New(apperror.InvalidInput, "name", "environment type or name is invalid")
	}
	var created Requirement
	err := s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", in.TenantID, in.ResponsibilityID).First(&draft).Error; err != nil {
			return apperror.New(apperror.NotFound, "responsibility", "capture not found")
		}
		if draft.Status != "OPEN" || draft.Kind == "RECAPTURE" {
			return apperror.New(apperror.InvalidState, "responsibility", "capture is unavailable")
		}
		var version database.TemplateVersion
		if err := tx.Where("tenant_id=? AND id=?", in.TenantID, draft.TemplateVersionID).Take(&version).Error; err != nil {
			return err
		}
		if version.SchemaVersion < 2 {
			return apperror.New(apperror.InvalidState, "templateVersion", "environment capture is not enabled")
		}
		var definition catalog.TemplateDocument
		if err := json.Unmarshal(version.DefinitionJSON, &definition); err != nil {
			return apperror.New(apperror.InvalidState, "templateVersion", "environment capture limits are unavailable")
		}
		limits := definition.EnvironmentCapture
		if limits == nil {
			limits = catalog.DefaultEnvironmentCapture()
		}
		requirements, err := decodeAllRequirements(draft.Requirements)
		if err != nil {
			return err
		}
		keyID := identity.NewID()
		if strings.TrimSpace(in.ClientMutationID) != "" {
			keyID = identity.NewDeterministicID("capture-environment", in.ResponsibilityID.String()+":"+in.ClientMutationID)
		}
		key := "env:new:" + keyID.String()
		activeCount := 0
		for _, existing := range requirements {
			// A replay of a removed environment must not append a second requirement with its key.
			if existing.Key == key && existing.Deleted {
				return apperror.New(apperror.Conflict, "clientMutationId", "environment was removed")
			}
			if existing.Deleted {
				continue
			}
			activeCount++
			if existing.Key == key {
				if existing.EnvironmentTypeCode != in.TypeCode || existing.EnvironmentName != in.Name {
					return apperror.New(apperror.Conflict, "clientMutationId", "client mutation id was reused with different environment data")
				}
				created = existing
				return nil
			}
			if strings.EqualFold(existing.EnvironmentTypeCode, in.TypeCode) && normalizedEnvironmentName(existing.EnvironmentName) == normalizedEnvironmentName(in.Name) {
				return apperror.New(apperror.Conflict, "name", "DUPLICATE_ENVIRONMENT")
			}
		}
		if activeCount >= 60 {
			return apperror.New(apperror.InvalidInput, "environments", "LIMIT_ENVIRONMENTS")
		}
		id := keyID
		minimum, maximum := limits.Photo.Minimum, limits.Photo.Maximum
		if draft.MediaKind == "VIDEO" {
			minimum, maximum = 1, limits.Video.MaximumClips
		}
		created = Requirement{Key: key, Section: in.TypeCode, Label: in.Name, EvidenceKind: draft.MediaKind, Required: true, ImpossibilityAllowed: true, MinimumMedia: minimum, MaximumMedia: maximum, CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: "CURRENT_ONLY", EnvironmentIdentityID: id.String(), EnvironmentTypeCode: in.TypeCode, EnvironmentName: in.Name, AddedDuringCapture: true, Expected: false}
		requirements = append(requirements, created)
		encoded, err := json.Marshal(requirements)
		if err != nil {
			return err
		}
		return tx.Model(&draft).Updates(map[string]any{"requirements": encoded, "version": gorm.Expr("version + 1"), "updated_at": s.now()}).Error
	})
	return created, err
}

// ManageEnvironment renames an added environment or soft-deletes it and its media.
func (s Service) ManageEnvironment(ctx context.Context, in ManageEnvironmentInput) (Requirement, error) {
	in.Name = strings.Join(strings.Fields(strings.TrimSpace(in.Name)), " ")
	var updated Requirement
	err := s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", in.TenantID, in.ResponsibilityID).First(&draft).Error; err != nil {
			return apperror.New(apperror.NotFound, "responsibility", "capture not found")
		}
		if draft.Status != "OPEN" || draft.MediaKind != "VIDEO" || draft.Kind == "RECAPTURE" {
			return apperror.New(apperror.InvalidState, "responsibility", "environment cannot be changed")
		}
		requirements, err := decodeAllRequirements(draft.Requirements)
		if err != nil {
			return err
		}
		index := -1
		for i := range requirements {
			if requirements[i].Key == in.RequirementKey {
				index = i
				break
			}
		}
		if index < 0 || !strings.HasPrefix(in.RequirementKey, "env:") || !requirements[index].AddedDuringCapture {
			return apperror.New(apperror.InvalidInput, "requirementKey", "only environments added during capture can be changed")
		}
		updated = requirements[index]
		if in.Remove {
			if updated.Deleted {
				return nil
			}
			updated.Deleted = true
			requirements[index] = updated
			encodedRequirements, err := json.Marshal(requirements)
			if err != nil {
				return err
			}
			if err := tx.Model(&draft).Updates(map[string]any{"requirements": encodedRequirements, "version": gorm.Expr("version + 1"), "updated_at": s.now()}).Error; err != nil {
				return err
			}
			var media []database.MediaObject
			if err := tx.Where("tenant_id=? AND responsibility_id=? AND requirement_key=? AND status NOT IN ?", in.TenantID, in.ResponsibilityID, in.RequirementKey, []string{"ABORTED", "PURGED"}).Find(&media).Error; err != nil {
				return err
			}
			if len(media) > 0 {
				if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND requirement_key=? AND status NOT IN ?", in.TenantID, in.ResponsibilityID, in.RequirementKey, []string{"ABORTED", "PURGED"}).Update("status", "ABORTED").Error; err != nil {
					return err
				}
				if err := tx.Model(&database.RequirementAnswer{}).Where("tenant_id=? AND draft_id=? AND requirement_key=?", in.TenantID, draft.ID, in.RequirementKey).Updates(map[string]any{"media_ids": json.RawMessage(`[]`), "version": gorm.Expr("version + 1"), "updated_at": s.now()}).Error; err != nil {
					return err
				}
				eventID := identity.NewID()
				if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "capture.environment_removed.v1", SchemaVersion: 1, OccurredAt: s.now(), TenantID: in.TenantID, AggregateID: in.ResponsibilityID, CorrelationID: "capture-environment-remove-" + eventID.String(), Payload: map[string]any{"responsibilityId": in.ResponsibilityID, "requirementKey": in.RequirementKey}}); err != nil {
					return err
				}
			}
			return nil
		}
		if updated.Deleted {
			return apperror.New(apperror.InvalidState, "requirementKey", "environment was removed")
		}
		if err := inventoryvocabulary.ValidateEnvironment(updated.EnvironmentTypeCode, in.Name); err != nil {
			return apperror.New(apperror.InvalidInput, "name", "environment name is invalid")
		}
		for _, existing := range requirements {
			if existing.Deleted || existing.Key == updated.Key {
				continue
			}
			if strings.EqualFold(existing.EnvironmentTypeCode, updated.EnvironmentTypeCode) && normalizedEnvironmentName(existing.EnvironmentName) == normalizedEnvironmentName(in.Name) {
				return apperror.New(apperror.Conflict, "name", "DUPLICATE_ENVIRONMENT")
			}
		}
		updated.EnvironmentName, updated.Label = in.Name, in.Name
		requirements[index] = updated
		encodedRequirements, err := json.Marshal(requirements)
		if err != nil {
			return err
		}
		return tx.Model(&draft).Updates(map[string]any{"requirements": encodedRequirements, "version": gorm.Expr("version + 1"), "updated_at": s.now()}).Error
	})
	return updated, err
}

func normalizedEnvironmentName(value string) string {
	value = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
	decomposed := norm.NFD.String(value)
	var normalized strings.Builder
	for _, r := range decomposed {
		if !unicode.Is(unicode.Mn, r) {
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
}

// RecordQualityWarning stores a unique, non-blocking quality flag on an answer.
func (s Service) RecordQualityWarning(ctx context.Context, in RecordQualityWarningInput) ([]string, error) {
	code := strings.ToUpper(strings.TrimSpace(in.Code))
	valid := code == "DARK" || code == "BLURRY" || code == "SHORT" || code == "TOO_FEW_PHOTOS"
	if !valid {
		return nil, apperror.New(apperror.InvalidInput, "code", "quality warning code is invalid")
	}
	var result []string
	err := s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", in.TenantID, in.ResponsibilityID).First(&draft).Error; err != nil || draft.Status != "OPEN" {
			return apperror.New(apperror.InvalidState, "responsibility", "capture is unavailable")
		}
		requirements, err := decodeRequirements(draft.Requirements)
		if err != nil {
			return err
		}
		found := false
		for _, requirement := range requirements {
			if requirement.Key == in.RequirementKey {
				found = true
				break
			}
		}
		if !found {
			return apperror.New(apperror.InvalidInput, "requirementKey", "requirement is not part of this capture")
		}
		var answer database.RequirementAnswer
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND draft_id=? AND requirement_key=?", in.TenantID, draft.ID, in.RequirementKey).First(&answer).Error
		isNew := errors.Is(err, gorm.ErrRecordNotFound)
		if isNew {
			answer = database.RequirementAnswer{ID: identity.NewID(), TenantID: in.TenantID, DraftID: draft.ID, RequirementKey: in.RequirementKey, MediaIDs: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), Version: 1, CreatedAt: s.now(), UpdatedAt: s.now()}
		} else if err != nil {
			return err
		}
		if len(answer.Flags) > 0 {
			if err := json.Unmarshal(answer.Flags, &result); err != nil {
				return err
			}
		}
		flag := "QUALITY_WARNING:" + code
		if in.Ignored {
			flag = "QUALITY_WARNING_IGNORED:" + code
		}
		if slices.Contains(result, flag) {
			return nil
		}
		result = appendUniqueFlag(result, flag)
		encoded, _ := json.Marshal(result)
		answer.Flags = encoded
		answer.UpdatedAt = s.now()
		if isNew {
			return tx.Create(&answer).Error
		}
		return tx.Save(&answer).Error
	})
	return result, err
}

func appendUniqueFlag(flags []string, value string) []string {
	for _, flag := range flags {
		if flag == value {
			return flags
		}
	}
	return append(flags, value)
}

func captureProfileDigest(requirementKey string) string {
	if strings.HasPrefix(requirementKey, "env:") {
		return video.DefaultProfile().Digest()
	}
	profile, err := video.ProfileByNameVersion("standard", 1)
	if err != nil {
		return video.DefaultProfile().Digest()
	}
	return profile.Digest()
}

func (s Service) SaveMetadata(ctx context.Context, in MetadataInput) (database.MediaObject, error) {
	if strings.TrimSpace(in.RequirementKey) == "" {
		return database.MediaObject{}, apperror.New(apperror.InvalidInput, "requirementKey", "requirement is required")
	}
	if in.CaptureSource != "CAMERA" && in.CaptureSource != "GALLERY" && in.CaptureSource != "FILE" {
		return database.MediaObject{}, apperror.New(apperror.InvalidInput, "captureSource", "capture source is invalid")
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
		if draft.MediaKind != "VIDEO" && in.CaptureSource == "FILE" {
			return apperror.New(apperror.InvalidInput, "captureSource", "file capture is only available for video")
		}
		gallerySource := in.CaptureSource == "GALLERY" || in.CaptureSource == "FILE"
		policy := GPSPolicy{Required: snapshot.Required && !s.DisableRequiredGPS, AllowGallery: snapshot.AllowGallery, GeofenceMeters: snapshot.GeofenceMeters}
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
		if gallerySource {
			decision.Flags = append(decision.Flags, "GALLERY_SOURCE")
		}
		flags, _ := json.Marshal(decision.Flags)
		requirements, err := decodeRequirements(draft.Requirements)
		if err != nil {
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
		if draft.MediaKind == "VIDEO" && (in.RequirementKey == "video:main" || in.RequirementKey == "video:supplement") {
			owned = true
			selected = Requirement{Key: in.RequirementKey, CaptureSourcePolicy: "CAMERA_DEFAULT"}
		} else if draft.MediaKind == "VIDEO" && !(owned && strings.HasPrefix(in.RequirementKey, "env:")) && !strings.HasPrefix(in.RequirementKey, "video:") {
			return apperror.New(apperror.InvalidInput, "requirementKey", "video evidence requires a video requirement")
		}
		if !owned && (draft.Kind == "RECAPTURE" || !strings.HasPrefix(in.RequirementKey, "extra:")) {
			return apperror.New(apperror.Forbidden, "requirementKey", "requirement is outside this responsibility")
		}
		// Descriptions enrich evidence but are optional for declared requirements,
		// including requirements from older templates. Ad-hoc extra evidence
		// remains self-describing for the existing extra-evidence contract.
		if err := ValidateDescription(in.Description, strings.HasPrefix(in.RequirementKey, "extra:")); err != nil {
			return err
		}
		if gallerySource && (!policy.AllowGallery || selected.CaptureSourcePolicy == "CAMERA_ONLY") {
			return apperror.New(apperror.InvalidInput, "captureSource", "gallery capture is not allowed")
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND responsibility_id=?", in.TenantID, in.MediaID, in.ResponsibilityID).First(&out).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.NotFound, "mediaId", "media not found")
			}
			return err
		}
		if out.Status != "READY" && out.Status != "SCREENED" && !(draft.MediaKind == "VIDEO" && out.MediaKind == "VIDEO" && out.Status == "RECEIVED") {
			return apperror.New(apperror.InvalidState, "mediaId", "media verification and screening are pending")
		}
		if draft.MediaKind == "VIDEO" && (out.MediaKind != "VIDEO" || out.VideoAttemptID == nil) {
			return apperror.New(apperror.InvalidInput, "mediaId", "video evidence is required")
		}
		// Video upload reserves its requirement key before capture metadata is saved.
		// CapturedAt is written only by SaveMetadata and distinguishes that reservation.
		if out.RequirementKey != "" && out.CapturedAt != nil {
			if metadataMatches(out, in, reading, device, capturedAtProvided) {
				return nil
			}
			return apperror.New(apperror.Conflict, "mediaId", "capture metadata was already saved")
		}
		var replacedMediaIDs []identity.ID
		if in.ReplacesMediaID != nil {
			var replaced database.MediaObject
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=? AND id=?", in.TenantID, in.ResponsibilityID, *in.ReplacesMediaID).First(&replaced).Error; err != nil {
				return apperror.New(apperror.NotFound, "replacesMediaId", "replacement media not found")
			}
			var replacements int64
			if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND replaces_media_id=? AND status NOT IN ?", in.TenantID, in.ResponsibilityID, replaced.ID, []string{"ABORTED", "PURGED"}).Count(&replacements).Error; err != nil {
				return err
			}
			if replaced.RequirementKey != in.RequirementKey || replaced.Status != "READY" || replacements > 0 {
				return apperror.New(apperror.Conflict, "replacesMediaId", "media is not available for replacement")
			}
			if out.Status != "READY" {
				return apperror.New(apperror.InvalidState, "mediaId", "replacement media must be approved")
			}
			replacedMediaIDs = append(replacedMediaIDs, replaced.ID)
		}
		if selected.MaximumMedia > 0 {
			var active []database.MediaObject
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=? AND requirement_key=? AND id<>? AND status NOT IN ?", in.TenantID, in.ResponsibilityID, in.RequirementKey, out.ID, []string{"ABORTED", "PURGED"}).Order("created_at,id").Find(&active).Error; err != nil {
				return err
			}
			if len(active) >= selected.MaximumMedia && len(replacedMediaIDs) == 0 {
				if strings.HasPrefix(in.RequirementKey, "env:") {
					return apperror.New(apperror.InvalidInput, in.RequirementKey, "LIMIT_MEDIA_PER_ENVIRONMENT")
				}
				requiredSlots := len(active) - selected.MaximumMedia + 1
				for _, media := range active {
					if media.Status == "SCREENED" && len(replacedMediaIDs) < requiredSlots {
						replacedMediaIDs = append(replacedMediaIDs, media.ID)
					}
				}
				if len(replacedMediaIDs) != requiredSlots {
					return apperror.New(apperror.InvalidState, "requirementKey", "requirement media limit reached")
				}
				aborted := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND id IN ? AND status='SCREENED'", in.TenantID, in.ResponsibilityID, replacedMediaIDs).Update("status", "ABORTED")
				if aborted.Error != nil {
					return aborted.Error
				}
				if aborted.RowsAffected != int64(len(replacedMediaIDs)) {
					return apperror.New(apperror.Conflict, "requirementKey", "blocked media replacement changed concurrently")
				}
			}
		}
		if in.ReplacesMediaID != nil {
			aborted := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND id=? AND status='READY'", in.TenantID, in.ResponsibilityID, *in.ReplacesMediaID).Update("status", "ABORTED")
			if aborted.Error != nil {
				return aborted.Error
			}
			if aborted.RowsAffected != 1 {
				return apperror.New(apperror.Conflict, "replacesMediaId", "replacement media changed concurrently")
			}
			replacedID := *in.ReplacesMediaID
			out.ReplacesMediaID = &replacedID
		} else if len(replacedMediaIDs) > 0 {
			replacedID := replacedMediaIDs[len(replacedMediaIDs)-1]
			out.ReplacesMediaID = &replacedID
		}
		var existingFlags []string
		_ = json.Unmarshal(out.Flags, &existingFlags)
		out.RequirementKey, out.Description, out.CaptureSource, out.CapturedAt, out.DeviceContext = in.RequirementKey, strings.TrimSpace(in.Description), in.CaptureSource, &in.CapturedAt, device
		if strings.HasPrefix(in.RequirementKey, "env:") {
			out.Flags = mergeFlags(existingFlags, decision.Flags)
		} else {
			out.Flags = flags
		}
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
		var answerFlags []string
		if find.Error == nil {
			_ = json.Unmarshal(answer.MediaIDs, &mediaIDs)
			_ = json.Unmarshal(answer.Flags, &answerFlags)
		}
		if len(replacedMediaIDs) > 0 {
			replaced := make(map[identity.ID]struct{}, len(replacedMediaIDs))
			for _, id := range replacedMediaIDs {
				replaced[id] = struct{}{}
			}
			retained := mediaIDs[:0]
			for _, id := range mediaIDs {
				if _, removed := replaced[id]; !removed {
					retained = append(retained, id)
				}
			}
			mediaIDs = retained
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
		updates := map[string]any{"media_ids": encoded, "version": answer.Version + 1, "updated_at": s.now()}
		if strings.HasPrefix(in.RequirementKey, "env:") {
			keptFlags := make([]string, 0, len(answerFlags))
			for _, flag := range answerFlags {
				if !strings.HasPrefix(flag, "IMPOSSIBILITY:") {
					keptFlags = append(keptFlags, flag)
				}
			}
			updates["impossibility_reason"] = ""
			updates["flags"] = mergeFlags(keptFlags, decision.Flags)
		} else {
			// Preserve the v1 answer contract: metadata replaces its flag payload
			// and leaves impossibility_reason untouched.
			updates["flags"] = flags
		}
		return tx.Model(&answer).Updates(updates).Error
	})
	return out, err
}

func withoutRequiredGPS(payload json.RawMessage) (json.RawMessage, error) {
	var policy map[string]any
	if err := json.Unmarshal(payload, &policy); err != nil {
		return nil, err
	}
	if policy == nil {
		policy = map[string]any{}
	}
	policy["gpsRequired"] = false
	return json.Marshal(policy)
}

func metadataMatches(media database.MediaObject, in MetadataInput, reading *GPSReading, device json.RawMessage, capturedAtProvided bool) bool {
	if in.ReplacesMediaID != nil && (media.ReplacesMediaID == nil || *media.ReplacesMediaID != *in.ReplacesMediaID) {
		return false
	}
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
	return s.DeclareImpossibilityWithCode(ctx, tenantID, responsibilityID, requirementKey, "OTHER", reason, expectedVersion)
}

// DeclareImpossibilityWithCode records a controlled reason and optional detail.
func (s Service) DeclareImpossibilityWithCode(ctx context.Context, tenantID, responsibilityID identity.ID, requirementKey, reasonCode, reasonText string, expectedVersion int64) error {
	reasonCode = strings.ToUpper(strings.TrimSpace(reasonCode))
	v2Environment := strings.HasPrefix(requirementKey, "env:")
	reasonText = strings.TrimSpace(reasonText)
	if !v2Environment && reasonText == "" {
		return apperror.New(apperror.InvalidInput, "reason", "impossibility reason is required")
	}
	validReason := reasonCode == "LOCKED" || reasonCode == "NO_ACCESS" || reasonCode == "DOES_NOT_EXIST" || reasonCode == "UNSAFE" || reasonCode == "OTHER"
	if v2Environment && (!validReason || (reasonCode == "OTHER" && strings.TrimSpace(reasonText) == "")) {
		return apperror.New(apperror.InvalidInput, "reasonCode", "impossibility reason code is invalid")
	}
	if utf8.RuneCountInString(reasonText) > 2000 {
		if !v2Environment {
			return ValidateDescription(reasonText, true)
		}
		return apperror.New(apperror.InvalidInput, "reasonText", "impossibility reason is too long")
	}
	if reasonText != "" {
		if err := ValidateDescription(reasonText, true); err != nil {
			return err
		}
	}
	return s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", tenantID, responsibilityID).First(&draft).Error; err != nil {
			return apperror.New(apperror.InvalidState, "responsibility", "capture is unavailable")
		}
		requirements, _ := decodeRequirements(draft.Requirements)
		allowed := false
		for _, r := range requirements {
			if r.Key == requirementKey && r.ImpossibilityAllowed {
				allowed = true
			}
		}
		if !allowed {
			return apperror.New(apperror.InvalidInput, "requirementKey", "impossibility is not allowed")
		}
		if !v2Environment && reasonText == "" {
			return apperror.New(apperror.InvalidInput, "reason", "impossibility reason is required")
		}
		var answer database.RequirementAnswer
		err := tx.Where("tenant_id=? AND draft_id=? AND requirement_key=?", tenantID, draft.ID, requirementKey).First(&answer).Error
		if err == gorm.ErrRecordNotFound {
			storedReason := reasonText
			if v2Environment && storedReason == "" {
				storedReason = reasonCode
			}
			flags := json.RawMessage(`[]`)
			if v2Environment {
				flags, _ = json.Marshal([]string{"IMPOSSIBILITY:" + reasonCode})
			}
			return tx.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: tenantID, DraftID: draft.ID, RequirementKey: requirementKey, MediaIDs: json.RawMessage(`[]`), Flags: flags, ImpossibilityReason: storedReason, Version: 1, CreatedAt: s.now(), UpdatedAt: s.now()}).Error
		}
		if err != nil {
			return err
		}
		if expectedVersion > 0 && answer.Version != expectedVersion {
			return apperror.New(apperror.Conflict, "version", "stale requirement answer")
		}
		storedReason := reasonText
		if v2Environment && storedReason == "" {
			storedReason = reasonCode
		}
		if answer.ImpossibilityReason == storedReason {
			if !v2Environment {
				return nil
			}
			var flags []string
			_ = json.Unmarshal(answer.Flags, &flags)
			hasCurrentReason, hasStaleReason := slices.Contains(flags, "IMPOSSIBILITY:"+reasonCode), false
			for _, flag := range flags {
				if strings.HasPrefix(flag, "IMPOSSIBILITY:") && flag != "IMPOSSIBILITY:"+reasonCode {
					hasStaleReason = true
					break
				}
			}
			if hasCurrentReason && !hasStaleReason {
				return nil
			}
		}
		var flags []string
		if len(answer.Flags) > 0 {
			_ = json.Unmarshal(answer.Flags, &flags)
		}
		updates := map[string]any{"impossibility_reason": storedReason, "version": answer.Version + 1, "updated_at": s.now()}
		if v2Environment {
			flags = slices.DeleteFunc(flags, func(flag string) bool { return strings.HasPrefix(flag, "IMPOSSIBILITY:") })
			flags = appendUniqueFlag(flags, "IMPOSSIBILITY:"+reasonCode)
			encoded, _ := json.Marshal(flags)
			updates["flags"] = encoded
		}
		return tx.Model(&answer).Updates(updates).Error
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
		schemaVersion := 1
		draftRequirements, err := decodeRequirements(draft.Requirements)
		if err != nil {
			return err
		}
		for _, requirement := range draftRequirements {
			if strings.HasPrefix(requirement.Key, "env:") {
				schemaVersion = 2
				break
			}
		}
		if len(draftRequirements) == 0 && draft.TemplateVersionID != (identity.ID{}) {
			var templateVersion database.TemplateVersion
			if err := tx.Where("tenant_id=? AND id=?", tenantID, draft.TemplateVersionID).Take(&templateVersion).Error; err == nil {
				schemaVersion = templateVersion.SchemaVersion
			} else {
				return err
			}
		}
		if schemaVersion >= 2 && len(draftRequirements) == 0 {
			return apperror.New(apperror.InvalidState, "requirements", "NO_ENVIRONMENTS")
		}
		if draft.MediaKind == "VIDEO" {
			requirements, err := decodeRequirements(draft.Requirements)
			if err != nil {
				return err
			}
			if schemaVersion >= 2 {
				return s.submitEnvironmentVideo(ctx, tx, draft, requirements, &result, confirmIncomplete)
			}
			var acceptance database.ProcessingAcceptance
			if err := tx.Where("tenant_id=? AND responsibility_id=? AND video_processing AND ai_analysis AND gps_use", tenantID, responsibilityID).First(&acceptance).Error; err != nil {
				return apperror.New(apperror.InvalidState, "consent", "video processing acceptance is required")
			}
			var media []database.MediaObject
			if err := tx.Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO' AND requirement_key IN ? AND status IN ?", tenantID, responsibilityID, []string{"video:main", "video:supplement"}, []string{"RECEIVED", "VERIFIED", "SCREENED", "READY"}).Find(&media).Error; err != nil {
				return err
			}
			if len(media) != 1 || media[0].VideoAttemptID == nil {
				return apperror.New(apperror.InvalidState, "media", "one confirmed video receipt is required before submission")
			}
			var attempt database.VideoAttempt
			if err := tx.Where("tenant_id=? AND responsibility_id=? AND id=? AND status='RECEIVED'", tenantID, responsibilityID, *media[0].VideoAttemptID).First(&attempt).Error; err != nil {
				return apperror.New(apperror.InvalidState, "media", "one confirmed video receipt is required before submission")
			}
			payload, _ := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": media[0].ID, "attemptId": media[0].VideoAttemptID, "preparationStatus": "PENDING_VALIDATION", "complete": true, "requiresAttention": false})
			digest := sha256.Sum256(payload)
			now := s.now()
			var previousVersions int64
			if err := tx.Model(&database.SubmissionVersion{}).Where("tenant_id=? AND draft_id=?", tenantID, draft.ID).Count(&previousVersions).Error; err != nil {
				return err
			}
			result = database.SubmissionVersion{ID: identity.NewID(), TenantID: tenantID, DraftID: draft.ID, ResponsibilityID: responsibilityID, VersionNumber: int(previousVersions) + 1, Complete: true, RequiresAttention: false, Payload: payload, PayloadDigest: hex.EncodeToString(digest[:]), SubmittedAt: now}
			if err := tx.Create(&result).Error; err != nil {
				return err
			}
			if err := tx.Model(&draft).Updates(map[string]any{"status": "SUBMITTED", "version": draft.Version + 1, "updated_at": now}).Error; err != nil {
				return err
			}
			var job database.VideoPreparationJob
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id=?", tenantID, media[0].ID).Order("created_at DESC, id DESC").Take(&job).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				job = database.VideoPreparationJob{ID: identity.NewID(), TenantID: tenantID, MediaID: media[0].ID, SubmissionID: &result.ID, Status: "QUEUED", Phase: "RECEIVED", PreparationVersion: 1, Generation: 1, ProfileDigest: captureProfileDigest(media[0].RequirementKey), NextAttemptAt: now, PendingObjectKeys: []byte("[]"), CreatedAt: now}
				if err := tx.Create(&job).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else if job.SubmissionID == nil {
				if err := tx.Model(&job).Update("submission_id", result.ID).Error; err != nil {
					return err
				}
			} else if *job.SubmissionID != result.ID {
				return apperror.New(apperror.Conflict, "mediaId", "video preparation is linked to another submission")
			}
			if err := tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND responsibility_id=? AND revoked_at IS NULL", tenantID, responsibilityID).Update("revoked_at", now).Error; err != nil {
				return err
			}
			if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", tenantID, responsibilityID).Updates(map[string]any{"status": "COMPLETED", "revoked_at": now}).Error; err != nil {
				return err
			}
			eventID := identity.NewID()
			if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "capture.video_submitted.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: responsibilityID, CorrelationID: "capture-video-submit-" + result.ID.String(), Payload: map[string]any{"draftId": draft.ID, "responsibilityId": responsibilityID, "submissionId": result.ID, "mediaId": media[0].ID, "preparationStatus": "PENDING_VALIDATION"}}); err != nil {
				return err
			}
			if s.Finalizer != nil {
				return s.Finalizer.Finalize(ctx, tx, draft, result)
			}
			return nil
		}
		var acceptance int64
		if err := tx.Model(&database.ProcessingAcceptance{}).Where("tenant_id=? AND responsibility_id=? AND photo_processing AND ai_analysis AND gps_use", tenantID, responsibilityID).Count(&acceptance).Error; err != nil || acceptance != 1 {
			return apperror.New(apperror.InvalidState, "consent", "processing acceptance is required")
		}
		requirements, err := decodeRequirements(draft.Requirements)
		if err != nil {
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
		photoLimit := MaxActivePhotos
		if schemaVersion >= 2 {
			photoLimit = maxEnvironmentInspectionPhotoLimit(draftRequirements)
		}
		if len(media) > photoLimit {
			return apperror.New(apperror.InvalidState, "media", "capture media limit exceeded")
		}
		ready := make(map[identity.ID]database.MediaObject, len(media))
		perRequirementCount := make(map[string]int, len(requirements))
		for _, item := range media {
			if schemaVersion >= 2 && item.Status == "UPLOADING" && strings.HasPrefix(item.RequirementKey, "env:") {
				return apperror.New(apperror.InvalidState, item.RequirementKey, "UPLOADS_IN_PROGRESS")
			}
			if item.Status != "READY" {
				return apperror.New(apperror.InvalidState, item.ID.String(), "media verification and screening must complete before submission")
			}
			_, owned := byKey[item.RequirementKey]
			extra := strings.HasPrefix(item.RequirementKey, "extra:") && draft.Kind != "RECAPTURE"
			if !owned && !extra {
				return apperror.New(apperror.InvalidInput, item.ID.String(), "media must belong to an applicable requirement")
			}
			perRequirementCount[item.RequirementKey]++
			if owned && byKey[item.RequirementKey].MaximumMedia > 0 && perRequirementCount[item.RequirementKey] > byKey[item.RequirementKey].MaximumMedia {
				return apperror.New(apperror.InvalidInput, item.RequirementKey, "LIMIT_MEDIA_PER_ENVIRONMENT")
			}
			if err := ValidateDescription(item.Description, strings.HasPrefix(item.RequirementKey, "extra:")); err != nil {
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
		if schemaVersion >= 2 {
			keys := make([]string, 0, len(requirements))
			for _, requirement := range requirements {
				keys = append(keys, requirement.Key)
			}
			var uploading database.MediaObject
			if err := tx.Where("tenant_id=? AND responsibility_id=? AND status='UPLOADING' AND requirement_key IN ?", tenantID, responsibilityID, keys).Order("requirement_key").Take(&uploading).Error; err == nil {
				return apperror.New(apperror.InvalidState, uploading.RequirementKey, "UPLOADS_IN_PROGRESS")
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
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
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND id=?", tenantID, responsibilityID).First(&responsibility).Error; err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		eventID := identity.NewID()
		requirementKeys := make([]string, 0, len(requirements))
		if schemaVersion >= 2 {
			for _, requirement := range requirements {
				requirementKeys = append(requirementKeys, requirement.Key)
			}
		}
		outboxPayload := map[string]any{"draftId": draft.ID, "kind": draft.Kind, "responsibilityId": responsibilityID, "inspectionId": responsibility.InspectionID, "submissionId": result.ID, "complete": result.Complete, "requiresAttention": result.RequiresAttention}
		if schemaVersion >= 2 {
			outboxPayload["requirementKeys"] = requirementKeys
		}
		if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "capture.submitted.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: responsibilityID, CorrelationID: "capture-submit-" + result.ID.String(), Payload: outboxPayload}); err != nil {
			return err
		}
		if s.Finalizer != nil {
			return s.Finalizer.Finalize(ctx, tx, draft, result)
		}
		return nil
	})
	return result, err
}

func (s Service) submitEnvironmentVideo(ctx context.Context, tx *gorm.DB, draft database.CaptureDraft, requirements []Requirement, result *database.SubmissionVersion, confirmIncomplete bool) error {
	if len(requirements) == 0 {
		return apperror.New(apperror.InvalidState, "requirements", "NO_ENVIRONMENTS")
	}
	var acceptance database.ProcessingAcceptance
	if err := tx.Where("tenant_id=? AND responsibility_id=? AND video_processing AND ai_analysis AND gps_use", draft.TenantID, draft.ResponsibilityID).First(&acceptance).Error; err != nil {
		return apperror.New(apperror.InvalidState, "consent", "video processing acceptance is required")
	}
	keys := make([]string, 0, len(requirements))
	byKey := make(map[string]Requirement, len(requirements))
	for _, requirement := range requirements {
		keys = append(keys, requirement.Key)
		byKey[requirement.Key] = requirement
	}
	var uploading database.MediaObject
	if err := tx.Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO' AND requirement_key IN ? AND status='UPLOADING'", draft.TenantID, draft.ResponsibilityID, keys).Order("requirement_key").Take(&uploading).Error; err == nil {
		return apperror.New(apperror.InvalidState, uploading.RequirementKey, "UPLOADS_IN_PROGRESS")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var media []database.MediaObject
	if err := tx.Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO' AND requirement_key IN ? AND status IN ?", draft.TenantID, draft.ResponsibilityID, keys, []string{"RECEIVED", "VERIFIED", "SCREENED", "READY"}).Order("requirement_key,created_at,id").Find(&media).Error; err != nil {
		return err
	}
	var invalidStatus database.MediaObject
	if err := tx.Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO' AND requirement_key IN ? AND status NOT IN ?", draft.TenantID, draft.ResponsibilityID, keys, []string{"ABORTED", "PURGED", "RECEIVED", "VERIFIED", "SCREENED", "READY"}).Order("requirement_key").Take(&invalidStatus).Error; err == nil {
		return apperror.New(apperror.InvalidState, invalidStatus.RequirementKey, "video receipt is not complete")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	byMediaKey := make(map[string][]database.MediaObject, len(requirements))
	for _, item := range media {
		byMediaKey[item.RequirementKey] = append(byMediaKey[item.RequirementKey], item)
	}
	var answers []database.RequirementAnswer
	if err := tx.Where("tenant_id=? AND draft_id=?", draft.TenantID, draft.ID).Find(&answers).Error; err != nil {
		return err
	}
	answerByKey := make(map[string]database.RequirementAnswer, len(answers))
	for _, answer := range answers {
		answerByKey[answer.RequirementKey] = answer
	}
	evaluationAnswers := make([]Answer, 0, len(requirements))
	for _, requirement := range requirements {
		answer := answerByKey[requirement.Key]
		evaluationAnswers = append(evaluationAnswers, Answer{RequirementKey: requirement.Key, ReadyMedia: len(byMediaKey[requirement.Key]), Impossibility: answer.ImpossibilityReason})
	}
	completeness, evaluationErr := Evaluate(requirements, evaluationAnswers, confirmIncomplete)
	if evaluationErr != nil {
		if len(completeness.PendingKeys) > 0 {
			return PendingRequirementError{Keys: completeness.PendingKeys, Err: evaluationErr}
		}
		return evaluationErr
	}
	var clips []events.CaptureVideoClip
	for _, key := range keys {
		requirement := byKey[key]
		items := byMediaKey[key]
		if requirement.MaximumMedia > 0 && len(items) > requirement.MaximumMedia {
			return apperror.New(apperror.InvalidInput, key, "LIMIT_MEDIA_PER_ENVIRONMENT")
		}
		for _, item := range items {
			if item.VideoAttemptID == nil {
				return apperror.New(apperror.InvalidState, key, "video attempt is unavailable")
			}
			var attempt database.VideoAttempt
			if err := tx.Where("tenant_id=? AND responsibility_id=? AND id=? AND status='RECEIVED'", draft.TenantID, draft.ResponsibilityID, *item.VideoAttemptID).Take(&attempt).Error; err != nil {
				return apperror.New(apperror.InvalidState, key, "video receipt is not complete")
			}
			clips = append(clips, events.CaptureVideoClip{RequirementKey: key, MediaID: item.ID})
		}
	}
	var responsibility database.Responsibility
	if err := tx.Where("tenant_id=? AND id=?", draft.TenantID, draft.ResponsibilityID).First(&responsibility).Error; err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"mediaKind": "VIDEO", "clips": clips, "complete": completeness.Complete, "requiresAttention": completeness.RequiresAttention})
	digest := sha256.Sum256(payload)
	now := s.now()
	var previous int64
	if err := tx.Model(&database.SubmissionVersion{}).Where("tenant_id=? AND draft_id=?", draft.TenantID, draft.ID).Count(&previous).Error; err != nil {
		return err
	}
	*result = database.SubmissionVersion{ID: identity.NewID(), TenantID: draft.TenantID, DraftID: draft.ID, ResponsibilityID: draft.ResponsibilityID, VersionNumber: int(previous) + 1, Complete: completeness.Complete, RequiresAttention: completeness.RequiresAttention, Payload: payload, PayloadDigest: hex.EncodeToString(digest[:]), SubmittedAt: now}
	if err := tx.Create(result).Error; err != nil {
		return err
	}
	if err := tx.Model(&draft).Updates(map[string]any{"status": "SUBMITTED", "version": draft.Version + 1, "updated_at": now}).Error; err != nil {
		return err
	}
	for _, clip := range clips {
		var job database.VideoPreparationJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id=?", draft.TenantID, clip.MediaID).Order("created_at DESC,id DESC").Take(&job).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			key := clip.RequirementKey
			job = database.VideoPreparationJob{ID: identity.NewID(), TenantID: draft.TenantID, MediaID: clip.MediaID, SubmissionID: &result.ID, RequirementKey: &key, Status: "QUEUED", Phase: "RECEIVED", PreparationVersion: 1, Generation: 1, ProfileDigest: captureProfileDigest(key), NextAttemptAt: now, PendingObjectKeys: []byte("[]"), CreatedAt: now}
			if err := tx.Create(&job).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if job.SubmissionID == nil {
			if err := tx.Model(&job).Updates(map[string]any{"submission_id": result.ID, "requirement_key": clip.RequirementKey}).Error; err != nil {
				return err
			}
		} else if *job.SubmissionID != result.ID {
			return apperror.New(apperror.Conflict, clip.RequirementKey, "video preparation is linked to another submission")
		}
	}
	if err := tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND responsibility_id=? AND revoked_at IS NULL", draft.TenantID, draft.ResponsibilityID).Update("revoked_at", now).Error; err != nil {
		return err
	}
	if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", draft.TenantID, draft.ResponsibilityID).Updates(map[string]any{"status": "COMPLETED", "revoked_at": now}).Error; err != nil {
		return err
	}
	eventID := identity.NewID()
	if len(clips) == 0 {
		requirementKeys := make([]string, 0, len(requirements))
		for _, requirement := range requirements {
			requirementKeys = append(requirementKeys, requirement.Key)
		}
		payload := map[string]any{"draftId": draft.ID, "kind": draft.Kind, "responsibilityId": draft.ResponsibilityID, "inspectionId": responsibility.InspectionID, "submissionId": result.ID, "complete": result.Complete, "requiresAttention": result.RequiresAttention, "requirementKeys": requirementKeys}
		if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "capture.submitted.v1", SchemaVersion: 1, OccurredAt: now, TenantID: draft.TenantID, AggregateID: draft.ResponsibilityID, CorrelationID: "capture-submit-" + result.ID.String(), Payload: payload}); err != nil {
			return err
		}
	} else if err := messaging.AddOutbox(tx, events.Envelope[events.CaptureVideoSubmittedV2Payload]{ID: eventID, Type: "capture.video_submitted.v2", SchemaVersion: 2, OccurredAt: now, TenantID: draft.TenantID, AggregateID: draft.ResponsibilityID, CorrelationID: "capture-video-submit-" + result.ID.String(), Payload: events.CaptureVideoSubmittedV2Payload{InspectionID: responsibility.InspectionID, ResponsibilityID: draft.ResponsibilityID, SubmissionVersionID: result.ID, Clips: clips}}); err != nil {
		return err
	}
	if s.Finalizer != nil {
		return s.Finalizer.Finalize(ctx, tx, draft, *result)
	}
	return nil
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

func maxEnvironmentInspectionPhotoLimit(requirements []Requirement) int {
	maximum := catalog.MaxEnvironmentPhotos
	for _, requirement := range requirements {
		if strings.HasPrefix(requirement.Key, "env:") && requirement.EvidenceKind != "VIDEO" && requirement.MaximumMedia > maximum {
			maximum = requirement.MaximumMedia
		}
	}
	return catalog.MaxEnvironments * maximum
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
