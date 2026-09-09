# Task 03 Memory

## Current State

- Completed the Task 3 backend work, including the media multipart-recovery application contract for Capture.

## Decisions

- Recovery trusts the object-store part catalog and persists its confirmed ETags/sizes; it never treats client-local state as confirmation.
- A recovery request accepts only active, unexpired uploads and returns the exact missing 5 MiB part positions. Invalid provider part state fails closed.
- The provider capability is optional at the object-store interface boundary, but the recovery VSA setup requires it. MinIO implements it with paged `ListObjectParts` reads.
- Reconciliation deletes stale local part rows before persisting the provider catalog, and presigning refuses already provider-confirmed parts.

## Touched Surfaces

- `services/inspection/internal/features/media/core`
- `services/inspection/internal/features/media/reconcile_upload`
- `services/inspection/internal/platform/objectstore`
- `services/inspection/cmd/inspection-api/main.go`
- `services/inspection/internal/features/media/core/core_test.go`

## Verification

- `go test ./services/inspection/internal/features/media/core ./services/inspection/internal/features/media/reconcile_upload ./services/inspection/internal/platform/objectstore` passed.
- `go test ./...`, `go vet ./...`, and `go build ./...` passed.

## Follow-ups

- Task 4 must expose the mediator query through the canonical GraphQL contract; Task 7 must use it to drive Capture recovery UI.
