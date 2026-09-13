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
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

const (
	requestStatusSubmitted = "SUBMITTED"
	deliveryPending        = "PENDING"
)

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
	DB       *gorm.DB
	Bus      *mediator.Bus
	Sessions onboardingsession.Service
	Now      func() time.Time
}

// Complete is idempotent per onboarding session. Browser values are ignored;
// only immutable checkpoints loaded through the verified cookie/CSRF pair are
// materialized.
func (s Service) Complete(ctx context.Context, locator, csrf, idempotencyKey string) (Result, error) {
	if s.DB == nil || s.Bus == nil {
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
		return completedResult(current, existing, *existing.InspectionID), nil
	}

	now := s.now()
	originMode := strings.ToUpper(stepString(submission.Steps[onboardingsession.StepOrigin], "mode"))
	templateMode := templatecatalog.ChecklistOnly
	originStatus := "NOT_REQUIRED"
	if originMode == string(templatecatalog.FixedOrigin) {
		templateMode = templatecatalog.FixedOrigin
		originStatus = "PENDING"
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

	segment, profile, template, err := s.ensureCatalog(domainCtx, tenantID, member.IdentityID, templateMode)
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
	deadline, err := deadlineAt(property)
	if err != nil {
		return Result{}, err
	}
	inspection, err := (inspectioncore.Service{DB: s.DB, Bus: s.Bus, Authorizer: auth.Authorizer{}, Now: s.Now}).Create(domainCtx, inspectioncore.CreateInput{
		TenantID: tenantID, AssetID: asset.Asset.ID, ParticipantID: participant.Participant.ID, TemplateID: &template.ID,
		Source: inspectioncore.SourceManual, SourceKey: "onboarding:" + submission.Session.ID.String(),
		Reason: "Primeira vistoria criada pelo onboarding", DueAt: now, DeadlineAt: deadline,
	})
	if err != nil {
		return Result{}, fmt.Errorf("create onboarding inspection: %w", err)
	}
	_ = profile // The active profile is pinned inside the template snapshot.

	request := database.OnboardingRequest{
		ID: identity.NewID(), TenantID: tenantID, SessionID: submission.Session.ID,
		InspectionID: &inspection.Inspection.ID, AssetID: &asset.Asset.ID, ParticipantID: &participant.Participant.ID,
		TemplateID: &template.ID, Status: requestStatusSubmitted, IdempotencyKey: idempotencyKey, CreatedAt: now, UpdatedAt: now,
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
	return completedResult(current, request, *request.InspectionID), nil
}

func (s Service) ensureCatalog(ctx context.Context, tenantID, actorID identity.ID, mode templatecatalog.ComparisonMode) (database.SegmentDefinitionVersion, database.AnalysisProfileVersion, database.Template, error) {
	segmentService := segmentcore.Service{DB: s.DB, Authorizer: auth.Authorizer{}}
	segmentView, err := segmentService.Publish(ctx, tenantID, "real-estate", "Imóveis", "onboarding:segment:v1", []byte(`{"type":"object","properties":{"propertyType":{"type":"string","maxLength":100},"rooms":{"type":"integer"},"purpose":{"type":"string","maxLength":100}},"required":["propertyType","rooms","purpose"],"additionalProperties":false}`), []byte(`{}`))
	if err != nil {
		return database.SegmentDefinitionVersion{}, database.AnalysisProfileVersion{}, database.Template{}, err
	}
	if _, err := segmentService.Activate(ctx, tenantID, segmentView.Version.ID, segmentView.Definition.Version); err != nil {
		return database.SegmentDefinitionVersion{}, database.AnalysisProfileVersion{}, database.Template{}, err
	}
	templates := templatecore.Service{DB: s.DB, Bus: s.Bus, Authorizer: auth.Authorizer{}}
	profilePayload := []byte(`{"schemaVersion":1,"modelAlias":"inspection-default","promptVersion":"v1","outputSchema":{"type":"object"},"minimumConfidenceBps":0}`)
	profile, err := templates.PublishProfile(ctx, tenantID, onboardingcatalog.AnalysisProfileKey, "onboarding:profile:v1", profilePayload)
	if err != nil {
		return database.SegmentDefinitionVersion{}, database.AnalysisProfileVersion{}, database.Template{}, err
	}
	documents := onboardingcatalog.TemplateDocuments()
	key := onboardingcatalog.ChecklistTemplateKey
	if mode == templatecatalog.FixedOrigin {
		key = onboardingcatalog.OriginTemplateKey
	}
	document := documents[key]
	document.SegmentVersionID = segmentView.Version.ID.String()
	document.AnalysisProfile = profile.ID.String()
	payload, err := json.Marshal(document)
	if err != nil {
		return database.SegmentDefinitionVersion{}, database.AnalysisProfileVersion{}, database.Template{}, err
	}
	templateName := "Primeira vistoria do imóvel"
	if key == onboardingcatalog.OriginTemplateKey {
		templateName = "Vistoria comparativa do imóvel"
	}
	templateView, err := templates.Publish(ctx, tenantID, key, templateName, "onboarding:template:"+key+":v2", payload)
	if err != nil {
		return database.SegmentDefinitionVersion{}, database.AnalysisProfileVersion{}, database.Template{}, err
	}
	templateView, err = templates.Activate(ctx, tenantID, templateView.Version.ID, templateView.Template.Version)
	if err != nil {
		return database.SegmentDefinitionVersion{}, database.AnalysisProfileVersion{}, database.Template{}, err
	}
	_ = actorID
	return segmentView.Version, profile, templateView.Template, nil
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

func completedResult(session onboardingsession.Session, request database.OnboardingRequest, inspectionID identity.ID) Result {
	return Result{Session: session, Request: request, InspectionID: inspectionID, State: coordinator.StateSubmitted, NextAction: "WAIT_FOR_DELIVERY", OriginStatus: "NOT_REQUIRED", Delivery: deliveryPending}
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
