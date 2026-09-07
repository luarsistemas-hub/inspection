---
round: 1
round_created_at: 2026-09-07T16:06:17.306622Z
status: pending
file: services/inspection/cmd/inspection-scheduler/main.go
line: 83
severity: high
author: reviewer
---

# Issue 001: Scheduler bypasses tenant RLS context

## Review Comment

`runMaterializer` queries and writes through the runtime DB at line83 and `emitRetentionDue` at lines106–132 without calling `tenanttx.Runner.Within` or setting `app.tenant_id`. The migration forces RLS and the tenant policy requires that setting (`planner.go:64-67`), so the scheduler will discover no tenants and cannot insert retention outbox rows. Execute scheduler work through a tenant-scoped transaction or use an explicitly authorized worker role with equivalent isolation guarantees.

## Triage

- Decision: `UNREVIEWED`
- Notes:
