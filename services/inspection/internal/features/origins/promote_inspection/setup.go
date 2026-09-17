// Package promote_inspection promotes selected completed onboarding evidence
// into an independently retained, immutable origin for later comparisons.
package promote_inspection

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	StatusPending    = "PENDING"
	StatusProcessing = "PROCESSING"
	StatusActive     = "ACTIVE"
	StatusFailed     = "FAILED"
)

type Command struct {
	TenantID             identity.ID
	InspectionID         identity.ID
	MediaIDs             []identity.ID
	ExpectedAssetVersion int64
	ClientMutationID     string
}

type Query struct {
	TenantID     identity.ID
	InspectionID identity.ID
}

type Media struct {
	ID          identity.ID
	Description string
	DisplayKey  string
}

type Result struct {
	Promotion database.OriginPromotion
	Eligible  []Media
}

type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

type manifest struct {
	Items []manifestItem `json:"items"`
}

type manifestItem struct {
	SourceID           identity.ID `json:"sourceId"`
	TargetID           identity.ID `json:"targetId"`
	OriginalKey        string      `json:"originalKey,omitempty"`
	OriginalHash       string      `json:"originalHash,omitempty"`
	DisplayKey         string      `json:"displayKey,omitempty"`
	DisplayHash        string      `json:"displayHash,omitempty"`
	DisplayContentType string      `json:"displayContentType,omitempty"`
}

// Setup registers the command and query boundary for the promotion workflow.
func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice origins/promote_inspection: missing dependency")
	}
	if err := d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return request(ctx, d, raw.(Command))
	}); err != nil {
		return err
	}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		return status(ctx, d, raw.(Query))
	})
}

func request(ctx context.Context, d Dependencies, c Command) (Result, error) {
	if c.TenantID == (identity.ID{}) || c.InspectionID == (identity.ID{}) || c.ExpectedAssetVersion <= 0 || strings.TrimSpace(c.ClientMutationID) == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "input", "inspection, asset version, and client mutation id are required")
	}
	c.MediaIDs = uniqueIDs(c.MediaIDs)
	if len(c.MediaIDs) == 0 {
		return Result{}, apperror.New(apperror.InvalidInput, "mediaIds", "select at least one photo")
	}
	var inspection database.Inspection
	var asset database.Asset
	if err := (tenanttx.Runner{DB: d.DB}).Within(ctx, c.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", c.TenantID, c.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND id=?", c.TenantID, inspection.AssetID).First(&asset).Error
	}); err != nil {
		return Result{}, apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
	}
	if _, err := d.Authorizer.Authorize(ctx, c.TenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.BusinessUnitID}, true); err != nil {
		return Result{}, err
	}
	meta, _ := requestctx.FromContext(ctx)
	selectedJSON, _ := json.Marshal(c.MediaIDs)
	var result Result
	now := time.Now().UTC()
	err := (tenanttx.Runner{DB: d.DB}).Within(ctx, c.TenantID, func(tx *gorm.DB) error {
		if err := eligibleInspection(tx, c.TenantID, inspection); err != nil {
			return err
		}
		eligible, err := selectedMedia(tx, c.TenantID, inspection.ID, c.MediaIDs)
		if err != nil {
			return err
		}
		var existing database.OriginPromotion
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND inspection_id=?", c.TenantID, c.InspectionID).First(&existing).Error
		if err == nil {
			if string(existing.SelectedMediaIDs) != string(selectedJSON) || existing.ExpectedAssetVersion != c.ExpectedAssetVersion {
				return apperror.New(apperror.Conflict, "mediaIds", "a different reference selection was already requested")
			}
			result = Result{Promotion: existing, Eligible: eligible}
			if existing.Status == StatusPending || existing.Status == StatusFailed {
				return enqueue(tx, c.TenantID, existing.ID, now)
			}
			return nil
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		m := manifest{Items: make([]manifestItem, 0, len(eligible))}
		for _, photo := range eligible {
			m.Items = append(m.Items, manifestItem{SourceID: photo.ID, TargetID: identity.NewID()})
		}
		encodedManifest, _ := json.Marshal(m)
		promotion := database.OriginPromotion{ID: identity.NewID(), TenantID: c.TenantID, InspectionID: inspection.ID, AssetID: inspection.AssetID, TemplateID: identity.ID{}, ExpectedAssetVersion: c.ExpectedAssetVersion, SelectedMediaIDs: selectedJSON, Manifest: encodedManifest, Status: StatusPending, RequestedBy: meta.Principal.IdentityID, RequestedAt: now, UpdatedAt: now}
		result = Result{Promotion: promotion, Eligible: eligible}
		if err := tx.Create(&promotion).Error; err != nil {
			return err
		}
		if err := audit(tx, c.TenantID, meta.Principal.IdentityID, "origin.promotion_requested", promotion.ID, "SUCCESS", ""); err != nil {
			return err
		}
		return enqueue(tx, c.TenantID, promotion.ID, now)
	})
	return result, err
}

