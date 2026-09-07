---
round: 8
round_created_at: 2026-09-07T17:44:36.410617Z
status: resolved
file: scripts/dev.sh
line: 30
severity: medium
author: reviewer
---

# Issue 002: dev.sh leaves CORS origin on the default port

## Review Comment

`scripts/dev.sh` derives the API listener from `INSPECTION_API_PORT`, but does not derive `INSPECTION_ALLOWED_ORIGIN` from `INSPECTION_WEB_PORT`. With the example environment and `INSPECTION_WEB_PORT=3100`, the API still accepts only `http://localhost:3000`; `httpboundary.CORS` compares origins exactly and returns403 for the web process on port3100. Apply the same origin derivation used by `scripts/local.sh` and add a regression check.

## Triage

- Decision: `VALID`
- Root cause: `scripts/dev.sh` loaded the default `INSPECTION_ALLOWED_ORIGIN=http://localhost:3000` but did not derive it from a custom `INSPECTION_WEB_PORT`, while the API CORS boundary compares the request origin exactly. A web process on port 3100 therefore sent `Origin: http://localhost:3100`, which the API rejected.
- Fix: mirror the existing `scripts/local.sh` derivation in `dev.sh`, preserving an explicit non-default `INSPECTION_ALLOWED_ORIGIN` override. Extended `scripts/dev_test.sh` to verify that a default origin is rewritten to `http://localhost:3100` alongside the existing custom API URL check.
- Verification: `bash -n scripts/dev.sh scripts/dev_test.sh`, `bash scripts/dev_test.sh`, and `git diff --check` all passed. The required `systematic-debugging` and `no-workarounds` skills were unavailable because their native skill backend returned `backend_unhealthy`; root-cause triage and complete-fix discipline were applied manually.
