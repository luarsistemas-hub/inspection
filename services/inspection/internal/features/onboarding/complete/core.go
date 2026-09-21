// Package complete turns the public onboarding checkpoints into the first
// durable inspection. It is the single orchestration boundary for this use
// case; the catalog, participant, asset, and inspection domains keep owning
// their invariants.
package complete

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	"inspection/services/inspection/internal/features/onboarding/coordinator"
	onboardingcatalog "inspection/services/inspection/internal/features/onboarding/real_estate_catalog"
	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	segmentcore "inspection/services/inspection/internal/features/segments/core"
	templatecatalog "inspection/services/inspection/internal/features/templates/catalog"
	templatecore "inspection/services/inspection/internal/features/templates/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

const (
	requestStatusSubmitted      = "SUBMITTED"
	deliveryPending             = "PENDING"
	activationInvitationPending = "INVITATION_PENDING"
	activationInvitationSent    = "INVITED"
	activationInvitationTTL     = 24 * time.Hour
)

// ActivationInvitationNotifier sends the verified agency owner to the Admin
// password-creation flow after onboarding is durably complete.
type ActivationInvitationNotifier interface {
	SendActivationInvitation(context.Context, string, string) error
}

// Result is the durable public result of completing onboarding.
type Result struct {
	Session      onboardingsession.Session
	Request      database.OnboardingRequest
	InspectionID identity.ID
	State        string
	NextAction   string
	OriginStatus string
	Delivery     string
}

