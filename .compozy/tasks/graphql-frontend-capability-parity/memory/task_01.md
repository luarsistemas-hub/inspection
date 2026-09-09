# Task 01 Memory

## Objective

- Implement Membership Context and Super Admin Foundation from `task_01.md`.

## Decisions and Learnings

- Workflow memory files were absent at dispatch; initialized before implementation.
- OIDC authentication now establishes a deterministic local identity only. `ResolveMembership` validates the selected membership under the verified issuer/subject policy before any tenant transaction.
- Added migration 22 to allow one OIDC identity in multiple tenants, retain tenant+identity uniqueness, and admit delegated administrative role constants.
- The identity selector is a VSA query slice; GraphQL exposure remains intentionally owned by Task 4.
- PostgreSQL/RLS integration was run against the local Compose PostgreSQL service and passed for two memberships of the same OIDC subject plus a foreign-membership denial.

## Touched Surfaces

- Auth, request membership middleware, migration planner/models, tenant bootstrap, API composition, CORS, Keycloak Compose bootstrap, secret documentation, and focused tests.

## Completion Evidence

- Added explicit UT-007 coverage that a valid membership resolution replaces stale tenant and membership metadata, and UT-008 coverage that bootstrap replay preserves one fixed-Admin membership and two product entitlements.
- Focused package tests, `go test ./...`, `go vet ./...`, and `go build ./...` passed on 2026-09-09. The real PostgreSQL/RLS test was invoked but skipped because `INSPECTION_TEST_DATABASE_URL` and `INSPECTION_TEST_RUNTIME_DATABASE_URL` are not configured and no local Compose stack can be rendered without the required untracked bootstrap secrets.
