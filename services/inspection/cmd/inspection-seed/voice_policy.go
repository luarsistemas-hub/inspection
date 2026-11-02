package main

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/transcription/configure_policy"
	"inspection/services/inspection/internal/features/transcription/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

const qaVoiceMutationPrefix = "qa-seed-voice-v1"

var qaVoicePolicy = core.Policy{
	Enabled:                true,
	TenantNoteLimit:        500,
	TenantAudioLimitMS:     300 * 60 * 1000,
	InspectionNoteLimit:    20,
	InspectionAudioLimitMS: 30 * 60 * 1000,
}

type voicePolicyReader interface {
	CurrentPolicy(context.Context, identity.ID) (core.Policy, bool, error)
}

type voicePolicyConfigurer interface {
	Configure(context.Context, configure_policy.ConfigureInput) (core.Policy, error)
}

type databaseVoicePolicy struct{ db *gorm.DB }

func (p databaseVoicePolicy) CurrentPolicy(ctx context.Context, tenantID identity.ID) (core.Policy, bool, error) {
	var policy core.Policy
	result := p.db.WithContext(ctx).Raw(`
		SELECT enabled, tenant_note_limit, tenant_audio_limit_ms,
		       inspection_note_limit, inspection_audio_limit_ms, version
		FROM transcription.tenant_policies WHERE tenant_id = ?`, tenantID).Scan(&policy)
	if result.Error != nil {
		return core.Policy{}, false, result.Error
	}
	return policy, result.RowsAffected != 0, nil
}

func (p databaseVoicePolicy) Configure(ctx context.Context, in configure_policy.ConfigureInput) (core.Policy, error) {
	return configure_policy.Setup(ctx, p.db, in)
}

func configureQAVoice(ctx context.Context, db *gorm.DB, issuer string) error {
	var membership database.Membership
	if err := db.WithContext(ctx).
		Where("issuer = ? AND role = ? AND status = ?", issuer, "TENANT_ADMIN", "ACTIVE").
		Order("created_at ASC").First(&membership).Error; err != nil {
		return fmt.Errorf("find QA tenant for voice policy: %w", err)
	}
	return applyQAVoicePolicy(ctx, membership.TenantID, databaseVoicePolicy{db: db}, databaseVoicePolicy{db: db})
}

func applyQAVoicePolicy(ctx context.Context, tenantID identity.ID, reader voicePolicyReader, configurer voicePolicyConfigurer) error {
	current, exists, err := reader.CurrentPolicy(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("read current voice policy: %w", err)
	}
	if exists && samePolicyLimits(current, qaVoicePolicy) {
		return nil
	}
	expectedVersion := current.Version
	_, err = configurer.Configure(ctx, configure_policy.ConfigureInput{
		TenantID:         tenantID,
		Policy:           qaVoicePolicy,
		ExpectedVersion:  expectedVersion,
		ClientMutationID: fmt.Sprintf("%s-v%d", qaVoiceMutationPrefix, expectedVersion),
	})
	if err != nil {
		return fmt.Errorf("configure QA voice policy: %w", err)
	}
	return nil
}

func samePolicyLimits(a, b core.Policy) bool {
	return a.Enabled == b.Enabled &&
		a.TenantNoteLimit == b.TenantNoteLimit &&
		a.TenantAudioLimitMS == b.TenantAudioLimitMS &&
		a.InspectionNoteLimit == b.InspectionNoteLimit &&
		a.InspectionAudioLimitMS == b.InspectionAudioLimitMS
}
