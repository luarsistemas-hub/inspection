# Workflow Memory

## Current state

- Task 01 is implementing the shared access, identity, and authorization foundation.
- The supplied workflow referenced `_spec.md` and these memory files, but the repository contains `_prd.md` and `_techspec.md`; the latter are the available canonical artifacts.

## Durable decisions

- Preserve existing membership and resource-scope table names while adding product entitlements and current-state hierarchy resolution.
- Keep OIDC product identity and product entitlement checks independent; Capture remains a separate external-session authority.

## Open risks

- The task's full GraphQL invitation and access surface is larger than the current repository implementation and must be completed without weakening existing resolver authorization.
