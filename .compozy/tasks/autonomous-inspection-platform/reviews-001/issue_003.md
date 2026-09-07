---
round: 1
round_created_at: 2026-09-07T16:06:17.306622Z
status: pending
file: services/inspection/internal/platform/graphql/resolvers/schema.resolvers.go
line: 1591
severity: high
author: reviewer
---

# Issue 003: Report and triage queries omit scope authorization

## Review Comment

`Report`, `ReportDownload`, `DashboardSummary`, `TriageInspections`, and related Task06 queries only verify that request metadata exists and then scope by tenant. They never call the configured `Authorizer` or check the caller's business-unit/asset scope. A tenant employee or viewer can therefore read reports, triage rows, notification history, and timeline data outside their assigned scope, violating the requirement that these surfaces remain scope-authorized.

## Triage

- Decision: `UNREVIEWED`
- Notes:
