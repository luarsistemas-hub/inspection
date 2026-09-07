package invite_capture

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	linkdelivery "inspection/services/inspection/internal/features/invitations/link_delivery"
	origincore "inspection/services/inspection/internal/features/origins/core"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

type Command struct {
	TenantID, AssetID, ParticipantID identity.ID
	ExpiresAt                        time.Time
	IdempotencyKey                   string
}

type Dependencies struct {
	DB            *gorm.DB
	Bus           *mediator.Bus
	Service       origincore.Service
	Notifications *notifications.Registry
	Authorizer    auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil || d.Service.DB == nil || d.Notifications == nil {
		return fmt.Errorf("slice origins/invite_capture: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		assetRaw, err := d.Bus.Ask(ctx, assetget.Query{TenantID: command.TenantID, AssetID: command.AssetID})
		if err != nil {
			return nil, err
		}
		asset := assetRaw.(assetcore.View)
		if _, err := d.Authorizer.Authorize(ctx, command.TenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.Asset.BusinessUnitID}, true); err != nil {
			return nil, err
		}
		if asset.Asset.Status != "ACTIVE" || asset.Asset.TemplateID == nil {
			return nil, apperror.New(apperror.InvalidState, "assetId", "active configured asset is required")
		}
		assigned := false
		for _, assignment := range asset.Assignments {
			assigned = assigned || (assignment.Active && assignment.ParticipantID == command.ParticipantID)
		}
		if !assigned {
			return nil, apperror.New(apperror.InvalidInput, "participantId", "participant is not assigned to the asset")
		}
		participantRaw, err := d.Bus.Ask(ctx, participantget.Query{TenantID: command.TenantID, ParticipantID: command.ParticipantID})
		if err != nil {
			return nil, err
		}
		participant := participantRaw.(participantcore.ParticipantView)
		delivery := selectedDeliveries(participant)
		if len(delivery) == 0 {
			return nil, apperror.New(apperror.InvalidState, "participantId", "a selected verified delivery channel is required")
		}
		templateRaw, err := d.Bus.Ask(ctx, templateresolve.Query{TenantID: command.TenantID, TemplateID: *asset.Asset.TemplateID})
		if err != nil {
			return nil, err
		}
		template := templateRaw.(templateresolve.Result)
		var document catalog.TemplateDocument
		if err := json.Unmarshal(template.Version.DefinitionJSON, &document); err != nil {
			return nil, apperror.Wrap(apperror.InvalidState, err)
		}
		var overrides catalog.PolicyOverride
		_ = json.Unmarshal(asset.Asset.PolicyOverrides, &overrides)
		policy, err := catalog.ResolvePolicy(document.Policy, overrides, catalog.ChecklistOnly, "")
		if err != nil {
			return nil, apperror.New(apperror.InvalidState, "policy", "capture policy is invalid")
		}
		policyJSON, _ := json.Marshal(map[string]any{"gpsRequired": policy.GPSRequired, "geofenceMeters": policy.GeofenceMeters, "allowGallery": policy.AllowGallery, "assetLatitudeE6": asset.Asset.LatitudeE6, "assetLongitudeE6": asset.Asset.LongitudeE6})
		requirements := make([]capturecore.Requirement, 0, len(document.Requirements))
		for _, requirement := range document.Requirements {
			requirements = append(requirements, capturecore.Requirement{
				Key: requirement.Key, Section: requirement.Section, Label: requirement.Label,
				Instructions: requirement.Instructions, EvidenceKind: requirement.EvidenceKind,
				Required: requirement.Required, ImpossibilityAllowed: true,
				MinimumMedia: requirement.MinimumCount, MaximumMedia: requirement.MaximumCount,
				DescriptionRequired: requirement.DescriptionRequired,
				CaptureSourcePolicy: requirement.CaptureSourcePolicy,
				ComparisonTarget:    string(requirement.ComparisonTarget),
			})
		}
		created, err := d.Service.Invite(ctx, origincore.InviteInput{TenantID: command.TenantID, AssetID: command.AssetID, TemplateID: *asset.Asset.TemplateID, TemplateVersionID: template.Version.ID, Delivery: delivery, Requirements: requirements, Policy: policyJSON, ExpiresAt: command.ExpiresAt, IdempotencyKey: command.IdempotencyKey})
		if err != nil {
			return nil, err
		}
		if created.LinkToken == "" {
			created.LinkToken, err = linkdelivery.RetryToken(ctx, d.DB, command.TenantID, created.InvitationID)
			if err != nil {
				return nil, err
			}
		}
		if created.LinkToken != "" {
			if err := linkdelivery.Deliver(ctx, d.DB, d.Notifications, command.TenantID, created.InvitationID, created.LinkToken, delivery, "inspection-capture-link"); err != nil {
				return nil, err
			}
		}
		return created, nil
	})
}

func selectedDeliveries(participant participantcore.ParticipantView) []invitationcore.DeliveryIntent {
	selected := map[identity.ID]bool{}
	for _, id := range participant.Selected {
		selected[id] = true
	}
	result := []invitationcore.DeliveryIntent{}
	for _, contact := range participant.Contacts {
		if selected[contact.ID] && participant.Verified[contact.ID] && contact.Active {
			result = append(result, invitationcore.DeliveryIntent{Channel: contact.Channel, Destination: contact.Value})
		}
	}
	return result
}