func status(ctx context.Context, d Dependencies, q Query) (Result, error) {
	var result Result
	var inspection database.Inspection
	err := (tenanttx.Runner{DB: d.DB}).Within(ctx, q.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", q.TenantID, q.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		var asset database.Asset
		if err := tx.Where("tenant_id=? AND id=?", q.TenantID, inspection.AssetID).First(&asset).Error; err != nil {
			return err
		}
		if _, err := d.Authorizer.Authorize(ctx, q.TenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.BusinessUnitID}, false); err != nil {
			return err
		}
		if err := eligibleInspection(tx, q.TenantID, inspection); err != nil {
			return err
		}
		result.Promotion = database.OriginPromotion{InspectionID: inspection.ID, Status: StatusPending}
		_ = tx.Where("tenant_id=? AND inspection_id=?", q.TenantID, q.InspectionID).First(&result.Promotion).Error
		photos, err := availableMedia(tx, q.TenantID, inspection.ID)
		result.Eligible = photos
		return err
	})
	if err != nil {
		return Result{}, apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
	}
	return result, nil
}

func eligibleInspection(tx *gorm.DB, tenantID identity.ID, inspection database.Inspection) error {
	if inspection.Status != "COMPLETED" {
		return apperror.New(apperror.InvalidState, "inspectionId", "completed onboarding inspection required")
	}
	var request database.OnboardingRequest
	if err := tx.Where("tenant_id=? AND inspection_id=?", tenantID, inspection.ID).First(&request).Error; err != nil {
		return apperror.New(apperror.InvalidState, "inspectionId", "only the first onboarding inspection can become a reference")
	}
	if request.OriginVersionID != nil {
		return apperror.New(apperror.InvalidState, "inspectionId", "inspection already started with a reference")
	}
	return nil
}

func availableMedia(tx *gorm.DB, tenantID, inspectionID identity.ID) ([]Media, error) {
	var rows []database.MediaObject
	err := tx.Table("media.media_objects m").
		Select("m.*").
		Joins("JOIN inspections.responsibilities r ON r.id=m.responsibility_id AND r.tenant_id=m.tenant_id").
		Joins("JOIN capture.capture_drafts d ON d.responsibility_id=m.responsibility_id AND d.tenant_id=m.tenant_id AND d.status='SUBMITTED'").
		Where("m.tenant_id=? AND r.inspection_id=? AND m.status='READY' AND trim(coalesce(m.description,'')) <> ''", tenantID, inspectionID).
		Order("m.created_at,m.id").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]Media, 0, len(rows))
	for _, row := range rows {
		var derivative database.MediaDerivative
		if err := tx.Where("tenant_id=? AND media_id=? AND kind='DISPLAY'", tenantID, row.ID).First(&derivative).Error; err != nil {
			continue
		}
		result = append(result, Media{ID: row.ID, Description: row.Description, DisplayKey: derivative.ObjectKey})
	}
	return result, nil
}

func selectedMedia(tx *gorm.DB, tenantID, inspectionID identity.ID, ids []identity.ID) ([]Media, error) {
	available, err := availableMedia(tx, tenantID, inspectionID)
	if err != nil {
		return nil, err
	}
	allowed := make(map[identity.ID]Media, len(available))
	for _, item := range available {
		allowed[item.ID] = item
	}
	result := make([]Media, 0, len(ids))
	for _, id := range ids {
		item, ok := allowed[id]
		if !ok {
			return nil, apperror.New(apperror.InvalidInput, "mediaIds", "each selected photo must be ready evidence from this inspection with a description")
		}
		result = append(result, item)
	}
	return result, nil
}

func enqueue(tx *gorm.DB, tenantID, promotionID identity.ID, now time.Time) error {
	eventID := identity.NewID()
	return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "origin.promotion_requested.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: promotionID, CorrelationID: "origin-promotion:" + promotionID.String(), Payload: map[string]any{"promotionId": promotionID}})
}

func audit(tx *gorm.DB, tenantID, actorID identity.ID, action string, target identity.ID, outcome, reason string) error {
	return tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: tenantID, ActorID: actorID, Action: action, TargetType: "ORIGIN_PROMOTION", TargetID: target.String(), Outcome: outcome, Reason: reason, CorrelationID: "origin-promotion:" + target.String(), OccurredAt: time.Now().UTC()}).Error
}

func uniqueIDs(ids []identity.ID) []identity.ID {
	seen := make(map[identity.ID]struct{}, len(ids))
	result := make([]identity.ID, 0, len(ids))
	for _, id := range ids {
		if id != (identity.ID{}) {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				result = append(result, id)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}
