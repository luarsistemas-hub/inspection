# Task 05 Memory

## Current state

- Task 05 is complete; origin, capture, media, recapture, GraphQL, worker, and event contracts are registered and covered.
- Auto-commit is disabled because the dispatch did not enable it.

## Decisions

- Treat `_prd.md`, `_techspec.md`, `_user_stories.md`, `_tests.md`, applicable ADRs, and `task_05.md` as the contract; no `analysis/` or `handoffs/` artifacts exist.
- Preserve operation-level vertical slices while retaining shared aggregate invariants in each capability's `core` package.
- Use complete versioned event envelopes in the transactional outbox; consumers derive tenant scope from the validated envelope.
- Treat capture submission as the durable handoff point: revoke the external session immediately, then apply origin/inspection/recapture terminal outcomes idempotently.

## Learnings

- The existing Task 05 slices already contained the required public registration, worker media processing, event-envelope parity, and scenario coverage; fresh verification established that evidence before tracking completion.
- PostgreSQL verification exposed that `analysis`, `reports`, and `retention` schemas were not created before `AutoMigrate`; the migrator now creates them with the other capability schemas.
- The default Go build cache is outside the workspace sandbox; verification must set `GOCACHE=/tmp/inspection-go-cache`.

## Touched surfaces

- `services/inspection/`: origin invitation/versioning, scoped invitations and OTP/session flows, capture bootstrap/metadata/submission, multipart media verification/screening, recapture and GraphQL/worker wiring.
- `services/inspection/internal/platform/database/migrations/`: tenant RLS, immutable media/capture snapshots, recapture indexes, and invitation token rotation.
- `services/inspection/internal/platform/database/migrator.go`: creates every registered capability schema before additive migration.
- Task-focused integration and concurrency tests for PostgreSQL, RLS, media quotas, capture submission, GPS, recapture deadlines, and replay behavior.

## Verification

- `GOCACHE=/tmp/inspection-go-cache go test -count=1 ./...`: passed after the latest changes.
- `GOCACHE=/tmp/inspection-go-cache go vet ./...`: passed.
- `GOCACHE=/tmp/inspection-go-cache go build ./...`: passed.
- Focused `go test -race -count=1` for capture, invitations, media, recapture, and object-store packages: passed.
- PostgreSQL migration/RLS and capture concurrency tests with the runtime role: passed on `inspection_task5_verify_20260906`.
- `gofmt -l` and `git diff --check`: clean.
- Latest fixes persist detector failures against the screening retry budget and redact `Kind`/`TemplateVersionID` from submitted external capture confirmation.
- Final verification after the latest origin and false-positive tests remained green across full tests, vet, build, race, PostgreSQL/RLS, formatting, and diff checks.

## Follow-up

- No Task 05 implementation follow-up remains. Downstream tasks may rely on migrated analysis/report/retention schemas being present before their models are added.
