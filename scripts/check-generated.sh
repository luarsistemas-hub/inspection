#!/usr/bin/env sh
set -eu

workspace=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$workspace"

generated_diff() {
  git diff "$@" -- \
    services/inspection/internal/platform/graphql \
    services/inspection/operation-manifest.json \
    apps/admin/src/graphql/generated.ts \
    apps/admin/src/graphql/schema.ts \
    apps/dashboard/src/graphql/generated.ts \
    apps/dashboard/src/graphql/schema.ts \
    apps/capture/src/graphql/generated.ts \
    apps/capture/src/graphql/schema.ts \
    apps/onboarding/src/graphql/generated.ts \
    apps/onboarding/src/graphql/schema.ts
}

if [ "${1:-}" = '--require-committed' ] && ! generated_diff --quiet HEAD; then
  printf '%s\n' 'Existem artefatos GraphQL gerados que ainda não estão no commit atual.' >&2
  printf '%s\n' 'Faça commit dos arquivos abaixo antes do push:' >&2
  generated_diff --name-only HEAD >&2
  exit 1
fi

snapshot_dir=$(mktemp -d "${TMPDIR:-/tmp}/inspection-generated.XXXXXX")
trap 'rm -rf "$snapshot_dir"' EXIT HUP INT TERM
generated_diff --binary >"$snapshot_dir/before.diff"

printf '%s\n' 'Gerando contratos GraphQL do backend...'
./scripts/generate-graphql-contracts.sh

for product in admin dashboard capture onboarding; do
  printf '%s\n' "Gerando contratos GraphQL de apps/$product..."
  (cd "apps/$product" && npm run codegen)
done

generated_diff --binary >"$snapshot_dir/after.diff"

if ! cmp -s "$snapshot_dir/before.diff" "$snapshot_dir/after.diff"; then
  printf '%s\n' 'O codegen atualizou artefatos GraphQL.' >&2
  printf '%s\n' 'Revise e faça commit dos arquivos abaixo antes do push:' >&2
  generated_diff --name-only >&2
  exit 1
fi

printf '%s\n' 'Artefatos gerados estão atualizados.'
