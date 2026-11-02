# Parity release gate

The release gate owns its state and tears it down on success, interruption, or failure. It requires Docker, Go 1.26.5, Node 22, browser dependencies, and test-only secrets supplied through the environment.

Run the complete local gate from the repository root:

```sh
./scripts/parity-gate.sh
```

The script validates `docs/design-system/migration-inventory.json` and `docs/design-system/accessibility-evidence.json`, then requires real-E2E prerequisites. It starts and seeds the isolated Compose stack, regenerates and checks GraphQL artifacts, runs Go verification, runs all four product checks and Playwright suites, writes redacted journey evidence, validates the accessibility evidence, captures Compose logs, and removes the temporary volumes. The inventory is route and state coverage; accessibility evidence records executed assessments and unresolved manual checks. Set `INSPECTION_PARITY_ARTIFACTS` to retain artifacts outside the repository.

Review the route/state inventory and accessibility evidence directly at `docs/design-system/migration-inventory.json` and `docs/design-system/accessibility-evidence.json` (with the human-readable companion `accessibility-evidence.md`). Validate them with `node scripts/lib/design-system-migration.mjs validate --repository . --inventory docs/design-system/migration-inventory.json --evidence docs/design-system/accessibility-evidence.json`.

The gate blocks on migration inventory drift, failed authenticated journeys, missing journey-evidence fields, or sensitive values in generated evidence. Accessibility records that remain `not-tested` or `blocked` stay unresolved and prevent a conformance claim. Historical Admin links use `/dashboard` and `/capture` redirects; unknown paths remain owned by the destination's not-found behavior. A failed or interrupted run leaves the captured failure log and never deletes the versioned inventory.
