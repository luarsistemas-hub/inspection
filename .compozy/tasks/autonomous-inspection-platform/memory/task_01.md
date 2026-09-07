# Task 01 Memory

## Objective

- Implement Foundation, Tenant Isolation, Access & Audit from `task_01.md`.

## Initial state

- Task status: pending.
- No source files for `services/inspection` exist yet.
- Required workflow memory files were missing and have now been initialized.

## Completed state

- Added API, worker, scheduler, and migrator roots with graceful lifecycle and compatibility readiness.
- Added schema/model registry, checksum planner, advisory migration lock, non-owner runtime role grants, and forced RLS.
- Added tenant transaction, typed mediator/registry contracts, local authorization/OIDC adapter boundary, stable GraphQL errors/cursors, gqlgen output, safe telemetry, and health/readiness/private metrics.
- Added tenant bootstrap/business-unit, role-scope assignment, and audit record/list operation slices.
- Changed local MinIO configuration from public wildcard access to pinned, private configuration.
- Added focused unit/transport tests and a real PostgreSQL migration/RLS integration test.

## Decisions

- Use `_prd.md`, `_techspec.md`, `_user_stories.md`, `_tests.md`, the task file, and relevant ADRs as the available contract corpus.
- Do not modify `services/contract` or unrelated existing worktree changes.
- Preserve the pre-existing untracked Contract Service `.env` and executable without reading, modifying, staging, or deleting them.
- Keep the temporary PostgreSQL integration environment ephemeral; all three validation containers were removed without named volumes.

## Verification

- `go test ./services/inspection/...` passed after gqlgen generation.
- Fresh PostgreSQL 18 integration test passed migration retry, checksum/version, role ownership/BYPASSRLS, missing-context isolation, tenant read isolation, and cross-tenant write denial.
- Docker Compose config validation passed after private MinIO changes.

## Follow-up

- Task 2 should build the durable dispatcher/consumer runtime on the `messaging.outbox` intent table; task 1 intentionally exposes the durable contract only.
