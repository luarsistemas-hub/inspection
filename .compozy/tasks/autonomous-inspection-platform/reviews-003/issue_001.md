---
round: 3
round_created_at: 2026-09-07T16:32:10.500689Z
status: resolved
file: deploy/docker-compose.yml
line: 153
severity: high
author: reviewer
---

# Issue 001: Custom web port is rejected by the API CORS policy

## Review Comment

The Compose file publishes the web service from `${INSPECTION_WEB_PORT:-3000}:3000` at `deploy/docker-compose.yml:222`, but the API still receives `INSPECTION_ALLOWED_ORIGIN: http://localhost:3000` at line153. Fresh evidence with `INSPECTION_WEB_PORT=3100 docker compose ... config --format json` resolved the web port to `3100` while resolving the API origin to `http://localhost:3000`. A browser loaded from `http://localhost:3100` therefore fails the API's exact-origin CORS check. Derive the allowed origin from the resolved web port, preserving an explicit override.

## Triage

- Decision: `VALID`
- Root cause: the published `web` port was interpolated from `INSPECTION_WEB_PORT`, while both the migration and shared runtime environments hard-coded `INSPECTION_ALLOWED_ORIGIN` to `http://localhost:3000`. The API CORS middleware compares the request `Origin` as an exact string, so a browser served from a custom web port could not pass CORS.
- Fix: derive the default allowed origin from `INSPECTION_WEB_PORT` and retain `INSPECTION_ALLOWED_ORIGIN` as the higher-priority explicit override in both environment definitions. The shared runtime anchor propagates the corrected value to the API, worker, and scheduler.
- Verification: `INSPECTION_WEB_PORT=3100 docker compose -f deploy/docker-compose.yml config --format json` resolves the web binding to `3100` and the API/migration allowed origin to `http://localhost:3100`. With `INSPECTION_ALLOWED_ORIGIN=https://dev.example.test`, the same command preserves the explicit override for both services. `git diff --check` passes. The repository's canonical Compose validation is `docker compose -f deploy/docker-compose.yml config --quiet` (also used by `scripts/verify.sh` and CI); the focused matrix above verifies the changed invariant.
