# ADR-012: Secret-Provisioned Fixed Super Administrator

## Status

Accepted

## Date

2026-09-08

## Context

The fixed Admin identity is accepted, but credentials cannot appear in repository or distributed artifacts.

## Decision

Provision Admin through environment-secret-backed Keycloak bootstrap only. Bootstrap is idempotent and adds its explicit TENANT_ADMIN membership on tenant creation. Tests use a substituted fixture secret.

## Alternatives Considered

### Alternative 1: Manual non-local setup

- **Description**: Automate local setup only.
- **Pros**: Small implementation.
- **Cons**: Environment drift.
- **Why rejected**: The account must be reliably provisioned everywhere.

### Alternative 2: Development credential in the realm

- **Description**: Commit a development password.
- **Pros**: Easy local startup.
- **Cons**: Credential leakage risk.
- **Why rejected**: No reusable password belongs in source control.

## Consequences

### Positive

- Reproducible bootstrap without credential artifacts.

### Negative

- Deployments require secret injection.

### Risks

- Missing secret fails closed with redacted readiness failure.

## Implementation Notes

- Application code never validates the password.

## References

- deploy/keycloak/inspection-realm.json
