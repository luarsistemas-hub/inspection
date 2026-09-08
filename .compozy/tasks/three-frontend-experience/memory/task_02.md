# Task 02 Memory

## Current state

- Task memory was missing at kickoff and was created from the supplied workflow path.
- Task 01 changes are present in the worktree and must be preserved.

## Decisions

- Implement publication visibility as mutable ledger state separate from immutable report snapshots.
- Keep customer projections in dedicated types and packages; never reuse internal report serialization.

## Learnings

- The repository currently has report snapshots, artifacts, internal notification recipients, and the transactional outbox, but no publication ledger or recipient-owned in-app notification model.
- Added publication policy/ledger and recipient channel/notification models with migration 21 and tenant RLS.
- Added pure publication, customer evidence, media URL, and recipient notification components plus focused unit coverage.
- Regenerated GraphQL artifacts after adding the customer/publication/notification schema types.

## Open risks

- The task assigns a broad GraphQL and integration contract; generated GraphQL files may require regeneration after schema changes.
- Full resolver wiring for customer projections, publication mutations, notification fan-out, and outbox integration remains follow-up work; new generated resolver stubs are safe (no panic) but return empty/not-configured results.
