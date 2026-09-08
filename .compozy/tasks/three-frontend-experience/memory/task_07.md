# Task 07 Memory

## Objective

- Implement Three-Product Runtime, CI and Cutover within the task scope.

## Decisions

- The repository's canonical specification artifacts are `_prd.md` and `_techspec.md`; no `_spec.md` is present.

## Touched surfaces

- Runtime configuration: Compose, Keycloak realm, root environment example.
- Local and CI orchestration: `scripts/local.sh`, `scripts/dev.sh`, smoke/security/verify scripts, CI workflow.
- Product runtime hardening: Next security headers, frontend Dockerfiles, product route manifests, Capture transport error shape.

## Learnings and corrections

- Task-local memory file was missing at kickoff and was initialized at the caller-provided path.
- The canonical task catalog assigns UT-074/UT-075 and IT-219/IT-230; `_spec.md` is absent and `_prd.md`/`_techspec.md` are the available canonical artifacts.
- `docker compose config --quiet`, Go test/vet/build with `GOCACHE=/tmp/inspection-go-cache`, Dashboard/Capture/design-system checks, and `bash scripts/dev_test.sh` pass.
- Admin clean install cannot complete because `INSPECTION_NPM_REGISTRY` is unset and the private `@inspection/design-system@0.1.0` registry is unavailable; offline npm reports `ENOTCACHED`.
- `validate-compozy-tasks.sh` cannot reach `proxy.golang.org` in the restricted network.

## Follow-up

- Re-run Admin clean install and Compozy task validation with the private npm registry and network access, then run full Compose/browser/security/load gates.
- Remove `apps/web` only after all replacement and cross-product gates pass; legacy app remains intentionally present for now.
