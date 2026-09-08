# Workflow Memory

## Current state

- Task 01 is implementing the shared access, identity, and authorization foundation.
- Task 03 created the independently installable `@inspection/design-system` package at version `0.1.0`; downstream Admin, Dashboard, and Capture tasks must consume that exact registry version rather than a workspace or source link.
- The supplied workflow referenced `_spec.md` and these memory files, but the repository contains `_prd.md` and `_techspec.md`; the latter are the available canonical artifacts.
- Access foundation changes are implemented and verified locally; invitation provisioning and the larger access GraphQL slices remain follow-up work for the remaining task scope.

## Durable decisions

- Preserve existing membership and resource-scope table names while adding product entitlements and current-state hierarchy resolution.
- Keep OIDC product identity and product entitlement checks independent; Capture remains a separate external-session authority.
- Product entitlements are backfilled from active memberships, with tenant administrators receiving ADMIN and DASHBOARD and other active roles receiving DASHBOARD.

## Open risks

- The task's full GraphQL invitation and access surface is larger than the current repository implementation and must be completed without weakening existing resolver authorization.
- The private registry endpoint for `@inspection/design-system@0.1.0` is not configured in this environment; downstream standalone frontend lockfiles cannot yet be generated and clean-installed against the published artifact.
- Task 05 added `apps/dashboard` with separate `inspection-dashboard` PKCE/session, role-derived internal/customer compositions, customer-safe GraphQL documents, and visible-only notification polling. The task still needs a registry-backed clean install and API-backed end-to-end coverage before cutover.
