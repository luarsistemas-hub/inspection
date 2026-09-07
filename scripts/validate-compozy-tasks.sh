#!/usr/bin/env sh
set -eu

# Pin the CLI source and isolate its home directory. This avoids accepting a
# newer global configuration with the 0.2.14 validator by accident.
workspace=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$workspace/.tools/compozy-home"
COMPOZY_HOME="$workspace/.tools/compozy-home" \
  go run github.com/compozy/compozy/cmd/compozy@v0.2.14 tasks validate \
  --tasks-dir "$workspace/.compozy/tasks/autonomous-inspection-platform" --format json