// Service coordinates existing domain services for the first inspection.
type Service struct {
	DB                 *gorm.DB
	Bus                *mediator.Bus
	Sessions           onboardingsession.Service
	ActivationNotifier ActivationInvitationNotifier
	AdminOrigin        string
	OwnerIssuer        string
	Now                func() time.Time
	Within             func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Complete is idempotent per onboarding session. Browser values are ignored;
// only immutable checkpoints loaded through the verified cookie/CSRF pair are
// materialized.
func (s Service) Complete(ctx context.Context, locator, csrf, idempotencyKey string) (Result, error) {
	if s.DB == nil || s.Bus == nil || s.ActivationNotifier == nil || strings.TrimSpace(s.AdminOrigin) == "" || strings.TrimSpace(s.OwnerIssuer) == "" {
		return Result{}, errors.New("onboarding complete: missing dependency")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "clientMutationId", "idempotency key is required")
	}
	submission, err := s.Sessions.LoadSubmission(ctx, locator, csrf)
	if err != nil {
		return Result{}, err
	}
	if submission.Session.TenantID == nil || submission.Session.Owner.Subject == "" {
		return Result{}, apperror.New(apperror.InvalidState, "agency", "agency setup is incomplete")
	}
	tenantID := *submission.Session.TenantID
	if existing, ok, err := s.existing(ctx, tenantID, submission.Session.ID); err != nil {
		return Result{}, err
	} else if ok && existing.InspectionID != nil {
		current := submission.Session
		if current.State != coordinator.StateSubmitted {
			current, err = s.Sessions.MarkSubmitted(ctx, locator, csrf)
			if err != nil {
				return Result{}, err
			}
		}
		_, member, err := s.onboardingOwner(ctx, tenantID, submission.Session.Owner.Subject)
		if err != nil {
			return Result{}, err
		}
		if err := s.ensureActivationInvitation(ctx, current, member); err != nil {
			return Result{}, err
		}
		return completedResult(current, existing, *existing.InspectionID), nil
	}

	now := s.now()
	originMode := strings.ToUpper(stepString(submission.Steps[onboardingsession.StepOrigin], "mode"))
	templateMode := templatecatalog.ChecklistOnly
	originStatus := "NOT_REQUIRED"
	var originMediaIDs []identity.ID
	if originMode == string(templatecatalog.FixedOrigin) {
		templateMode = templatecatalog.FixedOrigin
		originMediaIDs, err = coordinator.OriginMediaIDs(submission.Steps[onboardingsession.StepOrigin])
		if err != nil || len(originMediaIDs) == 0 {
			return Result{}, apperror.New(apperror.InvalidState, "referencePhotos", "As fotos deste cadastro não foram enviadas. Inicie um novo cadastro e selecione-as novamente.")
		}
		originStatus, err = s.originMediaStatus(ctx, tenantID, submission.Session.ID, originMediaIDs)
		if err != nil {
			return Result{}, err
		}
	}
	decision, err := coordinator.ValidateSubmit(coordinator.SubmitInput{Now: now, Steps: submission.Steps, OriginStatus: originStatus, DeliveryState: deliveryPending, TemplateMode: templateMode, IdempotencyKey: idempotencyKey})
	if err != nil {
		return Result{}, err
	}
	if decision.State == coordinator.StateOriginPending {
		return pendingResult(submission.Session, decision), nil
	}

	unit, member, err := s.onboardingOwner(ctx, tenantID, submission.Session.Owner.Subject)
	if err != nil {
		return Result{}, err
	}
	domainCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{
		TenantID: tenantID,
		Principal: requestctx.Principal{
			IdentityID: member.IdentityID, MembershipID: member.ID, MembershipVersion: member.Version,
			TenantID: tenantID, Issuer: member.Issuer, Subject: member.Subject,
			Roles: []string{auth.TenantAdmin}, ProductEntitlements: []string{auth.AdminProduct},
			Internal: true,
		},
		CorrelationID: correlationID(ctx), StartedAt: now,
	})

	segment, analysisType, template, err := s.ensureCatalog(domainCtx, tenantID, member.IdentityID, templateMode)
	if err != nil {
		return Result{}, fmt.Errorf("prepare onboarding catalog: %w", err)
	}
	participant, err := s.ensureParticipant(domainCtx, tenantID, unit.ID, submission)
	if err != nil {
		return Result{}, fmt.Errorf("prepare onboarding participant: %w", err)
	}
	property := submission.Steps[onboardingsession.StepProperty]
	rooms, err := strconv.ParseFloat(stepString(property, "rooms"), 64)
	if err != nil || rooms <= 0 || rooms != float64(int64(rooms)) {
		return Result{}, apperror.New(apperror.InvalidInput, "rooms", "rooms must be a positive integer")
	}
	asset, err := (assetcore.Service{DB: s.DB, Bus: s.Bus, Authorizer: auth.Authorizer{}}).Register(domainCtx, assetcore.Input{
		TenantID: tenantID, BusinessUnitID: unit.ID, SegmentVersionID: segment.ID, TemplateID: &template.ID,
		Name: limitedName(stepString(property, "address")), ExternalKey: "onboarding-" + submission.Session.ID.String(),
		Address: stepString(property, "address"), GeofenceMeters: templatecatalog.DefaultGeofence,
		Attributes:     map[string]any{"propertyType": property["propertyType"], "rooms": rooms, "purpose": property["purpose"]},
		Assignments:    []assetcore.AssignmentInput{{ParticipantID: participant.Participant.ID, Role: "PROPERTY_OWNER"}},
		IdempotencyKey: "onboarding:asset:" + submission.Session.ID.String(),
	})
	if err != nil {
		return Result{}, fmt.Errorf("prepare onboarding asset: %w", err)
	}
	var referenceVersionID *identity.ID
	if templateMode == templatecatalog.FixedOrigin {
		if template.ActiveVersionID == nil {
			return Result{}, errors.New("onboarding origin template has no active version")
		}
		versionID, err := s.ensureOrigin(ctx, tenantID, submission.Session.ID, asset.Asset.ID, template.ID, *template.ActiveVersionID, originMediaIDs)
		if err != nil {
			return Result{}, fmt.Errorf("prepare onboarding origin: %w", err)
		}
		referenceVersionID = &versionID
	}
	deadline, err := deadlineAt(property)
	if err != nil {
		return Result{}, err
	}
	inspection, err := (inspectioncore.Service{DB: s.DB, Bus: s.Bus, Authorizer: auth.Authorizer{}, Now: s.Now}).Create(domainCtx, inspectioncore.CreateInput{
		TenantID: tenantID, AssetID: asset.Asset.ID, ParticipantID: participant.Participant.ID, TemplateID: &template.ID, ReferenceVersionID: referenceVersionID,
		Source: inspectioncore.SourceManual, SourceKey: "onboarding:" + submission.Session.ID.String(),
		Reason: "Primeira vistoria criada pelo onboarding", DueAt: now, DeadlineAt: deadline,
	})
	if err != nil {
		return Result{}, fmt.Errorf("create onboarding inspection: %w", err)
	}
	_ = analysisType // The global prompt is resolved and pinned by inspection creation.

	request := database.OnboardingRequest{
		ID: identity.NewID(), TenantID: tenantID, SessionID: submission.Session.ID,
		InspectionID: &inspection.Inspection.ID, AssetID: &asset.Asset.ID, ParticipantID: &participant.Participant.ID,
		TemplateID: &template.ID, OriginVersionID: referenceVersionID, Status: requestStatusSubmitted, IdempotencyKey: idempotencyKey, CreatedAt: now, UpdatedAt: now,
	}
	if err := (tenanttx.Runner{DB: s.DB}).Within(domainCtx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("session_id = ?", submission.Session.ID).Attrs(request).FirstOrCreate(&request).Error
	}); err != nil {
		return Result{}, err
	}
	if request.InspectionID == nil {
		return Result{}, errors.New("onboarding complete: persisted request has no inspection")
	}
	current, err := s.Sessions.MarkSubmitted(ctx, locator, csrf)
	if err != nil {
		return Result{}, err
	}
	if err := s.ensureActivationInvitation(ctx, current, member); err != nil {
		return Result{}, err
	}
	return completedResult(current, request, *request.InspectionID), nil
}

