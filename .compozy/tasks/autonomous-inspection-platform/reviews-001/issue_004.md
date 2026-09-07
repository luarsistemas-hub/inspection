---
round: 1
round_created_at: 2026-09-07T16:06:17.306622Z
status: pending
file: services/inspection/internal/features/retention/core/core.go
line: 25
severity: high
author: reviewer
---

# Issue 004: Configured security retention is never enforced

## Review Comment

The GraphQL mutation accepts and persists `securityDays`, but the retention domain `Policy` contains only evidence and operational durations, and the scheduler/worker only reconstruct those two classes. A configured security-expiry policy is consequently not applied to OTP, invitation, session, or other security material. Implement the security-secret retention path, or reject the setting until it is enforced.

## Triage

- Decision: `UNREVIEWED`
- Notes:
