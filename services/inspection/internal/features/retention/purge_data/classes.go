package purge_data

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SecretsPurge counts what one security-class purge removed.
type SecretsPurge struct{ OTPChallenges, Sessions, Invitations int64 }

// Total is the number of secrets the purge removed.
func (p SecretsPurge) Total() int64 { return p.OTPChallenges + p.Sessions + p.Invitations }

// PurgeLocation irreversibly nulls the capture location (latitude, longitude,
// accuracy and distance) of every evidence item of an inspection once its
// operational clock elapsed. The evidence itself stays until the evidence class
// expires. It is idempotent: a repeated call finds no location, removes nothing
// and writes no second audit event. It returns how many items lost a location.
func PurgeLocation(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, clock retentioncore.Clock, correlationID string) (int64, error) {
	if tx == nil || tenantID == (identity.ID{}) || inspectionID == (identity.ID{}) {
		return 0, fmt.Errorf("retention location purge: missing identity")
	}
	now := time.Now().UTC()
	if !clock.Eligible(now) {
		return 0, apperror.New(apperror.InvalidState, "retention", "O prazo de retenção não terminou ou há retenção legal ativa.")
	}
	tx = tx.WithContext(ctx)
	if err := lockInspectionAndCheckLegalHold(tx, tenantID, inspectionID); err != nil {
		return 0, err
	}
	if tx.Dialector.Name() == "postgres" {
		if err := tx.Exec("SELECT set_config('app.retention_location', 'on', true)").Error; err != nil {
			return 0, err
		}
	}
	result := tx.Model(&database.MediaObject{}).
		Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?) AND "+retentioncore.LocationColumnsPresentSQL("media.media_objects"), tenantID, tenantID, inspectionID).
		Updates(map[string]any{"latitude_e6": nil, "longitude_e6": nil, "accuracy_mm": nil, "distance_mm": nil})
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		return 0, nil
	}
	reason := fmt.Sprintf("location removed after operational retention; class=%s start=%s due=%s items=%d", clock.Class, clock.StartsAt.UTC().Format(time.RFC3339), clock.DueAt.UTC().Format(time.RFC3339), result.RowsAffected)
	if err := tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: tenantID, ActorID: identity.ID{}, Action: retentioncore.PurgeActionLocation, TargetType: "inspection", TargetID: inspectionID.String(), Outcome: "COMPLETED", Reason: reason, CorrelationID: correlationID, OccurredAt: now}).Error; err != nil {
		return 0, err
	}
	return result.RowsAffected, nil
}

// PurgeSecrets removes the invitation, OTP and session secrets of the tenant
// whose own expiry plus the security grace period elapsed: OTP challenges and
// external sessions are deleted and the link digests of expired invitations are
// replaced by random digests (the invitation row stays as lineage). Each call
// is idempotent, and the audit event is written only when something was removed.
func PurgeSecrets(ctx context.Context, tx *gorm.DB, tenantID identity.ID, now time.Time, policy retentioncore.Policy, correlationID string) (SecretsPurge, error) {
	var purge SecretsPurge
	if tx == nil || tenantID == (identity.ID{}) {
		return purge, fmt.Errorf("retention secrets purge: missing identity")
	}
	tx = tx.WithContext(ctx)
	now = now.UTC()
	cutoff := retentioncore.SecretsCutoff(now, policy)
	otp := tx.Exec("DELETE FROM invitations.otp_challenges WHERE tenant_id=? AND expires_at<=?", tenantID, cutoff)
	if otp.Error != nil {
		return purge, otp.Error
	}
	sessions := tx.Exec("DELETE FROM invitations.external_sessions WHERE tenant_id=? AND expires_at<=?", tenantID, cutoff)
	if sessions.Error != nil {
		return purge, sessions.Error
	}
	invitations := tx.Exec(`UPDATE invitations.invitations
		SET token_hash=sha256(convert_to(gen_random_uuid()::text || clock_timestamp()::text, 'UTF8')), previous_token_hash=NULL, secrets_purged_at=?
		WHERE tenant_id=? AND secrets_purged_at IS NULL AND expires_at<=?`, now, tenantID, cutoff)
	if invitations.Error != nil {
		return purge, invitations.Error
	}
	purge = SecretsPurge{OTPChallenges: otp.RowsAffected, Sessions: sessions.RowsAffected, Invitations: invitations.RowsAffected}
	if purge.Total() == 0 {
		return purge, nil
	}
	reason := fmt.Sprintf("expired secrets removed; class=%s otp_challenges=%d sessions=%d invitations=%d", retentioncore.Secrets, purge.OTPChallenges, purge.Sessions, purge.Invitations)
	err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.AuditEvent{ID: identity.NewID(), TenantID: tenantID, ActorID: identity.ID{}, Action: retentioncore.PurgeActionSecrets, TargetType: "tenant", TargetID: tenantID.String(), Outcome: "COMPLETED", Reason: reason, CorrelationID: correlationID, OccurredAt: now}).Error
	return purge, err
}
