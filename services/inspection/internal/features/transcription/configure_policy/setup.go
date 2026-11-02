package configure_policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/transcription/core"

	"gorm.io/gorm"
)

// ConfigureInput contains an optimistic tenant policy edit.
type ConfigureInput struct {
	TenantID         identity.ID
	Policy           core.Policy
	ExpectedVersion  int
	ClientMutationID string
}

// Setup persists a policy edit atomically and serializes concurrent updates per tenant.
func Setup(ctx context.Context, db *gorm.DB, in ConfigureInput) (core.Policy, error) {
	if db == nil || in.TenantID == (identity.ID{}) || in.ExpectedVersion < 0 {
		return core.Policy{}, core.ErrInvalidInput
	}
	if err := in.Policy.Validate(); err != nil {
		return core.Policy{}, err
	}
	var result core.Policy
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", in.TenantID.String()).Error; err != nil {
				return err
			}
		}
		// Lock the parent tenant row so concurrent first-time policy writes serialize too.
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT id FROM tenancy.tenants WHERE id = ? FOR UPDATE", in.TenantID).Error; err != nil {
				return err
			}
		}
		mutationDigest := ""
		if in.ClientMutationID != "" {
			payload, _ := json.Marshal(struct {
				Policy          core.Policy
				ExpectedVersion int
			}{in.Policy, in.ExpectedVersion})
			digest := sha256.Sum256(payload)
			mutationDigest = hex.EncodeToString(digest[:])
			var receipt struct {
				ClientMutationID       string
				PayloadDigest          string
				Enabled                bool
				TenantNoteLimit        int
				TenantAudioLimitMS     int64
				InspectionNoteLimit    int
				InspectionAudioLimitMS int64
				ResultVersion          int
			}
			if err := tx.Raw("SELECT client_mutation_id,payload_digest,enabled,tenant_note_limit,tenant_audio_limit_ms,inspection_note_limit,inspection_audio_limit_ms,result_version FROM transcription.policy_mutation_receipts WHERE tenant_id=? AND client_mutation_id=?", in.TenantID, in.ClientMutationID).Scan(&receipt).Error; err != nil {
				return err
			}
			if receipt.ClientMutationID != "" {
				if receipt.PayloadDigest != mutationDigest {
					return core.ErrConflict
				}
				result = core.Policy{Enabled: receipt.Enabled, TenantNoteLimit: receipt.TenantNoteLimit, TenantAudioLimitMS: receipt.TenantAudioLimitMS, InspectionNoteLimit: receipt.InspectionNoteLimit, InspectionAudioLimitMS: receipt.InspectionAudioLimitMS, Version: receipt.ResultVersion}
				return nil
			}
		}
		var current struct {
			Version int `gorm:"column:version"`
		}
		query := "SELECT version FROM transcription.tenant_policies WHERE tenant_id = ?"
		if tx.Dialector.Name() == "postgres" {
			query += " FOR UPDATE"
		}
		err := tx.Raw(query, in.TenantID).Scan(&current).Error
		if err != nil {
			return err
		}
		if current.Version != in.ExpectedVersion {
			return core.ErrConflict
		}
		version := current.Version + 1
		now := time.Now().UTC()
		row := map[string]any{
			"tenant_id": in.TenantID, "enabled": in.Policy.Enabled,
			"tenant_note_limit": in.Policy.TenantNoteLimit, "tenant_audio_limit_ms": in.Policy.TenantAudioLimitMS,
			"inspection_note_limit": in.Policy.InspectionNoteLimit, "inspection_audio_limit_ms": in.Policy.InspectionAudioLimitMS,
			"version": version, "updated_at": now,
		}
		if current.Version == 0 {
			row["created_at"] = now
			if err := tx.Table("transcription.tenant_policies").Create(&row).Error; err != nil {
				return err
			}
		} else if err := tx.Table("transcription.tenant_policies").Where("tenant_id = ? AND version = ?", in.TenantID, current.Version).Updates(row).Error; err != nil {
			return err
		}
		result = in.Policy
		result.Version = version
		if in.ClientMutationID != "" {
			if err := tx.Table("transcription.policy_mutation_receipts").Create(&map[string]any{
				"tenant_id": in.TenantID, "client_mutation_id": in.ClientMutationID, "payload_digest": mutationDigest,
				"enabled": result.Enabled, "tenant_note_limit": result.TenantNoteLimit, "tenant_audio_limit_ms": result.TenantAudioLimitMS,
				"inspection_note_limit": result.InspectionNoteLimit, "inspection_audio_limit_ms": result.InspectionAudioLimitMS,
				"result_version": result.Version, "created_at": now,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = core.ErrConflict
	}
	return result, err
}
