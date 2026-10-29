package core

import (
	"context"
	"encoding/json"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxDecisions = 200

// Service owns the reviewed inventory lifecycle and its append-only audit trail.
type Service struct {
	DB             *gorm.DB
	Now            func() time.Time
	Within         func(context.Context, identity.ID, func(*gorm.DB) error) error
	Retention      RetentionTransition
	Counterparties CounterpartyInviter
}

// RetentionTransition recomputes source retention for every video source
// referenced by the given origin versions after an activation or supersession.
type RetentionTransition interface {
	TransitionVersions(context.Context, *gorm.DB, identity.ID, []identity.ID) error
}

// CounterpartyInviter creates invitations as part of an activation transaction.
type CounterpartyInviter interface {
	Invite(*gorm.DB, identity.ID, identity.ID, []identity.ID, identity.ID) error
}

// NoopRetentionTransition is used until the retention slice supplies its hook.
type NoopRetentionTransition struct{}

// TransitionVersions is a no-op retention hook.
func (NoopRetentionTransition) TransitionVersions(context.Context, *gorm.DB, identity.ID, []identity.ID) error {
	return nil
}

// NoopCounterpartyInviter is used until the counterparty slice supplies its hook.
type NoopCounterpartyInviter struct{}

// Invite is a no-op counterparty hook.
func (NoopCounterpartyInviter) Invite(*gorm.DB, identity.ID, identity.ID, []identity.ID, identity.ID) error {
	return nil
}

// DraftInput describes the origin and source used to create a draft.
type DraftInput struct {
	TenantID, AssetID, OriginID, SourceInspectionID, SourceVersionID identity.ID
	Source                                                           string
	ActorMembershipID                                                identity.ID
}

// Decision is one optimistic item review action.
type Decision struct {
	ItemID             identity.ID
	Action             string
	ExpectedVersion    int
	Patch              *ItemPatch
	Reason, ReasonText string
}

// DecideInput is a batch of reviewer decisions.
type DecideInput struct {
	TenantID, OriginVersionID, ActorMembershipID identity.ID
	Decisions                                    []Decision
}

// DecideResult describes updated items and environments with no remaining included items.
type DecideResult struct {
	Items             []database.InventoryItem
	EmptyEnvironments []identity.ID
}

// AddItemInput describes a manually added inventory item.
type AddItemInput struct {
	TenantID, OriginVersionID, EnvironmentID, ActorMembershipID identity.ID
	TypeCode, Name, Condition, Observation                      string
	Material, Color, Limitation                                 *string
	Quantity                                                    *int
	Evidence                                                    []EvidenceInput
}

// EvidenceInput identifies source evidence attached to an item.
type EvidenceInput struct {
	Kind    string
	MediaID identity.ID
	FrameID *identity.ID
	Region  json.RawMessage
}

// MergeItemsInput describes the items to merge and the result environment.
type MergeItemsInput struct {
	TenantID, OriginVersionID, ActorMembershipID identity.ID
	ItemIDs                                      []identity.ID
	ResultEnvironmentID                          *identity.ID
}

// SplitPart describes one item produced by a split.
type SplitPart struct {
	TypeCode, Name string
	EvidenceIDs    []identity.ID
	Condition      string
}

// SplitItemInput describes an item split into multiple parts.
type SplitItemInput struct {
	TenantID, OriginVersionID, ItemID, ActorMembershipID identity.ID
	ExpectedVersion                                      int
	Parts                                                []SplitPart
}

// UpsertEnvironmentInput describes creation or editing of an environment.
type UpsertEnvironmentInput struct {
	TenantID, OriginVersionID, EnvironmentID, ActorMembershipID identity.ID
	TypeCode, Name                                              string
	Expected                                                    bool
}

// RemoveEnvironmentInput describes environment removal.
type RemoveEnvironmentInput struct {
	TenantID, OriginVersionID, EnvironmentID, ActorMembershipID identity.ID
	ConfirmItemExclusion                                        bool
}

// ActivateInput describes baseline activation and optional counterparties.
type ActivateInput struct {
	TenantID, OriginVersionID, ActorMembershipID identity.ID
	Purpose                                      string
	ExpectedOriginVersion                        int64
	CounterpartyParticipantIDs                   []identity.ID
	// AllowEmptyItems permits an explicit inspection promotion to activate an environment-only origin.
	AllowEmptyItems bool
}

// ItemCounts are stored on activation audit events.
type ItemCounts struct {
	Included    int `json:"included"`
	Rejected    int `json:"rejected"`
	NotReviewed int `json:"notReviewed"`
}

// DraftView contains the ordered draft and its review progress.
type DraftView struct {
	Version          database.OriginVersion
	Environments     []EnvironmentView
	Decided, Pending int
	ReadOnly         bool
	NoticeCode       string
	Warnings         []string
}

// EnvironmentView pairs an environment with its ordered items.
type EnvironmentView struct {
	Environment database.InventoryEnvironment
	Items       []ItemView
}

// ItemView adds evidence availability to the persisted item.
type ItemView struct {
	Item     database.InventoryItem
	Evidence []EvidenceView
}

// EvidenceView reports the retained evidence's public availability.
type EvidenceView struct {
	Evidence              database.InventoryItemEvidence
	Availability          string
	RetentionAvailability string
}

// BaselineView is the internal or customer-safe inventory projection.
type BaselineView struct {
	HasInventory bool
	Version      *database.OriginVersion
	Environments []EnvironmentView
}

// TrailView returns append-only inventory decision rows.
type TrailView struct {
	Suggestions        []database.InventoryItem
	SuggestionEvidence map[identity.ID][]identity.ID
	Decisions          []database.InventoryDecision
	CounterpartyEvents []database.CounterpartyInvitation
}

var purposes = map[string]bool{"MOVE_IN": true, "CONSTRUCTION_HANDOVER": true, "POST_RENOVATION": true, "INVENTORY_UPDATE": true, "CONTESTATION_ADJUSTMENT": true}

func (s Service) editable(tx *gorm.DB, tenant, versionID identity.ID) (database.OriginVersion, error) {
	var v database.OriginVersion
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND has_inventory", tenant, versionID).First(&v).Error; err != nil {
		return v, apperror.New(apperror.NotFound, "originVersionId", "inventory draft not found")
	}
	if err := s.assertSourceValid(tx, tenant, v); err != nil {
		return v, err
	}
	if v.Status != "DRAFT" {
		return v, draftStateError(v)
	}
	if v.DraftState == nil || *v.DraftState != "READY" {
		return v, apperror.New(apperror.InvalidState, "originVersionId", "draft is not ready")
	}
	return v, nil
}
func draftStateError(v database.OriginVersion) error {
	if v.Status == "DISCARDED" {
		return apperror.New(apperror.InvalidState, "originVersionId", "DRAFT_DISCARDED")
	}
	return apperror.New(apperror.InvalidState, "originVersionId", "VERSION_NOT_EDITABLE")
}
func (s Service) assertSourceValid(tx *gorm.DB, tenant identity.ID, v database.OriginVersion) error {
	if v.SourceInspectionID == nil {
		return nil
	}
	var i database.Inspection
	if err := tx.Where("tenant_id=? AND id=?", tenant, *v.SourceInspectionID).First(&i).Error; err != nil {
		return err
	}
	if i.Status == "INVALIDATED" {
		return apperror.New(apperror.InvalidState, "originVersionId", "SOURCE_INVALIDATED")
	}
	return nil
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
func marshal(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func marshalRaw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	return marshal(v)
}
func stringPtr(v string) *string { return &v }