func (s Service) ensureActivationInvitation(ctx context.Context, current onboardingsession.Session, member database.Membership) error {
	if current.TenantID == nil || member.IdentityID == (identity.ID{}) {
		return errors.New("onboarding complete: owner activation is unavailable")
	}
	tenantID := *current.TenantID
	now := s.now()
	shouldSend := false
	token, err := security.NewOpaqueToken()
	if err != nil {
		return fmt.Errorf("prepare owner activation token: %w", err)
	}
	tokenDigest := security.HashToken(token)
	tokenExpiresAt := now.Add(activationInvitationTTL)
	if err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var activation database.OnboardingActivation
		err := tx.Where("tenant_id = ?", tenantID).First(&activation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			activation = database.OnboardingActivation{
				TenantID: tenantID, IdentityID: member.IdentityID, SessionID: current.ID, Purpose: onboardingsession.PurposeActivation,
				Status: activationInvitationPending, InvitationTokenDigest: tokenDigest[:], InvitationExpiresAt: &tokenExpiresAt,
				IdempotencyKey: current.ID.String(), CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&activation).Error; err != nil {
				return err
			}
			shouldSend = true
			return nil
		}
		if err != nil {
			return err
		}
		shouldSend = activation.Status == activationInvitationPending ||
			(activation.Status == activationInvitationSent &&
				(len(activation.InvitationTokenDigest) == 0 || activation.SessionID != current.ID))
		if !shouldSend {
			return nil
		}
		return tx.Model(&activation).Updates(map[string]any{
			"identity_id": member.IdentityID, "session_id": current.ID, "status": activationInvitationPending,
			"invitation_token_digest": tokenDigest[:], "invitation_expires_at": tokenExpiresAt,
			"invitation_claimed_at": nil, "updated_at": now,
		}).Error
	}); err != nil {
		return fmt.Errorf("prepare owner activation invitation: %w", err)
	}
	if !shouldSend {
		return nil
	}
	activationURL, err := url.Parse(strings.TrimRight(s.AdminOrigin, "/") + "/activate")
	if err != nil {
		return fmt.Errorf("prepare owner activation URL: %w", err)
	}
	query := activationURL.Query()
	query.Set("token", token)
	activationURL.RawQuery = query.Encode()
	if err := s.ActivationNotifier.SendActivationInvitation(ctx, current.Owner.Email, activationURL.String()); err != nil {
		return fmt.Errorf("send owner activation invitation: %w", err)
	}
	if err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		result := tx.Model(&database.OnboardingActivation{}).
			Where("tenant_id = ? AND status = ? AND invitation_token_digest = ?", tenantID, activationInvitationPending, tokenDigest[:]).
			Updates(map[string]any{"status": activationInvitationSent, "updated_at": s.now()}).Error
		return result
	}); err != nil {
		return fmt.Errorf("confirm owner activation invitation: %w", err)
	}
	return nil
}

