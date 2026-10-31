#!/usr/bin/env sh
set -eu

workspace=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$workspace"

printf '%s\n' 'Executando testes unitários do Go...'
go test ./...

printf '%s\n' 'Executando testes unitários do Design System...'
npm test --prefix packages/inspection-design-system

for product in admin dashboard capture onboarding; do
  printf '%s\n' "Executando testes unitários de apps/$product..."
  npm test --prefix "apps/$product"
done

printf '%s\n' 'Todos os testes unitários passaram.'
