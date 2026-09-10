# Parity release gate

The release gate owns its state and tears it down on success, interruption, or failure. It requires Docker, Go 1.26.5, Node 22, browser dependencies, and test-only secrets supplied through the environment.

Run the complete local gate from the repository root:

```sh
./scripts/parity-gate.sh
```

The script starts and seeds the isolated Compose stack, regenerates and checks GraphQL artifacts, runs Go verification, runs the three product checks and authenticated Playwright suites, writes redacted evidence, validates the legacy inventory against `docs/legacy-inventory.json`, captures Compose logs, and removes the temporary volumes. Set `INSPECTION_PARITY_ARTIFACTS` to retain artifacts outside the repository.

The inventory is independently reviewable:

```sh
node scripts/lib/legacy-inventory.mjs generate --legacy-root apps/web --output /tmp/legacy-inventory.json
node scripts/lib/legacy-inventory.mjs validate --input /tmp/legacy-inventory.json --baseline docs/legacy-inventory.json --fail-on-unclassified --fail-on-drift
```

The gate blocks on any active legacy reference, unclassified entry, fingerprint drift, generated-artifact drift, failed authenticated journey, missing evidence field, or sensitive value in evidence. Historical Admin links use `/dashboard` and `/capture` redirects; unknown paths remain owned by the destination's not-found behavior. A failed or interrupted run leaves the captured failure log and never deletes the versioned inventory.
