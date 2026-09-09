# ADR-004: Versioned Resource Lifecycle, Origin Provenance, and Retention

## Status

Accepted

## Date

2026-09-08

## Context

Participants, segments, templates, assets, origins, and policies require complete management, but many are audit-sensitive and should not be physically deleted through ordinary CRUD. Origin images need both guided capture and administrative upload. Privacy and retention workflows must distinguish logical deletion, legal hold, physical purge, and residual audit evidence.

## Decision

Use create, update, activate, deactivate, invalidate, archive, and version semantics for normal resource lifecycles. Do not expose ordinary hard delete for audit-sensitive resources.

Support origin images through two channels:

1. Invited Capture flow with OTP, consent, provenance, and capture metadata.
2. Administrative upload marked as `ADMIN_UPLOAD`, with original file preservation, actor, declared source, capture date, justification, and audit history.

Both channels create immutable origin versions. Administrators activate or invalidate versions without overwriting original media.

Only authorized administrators may initiate deletion. The initial action is an automatic soft delete that removes the resource from active journeys. A scheduled physical purge occurs after the configured retention period unless blocked by legal hold. Purge removes personal data, media, and operational content but retains a non-sensitive audit tombstone containing technical identifier, resource type, dates, actor, and reason.

## Alternatives Considered

### Alternative 1: Traditional hard-delete CRUD

- **Description**: Permanently delete resources directly when no dependency is detected.
- **Pros**: Familiar CRUD semantics.
- **Cons**: Breaks provenance, audit, and version history.
- **Why rejected**: The user selected archive/version semantics.

### Alternative 2: Capture-only origins

- **Description**: Require every origin image to be recorded through an invitation.
- **Pros**: Strong and uniform capture provenance.
- **Cons**: Prevents migration of legitimate existing imagery.
- **Why rejected**: Administrative upload was explicitly requested.

### Alternative 3: Retain soft-deleted data indefinitely

- **Description**: Never physically purge records.
- **Pros**: Maximum recoverability.
- **Cons**: Conflicts with retention and privacy objectives.
- **Why rejected**: Automatic eventual purge was selected.

### Alternative 4: Remove every audit trace

- **Description**: Delete both content and all event evidence.
- **Pros**: Minimal residual data.
- **Cons**: Prevents proof that a governed deletion occurred.
- **Why rejected**: A non-sensitive tombstone was selected.

## Consequences

### Positive

- Resource and media history remains understandable.
- Existing source images can be onboarded without misrepresenting their provenance.
- Privacy deletion and auditability coexist.
- Legal holds provide a clear purge boundary.

### Negative

- Lifecycle states are more complex than simple CRUD.
- Administrative upload requires additional provenance fields.
- Purge jobs must span related data and object storage safely.

### Risks

- Soft-deleted data may leak into active queries; mitigate with default exclusion and explicit privileged recovery views.
- A purge may remove data still under hold; mitigate with final transactional hold checks and auditable purge decisions.
- Tombstones may retain personal content accidentally; mitigate with a strict allowlist of residual fields.

## Implementation Notes

- Bulk operations may import participants and assets, export filtered collections, and apply only safe, explainable changes.
- Bulk validation must provide item-level results and must not partially hide failures.
- Version conflicts must never overwrite newer data silently.
- Archive and soft-delete states must be excluded from new assignments by default.

## References

- `services/inspection/internal/features/retention`
- `services/inspection/internal/features/assets`
- `services/inspection/internal/features/invitations`
- `services/inspection/schema.graphqls`

