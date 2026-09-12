package database

import (
	"encoding/json"
	"time"

	"inspection/libs/identity"
)

// BootstrapRequest durably returns the original result for safe retries.
type BootstrapRequest struct {
	ID             identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID       identity.ID     `gorm:"type:uuid;not null;index:idx_bootstrap_tenant"`
	SubjectKey     string          `gorm:"size:1000;not null;uniqueIndex:idx_bootstrap_request,priority:1"`
	IdempotencyKey string          `gorm:"size:200;not null;uniqueIndex:idx_bootstrap_request,priority:2"`
	PayloadDigest  string          `gorm:"size:64;not null"`
	Result         json.RawMessage `gorm:"type:jsonb;not null"`
	CreatedAt      time.Time
}

func (BootstrapRequest) TableName() string { return "tenancy.bootstrap_requests" }

// Tenant is the root tenant record. TenantID mirrors ID so every protected
// table, including the tenant root, can use the same RLS policy.
type Tenant struct {
	ID              identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_tenants_tenant_id"`
	Name            string      `gorm:"size:200;not null"`
	Language        string      `gorm:"size:16;not null"`
	DefaultTimezone string      `gorm:"size:64;not null"`
	Status          string      `gorm:"size:16;not null;index"`
	Version         int64       `gorm:"not null;default:1"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (Tenant) TableName() string { return "tenancy.tenants" }

// BusinessUnit belongs permanently to one tenant.
type BusinessUnit struct {
	ID       identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_business_units_tenant_code,priority:1;index:idx_business_units_tenant_status,priority:1"`
	Code     string      `gorm:"size:200;not null;uniqueIndex:idx_business_units_tenant_code,priority:2"`
	Name     string      `gorm:"size:200;not null"`
	Status   string      `gorm:"size:16;not null;index:idx_business_units_tenant_status,priority:2"`
	Version  int64       `gorm:"not null;default:1"`
	// IdempotencyKey binds a client mutation retry to exactly one unit.
	IdempotencyKey string `gorm:"size:200;not null;default:''"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (BusinessUnit) TableName() string { return "tenancy.business_units" }

// Membership maps one OIDC identity to local, immediately revocable access.
type Membership struct {
	ID         identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID   identity.ID `gorm:"type:uuid;not null;index:idx_memberships_tenant_identity,priority:1"`
	IdentityID identity.ID `gorm:"type:uuid;not null;index:idx_memberships_tenant_identity,priority:2"`
	Issuer     string      `gorm:"size:500;not null;index:idx_memberships_oidc,priority:1"`
	Subject    string      `gorm:"size:500;not null;index:idx_memberships_oidc,priority:2"`
	Role       string      `gorm:"size:32;not null"`
	Status     string      `gorm:"size:16;not null"`
	Version    int64       `gorm:"not null;default:1"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (Membership) TableName() string { return "access.memberships" }

// ProductEntitlement grants independent access to Admin or Dashboard.
type ProductEntitlement struct {
	ID           identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID     identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_entitlements_unique,priority:1"`
	MembershipID identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_entitlements_unique,priority:2;index"`
	Product      string      `gorm:"size:16;not null;uniqueIndex:idx_entitlements_unique,priority:3"`
	CreatedAt    time.Time
}

func (ProductEntitlement) TableName() string { return "access.product_entitlements" }

// ResourceScope stores an explicit hierarchical assignment.
type ResourceScope struct {
	ID           identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID     identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_scopes_unique,priority:1;index:idx_scopes_tenant_member,priority:1"`
	MembershipID identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_scopes_unique,priority:2;index:idx_scopes_tenant_member,priority:2"`
	Kind         string      `gorm:"size:32;not null;uniqueIndex:idx_scopes_unique,priority:3"`
	ResourceID   identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_scopes_unique,priority:4"`
	CreatedAt    time.Time
}

func (ResourceScope) TableName() string { return "access.resource_scopes" }

// AuditEvent is append-only and retains target text after business deletion.
type AuditEvent struct {
	ID            identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID      identity.ID `gorm:"type:uuid;not null;index:idx_audit_tenant_cursor,priority:1"`
	ActorID       identity.ID `gorm:"type:uuid;not null;index"`
	Action        string      `gorm:"size:200;not null;index"`
	TargetType    string      `gorm:"size:100;not null"`
	TargetID      string      `gorm:"size:200;not null"`
	Outcome       string      `gorm:"size:32;not null"`
	Reason        string      `gorm:"size:2000"`
	CorrelationID string      `gorm:"size:200;not null;index"`
	OccurredAt    time.Time   `gorm:"not null;index:idx_audit_tenant_cursor,priority:2"`
}

func (AuditEvent) TableName() string { return "audit.events" }

// OutboxIntent is the durable handoff contract for later messaging slices.
type OutboxIntent struct {
	ID            identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID      identity.ID `gorm:"type:uuid;not null;index:idx_outbox_pending,priority:1"`
	Type          string      `gorm:"size:200;not null"`
	SchemaVersion int         `gorm:"not null"`
	Payload       []byte      `gorm:"type:jsonb;not null"`
	CorrelationID string      `gorm:"size:200;not null"`
	CausationID   string      `gorm:"size:200"`
	Status        string      `gorm:"size:20;not null;index:idx_outbox_pending,priority:2"`
	Attempts      int         `gorm:"not null;default:0"`
	NextAttemptAt time.Time   `gorm:"not null;index:idx_outbox_pending,priority:3"`
	ClaimedAt     *time.Time  `gorm:"index"`
	LastError     string      `gorm:"size:200"`
	PublishedAt   *time.Time
	CreatedAt     time.Time
}

func (OutboxIntent) TableName() string { return "messaging.outbox" }

type InboxReceipt struct {
	ID          identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID    identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_inbox_delivery,priority:1"`
	Consumer    string      `gorm:"size:200;not null;uniqueIndex:idx_inbox_delivery,priority:2"`
	EventID     identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_inbox_delivery,priority:3"`
	Generation  int         `gorm:"not null;default:0;uniqueIndex:idx_inbox_delivery,priority:4"`
	ProcessedAt time.Time   `gorm:"not null"`
}

func (InboxReceipt) TableName() string { return "messaging.inbox" }

// Participant is a tenant-owned external person. Contacts and selected
// delivery channels are modeled independently so no preferred channel exists.
type Participant struct {
	ID             identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID       identity.ID `gorm:"type:uuid;not null;index:idx_participants_tenant_name,priority:1;uniqueIndex:idx_participant_idempotency,priority:1"`
	BusinessUnitID identity.ID `gorm:"type:uuid;not null;index:idx_participants_tenant_unit,priority:2"`
	Name           string      `gorm:"size:200;not null;index:idx_participants_tenant_name,priority:2"`
	SegmentRole    string      `gorm:"size:100;not null"`
	Status         string      `gorm:"size:16;not null;index"`
	Version        int64       `gorm:"not null;default:1"`
	IdempotencyKey string      `gorm:"size:200;not null;uniqueIndex:idx_participant_idempotency,priority:2"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (Participant) TableName() string { return "participants.participants" }

type ParticipantContact struct {
	ID            identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID      identity.ID `gorm:"type:uuid;not null;index:idx_contacts_tenant_participant,priority:1;uniqueIndex:idx_contact_normalized,priority:1"`
	ParticipantID identity.ID `gorm:"type:uuid;not null;index:idx_contacts_tenant_participant,priority:2;uniqueIndex:idx_contact_normalized,priority:2"`
	Channel       string      `gorm:"size:16;not null;uniqueIndex:idx_contact_normalized,priority:3"`
	Value         string      `gorm:"size:320;not null"`
	Normalized    string      `gorm:"size:320;not null;uniqueIndex:idx_contact_normalized,priority:4"`
	Active        bool        `gorm:"not null;default:true"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (ParticipantContact) TableName() string { return "participants.contacts" }

type ContactVerification struct {
	ID             identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID       identity.ID `gorm:"type:uuid;not null;index:idx_verifications_tenant_contact,priority:1;uniqueIndex:idx_verification_idempotency,priority:1"`
	ContactID      identity.ID `gorm:"type:uuid;not null;index:idx_verifications_tenant_contact,priority:2"`
	IdempotencyKey string      `gorm:"size:200;uniqueIndex:idx_verification_idempotency,priority:2"`
	Status         string      `gorm:"size:16;not null"`
	VerifiedAt     *time.Time
	CreatedAt      time.Time
}

func (ContactVerification) TableName() string { return "participants.contact_verifications" }

type ChannelSelection struct {
	ID            identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID      identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_channel_selection,priority:1"`
	ParticipantID identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_channel_selection,priority:2"`
	ContactID     identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_channel_selection,priority:3"`
	CreatedAt     time.Time
}

func (ChannelSelection) TableName() string { return "participants.channel_selections" }

type SegmentDefinition struct {
	ID              identity.ID  `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_segment_key,priority:1"`
	Key             string       `gorm:"size:100;not null;uniqueIndex:idx_segment_key,priority:2"`
	Name            string       `gorm:"size:200;not null"`
	ActiveVersionID *identity.ID `gorm:"type:uuid"`
	Version         int64        `gorm:"not null;default:1"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (SegmentDefinition) TableName() string { return "segments.definitions" }

type SegmentDefinitionVersion struct {
	ID              identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_segment_version,priority:1;uniqueIndex:idx_segment_digest,priority:1;uniqueIndex:idx_segment_publish_idempotency,priority:1"`
	DefinitionID    identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_segment_version,priority:2;uniqueIndex:idx_segment_digest,priority:2"`
	VersionNumber   int             `gorm:"not null;uniqueIndex:idx_segment_version,priority:3"`
	SchemaVersion   int             `gorm:"not null"`
	SchemaJSON      json.RawMessage `gorm:"type:jsonb;not null"`
	UISchemaJSON    json.RawMessage `gorm:"type:jsonb;not null"`
	CanonicalDigest string          `gorm:"size:64;not null;uniqueIndex:idx_segment_digest,priority:3"`
	Status          string          `gorm:"size:16;not null"`
	IdempotencyKey  string          `gorm:"size:200;not null;uniqueIndex:idx_segment_publish_idempotency,priority:2"`
	PublishedAt     time.Time       `gorm:"not null"`
	CreatedBy       identity.ID     `gorm:"type:uuid;not null"`
}

func (SegmentDefinitionVersion) TableName() string { return "segments.definition_versions" }

type Template struct {
	ID               identity.ID  `gorm:"type:uuid;primaryKey"`
	TenantID         identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_template_key,priority:1"`
	Key              string       `gorm:"size:100;not null;uniqueIndex:idx_template_key,priority:2"`
	Name             string       `gorm:"size:200;not null"`
	SegmentVersionID identity.ID  `gorm:"type:uuid;not null;index"`
	ActiveVersionID  *identity.ID `gorm:"type:uuid"`
	Version          int64        `gorm:"not null;default:1"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (Template) TableName() string { return "templates.templates" }

type TemplateVersion struct {
	ID              identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_template_version,priority:1;uniqueIndex:idx_template_publish_idempotency,priority:1"`
	TemplateID      identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_template_version,priority:2"`
	VersionNumber   int             `gorm:"not null;uniqueIndex:idx_template_version,priority:3"`
	SchemaVersion   int             `gorm:"not null"`
	DefinitionJSON  json.RawMessage `gorm:"type:jsonb;not null"`
	CanonicalDigest string          `gorm:"size:64;not null"`
	Status          string          `gorm:"size:16;not null;index"`
	IdempotencyKey  string          `gorm:"size:200;not null;uniqueIndex:idx_template_publish_idempotency,priority:2"`
	PublishedAt     time.Time       `gorm:"not null"`
	CreatedBy       identity.ID     `gorm:"type:uuid;not null"`
}

func (TemplateVersion) TableName() string { return "templates.template_versions" }

type AnalysisProfile struct {
	ID              identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_profile_key,priority:1"`
	Key             string      `gorm:"size:100;not null;uniqueIndex:idx_profile_key,priority:2"`
	ActiveVersionID identity.ID `gorm:"type:uuid;not null"`
	Version         int64       `gorm:"not null;default:1"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (AnalysisProfile) TableName() string { return "templates.analysis_profiles" }

type AnalysisProfileVersion struct {
	ID              identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_profile_key_version,priority:1;uniqueIndex:idx_profile_publish_idempotency,priority:1"`
	ProfileID       identity.ID     `gorm:"type:uuid;not null;index"`
	Key             string          `gorm:"size:100;not null;uniqueIndex:idx_profile_key_version,priority:2"`
	VersionNumber   int             `gorm:"not null;uniqueIndex:idx_profile_key_version,priority:3"`
	SchemaVersion   int             `gorm:"not null"`
	DefinitionJSON  json.RawMessage `gorm:"type:jsonb;not null"`
	CanonicalDigest string          `gorm:"size:64;not null"`
	Status          string          `gorm:"size:16;not null"`
	IdempotencyKey  string          `gorm:"size:200;not null;uniqueIndex:idx_profile_publish_idempotency,priority:2"`
	PublishedAt     time.Time       `gorm:"not null"`
	CreatedBy       identity.ID     `gorm:"type:uuid;not null"`
}

func (AnalysisProfileVersion) TableName() string { return "templates.analysis_profile_versions" }

// OnboardingSession stores the public journey state. The locator digest is
// used before tenant provisioning; after provisioning tenant_id scopes access.
type OnboardingSession struct {
	ID                   identity.ID  `gorm:"type:uuid;primaryKey"`
	TenantID             *identity.ID `gorm:"type:uuid;index:idx_onboarding_sessions_tenant"`
	Email                string       `gorm:"size:320;not null"`
	OwnerName            string       `gorm:"size:200;not null"`
	OwnerSubject         string       `gorm:"size:500"`
	SessionLocatorDigest []byte       `gorm:"type:bytea;size:32;not null;uniqueIndex"`
	State                string       `gorm:"size:32;not null;index"`
	CurrentStep          string       `gorm:"size:64;not null"`
	Version              int64        `gorm:"not null;default:1"`
	ExpiresAt            time.Time    `gorm:"not null;index"`
	CSRFDigest           []byte       `gorm:"type:bytea;size:32"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func (OnboardingSession) TableName() string { return "onboarding.sessions" }

// OnboardingOTPChallenge separates onboarding and activation challenge data.
type OnboardingOTPChallenge struct {
	ID         identity.ID  `gorm:"type:uuid;primaryKey"`
	TenantID   *identity.ID `gorm:"type:uuid;index:idx_onboarding_otp_tenant"`
	SessionID  identity.ID  `gorm:"type:uuid;not null;index:idx_onboarding_otp_session"`
	Purpose    string       `gorm:"size:32;not null;index"`
	CodeHMAC   []byte       `gorm:"type:bytea;size:32;not null"`
	Attempts   int          `gorm:"not null;default:0"`
	ExpiresAt  time.Time    `gorm:"not null;index"`
	VerifiedAt *time.Time
	CreatedAt  time.Time
}

func (OnboardingOTPChallenge) TableName() string { return "onboarding.otp_challenges" }

// OnboardingStepRecord is an immutable, versioned checkpoint payload.
type OnboardingStepRecord struct {
	ID            identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID      *identity.ID    `gorm:"type:uuid;index:idx_onboarding_steps_tenant"`
	SessionID     identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_onboarding_step_version,priority:1"`
	Step          string          `gorm:"size:64;not null;uniqueIndex:idx_onboarding_step_version,priority:2"`
	Payload       json.RawMessage `gorm:"type:jsonb;not null"`
	PayloadDigest string          `gorm:"size:64;not null"`
	Version       int64           `gorm:"not null;uniqueIndex:idx_onboarding_step_version,priority:3"`
	CompletedAt   time.Time       `gorm:"not null"`
}

func (OnboardingStepRecord) TableName() string { return "onboarding.step_records" }

// OnboardingRequest is the durable projection for the first inspection.
type OnboardingRequest struct {
	ID              identity.ID  `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID  `gorm:"type:uuid;not null;index:idx_onboarding_requests_tenant"`
	SessionID       identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_onboarding_request_idempotency,priority:1"`
	AssetID         *identity.ID `gorm:"type:uuid;index"`
	ParticipantID   *identity.ID `gorm:"type:uuid;index"`
	OriginVersionID *identity.ID `gorm:"type:uuid;index"`
	TemplateID      *identity.ID `gorm:"type:uuid;index"`
	Status          string       `gorm:"size:32;not null;index"`
	IdempotencyKey  string       `gorm:"size:200;not null;uniqueIndex:idx_onboarding_request_idempotency,priority:2"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (OnboardingRequest) TableName() string { return "onboarding.requests" }

// OnboardingActivation tracks the separate owner Admin activation challenge.
type OnboardingActivation struct {
	TenantID       identity.ID `gorm:"type:uuid;primaryKey"`
	IdentityID     identity.ID `gorm:"type:uuid;not null;uniqueIndex"`
	Purpose        string      `gorm:"size:32;not null"`
	Status         string      `gorm:"size:32;not null;index"`
	ActivatedAt    *time.Time
	IdempotencyKey string `gorm:"size:200;not null;uniqueIndex"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (OnboardingActivation) TableName() string { return "onboarding.activation" }

type Asset struct {
	ID               identity.ID  `gorm:"type:uuid;primaryKey"`
	TenantID         identity.ID  `gorm:"type:uuid;not null;index:idx_assets_tenant_unit_status,priority:1;uniqueIndex:idx_asset_idempotency,priority:1"`
	BusinessUnitID   identity.ID  `gorm:"type:uuid;not null;index:idx_assets_tenant_unit_status,priority:2"`
	SegmentVersionID identity.ID  `gorm:"type:uuid;not null;index"`
	TemplateID       *identity.ID `gorm:"type:uuid"`
	Name             string       `gorm:"size:200;not null;index"`
	ExternalKey      string       `gorm:"size:200;not null"`
	Address          string       `gorm:"size:2000;not null"`
	LatitudeE6       *int32
	LongitudeE6      *int32
	GeofenceMeters   int             `gorm:"not null;default:150"`
	PolicyOverrides  json.RawMessage `gorm:"type:jsonb;not null"`
	Status           string          `gorm:"size:16;not null;index:idx_assets_tenant_unit_status,priority:3"`
	Version          int64           `gorm:"not null;default:1"`
	IdempotencyKey   string          `gorm:"size:200;not null;uniqueIndex:idx_asset_idempotency,priority:2"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (Asset) TableName() string { return "assets.assets" }

type AssetAttributeVersion struct {
	ID               identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID         identity.ID     `gorm:"type:uuid;not null;index:idx_asset_attributes_tenant_asset,priority:1"`
	AssetID          identity.ID     `gorm:"type:uuid;not null;index:idx_asset_attributes_tenant_asset,priority:2"`
	SegmentVersionID identity.ID     `gorm:"type:uuid;not null"`
	VersionNumber    int64           `gorm:"not null"`
	AttributesJSON   json.RawMessage `gorm:"type:jsonb;not null"`
	CanonicalDigest  string          `gorm:"size:64;not null"`
	CreatedAt        time.Time
}

func (AssetAttributeVersion) TableName() string { return "assets.asset_attribute_versions" }

type AssetAssignment struct {
	ID            identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID      identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_asset_assignment,priority:1"`
	AssetID       identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_asset_assignment,priority:2"`
	ParticipantID identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_asset_assignment,priority:3"`
	Role          string      `gorm:"size:100;not null;uniqueIndex:idx_asset_assignment,priority:4"`
	Active        bool        `gorm:"not null;default:true"`
	CreatedAt     time.Time
}

func (AssetAssignment) TableName() string { return "assets.asset_assignments" }

type Invitation struct {
	ID                identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID          identity.ID     `gorm:"type:uuid;not null;index:idx_invitation_tenant_responsibility,priority:1"`
	ResponsibilityID  identity.ID     `gorm:"type:uuid;not null;index:idx_invitation_tenant_responsibility,priority:2"`
	TokenHash         []byte          `gorm:"type:bytea;size:32;not null;uniqueIndex"`
	PreviousTokenHash []byte          `gorm:"type:bytea;size:32;index"`
	DeliveryIntents   json.RawMessage `gorm:"type:jsonb;not null"`
	Status            string          `gorm:"size:20;not null;index"`
	ExpiresAt         time.Time       `gorm:"not null;index"`
	RevokedAt         *time.Time
	CreatedAt         time.Time
}

func (Invitation) TableName() string { return "invitations.invitations" }

type OTPChallenge struct {
	ID           identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID     identity.ID `gorm:"type:uuid;not null;index:idx_otp_invitation,priority:1"`
	InvitationID identity.ID `gorm:"type:uuid;not null;index:idx_otp_invitation,priority:2"`
	CodeHMAC     []byte      `gorm:"type:bytea;size:32;not null"`
	Attempts     int         `gorm:"not null;default:0"`
	SendCount    int         `gorm:"not null;default:1"`
	LastSentAt   time.Time   `gorm:"not null"`
	ExpiresAt    time.Time   `gorm:"not null;index"`
	VerifiedAt   *time.Time
	CreatedAt    time.Time
}

func (OTPChallenge) TableName() string { return "invitations.otp_challenges" }

type ExternalSession struct {
	ID               identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID         identity.ID `gorm:"type:uuid;not null;index:idx_session_responsibility,priority:1"`
	InvitationID     identity.ID `gorm:"type:uuid;not null;index"`
	ResponsibilityID identity.ID `gorm:"type:uuid;not null;index:idx_session_responsibility,priority:2"`
	SessionDigest    []byte      `gorm:"type:bytea;size:32;not null;uniqueIndex"`
	CSRFDigest       []byte      `gorm:"type:bytea;size:32;not null"`
	ExpiresAt        time.Time   `gorm:"not null;index"`
	RevokedAt        *time.Time
	CreatedAt        time.Time
}

func (ExternalSession) TableName() string { return "invitations.external_sessions" }

type ProcessingAcceptance struct {
	ID                identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID          identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_processing_acceptance,priority:1"`
	ResponsibilityID  identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_processing_acceptance,priority:2"`
	DisclosureVersion string      `gorm:"size:100;not null"`
	PhotoProcessing   bool        `gorm:"not null"`
	AIAnalysis        bool        `gorm:"not null"`
	GPSUse            bool        `gorm:"not null"`
	AcceptedAt        time.Time   `gorm:"not null"`
}

func (ProcessingAcceptance) TableName() string { return "invitations.processing_acceptances" }

type Origin struct {
	ID              identity.ID  `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_origin_context,priority:1"`
	AssetID         identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_origin_context,priority:2"`
	TemplateID      identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_origin_context,priority:3"`
	ActiveVersionID *identity.ID `gorm:"type:uuid"`
	Version         int64        `gorm:"not null;default:1"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (Origin) TableName() string { return "origins.origins" }

type OriginVersion struct {
	ID               identity.ID  `gorm:"type:uuid;primaryKey"`
	TenantID         identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_origin_version,priority:1;uniqueIndex:idx_origin_invite_idempotency,priority:1"`
	OriginID         identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_origin_version,priority:2"`
	VersionNumber    int          `gorm:"not null;uniqueIndex:idx_origin_version,priority:3"`
	ResponsibilityID identity.ID  `gorm:"type:uuid;not null;uniqueIndex"`
	SupersedesID     *identity.ID `gorm:"type:uuid"`
	Status           string       `gorm:"size:20;not null;index"`
	IdempotencyKey   string       `gorm:"size:200;not null;uniqueIndex:idx_origin_invite_idempotency,priority:2"`
	CreatedAt        time.Time
	SubmittedAt      *time.Time
	ActivatedAt      *time.Time
	InvalidatedAt    *time.Time
}

func (OriginVersion) TableName() string { return "origins.origin_versions" }

type OriginEvidence struct {
	ID              identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID `gorm:"type:uuid;not null;index:idx_origin_evidence,priority:1"`
	OriginVersionID identity.ID `gorm:"type:uuid;not null;index:idx_origin_evidence,priority:2"`
	MediaID         identity.ID `gorm:"type:uuid;not null;uniqueIndex"`
	Category        string      `gorm:"size:200;not null"`
	Description     string      `gorm:"size:2000;not null"`
	CreatedAt       time.Time
}

func (OriginEvidence) TableName() string { return "origins.origin_evidence" }

type MediaObject struct {
	ID               identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID         identity.ID `gorm:"type:uuid;not null;index:idx_media_tenant_responsibility,priority:1;uniqueIndex:idx_media_create_idempotency,priority:1"`
	ResponsibilityID identity.ID `gorm:"type:uuid;not null;index:idx_media_tenant_responsibility,priority:2"`
	ObjectKey        string      `gorm:"size:1000;not null;uniqueIndex"`
	ContentType      string      `gorm:"size:100;not null"`
	SHA256           string      `gorm:"size:64;not null"`
	SizeBytes        int64       `gorm:"not null"`
	Status           string      `gorm:"size:20;not null;index"`
	IdempotencyKey   string      `gorm:"size:200;uniqueIndex:idx_media_create_idempotency,priority:2"`
	RequirementKey   string      `gorm:"size:200;index"`
	Description      string      `gorm:"size:2000"`
	CaptureSource    string      `gorm:"size:16"`
	CapturedAt       *time.Time
	DeviceContext    json.RawMessage `gorm:"type:jsonb"`
	LatitudeE6       *int32
	LongitudeE6      *int32
	AccuracyMM       *int
	DistanceMM       *int64
	Flags            json.RawMessage `gorm:"type:jsonb"`
	ReplacesMediaID  *identity.ID    `gorm:"type:uuid;index"`
	CreatedAt        time.Time
}

func (MediaObject) TableName() string { return "media.media_objects" }

type MultipartUpload struct {
	ID          identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID    identity.ID `gorm:"type:uuid;not null;index:idx_multipart_tenant_status,priority:1"`
	MediaID     identity.ID `gorm:"type:uuid;not null;uniqueIndex"`
	UploadID    string      `gorm:"size:1000;not null;uniqueIndex"`
	ObjectKey   string      `gorm:"size:1000;not null;uniqueIndex"`
	Status      string      `gorm:"size:20;not null;index:idx_multipart_tenant_status,priority:2"`
	ExpiresAt   time.Time   `gorm:"not null;index"`
	CreatedAt   time.Time
	CompletedAt *time.Time
}

func (MultipartUpload) TableName() string { return "media.multipart_uploads" }

type UploadPart struct {
	ID        identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID  identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_upload_part,priority:1"`
	UploadID  identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_upload_part,priority:2"`
	Part      int         `gorm:"not null;uniqueIndex:idx_upload_part,priority:3"`
	ETag      string      `gorm:"size:500;not null"`
	SizeBytes int64       `gorm:"not null"`
	CreatedAt time.Time
}

func (UploadPart) TableName() string { return "media.upload_parts" }

type MediaDerivative struct {
	ID        identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID  identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_media_derivative_kind,priority:1"`
	MediaID   identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_media_derivative_kind,priority:2"`
	ObjectKey string      `gorm:"size:1000;not null;uniqueIndex"`
	Kind      string      `gorm:"size:50;not null;uniqueIndex:idx_media_derivative_kind,priority:3"`
	SHA256    string      `gorm:"size:64;not null"`
	CreatedAt time.Time
}

func (MediaDerivative) TableName() string { return "media.derivatives" }

type ScreeningRun struct {
	ID                  identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID            identity.ID     `gorm:"type:uuid;not null;index:idx_screening_media,priority:1"`
	MediaID             identity.ID     `gorm:"type:uuid;not null;index:idx_screening_media,priority:2"`
	Status              string          `gorm:"size:20;not null;index"`
	ModelDigest         string          `gorm:"size:64;not null"`
	Regions             json.RawMessage `gorm:"type:jsonb;not null"`
	FalsePositiveReason string          `gorm:"size:2000"`
	OverriddenAt        *time.Time
	CreatedAt           time.Time
}

func (ScreeningRun) TableName() string { return "media.screening_runs" }

type CaptureDraft struct {
	ID                                            identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID                                      identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_capture_draft_responsibility,priority:1"`
	ResponsibilityID                              identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_capture_draft_responsibility,priority:2"`
	Kind                                          string          `gorm:"size:20;not null"`
	TemplateVersionID                             identity.ID     `gorm:"type:uuid;not null"`
	ReferencePayload, PolicyPayload, Requirements json.RawMessage `gorm:"type:jsonb;not null"`
	Status                                        string          `gorm:"size:20;not null;index"`
	Version                                       int64           `gorm:"not null;default:1"`
	CreatedAt, UpdatedAt                          time.Time
}

func (CaptureDraft) TableName() string { return "capture.capture_drafts" }

type RequirementAnswer struct {
	ID                   identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID             identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_answer_lineage,priority:1"`
	DraftID              identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_answer_lineage,priority:2"`
	RequirementKey       string          `gorm:"size:200;not null;uniqueIndex:idx_answer_lineage,priority:3"`
	MediaIDs, Flags      json.RawMessage `gorm:"type:jsonb;not null"`
	ImpossibilityReason  string          `gorm:"size:2000"`
	Version              int64           `gorm:"not null;default:1"`
	CreatedAt, UpdatedAt time.Time
}

func (RequirementAnswer) TableName() string { return "capture.requirement_answers" }

type SubmissionVersion struct {
	ID                identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID          identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_submission_version,priority:1"`
	DraftID           identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_submission_version,priority:2"`
	ResponsibilityID  identity.ID `gorm:"type:uuid;not null"`
	VersionNumber     int         `gorm:"not null;uniqueIndex:idx_submission_version,priority:3"`
	Complete          bool
	RequiresAttention bool
	Payload           json.RawMessage `gorm:"type:jsonb;not null"`
	PayloadDigest     string          `gorm:"size:64;not null"`
	SubmittedAt       time.Time
}

func (SubmissionVersion) TableName() string { return "capture.submission_versions" }

type RecaptureRequest struct {
	ID               identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID         identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_recapture_idempotency,priority:1;index:idx_recapture_inspection,priority:1"`
	InspectionID     identity.ID `gorm:"type:uuid;not null;index:idx_recapture_inspection,priority:2"`
	ResponsibilityID identity.ID `gorm:"type:uuid;not null;uniqueIndex"`
	Status           string      `gorm:"size:20;not null;index"`
	DeadlineAt       time.Time   `gorm:"not null;index"`
	IdempotencyKey   string      `gorm:"size:200;not null;uniqueIndex:idx_recapture_idempotency,priority:2"`
	PayloadDigest    string      `gorm:"size:64;not null;default:''"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (RecaptureRequest) TableName() string { return "recapture.requests" }

type RecaptureRequirement struct {
	ID              identity.ID  `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_recapture_requirement,priority:1"`
	RequestID       identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_recapture_requirement,priority:2"`
	RequirementKey  string       `gorm:"size:200;not null;uniqueIndex:idx_recapture_requirement,priority:3"`
	OriginalMediaID *identity.ID `gorm:"type:uuid"`
	Reason          string       `gorm:"size:2000;not null"`
	Status          string       `gorm:"size:20;not null"`
	CreatedAt       time.Time
}

func (RecaptureRequirement) TableName() string { return "recapture.request_requirements" }

type Delivery struct {
	ID               identity.ID  `gorm:"type:uuid;primaryKey"`
	TenantID         identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_delivery_intent,priority:1"`
	IntentID         identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_delivery_intent,priority:2"`
	InspectionID     *identity.ID `gorm:"type:uuid;index"`
	Status           string       `gorm:"size:20;not null;index"`
	LogicalTemplate  string       `gorm:"size:100;not null;default:''"`
	TemplateVersion  string       `gorm:"size:32;not null;default:''"`
	CorrelationID    string       `gorm:"size:200;not null;default:''"`
	IdempotencyKey   string       `gorm:"size:200;not null;default:''"`
	RequestDigest    string       `gorm:"size:64;not null;default:''"`
	RecipientID      string       `gorm:"size:200;not null;default:''"`
	SelectedProvider string       `gorm:"size:50;not null;default:''"`
	ScheduledAt      *time.Time
	LeaseExpiresAt   *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (Delivery) TableName() string { return "notifications.deliveries" }

type ChannelAttempt struct {
	ID                identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID          identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_channel_destination,priority:1"`
	DeliveryID        identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_channel_destination,priority:2"`
	Channel           string      `gorm:"size:20;not null;uniqueIndex:idx_channel_destination,priority:3"`
	Destination       string      `gorm:"size:320;not null;uniqueIndex:idx_channel_destination,priority:4"`
	Status            string      `gorm:"size:20;not null;index"`
	Provider          string      `gorm:"size:50"`
	ProviderAccount   string      `gorm:"size:200;not null;default:''"`
	ReceiptID         string      `gorm:"size:500;index"`
	Attempts          int         `gorm:"not null;default:0"`
	LastError         string      `gorm:"size:200"`
	TemplateVariables []byte      `gorm:"type:jsonb;default:'{}'"`
	NextAttemptAt     time.Time
	LeaseExpiresAt    *time.Time
	LastAttemptAt     *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (ChannelAttempt) TableName() string { return "notifications.channel_attempts" }

// NotificationAttempt records a single provider invocation without mutating prior history.
type NotificationAttempt struct {
	ID               identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID         identity.ID `gorm:"type:uuid;not null;index:idx_notification_attempts,priority:1"`
	ChannelAttemptID identity.ID `gorm:"type:uuid;not null;index:idx_notification_attempts,priority:2"`
	Sequence         int         `gorm:"not null;index:idx_notification_attempts,priority:3"`
	Provider         string      `gorm:"size:50;not null"`
	Status           string      `gorm:"size:20;not null"`
	ReceiptID        string      `gorm:"size:500"`
	ErrorCode        string      `gorm:"size:100"`
	StartedAt        time.Time   `gorm:"not null"`
	FinishedAt       *time.Time
}

func (NotificationAttempt) TableName() string { return "notifications.attempt_history" }

// NotificationPayload contains encrypted execution-only material.
type NotificationPayload struct {
	ID         identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID   identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_notification_payload,priority:1"`
	DeliveryID identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_notification_payload,priority:2"`
	KeyID      string      `gorm:"size:100;not null"`
	Nonce      []byte      `gorm:"type:bytea;not null"`
	Ciphertext []byte      `gorm:"type:bytea;not null"`
	CreatedAt  time.Time   `gorm:"not null"`
	ExpiresAt  *time.Time
}

func (NotificationPayload) TableName() string { return "notifications.execution_payloads" }

// NotificationRecipient is an explicitly configured internal alert target.
// It is intentionally separate from participant contacts so external people
// can never become automatic recipients of internal findings.
type NotificationRecipient struct {
	ID          identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID    identity.ID `gorm:"type:uuid;not null;index"`
	IdentityID  identity.ID `gorm:"type:uuid;not null;index"`
	Channel     string      `gorm:"size:20;not null"`
	Destination string      `gorm:"size:320;not null"`
	Verified    bool        `gorm:"not null;default:false"`
	Selected    bool        `gorm:"not null;default:false"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (NotificationRecipient) TableName() string { return "notifications.recipients" }

type ProviderCallback struct {
	ID               identity.ID  `gorm:"type:uuid;primaryKey"`
	TenantID         identity.ID  `gorm:"type:uuid;not null;uniqueIndex:idx_provider_callback_scope,priority:1"`
	Provider         string       `gorm:"size:50;not null;uniqueIndex:idx_provider_callback_scope,priority:2"`
	ProviderAccount  string       `gorm:"size:200;not null;default:'';uniqueIndex:idx_provider_callback_scope,priority:3"`
	CallbackID       string       `gorm:"size:500;not null;uniqueIndex:idx_provider_callback_scope,priority:4"`
	ReceiptID        string       `gorm:"size:500;not null;index"`
	ChannelAttemptID *identity.ID `gorm:"type:uuid;index"`
	Status           string       `gorm:"size:50;not null"`
	ReceivedAt       time.Time    `gorm:"not null"`
}

func (ProviderCallback) TableName() string { return "notifications.provider_callbacks" }

// Schedule is the mutable recurrence configuration. Materialized occurrences
// retain their own immutable snapshot and are not rewritten with this row.
type Schedule struct {
	ID                 identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID           identity.ID     `gorm:"type:uuid;not null;index:idx_schedules_due,priority:1;uniqueIndex:idx_schedule_idempotency,priority:1"`
	BusinessUnitID     identity.ID     `gorm:"type:uuid;not null;index:idx_schedules_scope,priority:2"`
	AssetID            identity.ID     `gorm:"type:uuid;not null;index:idx_schedules_scope,priority:3"`
	ParticipantID      identity.ID     `gorm:"type:uuid;not null"`
	TemplateID         identity.ID     `gorm:"type:uuid;not null"`
	ReferenceVersionID *identity.ID    `gorm:"type:uuid"`
	RRule              string          `gorm:"size:2000;not null"`
	Timezone           string          `gorm:"size:64;not null"`
	StartsAt           time.Time       `gorm:"not null"`
	NextDueAt          time.Time       `gorm:"not null;index:idx_schedules_due,priority:3"`
	DeadlineMinutes    int             `gorm:"not null"`
	ReminderOffsets    json.RawMessage `gorm:"type:jsonb;not null"`
	Status             string          `gorm:"size:16;not null;index:idx_schedules_due,priority:2"`
	Version            int64           `gorm:"not null;default:1"`
	IdempotencyKey     string          `gorm:"size:200;not null;uniqueIndex:idx_schedule_idempotency,priority:2"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (Schedule) TableName() string { return "schedules.schedules" }

type OccurrenceMaterialization struct {
	ID           identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID     identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_materialization_due,priority:1"`
	ScheduleID   identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_materialization_due,priority:2"`
	DueInstant   time.Time   `gorm:"not null;uniqueIndex:idx_materialization_due,priority:3"`
	InspectionID identity.ID `gorm:"type:uuid;not null;uniqueIndex"`
	EventID      identity.ID `gorm:"type:uuid;not null"`
	CreatedAt    time.Time
}

func (OccurrenceMaterialization) TableName() string {
	return "schedules.occurrence_materializations"
}

type ReminderPlan struct {
	ID           identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID     identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_reminder_plan,priority:1;index:idx_reminders_due,priority:1"`
	InspectionID identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_reminder_plan,priority:2"`
	RemindAt     time.Time   `gorm:"not null;uniqueIndex:idx_reminder_plan,priority:3;index:idx_reminders_due,priority:3"`
	Status       string      `gorm:"size:16;not null;index:idx_reminders_due,priority:2"`
	CreatedAt    time.Time
}

func (ReminderPlan) TableName() string { return "schedules.reminder_plans" }

type Inspection struct {
	ID                       identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID                 identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_inspection_source,priority:1;index:idx_inspections_cursor,priority:1"`
	BusinessUnitID           identity.ID     `gorm:"type:uuid;not null;index:idx_inspections_scope,priority:2"`
	AssetID                  identity.ID     `gorm:"type:uuid;not null;index:idx_inspections_scope,priority:3"`
	ParticipantID            identity.ID     `gorm:"type:uuid;not null"`
	TemplateID               identity.ID     `gorm:"type:uuid;not null"`
	TemplateVersionID        identity.ID     `gorm:"type:uuid;not null"`
	AnalysisProfileVersionID identity.ID     `gorm:"type:uuid;not null"`
	ProjectID                *identity.ID    `gorm:"type:uuid;index"`
	StageID                  *identity.ID    `gorm:"type:uuid;index"`
	Source                   string          `gorm:"size:20;not null;uniqueIndex:idx_inspection_source,priority:2"`
	SourceKey                string          `gorm:"size:300;not null;uniqueIndex:idx_inspection_source,priority:3"`
	SourceReason             string          `gorm:"size:2000"`
	StateReason              string          `gorm:"size:2000"`
	Status                   string          `gorm:"size:24;not null;index"`
	EvidenceCount            int             `gorm:"not null;default:0"`
	DueAt                    time.Time       `gorm:"not null"`
	DeadlineAt               time.Time       `gorm:"not null"`
	ReminderInstants         json.RawMessage `gorm:"type:jsonb;not null"`
	ContextSnapshot          json.RawMessage `gorm:"type:jsonb;not null"`
	Version                  int64           `gorm:"not null;default:1"`
	CreatedAt                time.Time       `gorm:"not null;index:idx_inspections_cursor,priority:2"`
	UpdatedAt                time.Time
}

func (Inspection) TableName() string { return "inspections.inspections" }

type Responsibility struct {
	ID            identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID      identity.ID `gorm:"type:uuid;not null;index:idx_inspection_responsibility,priority:1"`
	InspectionID  identity.ID `gorm:"type:uuid;not null;index:idx_inspection_responsibility,priority:2"`
	ParticipantID identity.ID `gorm:"type:uuid;not null"`
	Status        string      `gorm:"size:20;not null;index"`
	Version       int64       `gorm:"not null;default:1"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (Responsibility) TableName() string { return "inspections.responsibilities" }

type PolicySnapshot struct {
	ID              identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_policy_snapshot,priority:1"`
	InspectionID    identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_policy_snapshot,priority:2"`
	SchemaVersion   int             `gorm:"not null"`
	Payload         json.RawMessage `gorm:"type:jsonb;not null"`
	CanonicalDigest string          `gorm:"size:64;not null"`
	CreatedAt       time.Time
}

func (PolicySnapshot) TableName() string { return "inspections.policy_snapshots" }

type ReferenceSnapshot struct {
	ID                 identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID           identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_reference_snapshot,priority:1"`
	InspectionID       identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_reference_snapshot,priority:2"`
	ReferenceVersionID *identity.ID    `gorm:"type:uuid"`
	ComparisonMode     string          `gorm:"size:32;not null"`
	Payload            json.RawMessage `gorm:"type:jsonb;not null"`
	CreatedAt          time.Time
}

func (ReferenceSnapshot) TableName() string { return "inspections.reference_snapshots" }

type Project struct {
	ID                identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID          identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_project_idempotency,priority:1;index:idx_projects_cursor,priority:1"`
	BusinessUnitID    identity.ID `gorm:"type:uuid;not null;index:idx_projects_scope,priority:2"`
	AssetID           identity.ID `gorm:"type:uuid;not null;index:idx_projects_scope,priority:3"`
	ParticipantID     identity.ID `gorm:"type:uuid;not null"`
	TemplateID        identity.ID `gorm:"type:uuid;not null"`
	TemplateVersionID identity.ID `gorm:"type:uuid;not null"`
	ReportMode        string      `gorm:"size:16;not null"`
	Status            string      `gorm:"size:16;not null;index"`
	Version           int64       `gorm:"not null;default:1"`
	IdempotencyKey    string      `gorm:"size:200;not null;uniqueIndex:idx_project_idempotency,priority:2"`
	CreatedAt         time.Time   `gorm:"not null;index:idx_projects_cursor,priority:2"`
	UpdatedAt         time.Time
}

func (Project) TableName() string { return "projects.projects" }

type ProjectStage struct {
	ID                 identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID           identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_project_stage_key,priority:1;uniqueIndex:idx_project_stage_position,priority:1"`
	ProjectID          identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_project_stage_key,priority:2;uniqueIndex:idx_project_stage_position,priority:2;index"`
	Key                string      `gorm:"size:200;not null;uniqueIndex:idx_project_stage_key,priority:3"`
	Label              string      `gorm:"size:200;not null"`
	Kind               string      `gorm:"size:16;not null"`
	Position           int         `gorm:"not null;uniqueIndex:idx_project_stage_position,priority:3"`
	Status             string      `gorm:"size:20;not null;index"`
	PlannedAt          *time.Time
	Requirements       json.RawMessage `gorm:"type:jsonb;not null"`
	EffectiveReference json.RawMessage `gorm:"type:jsonb;not null"`
	Reason             string          `gorm:"size:2000"`
	InspectionID       *identity.ID    `gorm:"type:uuid"`
	Version            int64           `gorm:"not null;default:1"`
	IdempotencyKey     string          `gorm:"size:200"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (ProjectStage) TableName() string { return "projects.project_stages" }

type StageTransition struct {
	ID            identity.ID  `gorm:"type:uuid;primaryKey"`
	TenantID      identity.ID  `gorm:"type:uuid;not null;index:idx_stage_transitions,priority:1"`
	ProjectID     identity.ID  `gorm:"type:uuid;not null;index:idx_stage_transitions,priority:2"`
	StageID       *identity.ID `gorm:"type:uuid"`
	ActorID       identity.ID  `gorm:"type:uuid;not null"`
	FromState     string       `gorm:"size:20"`
	ToState       string       `gorm:"size:20;not null"`
	Reason        string       `gorm:"size:2000"`
	CorrelationID string       `gorm:"size:200;not null"`
	OccurredAt    time.Time    `gorm:"not null;index:idx_stage_transitions,priority:3"`
}

func (StageTransition) TableName() string { return "projects.stage_transitions" }

// ComparisonJob is immutable input identity and mutable bounded execution state.
type ComparisonJob struct {
	ID, TenantID, InspectionID                             identity.ID `gorm:"type:uuid;primaryKey"`
	RequirementKey, ModelAlias, PromptVersion, InputDigest string      `gorm:"size:200;not null"`
	Status                                                 string      `gorm:"size:20;not null;index"`
	Attempts                                               int         `gorm:"not null;default:0"`
	CreatedAt, UpdatedAt                                   time.Time
}

func (ComparisonJob) TableName() string { return "analysis.comparison_jobs" }

type AnalysisRun struct {
	ID, TenantID, JobID                                           identity.ID     `gorm:"type:uuid;primaryKey"`
	Provider, Model, GatewayRequestID, PromptVersion, InputDigest string          `gorm:"size:200;not null"`
	Output                                                        json.RawMessage `gorm:"type:jsonb;not null"`
	InputTokens, OutputTokens                                     *int64
	Cost                                                          *float64
	LatencyMS                                                     int64  `gorm:"not null"`
	ValidationOutcome                                             string `gorm:"size:32;not null"`
	CreatedAt                                                     time.Time
}

func (AnalysisRun) TableName() string { return "analysis.analysis_runs" }

type FindingRecord struct {
	ID, TenantID, AnalysisRunID                                        identity.ID     `gorm:"type:uuid;primaryKey"`
	Category, Title, Description, Severity, Quality, RecommendedAction string          `gorm:"size:2000;not null"`
	Confidence                                                         float64         `gorm:"not null"`
	Evidence                                                           json.RawMessage `gorm:"type:jsonb;not null"`
	CreatedAt                                                          time.Time
}

func (FindingRecord) TableName() string { return "analysis.findings" }

type ClassificationRun struct {
	ID, TenantID, InspectionID, ProfileVersionID identity.ID     `gorm:"type:uuid;primaryKey"`
	Classification                               string          `gorm:"size:16;not null"`
	ReasonCodes                                  json.RawMessage `gorm:"type:jsonb;not null"`
	CreatedAt                                    time.Time
}

func (ClassificationRun) TableName() string { return "analysis.classification_runs" }

type ReportSnapshot struct {
	ID, TenantID, InspectionID                   identity.ID     `gorm:"type:uuid;primaryKey"`
	ProjectID                                    *identity.ID    `gorm:"type:uuid"`
	Mode, Classification, JSONDigest, HTMLDigest string          `gorm:"size:64;not null"`
	VersionNumber                                int             `gorm:"not null"`
	PublicationPolicyVersion                     int64           `gorm:"not null;default:0"`
	CanonicalJSON, HTML                          json.RawMessage `gorm:"type:jsonb;not null"`
	CreatedAt                                    time.Time
}

func (ReportSnapshot) TableName() string { return "reports.report_snapshots" }

type ReportArtifact struct {
	ID, TenantID, SnapshotID        identity.ID `gorm:"type:uuid;primaryKey"`
	Kind, ObjectKey, SHA256, Status string      `gorm:"size:1000;not null"`
	CreatedAt                       time.Time
}

func (ReportArtifact) TableName() string { return "reports.report_artifacts" }

// PublicationPolicy stores the current tenant publication preference. A
// missing row is intentionally equivalent to MANUAL version zero.
type PublicationPolicy struct {
	ID, TenantID identity.ID `gorm:"type:uuid;primaryKey"`
	Mode         string      `gorm:"size:16;not null"`
	Version      int64       `gorm:"not null;default:1"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (PublicationPolicy) TableName() string { return "reports.publication_policies" }

// ReportPublication is the audited, mutable visibility ledger for an
// immutable report snapshot.
type ReportPublication struct {
	ID, TenantID, SnapshotID, InspectionID identity.ID `gorm:"type:uuid;primaryKey"`
	PolicyVersion                          int64       `gorm:"not null"`
	ClientMutationID                       string      `gorm:"size:200;not null"`
	Status                                 string      `gorm:"size:20;not null;index"`
	ActorID                                identity.ID `gorm:"type:uuid;not null"`
	Reason                                 string      `gorm:"size:2000"`
	Version                                int64       `gorm:"not null;default:1"`
	PublishedAt                            *time.Time
	InvalidatedAt                          *time.Time
	SupersededBy                           *identity.ID `gorm:"type:uuid"`
	CreatedAt                              time.Time
	UpdatedAt                              time.Time
}

func (ReportPublication) TableName() string { return "reports.report_publications" }

// RecipientChannel is owned by a dashboard membership, unlike participant
// contacts and internal alert recipients.
type RecipientChannel struct {
	ID, TenantID, RecipientMembershipID identity.ID `gorm:"type:uuid;primaryKey"`
	Channel                             string      `gorm:"size:20;not null"`
	Destination                         string      `gorm:"size:320;not null"`
	VerifiedAt                          *time.Time
	Selected                            bool  `gorm:"not null;default:false"`
	Version                             int64 `gorm:"not null;default:1"`
	CreatedAt                           time.Time
	UpdatedAt                           time.Time
}

func (RecipientChannel) TableName() string { return "notifications.recipient_channels" }

// RecipientNotification is a safe, per-membership in-app notification.
type RecipientNotification struct {
	ID, TenantID, RecipientMembershipID, EventID identity.ID  `gorm:"type:uuid;primaryKey"`
	Kind, Title, Body, ResourceKind              string       `gorm:"size:500;not null"`
	ResourceID                                   *identity.ID `gorm:"type:uuid"`
	CreatedAt                                    time.Time
	ReadAt                                       *time.Time
}

func (RecipientNotification) TableName() string { return "notifications.recipient_notifications" }

type RetentionPolicy struct {
	ID, TenantID                  identity.ID `gorm:"type:uuid;primaryKey"`
	EvidenceDays, OperationalDays int         `gorm:"not null"`
	SecurityDays                  int         `gorm:"not null;default:0"`
	Version                       int64       `gorm:"not null;default:1"`
	UpdatedAt                     time.Time
	CreatedAt                     time.Time
}

func (RetentionPolicy) TableName() string { return "retention.policies" }

// DashboardInspection is a disposable, rebuildable tenant projection. It is
// never the source of truth for an inspection or its immutable report.
type DashboardInspection struct {
	ID, TenantID, InspectionID identity.ID  `gorm:"type:uuid;primaryKey"`
	ProjectID                  *identity.ID `gorm:"type:uuid;index"`
	AssetID                    identity.ID  `gorm:"type:uuid;index"`
	Classification             string       `gorm:"size:16;not null;index"`
	Status                     string       `gorm:"size:24;not null;index"`
	Invalidated                bool         `gorm:"not null;default:false;index"`
	Sequence                   int64        `gorm:"not null;index"`
	UpdatedAt                  time.Time
}

func (DashboardInspection) TableName() string { return "dashboard.inspections" }

// UsageRecord stores provider-neutral metering for reproducibility and
// reconciliation. It intentionally contains no prompt or image content.
type UsageRecord struct {
	ID, TenantID, InspectionID, JobID identity.ID `gorm:"type:uuid;primaryKey"`
	Provider, Model, PromptVersion    string      `gorm:"size:200;not null"`
	InputTokens, OutputTokens         *int64
	Cost                              *float64
	LatencyMS                         int64 `gorm:"not null"`
	CreatedAt                         time.Time
}

func (UsageRecord) TableName() string { return "usage.records" }

type UsageDailySummary struct {
	ID, TenantID identity.ID `gorm:"type:uuid;primaryKey"`
	Day          time.Time   `gorm:"type:date;not null;uniqueIndex:idx_usage_daily,priority:2"`
	Requests     int64       `gorm:"not null"`
	InputTokens  int64       `gorm:"not null"`
	OutputTokens int64       `gorm:"not null"`
	Cost         float64     `gorm:"not null"`
	UpdatedAt    time.Time
}

func (UsageDailySummary) TableName() string { return "usage.daily_summaries" }

type DeletionRequest struct {
	ID, TenantID, InspectionID identity.ID `gorm:"type:uuid;primaryKey"`
	Status                     string      `gorm:"size:24;not null;index"`
	Reason                     string      `gorm:"size:2000"`
	RequestedAt                time.Time   `gorm:"not null"`
	ProcessedAt                *time.Time
}

func (DeletionRequest) TableName() string { return "retention.deletion_requests" }

type LegalHold struct {
	ID, TenantID, InspectionID identity.ID `gorm:"type:uuid;primaryKey"`
	Reason                     string      `gorm:"size:2000;not null"`
	Active                     bool        `gorm:"not null;default:true;index"`
	CreatedAt                  time.Time
	ReleasedAt                 *time.Time
}

func (LegalHold) TableName() string { return "retention.legal_holds" }

type PurgeRun struct {
	ID, TenantID, InspectionID identity.ID     `gorm:"type:uuid;primaryKey"`
	Status                     string          `gorm:"size:24;not null;index"`
	Manifest                   json.RawMessage `gorm:"type:jsonb;not null"`
	CompletedAt                *time.Time
	CreatedAt                  time.Time
}

func (PurgeRun) TableName() string { return "retention.purge_runs" }

// Models is the explicit additive migration allowlist.
func Models() []any {
	return []any{&BootstrapRequest{}, &Tenant{}, &BusinessUnit{}, &Membership{}, &ProductEntitlement{}, &ResourceScope{}, &AuditEvent{}, &OutboxIntent{}, &InboxReceipt{},
		&Participant{}, &ParticipantContact{}, &ContactVerification{}, &ChannelSelection{},
		&SegmentDefinition{}, &SegmentDefinitionVersion{}, &Template{}, &TemplateVersion{}, &AnalysisProfile{}, &AnalysisProfileVersion{},
		&OnboardingSession{}, &OnboardingOTPChallenge{}, &OnboardingStepRecord{}, &OnboardingRequest{}, &OnboardingActivation{},
		&Asset{}, &AssetAttributeVersion{}, &AssetAssignment{},
		&Invitation{}, &OTPChallenge{}, &ExternalSession{}, &ProcessingAcceptance{},
		&Origin{}, &OriginVersion{}, &OriginEvidence{},
		&MediaObject{}, &MultipartUpload{}, &UploadPart{}, &MediaDerivative{}, &ScreeningRun{},
		&CaptureDraft{}, &RequirementAnswer{}, &SubmissionVersion{}, &RecaptureRequest{}, &RecaptureRequirement{},
		&Delivery{}, &ChannelAttempt{}, &NotificationAttempt{}, &NotificationPayload{}, &NotificationRecipient{}, &ProviderCallback{},
		&Schedule{}, &OccurrenceMaterialization{}, &ReminderPlan{},
		&Inspection{}, &Responsibility{}, &PolicySnapshot{}, &ReferenceSnapshot{},
		&Project{}, &ProjectStage{}, &StageTransition{},
		&ComparisonJob{}, &AnalysisRun{}, &FindingRecord{}, &ClassificationRun{},
		&ReportSnapshot{}, &ReportArtifact{}, &RetentionPolicy{},
		&PublicationPolicy{}, &ReportPublication{}, &RecipientChannel{}, &RecipientNotification{},
		&DashboardInspection{}, &UsageRecord{}, &UsageDailySummary{},
		&DeletionRequest{}, &LegalHold{}, &PurgeRun{}}
}
