# Task 01 Memory

## Objective

Implement access, identity, and authorization foundation from task_01 and the available PRD/TechSpec contracts.

## Decisions

- Use a value-based authorization request while retaining a compatibility wrapper for existing slices during migration.
- Resolve hierarchy from canonical business-unit, asset, project, and inspection relationships at request time.

## Touched surfaces

- request context, auth, config, CORS, database models/migrations, GraphQL `me` contract, tenant bootstrap, role/scope access slices, and focused tests.

## Learnings

- Existing slices still call the legacy authorizer shape; the authorizer accepts both that shape and the explicit product-aware request while the composition root uses current hierarchy resolution.
- GraphQL Go boundary must be regenerated after schema changes with gqlgen.
- The Go build cache requires escalated permission in this managed workspace.

## Follow-up

- Customer invitation/Keycloak provisioning, effective-access/scope-preview GraphQL slices, and complete resolver-by-resolver product metadata are not part of this incremental foundation patch.

## Verification

- `go test ./...` passed (exit 0).
- `go vet ./...` passed (exit 0).
- `go build ./...` passed (exit 0).
