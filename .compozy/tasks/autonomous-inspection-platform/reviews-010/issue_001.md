---
round: 10
round_created_at: 2026-09-07T17:58:51.187474Z
status: resolved
file: .env.example
line: 14
severity: high
author: reviewer
---

# Issue 001: Host scripts export the Compose expression as a literal origin

## Review Comment

`.env.example` now sets `INSPECTION_ALLOWED_ORIGIN=http://localhost:${INSPECTION_WEB_PORT:-3000}`. The custom parser in `scripts/local.sh` and `scripts/dev.sh` exports values literally and only rewrites the exact string `http://localhost:3000`. When host development loads the copied example file, the API therefore uses the literal expression as its allowed CORS origin, while the browser sends `http://localhost:3000` or the configured web port; the exact-origin CORS check rejects those requests. Compose interpolation passing does not cover the host-mode scripts. Parse the example file with shell-compatible expansion or derive the origin after loading the variables, and add a regression using `.env.example` itself.

## Triage

- Decision: `INVALID`
- Disproving evidence: `git show HEAD:.env.example` and the working tree both contain `INSPECTION_ALLOWED_ORIGIN=http://localhost:3000`; neither contains the Compose expression `${INSPECTION_WEB_PORT:-3000}` described by this issue. The host scripts' existing post-load logic also rewrites that concrete default when `INSPECTION_WEB_PORT` is customized.
- The reported literal-origin defect is therefore not present in this checkout and requires no production-code fix. `scripts/dev_test.sh` now uses `.env.example` as its fixture while overriding API and web ports, preserving regression coverage for the host-mode derivation.