func (s Service) ensureCatalog(ctx context.Context, tenantID, actorID identity.ID, mode templatecatalog.ComparisonMode) (database.SegmentDefinitionVersion, string, database.Template, error) {
	segmentService := segmentcore.Service{DB: s.DB, Authorizer: auth.Authorizer{}}
	segmentView, err := segmentService.Publish(ctx, tenantID, "real-estate", "Imóveis", "onboarding:segment:v1", []byte(`{"type":"object","properties":{"propertyType":{"type":"string","maxLength":100},"rooms":{"type":"integer"},"purpose":{"type":"string","maxLength":100}},"required":["propertyType","rooms","purpose"],"additionalProperties":false}`), []byte(`{}`))
	if err != nil {
		return database.SegmentDefinitionVersion{}, "", database.Template{}, err
	}
	if _, err := segmentService.Activate(ctx, tenantID, segmentView.Version.ID, segmentView.Definition.Version); err != nil {
		return database.SegmentDefinitionVersion{}, "", database.Template{}, err
	}
	templates := templatecore.Service{DB: s.DB, Bus: s.Bus, Authorizer: auth.Authorizer{}}
	documents := onboardingcatalog.TemplateDocuments()
	templatesByKey := make(map[string]database.Template, len(documents))
	for key, document := range documents {
		document.SegmentVersionID = segmentView.Version.ID.String()
		payload, err := json.Marshal(document)
		if err != nil {
			return database.SegmentDefinitionVersion{}, "", database.Template{}, err
		}
		templateName := "Primeira vistoria do imóvel"
		if key == onboardingcatalog.OriginTemplateKey {
			templateName = "Vistoria comparativa do imóvel"
		}
		templateView, err := templates.Publish(ctx, tenantID, key, templateName, "onboarding:template:"+key+":v3", payload)
		if err != nil {
			return database.SegmentDefinitionVersion{}, "", database.Template{}, err
		}
		templateView, err = templates.Activate(ctx, tenantID, templateView.Version.ID, templateView.Template.Version)
		if err != nil {
			return database.SegmentDefinitionVersion{}, "", database.Template{}, err
		}
		templatesByKey[key] = templateView.Template
	}
	_ = actorID
	key := onboardingcatalog.ChecklistTemplateKey
	if mode == templatecatalog.FixedOrigin {
		key = onboardingcatalog.OriginTemplateKey
	}
	return segmentView.Version, "REAL_ESTATE", templatesByKey[key], nil
}

