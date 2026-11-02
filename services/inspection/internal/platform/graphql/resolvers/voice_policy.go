package resolvers

import (
	"context"
	"errors"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/transcription/configure_policy"
	"inspection/services/inspection/internal/features/transcription/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func resolveVoicePolicy(ctx context.Context, r *Resolver) (*graphql1.VoicePolicy, error) {
	var tenantID identity.ID
	// Capture requests may also carry operator metadata from a shared transport
	// or test harness. The external session is the authority for this read-only
	// query whenever capture credentials are present.
	if _, ok := requestctx.ExternalCredentialsFromContext(ctx); ok {
		resolvedTenant, _, err := r.externalResponsibility(ctx)
		if err != nil {
			return nil, err
		}
		tenantID = resolvedTenant
	} else if meta, ok := requestctx.FromContext(ctx); ok {
		if _, err := r.authorizeOperation(ctx, meta, auth.AdminProduct, false); err != nil {
			return nil, err
		}
		tenantID = meta.TenantID
	} else {
		return nil, unauthenticated()
	}
	policy := &graphql1.VoicePolicy{Enabled: false, Version: 0}
	err := withTask06Tenant(ctx, r.DB, tenantID, func(tx *gorm.DB) error {
		var row struct {
			Enabled                bool
			TenantNoteLimit        *int
			TenantAudioLimitMS     *int64
			InspectionNoteLimit    *int
			InspectionAudioLimitMS *int64
			Version                int
		}
		if err := tx.Raw("SELECT enabled, tenant_note_limit, tenant_audio_limit_ms, inspection_note_limit, inspection_audio_limit_ms, version FROM transcription.tenant_policies WHERE tenant_id = ?", tenantID).Scan(&row).Error; err != nil {
			return err
		}
		policy.Enabled, policy.Version = row.Enabled, row.Version
		policy.TenantNoteLimit = row.TenantNoteLimit
		policy.InspectionNoteLimit = row.InspectionNoteLimit
		policy.TenantAudioLimitMs = int64PointerToInt(row.TenantAudioLimitMS)
		policy.InspectionAudioLimitMs = int64PointerToInt(row.InspectionAudioLimitMS)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return policy, nil
}

func configureVoicePolicy(ctx context.Context, r *Resolver, input graphql1.ConfigureVoicePolicyInput) (*graphql1.VoicePolicyPayload, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if _, err := r.authorizeOperation(ctx, meta, auth.AdminProduct, true); err != nil {
		return nil, err
	}
	policy, err := configure_policy.Setup(ctx, r.DB, configure_policy.ConfigureInput{
		TenantID: meta.TenantID, ExpectedVersion: input.ExpectedVersion,
		ClientMutationID: input.ClientMutationID,
		Policy:           core.Policy{Enabled: input.Enabled, TenantNoteLimit: input.TenantNoteLimit, TenantAudioLimitMS: int64(input.TenantAudioLimitMs), InspectionNoteLimit: input.InspectionNoteLimit, InspectionAudioLimitMS: int64(input.InspectionAudioLimitMs)},
	})
	if err != nil {
		if errors.Is(err, core.ErrConflict) {
			return nil, apperror.New(apperror.Conflict, "expectedVersion", "A política de voz foi alterada. Atualize os dados e tente novamente.")
		}
		if errors.Is(err, core.ErrInvalidInput) {
			return nil, apperror.New(apperror.InvalidInput, "limits", "Os quatro limites de uso de voz devem ser maiores que zero.")
		}
		return nil, apperror.Wrap(apperror.Internal, err)
	}
	return &graphql1.VoicePolicyPayload{Policy: mapVoicePolicy(policy), UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}, nil
}

func mapVoicePolicy(p core.Policy) *graphql1.VoicePolicy {
	return &graphql1.VoicePolicy{Enabled: p.Enabled, TenantNoteLimit: intPointer(p.TenantNoteLimit), TenantAudioLimitMs: intPointer(int(p.TenantAudioLimitMS)), InspectionNoteLimit: intPointer(p.InspectionNoteLimit), InspectionAudioLimitMs: intPointer(int(p.InspectionAudioLimitMS)), Version: p.Version}
}

func int64PointerToInt(value *int64) *int {
	if value == nil {
		return nil
	}
	converted := int(*value)
	return &converted
}
