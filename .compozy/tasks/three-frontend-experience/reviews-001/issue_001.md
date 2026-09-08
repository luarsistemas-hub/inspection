---
round: 1
round_created_at: 2026-09-08T03:15:57.767566Z
status: resolved
file: apps/admin/package-lock.json
line: 11
severity: high
author: reviewer
---

# Issue 001: Frontend lockfiles cannot install pinned dependencies

## Review Comment

`apps/admin/package-lock.json`, `apps/dashboard/package-lock.json`, and `apps/capture/package-lock.json` contain only the root package and no dependency entries or integrity hashes. Fresh `npm ls --package-lock-only --all` reports every dependency, including `@inspection/design-system@0.1.0`, as `UNMET DEPENDENCY` and exits1. This violates the exact-version/lockfile contract and prevents clean installation.

## Triage

- Decision: `VALID`
- Root cause: `apps/admin/package-lock.json` contains only the root package entry, so none of the manifest dependencies has a locked version, resolved URL, or integrity hash. Local `npm ls --package-lock-only --all` reproduced the finding with `ELSPROBLEMS` for every dependency.
- The complete fix requires regenerating the lockfile through the repository's authorized registry. `apps/admin/.npmrc` resolves `@inspection` through `INSPECTION_NPM_REGISTRY`, but that environment variable is absent in this session; npm therefore fails with `ERR_INVALID_URL` before it can resolve `@inspection/design-system@0.1.0`. The package is not available in the local npm cache, and only its source directory exists in `packages/inspection-design-system`.
- Remediation is blocked until the authorized private registry URL is supplied to the install environment. The public registry must not be substituted because it would bypass the repository's scoped private-registry policy and risk dependency confusion. No production code or fabricated lock metadata was written.
