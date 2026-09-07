---
round: 9
round_created_at: 2026-09-07T17:51:14.34682Z
status: resolved
file: .env.example
line: 18
severity: medium
author: unknown
---

# Issue 001: Example env leaves CORS on port3000

## Review Comment

With `INSPECTION_WEB_PORT=3100 docker compose --env-file .env.example -f deploy/docker-compose.yml config --format json`, Compose publishes the web service on `3100` but resolves `INSPECTION_ALLOWED_ORIGIN` for both API and migration to `http://localhost:3000`, because `.env.example` line18 explicitly overrides the Compose fallback. Browser requests from the configured web port therefore receive403 from the exact-origin CORS check. Remove this fixed assignment or derive it from `INSPECTION_WEB_PORT`, preserving explicit overrides.

## Triage

- Decision: `VALID`
- Root cause: `.env.example` hard-coded `INSPECTION_ALLOWED_ORIGIN` to port 3000, so Compose treated that value as an explicit override and ignored its `INSPECTION_WEB_PORT`-based fallback.
- Fix: derive the example origin with Compose interpolation from `INSPECTION_WEB_PORT`, while retaining the existing precedence of an explicitly supplied `INSPECTION_ALLOWED_ORIGIN`.
- Verification target: `docker compose --env-file .env.example -f deploy/docker-compose.yml config --format json` with `INSPECTION_WEB_PORT=3100` must resolve the API and migration origin to `http://localhost:3100`; an explicit origin override must remain unchanged.
- Verification result: passed. Compose resolved both API and migration origins to `http://localhost:3100`, published web port `3100`, preserved the default origin `http://localhost:3000`, and preserved an explicit `https://review.example` override. `docker compose --env-file .env.example -f deploy/docker-compose.yml config --quiet` also exited 0.
