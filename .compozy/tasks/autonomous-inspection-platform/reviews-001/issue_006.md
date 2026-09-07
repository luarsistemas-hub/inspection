---
round: 1
round_created_at: 2026-09-07T16:06:17.306622Z
status: pending
file: apps/web/app/(dashboard)/page.tsx
line: 572
severity: medium
author: reviewer
---

# Issue 006: HTML-only report fallback cannot be downloaded by the UI

## Review Comment

The PDF worker correctly creates an `HTML` artifact when Gotenberg permanently fails, but the dashboard always requests `kind: "PDF"` and reports that the PDF is unavailable. Thus the required HTML-only fallback is not usable through the shipped internal workflow; the UI must request the available artifact kind or the resolver must fall back from PDF to HTML.

## Triage

- Decision: `UNREVIEWED`
- Notes:
