# Task 04 Memory

## Objective

- Establish canonical GraphQL generation and frontend typed-operation/design-system foundations.

## Decisions and Learnings

- Task 4 owns the canonical GraphQL boundary, generated client contracts, capability validation, and neutral primitives; it must not absorb the unfinished business slices from Tasks 2 and 3.
- The schema is the canonical server contract. Product GraphQL documents remain owned by their respective applications and keep their existing distinct transports.
- The operation-manifest builder now validates each product document against the canonical schema. It caught and led to removal of the unused `$search` variable from `AdminOrganization`.
- Added explicitly named US-001 edge-case coverage (`UT-133.01` through `UT-133.10` and `IT-034.01` through `IT-034.10`) in the GraphQL contract package, including invalid selections, stale schema hashes, partial product inventories, mutation identity, and deterministic inventory checks.

## Touched Surfaces

- `services/inspection/internal/platform/graphqlcontract/{validator.go,validator_test.go,manifest_test.go}`
- `services/inspection/operation-manifest.json`
- `apps/admin/src/features/admin/operations.graphql` and regenerated Admin GraphQL types

## Verification

- `GOCACHE=/tmp/inspection-go-cache go test ./internal/platform/graphqlcontract/... -count=1` passed after manifest regeneration.
- Admin, Dashboard, Capture code generation and unit tests passed; design-system unit tests, lint, and build passed.
- Root Go test output passed through all packages before the combined command timed out during later chained gates; individual `go vet ./...` and `go build ./...` had already passed.

## Open Risks

- Tasks 2 and 3 are still tracked as pending. The resolver still contains persistence-owning Task 6 helpers, so Task 4 cannot truthfully be marked complete against its thin-resolver acceptance criterion until those prerequisite slices are completed and the resolver boundary is reduced.
