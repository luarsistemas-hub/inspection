# ADR-003: Tenant Isolation, Super Admin Memberships, and Delegated Administration

## Status

Accepted

## Date

2026-09-08

## Context

The backend enforces tenant isolation and the current Admin admits `TENANT_ADMIN`. The product also needs an internal super administrator who can enter every tenant without introducing cross-tenant business queries. Administrative responsibility must be delegable without granting every administrator retention, publication, and access-management authority.

## Decision

Keep all product operations tenant-isolated. A super administrator is one internal identity with an explicit `TENANT_ADMIN` membership automatically provisioned in every tenant. The identity selects a tenant membership before accessing data, and every action is audited in that tenant context. No global query may aggregate customer business data across tenants.

Retain `TENANT_ADMIN` as full tenant authority and add six delegated administrative roles:

- `ORGANIZATION_ADMIN`: tenant settings, business units, and origins.
- `ACCESS_ADMIN`: users, invitations, roles, scopes, permissions, and effective access.
- `PARTICIPATION_ADMIN`: participants and segments.
- `INSPECTION_CONFIG_ADMIN`: templates, analysis profiles, and assets.
- `GOVERNANCE_ADMIN`: publication, notification, retention, deletion, and legal hold.
- `AUDITOR`: read-only administrative data, audit, and usage.

Every delegated role remains constrained by explicitly assigned resource scopes. The Admin must explain effective access, including direct, inherited, role-derived, and scope-derived grants.

## Alternatives Considered

### Alternative 1: Global platform administration queries

- **Description**: Allow a super admin to list and operate across all tenants from one data context.
- **Pros**: Convenient global oversight.
- **Cons**: Weakens isolation and raises the impact of authorization defects.
- **Why rejected**: The selected model requires entering each tenant explicitly.

### Alternative 2: Manual super admin membership

- **Description**: Add the super administrator independently to each tenant.
- **Pros**: No automatic provisioning.
- **Cons**: Easy to omit a tenant and creates operational recovery gaps.
- **Why rejected**: Automatic membership was explicitly selected.

### Alternative 3: Only `TENANT_ADMIN`

- **Description**: Give every administrator all administrative powers.
- **Pros**: Simple role model.
- **Cons**: Violates least privilege and the accepted Open Design access model.
- **Why rejected**: Delegated domain roles were explicitly selected.

## Consequences

### Positive

- Tenant data remains isolated even for super administrators.
- Administrative duties can be delegated according to least privilege.
- Effective access becomes understandable and auditable.
- Super admin availability is automatic for new tenants.

### Negative

- One internal identity may accumulate many memberships.
- Role and scope changes require migration and clear UI explanation.
- Automatic provisioning requires reliable lifecycle handling when tenants are created.

### Risks

- A super admin may act in the wrong tenant; mitigate with persistent tenant/scope context and high-impact confirmations.
- Role combinations may become confusing; mitigate with an effective-access explanation and redundant/conflicting grant warnings.

## Implementation Notes

- The tenant selector lists only memberships belonging to the authenticated identity.
- Switching tenants must establish a new tenant context before any data is loaded.
- Authorization is enforced on the server; frontend capability gates are explanatory only.
- Super admin actions must be distinguishable in audit records.

## References

- `services/inspection/internal/platform/auth/authorize.go`
- `apps/admin/src/auth/session.ts`
- Open Design: `referencia-mercado-admin-inspection.md`

