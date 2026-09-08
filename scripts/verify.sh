#!/usr/bin/env sh
set -eu

workspace=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$workspace"

(cd services/inspection && go run github.com/99designs/gqlgen@v0.17.95 generate --config gqlgen.yml)
git diff --exit-code -- services/inspection/internal/platform/graphql
test -z "$(gofmt -l services/inspection libs)"
go test ./...
go vet ./...
go build ./...

for product in admin dashboard capture; do
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
docker compose -f deploy/docker-compose.yml config --quiet
./scripts/smoke.sh
./scripts/security-smoke.sh
./scripts/load-smoke.sh
./scripts/validate-compozy-tasks.sh
