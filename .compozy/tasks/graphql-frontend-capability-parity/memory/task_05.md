# Task 05 — Complete Evidence Trail Admin

## Current state

- Replaced the raw Admin query/JSON shell with a typed-document collection console, membership selector and grouped product navigation.
- The task-local memory file was absent at dispatch time and was initialized at the caller-provided path.
- The Admin shell now uses URL-backed `search`/`after` state, aborts superseded collection requests, and ignores stale responses by request generation.
- Primary unit, invitation, participant, asset, and publication-policy actions use feature-owned generated documents and expose pending/error states; audit export is generated from the current typed collection.
- History opens a detail panel and loads a typed audit query; governance uses fixed resource-specific columns and audit includes usage summary fields.

## Decisions and learnings

- Generated `TypedDocumentNode` artifacts are used by the Admin transport; product calls must provide generated operation result types and never inline GraphQL source.
- Identity bootstrap is the only membership-free request. Every tenant business operation requires and sends `X-Inspection-Membership-ID`.
- A context switch clears the per-tab `sessionStorage` context and loaded collection state before the next protected request.

## Touched surfaces

- `apps/admin/src/auth/session.ts`, `src/graphql/client.ts`, `src/features/admin/admin-shell.tsx`, and `src/features/admin/operations.graphql`
- `apps/admin/app/overview/page.tsx`, app styles, routes, code generation, and Admin unit tests
- `apps/admin/app/{access,assets,audit,catalogs,governance,organization,overview}/page.tsx`, `tests/e2e/admin.spec.ts`, and `tests/e2e/authenticated-admin.spec.ts`
- `apps/admin/src/graphql/generated.ts` and feature-owned GraphQL documents for Admin mutations/history/usage

## Follow-up risks

- Authenticated browser journeys require the local seeded stack and `INSPECTION_E2E_AUTH=true`; the normal browser suite reports them as skipped when absent.
- `npm run codegen:check` reports the expected generated-artifact diff while these uncommitted operation documents are part of the task; `npm run codegen`, unit tests, lint, and production build pass.
