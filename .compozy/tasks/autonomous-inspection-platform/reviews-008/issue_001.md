---
round: 8
round_created_at: 2026-09-07T17:44:36.410617Z
status: resolved
file: .env.example
line: 51
severity: high
author: reviewer
---

# Issue 001: Example env overrides custom API port

## Review Comment

With `INSPECTION_API_PORT=8180 docker compose --env-file .env.example -f deploy/docker-compose.yml config --format json`, the API is published on `8180`, but `INSPECTION_TWILIO_CALLBACK_URL` and the web API URL remain on `http://localhost:8080`. The documented environment therefore breaks callbacks and can point the browser at the wrong API. Remove these fixed overrides or derive them from `INSPECTION_API_PORT`.

## Triage

- Decision: `VALID`
- Root cause: `.env.example` supplied fixed values for `INSPECTION_TWILIO_CALLBACK_URL` and `NEXT_PUBLIC_INSPECTION_API_URL`, which overrode the existing Compose fallbacks derived from `INSPECTION_API_PORT`.
- Resolution: removed both fixed assignments from the example environment and documented that Compose derives them from `INSPECTION_API_PORT` when unset. Explicit operator overrides remain supported.
- Verification: Compose configuration with `INSPECTION_API_PORT=8180` resolves the API publication, Twilio callback, web build argument, and web runtime endpoint to port `8180`; default configuration remains on port `8080`.