func (s Service) ensureParticipant(ctx context.Context, tenantID, unitID identity.ID, submission onboardingsession.Submission) (participantcore.ParticipantView, error) {
	payload := submission.Steps[onboardingsession.StepParticipant]
	name, email := submission.Session.Owner.Name, submission.Session.Owner.Email
	if strings.EqualFold(stepString(payload, "mode"), "DELEGATE") {
		name, email = stepString(payload, "name"), stepString(payload, "email")
	}
	participants := participantcore.Service{DB: s.DB, Authorizer: auth.Authorizer{}}
	view, err := participants.Upsert(ctx, tenantID, unitID, nil, 0, name, "PROPERTY_OWNER", "onboarding:participant:"+submission.Session.ID.String(), []participantcore.ContactInput{{Channel: "EMAIL", Value: email}})
	if err != nil {
		return participantcore.ParticipantView{}, err
	}
	if len(view.Contacts) == 0 {
		return participantcore.ParticipantView{}, errors.New("onboarding participant has no contact")
	}
	if _, err := participants.Verify(ctx, tenantID, view.Contacts[0].ID, true, "onboarding:verification:"+submission.Session.ID.String()); err != nil {
		return participantcore.ParticipantView{}, err
	}
	return participants.SetChannels(ctx, tenantID, view.Participant.ID, []identity.ID{view.Contacts[0].ID}, view.Participant.Version)
}

func (s Service) onboardingOwner(ctx context.Context, tenantID identity.ID, subject string) (database.BusinessUnit, database.Membership, error) {
	var unit database.BusinessUnit
	var member database.Membership
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND status = 'ACTIVE'", tenantID).Order("created_at, id").First(&unit).Error; err != nil {
			return err
		}
		// Public onboarding has no authenticated OIDC request context. The
		// membership RLS policy therefore needs the verified owner's issuer and
		// subject explicitly before this lookup can see the provisioned row.
		if err := tx.Exec(`SELECT set_config('app.oidc_issuer', ?, true)`, s.OwnerIssuer).Error; err != nil {
			return err
		}
		if err := tx.Exec(`SELECT set_config('app.oidc_subject', ?, true)`, subject).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id = ? AND subject = ?", tenantID, subject).First(&member).Error
	})
	return unit, member, err
}

func (s Service) existing(ctx context.Context, tenantID, sessionID identity.ID) (database.OnboardingRequest, bool, error) {
	var request database.OnboardingRequest
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("session_id = ?", sessionID).First(&request).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return database.OnboardingRequest{}, false, nil
	}
	return request, err == nil, err
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s Service) within(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if s.Within != nil {
		return s.Within(ctx, tenantID, fn)
	}
	return (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, fn)
}

func completedResult(session onboardingsession.Session, request database.OnboardingRequest, inspectionID identity.ID) Result {
	originStatus := "NOT_REQUIRED"
	if request.OriginVersionID != nil {
		originStatus = "ACTIVE"
	}
	return Result{Session: session, Request: request, InspectionID: inspectionID, State: coordinator.StateSubmitted, NextAction: "WAIT_FOR_DELIVERY", OriginStatus: originStatus, Delivery: deliveryPending}
}

func pendingResult(session onboardingsession.Session, decision coordinator.SubmitResult) Result {
	return Result{
		Session:      session,
		State:        decision.State,
		NextAction:   decision.NextAction,
		OriginStatus: decision.OriginStatus,
		Delivery:     decision.DeliveryStatus,
	}
}

func correlationID(ctx context.Context) string {
	if metadata, ok := requestctx.FromContext(ctx); ok && metadata.CorrelationID != "" {
		return metadata.CorrelationID
	}
	return identity.NewID().String()
}

func stepString(payload coordinator.StepPayload, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func deadlineAt(payload coordinator.StepPayload) (time.Time, error) {
	value := stepString(payload, "deadline")
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, apperror.New(apperror.InvalidInput, "deadline", "invalid inspection deadline")
	}
	return date.Add(24*time.Hour - time.Nanosecond), nil
}

func limitedName(value string) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= templatecatalog.MaxNameCodePoints {
		return value
	}
	runes := []rune(value)
	return string(runes[:templatecatalog.MaxNameCodePoints])
}
