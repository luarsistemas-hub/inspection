package core

import "time"

// Retention classes and what each one removes (US-031 AC-1/AC-2, TechSpec
// Retention). Every class has its own scheduler job, purger, audit event and
// idempotency rule:
//
//	EvidenceReports  closure + EvidenceDays: the whole inspection (originals,
//	                 derivatives, reports, analysis and linked personal data).
//	Operational      closure + OperationalDays: the separately usable location
//	                 of every evidence item of the inspection, which are the
//	                 media.media_objects columns latitude_e6, longitude_e6,
//	                 accuracy_mm and distance_mm. The evidence stays; only the
//	                 location is irreversibly nulled.
//	Secrets          expires_at + SecurityDays: the invitation, OTP and session
//	                 secrets of the external capture flow, which are
//	                 invitations.otp_challenges (deleted),
//	                 invitations.external_sessions (deleted) and the token
//	                 digests of invitations.invitations (replaced by random
//	                 digests and stamped with secrets_purged_at).
//
// Not covered because the TechSpec does not enumerate them: other
// "operational records" than location, the internal-user invitation secrets
// (access.user_invitations and its OTP challenges), counterparty invitation
// tokens and the onboarding OTP challenges.
const (
	// PurgeActionLocation and PurgeActionSecrets name the audit event that each
	// class purge records.
	PurgeActionLocation = "retention.location_purged"
	PurgeActionSecrets  = "retention.secrets_purged"
)

// SecretsCutoff returns the instant before which a secret's own expiry makes it
// purgeable under policy.
func SecretsCutoff(now time.Time, policy Policy) time.Time { return now.Add(-policy.Security) }

// LocationColumnsPresentSQL is the predicate, over a media.media_objects row
// aliased alias, that holds while any location column is still stored.
func LocationColumnsPresentSQL(alias string) string {
	return "(" + alias + ".latitude_e6 IS NOT NULL OR " + alias + ".longitude_e6 IS NOT NULL OR " + alias + ".accuracy_mm IS NOT NULL OR " + alias + ".distance_mm IS NOT NULL)"
}
