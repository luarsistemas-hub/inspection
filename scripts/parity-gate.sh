#!/usr/bin/env bash
set -Eeuo pipefail

workspace="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
artifacts="${INSPECTION_PARITY_ARTIFACTS:-$workspace/artifacts/parity-gate}"
env_file="${INSPECTION_ENV_FILE:-$workspace/.env.inspection}"
compose_file="$workspace/deploy/docker-compose.yml"
trap 'code=$?; set +e; mkdir -p "$artifacts"; [[ -f "$env_file" ]] && docker compose --env-file "$env_file" -f "$compose_file" logs --no-color > "$artifacts/compose.log" 2>&1; [[ -f "$env_file" ]] && docker compose --env-file "$env_file" -f "$compose_file" down --volumes --remove-orphans; exit "$code"' EXIT

cd "$workspace"
[[ -f "$env_file" ]] || cp .env.example "$env_file"
export INSPECTION_SCHEMA_HASH="$(node -e 'const fs=require("node:fs"); const c=require("node:crypto"); process.stdout.write(c.createHash("sha256").update(fs.readFileSync("services/inspection/schema.graphqls")).digest("hex"))')"
./scripts/local.sh up
./scripts/local.sh seed
(cd services/inspection && go run github.com/99designs/gqlgen@v0.17.95 generate)
git diff --exit-code -- services/inspection/internal/platform/graphql services/inspection/operation-manifest.json
go test ./...
go vet ./...
go build ./...
for product in admin dashboard capture; do
  (cd "apps/$product" && npm ci && npm run codegen:check && npm run lint && npm run test && npm run build && npm run test:e2e)
done
node scripts/lib/parity-evidence.mjs generate --journey E2E-033 --test-id E2E-001,E2E-027,E2E-033,E2E-034.01,E2E-060,E2E-066 --input "$workspace/artifacts" --output "$artifacts/evidence"
node scripts/lib/parity-evidence.mjs validate --input "$artifacts/evidence"
node scripts/lib/legacy-inventory.mjs generate --legacy-root apps/web --output "$artifacts/legacy-inventory.json"
node scripts/lib/legacy-inventory.mjs validate --input "$artifacts/legacy-inventory.json" --baseline docs/legacy-inventory.json --fail-on-unclassified --fail-on-drift
