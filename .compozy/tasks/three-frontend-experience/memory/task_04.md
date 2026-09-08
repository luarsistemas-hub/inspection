# Task 04 Memory

## Objective

- Establish the standalone Admin frontend with its own product boundary, Admin OIDC session, GraphQL transport, route ownership, and focused verification.

## Decisions

- The task's test list is authoritative: IT-011–020 and IT-031–050 are assigned; US-003 edge cases IT-021–030 remain behavioral context but are not asserted as task-assigned IDs.
- Admin return paths accept only owned same-origin route families and every initial protected screen fails closed before requesting configuration data.

## Touched surfaces

- `apps/admin`: standalone Next.js metadata, runtime security headers, Dockerfile, auth/session modules, product GraphQL transport/documents, route shell, unit tests, browser guard tests, and generated schema types.

## Verification

- `npm run codegen`, `npm run lint`, `npm run test`, `npm run build`, and `npm run test:e2e` were run locally using the existing dependency cache plus a temporary ignored design-system package link.

## Open risks

- The private `@inspection/design-system@0.1.0` registry endpoint is not configured in this environment. `npm install --package-lock-only --ignore-scripts` cannot generate a registry-integrity lockfile, so clean isolated `npm ci` and image verification remain blocked until the installation registry is supplied.
- The supplied backend memories record incomplete invitation/effective-access/publication resolver wiring. The Admin's full mutation and real GraphQL acceptance journeys must be completed against those backend contracts before task tracking can be marked complete.
