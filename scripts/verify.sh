#!/usr/bin/env sh
set -eu

workspace=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
env_file=${INSPECTION_ENV_FILE:-$workspace/.env.inspection}
cd "$workspace"

node scripts/lib/design-system-migration.mjs validate \
  --repository "$workspace" \
  --inventory docs/design-system/migration-inventory.json \
  --evidence docs/design-system/accessibility-evidence.json

(cd services/inspection && go run github.com/99designs/gqlgen@v0.17.95 generate --config gqlgen.yml)
git diff --exit-code -- services/inspection/internal/platform/graphql
test -z "$(gofmt -l services/inspection libs)"
go test ./...
go vet ./...
go build ./...

cd "$workspace/packages/inspection-design-system"
npm ci
npm run lint
npm test
npm run build
npm run pack:check

for product in admin dashboard capture onboarding; do
  node "$workspace/scripts/prepare-design-system.mjs"
  cd "$workspace/apps/$product"
  npm ci
  npm audit --audit-level=high
  npm run codegen:check
  npm run lint
  npm run test
  npm run build
  npx playwright install chromium webkit
  npm run test:e2e
done

cd "$workspace"
docker compose --env-file "$env_file" -f deploy/docker-compose.yml config --quiet
./scripts/smoke.sh
./scripts/security-smoke.sh
./scripts/load-smoke.sh
./scripts/validate-compozy-tasks.sh
node --test scripts/lib/parity-tools.test.mjs scripts/lib/design-system-migration.test.mjs
