---
round: 2
round_created_at: 2026-09-07T16:24:41.347579Z
status: resolved
file: deploy/docker-compose.yml
line: 210
severity: medium
author: reviewer
---

# Issue 001: Custom API port leaves frontend pointed at8080

## Review Comment

The Compose change makes the API port configurable (`${INSPECTION_API_PORT:-8080}:8080` at deploy/docker-compose.yml:146), and `scripts/local.sh` waits on that configured port, but the web service still receives and builds `NEXT_PUBLIC_INSPECTION_API_URL: http://localhost:8080/graphql` at deploy/docker-compose.yml:210-214 and223-227. Reproducible evidence: `INSPECTION_API_PORT=8180 INSPECTION_WEB_PORT=3100 docker compose ... config` reports API published on8180 while the web environment/build argument remains localhost:8080. Starting with a non-default API port therefore makes the browser call the wrong endpoint. Propagate the resolved API port into the web build/runtime configuration and displayed URLs.

## Triage

- Decision: `VALID`
- Root cause: the API host port is configurable in the Compose publication, but the web build argument and runtime environment independently hard-code port `8080`. The browser therefore receives a stale endpoint whenever `INSPECTION_API_PORT` differs from its default.
- Fix: derive both web values from the same `INSPECTION_API_PORT` interpolation while preserving an explicit `NEXT_PUBLIC_INSPECTION_API_URL` override. Compose configuration validation will confirm the resolved build/runtime URL and API published port agree for custom and default ports.
