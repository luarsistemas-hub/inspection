# ADR-006: Fixed Cross-Environment Super Admin Credential

## Status

Accepted

## Date

2026-09-08

## Context

The product requires a super administrator identity that is available in every environment and automatically receives `TENANT_ADMIN` membership in every tenant. A safer model using separate secrets by environment, initial password rotation, and MFA was recommended. The user explicitly selected one shared static username and password for development, staging, and production and accepted the security exception.

## Decision

Provision a fixed `Admin` identity in every environment using the operator-supplied shared static credential. Automatically grant its identity an explicit `TENANT_ADMIN` membership in every tenant.

The credential value is intentionally omitted from this ADR and all PRD artifacts. It must not appear in frontend bundles, GraphQL responses, source control, logs, test snapshots, generated files, or user-facing documentation. Authentication remains owned by the configured OIDC identity provider.

## Alternatives Considered

### Alternative 1: Separate secret per environment with MFA

- **Description**: Keep the username stable, inject a unique password per environment, require rotation, and enforce MFA in production.
- **Pros**: Limits credential reuse and reduces production takeover risk.
- **Cons**: Requires environment-specific secret management and operational setup.
- **Why rejected**: The user explicitly selected the same static credential in all environments.

### Alternative 2: Disabled break-glass account

- **Description**: Provision the account but activate it only during recovery.
- **Pros**: Minimizes standing privilege.
- **Cons**: Adds an activation procedure and delays routine access.
- **Why rejected**: The requested account must remain directly usable.

### Alternative 3: Manual tenant membership

- **Description**: Add the identity to tenants individually.
- **Pros**: Explicit per-tenant enrollment.
- **Cons**: Can omit newly created tenants.
- **Why rejected**: Automatic provisioning was selected.

## Consequences

### Positive

- Operational access is predictable in every environment.
- Newly created tenants are immediately recoverable by the super administrator.
- No global cross-tenant business query is required.

### Negative

- Compromise in any environment compromises administrative access in all environments.
- Shared credentials weaken attribution among human operators.
- Password rotation and individual accountability are materially constrained.

### Risks

- **Critical credential reuse risk**: one disclosure grants control of every tenant in every environment.
- **Repository leakage risk**: accidental inclusion in source or generated artifacts exposes production access.
- **Audit attribution risk**: actions identify the shared account rather than a unique human.
- **Mitigation**: keep the value only in the identity-provider bootstrap secret path, redact authentication logs, audit every tenant entry and action, alert on use, and retain a documented emergency rotation procedure.

## Implementation Notes

- The local seed may read the configured value from an ignored local environment file.
- Non-local deployment must inject the same value through the platform secret mechanism, never a committed default.
- Tests must use a substituted fixture secret and must not snapshot or print credentials.
- This ADR must be revisited before any external production launch or security certification.

## References

- `apps/admin/src/auth/pkce.ts`
- `services/inspection/internal/platform/auth/oidc.go`
- `scripts/local.sh`
