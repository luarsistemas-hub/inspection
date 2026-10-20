// Package request_video_replacement asks a participant for a new recording of a blocked video.
package request_video_replacement

import (
	"context"
	"errors"
	"fmt"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// Command requests a replacement recording; the input tenant comes from the request context.
type Command struct {
	Input recapturecore.VideoReplacementInput
}

// Dependencies are the collaborators needed to authorize, persist and deliver the request.
type Dependencies struct {
	DB             *gorm.DB
	Bus            *mediator.Bus
	Service        recapturecore.Service
	Notifications  notificationcore.NotificationService
	CaptureBaseURL string
	Authorizer     auth.Authorizer
	// Within runs a tenant-scoped transaction; it defaults to the production runner.
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Setup registers the replacement command on the mediator.
func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil || d.Service.DB == nil || d.Notifications == nil || d.CaptureBaseURL == "" {
		return fmt.Errorf("slice recapture/request_video_replacement: missing dependency")
	}
	within := d.Within
	if within == nil {
		within = (tenanttx.Runner{DB: d.DB}).Within
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		input := raw.(Command).Input
		meta, ok := requestctx.FromContext(ctx)
		if !ok || !meta.Principal.Internal || meta.Principal.IdentityID == (identity.ID{}) {
			return nil, apperror.New(apperror.Forbidden, "", "permission denied")
		}
		var inspection database.Inspection
		if err := within(ctx, input.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", input.TenantID, input.InspectionID).Take(&inspection).Error
		}); errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
		} else if err != nil {
			return nil, err
		}
		if _, err := d.Authorizer.Authorize(ctx, input.TenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "INSPECTION", ID: inspection.ID}, true); err != nil {
			return nil, err
		}
		participantRaw, err := d.Bus.Ask(ctx, participantget.Query{TenantID: input.TenantID, ParticipantID: inspection.ParticipantID})
		if err != nil {
			return nil, err
		}
		participant := participantRaw.(participantcore.ParticipantView)
		input.Delivery = deliveries(participant)
		input.ActorID = meta.Principal.IdentityID
		result, err := d.Service.RequestVideoReplacement(ctx, input)
		if err != nil {
			return nil, err
		}
		if result.LinkToken != "" {
			assetName, assetAddress := "", ""
			if rawAsset, err := d.Bus.Ask(ctx, assetget.Query{TenantID: input.TenantID, AssetID: inspection.AssetID}); err == nil {
				if view, ok := rawAsset.(assetcore.View); ok {
					assetName, assetAddress = view.Asset.Name, view.Asset.Address
				}
			}
			if err := deliver(ctx, d.Notifications, d.CaptureBaseURL, input.TenantID, inspection.ID, result, participant.Participant.Name, assetName, assetAddress, input.Delivery); err != nil {
				return nil, err
			}
		}
		return result, nil
	})
}

func deliveries(participant participantcore.ParticipantView) []invitationcore.DeliveryIntent {
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

func deliver(ctx context.Context, service notificationcore.NotificationService, baseURL string, tenantID, inspectionID identity.ID, result recapturecore.VideoReplacementResult, recipientName, assetName, assetAddress string, delivery []invitationcore.DeliveryIntent) error {
	for _, target := range delivery {
		channel := notificationcore.Channel(target.Channel)
		template, variables := notificationcore.CaptureLinkNotification(channel, recipientName, assetName, assetAddress, result.DeadlineAt)
		_, err := service.Send(ctx, notificationcore.Notification{TenantID: tenantID, InspectionID: &inspectionID, InvitationID: &result.InvitationID, Recipient: notificationcore.Recipient{Destination: target.Destination}, Channel: channel, Template: template, Variables: variables, CorrelationID: "video-replacement-" + result.ID.String(), IdempotencyKey: notificationcore.RecipientIdempotencyKey(result.ID, channel, target.Destination), Execution: &notificationcore.ExecutionPayload{InvitationID: result.InvitationID, Token: result.LinkToken, URLVariable: "captureUrl", BaseURL: baseURL, ExpiresAt: result.DeadlineAt.Unix()}})
		if apperror.Is(err, apperror.IntegrationDisabled) {
			// Disabled channels are skipped so enabled channels still deliver.
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}
