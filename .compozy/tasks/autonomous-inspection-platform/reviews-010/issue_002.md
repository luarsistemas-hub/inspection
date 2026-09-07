---
round: 10
round_created_at: 2026-09-07T17:58:51.187474Z
status: resolved
file: services/inspection/internal/features/retention/purge_data/setup.go
line: 123
severity: critical
author: reviewer
---

# Issue 002: Purge can bypass a concurrently-created legal hold

## Review Comment

The worker checks for an active legal hold before calling `PurgeWithStore` (`services/inspection/cmd/inspection-worker/main.go:301`), but `PurgeWithStore` does not lock or re-check the hold and deletes all `LegalHold` rows at lines123-132 before deleting the inspection. A hold committed after the initial check can therefore be deleted by the purge transaction and the inspection can then be purged. This violates the contract that a legal hold may extend retention and creates a data-loss/compliance race. Serialize purge against hold creation (for example with a per-inspection lock and a locked, authoritative re-check) and preserve the hold record instead of deleting it as purge data.

## Triage

- Decision: `VALID`
- Root cause: `PurgeWithStore` previously performed no authoritative legal-hold check inside its deletion transaction, deleted `retention.legal_holds` rows, and the GraphQL hold mutations did not serialize with purge. A hold transaction could therefore commit between the worker's pre-check and purge and then be removed by the purge.
- Fix: purge now locks the tenant inspection row with `FOR UPDATE`, checks for an active hold inside the same transaction, and aborts with `gorm.ErrInvalidData` before deleting any data. Applying or releasing a hold acquires the same inspection-row lock, closing the concurrent-creation race. Legal-hold rows are excluded from purge deletion so their audit history is preserved.
- Verification: focused regression coverage in `services/inspection/internal/features/retention/purge_data/setup_test.go` proves active holds block the authoritative check and no active hold permits it.
