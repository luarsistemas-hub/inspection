package resolvers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	recordanswer "inspection/services/inspection/internal/features/capture/record_component_answer"
	handoverrecord "inspection/services/inspection/internal/features/capture/upsert_handover_record"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

func parseDeviceCaptureTime(value *string) (time.Time, error) {
	if value == nil {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return time.Time{}, apperror.New(apperror.InvalidInput, "capturedAt", "Informe uma data e hora válida no formato RFC 3339.")
	}
	return parsed, nil
}

func (r *mutationResolver) recordComponentAnswer(ctx context.Context, input graphql1.RecordComponentAnswerInput) (*graphql1.ComponentAnswerPayload, error) {
	payload := &graphql1.ComponentAnswerPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	tenantID, responsibilityID, err := r.externalResponsibility(ctx)
	if err != nil {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
		return payload, nil
	}
	evidenceIDs := make([]identity.ID, 0, len(input.EvidenceIds))
	for _, raw := range input.EvidenceIds {
		id, parseErr := identity.ParseID(raw)
		if parseErr != nil {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(apperror.New(apperror.InvalidInput, "evidenceIds", "Uma das evidências informadas é inválida.")))
			return payload, nil
		}
		evidenceIDs = append(evidenceIDs, id)
	}
	viewKey, reason := "", ""
	if input.ViewKey != nil {
		viewKey = *input.ViewKey
	}
	if input.Reason != nil {
		reason = *input.Reason
	}
	result, err := r.Bus.Send(ctx, recordanswer.Command{TenantID: tenantID, ResponsibilityID: responsibilityID, RequirementKey: input.RequirementKey, ComponentKey: input.ComponentKey, ViewKey: viewKey, State: input.State, Reason: reason, EvidenceIDs: evidenceIDs, ExpectedVersion: int64(input.ExpectedVersion)})
	if err != nil {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
		return payload, nil
	}
	answer, ok := result.(database.ComponentAnswer)
	if !ok {
		return nil, errors.New("capture component answer handler returned an invalid result")
	}
	payload.Answer = mapComponentCaptureAnswer(answer)
	return payload, nil
}

func (r *mutationResolver) upsertHandoverRecord(ctx context.Context, input graphql1.UpsertHandoverRecordInput) (*graphql1.HandoverRecordPayload, error) {
	payload := &graphql1.HandoverRecordPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	tenantID, responsibilityID, err := r.externalResponsibility(ctx)
	if err != nil {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
		return payload, nil
	}
	encoded, err := json.Marshal(input.Payload)
	if err != nil {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(apperror.New(apperror.InvalidInput, "payload", "Informe os dados da declaração.")))
		return payload, nil
	}
	mediaIDs := make([]identity.ID, 0, len(input.MediaIds))
	for _, raw := range input.MediaIds {
		id, parseErr := identity.ParseID(raw)
		if parseErr != nil {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(apperror.New(apperror.InvalidInput, "mediaIds", "Uma das mídias informadas é inválida.")))
			return payload, nil
		}
		mediaIDs = append(mediaIDs, id)
	}
	result, err := r.Bus.Send(ctx, handoverrecord.Command{TenantID: tenantID, ResponsibilityID: responsibilityID, RecordKey: input.RecordKey, Kind: input.Kind, Declarant: input.Declarant, Payload: encoded, MediaIDs: mediaIDs, ExpectedVersion: int64(input.ExpectedVersion)})
	if err != nil {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
		return payload, nil
	}
	record, ok := result.(database.HandoverRecord)
	if !ok {
		return nil, errors.New("capture handover handler returned an invalid result")
	}
	payload.Record, err = mapHandoverCaptureRecord(record)
	if err != nil {
		return nil, errors.New("capture handover result could not be projected")
	}
	return payload, nil
}

func (r *queryResolver) inspectionCoverage(ctx context.Context, inspectionID string) (*graphql1.InspectionCoverage, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	id, err := identity.ParseID(inspectionID)
	if err != nil {
		return nil, invalidID("inspectionId")
	}
	if _, err := r.authorizeOperation(ctx, meta, auth.DashboardProduct, false, auth.Manager, auth.Employee, auth.Viewer); err != nil {
		return nil, err
	}
	if _, err := r.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: meta.TenantID, Product: auth.DashboardProduct, Roles: []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}, Resource: &requestctx.Scope{Kind: "INSPECTION", ID: id}}); err != nil {
		return nil, err
	}
	var inspection database.Inspection
	var responsibility database.Responsibility
	if err := (tenanttx.Runner{DB: r.DB}).Within(ctx, meta.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", meta.TenantID, id).Take(&inspection).Error; err != nil {
			return err
		}
		// A recapture adds a responsibility to the inspection; coverage is
		// measured on the original capture, which is the oldest one.
		return tx.Where("tenant_id=? AND inspection_id=?", meta.TenantID, id).Order("created_at ASC, id ASC").Take(&responsibility).Error
	}); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.New(apperror.NotFound, "inspectionId", "A vistoria não foi encontrada.")
		}
		return nil, err
	}
	summary, err := (capturecore.Service{DB: r.DB, Within: r.WithinTenant}).Coverage(ctx, meta.TenantID, responsibility.ID)
	if err != nil {
		return nil, err
	}
	return mapInspectionCoverage(summary), nil
}
