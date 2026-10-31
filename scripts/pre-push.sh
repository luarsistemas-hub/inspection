#!/usr/bin/env sh
set -eu

workspace=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$workspace"

./scripts/check-generated.sh --require-committed
./scripts/test-unit.sh
