---
round: 3
round_created_at: 2026-09-07T16:32:10.500689Z
status: resolved
file: apps/web/next.config.ts
line: 19
severity: high
author: reviewer
---

# Issue 002: Production CSP blocks a configured API endpoint

## Review Comment

`apps/web/next.config.ts:19` hard-codes `http://localhost:8080` in `connect-src`. With a custom API port, the frontend endpoint can resolve to `http://localhost:8180/graphql`, but the CSP omits that origin, so browser GraphQL requests are blocked. Build the API origin into CSP or remove the duplicate static policy in favor of the middleware policy, which derives it from `NEXT_PUBLIC_INSPECTION_API_URL`.

## Triage

- Decision: `VALID`
- Root cause: `next.config.ts` hard-coded `http://localhost:8080` in its static `connect-src` policy, so a configured API such as `http://localhost:8180/graphql` was absent from that policy. The production middleware derives its own policy, but it does not remove the configuration-level defect or cover every response path using the static headers.
- Resolution: derive the API origin from `NEXT_PUBLIC_INSPECTION_API_URL`, with the same default origin as the GraphQL client, and add a regression test that asserts a custom API port is allowed.
